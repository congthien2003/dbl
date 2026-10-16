package postgres

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"dbl/internal/connection"
	"dbl/internal/database"
)

// The tests in this file need a PostgreSQL server that may be written to
// and read freely, such as a throw-away container. They are skipped unless
// the environment names one:
//
//	DBL_TEST_PG_HOST      required, for example 127.0.0.1
//	DBL_TEST_PG_PORT      default 5432
//	DBL_TEST_PG_USER      required
//	DBL_TEST_PG_PASSWORD  the password of that user
//	DBL_TEST_PG_DB        required, a database the user may connect to
//	DBL_TEST_PG_TLS       disable (default), require, verify-ca or verify-full
//
// They run in the DBL process of `go test`, so the libpq variables are
// cleared first, as main does.

func integrationProfile(t *testing.T) (connection.Profile, string) {
	t.Helper()
	host := os.Getenv("DBL_TEST_PG_HOST")
	if host == "" {
		t.Skip("DBL_TEST_PG_HOST is not set; skipping the PostgreSQL integration test")
	}
	user, db := os.Getenv("DBL_TEST_PG_USER"), os.Getenv("DBL_TEST_PG_DB")
	if user == "" || db == "" {
		t.Skip("DBL_TEST_PG_USER and DBL_TEST_PG_DB are required")
	}
	port := 5432
	if s := os.Getenv("DBL_TEST_PG_PORT"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			t.Fatalf("DBL_TEST_PG_PORT: %v", err)
		}
		port = n
	}
	mode := connection.TLSMode(os.Getenv("DBL_TEST_PG_TLS"))
	if mode == "" {
		mode = connection.TLSDisable
	}
	IgnoreLibpqEnvironment()
	p := connection.Profile{
		ID: "it", Name: "integration", Driver: connection.DriverPostgres, Host: host, Port: port,
		Database: db, Username: user, TLS: connection.TLSOptions{Mode: mode}, ConnectTimeoutSec: 5,
	}.Normalized()
	return p, os.Getenv("DBL_TEST_PG_PASSWORD")
}

func TestIntegrationConnectAndClose(t *testing.T) {
	p, pw := integrationProfile(t)
	ad, err := Connect(context.Background(), p, pw)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ad.Ping(ctx); err != nil {
		t.Errorf("Ping: %v", err)
	}
	if err := ad.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := ad.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if err := ad.Ping(ctx); err == nil {
		t.Error("Ping after Close succeeded")
	}
}

func TestIntegrationWrongPassword(t *testing.T) {
	p, pw := integrationProfile(t)
	wrong := pw + "-wrong"
	ad, err := Connect(context.Background(), p, wrong)
	if ad != nil {
		_ = ad.Close()
		t.Skip("the server accepts any password (trust authentication)")
	}
	if database.KindOf(err) != database.KindAuthentication {
		t.Errorf("kind %v: %v", database.KindOf(err), err)
	}
	if strings.Contains(err.Error(), wrong) {
		t.Errorf("the error leaks the password: %v", err)
	}
}

func TestIntegrationMissingDatabase(t *testing.T) {
	p, pw := integrationProfile(t)
	p.Database = "dbl_no_such_database"
	ad, err := Connect(context.Background(), p, pw)
	if ad != nil {
		_ = ad.Close()
		t.Fatal("connected to a database that should not exist")
	}
	if database.KindOf(err) == 0 || !strings.Contains(err.Error(), "dbl_no_such_database") {
		t.Errorf("error %v", err)
	}
}

func TestIntegrationUnverifiableTLSFails(t *testing.T) {
	p, pw := integrationProfile(t)
	p.TLS = connection.TLSOptions{Mode: connection.TLSVerifyFull, RootCert: writeCA(t)}
	ad, err := Connect(context.Background(), p, pw)
	if ad != nil {
		_ = ad.Close()
		t.Fatal("verify-full succeeded against an unrelated CA")
	}
	if database.KindOf(err) != database.KindConnection {
		t.Errorf("kind %v: %v", database.KindOf(err), err)
	}
}
