package sqlite

import (
	"strings"
	"unicode/utf8"

	"dbl/internal/database"
)

type sqlStatement struct {
	span  database.SQLSpan
	words []string // the unquoted words, in upper case
}

func sqlValidation(message string) error {
	return &database.Error{Kind: database.KindValidation, Message: message}
}

// SelectSQLiteSQL picks the SQL to run: the one contiguous selection when
// there is one, else the statement at the cursor. It recognizes lexical
// statement boundaries, not the SQL grammar: strings, quoted names,
// comments, and the body of a CREATE TRIGGER, which holds semicolons.
func SelectSQLiteSQL(in database.SQLInput) (database.SQLSelection, error) {
	if len(in.Text) > database.SQLMaxInputBytes || !utf8.ValidString(in.Text) || strings.ContainsRune(in.Text, 0) {
		return database.SQLSelection{}, sqlValidation("SQL must be valid UTF-8 without NUL and at most 8 MiB.")
	}
	valid := func(i int) bool {
		return i >= 0 && i <= len(in.Text) && (i == len(in.Text) || utf8.RuneStart(in.Text[i]))
	}
	if !valid(in.Cursor) {
		return database.SQLSelection{}, sqlValidation("The SQL cursor is outside the document.")
	}
	var selected *database.SQLSpan
	for _, span := range in.Selections {
		if !valid(span.Start) || !valid(span.End) || span.Start > span.End {
			return database.SQLSelection{}, sqlValidation("The SQL selection is invalid.")
		}
		if span.Start == span.End {
			continue
		}
		if selected != nil {
			return database.SQLSelection{}, sqlValidation("Select one contiguous SQL block to execute.")
		}
		copy := span
		selected = &copy
	}
	text := in.Text
	if selected != nil {
		text = in.Text[selected.Start:selected.End]
	}
	statements, err := scanSQLStatements(text)
	if err != nil {
		return database.SQLSelection{}, err
	}
	if len(statements) == 0 {
		return database.SQLSelection{}, nil
	}
	if selected != nil {
		return database.SQLSelection{SQL: text, Span: *selected}, nil
	}
	chosen := statements[len(statements)-1].span
	for _, stmt := range statements {
		if in.Cursor < stmt.span.End {
			chosen = stmt.span
			break
		}
	}
	return database.SQLSelection{SQL: in.Text[chosen.Start:chosen.End], Span: chosen}, nil
}

// isTriggerStart reports whether the words begin CREATE [TEMP] TRIGGER.
func isTriggerStart(w []string) bool {
	if len(w) < 2 || w[0] != "CREATE" {
		return false
	}
	if w[1] == "TRIGGER" {
		return true
	}
	return (w[1] == "TEMP" || w[1] == "TEMPORARY") && len(w) > 2 && w[2] == "TRIGGER"
}

// triggerScan follows the BEGIN ... END body of a CREATE TRIGGER, where a
// semicolon ends a statement of the body, not the trigger. CASE ... END
// pairs are counted so their END is not taken for the end of the body.
type triggerScan struct {
	on        bool // the statement is a CREATE TRIGGER
	inBody    bool
	done      bool
	caseDepth int
}

func (t *triggerScan) word(w string, words []string) {
	if !t.on && isTriggerStart(words) {
		t.on = true
	}
	if !t.on || t.done {
		return
	}
	switch w {
	case "CASE":
		t.caseDepth++
	case "END":
		switch {
		case t.caseDepth > 0:
			t.caseDepth--
		case t.inBody:
			t.inBody, t.done = false, true
		}
	case "BEGIN":
		if !t.inBody && t.caseDepth == 0 {
			t.inBody = true
		}
	}
}

func scanSQLStatements(s string) ([]sqlStatement, error) {
	var out []sqlStatement
	start := -1
	var words []string
	var trig triggerScan
	finish := func(end int) {
		if start >= 0 {
			out = append(out, sqlStatement{database.SQLSpan{Start: start, End: end}, words})
			start = -1
			words = nil
			trig = triggerScan{}
		}
	}
	for i := 0; i < len(s); {
		switch {
		case s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == '\f':
			i++
		case strings.HasPrefix(s[i:], "--"):
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case strings.HasPrefix(s[i:], "/*"):
			// SQLite block comments do not nest, and one left open runs to
			// the end of the input.
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				i = len(s)
			} else {
				i += 2 + end + 2
			}
		case s[i] == ';':
			if trig.inBody {
				i++
				continue
			}
			finish(i + 1)
			i++
		case s[i] == '\'' || s[i] == '"' || s[i] == '`' || s[i] == '[':
			if start < 0 {
				start = i
			}
			closing := s[i]
			if closing == '[' {
				closing = ']'
			}
			i++
			closed := false
			for i < len(s) {
				if s[i] == closing {
					// A doubled quote is a quote in the text; a bracket name
					// has no such escape.
					if closing != ']' && i+1 < len(s) && s[i+1] == closing {
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, sqlValidation("The SQL contains an unterminated quoted value or name. Select a complete block.")
			}
		case sqlLetter(s[i]):
			if start < 0 {
				start = i
			}
			end := i + 1
			for end < len(s) && sqlIdent(s[end]) {
				end++
			}
			word := strings.ToUpper(s[i:end])
			words = append(words, word)
			trig.word(word, words)
			i = end
		default:
			if start < 0 {
				start = i
			}
			i++
		}
	}
	finish(len(s))
	return out, nil
}

func sqlLetter(b byte) bool {
	return b == '_' || b >= 0x80 || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func sqlIdent(b byte) bool { return sqlLetter(b) || b >= '0' && b <= '9' || b == '$' }
