package postgres

import (
	"strings"
	"unicode/utf8"

	"dbl/internal/database"
)

type sqlStatement struct {
	span  database.SQLSpan
	words []string
}

func sqlValidation(message string) error {
	return &database.Error{Kind: database.KindValidation, Message: message}
}
func sqlUnsupported(message string) error {
	return &database.Error{Kind: database.KindUnsupported, Message: message}
}

// SelectPostgresSQL recognizes lexical statement boundaries, not the SQL grammar.
func SelectPostgresSQL(in database.SQLInput, standardStrings bool) (database.SQLSelection, error) {
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
	text, base := in.Text, 0
	if selected != nil {
		text, base = in.Text[selected.Start:selected.End], selected.Start
	}
	statements, err := scanSQLStatements(text, standardStrings)
	if err != nil {
		return database.SQLSelection{}, err
	}
	if len(statements) == 0 {
		return database.SQLSelection{}, nil
	}
	for _, stmt := range statements {
		w := stmt.words
		if len(w) > 0 && w[0] == "COPY" {
			for i := 1; i < len(w); i++ {
				if (w[i-1] == "FROM" && w[i] == "STDIN") || (w[i-1] == "TO" && w[i] == "STDOUT") {
					return database.SQLSelection{}, sqlUnsupported("COPY through STDIN/STDOUT is not supported by this SQL editor.")
				}
			}
		}
		if selected == nil {
			for i := 1; i < len(w); i++ {
				if w[i-1] == "BEGIN" && w[i] == "ATOMIC" {
					return database.SQLSelection{}, sqlUnsupported("Select the entire SQL-standard BEGIN ATOMIC body to execute it.")
				}
			}
		}
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
	chosen.Start += base
	chosen.End += base
	return database.SQLSelection{SQL: in.Text[chosen.Start:chosen.End], Span: chosen}, nil
}

func scanSQLStatements(s string, standard bool) ([]sqlStatement, error) {
	var out []sqlStatement
	start := -1
	var words []string
	finish := func(end int) {
		if start >= 0 {
			out = append(out, sqlStatement{database.SQLSpan{Start: start, End: end}, words})
			start = -1
			words = nil
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
			i += 2
			depth := 1
			for i < len(s) && depth > 0 {
				if strings.HasPrefix(s[i:], "/*") {
					depth++
					i += 2
				} else if strings.HasPrefix(s[i:], "*/") {
					depth--
					i += 2
				} else {
					i++
				}
			}
			if depth > 0 {
				return nil, sqlValidation("The SQL contains an unterminated block comment. Select a complete block.")
			}
		case s[i] == ';':
			finish(i + 1)
			i++
		case s[i] == '\\':
			return nil, sqlUnsupported("psql commands are not supported. Execute SQL statements directly.")
		case s[i] == '\'' || s[i] == '"':
			if start < 0 {
				start = i
			}
			quote := s[i]
			escape := quote == '\'' && (!standard || (i > 0 && (s[i-1] == 'E' || s[i-1] == 'e') && (i < 2 || !sqlIdent(s[i-2]))))
			i++
			closed := false
			for i < len(s) {
				if escape && s[i] == '\\' {
					i = min(i+2, len(s))
					continue
				}
				if s[i] == quote {
					if i+1 < len(s) && s[i+1] == quote {
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
				return nil, sqlValidation("The SQL contains an unterminated quoted value. Select a complete block.")
			}
		case s[i] == '$' && (i == 0 || !sqlIdent(s[i-1])):
			if start < 0 {
				start = i
			}
			end := i + 1
			for end < len(s) && (sqlLetter(s[end]) || (end > i+1 && s[end] >= '0' && s[end] <= '9')) {
				end++
			}
			if end < len(s) && s[end] == '$' {
				tag := s[i : end+1]
				close := strings.Index(s[end+1:], tag)
				if close < 0 {
					return nil, sqlValidation("The SQL contains an unterminated dollar-quoted body.")
				}
				i = end + 1 + close + len(tag)
			} else {
				i++
			}
		case sqlLetter(s[i]):
			if start < 0 {
				start = i
			}
			end := i + 1
			for end < len(s) && sqlIdent(s[end]) {
				end++
			}
			words = append(words, strings.ToUpper(s[i:end]))
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
