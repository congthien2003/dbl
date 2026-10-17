package mysql

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

const delimiterMessage = "DELIMITER is a command of the mysql client, not SQL. Select the whole CREATE statement, routine body included, and run the selection instead."

// SelectMySQLSQL picks the SQL to run: the one contiguous selection when
// there is one, else the statement at the cursor. It recognizes lexical
// statement boundaries, not the SQL grammar: strings, quoted names,
// comments, and the BEGIN ... END body of a stored routine, trigger or
// event, which holds semicolons. noBackslashEscapes is the server's
// NO_BACKSLASH_ESCAPES mode, under which a backslash in a string is plain.
func SelectMySQLSQL(in database.SQLInput, noBackslashEscapes bool) (database.SQLSelection, error) {
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
	statements, err := scanSQLStatements(text, noBackslashEscapes)
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

// isCompoundStart reports whether the words begin the CREATE of a stored
// program, whose body may be a BEGIN ... END block: CREATE [OR REPLACE]
// [DEFINER = ...] [AGGREGATE] PROCEDURE | FUNCTION | TRIGGER | EVENT.
func isCompoundStart(w []string) bool {
	if len(w) < 2 || w[0] != "CREATE" {
		return false
	}
	for _, x := range w[1:min(len(w), 12)] {
		switch x {
		case "PROCEDURE", "FUNCTION", "TRIGGER", "EVENT":
			return true
		case "TABLE", "VIEW", "INDEX", "DATABASE", "SCHEMA", "USER", "ROLE", "SERVER", "TABLESPACE":
			return false
		}
	}
	return false
}

// compoundScan follows the body of a stored program, where a semicolon ends
// a statement of the body, not the program. Each BEGIN and each CASE opens
// a block that an END closes; END IF, END LOOP, END WHILE and END REPEAT
// close blocks whose start need not be counted, so they are skipped. An END
// is settled when the word after it is known.
type compoundScan struct {
	on         bool // the statement creates a stored program
	depth      int
	pendingEnd bool
}

func (c *compoundScan) word(w string, words []string) {
	if !c.on && isCompoundStart(words) {
		c.on = true
	}
	if !c.on {
		return
	}
	if c.pendingEnd {
		c.pendingEnd = false
		switch w {
		case "IF", "LOOP", "WHILE", "REPEAT":
			return
		}
		c.close()
		if w == "CASE" { // END CASE closes the CASE statement it names
			return
		}
	}
	switch w {
	case "BEGIN", "CASE":
		c.depth++
	case "END":
		c.pendingEnd = true
	}
}

// boundary settles an END followed by something that is not a word.
func (c *compoundScan) boundary() {
	if c.pendingEnd {
		c.pendingEnd = false
		c.close()
	}
}

func (c *compoundScan) close() {
	if c.depth > 0 {
		c.depth--
	}
}

func scanSQLStatements(s string, noBackslashEscapes bool) ([]sqlStatement, error) {
	var out []sqlStatement
	start := -1
	var words []string
	var body compoundScan
	finish := func(end int) {
		if start >= 0 {
			out = append(out, sqlStatement{database.SQLSpan{Start: start, End: end}, words})
			start = -1
			words = nil
			body = compoundScan{}
		}
	}
	begin := func(i int) {
		if start < 0 {
			start = i
		}
	}
	for i := 0; i < len(s); {
		switch {
		case s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == '\f' || s[i] == '\v':
			i++
		case s[i] == '#' || dashComment(s, i):
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case strings.HasPrefix(s[i:], "/*"):
			// An executable comment, /*! ... */, is code for the server.
			if strings.HasPrefix(s[i:], "/*!") || strings.HasPrefix(s[i:], "/*M!") {
				begin(i)
			}
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return nil, sqlValidation("The SQL contains an unterminated comment. Select a complete block.")
			}
			i += 2 + end + 2
		case s[i] == ';':
			body.boundary()
			if body.depth > 0 {
				i++
				continue
			}
			finish(i + 1)
			i++
		case s[i] == '\'' || s[i] == '"' || s[i] == '`':
			body.boundary()
			begin(i)
			quote := s[i]
			i++
			closed := false
			for i < len(s) {
				if s[i] == '\\' && quote != '`' && !noBackslashEscapes {
					i += 2
					continue
				}
				if s[i] == quote {
					// A doubled quote is a quote in the text.
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
				return nil, sqlValidation("The SQL contains an unterminated quoted value or name. Select a complete block.")
			}
		case sqlLetter(s[i]):
			end := i + 1
			for end < len(s) && sqlIdent(s[end]) {
				end++
			}
			word := strings.ToUpper(s[i:end])
			if start < 0 && word == "DELIMITER" {
				return nil, sqlValidation(delimiterMessage)
			}
			begin(i)
			words = append(words, word)
			body.word(word, words)
			i = end
		default:
			body.boundary()
			begin(i)
			i++
		}
	}
	finish(len(s))
	return out, nil
}

// dashComment reports whether a "--" comment starts at i: MySQL needs a
// space, a control character or the end of the text after the dashes, so
// that 1--1 stays an expression.
func dashComment(s string, i int) bool {
	if !strings.HasPrefix(s[i:], "--") {
		return false
	}
	return i+2 == len(s) || s[i+2] <= ' '
}

func sqlLetter(b byte) bool {
	return b == '_' || b == '$' || b >= 0x80 || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func sqlIdent(b byte) bool { return sqlLetter(b) || b >= '0' && b <= '9' }
