package sqlite

import (
	"context"
	"errors"
	"io/fs"
	"strings"

	"github.com/ncruces/go-sqlite3"

	"dbl/internal/database"
)

// classify turns an error from the driver into a *database.Error with a
// category and a message for the user. doing names what was attempted, for
// the generic message ("open the database"). A SQLite message names no
// secret: the adapter has no password.
func classify(ctx context.Context, err error, doing string) *database.Error {
	if err == nil {
		return nil
	}
	var existing *database.Error
	if errors.As(err, &existing) {
		return existing
	}
	fail := func(kind database.Kind, msg, hint string) *database.Error {
		return &database.Error{Kind: kind, Message: msg, Hint: hint, Err: err}
	}
	// A canceled or expired ctx interrupts SQLite, which then reports
	// SQLITE_INTERRUPT; the ctx says why.
	cause := err
	if ctx != nil && ctx.Err() != nil {
		cause = ctx.Err()
	}
	switch {
	case errors.Is(cause, context.Canceled):
		return fail(database.KindCanceled, "The operation was canceled.", "")
	case errors.Is(cause, context.DeadlineExceeded):
		return fail(database.KindTimeout, "The operation timed out.", "")
	case errors.Is(err, fs.ErrNotExist):
		return fail(database.KindFileAccess, "File not found", "Check the path of the database file.")
	}
	// The driver returns a *sqlite3.Error, or a bare ErrorCode when SQLite
	// gives no message; both answer errors.As for an ErrorCode.
	var code sqlite3.ErrorCode
	if errors.As(err, &code) {
		msg := statementMessage(err)
		switch code {
		case sqlite3.BUSY, sqlite3.LOCKED:
			return fail(database.KindConnection, "Database is locked by another process",
				"Close the other program that uses the file, or wait for it to finish, then try again.")
		case sqlite3.NOTADB:
			return fail(database.KindConnection, "Not a SQLite database file", "Choose a SQLite database file.")
		case sqlite3.CORRUPT:
			return fail(database.KindConnection, "The database file is damaged", "")
		case sqlite3.CANTOPEN, sqlite3.PERM, sqlite3.AUTH:
			return fail(database.KindPermission, "The database file cannot be opened. Check that you may read it.", "")
		case sqlite3.READONLY:
			return fail(database.KindPermission, "The database is read-only", "")
		case sqlite3.INTERRUPT:
			return fail(database.KindCanceled, "The operation was canceled.", "")
		case sqlite3.CONSTRAINT:
			return fail(database.KindValidation, "Constraint failed: "+msg, "")
		case sqlite3.ERROR:
			// Any statement error (syntax, an unknown table or column)
			// is the statement's fault, not the connection's.
			return fail(database.KindSyntax, msg, "")
		}
	}
	return fail(database.KindConnection, "Could not "+doing+": "+err.Error(), "")
}

// statementMessage is the text of a SQLite error without the driver's
// prefixes: "near \"x\": syntax error", not "sqlite3: SQL logic error: ...".
func statementMessage(err error) string {
	msg := strings.TrimPrefix(err.Error(), "sqlite3: ")
	return strings.TrimPrefix(msg, "SQL logic error: ")
}
