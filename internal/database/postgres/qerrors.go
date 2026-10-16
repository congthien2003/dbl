package postgres

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"dbl/internal/database"
)

// classifyQuery turns an error from a query or a metadata read into a
// *database.Error. what says what was being done ("Reading the page"); the
// text of the server's own message is kept because it names the object and
// the reason, and it never holds a password: the session was authenticated
// before any query ran.
//
// ctx is the context the query ran with: when it ended, that is the reason,
// whatever shape the driver gave the error.
func classifyQuery(ctx context.Context, err error, what string) *database.Error {
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
	if ctx != nil {
		switch ctx.Err() {
		case context.Canceled:
			return fail(database.KindCanceled, what+" was canceled.", "")
		case context.DeadlineExceeded:
			return fail(database.KindTimeout, what+" timed out.", "Try a narrower filter, or try again.")
		}
	}
	switch {
	case errors.Is(err, context.Canceled):
		return fail(database.KindCanceled, what+" was canceled.", "")
	case errors.Is(err, context.DeadlineExceeded):
		return fail(database.KindTimeout, what+" timed out.", "Try a narrower filter, or try again.")
	}

	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		msg := pg.Message
		switch {
		case pg.Code == "57014": // query_canceled: statement_timeout or a cancel request
			return fail(database.KindTimeout, what+" was stopped by the server: "+msg, "")
		case pg.Code == "42501":
			return fail(database.KindPermission, "Permission denied: "+msg, "Your user may not read this object.")
		case pg.Code == "42P01" || pg.Code == "42703" || pg.Code == "3F000" || pg.Code == "42P02":
			return fail(database.KindMetadataUnavailable, "The table or a column no longer matches what was read: "+msg,
				"Refresh the node in the explorer, then reload the table.")
		case pg.Code == "42883" || pg.Code == "0A000" || pg.Code == "42846":
			return fail(database.KindUnsupported, "The server cannot do this for the column: "+msg, "")
		case strings.HasPrefix(pg.Code, "22"): // data exception: the value does not fit the column's type
			return fail(database.KindValidation, "The value is not valid for the column: "+msg, "")
		case strings.HasPrefix(pg.Code, "28"):
			return fail(database.KindAuthentication, "Authentication failed: "+msg, "")
		case strings.HasPrefix(pg.Code, "42"):
			return fail(database.KindSyntax, "The server rejected the query: "+msg, "")
		case strings.HasPrefix(pg.Code, "08") || strings.HasPrefix(pg.Code, "53") ||
			strings.HasPrefix(pg.Code, "57") || strings.HasPrefix(pg.Code, "58") || strings.HasPrefix(pg.Code, "XX"):
			return fail(database.KindConnection, what+" failed: "+msg, "")
		}
		return fail(database.KindSyntax, what+" failed: "+msg, "")
	}

	var netErr net.Error
	switch {
	case errors.Is(err, sql.ErrConnDone) || strings.Contains(err.Error(), "database is closed"):
		return fail(database.KindConnection, "The connection was closed.", "Connect again from the explorer.")
	case errors.As(err, &netErr) && netErr.Timeout():
		return fail(database.KindTimeout, what+" timed out.", "")
	}
	return fail(database.KindConnection, what+" failed: "+err.Error(), "Check that the server is still reachable.")
}
