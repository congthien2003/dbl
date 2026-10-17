package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"dbl/internal/database"
)

// mainSchema is the one schema the explorer shows. A SQLite file has the
// schema "main"; attached databases and the temp schema are not listed.
const mainSchema = "main"

// readOnlyReason is why every table of a read-only connection is read-only.
const readOnlyReason = "The connection is read-only."

// The metadata is read from sqlite_master and the table PRAGMAs. Names go
// into the PRAGMAs quoted (they cannot be bound); the values of the other
// queries are bound.

// ListSchemas returns the one schema, main.
func (a *Adapter) ListSchemas(ctx context.Context) ([]database.Schema, error) {
	if err := ctx.Err(); err != nil {
		return nil, classify(ctx, err, "list the schemas")
	}
	return []database.Schema{{Name: mainSchema}}, nil
}

// ListTables lists the tables and views of the file, by name, without
// SQLite's own sqlite_* tables.
func (a *Adapter) ListTables(ctx context.Context, schema string) ([]database.Table, error) {
	if schema != mainSchema {
		return nil, nil
	}
	rows, err := a.db.QueryContext(ctx, `
SELECT name, type, COALESCE(sql, '')
FROM sqlite_master
WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite\_%' ESCAPE '\'
ORDER BY lower(name), name`)
	if err != nil {
		return nil, classify(ctx, err, "list the tables")
	}
	defer rows.Close()
	var out []database.Table
	for rows.Next() {
		var name, typ, ddl string
		if err := rows.Scan(&name, &typ, &ddl); err != nil {
			return nil, classify(ctx, err, "list the tables")
		}
		t := database.Table{Ref: database.TableRef{Catalog: a.catalog, Schema: mainSchema, Name: name}}
		t.Kind, t.Subtype = objectKind(typ, ddl)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, classify(ctx, err, "list the tables")
	}
	return out, nil
}

var virtualRE = regexp.MustCompile(`(?i)^\s*CREATE\s+VIRTUAL\s+TABLE`)

func objectKind(typ, ddl string) (database.ObjectKind, string) {
	switch {
	case typ == "view":
		return database.ObjectView, ""
	case virtualRE.MatchString(ddl):
		return database.ObjectTable, "virtual table"
	}
	return database.ObjectTable, ""
}

// DescribeTable reads the metadata of a table or view. Without the columns
// there is nothing to show, so that read fails the call; every other
// section reports its own failure.
func (a *Adapter) DescribeTable(ctx context.Context, ref database.TableRef) (*database.TableMetadata, error) {
	ref.Catalog, ref.Schema = a.catalog, mainSchema
	m := &database.TableMetadata{Ref: ref, EstimatedRows: -1}

	var typ, ddl string
	err := a.db.QueryRowContext(ctx,
		`SELECT type, COALESCE(sql, '') FROM sqlite_master WHERE type IN ('table', 'view') AND name = ?`,
		ref.Name).Scan(&typ, &ddl)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &database.Error{
			Kind:    database.KindMetadataUnavailable,
			Message: "The table " + ref.Name + " no longer exists.",
			Hint:    "Refresh the node in the explorer.",
			Err:     err,
		}
	}
	if err != nil {
		return nil, metadataError(ctx, err, "read the table")
	}
	m.Kind, m.Subtype = objectKind(typ, ddl)

	pk, err := a.readColumns(ctx, ref, m)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, classify(ctx, err, "read the table")
	}

	na := func(reason string) (database.SectionState, string) { return database.SectionNotApplicable, reason }
	if m.Kind == database.ObjectView {
		m.Constraints.State, m.Constraints.Reason = na("Views have no constraints.")
		m.ForeignKeys.State, m.ForeignKeys.Reason = na("Views have no foreign keys.")
		m.Referenced.State, m.Referenced.Reason = na("Views cannot be referenced by foreign keys.")
		m.Indexes.State, m.Indexes.Reason = na("Views have no indexes.")
	} else {
		var indexes []indexInfo
		m.Indexes = readSection(func() ([]database.IndexMetadata, error) {
			items, list, err := a.readIndexes(ctx, ref)
			indexes = list
			return items, err
		}, "indexes")
		m.Constraints = readSection(func() ([]database.ConstraintMetadata, error) {
			if !m.Indexes.OK() {
				return nil, errors.New(m.Indexes.Reason)
			}
			return constraintsOf(pk, indexes), nil
		}, "constraints")
		m.ForeignKeys = readSection(func() ([]database.ForeignKeyMetadata, error) {
			return a.foreignKeysOf(ctx, ref.Name)
		}, "foreign keys")
		m.Referenced = readSection(func() ([]database.ForeignKeyMetadata, error) {
			return a.readReferenced(ctx, ref)
		}, "incoming foreign keys")
	}
	m.Triggers = readSection(func() ([]database.TriggerMetadata, error) {
		return a.readTriggers(ctx, ref)
	}, "triggers")
	if err := ctx.Err(); err != nil {
		return nil, classify(ctx, err, "read the table")
	}

	m.MarkKeys()
	m.ResolveIdentity()
	if a.readOnly {
		m.ReadOnly = readOnlyReason
	}
	return m, nil
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
func readSection[T any](read func() ([]T, error), what string) database.Section[T] {
	items, err := read()
	if err == nil {
		return database.Section[T]{Items: items}
	}
	sec := database.Section[T]{State: database.SectionFailed, Reason: "The " + what + " could not be read: " + err.Error()}
	var de *database.Error
	if errors.As(err, &de) {
		sec.Reason = "The " + what + " could not be read: " + de.Message
		if de.Kind == database.KindPermission {
			sec.State = database.SectionDenied
			sec.Reason = "You may not read the " + what + ": " + de.Message
		}
	}
	return sec
}

