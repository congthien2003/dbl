package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"dbl/internal/database"
)

// Metadata is read from information_schema. The server lists there only
// what the user has some privilege on, so a database, table or index the
// user may not see is missing rather than reported as denied.

var _ database.SQLAdapter = (*Adapter)(nil)

// eachRow runs a query and calls scan for every row; the rows are closed
// before it returns.
func (a *Adapter) eachRow(ctx context.Context, query string, args []any, scan func(*sql.Rows) error) error {
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return rows.Close()
}

// ListSchemas lists the databases of the server the user may see, without
// the server's own.
func (a *Adapter) ListSchemas(ctx context.Context) ([]database.Schema, error) {
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(systemSchemas)), ", ")
	args := make([]any, len(systemSchemas))
	for i, s := range systemSchemas {
		args[i] = s
	}
	var out []database.Schema
	err := a.eachRow(ctx,
		"SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME NOT IN ("+marks+") ORDER BY SCHEMA_NAME",
		args, func(r *sql.Rows) error {
			var s database.Schema
			if err := r.Scan(&s.Name); err != nil {
				return err
			}
			out = append(out, s)
			return nil
		})
	if err != nil {
		return nil, metadataError(ctx, err, "read the databases")
	}
	return out, nil
}

// objectKind maps TABLE_TYPE to a kind and the subtype shown.
func objectKind(tableType string) (database.ObjectKind, string) {
	switch strings.ToUpper(tableType) {
	case "VIEW", "SYSTEM VIEW":
		return database.ObjectView, ""
	case "BASE TABLE":
		return database.ObjectTable, ""
	}
	// MariaDB's SYSTEM VERSIONED tables and SEQUENCEs.
	return database.ObjectTable, strings.ToLower(tableType)
}

// ListTables lists the tables and views of a database.
func (a *Adapter) ListTables(ctx context.Context, schema string) ([]database.Table, error) {
	var out []database.Table
	err := a.eachRow(ctx,
		"SELECT TABLE_NAME, TABLE_TYPE FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME",
		[]any{schema}, func(r *sql.Rows) error {
			var name, typ string
			if err := r.Scan(&name, &typ); err != nil {
				return err
			}
			kind, sub := objectKind(typ)
			out = append(out, database.Table{Ref: database.TableRef{Schema: schema, Name: name}, Kind: kind, Subtype: sub})
			return nil
		})
	if err != nil {
		return nil, metadataError(ctx, err, "read the tables")
	}
	return out, nil
}

