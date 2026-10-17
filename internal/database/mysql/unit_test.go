package mysql

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gomysql "github.com/go-sql-driver/mysql"

	"dbl/internal/connection"
	"dbl/internal/database"
	"dbl/internal/grid"
)

func testProfile() connection.Profile {
	return connection.Profile{
		ID: "p", Name: "m", Driver: connection.DriverMySQL, Host: "db.example", Port: 3306,
		Username: "app", TLS: connection.TLSOptions{Mode: connection.TLSDisable}, ConnectTimeoutSec: 7,
	}.Normalized()
}

func TestBuildConfigPinsEveryOption(t *testing.T) {
	p := testProfile()
	p.Database = "shop"
	cfg, err := buildConfig(p, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case cfg.Net != "tcp" || cfg.Addr != "db.example:3306" || cfg.DBName != "shop" || cfg.User != "app" || cfg.Passwd != "s3cret":
		t.Errorf("address/login: %+v", cfg)
	case cfg.Timeout.Seconds() != 7:
		t.Errorf("timeout %v", cfg.Timeout)
	case cfg.ParseTime || cfg.InterpolateParams || cfg.MultiStatements:
		t.Error("parseTime, interpolateParams and multiStatements must be off")
	case !cfg.ClientFoundRows:
		t.Error("clientFoundRows must be on for Save")
	case cfg.AllowAllFiles || cfg.AllowCleartextPasswords || cfg.AllowOldPasswords || cfg.AllowFallbackToPlaintext:
		t.Error("an unsafe option is on")
	case cfg.TLS != nil:
		t.Error("TLS disable must have no TLS config")
	}
}

