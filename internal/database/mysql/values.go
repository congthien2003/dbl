package mysql

import (
	"database/sql"
	"strings"

	"dbl/internal/database"
	"dbl/internal/grid"
)

// columnType is what classOf reads of a column: its classification, the
// width of an integer and whether it is unsigned.
type columnType struct {
	Class    database.ColumnClass
	IntBits  int
	Unsigned bool
}

// classOf classifies a column from information_schema: dataType is
// DATA_TYPE ("int"), full is COLUMN_TYPE ("int(10) unsigned").
//
// TINYINT(1) stays a number: MySQL has no boolean type, and the column may
// hold any value from -128 to 127. BIT is binary. A spatial or vector value
// is binary too: it has no text form worth showing.
func classOf(dataType, full string) columnType {
	t := strings.ToLower(strings.TrimSpace(dataType))
	unsigned := strings.Contains(strings.ToLower(full), "unsigned")
	switch t {
	case "tinyint":
		return columnType{database.ClassNumber, 8, unsigned}
	case "smallint":
		return columnType{database.ClassNumber, 16, unsigned}
	case "mediumint":
		return columnType{database.ClassNumber, 24, unsigned}
	case "int", "integer":
		return columnType{database.ClassNumber, 32, unsigned}
	case "bigint":
		return columnType{database.ClassNumber, 64, unsigned}
	case "decimal", "numeric", "float", "double", "real", "double precision", "year":
		return columnType{Class: database.ClassNumber, Unsigned: unsigned}
	case "char", "varchar", "tinytext", "text", "mediumtext", "longtext", "enum", "set":
		return columnType{Class: database.ClassText}
	case "date", "datetime", "timestamp", "time":
		return columnType{Class: database.ClassDateTime}
	case "json":
		return columnType{Class: database.ClassJSON}
	case "binary", "varbinary", "tinyblob", "blob", "mediumblob", "longblob", "bit",
		"geometry", "point", "linestring", "polygon", "multipoint", "multilinestring",
		"multipolygon", "geometrycollection", "geomcollection", "vector":
		return columnType{Class: database.ClassBinary}
	case "uuid", "inet4", "inet6":
		return columnType{Class: database.ClassOrdered}
	}
	return columnType{Class: database.ClassOther}
}

// generatedExtra reports whether the EXTRA of a column says the server
// computes it. DEFAULT_GENERATED (a default that is an expression) and
// "on update CURRENT_TIMESTAMP" are not generated: the column can be
// written.
func generatedExtra(extra string) bool {
	e := strings.ToUpper(extra)
	return strings.Contains(e, "VIRTUAL GENERATED") || strings.Contains(e, "STORED GENERATED") ||
		strings.Contains(e, "PERSISTENT GENERATED") || e == "VIRTUAL" || e == "PERSISTENT"
}

// toValue converts what a page read for a column into a grid value. Every
// column that is not binary is read as CAST(col AS CHAR), so the text is the
// server's own rendering of the value, whichever protocol carried it: the
// same text the SQL tab shows, and the text Save compares against.
func toValue(class database.ColumnClass, v sql.NullString) grid.Value {
	if !v.Valid {
		return grid.Value{Kind: grid.KindNull}
	}
	return grid.Value{Kind: class.GridKind(), Text: v.String}
}