// DescribeTable reads the metadata of a table or view. Without columns
// there is nothing to show, so that read fails the call; every other
// section reports its own failure.
func (a *Adapter) DescribeTable(ctx context.Context, ref database.TableRef) (*database.TableMetadata, error) {
	ref.Catalog = ""
	m := &database.TableMetadata{Ref: ref, EstimatedRows: -1}

	var typ, engine string
	var rowsEstimate sql.NullInt64
	err := a.db.QueryRowContext(ctx,
		"SELECT TABLE_TYPE, IFNULL(ENGINE, ''), TABLE_ROWS FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?",
		ref.Schema, ref.Name).Scan(&typ, &engine, &rowsEstimate)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &database.Error{
			Kind:    database.KindMetadataUnavailable,
			Message: "The table " + ref.Name + " no longer exists, or you may not see it.",
			Hint:    "Refresh the node in the explorer.",
			Err:     err,
		}
	}
	if err != nil {
		return nil, metadataError(ctx, err, "read the table")
	}
	m.Kind, m.Subtype = objectKind(typ)
	if m.Kind == database.ObjectTable && rowsEstimate.Valid {
		m.EstimatedRows = rowsEstimate.Int64
	}

	if err := a.readColumns(ctx, ref, m); err != nil {
		return nil, metadataError(ctx, err, "read the columns")
	}
	if len(m.Columns) == 0 {
		return nil, &database.Error{Kind: database.KindMetadataUnavailable, Message: "The table has no columns you may read."}
	}

	na := func(reason string) (database.SectionState, string) { return database.SectionNotApplicable, reason }
	if m.Kind == database.ObjectView {
		m.Constraints.State, m.Constraints.Reason = na("Views have no constraints.")
		m.ForeignKeys.State, m.ForeignKeys.Reason = na("Views have no foreign keys.")
		m.Referenced.State, m.Referenced.Reason = na("Views cannot be referenced by foreign keys.")
		m.Indexes.State, m.Indexes.Reason = na("Views have no indexes.")
		m.Triggers.State, m.Triggers.Reason = na("Views have no triggers.")
	} else {
		var checks []checkConstraint
		m.Constraints = readSection(ctx, func() ([]database.ConstraintMetadata, error) {
			items, ch, err := a.readConstraints(ctx, ref)
			checks = ch
			return items, err
		}, "constraints")
		markJSONColumns(m, checks)
		m.ForeignKeys = readSection(ctx, func() ([]database.ForeignKeyMetadata, error) {
			return a.readForeignKeys(ctx, "k.TABLE_SCHEMA = ? AND k.TABLE_NAME = ?", ref)
		}, "foreign keys")
		m.Referenced = readSection(ctx, func() ([]database.ForeignKeyMetadata, error) {
			return a.readForeignKeys(ctx, "k.REFERENCED_TABLE_SCHEMA = ? AND k.REFERENCED_TABLE_NAME = ?", ref)
		}, "incoming foreign keys")
		m.Indexes = readSection(ctx, func() ([]database.IndexMetadata, error) {
			return a.readIndexes(ctx, ref)
		}, "indexes")
		m.Triggers = readSection(ctx, func() ([]database.TriggerMetadata, error) {
			return a.readTriggers(ctx, ref)
		}, "triggers")
		m.ReadOnly = a.engineReadOnly(ctx, engine)
	}
	if err := ctx.Err(); err != nil {
		return nil, classify(ctx, err, "read the table")
	}

	m.MarkKeys()
	m.ResolveIdentity()
	return m, nil
}

// engineReadOnly is why a table of the engine cannot be saved safely, or ""
// when its engine supports transactions. information_schema.ENGINES says
// which do; the answer is kept for the session.
func (a *Adapter) engineReadOnly(ctx context.Context, engine string) string {
	if engine == "" {
		return "The storage engine of the table is unknown, so changes cannot be saved safely."
	}
	key := strings.ToUpper(engine)
	a.engMu.Lock()
	ok, known := a.engines[key]
	a.engMu.Unlock()
	if !known {
		var tx sql.NullString
		err := a.db.QueryRowContext(ctx,
			"SELECT TRANSACTIONS FROM information_schema.ENGINES WHERE UPPER(ENGINE) = ?", key).Scan(&tx)
		if err != nil {
			return "Whether the " + engine + " storage engine supports transactions could not be checked, so changes cannot be saved safely."
		}
		ok = strings.EqualFold(tx.String, "YES")
		a.engMu.Lock()
		if a.engines == nil {
			a.engines = map[string]bool{}
		}
		a.engines[key] = ok
		a.engMu.Unlock()
	}
	if ok {
		return ""
	}
	return "The " + engine + " storage engine does not support transactions, so changes cannot be saved safely."
}

// metadataError classifies a failure of a metadata read that is required.
func metadataError(ctx context.Context, err error, what string) *database.Error {
	e := classify(ctx, err, what)
	if e.Kind == database.KindConnection || e.Kind == database.KindSyntax {
		c := *e
		c.Kind = database.KindMetadataUnavailable
		return &c
	}
	return e
}

// readSection runs one metadata read and turns its failure into the state
// of the section.
func readSection[T any](ctx context.Context, read func() ([]T, error), what string) database.Section[T] {
	items, err := read()
	if err == nil {
		return database.Section[T]{Items: items}
	}
	de := classify(ctx, err, "read the "+what)
	if de.Kind == database.KindPermission {
		return database.Section[T]{State: database.SectionDenied, Reason: "You may not read the " + what + ": " + de.Message}
	}
	return database.Section[T]{State: database.SectionFailed, Reason: "The " + what + " could not be read: " + de.Message}
}

