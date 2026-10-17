package postgres

import (
	"encoding/hex"
	"strconv"
	"unicode/utf8"

	"dbl/internal/database"
	"dbl/internal/grid"
)

func sqlColumn(name string, oid uint32) database.SQLColumn {
	c := database.SQLColumn{Name: name, TypeOID: oid, TypeName: "OID " + strconv.FormatUint(uint64(oid), 10), Kind: grid.KindText}
	switch oid {
	case 16:
		c.Kind, c.TypeName = grid.KindBool, "boolean"
	case 17:
		c.Kind, c.TypeName = grid.KindBinary, "bytea"
	case 20, 21, 23, 26, 700, 701, 1700:
		c.Kind, c.TypeName = grid.KindNumber, "numeric"
	case 1082, 1083, 1114, 1184, 1186, 1266:
		c.Kind, c.TypeName = grid.KindDateTime, "date/time"
	case 114, 3802:
		c.Kind, c.TypeName = grid.KindJSON, "json"
	case 18, 19, 25, 1042, 1043:
		c.TypeName = "text"
	case 2950:
		c.TypeName = "uuid"
	}
	return c
}
func sqlValue(raw []byte, col database.SQLColumn) grid.Value {
	if raw == nil {
		return grid.Value{Kind: grid.KindNull}
	}
	if col.Kind == grid.KindBinary {
		size := len(raw)
		if len(raw) >= 2 && raw[0] == '\\' && raw[1] == 'x' {
			size = hex.DecodedLen(len(raw) - 2)
		} else {
			size = 0
			for i := 0; i < len(raw); i++ {
				size++
				if raw[i] == '\\' && i+1 < len(raw) {
					if raw[i+1] == '\\' {
						i++
					} else if i+3 < len(raw) {
						i += 3
					}
				}
			}
		}
		return grid.Value{Kind: grid.KindBinary, Size: size}
	}
	return grid.Value{Kind: col.Kind, Text: string(raw)}
}
func boundedSQLText(text string, size int) string {
	if len(text) <= size {
		return text
	}
	end := size - 3
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + "…"
}
