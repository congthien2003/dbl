package postgres

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"dbl/internal/database"
)

func TestSQLExecuteBoundedRead(t *testing.T) {
	var header [5]byte
	header[0] = 'D'
	binary.BigEndian.PutUint32(header[1:], database.SQLMessageBytes+5)
	m := &sqlProtocolReader{source: bytes.NewReader(header[:])}
	m.begin()
	_, err := io.ReadAll(m)
	if !errors.Is(err, errSQLResultLimit) {
		t.Fatalf("oversize header: %v", err)
	}
}

func TestSQLProtocolAggregateBudgetsIncludeDiscardedMessages(t *testing.T) {
	for _, kind := range []byte{'N', 'A', 'D'} {
		var frame [6]byte
		frame[0] = kind
		binary.BigEndian.PutUint32(frame[1:5], 5)
		reader := &sqlProtocolReader{source: bytes.NewReader(frame[:])}
		reader.begin()
		reader.bytes = database.SQLReadBytes
		if _, err := io.ReadAll(reader); !errors.Is(err, errSQLResultLimit) {
			t.Fatalf("message %q bypassed payload budget: %v", kind, err)
		}
	}
}