// readColumns reads the columns in table order.
func (a *Adapter) readColumns(ctx context.Context, ref database.TableRef, m *database.TableMetadata) error {
	return a.eachRow(ctx, `SELECT COLUMN_NAME, ORDINAL_POSITION, DATA_TYPE, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, EXTRA
		FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION`,
		[]any{ref.Schema, ref.Name}, func(r *sql.Rows) error {
			var c database.ColumnMetadata
			var dataType, nullable, extra string
			var def sql.NullString
			if err := r.Scan(&c.Name, &c.Ordinal, &dataType, &c.NativeType, &nullable, &def, &extra); err != nil {
				return err
			}
			ct := classOf(dataType, c.NativeType)
			c.Class, c.IntBits, c.Unsigned = ct.Class, ct.IntBits, ct.Unsigned
			c.Nullable = strings.EqualFold(nullable, "YES")
			if def.Valid {
				c.Default = &def.String
			}
			c.Generated = generatedExtra(extra)
			if strings.Contains(strings.ToLower(extra), "auto_increment") {
				c.Identity = database.IdentityByDefault
			}
			m.Columns = append(m.Columns, c)
			return nil
		})
}

// checkConstraint is a CHECK constraint as the server prints it.
type checkConstraint struct{ name, clause string }

// readConstraints reads the primary key, unique, foreign key and check
// constraints with their columns.
func (a *Adapter) readConstraints(ctx context.Context, ref database.TableRef) ([]database.ConstraintMetadata, []checkConstraint, error) {
	type entry struct {
		c   database.ConstraintMetadata
		ref string // FOREIGN KEY: the referenced table, for display
	}
	var order []string
	byName := map[string]*entry{}
	err := a.eachRow(ctx, `SELECT CONSTRAINT_NAME, CONSTRAINT_TYPE FROM information_schema.TABLE_CONSTRAINTS
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY CONSTRAINT_TYPE = 'PRIMARY KEY' DESC, CONSTRAINT_NAME`,
		[]any{ref.Schema, ref.Name}, func(r *sql.Rows) error {
			var name, typ string
			if err := r.Scan(&name, &typ); err != nil {
				return err
			}
			var t database.ConstraintType
			switch strings.ToUpper(typ) {
			case "PRIMARY KEY":
				t = database.ConstraintPrimaryKey
			case "UNIQUE":
				t = database.ConstraintUnique
			case "FOREIGN KEY":
				t = database.ConstraintForeignKey
			case "CHECK":
				t = database.ConstraintCheck
			default:
				return nil
			}
			if _, dup := byName[name]; !dup {
				order = append(order, name)
				byName[name] = &entry{c: database.ConstraintMetadata{Name: name, Type: t}}
			}
			return nil
		})
	if err != nil {
		return nil, nil, err
	}
	refCols := map[string][]string{}
	err = a.eachRow(ctx, `SELECT CONSTRAINT_NAME, COLUMN_NAME, IFNULL(REFERENCED_TABLE_SCHEMA, ''), IFNULL(REFERENCED_TABLE_NAME, ''), IFNULL(REFERENCED_COLUMN_NAME, '')
		FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY CONSTRAINT_NAME, ORDINAL_POSITION`,
		[]any{ref.Schema, ref.Name}, func(r *sql.Rows) error {
			var name, col, rs, rt, rc string
			if err := r.Scan(&name, &col, &rs, &rt, &rc); err != nil {
				return err
			}
			e := byName[name]
			if e == nil {
				return nil
			}
			e.c.Columns = append(e.c.Columns, col)
			if rt != "" {
				e.ref = quoteIdent(rs) + "." + quoteIdent(rt)
				refCols[name] = append(refCols[name], rc)
			}
			return nil
		})
	if err != nil {
		return nil, nil, err
	}
	// A MariaDB constraint name is unique in its table only, so the check
	// clauses are matched on the table too where the server has the column.
	checkQuery := `SELECT cc.CONSTRAINT_NAME, cc.CHECK_CLAUSE FROM information_schema.CHECK_CONSTRAINTS cc
		JOIN information_schema.TABLE_CONSTRAINTS tc ON tc.CONSTRAINT_SCHEMA = cc.CONSTRAINT_SCHEMA AND tc.CONSTRAINT_NAME = cc.CONSTRAINT_NAME
		WHERE tc.TABLE_SCHEMA = ? AND tc.TABLE_NAME = ? AND tc.CONSTRAINT_TYPE = 'CHECK'`
	if a.server.MariaDB {
		checkQuery += " AND cc.TABLE_NAME = tc.TABLE_NAME"
	}
	var checks []checkConstraint
	err = a.eachRow(ctx, checkQuery, []any{ref.Schema, ref.Name}, func(r *sql.Rows) error {
		var ch checkConstraint
		if err := r.Scan(&ch.name, &ch.clause); err != nil {
			return err
		}
		checks = append(checks, ch)
		if e := byName[ch.name]; e != nil {
			e.c.Definition = "CHECK (" + ch.clause + ")"
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	out := make([]database.ConstraintMetadata, 0, len(order))
	for _, name := range order {
		e := byName[name]
		switch e.c.Type {
		case database.ConstraintPrimaryKey:
			e.c.Definition = "PRIMARY KEY (" + joinQuoted(e.c.Columns) + ")"
		case database.ConstraintUnique:
			e.c.Definition = "UNIQUE (" + joinQuoted(e.c.Columns) + ")"
		case database.ConstraintForeignKey:
			e.c.Definition = "FOREIGN KEY (" + joinQuoted(e.c.Columns) + ") REFERENCES " + e.ref + " (" + joinQuoted(refCols[name]) + ")"
		}
		out = append(out, e.c)
	}
	return out, checks, nil
}

// markJSONColumns marks the JSON columns of a MariaDB table: MariaDB's JSON
// is LONGTEXT with a json_valid() check, which is how it is recognized.
func markJSONColumns(m *database.TableMetadata, checks []checkConstraint) {
	for _, ch := range checks {
		clause := strings.ReplaceAll(strings.TrimSpace(ch.clause), " ", "")
		for i := range m.Columns {
			c := &m.Columns[i]
			if c.Class == database.ClassText && strings.EqualFold(clause, "json_valid("+quoteIdent(c.Name)+")") {
				c.Class = database.ClassJSON
			}
		}
	}
}

func joinQuoted(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = quoteIdent(n)
	}
	return strings.Join(q, ", ")
}

// readForeignKeys reads foreign keys matched by where, which compares a
// schema and a table name with the two placeholders.
func (a *Adapter) readForeignKeys(ctx context.Context, where string, ref database.TableRef) ([]database.ForeignKeyMetadata, error) {
	var out []database.ForeignKeyMetadata
	index := map[string]int{}
	err := a.eachRow(ctx, `SELECT k.CONSTRAINT_NAME, k.TABLE_SCHEMA, k.TABLE_NAME, k.COLUMN_NAME,
			k.REFERENCED_TABLE_SCHEMA, k.REFERENCED_TABLE_NAME, k.REFERENCED_COLUMN_NAME, r.UPDATE_RULE, r.DELETE_RULE
		FROM information_schema.KEY_COLUMN_USAGE k
		JOIN information_schema.REFERENTIAL_CONSTRAINTS r
			ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME AND r.TABLE_NAME = k.TABLE_NAME
		WHERE `+where+` AND k.REFERENCED_TABLE_NAME IS NOT NULL
		ORDER BY k.TABLE_SCHEMA, k.TABLE_NAME, k.CONSTRAINT_NAME, k.ORDINAL_POSITION`,
		[]any{ref.Schema, ref.Name}, func(r *sql.Rows) error {
			var name, ts, tn, col, rs, rt, rc, onUpdate, onDelete string
			if err := r.Scan(&name, &ts, &tn, &col, &rs, &rt, &rc, &onUpdate, &onDelete); err != nil {
				return err
			}
			key := ts + "\x00" + tn + "\x00" + name
			i, ok := index[key]
			if !ok {
				i = len(out)
				index[key] = i
				out = append(out, database.ForeignKeyMetadata{
					Name:     name,
					Table:    database.TableRef{Schema: ts, Name: tn},
					RefTable: database.TableRef{Schema: rs, Name: rt},
					OnUpdate: onUpdate,
					OnDelete: onDelete,
				})
			}
			out[i].Columns = append(out[i].Columns, col)
			out[i].RefColumns = append(out[i].RefColumns, rc)
			return nil
		})
	return out, err
}

// readIndexes reads the indexes and their parts. A part that is an
// expression (a functional index) makes the index unusable as a row
// identity; a prefix part is shown with its length.
func (a *Adapter) readIndexes(ctx context.Context, ref database.TableRef) ([]database.IndexMetadata, error) {
	expr := "''"
	if !a.server.MariaDB {
		expr = "IFNULL(EXPRESSION, '')" // functional key parts, MySQL 8.0.13+
	}
	var out []database.IndexMetadata
	index := map[string]int{}
	exprIndex := map[string]bool{}
	err := a.eachRow(ctx, `SELECT INDEX_NAME, NON_UNIQUE, IFNULL(COLUMN_NAME, ''), SUB_PART, INDEX_TYPE, `+expr+`
		FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY INDEX_NAME = 'PRIMARY' DESC, INDEX_NAME, SEQ_IN_INDEX`,
		[]any{ref.Schema, ref.Name}, func(r *sql.Rows) error {
			var name, col, method, expression string
			var nonUnique int
			var sub sql.NullInt64
			if err := r.Scan(&name, &nonUnique, &col, &sub, &method, &expression); err != nil {
				return err
			}
			i, ok := index[name]
			if !ok {
				i = len(out)
				index[name] = i
				out = append(out, database.IndexMetadata{
					Name: name, Method: method, Unique: nonUnique == 0, Primary: name == "PRIMARY", Valid: true,
				})
			}
			switch {
			case col == "":
				exprIndex[name] = true
				out[i].Columns = append(out[i].Columns, "("+expression+")")
			case sub.Valid:
				out[i].Columns = append(out[i].Columns, fmt.Sprintf("%s(%d)", col, sub.Int64))
				out[i].KeyColumns = append(out[i].KeyColumns, col)
			default:
				out[i].Columns = append(out[i].Columns, col)
				out[i].KeyColumns = append(out[i].KeyColumns, col)
			}
			return nil
		})
	for i := range out {
		if exprIndex[out[i].Name] {
			out[i].KeyColumns = nil
		}
	}
	return out, err
}

// readTriggers reads the triggers of the table.
func (a *Adapter) readTriggers(ctx context.Context, ref database.TableRef) ([]database.TriggerMetadata, error) {
	var out []database.TriggerMetadata
	err := a.eachRow(ctx, `SELECT TRIGGER_NAME, ACTION_TIMING, EVENT_MANIPULATION, ACTION_STATEMENT
		FROM information_schema.TRIGGERS WHERE EVENT_OBJECT_SCHEMA = ? AND EVENT_OBJECT_TABLE = ? ORDER BY ACTION_TIMING, EVENT_MANIPULATION, ACTION_ORDER`,
		[]any{ref.Schema, ref.Name}, func(r *sql.Rows) error {
			var t database.TriggerMetadata
			var event string
			if err := r.Scan(&t.Name, &t.Timing, &event, &t.Definition); err != nil {
				return err
			}
			t.Events, t.PerRow, t.Enabled = []string{event}, true, "enabled"
			out = append(out, t)
			return nil
		})
	return out, err
}