func TestBuildConfigEmptyDatabaseAndIPv6(t *testing.T) {
	p := testProfile()
	p.Host = "[::1]"
	cfg, err := buildConfig(p, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "[::1]:3306" || cfg.DBName != "" {
		t.Errorf("addr %q db %q", cfg.Addr, cfg.DBName)
	}
}

func TestTLSModeMapping(t *testing.T) {
	for _, mode := range []connection.TLSMode{connection.TLSRequire, connection.TLSVerifyCA, connection.TLSVerifyFull} {
		c, err := tlsConfig(connection.TLSOptions{Mode: mode}, "db.example")
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if c == nil {
			t.Fatalf("%s: no TLS config", mode)
		}
		switch mode {
		case connection.TLSRequire:
			if !c.InsecureSkipVerify || c.VerifyConnection != nil {
				t.Error("require: encrypted without verification")
			}
		case connection.TLSVerifyCA:
			if !c.InsecureSkipVerify || c.VerifyConnection == nil {
				t.Error("verify-ca: must verify the chain itself")
			}
		case connection.TLSVerifyFull:
			if c.InsecureSkipVerify || c.ServerName != "db.example" {
				t.Error("verify-full: must verify chain and name")
			}
		}
	}
	if _, err := tlsConfig(connection.TLSOptions{Mode: "preferred"}, "h"); err == nil {
		t.Error("an unknown mode must fail, never fall back to plaintext")
	}
}

func TestTLSRootCertProblems(t *testing.T) {
	p := testProfile()
	p.TLS = connection.TLSOptions{Mode: connection.TLSVerifyFull, RootCert: filepath.Join(t.TempDir(), "missing.pem")}
	_, err := buildConfig(p, "")
	if e := connectError(err, p, ""); e.Kind != database.KindFileAccess {
		t.Errorf("missing CA: %v %v", e.Kind, e.Message)
	}
	bad := filepath.Join(t.TempDir(), "bad.pem")
	if err := os.WriteFile(bad, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	p.TLS.RootCert = bad
	_, err = buildConfig(p, "")
	if e := connectError(err, p, ""); e.Kind != database.KindFileAccess {
		t.Errorf("bad CA: %v %v", e.Kind, e.Message)
	}
}

func TestPasswordNeverInErrors(t *testing.T) {
	p := testProfile()
	errs := []error{
		errors.New("dial tcp: something with hunter2 inside"),
		&gomysql.MySQLError{Number: 1045, Message: "Access denied for user 'app' (using password: hunter2)"},
		&gomysql.MySQLError{Number: 2000, Message: "odd hunter2"},
	}
	for _, err := range errs {
		e := connectError(err, p, "hunter2")
		if strings.Contains(e.Message, "hunter2") || strings.Contains(e.Hint, "hunter2") {
			t.Errorf("password leaked: %q", e.Message)
		}
	}
}

func TestConnectErrorKinds(t *testing.T) {
	p := testProfile()
	p.Database = "nope"
	cases := []struct {
		err  error
		kind database.Kind
	}{
		{&gomysql.MySQLError{Number: 1045, Message: "denied"}, database.KindAuthentication},
		{&gomysql.MySQLError{Number: 1044, Message: "denied"}, database.KindPermission},
		{&gomysql.MySQLError{Number: 1049, Message: "unknown"}, database.KindConnection},
		{gomysql.ErrNoTLS, database.KindConnection},
		{context.DeadlineExceeded, database.KindTimeout},
		{context.Canceled, database.KindCanceled},
	}
	for _, c := range cases {
		if got := connectError(c.err, p, ""); got.Kind != c.kind {
			t.Errorf("%v: kind %v, want %v", c.err, got.Kind, c.kind)
		}
	}
}

func TestClassifyStatementErrors(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		num  uint16
		kind database.Kind
	}{
		{1064, database.KindSyntax}, {1146, database.KindSyntax}, {1142, database.KindPermission},
		{1213, database.KindConflict}, {1205, database.KindConflict}, {1317, database.KindCanceled},
		{3024, database.KindTimeout}, {1062, database.KindValidation}, {1406, database.KindValidation},
		{3819, database.KindValidation}, {1452, database.KindValidation}, {1046, database.KindValidation},
	}
	for _, c := range cases {
		if got := classify(ctx, &gomysql.MySQLError{Number: c.num, Message: "m"}, "x"); got.Kind != c.kind {
			t.Errorf("%d: kind %v, want %v", c.num, got.Kind, c.kind)
		}
	}
	if got := classify(ctx, gomysql.ErrInvalidConn, "x"); got.Kind != database.KindConnection {
		t.Errorf("invalid conn: %v", got.Kind)
	}
}

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in           string
		maria        bool
		major, minor int
		tested       bool
		label        string
	}{
		{"8.4.11", false, 8, 4, true, "MySQL 8.4.11"},
		{"8.0.36-0ubuntu0.22.04.1", false, 8, 0, true, "MySQL 8.0.36"},
		{"5.7.44-log", false, 5, 7, false, "MySQL 5.7.44"},
		{"11.4.13-MariaDB-ubu2404", true, 11, 4, true, "MariaDB 11.4.13"},
		{"10.6.20-MariaDB", true, 10, 6, true, "MariaDB 10.6.20"},
		{"10.5.9-MariaDB-log", true, 10, 5, false, "MariaDB 10.5.9"},
		{"5.5.5-10.11.6-MariaDB", true, 10, 11, true, "MariaDB 5.5.5"},
	}
	for _, c := range cases {
		s := parseVersion(c.in)
		if s.MariaDB != c.maria || s.Major != c.major || s.Minor != c.minor || s.Tested() != c.tested {
			t.Errorf("%s: %+v tested=%v", c.in, s, s.Tested())
		}
		if c.in != "5.5.5-10.11.6-MariaDB" && s.Label() != c.label {
			t.Errorf("%s: label %q", c.in, s.Label())
		}
	}
}

func TestClassOf(t *testing.T) {
	cases := []struct {
		dataType, full string
		want           columnType
	}{
		{"tinyint", "tinyint(1)", columnType{database.ClassNumber, 8, false}},
		{"tinyint", "tinyint unsigned", columnType{database.ClassNumber, 8, true}},
		{"mediumint", "mediumint", columnType{database.ClassNumber, 24, false}},
		{"int", "int unsigned zerofill", columnType{database.ClassNumber, 32, true}},
		{"bigint", "bigint unsigned", columnType{database.ClassNumber, 64, true}},
		{"decimal", "decimal(65,30)", columnType{Class: database.ClassNumber}},
		{"double", "double", columnType{Class: database.ClassNumber}},
		{"year", "year", columnType{Class: database.ClassNumber}},
		{"varchar", "varchar(20)", columnType{Class: database.ClassText}},
		{"enum", "enum('a','b')", columnType{Class: database.ClassText}},
		{"datetime", "datetime(6)", columnType{Class: database.ClassDateTime}},
		{"time", "time", columnType{Class: database.ClassDateTime}},
		{"json", "json", columnType{Class: database.ClassJSON}},
		{"blob", "blob", columnType{Class: database.ClassBinary}},
		{"bit", "bit(1)", columnType{Class: database.ClassBinary}},
		{"point", "point", columnType{Class: database.ClassBinary}},
		{"uuid", "uuid", columnType{Class: database.ClassOrdered}},
		{"weird", "weird", columnType{Class: database.ClassOther}},
	}
	for _, c := range cases {
		if got := classOf(c.dataType, c.full); got != c.want {
			t.Errorf("%s/%s: %+v, want %+v", c.dataType, c.full, got, c.want)
		}
	}
}

