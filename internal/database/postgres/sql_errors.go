package postgres

import (
	"context"
	"errors"
	"strings"

	"dbl/internal/database"
	"github.com/jackc/pgx/v5/pgconn"
)

func classifySQLExecution(ctx context.Context, err error, secret string) *database.Error {
	if err == nil {
		return nil
	}
	kind, hint := database.KindConnection, "Check the session and database state before executing again."
	var pg *pgconn.PgError
	message := database.Redact(err.Error(), secret)
	if errors.Is(err, errSQLCopyMode) {
		kind, hint = database.KindUnsupported, "Use SQL statements that do not stream through STDIN/STDOUT."
	}
	if errors.As(err, &pg) {
		message = database.Redact(pg.Message, secret)
		switch {
		case pg.Code == "42501":
			kind, hint = database.KindPermission, "Check your database privileges."
		case pg.Code == "57014":
			kind, hint = database.KindCanceled, "Check the transaction state before continuing."
			if strings.Contains(pg.Message, "statement timeout") {
				kind = database.KindTimeout
			}
		case strings.HasPrefix(pg.Code, "40"):
			kind, hint = database.KindConflict, "The transaction was interrupted by a concurrency conflict. Check its state before continuing."
		case pg.Code == "0A000":
			kind, hint = database.KindUnsupported, "The server does not support this SQL operation."
		case strings.HasPrefix(pg.Code, "25"):
			kind, hint = database.KindValidation, "The transaction may need ROLLBACK."
		case strings.HasPrefix(pg.Code, "22") || strings.HasPrefix(pg.Code, "23"):
			kind, hint = database.KindValidation, "Check the values and constraints."
		case strings.HasPrefix(pg.Code, "42"):
			kind, hint = database.KindSyntax, "Check the SQL statement and referenced objects."
		case strings.HasPrefix(pg.Code, "28"):
			kind, hint = database.KindAuthentication, "Check the connection credentials."
		}
	}
	if ctx != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			kind, message = database.KindCanceled, "The query was canceled."
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			kind, message = database.KindTimeout, "The query timed out."
		}
	}
	return &database.Error{Kind: kind, Message: boundedSQLText(message, database.SQLNoticeBytes), Hint: hint, Err: err}
}
