package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"

	"dbl/internal/connection"
	"dbl/internal/database"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestClassify(t *testing.T) {
	p := profile()
	wrap := func(err error) error {
		return fmt.Errorf("failed to connect to `user=alice database=appdb`: %w", err)
	}
	cases := []struct {
		name     string
		err      error
		kind     database.Kind
		contains string
		hint     string
	}{
		{"wrong password", wrap(&pgconn.PgError{Code: "28P01", Message: `password authentication failed for user "alice"`}),
			database.KindAuthentication, `Authentication failed for user "alice"`, "password"},
		{"invalid authorization", wrap(&pgconn.PgError{Code: "28000", Message: "no pg_hba.conf entry"}),
			database.KindAuthentication, "no pg_hba.conf entry", ""},
		{"permission denied", &pgconn.PgError{Code: "42501", Message: "permission denied for database appdb"},
			database.KindPermission, "permission denied for database", ""},
		{"missing database", wrap(&pgconn.PgError{Code: "3D000", Message: `database "appdb" does not exist`}),
			database.KindConnection, `Database "appdb" does not exist on db.example.com:6543`, "database name"},
		{"other server error", &pgconn.PgError{Code: "53300", Message: "too many connections"},
			database.KindConnection, "too many connections", ""},
		{"deadline", wrap(context.DeadlineExceeded), database.KindTimeout, "timed out after 10 s", "host and port"},
		{"net timeout", wrap(&net.OpError{Op: "dial", Err: timeoutErr{}}), database.KindTimeout, "timed out", ""},
		{"canceled", wrap(context.Canceled), database.KindCanceled, "canceled", ""},
		{"no tls", wrap(errors.New("server refused TLS connection")), database.KindConnection, "does not support TLS", `"disable"`},
		{"tls handshake", wrap(errors.New("tls error: x509: certificate signed by unknown authority")), database.KindConnection, "TLS handshake", "CA"},
		{"bad ca file", errors.New("failed to configure TLS (unable to read CA file: open x: no such file)"), database.KindFileAccess, "CA certificate", "PEM"},
		{"file error", fmt.Errorf("x: %w", fs.ErrNotExist), database.KindFileAccess, "CA certificate", ""},
		{"refused", wrap(errors.New("dial tcp 127.0.0.1:6543: connect: connection refused")), database.KindConnection, "Could not connect to db.example.com:6543", ""},
	}
	for _, c := range cases {
		got := classify(c.err, p, "")
		if got == nil {
			t.Errorf("%s: nil", c.name)
			continue
		}
		if got.Kind != c.kind {
			t.Errorf("%s: kind %v, want %v (%s)", c.name, got.Kind, c.kind, got.Message)
		}
		if !strings.Contains(got.Message, c.contains) {
			t.Errorf("%s: message %q does not contain %q", c.name, got.Message, c.contains)
		}
		if c.hint != "" && !strings.Contains(got.Hint, c.hint) {
			t.Errorf("%s: hint %q does not contain %q", c.name, got.Hint, c.hint)
		}
		if got.Err == nil {
			t.Errorf("%s: the cause is lost", c.name)
		}
	}
	if classify(nil, p, "") != nil {
		t.Error("classify(nil) is not nil")
	}
	existing := &database.Error{Kind: database.KindPermission, Message: "x"}
	if classify(fmt.Errorf("w: %w", existing), p, "") != existing {
		t.Error("an existing *database.Error was rebuilt")
	}
}

func TestClassifyNeverShowsThePassword(t *testing.T) {
	p := profile()
	for _, pw := range []string{"hunter2", "p@ss:w/rd", "é", "ab"} {
		errs := []error{
			errors.New("something about " + pw + " went wrong"),
			&pgconn.PgError{Code: "28P01", Message: "password " + pw + " rejected"},
			&pgconn.PgError{Code: "53300", Message: pw},
			errors.New("tls error: " + pw),
			errors.New("unable to read CA file: " + pw),
		}
		for _, err := range errs {
			got := classify(err, p, pw)
			if strings.Contains(got.Message, pw) || strings.Contains(got.Hint, pw) || strings.Contains(got.Error(), pw) {
				t.Errorf("password %q leaks in %q / %q", pw, got.Message, got.Hint)
			}
		}
	}
}

