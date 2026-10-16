package postgres

import (
	"fmt"
	"strconv"
	"strings"

	"dbl/internal/database"
)

// This file builds the SQL of a page. Nothing here runs a query. Identifiers
// are quoted by quoteIdent and values travel only as bound parameters, so
// the text of a query holds names the metadata returned and placeholders,
// never anything the user typed.

// quoteIdent quotes an identifier for PostgreSQL: it is wrapped in double
// quotes and a double quote inside it is doubled. Any other character,
// including a semicolon or a space, is part of the name.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// quoteTable returns the schema-qualified, quoted name of a table. The
// catalog is not part of it: a PostgreSQL connection is bound to one
// database.
func quoteTable(ref database.TableRef) string {
	return quoteIdent(ref.Schema) + "." + quoteIdent(ref.Name)
}

// likeEscaper escapes the characters LIKE and ILIKE give a meaning to, with
// the backslash, which is the default escape character.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// containsPattern turns text typed by the user into a pattern that matches
// any value that contains it literally.
func containsPattern(text string) string { return "%" + likeEscaper.Replace(text) + "%" }

// pageQuery is a page ready to run.
type pageQuery struct {
	SQL  string
	Args []any
	// Binary[i] is true when column i is selected as its byte length rather
	// than as text.
	Binary []bool
	// Size is the page size. The query asks for one row more, which is how
	// the caller learns that more rows exist.
	Size int
	// Reverse says the rows come in the opposite of display order.
	Reverse bool
}

// args collects bound parameters and hands out their placeholders.
type args struct{ list []any }

func (a *args) add(v any) string {
	a.list = append(a.list, v)
	return "$" + strconv.Itoa(len(a.list))
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
	pq := pageQuery{Size: req.Size, Binary: make([]bool, len(m.Columns))}
	var sel strings.Builder
	for i, c := range m.Columns {
		if i > 0 {
			sel.WriteString(", ")
		}
		if c.Class == database.ClassBinary {
			// A binary value is never loaded: the grid shows its size.
			pq.Binary[i] = true
			sel.WriteString("octet_length(" + quoteIdent(c.Name) + ")")
			continue
		}
		sel.WriteString(quoteIdent(c.Name) + "::text")
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

// filterClause renders the column filter. The column is used as it is, not
// cast, so the server compares it as its own type and may use an index;
// only "contains" compares text. The value is sent as text and the server
// reads it as the type of the column.
func filterClause(f *database.Filter, a *args) string {
	col := quoteIdent(f.Column)
	switch f.Op {
	case database.OpEquals:
		return col + " = " + a.add(f.Value)
	case database.OpNotEquals:
		return col + " <> " + a.add(f.Value)
	case database.OpContains:
		return col + "::text ILIKE " + a.add(containsPattern(f.Value))
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
	panic(fmt.Sprintf("postgres: unknown filter operator %d", f.Op))
}
