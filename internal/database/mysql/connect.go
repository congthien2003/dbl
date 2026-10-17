// Package mysql is the MySQL and MariaDB adapter. It connects through
// database/sql and github.com/go-sql-driver/mysql v1.10.1 (pinned in
// go.mod), which is pure Go. It does not import any UI package.
//
// The SQL tab reads the server status and affected rows the driver keeps
// unexported (see sqlSession.status). Changing the driver version requires
// running the MySQL integration tests, which check that this still works.
//
// A server holds many databases; the explorer shows each one as a schema,
// and a table reference names its database in Schema.
package mysql

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	"dbl/internal/connection"
	"dbl/internal/database"
)

// Pooled connections are recycled well before a server's wait_timeout
// (8 hours by default, often lowered) can close them under the pool.
const (
	connMaxLifetime = 5 * time.Minute
	connMaxIdleTime = time.Minute
)

// systemSchemas are the databases of the server itself; the explorer does
// not list them.
var systemSchemas = []string{"information_schema", "performance_schema", "mysql", "sys"}

// serverInfo is what the server says it is.
type serverInfo struct {
	Version      string // as VERSION() returns it
	MariaDB      bool
	Major, Minor int
}

// parseVersion reads VERSION(): "8.4.11" or "11.4.13-MariaDB-ubu2404".
func parseVersion(v string) serverInfo {
	s := serverInfo{Version: v, MariaDB: strings.Contains(strings.ToLower(v), "mariadb")}
	// A MariaDB server may announce itself as "5.5.5-10.6.x-MariaDB" to old
	// clients; the real version follows that prefix.
	v = strings.TrimPrefix(v, "5.5.5-")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) >= 2 {
		s.Major, _ = strconv.Atoi(parts[0])
		minor := parts[1]
		if i := strings.IndexFunc(minor, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
			minor = minor[:i]
		}
		s.Minor, _ = strconv.Atoi(minor)
	}
	return s
}

// Tested reports whether DBL is tested with this version: MySQL 8.0 and
// later, MariaDB 10.6 and later.
func (s serverInfo) Tested() bool {
	if s.MariaDB {
		return s.Major > 10 || s.Major == 10 && s.Minor >= 6
	}
	return s.Major >= 8
}

// Label names the server for the user: "MySQL 8.4.11".
func (s serverInfo) Label() string {
	name, v := "MySQL", s.Version
	if s.MariaDB {
		name = "MariaDB"
	}
	if i := strings.IndexByte(v, '-'); i > 0 {
		v = v[:i]
	}
	return name + " " + v
}

// Adapter is an open session with a server: a pool of connections for the
// explorer and the table tabs. Each SQL tab gets its own connection.
type Adapter struct {
	db         *sql.DB
	sessionCfg *gomysql.Config // the configuration of SQL tab connections
	server     serverInfo
	address    string // host:port, for the explorer

	engMu   sync.Mutex
	engines map[string]bool // storage engine → supports transactions

	sessMu     sync.Mutex
	sessions   map[*sqlSession]struct{}
	sessClosed bool
}

// Ping checks that the server still answers.
func (a *Adapter) Ping(ctx context.Context) error {
	if err := a.db.PingContext(ctx); err != nil {
		return classify(ctx, err, "reach the server")
	}
	return nil
}