func TestGeneratedExtra(t *testing.T) {
	yes := []string{"VIRTUAL GENERATED", "STORED GENERATED", "PERSISTENT GENERATED", "VIRTUAL GENERATED INVISIBLE"}
	no := []string{"", "auto_increment", "DEFAULT_GENERATED", "DEFAULT_GENERATED on update CURRENT_TIMESTAMP", "on update current_timestamp()", "INVISIBLE"}
	for _, e := range yes {
		if !generatedExtra(e) {
			t.Errorf("%q should be generated", e)
		}
	}
	for _, e := range no {
		if generatedExtra(e) {
			t.Errorf("%q should not be generated", e)
		}
	}
}

func testMeta() *database.TableMetadata {
	m := &database.TableMetadata{
		Ref: database.TableRef{Schema: "sh`op", Name: "Users; DROP"},
		Columns: []database.ColumnMetadata{
			{Name: "id", Class: database.ClassNumber, NativeType: "int", IntBits: 32},
			{Name: "na`me", Class: database.ClassText, Nullable: true, NativeType: "varchar(20)"},
			{Name: "blob", Class: database.ClassBinary, Nullable: true, NativeType: "blob"},
			{Name: "score", Class: database.ClassNumber, Nullable: true, NativeType: "float"},
		},
		Constraints: database.Section[database.ConstraintMetadata]{Items: []database.ConstraintMetadata{
			{Type: database.ConstraintPrimaryKey, Columns: []string{"id"}},
		}},
	}
	m.ResolveIdentity()
	return m
}

