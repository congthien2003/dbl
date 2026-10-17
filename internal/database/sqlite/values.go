package sqlite

import (
	"strconv"
	"strings"
	"time"

	"dbl/internal/database"
	"dbl/internal/grid"
)

// classOf classifies a column from its declared type the way SQLite does
// for affinity (https://sqlite.org/datatype3.html), with the names SQLite
// gives no affinity of its own (BOOLEAN, DATE, JSON) told apart first. The
// second result is the width of an integer column, 0 for other columns.
// A declared type cannot say what a cell holds: SQLite stores any value in
// any column, and the values keep their own storage class.
func classOf(declared string) (database.ColumnClass, int) {
	t := strings.ToUpper(strings.TrimSpace(declared))
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(t, s) {
				return true
			}
		}
		return false
	}
	switch {
	case t == "":
		return database.ClassOther, 0
	case has("JSON"):
		return database.ClassJSON, 0
	case has("BOOL"):
		// NUMERIC affinity: a boolean is stored as 0 or 1.
		return database.ClassNumber, 0
	case has("DATE", "TIME"):
		return database.ClassDateTime, 0
	case has("INT"):
		return database.ClassNumber, 64
	case has("CHAR", "CLOB", "TEXT"):
		return database.ClassText, 0
	case has("BLOB"):
		return database.ClassBinary, 0
	case has("REAL", "FLOA", "DOUB", "NUMERIC", "DECIMAL"):
		return database.ClassNumber, 0
	}
	return database.ClassOther, 0
}

// toValue converts a value the driver scanned into a grid value, keeping
// its storage class: an integer, a real, text and a blob are different
// values even when they print alike, so text kept in a numeric column stays
// text, and a number is never turned into a string to be compared.
func toValue(class database.ColumnClass, v any) grid.Value {
	switch x := v.(type) {
	case nil:
		return grid.Value{Kind: grid.KindNull}
	case int64:
		return grid.Value{Kind: grid.KindNumber, Text: strconv.FormatInt(x, 10)}
	case float64:
		return grid.Value{Kind: grid.KindNumber, Text: formatReal(x)}
	case bool:
		// The driver reads a BOOLEAN column's 0 and 1 as booleans.
		if x {
			return grid.Value{Kind: grid.KindNumber, Text: "1"}
		}
		return grid.Value{Kind: grid.KindNumber, Text: "0"}
	case []byte:
		return grid.Value{Kind: grid.KindBinary, Size: len(x)}
	case time.Time:
		return grid.Value{Kind: grid.KindDateTime, Text: x.Format(time.RFC3339Nano)}
	case string:
		switch class {
		case database.ClassDateTime:
			return grid.Value{Kind: grid.KindDateTime, Text: x}
		case database.ClassJSON:
			return grid.Value{Kind: grid.KindJSON, Text: x}
		}
		return grid.Value{Kind: grid.KindText, Text: x}
	}
	return grid.Value{Kind: grid.KindText, Text: ""}
}

// formatReal prints a REAL so that it keeps looking like one: 2 is "2.0".
func formatReal(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eEIN") { // not an exponent, Inf or NaN
		s += ".0"
	}
	return s
}
