package mysql

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"strings"

	gomysql "github.com/go-sql-driver/mysql"

	"dbl/internal/connection"
	"dbl/internal/database"
)

// Server error numbers DBL tells apart
// (https://dev.mysql.com/doc/mysql-errors/8.4/en/server-error-reference.html).
const (
	erDBAccessDenied        = 1044
	erAccessDenied          = 1045
	erBadDB                 = 1049
	erNoDBSelected          = 1046
	erTableAccessDenied     = 1142
	erColumnAccessDenied    = 1143
	erProcAccessDenied      = 1370
	erSpecificAccessDenied  = 1227
	erLockWaitTimeout       = 1205
	erLockDeadlock          = 1213
	erQueryInterrupted      = 1317
	erQueryTimeout          = 3024 // max_execution_time
	erMariaDBQueryTimeout   = 1969 // max_statement_time
	erServerShutdown        = 1053
	erDupEntry              = 1062
	erBadNull               = 1048
	erRowIsReferenced       = 1451
	erNoReferencedRow       = 1452
	erDataTooLong           = 1406
	erWarnDataOutOfRange    = 1264
	erTruncatedWrongValue   = 1292
	erTruncatedWrongField   = 1366
	erCheckConstraint       = 3819
	erMariaDBCheckViolation = 4025
)

// constraintErrors are the numbers of a value the server refused.
var constraintErrors = map[uint16]bool{
	erDupEntry: true, erBadNull: true, erRowIsReferenced: true, erNoReferencedRow: true,
	erDataTooLong: true, erWarnDataOutOfRange: true, erTruncatedWrongValue: true,
	erTruncatedWrongField: true, erCheckConstraint: true, erMariaDBCheckViolation: true,
	1263: true, 1265: true, 1364: true,
}

// connectionLost reports whether err means the connection itself failed,
// so the state of what was sent on it is not known.
func connectionLost(err error) bool {
	var me *gomysql.MySQLError
	if errors.As(err, &me) {
		return false
	}
	var netErr net.Error
	return errors.Is(err, driver.ErrBadConn) || errors.Is(err, gomysql.ErrInvalidConn) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &netErr)
}

// serverMessage is the server's text for an error, without the driver's
// "Error 1064 (42000): " prefix.
func serverMessage(err error) string {
	var me *gomysql.MySQLError
	if errors.As(err, &me) {
		return me.Message
	}
	return err.Error()
}

// classify turns an error of an operation on an open connection into a
// *database.Error. doing names what was attempted ("read the page"). The
// server's messages name objects and users, never a password.
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
	cause := err
	if ctx != nil && ctx.Err() != nil {
		cause = ctx.Err()
	}
	switch {
	case errors.Is(cause, context.Canceled):
		return fail(database.KindCanceled, "The operation was canceled.", "")
	case errors.Is(cause, context.DeadlineExceeded):
		return fail(database.KindTimeout, "The operation timed out.", "")
	}
	var me *gomysql.MySQLError
	if errors.As(err, &me) {
		msg := me.Message
		switch {
		case me.Number == erAccessDenied:
			return fail(database.KindAuthentication, msg, "Check the username and password.")
		case me.Number == erDBAccessDenied || me.Number == erTableAccessDenied || me.Number == erColumnAccessDenied ||
			me.Number == erProcAccessDenied || me.Number == erSpecificAccessDenied:
			return fail(database.KindPermission, "Permission denied: "+msg, "")
		case me.Number == erNoDBSelected:
			return fail(database.KindValidation, msg,
				"Name the database with the table (`db`.`table`), run USE db first, or set a default database in the connection.")
		case me.Number == erLockDeadlock:
			return fail(database.KindConflict, "Deadlock: "+msg, "The server rolled back the transaction.")
		case me.Number == erLockWaitTimeout:
			return fail(database.KindConflict, "Another transaction holds a lock on the rows: "+msg, "Try again when the other transaction ends.")
		case me.Number == erQueryInterrupted:
			return fail(database.KindCanceled, "The statement was canceled.", "")
		case me.Number == erQueryTimeout || me.Number == erMariaDBQueryTimeout:
			return fail(database.KindTimeout, "The statement ran longer than the server allows: "+msg, "")
		case me.Number == erServerShutdown:
			return fail(database.KindConnection, "The server is shutting down.", "")
		case constraintErrors[me.Number]:
			return fail(database.KindValidation, msg, "")
		}
		// Any other server error (syntax, an unknown table or column) is
		// the statement's fault, not the connection's.
		return fail(database.KindSyntax, msg, "")
	}
	if connectionLost(err) {
		return fail(database.KindConnection, "The connection to the server was lost.", "Reconnect and try again.")
	}
	return fail(database.KindConnection, "Could not "+doing+": "+err.Error(), "")
}

// connectError turns an error of a connection attempt into a
// *database.Error. The text never holds the password.
func connectError(err error, p connection.Profile, password string) *database.Error {
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
	var me *gomysql.MySQLError
	var netErr net.Error
	var ca *caError
	switch {
	case errors.Is(err, context.Canceled):
		return fail(database.KindCanceled, "The connection attempt was canceled.", "")
	case errors.As(err, &ca):
		if errors.Is(err, fs.ErrNotExist) {
			return fail(database.KindFileAccess, "The CA certificate file was not found.", "Check the path of the certificate.")
		}
		return fail(database.KindFileAccess, "The CA certificate could not be used: "+database.Redact(ca.err.Error(), password),
			"Check that the file is a PEM certificate.")
	case errors.As(err, &me):
		server := database.Redact(me.Message, password)
		switch me.Number {
		case erAccessDenied:
			return fail(database.KindAuthentication,
				fmt.Sprintf("Authentication failed for user %q: %s", p.Username, server),
				"Check the username and password.")
		case erDBAccessDenied:
			return fail(database.KindPermission, "Permission denied: "+server, "Check the database name, or leave it empty.")
		case erBadDB:
			return fail(database.KindConnection,
				fmt.Sprintf("Database %q does not exist on %s.", p.Database, target),
				"Check the database name, or leave it empty.")
		}
		return fail(database.KindConnection, fmt.Sprintf("The server at %s refused the connection: %s", target, server), "")
	case errors.Is(err, gomysql.ErrNoTLS):
		return fail(database.KindConnection,
			fmt.Sprintf("The server at %s does not support TLS.", target),
			"Choose TLS mode \"disable\" for this connection, or enable TLS on the server.")
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return fail(database.KindTimeout,
			fmt.Sprintf("Connecting to %s timed out after %d s.", target, p.Normalized().ConnectTimeoutSec),
			"Check the host and port, and that nothing blocks the connection.")
	case strings.Contains(raw, "tls:") || strings.Contains(raw, "x509:"):
		return fail(database.KindConnection,
			fmt.Sprintf("The TLS handshake with %s failed: %s", target, raw),
			"Check the TLS mode and the CA certificate.")
	}
	return fail(database.KindConnection, fmt.Sprintf("Could not connect to %s: %s", target, raw), "")
}