// listen returns a TCP listener on a free port of the loopback interface.
func listen(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen on the loopback interface: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func profileAt(l net.Listener, mode connection.TLSMode) connection.Profile {
	addr := l.Addr().(*net.TCPAddr)
	p := profile()
	p.Host, p.Port = "127.0.0.1", addr.Port
	p.TLS.Mode = mode
	p.ConnectTimeoutSec = 1
	return p
}

func TestConnectRefused(t *testing.T) {
	IgnoreLibpqEnvironment()
	l := listen(t)
	p := profileAt(l, connection.TLSDisable)
	_ = l.Close() // nothing listens on the port any more
	ad, err := Connect(context.Background(), p, "hunter2")
	if ad != nil || err == nil {
		t.Fatalf("Connect = %v, %v", ad, err)
	}
	if database.KindOf(err) != database.KindConnection {
		t.Errorf("kind %v: %v", database.KindOf(err), err)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the error leaks the password: %v", err)
	}
}

func TestConnectTimesOutOnASilentServer(t *testing.T) {
	IgnoreLibpqEnvironment()
	l := listen(t)
	go func() { // accepts and never answers
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	start := time.Now()
	ad, err := Connect(context.Background(), profileAt(l, connection.TLSDisable), "hunter2")
	if ad != nil || database.KindOf(err) != database.KindTimeout {
		t.Fatalf("Connect = %v, %v (kind %v)", ad, err, database.KindOf(err))
	}
	if d := time.Since(start); d < 800*time.Millisecond || d > 5*time.Second {
		t.Errorf("returned after %v, want about the 1 s timeout", d)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the error leaks the password: %v", err)
	}
}

func TestConnectCanBeCancelled(t *testing.T) {
	IgnoreLibpqEnvironment()
	l := listen(t)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	p := profileAt(l, connection.TLSDisable)
	p.ConnectTimeoutSec = 30
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(150 * time.Millisecond); cancel() }()
	start := time.Now()
	ad, err := Connect(ctx, p, "")
	if ad != nil || database.KindOf(err) != database.KindCanceled {
		t.Fatalf("Connect = %v, %v (kind %v)", ad, err, database.KindOf(err))
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("cancelling took %v", d)
	}
}

func TestConnectToAServerWithoutTLSSuggestsAnotherMode(t *testing.T) {
	IgnoreLibpqEnvironment()
	l := listen(t)
	go func() { // answers the SSLRequest with 'N': no TLS
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 8)
				if _, err := io.ReadFull(c, buf); err == nil {
					_, _ = c.Write([]byte("N"))
				}
			}()
		}
	}()
	_, err := Connect(context.Background(), profileAt(l, connection.TLSRequire), "")
	var de *database.Error
	if !errors.As(err, &de) || de.Kind != database.KindConnection {
		t.Fatalf("error %v", err)
	}
	if !strings.Contains(de.Message, "does not support TLS") || !strings.Contains(de.Hint, `"disable"`) {
		t.Errorf("message %q hint %q", de.Message, de.Hint)
	}
}

func TestConnectWithAnInvalidProfileFailsBeforeTheNetwork(t *testing.T) {
	IgnoreLibpqEnvironment()
	p := profile()
	p.Host = "a,b"
	ad, err := Connect(context.Background(), p, "x")
	if ad != nil || err == nil || database.KindOf(err) == 0 {
		t.Errorf("Connect = %v, %v", ad, err)
	}
}

func TestAdapterCloseTwiceAndPingAfterClose(t *testing.T) {
	IgnoreLibpqEnvironment()
	cfg, err := buildConfig(profile(), "")
	if err != nil {
		t.Fatal(err)
	}
	ad := &Adapter{db: stdlib.OpenDB(*cfg)} // opens no connection yet
	if err := ad.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := ad.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if err := ad.Ping(context.Background()); database.KindOf(err) == 0 {
		t.Errorf("Ping after Close: %v", err)
	}
}