// keyColumn is a column of a primary key and its place in the key.
type keyColumn struct {
	pos  int
	name string
}

func keyNames(keys []keyColumn) []string {
	sort.SliceStable(keys, func(i, j int) bool { return keys[i].pos < keys[j].pos })
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k.name
	}
	return out
}

// readColumns reads the columns with PRAGMA table_xinfo and returns the
// primary key columns in key order. Hidden columns of virtual tables are
// left out; generated columns are marked.
func (a *Adapter) readColumns(ctx context.Context, ref database.TableRef, m *database.TableMetadata) ([]string, error) {
	rows, err := a.db.QueryContext(ctx, "PRAGMA main.table_xinfo("+quoteIdent(ref.Name)+")")
	if err != nil {
		return nil, metadataError(ctx, err, "read the columns")
	}
	defer rows.Close()
	var keys []keyColumn
	for rows.Next() {
		var cid, notNull, pkPos, hidden int
		var name, declared string
		var def sql.NullString
		if err := rows.Scan(&cid, &name, &declared, &notNull, &def, &pkPos, &hidden); err != nil {
			return nil, metadataError(ctx, err, "read the columns")
		}
		if hidden == 1 { // a hidden column of a virtual table
			continue
		}
		c := database.ColumnMetadata{
			Name: name, Ordinal: len(m.Columns) + 1, NativeType: declared,
			Nullable:  notNull == 0 && pkPos == 0,
			Generated: hidden == 2 || hidden == 3,
		}
		c.Class, c.IntBits = classOf(declared)
		if def.Valid {
			d := def.String
			c.Default = &d
		}
		if pkPos > 0 {
			keys = append(keys, keyColumn{pkPos, name})
		}
		m.Columns = append(m.Columns, c)
	}
	if err := rows.Err(); err != nil {
		return nil, metadataError(ctx, err, "read the columns")
	}
	if len(m.Columns) == 0 {
		return nil, &database.Error{Kind: database.KindMetadataUnavailable, Message: "The table has no columns that can be read."}
	}
	return keyNames(keys), nil
}

// indexInfo is an index as PRAGMA index_list gave it, for the constraints.
type indexInfo struct {
	name    string
	origin  string // c: CREATE INDEX, u: UNIQUE constraint, pk: PRIMARY KEY
	unique  bool
	partial bool
	keys    []string // nil when the index has an expression
}