// Close closes every connection of the pool and of the SQL tabs; it is safe
// to call twice.
func (a *Adapter) Close() error {
	a.closeSQLSessions()
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

// ServerLabel names the server and the address the profile reaches it at,
// and says when its version is not one DBL is tested with.
func (a *Adapter) ServerLabel() string {
	label := a.address + " · " + a.server.Label()
	if !a.server.Tested() {
		label += " (untested version)"
	}
	return label
}

// buildConfig returns the driver configuration of a profile. It is built as
// a value, never as a DSN string, so the password cannot reach a message.
//
// Every option that changes what a statement does is pinned: values are
// always sent as bound parameters (no interpolation), a call runs a single
// statement, times are read as the text the server prints, LOAD DATA LOCAL
// cannot read files, and no password is ever sent in clear text. An
// UPDATE reports the rows it matched, not only the rows it changed, so a
// Save that writes the value a cell already holds is not taken for a
// conflict.
func buildConfig(p connection.Profile, password string) (*gomysql.Config, error) {
	p = p.Normalized()
	host := strings.Trim(p.Host, "[]")
	if host == "" || strings.ContainsAny(host, ", /\\") {
		return nil, errors.New("host must be one host name or IP address")
	}
	t, err := tlsConfig(p.TLS, host)
	if err != nil {
		return nil, err
	}
	cfg := gomysql.NewConfig()
	cfg.User = p.Username
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(host, strconv.Itoa(p.Port))
	cfg.DBName = p.Database
	cfg.Timeout = time.Duration(p.ConnectTimeoutSec) * time.Second
	cfg.TLS = t
	cfg.ConnectionAttributes = "program_name:DBL"
	cfg.ParseTime = false
	cfg.InterpolateParams = false
	cfg.MultiStatements = false
	cfg.ClientFoundRows = true
	cfg.AllowAllFiles = false
	cfg.AllowCleartextPasswords = false
	cfg.AllowOldPasswords = false
	cfg.AllowFallbackToPlaintext = false
	cfg.AllowNativePasswords = true
	cfg.CheckConnLiveness = true
	return cfg, nil
}

// tlsConfig maps a TLS mode to the transport configuration. There is no
// "preferred": a mode that asks for TLS fails when the server has none.
//
// With TLS disabled, a caching_sha2_password login that needs the full
// exchange fetches the server's RSA key over the same plaintext connection;
// a connection without TLS is open to an attacker on the path in any case.
func tlsConfig(o connection.TLSOptions, host string) (*tls.Config, error) {
	switch o.Mode {
	case connection.TLSDisable:
		return nil, nil
	case connection.TLSRequire:
		return &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, nil
	case connection.TLSVerifyCA, connection.TLSVerifyFull:
	default:
		return nil, fmt.Errorf("unknown TLS mode %q", o.Mode)
	}
	roots, err := rootPool(o.RootCert)
	if err != nil {
		return nil, err
	}
	if o.Mode == connection.TLSVerifyFull {
		return &tls.Config{RootCAs: roots, ServerName: host, MinVersion: tls.VersionTLS12}, nil
	}
	// verify-ca: the chain must lead to a trusted root, whatever name the
	// certificate gives.
	return &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("x509: the server sent no certificate")
			}
			inter := x509.NewCertPool()
			for _, c := range cs.PeerCertificates[1:] {
				inter.AddCert(c)
			}
			_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter})
			return err
		},
	}, nil
}

// rootPool reads the CA file of the profile, or uses the system's roots
// when there is none.
func rootPool(file string) (*x509.CertPool, error) {
	if file == "" {
		return x509.SystemCertPool()
	}
	pem, err := os.ReadFile(file)
	if err != nil {
		return nil, &caError{err}
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, &caError{errors.New("the file holds no PEM certificate")}
	}
	return pool, nil
}

// caError is a problem with the CA certificate file.
type caError struct{ err error }

func (e *caError) Error() string { return "CA certificate: " + e.err.Error() }
func (e *caError) Unwrap() error { return e.err }

// Connect opens a pool for the profile and checks it by reading the
// server's version, within the profile's timeout. On any failure nothing is
// left open, and the error is a *database.Error whose text has no password.
func Connect(ctx context.Context, p connection.Profile, password string) (database.Adapter, error) {
	p = p.Normalized()
	cfg, err := buildConfig(p, password)
	if err != nil {
		return nil, connectError(err, p, password)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.ConnectTimeoutSec)*time.Second)
	defer cancel()
	connector, err := gomysql.NewConnector(cfg)
	if err != nil {
		return nil, connectError(err, p, password)
	}
	db := sql.OpenDB(connector)
	// One connection more than the profile's pool, so a KILL QUERY sent to
	// stop a SQL tab never waits behind the table tabs.
	db.SetMaxOpenConns(p.Pool.MaxOpen + 1)
	db.SetMaxIdleConns(p.Pool.MaxIdle)
	db.SetConnMaxLifetime(connMaxLifetime)
	db.SetConnMaxIdleTime(connMaxIdleTime)
	var version string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		_ = db.Close()
		if ctx.Err() != nil {
			err = errors.Join(ctx.Err(), err)
		}
		return nil, connectError(err, p, password)
	}
	sess := cfg.Clone()
	// The SQL tab reports rows changed, as the MySQL client does.
	sess.ClientFoundRows = false
	return &Adapter{
		db:         db,
		sessionCfg: sess,
		server:     parseVersion(version),
		address:    net.JoinHostPort(strings.Trim(p.Host, "[]"), strconv.Itoa(p.Port)),
	}, nil
}
