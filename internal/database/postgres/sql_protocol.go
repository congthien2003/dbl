package postgres

import (
	"encoding/binary"
	"errors"
	"io"
	"sync/atomic"

	"dbl/internal/database"
)

var errSQLResultLimit = errors.New("SQL result read limit reached")
var errSQLCopyMode = errors.New("COPY streaming mode is not supported by the SQL editor")

// sqlProtocolReader preserves frame boundaries and applies budgets even when
// pgconn drains rows or handles notices internally. It never buffers a body.
type sqlProtocolReader struct {
	source                         io.Reader
	header                         [5]byte
	headerAt, headerEnd, remaining int
	bytes, rows                    int64
	active, limited                atomic.Bool
}

func (r *sqlProtocolReader) begin() {
	r.bytes, r.rows = 0, 0
	r.limited.Store(false)
	r.active.Store(true)
}
func (r *sqlProtocolReader) end() { r.active.Store(false) }
func (r *sqlProtocolReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.limited.Load() {
		return 0, errSQLResultLimit
	}
	if r.headerAt < r.headerEnd {
		n := copy(p, r.header[r.headerAt:r.headerEnd])
		r.headerAt += n
		return n, nil
	}
	if r.remaining > 0 {
		n, err := r.source.Read(p[:min(len(p), r.remaining)])
		r.remaining -= n
		return n, err
	}
	if _, err := io.ReadFull(r.source, r.header[:]); err != nil {
		return 0, err
	}
	length := int64(binary.BigEndian.Uint32(r.header[1:])) - 4
	if length < 0 {
		return 0, errors.New("invalid PostgreSQL message length")
	}
	if length > database.SQLMessageBytes {
		r.limited.Store(true)
		return 0, errSQLResultLimit
	}
	if r.active.Load() {
		if r.header[0] == 'G' || r.header[0] == 'H' || r.header[0] == 'W' {
			return 0, errSQLCopyMode
		}
		r.bytes += length
		if r.header[0] == 'D' {
			r.rows++
		}
		if r.bytes > database.SQLReadBytes || r.rows > database.SQLReadRows {
			r.limited.Store(true)
			return 0, errSQLResultLimit
		}
	}
	r.remaining = int(length)
	r.headerAt, r.headerEnd = 0, 5
	n := copy(p, r.header[:])
	r.headerAt = n
	return n, nil
}
