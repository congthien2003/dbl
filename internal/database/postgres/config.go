// Package postgres connects to PostgreSQL through pgx's database/sql driver.
// It does not import any UI package.
package postgres

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"dbl/internal/connection"
)

// libpqEnv lists the environment variables pgx's ParseConfig reads
// (parseEnvSettings in pgconn/config.go, pgx v5.11.0). DBL takes every
// connection setting from the profile, so none of them may reach a
// connection: a stray PGSSLMODE or PGPASSWORD would silently change
// security. IgnoreLibpqEnvironment removes them; if pgx adds a variable on
// upgrade, add it here.
var libpqEnv = []string{
	"PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGPASSFILE",
	"PGAPPNAME", "PGCONNECT_TIMEOUT", "PGSSLMODE", "PGSSLKEY", "PGSSLCERT",
	"PGSSLSNI", "PGSSLROOTCERT", "PGSSLPASSWORD", "PGSSLNEGOTIATION",
	"PGTARGETSESSIONATTRS", "PGSERVICE", "PGSERVICEFILE", "PGTZ", "PGOPTIONS",
	"PGMINPROTOCOLVERSION", "PGMAXPROTOCOLVERSION", "PGCHANNELBINDING",
	"PGREQUIREAUTH",
}

// IgnoreLibpqEnvironment removes the libpq environment variables from the
// process. Call it once, first thing in main, before any other goroutine
// starts. A PGSERVICE variable cannot be neutralised any other way: an
// empty service key still makes pgx read the service file.
func IgnoreLibpqEnvironment() {
	for _, k := range libpqEnv {
		_ = os.Unsetenv(k)
	}
}

// applicationName is shown by the server in pg_stat_activity.
const applicationName = "DBL"

// buildConfig returns the pgx configuration of a profile.
//
// pgx only accepts a configuration made by ParseConfig, and ParseConfig
// merges libpq's defaults: the environment, ~/.pgpass, the service file,
// ~/.postgresql/postgresql.{crt,key} and root.crt, and sslmode=prefer (a
// silent fall back to plaintext). So the connection URL pins every setting
// that matters, including an empty client certificate and key (which
// override the files under the home directory), and the password never goes
// into the URL: it is set on the parsed configuration, replacing anything
// ParseConfig found in ~/.pgpass.
func buildConfig(p connection.Profile, password string) (*pgx.ConnConfig, error) {
	p = p.Normalized()
	host := strings.Trim(p.Host, "[]")
	if host == "" || strings.ContainsAny(host, ", /\\") {
		return nil, errors.New("host must be one host name or IP address")
	}
	if !p.TLS.Mode.Valid() {
		return nil, fmt.Errorf("unknown TLS mode %q", p.TLS.Mode)
	}
	rootCert := ""
	if p.TLS.RootCert != "" {
		if !p.TLS.Mode.Verifies() {
			return nil, errors.New("a CA certificate is used only with verify-ca or verify-full")
		}
		// An absolute path also keeps the file from being read as pgx's
		// special value "system".
		abs, err := filepath.Abs(p.TLS.RootCert)
		if err != nil {
			return nil, err
		}
		rootCert = abs
	}
	q := url.Values{}
	q.Set("sslmode", string(p.TLS.Mode))
	q.Set("sslrootcert", rootCert)
	q.Set("sslcert", "")
	q.Set("sslkey", "")
	q.Set("sslpassword", "")
	q.Set("sslnegotiation", "postgres")
	q.Set("connect_timeout", strconv.Itoa(p.ConnectTimeoutSec))
	q.Set("target_session_attrs", "any")
	q.Set("channel_binding", "prefer")
	q.Set("min_protocol_version", "3.0")
	q.Set("max_protocol_version", "3.0")
	q.Set("require_auth", "")
	u := url.URL{
		Scheme:   "postgres",
		User:     url.User(p.Username),
		Host:     net.JoinHostPort(host, strconv.Itoa(p.Port)),
		Path:     "/" + p.Database,
		RawQuery: q.Encode(),
	}
	cfg, err := pgx.ParseConfig(u.String())
	if err != nil {
		return nil, err
	}
	if len(cfg.Fallbacks) != 0 {
		return nil, errors.New("internal error: the connection has fallback hosts")
	}
	cfg.Password = password
	cfg.RuntimeParams["application_name"] = applicationName
	return cfg, nil
}
