package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"dbl/internal/database"
)

// The metadata is read from pg_catalog, which every user may read whatever
// the privileges on the tables: information_schema lists only the objects
// the user can use, which would make a table without privileges look like
// a table that does not exist. The queries need PostgreSQL 12 or later
// (pg_attribute.attgenerated).
//
// Nothing in the SQL below comes from the user. The only values are the
// schema and table names, bound as parameters, and then the object ID the
// first query found.

// ListSchemas lists the schemas of the database, without the system ones
// (pg_*, information_schema).
func (a *Adapter) ListSchemas(ctx context.Context) ([]database.Schema, error) {
	rows, err := a.db.QueryContext(ctx, `
SELECT n.nspname::text
FROM pg_catalog.pg_namespace n
WHERE n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'
ORDER BY lower(n.nspname), n.nspname`)
	if err != nil {
		return nil, classifyQuery(ctx, err, "Listing the schemas")
	}
	defer rows.Close()
	var out []database.Schema
	for rows.Next() {
		var s database.Schema
		if err := rows.Scan(&s.Name); err != nil {
			return nil, classifyQuery(ctx, err, "Listing the schemas")
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyQuery(ctx, err, "Listing the schemas")
	}
	return out, nil
}

// ListTables lists the tables and views of a schema, by name.
func (a *Adapter) ListTables(ctx context.Context, schema string) ([]database.Table, error) {
	rows, err := a.db.QueryContext(ctx, `
SELECT c.relname::text, c.relkind::text, c.relispartition
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relkind IN ('r', 'p', 'v', 'm', 'f')
ORDER BY lower(c.relname), c.relname`, schema)
	if err != nil {
		return nil, classifyQuery(ctx, err, "Listing the tables")
	}
	defer rows.Close()
	var out []database.Table
	for rows.Next() {
		var name, kind string
		var partition bool
		if err := rows.Scan(&name, &kind, &partition); err != nil {
			return nil, classifyQuery(ctx, err, "Listing the tables")
		}
		t := database.Table{Ref: database.TableRef{Catalog: a.catalog, Schema: schema, Name: name}}
		t.Kind, t.Subtype = relationKind(kind, partition)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyQuery(ctx, err, "Listing the tables")
	}
	return out, nil
}

// relationKind maps pg_class.relkind to the kind shown by the explorer and
// the engine's own word for it, when it is not a plain table or view.
func relationKind(relkind string, partition bool) (database.ObjectKind, string) {
	switch relkind {
	case "v":
		return database.ObjectView, ""
	case "m":
		return database.ObjectView, "materialized view"
	case "f":
		return database.ObjectTable, "foreign table"
	case "p":
		return database.ObjectTable, "partitioned table"
	}
	if partition {
		return database.ObjectTable, "partition"
	}
	return database.ObjectTable, ""
}

// classOf classifies a column from its type's name and category
// (pg_type.typname and typcategory, of the base type for a domain). The name
// settles the types the category cannot tell apart; the category covers the
// rest, and anything unknown is ClassOther, which only allows NULL checks.
func classOf(typname, category string) database.ColumnClass {
	switch typname {
	case "bytea":
		return database.ClassBinary
	case "json", "jsonb":
		return database.ClassJSON
	case "money", "xml":
		return database.ClassOther
	case "uuid", "inet", "cidr", "macaddr", "macaddr8", "bit", "varbit":
		return database.ClassOrdered
	}
	switch category {
	case "N":
		return database.ClassNumber
	case "S", "E":
		return database.ClassText
	case "B":
		return database.ClassBool
	case "D", "T":
		return database.ClassDateTime
	}
	return database.ClassOther
}

// DescribeTable reads the metadata of a table or view. Without the columns
// there is nothing to show, so that read fails the call; every other
// section reports its own failure.
func (a *Adapter) DescribeTable(ctx context.Context, ref database.TableRef) (*database.TableMetadata, error) {
	ref.Catalog = a.catalog
	m := &database.TableMetadata{Ref: ref, EstimatedRows: -1}

	var oid, relkind string
	var partition bool
	var tuples, pages int64
	err := a.db.QueryRowContext(ctx, `
SELECT c.oid::text, c.relkind::text, c.relispartition, c.reltuples::bigint, c.relpages::bigint
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'p', 'v', 'm', 'f')`,
		ref.Schema, ref.Name).Scan(&oid, &relkind, &partition, &tuples, &pages)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &database.Error{
			Kind:    database.KindMetadataUnavailable,
			Message: "The table " + ref.Schema + "." + ref.Name + " no longer exists.",
			Hint:    "Refresh the node in the explorer.",
			Err:     err,
		}
	}
	if err != nil {
		return nil, classifyQuery(ctx, err, "Reading the table")
	}
	m.Kind, m.Subtype = relationKind(relkind, partition)
	if relkind != "v" {
		// PostgreSQL 14 and later report -1 for a table never analyzed;
		// earlier versions report 0, which with pages in the table means the
		// same. Either way the estimate is unknown.
		switch {
		case tuples < 0, tuples == 0 && pages > 0:
			m.EstimatedRows = -1
		default:
			m.EstimatedRows = tuples
		}
	}

	if err := a.readColumns(ctx, oid, m); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, classifyQuery(ctx, err, "Reading the table")
	}

	isView := relkind == "v" || relkind == "m"
	na := func(reason string) (st database.SectionState, r string) { return database.SectionNotApplicable, reason }

	if isView {
		m.Constraints.State, m.Constraints.Reason = na("Views have no constraints.")
		m.ForeignKeys.State, m.ForeignKeys.Reason = na("Views have no foreign keys.")
		m.Referenced.State, m.Referenced.Reason = na("Views cannot be referenced by foreign keys.")
	} else {
		m.Constraints = readSection(ctx, "constraints", func() ([]database.ConstraintMetadata, error) {
			return a.readConstraints(ctx, oid)
		})
		m.ForeignKeys = readSection(ctx, "foreign keys", func() ([]database.ForeignKeyMetadata, error) {
			return a.readForeignKeys(ctx, oid, "conrelid")
		})
		m.Referenced = readSection(ctx, "incoming foreign keys", func() ([]database.ForeignKeyMetadata, error) {
			return a.readForeignKeys(ctx, oid, "confrelid")
		})
	}
	if relkind == "v" {
		m.Indexes.State, m.Indexes.Reason = na("Views have no indexes.")
	} else {
		m.Indexes = readSection(ctx, "indexes", func() ([]database.IndexMetadata, error) {
			return a.readIndexes(ctx, oid)
		})
	}
	m.Triggers = readSection(ctx, "triggers", func() ([]database.TriggerMetadata, error) {
		return a.readTriggers(ctx, oid)
	})
	if err := ctx.Err(); err != nil {
		return nil, classifyQuery(ctx, err, "Reading the table")
	}

	m.MarkKeys()
	m.ResolveIdentity()
	return m, nil
}