// readIndexes reads PRAGMA index_list and index_info.
func (a *Adapter) readIndexes(ctx context.Context, ref database.TableRef) ([]database.IndexMetadata, []indexInfo, error) {
	rows, err := a.db.QueryContext(ctx, "PRAGMA main.index_list("+quoteIdent(ref.Name)+")")
	if err != nil {
		return nil, nil, classify(ctx, err, "read the indexes")
	}
	var list []indexInfo
	for rows.Next() {
		var seq, unique, partial int
		var ix indexInfo
		if err := rows.Scan(&seq, &ix.name, &unique, &ix.origin, &partial); err != nil {
			rows.Close()
			return nil, nil, classify(ctx, err, "read the indexes")
		}
		ix.unique, ix.partial = unique != 0, partial != 0
		list = append(list, ix)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, classify(ctx, err, "read the indexes")
	}

	out := make([]database.IndexMetadata, 0, len(list))
	for i := range list {
		ix := &list[i]
		cols, keys, err := a.indexColumns(ctx, ix.name)
		if err != nil {
			return nil, nil, err
		}
		ix.keys = keys
		md := database.IndexMetadata{
			Name: ix.name, Columns: cols, KeyColumns: keys, Method: "btree",
			Unique: ix.unique, Primary: ix.origin == "pk", Partial: ix.partial, Valid: true,
		}
		if ix.partial {
			md.Predicate = a.indexPredicate(ctx, ix.name)
		}
		out = append(out, md)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Primary != out[j].Primary {
			return out[i].Primary
		}
		return out[i].Name < out[j].Name
	})
	return out, list, nil
}

// indexColumns returns the columns of an index for display and the plain
// column names, nil when any key part is an expression.
func (a *Adapter) indexColumns(ctx context.Context, index string) (display, keys []string, err error) {
	rows, err := a.db.QueryContext(ctx, "PRAGMA main.index_info("+quoteIdent(index)+")")
	if err != nil {
		return nil, nil, classify(ctx, err, "read the indexes")
	}
	defer rows.Close()
	plain := true
	for rows.Next() {
		var seqno, cid int
		var name sql.NullString
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			return nil, nil, classify(ctx, err, "read the indexes")
		}
		if !name.Valid {
			plain = false
			display = append(display, "(expression)")
			continue
		}
		display = append(display, name.String)
		keys = append(keys, name.String)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, classify(ctx, err, "read the indexes")
	}
	if !plain {
		keys = nil
	}
	return display, keys, nil
}

var wherePartRE = regexp.MustCompile(`(?is)\bWHERE\b(.*)$`)

// indexPredicate returns the WHERE of a partial index, or "" when it cannot
// be found. It is for display only.
func (a *Adapter) indexPredicate(ctx context.Context, index string) string {
	var ddl sql.NullString
	if err := a.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&ddl); err != nil || !ddl.Valid {
		return ""
	}
	if m := wherePartRE.FindStringSubmatch(ddl.String); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// constraintsOf lists the primary key and the UNIQUE constraints. SQLite
// does not name them.
func constraintsOf(pk []string, indexes []indexInfo) []database.ConstraintMetadata {
	var out []database.ConstraintMetadata
	if len(pk) > 0 {
		out = append(out, database.ConstraintMetadata{
			Type: database.ConstraintPrimaryKey, Columns: pk,
			Definition: "PRIMARY KEY (" + joinQuoted(pk) + ")",
		})
	}
	for _, ix := range indexes {
		if ix.origin != "u" || len(ix.keys) == 0 {
			continue
		}
		out = append(out, database.ConstraintMetadata{
			Name: ix.name, Type: database.ConstraintUnique, Columns: ix.keys,
			Definition: "UNIQUE (" + joinQuoted(ix.keys) + ")",
		})
	}
	return out
}

func joinQuoted(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = quoteIdent(n)
	}
	return strings.Join(q, ", ")
}

// fkRow is a line of PRAGMA foreign_key_list.
type fkRow struct {
	id       int
	table    string
	from     string
	to       sql.NullString
	onUpdate string
	onDelete string
}

