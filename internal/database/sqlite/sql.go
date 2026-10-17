package sqlite

import (
	"fmt"
	"strings"

	"dbl/internal/database"
)

// quoteIdent quotes a name for use in SQL. SQLite reads "..." as an
// identifier, with a doubled quote for a quote inside.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// quoteTable returns the qualified, quoted name of a table. The schema is
// the one the explorer shows, always main.
func quoteTable(ref database.TableRef) string {
	schema := ref.Schema
	if schema == "" {
		schema = mainSchema
	}
	return quoteIdent(schema) + "." + quoteIdent(ref.Name)
}

// This file builds the SQL of a page. Nothing here runs a query. Names are
// quoted by quoteIdent and values travel only as bound parameters, so the
// text of a query holds names the metadata returned and placeholders, never
// anything the user typed.

// likeEscaper escapes what LIKE gives a meaning to, with the backslash named
// in the ESCAPE clause.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// containsPattern turns text typed by the user into a pattern that matches
// any value that contains it literally.
func containsPattern(text string) string { return "%" + likeEscaper.Replace(text) + "%" }

// pageQuery is a page ready to run. Every column of the table is selected as
// two result columns: its value, NULL when it is a blob, and the length of
// the blob, NULL when it is not. A blob is never loaded, and the pair also
// keeps the driver from decoding dates or booleans by the declared type, so
// a value comes back as SQLite stores it.
type pageQuery struct {
	SQL  string
	Args []any
	// Size is the page size. The query asks for one row more, which is how
	// the caller learns that more rows exist.
	Size int
	// Reverse says the rows come in the opposite of display order.
	Reverse bool
}

// args collects bound parameters.
type args struct{ list []any }

func (a *args) add(v any) string {
	a.list = append(a.list, v)
	return "?"
}

// buildPageQuery builds the query of a request. The request is validated
// first: an unknown column or an operator the column cannot take never
// reaches the text of a query.
func buildPageQuery(req database.PageRequest) (pageQuery, error) {
	if err := req.Validate(); err != nil {
		return pageQuery{}, err
	}
	m := req.Meta
	plan := database.PlanQuery(m, req.Sort)

	var a args
	pq := pageQuery{Size: req.Size}
	var sel strings.Builder
	for i, c := range m.Columns {
		if i > 0 {
			sel.WriteString(", ")
		}
		q := quoteIdent(c.Name)
		sel.WriteString("CASE WHEN typeof(" + q + ") = 'blob' THEN NULL ELSE " + q + " END, ")
		sel.WriteString("CASE WHEN typeof(" + q + ") = 'blob' THEN length(" + q + ") END")
	}

	var where []string
	if f := req.Filter; f != nil {
		where = append(where, filterClause(f, &a))
	}

	reverse := false
	if plan.Strategy == database.StrategyKeyset {
		switch req.Cursor.Dir {
		case database.CursorAfter, database.CursorBefore:
			reverse = req.Cursor.Dir == database.CursorBefore
			op := ">"
			if reverse {
				op = "<"
			}
			cols := make([]string, len(plan.Key))
			marks := make([]string, len(plan.Key))
			for i, k := range plan.Key {
				cols[i] = quoteIdent(k)
				marks[i] = a.add(req.Cursor.Key[i])
			}
			where = append(where, "("+strings.Join(cols, ", ")+") "+op+" ("+strings.Join(marks, ", ")+")")
		case database.CursorLast:
			reverse = true
		}
	}
	pq.Reverse = reverse

	var q strings.Builder
	q.WriteString("SELECT " + sel.String() + " FROM " + quoteTable(m.Ref))
	if len(where) > 0 {
		q.WriteString(" WHERE " + strings.Join(where, " AND "))
	}
	if len(plan.Order) > 0 {
		q.WriteString(" ORDER BY ")
		for i, o := range plan.Order {
			if i > 0 {
				q.WriteString(", ")
			}
			q.WriteString(quoteIdent(o.Column))
			if o.Desc != reverse {
				q.WriteString(" DESC")
			}
		}
	}
	// One row more than the page, to learn whether another page exists.
	q.WriteString(" LIMIT " + a.add(int64(req.Size)+1))
	if plan.Strategy == database.StrategyOffset && req.Offset > 0 {
		q.WriteString(" OFFSET " + a.add(int64(req.Offset)))
	}
	pq.SQL, pq.Args = q.String(), a.list
	return pq, nil
}

// filterClause renders the column filter. The column is compared as it is,
// so SQLite applies the column's affinity to the typed text; only
// "contains" compares text.
func filterClause(f *database.Filter, a *args) string {
	col := quoteIdent(f.Column)
	switch f.Op {
	case database.OpEquals:
		return col + " = " + a.add(f.Value)
	case database.OpNotEquals:
		return col + " <> " + a.add(f.Value)
	case database.OpContains:
		return "CAST(" + col + " AS TEXT) LIKE " + a.add(containsPattern(f.Value)) + ` ESCAPE '\'`
	case database.OpGreater:
		return col + " > " + a.add(f.Value)
	case database.OpLess:
		return col + " < " + a.add(f.Value)
	case database.OpIsNull:
		return col + " IS NULL"
	case database.OpIsNotNull:
		return col + " IS NOT NULL"
	}
	// Validate has rejected every other operator; reaching here is a bug.
	panic(fmt.Sprintf("sqlite: unknown filter operator %d", f.Op))
}