// readSection runs one metadata read and turns its failure into the state
// of the section: a missing privilege is told apart from another failure.
// A canceled context is not a section problem; the caller checks it.
func readSection[T any](ctx context.Context, what string, read func() ([]T, error)) database.Section[T] {
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

func (a *Adapter) readColumns(ctx context.Context, oid string, m *database.TableMetadata) error {
	rows, err := a.db.QueryContext(ctx, `
SELECT a.attnum::int,
       a.attname::text,
       pg_catalog.format_type(a.atttypid, a.atttypmod),
       COALESCE(bt.typname, t.typname)::text,
       COALESCE(bt.typcategory, t.typcategory)::text,
       a.attnotnull,
       pg_catalog.pg_get_expr(d.adbin, d.adrelid),
       a.attidentity::text,
       a.attgenerated::text
FROM pg_catalog.pg_attribute a
JOIN pg_catalog.pg_type t ON t.oid = a.atttypid
LEFT JOIN pg_catalog.pg_type bt ON t.typtype = 'd' AND bt.oid = t.typbasetype
LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE a.attrelid = $1::oid AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`, oid)
	if err != nil {
		return metadataError(ctx, err, "Reading the columns")
	}
	defer rows.Close()
	for rows.Next() {
		var c database.ColumnMetadata
		var typname, category, identity, generated string
		var notNull bool
		var def sql.NullString
		if err := rows.Scan(&c.Ordinal, &c.Name, &c.NativeType, &typname, &category, &notNull, &def, &identity, &generated); err != nil {
			return metadataError(ctx, err, "Reading the columns")
		}
		c.Class = classOf(typname, category)
		c.Nullable = !notNull
		if def.Valid {
			d := def.String
			c.Default = &d
		}
		switch identity {
		case "a":
			c.Identity = database.IdentityAlways
		case "d":
			c.Identity = database.IdentityByDefault
		default:
			if def.Valid && strings.HasPrefix(def.String, "nextval(") {
				c.Identity = database.IdentitySerial
			}
		}
		c.Generated = generated != ""
		m.Columns = append(m.Columns, c)
	}
	if err := rows.Err(); err != nil {
		return metadataError(ctx, err, "Reading the columns")
	}
	if len(m.Columns) == 0 {
		return &database.Error{Kind: database.KindMetadataUnavailable, Message: "The table has no columns that can be read."}
	}
	return nil
}

// metadataError classifies a failure of a metadata read that is required.
func metadataError(ctx context.Context, err error, what string) *database.Error {
	e := classifyQuery(ctx, err, what)
	if e.Kind == database.KindConnection || e.Kind == database.KindSyntax {
		// The metadata is what could not be had; say so, keeping the cause.
		c := *e
		c.Kind = database.KindMetadataUnavailable
		return &c
	}
	return e
}

// jsonNames decodes a JSON array of names built by the SQL below.
func jsonNames(s string) ([]string, error) {
	var names []string
	if err := json.Unmarshal([]byte(s), &names); err != nil {
		return nil, err
	}
	return names, nil
}

// attrNames is the SQL that lists the names of the columns a constraint
// refers to, in order, as JSON text. keys is the attnum array
// (conkey/confkey) and rel the table it refers to (conrelid/confrelid).
func attrNames(keys, rel string) string {
	return `(SELECT COALESCE(jsonb_agg(a.attname::text ORDER BY u.ord), '[]'::jsonb)::text
 FROM unnest(` + keys + `) WITH ORDINALITY AS u(attnum, ord)
 JOIN pg_catalog.pg_attribute a ON a.attrelid = ` + rel + ` AND a.attnum = u.attnum)`
}

func (a *Adapter) readConstraints(ctx context.Context, oid string) ([]database.ConstraintMetadata, error) {
	rows, err := a.db.QueryContext(ctx, `
SELECT c.conname::text, c.contype::text, pg_catalog.pg_get_constraintdef(c.oid, true), `+attrNames("c.conkey", "c.conrelid")+`
FROM pg_catalog.pg_constraint c
WHERE c.conrelid = $1::oid AND c.contype IN ('p', 'u', 'c', 'f', 'x')
ORDER BY CASE c.contype WHEN 'p' THEN 0 WHEN 'u' THEN 1 WHEN 'f' THEN 2 WHEN 'c' THEN 3 ELSE 4 END, c.conname`, oid)
	if err != nil {
		return nil, classifyQuery(ctx, err, "Reading the constraints")
	}
	defer rows.Close()
	var out []database.ConstraintMetadata
	for rows.Next() {
		var c database.ConstraintMetadata
		var typ, cols string
		if err := rows.Scan(&c.Name, &typ, &c.Definition, &cols); err != nil {
			return nil, classifyQuery(ctx, err, "Reading the constraints")
		}
		switch typ {
		case "p":
			c.Type = database.ConstraintPrimaryKey
		case "u":
			c.Type = database.ConstraintUnique
		case "c":
			c.Type = database.ConstraintCheck
		case "f":
			c.Type = database.ConstraintForeignKey
		default:
			c.Type = database.ConstraintExclusion
		}
		if c.Columns, err = jsonNames(cols); err != nil {
			return nil, classifyQuery(ctx, err, "Reading the constraints")
		}
		out = append(out, c)
	}
	return out, classifyIfErr(ctx, rows.Err(), "Reading the constraints")
}

// classifyIfErr returns nil for a nil error, else the classified error as an
// error value (a typed nil *Error must not become a non-nil error).
func classifyIfErr(ctx context.Context, err error, what string) error {
	if err == nil {
		return nil
	}
	return classifyQuery(ctx, err, what)
}

// fkAction renders the code of a foreign key action.
func fkAction(code string) string {
	switch code {
	case "c":
		return "CASCADE"
	case "n":
		return "SET NULL"
	case "d":
		return "SET DEFAULT"
	case "r":
		return "RESTRICT"
	}
	return "NO ACTION"
}

// readForeignKeys reads the foreign keys that have this table on one side:
// conrelid for the keys it holds, confrelid for the keys that point to it.
// side is one of those two fixed words, never user input.
func (a *Adapter) readForeignKeys(ctx context.Context, oid, side string) ([]database.ForeignKeyMetadata, error) {
	if side != "conrelid" && side != "confrelid" {
		return nil, errors.New("internal error: unknown foreign key side")
	}
	rows, err := a.db.QueryContext(ctx, `
SELECT c.conname::text, ln.nspname::text, lc.relname::text, rn.nspname::text, rc.relname::text,
       `+attrNames("c.conkey", "c.conrelid")+`,
       `+attrNames("c.confkey", "c.confrelid")+`,
       c.confupdtype::text, c.confdeltype::text
FROM pg_catalog.pg_constraint c
JOIN pg_catalog.pg_class lc ON lc.oid = c.conrelid
JOIN pg_catalog.pg_namespace ln ON ln.oid = lc.relnamespace
JOIN pg_catalog.pg_class rc ON rc.oid = c.confrelid
JOIN pg_catalog.pg_namespace rn ON rn.oid = rc.relnamespace
WHERE c.contype = 'f' AND c.`+side+` = $1::oid
ORDER BY ln.nspname, lc.relname, c.conname`, oid)
	if err != nil {
		return nil, classifyQuery(ctx, err, "Reading the foreign keys")
	}
	defer rows.Close()
	var out []database.ForeignKeyMetadata
	for rows.Next() {
		fk := database.ForeignKeyMetadata{}
		var lSchema, lName, rSchema, rName, cols, refCols, upd, del string
		if err := rows.Scan(&fk.Name, &lSchema, &lName, &rSchema, &rName, &cols, &refCols, &upd, &del); err != nil {
			return nil, classifyQuery(ctx, err, "Reading the foreign keys")
		}
		fk.Table = database.TableRef{Catalog: a.catalog, Schema: lSchema, Name: lName}
		fk.RefTable = database.TableRef{Catalog: a.catalog, Schema: rSchema, Name: rName}
		fk.OnUpdate, fk.OnDelete = fkAction(upd), fkAction(del)
		if fk.Columns, err = jsonNames(cols); err != nil {
			return nil, classifyQuery(ctx, err, "Reading the foreign keys")
		}
		if fk.RefColumns, err = jsonNames(refCols); err != nil {
			return nil, classifyQuery(ctx, err, "Reading the foreign keys")
		}
		out = append(out, fk)
	}
	return out, classifyIfErr(ctx, rows.Err(), "Reading the foreign keys")
}

func (a *Adapter) readIndexes(ctx context.Context, oid string) ([]database.IndexMetadata, error) {
	rows, err := a.db.QueryContext(ctx, `
SELECT ic.relname::text, am.amname::text, i.indisunique, i.indisprimary, i.indisvalid,
       pg_catalog.pg_get_expr(i.indpred, i.indrelid, true),
       (SELECT COALESCE(jsonb_agg(pg_catalog.pg_get_indexdef(i.indexrelid, k, true) ORDER BY k), '[]'::jsonb)::text
          FROM generate_series(1, i.indnkeyatts::int) AS k),
       (SELECT COALESCE(jsonb_agg(CASE WHEN x.attnum = 0 THEN NULL ELSE a.attname::text END ORDER BY x.ord), '[]'::jsonb)::text
          FROM unnest(string_to_array(i.indkey::text, ' ')::int2[]) WITH ORDINALITY AS x(attnum, ord)
          LEFT JOIN pg_catalog.pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = x.attnum
         WHERE x.ord <= i.indnkeyatts)
FROM pg_catalog.pg_index i
JOIN pg_catalog.pg_class ic ON ic.oid = i.indexrelid
JOIN pg_catalog.pg_am am ON am.oid = ic.relam
WHERE i.indrelid = $1::oid
ORDER BY i.indisprimary DESC, ic.relname`, oid)
	if err != nil {
		return nil, classifyQuery(ctx, err, "Reading the indexes")
	}
	defer rows.Close()
	var out []database.IndexMetadata
	for rows.Next() {
		var ix database.IndexMetadata
		var pred sql.NullString
		var display, keys string
		if err := rows.Scan(&ix.Name, &ix.Method, &ix.Unique, &ix.Primary, &ix.Valid, &pred, &display, &keys); err != nil {
			return nil, classifyQuery(ctx, err, "Reading the indexes")
		}
		if pred.Valid {
			ix.Partial, ix.Predicate = true, pred.String
		}
		if ix.Columns, err = jsonNames(display); err != nil {
			return nil, classifyQuery(ctx, err, "Reading the indexes")
		}
		var parts []*string
		if err := json.Unmarshal([]byte(keys), &parts); err != nil {
			return nil, classifyQuery(ctx, err, "Reading the indexes")
		}
		ix.KeyColumns = make([]string, 0, len(parts))
		for _, p := range parts {
			if p == nil { // an expression: there is no plain column list
				ix.KeyColumns = nil
				break
			}
			ix.KeyColumns = append(ix.KeyColumns, *p)
		}
		out = append(out, ix)
	}
	return out, classifyIfErr(ctx, rows.Err(), "Reading the indexes")
}

func (a *Adapter) readTriggers(ctx context.Context, oid string) ([]database.TriggerMetadata, error) {
	rows, err := a.db.QueryContext(ctx, `
SELECT t.tgname::text, t.tgenabled::text, t.tgtype::int, pg_catalog.pg_get_triggerdef(t.oid, true)
FROM pg_catalog.pg_trigger t
WHERE t.tgrelid = $1::oid AND NOT t.tgisinternal
ORDER BY t.tgname`, oid)
	if err != nil {
		return nil, classifyQuery(ctx, err, "Reading the triggers")
	}
	defer rows.Close()
	var out []database.TriggerMetadata
	for rows.Next() {
		var t database.TriggerMetadata
		var enabled string
		var kind int
		if err := rows.Scan(&t.Name, &enabled, &kind, &t.Definition); err != nil {
			return nil, classifyQuery(ctx, err, "Reading the triggers")
		}
		t.Timing, t.Events, t.PerRow = triggerKind(kind)
		t.Enabled = triggerEnabled(enabled)
		out = append(out, t)
	}
	return out, classifyIfErr(ctx, rows.Err(), "Reading the triggers")
}

// triggerKind decodes pg_trigger.tgtype: bit 0 row-level, 1 BEFORE, 2
// INSERT, 3 DELETE, 4 UPDATE, 5 TRUNCATE, 6 INSTEAD OF.
func triggerKind(tgtype int) (timing string, events []string, perRow bool) {
	switch {
	case tgtype&64 != 0:
		timing = "INSTEAD OF"
	case tgtype&2 != 0:
		timing = "BEFORE"
	default:
		timing = "AFTER"
	}
	for _, e := range []struct {
		bit  int
		name string
	}{{4, "INSERT"}, {8, "DELETE"}, {16, "UPDATE"}, {32, "TRUNCATE"}} {
		if tgtype&e.bit != 0 {
			events = append(events, e.name)
		}
	}
	return timing, events, tgtype&1 != 0
}

// triggerEnabled decodes pg_trigger.tgenabled.
func triggerEnabled(code string) string {
	switch code {
	case "D":
		return "disabled"
	case "R":
		return "replica only"
	case "A":
		return "always"
	}
	return "enabled"
}