func (a *Adapter) foreignKeyRows(ctx context.Context, table string) ([]fkRow, error) {
	rows, err := a.db.QueryContext(ctx, "PRAGMA main.foreign_key_list("+quoteIdent(table)+")")
	if err != nil {
		return nil, classify(ctx, err, "read the foreign keys")
	}
	defer rows.Close()
	var out []fkRow
	for rows.Next() {
		var r fkRow
		var seq int
		var match string
		if err := rows.Scan(&r.id, &seq, &r.table, &r.from, &r.to, &r.onUpdate, &r.onDelete, &match); err != nil {
			return nil, classify(ctx, err, "read the foreign keys")
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, classify(ctx, err, "read the foreign keys")
	}
	return out, nil
}

// foreignKeysOf reads the keys a table holds, grouped by key. A key without
// target columns points at the primary key of the other table.
func (a *Adapter) foreignKeysOf(ctx context.Context, table string) ([]database.ForeignKeyMetadata, error) {
	rows, err := a.foreignKeyRows(ctx, table)
	if err != nil {
		return nil, err
	}
	var out []database.ForeignKeyMetadata
	index := map[int]int{}
	for _, r := range rows {
		i, ok := index[r.id]
		if !ok {
			i = len(out)
			index[r.id] = i
			out = append(out, database.ForeignKeyMetadata{
				Table:    database.TableRef{Catalog: a.catalog, Schema: mainSchema, Name: table},
				RefTable: database.TableRef{Catalog: a.catalog, Schema: mainSchema, Name: r.table},
				OnUpdate: r.onUpdate, OnDelete: r.onDelete,
			})
		}
		out[i].Columns = append(out[i].Columns, r.from)
		if r.to.Valid {
			out[i].RefColumns = append(out[i].RefColumns, r.to.String)
		}
	}
	for i := range out {
		fk := &out[i]
		if len(fk.RefColumns) == 0 {
			if pk, err := a.primaryKeyOf(ctx, fk.RefTable.Name); err == nil {
				fk.RefColumns = pk
			}
		}
		fk.Name = fmt.Sprintf("fk_%s_%s", table, strings.Join(fk.Columns, "_"))
	}
	return out, nil
}

// primaryKeyOf returns the primary key columns of a table in key order.
func (a *Adapter) primaryKeyOf(ctx context.Context, table string) ([]string, error) {
	rows, err := a.db.QueryContext(ctx, "PRAGMA main.table_info("+quoteIdent(table)+")")
	if err != nil {
		return nil, classify(ctx, err, "read the columns")
	}
	defer rows.Close()
	var keys []keyColumn
	for rows.Next() {
		var cid, notNull, pkPos int
		var name, declared string
		var def sql.NullString
		if err := rows.Scan(&cid, &name, &declared, &notNull, &def, &pkPos); err != nil {
			return nil, classify(ctx, err, "read the columns")
		}
		if pkPos > 0 {
			keys = append(keys, keyColumn{pkPos, name})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, classify(ctx, err, "read the columns")
	}
	return keyNames(keys), nil
}

// readReferenced finds the keys of other tables that point at this one, by
// reading the keys of every table: SQLite has no catalog of them.
func (a *Adapter) readReferenced(ctx context.Context, ref database.TableRef) ([]database.ForeignKeyMetadata, error) {
	tables, err := a.ListTables(ctx, mainSchema)
	if err != nil {
		return nil, err
	}
	var out []database.ForeignKeyMetadata
	for _, t := range tables {
		if t.Kind == database.ObjectView {
			continue
		}
		fks, err := a.foreignKeysOf(ctx, t.Ref.Name)
		if err != nil {
			return nil, err
		}
		for _, fk := range fks {
			if strings.EqualFold(fk.RefTable.Name, ref.Name) {
				out = append(out, fk)
			}
		}
	}
	return out, nil
}

var triggerHeadRE = regexp.MustCompile(`(?is)^\s*CREATE\s+(?:TEMP(?:ORARY)?\s+)?TRIGGER\s+(?:IF\s+NOT\s+EXISTS\s+)?(?:"(?:[^"]|"")*"|[^\s]+)\s+(BEFORE|AFTER|INSTEAD\s+OF)?\s*(DELETE|INSERT|UPDATE)`)

func (a *Adapter) readTriggers(ctx context.Context, ref database.TableRef) ([]database.TriggerMetadata, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT name, COALESCE(sql, '') FROM sqlite_master WHERE type = 'trigger' AND tbl_name = ? ORDER BY name`, ref.Name)
	if err != nil {
		return nil, classify(ctx, err, "read the triggers")
	}
	defer rows.Close()
	var out []database.TriggerMetadata
	for rows.Next() {
		// A trigger without BEFORE or AFTER is a BEFORE trigger in SQLite.
		t := database.TriggerMetadata{PerRow: true, Enabled: "enabled", Timing: "BEFORE"}
		if err := rows.Scan(&t.Name, &t.Definition); err != nil {
			return nil, classify(ctx, err, "read the triggers")
		}
		if m := triggerHeadRE.FindStringSubmatch(t.Definition); m != nil {
			if m[1] != "" {
				t.Timing = strings.Join(strings.Fields(strings.ToUpper(m[1])), " ")
			}
			t.Events = []string{strings.ToUpper(m[2])}
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, classify(ctx, err, "read the triggers")
	}
	return out, nil
}
