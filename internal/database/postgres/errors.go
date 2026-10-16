package postgres

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"dbl/internal/connection"
	"dbl/internal/database"
)

// classify turns an error from pgx into a *database.Error with a category,
// a message for the user and, where one helps, a hint. The text never holds
// the password.
func classify(err error, p connection.Profile, password string) *database.Error {
	if err == nil {
		return nil
	}
	var existing *database.Error
	if errors.As(err, &existing) {
		return existing
	}
	target := net.JoinHostPort(strings.Trim(p.Host, "[]"), fmt.Sprint(p.Port))
	raw := database.Redact(err.Error(), password)
	fail := func(kind database.Kind, msg, hint string) *database.Error {
		return &database.Error{Kind: kind, Message: msg, Hint: hint, Err: err}
	}

	var pg *pgconn.PgError
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return fail(database.KindCanceled, "The connection attempt was canceled.", "")

	case errors.As(err, &pg):
		server := database.Redact(pg.Message, password)
		switch {
		case strings.HasPrefix(pg.Code, "28"): // invalid authorization (28P01, 28000, ...)
			return fail(database.KindAuthentication,
				fmt.Sprintf("Authentication failed for user %q: %s", p.Username, server),
				"Check the username and password.")
		case pg.Code == "42501":
			return fail(database.KindPermission, "Permission denied: "+server, "")
		case pg.Code == "3D000":
			return fail(database.KindConnection,
				fmt.Sprintf("Database %q does not exist on %s.", p.Database, target),
				"Check the database name.")
		}
		return fail(database.KindConnection, fmt.Sprintf("The server at %s refused the connection: %s", target, server), "")

	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return fail(database.KindTimeout,
			fmt.Sprintf("Connecting to %s timed out after %d s.", target, p.Normalized().ConnectTimeoutSec),
			"Check the host and port, and that nothing blocks the connection.")

	case strings.Contains(raw, "server refused TLS connection"):
		return fail(database.KindConnection,
			fmt.Sprintf("The server at %s does not support TLS.", target),
			"Choose TLS mode \"disable\" for this connection, or enable TLS on the server.")

	case strings.Contains(raw, "tls error") || strings.Contains(raw, "tls:") || strings.Contains(raw, "x509:"):
		return fail(database.KindConnection,
			fmt.Sprintf("The TLS handshake with %s failed: %s", target, raw),
			"Check the TLS mode and the CA certificate.")

	case errors.Is(err, fs.ErrNotExist) || strings.Contains(raw, "unable to read CA file") || strings.Contains(raw, "unable to add CA"):
		return fail(database.KindFileAccess, "The CA certificate could not be used: "+raw,
			"Check the path and that the file is a PEM certificate.")
	}
	return fail(database.KindConnection, fmt.Sprintf("Could not connect to %s: %s", target, raw), "")
}