func TestQuoteIdentBacktick(t *testing.T) {
	cases := map[string]string{"users": "`users`", "a`b": "`a``b`", "x; DROP": "`x; DROP`", "Ünï": "`Ünï`", `a"b`: "`a\"b`"}
	for in, want := range cases {
		if got := quoteIdent(in); got != want {
			t.Errorf("quoteIdent(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestPageQueryKeysetAndOffset(t *testing.T) {
	pq, err := buildPageQuery(database.PageRequest{Meta: testMeta(), Size: 100})
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT CAST(`id` AS CHAR), CAST(`na``me` AS CHAR), OCTET_LENGTH(`blob`), CAST(`score` AS CHAR) FROM `sh``op`.`Users; DROP` ORDER BY `id` LIMIT ?"
	if pq.SQL != want || len(pq.Args) != 1 || pq.Args[0] != int64(101) || !pq.Binary[2] {
		t.Errorf("first page:\n got %s %v\nwant %s", pq.SQL, pq.Args, want)
	}
	pq, err = buildPageQuery(database.PageRequest{Meta: testMeta(), Size: 10, Cursor: database.Cursor{Dir: database.CursorBefore, Key: []string{"42"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(pq.SQL, "WHERE (`id`) < (?) ORDER BY `id` DESC LIMIT ?") || !pq.Reverse || pq.Args[0] != "42" {
		t.Errorf("before: %s %v", pq.SQL, pq.Args)
	}
	pq, err = buildPageQuery(database.PageRequest{Meta: testMeta(), Size: 10, Offset: 20, Sort: &database.Sort{Column: "na`me", Desc: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(pq.SQL, "ORDER BY `na``me` DESC, `id` LIMIT ? OFFSET ?") || pq.Args[1] != int64(20) {
		t.Errorf("offset: %s %v", pq.SQL, pq.Args)
	}
}

func TestFilterBindsEveryValue(t *testing.T) {
	evil := "x' OR 1=1 -- `"
	for _, op := range []database.FilterOp{database.OpEquals, database.OpNotEquals, database.OpContains, database.OpGreater, database.OpLess} {
		pq, err := buildPageQuery(database.PageRequest{Meta: testMeta(), Size: 5, Sort: &database.Sort{Column: "id"}, Filter: &database.Filter{Column: "na`me", Op: op, Value: evil}})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(pq.SQL, "OR 1=1") {
			t.Errorf("%v: value in SQL: %s", op, pq.SQL)
		}
	}
	pq, _ := buildPageQuery(database.PageRequest{Meta: testMeta(), Size: 5, Sort: &database.Sort{Column: "id"}, Filter: &database.Filter{Column: "na`me", Op: database.OpContains, Value: `50%_off!\`}})
	if !strings.Contains(pq.SQL, "CAST(`na``me` AS CHAR) LIKE ? ESCAPE '!'") || pq.Args[0] != `%50!%!_off!!\%` {
		t.Errorf("contains: %s %v", pq.SQL, pq.Args)
	}
}

func TestBuildUpdateComparesExactText(t *testing.T) {
	m := testMeta()
	rc := database.RowChange{
		Key: []database.KeyValue{{Column: "id", Value: grid.Value{Kind: grid.KindNumber, Text: "7"}}},
		Cells: []database.CellChange{
			{Column: "na`me", Original: grid.Value{Kind: grid.KindText, Text: "abc "}, Current: grid.Value{Kind: grid.KindText, Text: "ABC"}},
			{Column: "score", Original: grid.Value{Kind: grid.KindNull}, Current: grid.Value{Kind: grid.KindNumber, Text: "0.1"}},
		},
	}
	q, args, err := buildUpdate(m, rc)
	if err != nil {
		t.Fatal(err)
	}
	want := "UPDATE `sh``op`.`Users; DROP` SET `na``me` = ?, `score` = ? WHERE `id` = ? AND CAST(CAST(`id` AS CHAR) AS BINARY) = CAST(? AS BINARY) AND CAST(CAST(`na``me` AS CHAR) AS BINARY) = CAST(? AS BINARY) AND `score` IS NULL"
	if q != want {
		t.Errorf("SQL\n got %s\nwant %s", q, want)
	}
	wantArgs := []any{"ABC", "0.1", "7", "7", "abc "}
	if len(args) != len(wantArgs) {
		t.Fatalf("args %v", args)
	}
	for i := range args {
		if args[i] != wantArgs[i] {
			t.Errorf("arg %d = %v, want %v", i, args[i], wantArgs[i])
		}
	}
	// A floating-point key is compared only by its text.
	m.Columns[0].NativeType = "double"
	q, _, _ = buildUpdate(m, rc)
	if strings.Contains(q, "`id` = ?") {
		t.Errorf("float key compared with =: %s", q)
	}
}

func TestStrictMode(t *testing.T) {
	cases := map[string]string{
		"":                                 "STRICT_ALL_TABLES",
		"ONLY_FULL_GROUP_BY":               "ONLY_FULL_GROUP_BY,STRICT_ALL_TABLES",
		"STRICT_ALL_TABLES,NO_ZERO_DATE":   "STRICT_ALL_TABLES,NO_ZERO_DATE",
		"STRICT_TRANS_TABLES,NO_ZERO_DATE": "STRICT_TRANS_TABLES,NO_ZERO_DATE,STRICT_ALL_TABLES",
	}
	for in, want := range cases {
		if got := strictMode(in); got != want {
			t.Errorf("strictMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResultValue(t *testing.T) {
	cases := []struct {
		class database.ColumnClass
		in    any
		want  grid.Value
	}{
		{database.ClassNumber, nil, grid.Value{Kind: grid.KindNull}},
		{database.ClassNumber, int64(-5), grid.Value{Kind: grid.KindNumber, Text: "-5"}},
		{database.ClassNumber, uint64(18446744073709551615), grid.Value{Kind: grid.KindNumber, Text: "18446744073709551615"}},
		{database.ClassNumber, float32(0.1), grid.Value{Kind: grid.KindNumber, Text: "0.1"}},
		{database.ClassNumber, []byte("12.500"), grid.Value{Kind: grid.KindNumber, Text: "12.500"}},
		{database.ClassDateTime, []byte("0000-00-00 00:00:00"), grid.Value{Kind: grid.KindDateTime, Text: "0000-00-00 00:00:00"}},
		{database.ClassBinary, []byte{1, 2, 3}, grid.Value{Kind: grid.KindBinary, Size: 3}},
		{database.ClassText, []byte("héllo"), grid.Value{Kind: grid.KindText, Text: "héllo"}},
	}
	for _, c := range cases {
		if got := resultValue(c.class, c.in); got != c.want {
			t.Errorf("%v: %+v, want %+v", c.in, got, c.want)
		}
	}
}
