package postgres

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dbl/internal/connection"
)

func profile() connection.Profile {
	return connection.Profile{
		ID: "p1", Name: "T", Driver: connection.DriverPostgres, Host: "db.example.com", Port: 6543,
		Database: "appdb", Username: "alice",
	}.Normalized()
}

// writeCA writes a self-signed CA certificate and returns its path.
func writeCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// hostileHome points the home directory at files libpq would read: a
// ~/.pgpass with a password, and client and root certificates that cannot
// be parsed. If any of them were read, buildConfig would fail or use them.
func hostileHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", home)
	for _, dir := range []string{filepath.Join(home, ".postgresql"), filepath.Join(home, "postgresql")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"postgresql.crt", "postgresql.key", "root.crt"} {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("not a certificate"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	line := []byte("*:*:*:*:pgpass-secret\n")
	if err := os.WriteFile(filepath.Join(home, ".pgpass"), line, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "postgresql", "pgpass.conf"), line, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestIgnoreLibpqEnvironmentClearsEveryVariable(t *testing.T) {
	for _, k := range libpqEnv {
		t.Setenv(k, "junk")
	}
	IgnoreLibpqEnvironment()
	for _, k := range libpqEnv {
		if v, ok := os.LookupEnv(k); ok {
			t.Errorf("%s is still set to %q", k, v)
		}
	}
	if len(libpqEnv) != 24 {
		t.Errorf("%d variables listed; pgx v5.11.0 reads 24", len(libpqEnv))
	}
}

func TestBuildConfigTakesEverythingFromTheProfile(t *testing.T) {
	// Hostile environment, scrubbed the way main does it.
	for _, k := range libpqEnv {
		t.Setenv(k, "junk")
	}
	IgnoreLibpqEnvironment()
	hostileHome(t)

	cfg, err := buildConfig(profile(), "dbl-secret")
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if cfg.Host != "db.example.com" || cfg.Port != 6543 || cfg.Database != "appdb" || cfg.User != "alice" {
		t.Errorf("target = %s:%d/%s as %s", cfg.Host, cfg.Port, cfg.Database, cfg.User)
	}
	if cfg.Password != "dbl-secret" {
		t.Errorf("password = %q, want the one DBL passed", cfg.Password)
	}
	if len(cfg.Fallbacks) != 0 {
		t.Errorf("%d fallback hosts: a plaintext fall back is possible", len(cfg.Fallbacks))
	}
	if cfg.RuntimeParams["application_name"] != "DBL" {
		t.Errorf("application_name = %q", cfg.RuntimeParams["application_name"])
	}
	if cfg.ConnectTimeout != 10*time.Second {
		t.Errorf("ConnectTimeout = %v", cfg.ConnectTimeout)
	}
}

func TestBuildConfigIgnoresPgpassWhenThereIsNoPassword(t *testing.T) {
	IgnoreLibpqEnvironment()
	hostileHome(t)
	cfg, err := buildConfig(profile(), "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Password != "" {
		t.Errorf("password = %q: the .pgpass file leaked in", cfg.Password)
	}
}

func TestBuildConfigDoesNotReadClientOrRootCertificatesFromHome(t *testing.T) {
	IgnoreLibpqEnvironment()
	hostileHome(t)
	for _, mode := range []connection.TLSMode{connection.TLSRequire, connection.TLSVerifyCA, connection.TLSVerifyFull} {
		p := profile()
		p.TLS.Mode = mode
		cfg, err := buildConfig(p, "x")
		if err != nil {
			t.Errorf("%s: %v (a certificate file under the home directory was used)", mode, err)
			continue
		}
		if cfg.TLSConfig == nil {
			t.Errorf("%s: TLS is off", mode)
			continue
		}
		if len(cfg.TLSConfig.Certificates) != 0 {
			t.Errorf("%s: a client certificate was loaded", mode)
		}
		if cfg.TLSConfig.RootCAs != nil {
			t.Errorf("%s: a root CA was loaded from the home directory", mode)
		}
	}
}

func TestBuildConfigTLSModes(t *testing.T) {
	IgnoreLibpqEnvironment()
	cases := []struct {
		mode      connection.TLSMode
		tls       bool
		skip      bool
		customVfy bool
	}{
		{connection.TLSDisable, false, false, false},
		{connection.TLSRequire, true, true, false},
		{connection.TLSVerifyCA, true, true, true}, // pgx checks the chain itself, not the host name
		{connection.TLSVerifyFull, true, false, false},
	}
	for _, c := range cases {
		p := profile()
		p.TLS.Mode = c.mode
		cfg, err := buildConfig(p, "")
		if err != nil {
			t.Fatalf("%s: %v", c.mode, err)
		}
		if (cfg.TLSConfig != nil) != c.tls {
			t.Errorf("%s: TLS configured = %v, want %v", c.mode, cfg.TLSConfig != nil, c.tls)
			continue
		}
		if len(cfg.Fallbacks) != 0 {
			t.Errorf("%s: has fallbacks", c.mode)
		}
		if !c.tls {
			continue
		}
		if cfg.TLSConfig.InsecureSkipVerify != c.skip {
			t.Errorf("%s: InsecureSkipVerify = %v, want %v", c.mode, cfg.TLSConfig.InsecureSkipVerify, c.skip)
		}
		if (cfg.TLSConfig.VerifyPeerCertificate != nil) != c.customVfy {
			t.Errorf("%s: custom chain verification = %v, want %v", c.mode, cfg.TLSConfig.VerifyPeerCertificate != nil, c.customVfy)
		}
		if c.mode == connection.TLSVerifyFull && cfg.TLSConfig.ServerName != "db.example.com" {
			t.Errorf("verify-full ServerName = %q", cfg.TLSConfig.ServerName)
		}
	}
}

func TestBuildConfigRootCertificate(t *testing.T) {
	IgnoreLibpqEnvironment()
	p := profile()
	p.TLS = connection.TLSOptions{Mode: connection.TLSVerifyFull, RootCert: writeCA(t)}
	cfg, err := buildConfig(p, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLSConfig.RootCAs == nil {
		t.Error("the CA file was not loaded")
	}

	p.TLS.RootCert = filepath.Join(t.TempDir(), "missing.pem")
	if _, err := buildConfig(p, ""); err == nil {
		t.Error("a missing CA file was accepted")
	}
	bad := filepath.Join(t.TempDir(), "bad.pem")
	if err := os.WriteFile(bad, []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	p.TLS.RootCert = bad
	if _, err := buildConfig(p, ""); err == nil || !strings.Contains(err.Error(), "CA") {
		t.Errorf("a CA file that is not PEM: %v", err)
	}

	// A root certificate only goes with verifying modes.
	p.TLS = connection.TLSOptions{Mode: connection.TLSRequire, RootCert: writeCA(t)}
	if _, err := buildConfig(p, ""); err == nil {
		t.Error("a CA with TLS mode require was accepted")
	}
	// The word "system" must be a file name, not pgx's system-roots switch.
	cwd := t.TempDir()
	t.Chdir(cwd)
	p.TLS = connection.TLSOptions{Mode: connection.TLSVerifyFull, RootCert: "system"}
	if _, err := buildConfig(p, ""); err == nil {
		t.Error(`RootCert "system" was taken as the system certificate pool`)
	}
}

func TestBuildConfigKeepsOddValuesIntact(t *testing.T) {
	IgnoreLibpqEnvironment()
	p := profile()
	p.Username = "a:b@c/d?e#f%g h"
	p.Database = "my/db?x=1&y#z é"
	password := "p@ss:w/rd?#%&=é\\'\" "
	cfg, err := buildConfig(p, password)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.User != p.Username {
		t.Errorf("user = %q, want %q", cfg.User, p.Username)
	}
	if cfg.Database != p.Database {
		t.Errorf("database = %q, want %q", cfg.Database, p.Database)
	}
	if cfg.Password != password {
		t.Errorf("password = %q, want %q", cfg.Password, password)
	}
}

func TestBuildConfigHosts(t *testing.T) {
	IgnoreLibpqEnvironment()
	for in, want := range map[string]string{"::1": "::1", "[::1]": "::1", "127.0.0.1": "127.0.0.1", "localhost": "localhost"} {
		p := profile()
		p.Host = in
		cfg, err := buildConfig(p, "")
		if err != nil || cfg.Host != want || cfg.Port != 6543 {
			t.Errorf("host %q: %v %q:%d", in, err, cfg.Host, cfg.Port)
		}
	}
	for _, bad := range []string{"", "a,b", "a/b", `a\b`, "/var/run/postgresql", "a b"} {
		p := profile()
		p.Host = bad
		if _, err := buildConfig(p, ""); err == nil {
			t.Errorf("host %q was accepted", bad)
		}
	}
}

func TestBuildConfigPasswordIsNotInTheParseError(t *testing.T) {
	IgnoreLibpqEnvironment()
	p := profile()
	p.Port = 99999 // pgx rejects the port
	_, err := buildConfig(p, "hunter2")
	if err == nil {
		t.Fatal("an invalid port was accepted")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the error leaks the password: %v", err)
	}
}

func TestBuildConfigPinsTheSettingsItDoesNotExposeToTheUser(t *testing.T) {
	IgnoreLibpqEnvironment()
	cfg, err := buildConfig(profile(), "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinProtocolVersion != "3.0" || cfg.MaxProtocolVersion != "3.0" {
		t.Errorf("protocol %s..%s", cfg.MinProtocolVersion, cfg.MaxProtocolVersion)
	}
	if cfg.ValidateConnect != nil {
		t.Error("target_session_attrs is not 'any'")
	}
}
