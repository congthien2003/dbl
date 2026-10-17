package mysql

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"dbl/internal/connection"
	"dbl/internal/database"
	"dbl/internal/grid"
)

// The tests in this file need a MySQL or MariaDB server that may be written
// to freely, such as a throw-away container. They are skipped unless the
// environment names one:
//
//	DBL_TEST_MYSQL_HOST      required, for example 127.0.0.1
//	DBL_TEST_MYSQL_PORT      default 3306
//	DBL_TEST_MYSQL_USER      required; some tests create a user, so root
//	DBL_TEST_MYSQL_PASSWORD  the password of that user
//	DBL_TEST_MYSQL_DB        required, a database the tests create tables in
//	DBL_TEST_MYSQL_TLS       disable (default), require, verify-ca or verify-full

func integrationProfile(t *testing.T) (connection.Profile, string) {
	t.Helper()
	host := os.Getenv("DBL_TEST_MYSQL_HOST")
	if host == "" {
		t.Skip("DBL_TEST_MYSQL_HOST is not set; skipping the MySQL integration test")
	}
	user, db := os.Getenv("DBL_TEST_MYSQL_USER"), os.Getenv("DBL_TEST_MYSQL_DB")
	if user == "" || db == "" {
		t.Skip("DBL_TEST_MYSQL_USER and DBL_TEST_MYSQL_DB are required")
	}
	port := 3306
	if s := os.Getenv("DBL_TEST_MYSQL_PORT"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			t.Fatalf("DBL_TEST_MYSQL_PORT: %v", err)
		}
		port = n
	}
	mode := connection.TLSMode(os.Getenv("DBL_TEST_MYSQL_TLS"))
	if mode == "" {
		mode = connection.TLSDisable
	}
	p := connection.Profile{
		ID: "it", Name: "integration", Driver: connection.DriverMySQL, Host: host, Port: port,
		Database: db, Username: user, TLS: connection.TLSOptions{Mode: mode}, ConnectTimeoutSec: 5,
	}.Normalized()
	return p, os.Getenv("DBL_TEST_MYSQL_PASSWORD")
}

func connectWith(t *testing.T, p connection.Profile, pw string) *Adapter {
	t.Helper()
	ad, err := Connect(context.Background(), p, pw)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	a := ad.(*Adapter)
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func openIntegration(t *testing.T) (*Adapter, string) {
	t.Helper()
	p, pw := integrationProfile(t)
	return connectWith(t, p, pw), p.Database
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// tableFor is a table name of the test's own.
func tableFor(t *testing.T) string {
	n := "dbl_" + nonWord.ReplaceAllString(strings.ToLower(t.Name()), "_")
	if len(n) > 60 {
		n = n[:60]
	}
	return n
}

func mustExec(t *testing.T, a *Adapter, stmts ...string) {
	t.Helper()
	for _, s := range stmts {
		if _, err := a.db.ExecContext(context.Background(), s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// newTable creates a table for the test, dropped at the end.
func newTable(t *testing.T, a *Adapter, db, columns string, inserts ...string) database.TableRef {
	t.Helper()
	name := tableFor(t)
	q := quoteIdent(db) + "." + quoteIdent(name)
	mustExec(t, a, "DROP TABLE IF EXISTS "+q, "CREATE TABLE "+q+" ("+columns+")")
	for _, ins := range inserts {
		mustExec(t, a, "INSERT INTO "+q+" "+ins)
	}
	t.Cleanup(func() { _, _ = a.db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+q) })
	return database.TableRef{Schema: db, Name: name}
}

func describe(t *testing.T, a *Adapter, ref database.TableRef) *database.TableMetadata {
	t.Helper()
	m, err := a.DescribeTable(context.Background(), ref)
	if err != nil {
		t.Fatalf("describe %s: %v", ref.Name, err)
	}
	return m
}

func firstPage(t *testing.T, a *Adapter, m *database.TableMetadata, f *database.Filter) [][]grid.Value {
	t.Helper()
	res, err := a.FetchPage(context.Background(), database.PageRequest{Meta: m, Size: 100, Filter: f})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	return res.Rows
}

func text(v grid.Value) string {
	if v.Kind == grid.KindNull {
		return "NULL"
	}
	return v.Text
}

// --- connection ---

func TestIntegrationConnectReportsVersion(t *testing.T) {
	a, _ := openIntegration(t)
	if !a.server.Tested() {
		t.Errorf("server %q is not a tested version", a.server.Version)
	}
	if l := a.ServerLabel(); !strings.Contains(l, "MySQL") && !strings.Contains(l, "MariaDB") {
		t.Errorf("label %q", l)
	}
	if err := a.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationWrongPasswordIsAuthError(t *testing.T) {
	p, _ := integrationProfile(t)
	_, err := Connect(context.Background(), p, "definitely-wrong-password")
	if database.KindOf(err) != database.KindAuthentication {
		t.Fatalf("got %v (%v)", database.KindOf(err), err)
	}
	if strings.Contains(err.Error(), "definitely-wrong-password") {
		t.Error("the password is in the message")
	}
}

func TestIntegrationUnknownDatabase(t *testing.T) {
	p, pw := integrationProfile(t)
	p.Database = "dbl_no_such_database"
	_, err := Connect(context.Background(), p, pw)
	var de *database.Error
	if !errors.As(err, &de) || !strings.Contains(de.Message, "does not exist") {
		t.Fatalf("got %v", err)
	}
}

func TestIntegrationNoDefaultDatabase(t *testing.T) {
	p, pw := integrationProfile(t)
	db := p.Database
	p.Database = ""
	a := connectWith(t, p, pw)
	ref := newTable(t, a, db, "id INT PRIMARY KEY", "VALUES (1)")
	if rows := firstPage(t, a, describe(t, a, ref), nil); len(rows) != 1 {
		t.Errorf("rows %v", rows)
	}
}

// --- explorer and metadata ---

func TestIntegrationListSchemasHidesSystem(t *testing.T) {
	a, db := openIntegration(t)
	mustExec(t, a, "CREATE DATABASE IF NOT EXISTS dbl_it_other")
	t.Cleanup(func() { _, _ = a.db.Exec("DROP DATABASE IF EXISTS dbl_it_other") })
	schemas, err := a.ListSchemas(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range schemas {
		names[s.Name] = true
	}
	if !names[db] || !names["dbl_it_other"] {
		t.Errorf("schemas %v", names)
	}
	for _, sys := range systemSchemas {
		if names[sys] {
			t.Errorf("%s is listed", sys)
		}
	}
}

func TestIntegrationDescribeTable(t *testing.T) {
	a, db := openIntegration(t)
	parent := newTable(t, a, db, "id INT PRIMARY KEY, code VARCHAR(10) NOT NULL UNIQUE")
	q := quoteIdent(db)
	child := tableFor(t) + "_c"
	cq := q + "." + quoteIdent(child)
	mustExec(t, a, "DROP TABLE IF EXISTS "+cq, `CREATE TABLE `+cq+` (
		a INT NOT NULL, b VARCHAR(20) NOT NULL, n INT AUTO_INCREMENT UNIQUE,
		parent_id INT NULL, total DECIMAL(10,2) DEFAULT 0, doubled INT AS (a * 2) STORED,
		created TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP, flags TINYINT(1) UNSIGNED,
		doc JSON, CONSTRAINT pos CHECK (total >= 0),
		PRIMARY KEY (a, b), KEY by_parent (parent_id), KEY prefix (b(5)),
		CONSTRAINT fk_parent FOREIGN KEY (parent_id) REFERENCES `+quoteTable(parent)+` (id) ON DELETE CASCADE
	) ENGINE=InnoDB`,
		"CREATE TRIGGER "+quoteIdent(child+"_tr")+" BEFORE INSERT ON "+cq+" FOR EACH ROW SET NEW.total = IFNULL(NEW.total, 0)")
	t.Cleanup(func() { _, _ = a.db.Exec("DROP TABLE IF EXISTS " + cq) })

	m := describe(t, a, database.TableRef{Schema: db, Name: child})
	if m.Kind != database.ObjectTable || m.ReadOnly != "" {
		t.Errorf("kind %v read-only %q", m.Kind, m.ReadOnly)
	}
	if got := strings.Join(m.Identity.Columns, ","); got != "a,b" || m.Identity.Source != database.IdentityPrimaryKey {
		t.Errorf("identity %v", m.Identity)
	}
	col := func(n string) database.ColumnMetadata {
		c, ok := m.Column(n)
		if !ok {
			t.Fatalf("no column %s", n)
		}
		return c
	}
	if c := col("n"); c.Identity != database.IdentityByDefault || c.IntBits != 32 {
		t.Errorf("n: %+v", c)
	}
	if c := col("doubled"); !c.Generated {
		t.Error("doubled is generated")
	}
	if c := col("created"); c.Generated || c.Class != database.ClassDateTime {
		t.Errorf("created: %+v", c)
	}
	if c := col("flags"); c.Class != database.ClassNumber || c.IntBits != 8 || !c.Unsigned {
		t.Errorf("flags: %+v", c)
	}
	if c := col("doc"); c.Class != database.ClassJSON {
		t.Errorf("doc: %+v (MariaDB JSON is LONGTEXT with a json_valid check)", c)
	}
	if !m.Constraints.OK() || !m.ForeignKeys.OK() || !m.Referenced.OK() || !m.Indexes.OK() || !m.Triggers.OK() {
		t.Fatalf("sections: %+v %+v %+v %+v %+v", m.Constraints, m.ForeignKeys, m.Referenced, m.Indexes, m.Triggers)
	}
	kinds := map[database.ConstraintType]int{}
	for _, c := range m.Constraints.Items {
		kinds[c.Type]++
	}
	if kinds[database.ConstraintPrimaryKey] != 1 || kinds[database.ConstraintForeignKey] != 1 || kinds[database.ConstraintCheck] < 1 || kinds[database.ConstraintUnique] < 1 {
		t.Errorf("constraints %+v", m.Constraints.Items)
	}
	if len(m.ForeignKeys.Items) != 1 || m.ForeignKeys.Items[0].RefTable.Name != parent.Name || m.ForeignKeys.Items[0].OnDelete != "CASCADE" {
		t.Errorf("fks %+v", m.ForeignKeys.Items)
	}
	var prefix *database.IndexMetadata
	for i := range m.Indexes.Items {
		if m.Indexes.Items[i].Name == "prefix" {
			prefix = &m.Indexes.Items[i]
		}
	}
	if prefix == nil || prefix.Columns[0] != "b(5)" || prefix.KeyColumns[0] != "b" {
		t.Errorf("prefix index %+v", prefix)
	}
	if len(m.Triggers.Items) != 1 || m.Triggers.Items[0].Timing != "BEFORE" || m.Triggers.Items[0].Events[0] != "INSERT" {
		t.Errorf("triggers %+v", m.Triggers.Items)
	}

	pm := describe(t, a, parent)
	if len(pm.Referenced.Items) != 1 || pm.Referenced.Items[0].Table.Name != child {
		t.Errorf("incoming %+v", pm.Referenced.Items)
	}

	view := tableFor(t) + "_v"
	vq := q + "." + quoteIdent(view)
	mustExec(t, a, "CREATE OR REPLACE VIEW "+vq+" AS SELECT a, b FROM "+cq)
	t.Cleanup(func() { _, _ = a.db.Exec("DROP VIEW IF EXISTS " + vq) })
	vm := describe(t, a, database.TableRef{Schema: db, Name: view})
	if ok, _ := vm.Editable(); vm.Kind != database.ObjectView || ok || vm.Indexes.State != database.SectionNotApplicable {
		t.Errorf("view %+v", vm)
	}
	tables, err := a.ListTables(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]database.ObjectKind{}
	for _, tb := range tables {
		seen[tb.Ref.Name] = tb.Kind
	}
	if k, ok := seen[view]; !ok || k != database.ObjectView {
		t.Errorf("tables %v", seen)
	}
}

func TestIntegrationNonTransactionalEngineIsReadOnly(t *testing.T) {
	a, db := openIntegration(t)
	for _, engine := range []string{"MyISAM", "MEMORY"} {
		t.Run(engine, func(t *testing.T) {
			ref := newTable(t, a, db, "id INT PRIMARY KEY, v VARCHAR(10)) ENGINE="+engine+" -- ", "VALUES (1, 'x')")
			m := describe(t, a, ref)
			if ok, why := m.Editable(); ok || !strings.Contains(why, engine) {
				t.Errorf("editable %v: %q", ok, why)
			}
			err := a.SaveChanges(context.Background(), database.SaveRequest{Meta: m, Changes: change("1", "v", "x", "y")})
			if err == nil {
				t.Error("Save on a non-transactional table must be refused")
			}
			if rows := firstPage(t, a, m, nil); text(rows[0][1]) != "x" {
				t.Errorf("value changed: %v", rows)
			}
		})
	}
}

func TestIntegrationSameTextWhateverTheProtocol(t *testing.T) {
	a, db := openIntegration(t)
	ref := newTable(t, a, db, `id INT PRIMARY KEY, f FLOAT, d DOUBLE, dec65 DECIMAL(65,30), dt DATETIME(6), tm TIME,
		y YEAR, u BIGINT UNSIGNED, b BIT(3), j JSON, e ENUM('a','b'), s SET('x','y'), v VARCHAR(10) CHARACTER SET latin1`,
		`VALUES (1, 0.1, 1e100, 12345678901234567890123456789012345.123456789012345678901234567890,
		'2024-02-29 13:14:15.123456', '838:59:59', 2024, 18446744073709551615, b'101', '{"k": [1, 2]}', 'b', 'x,y', 'café')`)
	m := describe(t, a, ref)
	plain := firstPage(t, a, m, nil)
	filtered := firstPage(t, a, m, &database.Filter{Column: "id", Op: database.OpEquals, Value: "1"})
	if len(plain) != 1 || len(filtered) != 1 {
		t.Fatalf("rows %v %v", plain, filtered)
	}
	for i := range plain[0] {
		if plain[0][i] != filtered[0][i] {
			t.Errorf("column %s: %+v vs %+v", m.Columns[i].Name, plain[0][i], filtered[0][i])
		}
	}
	want := map[string]string{
		"f": "0.1", "dec65": "12345678901234567890123456789012345.123456789012345678901234567890",
		"dt": "2024-02-29 13:14:15.123456", "tm": "838:59:59", "y": "2024", "u": "18446744073709551615",
		"e": "b", "s": "x,y", "v": "café",
	}
	for i, c := range m.Columns {
		if w, ok := want[c.Name]; ok && text(plain[0][i]) != w {
			t.Errorf("%s = %q, want %q", c.Name, text(plain[0][i]), w)
		}
	}
	if i := m.ColumnIndex("b"); plain[0][i].Kind != grid.KindBinary || plain[0][i].Size != 1 {
		t.Errorf("bit: %+v", plain[0][i])
	}
	if i := m.ColumnIndex("j"); plain[0][i].Kind != grid.KindJSON {
		t.Errorf("json: %+v", plain[0][i])
	}

	// The SQL tab prints the same text for what is not floating-point.
	s := openSession(t, a)
	r, err := s.Execute(context.Background(), "SELECT dec65, dt, tm, y, u, e, s, v FROM "+quoteTable(ref))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"dec65", "dt", "tm", "y", "u", "e", "s", "v"} {
		var got string
		for i, c := range r.Results[0].Columns {
			if c.Name == name {
				got = text(r.Results[0].Rows[0][i])
			}
		}
		if got != want[name] {
			t.Errorf("SQL tab %s = %q, want %q", name, got, want[name])
		}
	}
}

func TestIntegrationUnicodeAndReservedIdentifiers(t *testing.T) {
	a, db := openIntegration(t)
	name := "select ö `x` \"y\""
	q := quoteIdent(db) + "." + quoteIdent(name)
	mustExec(t, a, "DROP TABLE IF EXISTS "+q, "CREATE TABLE "+q+" (`order` INT PRIMARY KEY, `ğ \"`` col` VARCHAR(10))",
		"INSERT INTO "+q+" VALUES (1, 'ü')")
	t.Cleanup(func() { _, _ = a.db.Exec("DROP TABLE IF EXISTS " + q) })
	m := describe(t, a, database.TableRef{Schema: db, Name: name})
	rows := firstPage(t, a, m, nil)
	if len(rows) != 1 || text(rows[0][1]) != "ü" {
		t.Fatalf("rows %v", rows)
	}
	if err := a.SaveChanges(context.Background(), database.SaveRequest{Meta: m, Changes: database.ChangeSet{Rows: []database.RowChange{{
		Key:   []database.KeyValue{{Column: "order", Value: rows[0][0]}},
		Cells: []database.CellChange{{Column: "ğ \"` col", Original: rows[0][1], Current: grid.Value{Kind: grid.KindText, Text: "日本"}}},
	}}}}); err != nil {
		t.Fatal(err)
	}
	if rows := firstPage(t, a, m, nil); text(rows[0][1]) != "日本" {
		t.Errorf("rows %v", rows)
	}
}

func TestIntegrationPagingFilterSort(t *testing.T) {
	a, db := openIntegration(t)
	var vals []string
	for i := 1; i <= 25; i++ {
		v := "'n" + strconv.Itoa(i) + "'"
		if i%5 == 0 {
			v = "NULL"
		}
		vals = append(vals, "("+strconv.Itoa(i)+", "+v+")")
	}
	ref := newTable(t, a, db, "id INT PRIMARY KEY, v VARCHAR(10) NULL", "VALUES "+strings.Join(vals, ", "))
	m := describe(t, a, ref)

	// Keyset walk, page by page.
	var seen []string
	cur := database.Cursor{}
	for {
		res, err := a.FetchPage(context.Background(), database.PageRequest{Meta: m, Size: 10, Cursor: cur})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res.Rows {
			seen = append(seen, r[0].Text)
		}
		if !res.More {
			break
		}
		cur = database.Cursor{Dir: database.CursorAfter, Key: []string{res.Rows[len(res.Rows)-1][0].Text}}
	}
	if len(seen) != 25 || seen[0] != "1" || seen[24] != "25" {
		t.Errorf("walk %v", seen)
	}
	// The last page, read backwards.
	res, err := a.FetchPage(context.Background(), database.PageRequest{Meta: m, Size: 10, Cursor: database.Cursor{Dir: database.CursorLast}})
	if err != nil || len(res.Rows) != 10 || res.Rows[9][0].Text != "25" || res.Rows[0][0].Text != "16" {
		t.Errorf("last page %v %v", res, err)
	}
	// Contains is literal: '%' matches nothing.
	if rows := firstPage(t, a, m, &database.Filter{Column: "v", Op: database.OpContains, Value: "%"}); len(rows) != 0 {
		t.Errorf("contains %%: %v", rows)
	}
	if rows := firstPage(t, a, m, &database.Filter{Column: "v", Op: database.OpContains, Value: "N1"}); len(rows) != 9 {
		t.Errorf("contains N1 (case-insensitive): %d rows", len(rows))
	}
	if rows := firstPage(t, a, m, &database.Filter{Column: "v", Op: database.OpIsNull}); len(rows) != 5 {
		t.Errorf("is null: %d", len(rows))
	}
	if rows := firstPage(t, a, m, &database.Filter{Column: "id", Op: database.OpGreater, Value: "20"}); len(rows) != 5 {
		t.Errorf("greater: %d", len(rows))
	}
	// Sort by a nullable column, descending, with offset pages.
	res, err = a.FetchPage(context.Background(), database.PageRequest{Meta: m, Size: 10, Offset: 20, Sort: &database.Sort{Column: "v", Desc: true}})
	if err != nil || len(res.Rows) != 5 || res.More {
		t.Fatalf("sorted page %v %v", res, err)
	}
	for _, r := range res.Rows {
		if r[1].Kind != grid.KindNull {
			t.Errorf("NULLs sort last descending in MySQL; got %v", r)
		}
	}
}

func TestIntegrationFetchHonorsCancel(t *testing.T) {
	a, db := openIntegration(t)
	m := describe(t, a, newTable(t, a, db, "id INT PRIMARY KEY", "VALUES (1)"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := a.FetchPage(ctx, database.PageRequest{Meta: m, Size: 10})
	if database.KindOf(err) != database.KindCanceled {
		t.Errorf("got %v", err)
	}
}

func TestIntegrationPermissionDenied(t *testing.T) {
	a, db := openIntegration(t)
	open := newTable(t, a, db, "id INT PRIMARY KEY, v VARCHAR(5)", "VALUES (1, 'a')")
	hidden := tableFor(t) + "_hidden"
	mustExec(t, a, "DROP TABLE IF EXISTS "+quoteIdent(db)+"."+quoteIdent(hidden),
		"CREATE TABLE "+quoteIdent(db)+"."+quoteIdent(hidden)+" (id INT PRIMARY KEY)",
		"DROP USER IF EXISTS 'dbl_limited'@'%'",
		"CREATE USER 'dbl_limited'@'%' IDENTIFIED BY 'limited-pw-1'",
		"GRANT SELECT ON "+quoteTable(open)+" TO 'dbl_limited'@'%'")
	t.Cleanup(func() {
		_, _ = a.db.Exec("DROP USER IF EXISTS 'dbl_limited'@'%'")
		_, _ = a.db.Exec("DROP TABLE IF EXISTS " + quoteIdent(db) + "." + quoteIdent(hidden))
	})
	p, _ := integrationProfile(t)
	p.Username = "dbl_limited"
	lim := connectWith(t, p, "limited-pw-1")
	tables, err := lim.ListTables(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || tables[0].Ref.Name != open.Name {
		t.Errorf("the limited user sees %v", tables)
	}
	if _, err := lim.DescribeTable(context.Background(), database.TableRef{Schema: db, Name: hidden}); database.KindOf(err) != database.KindMetadataUnavailable {
		t.Errorf("hidden table: %v", err)
	}
	m := describe(t, lim, open)
	err = lim.SaveChanges(context.Background(), database.SaveRequest{Meta: m, Changes: change("1", "v", "a", "b")})
	var se *database.SaveError
	if !errors.As(err, &se) || se.Err.Kind != database.KindPermission || se.Outcome == database.SaveUnknown {
		t.Errorf("save without UPDATE privilege: %v", err)
	}
	s := openSession(t, lim)
	if _, err := s.Execute(context.Background(), "SELECT * FROM "+quoteIdent(db)+"."+quoteIdent(hidden)); database.KindOf(err) != database.KindPermission {
		t.Errorf("SQL on a hidden table: %v", err)
	}
}

// --- Save ---

// change is a one-cell change of the row whose id is id.
func change(id, column, from, to string) database.ChangeSet {
	cur := grid.Value{Kind: grid.KindText, Text: to}
	if to == "NULL" {
		cur = grid.Value{Kind: grid.KindNull}
	}
	orig := grid.Value{Kind: grid.KindText, Text: from}
	if from == "NULL" {
		orig = grid.Value{Kind: grid.KindNull}
	}
	return database.ChangeSet{Rows: []database.RowChange{{
		RowID: 1,
		Key:   []database.KeyValue{{Column: "id", Value: grid.Value{Kind: grid.KindNumber, Text: id}}},
		Cells: []database.CellChange{{Column: column, Original: orig, Current: cur}},
	}}}
}

func saveKind(err error) database.Kind {
	var se *database.SaveError
	if errors.As(err, &se) {
		return se.Err.Kind
	}
	return database.KindOf(err)
}

func valueOf(t *testing.T, a *Adapter, ref database.TableRef, column, id string) string {
	t.Helper()
	var v sql.NullString
	if err := a.db.QueryRow("SELECT CAST("+quoteIdent(column)+" AS CHAR) FROM "+quoteTable(ref)+" WHERE id = ?", id).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if !v.Valid {
		return "NULL"
	}
	return v.String
}

func TestIntegrationSaveUpdatesOneRow(t *testing.T) {
	a, db := openIntegration(t)
	ref := newTable(t, a, db, "id INT PRIMARY KEY, v VARCHAR(10)", "VALUES (1, 'a'), (2, 'b')")
	m := describe(t, a, ref)
	if err := a.SaveChanges(context.Background(), database.SaveRequest{Meta: m, Changes: change("1", "v", "a", "z")}); err != nil {
		t.Fatal(err)
	}
	if valueOf(t, a, ref, "v", "1") != "z" || valueOf(t, a, ref, "v", "2") != "b" {
		t.Error("wrong rows changed")
	}
}

func TestIntegrationSaveSameValueIsNotConflict(t *testing.T) {
	a, db := openIntegration(t)
	ref := newTable(t, a, db, "id INT PRIMARY KEY, v VARCHAR(10)", "VALUES (1, 'a')")
	m := describe(t, a, ref)
	if err := a.SaveChanges(context.Background(), database.SaveRequest{Meta: m, Changes: change("1", "v", "a", "a")}); err != nil {
		t.Fatalf("writing the value the cell holds: %v", err)
	}
}

func TestIntegrationSaveConflictOnCaseOrSpaceChange(t *testing.T) {
	a, db := openIntegration(t)
	collations := []string{"utf8mb4_general_ci", "utf8mb4_unicode_ci"}
	if !a.server.MariaDB {
		collations = append(collations, "utf8mb4_0900_ai_ci")
	}
	for _, coll := range collations {
		t.Run(coll, func(t *testing.T) {
			ref := newTable(t, a, db, "id INT PRIMARY KEY, v VARCHAR(10) COLLATE "+coll, "VALUES (1, 'abc'), (2, 'abc')")
			m := describe(t, a, ref)
			// Someone else changed only the case of one, and added a
			// trailing space to the other.
			mustExec(t, a, "UPDATE "+quoteTable(ref)+" SET v = 'ABC' WHERE id = 1", "UPDATE "+quoteTable(ref)+" SET v = 'abc ' WHERE id = 2")
			for _, id := range []string{"1", "2"} {
				err := a.SaveChanges(context.Background(), database.SaveRequest{Meta: m, Changes: change(id, "v", "abc", "mine")})
				if saveKind(err) != database.KindConflict {
					t.Errorf("row %s: got %v", id, err)
				}
			}
			if valueOf(t, a, ref, "v", "1") != "ABC" || valueOf(t, a, ref, "v", "2") != "abc " {
				t.Error("a conflicting save wrote")
			}
		})
	}
}

func TestIntegrationSaveLatin1ColumnNoFalseConflict(t *testing.T) {
	a, db := openIntegration(t)
	ref := newTable(t, a, db, "id INT PRIMARY KEY, v VARCHAR(10) CHARACTER SET latin1", "VALUES (1, 'café')")
	m := describe(t, a, ref)
	rows := firstPage(t, a, m, nil)
	if err := a.SaveChanges(context.Background(), database.SaveRequest{Meta: m, Changes: change("1", "v", text(rows[0][1]), "thé")}); err != nil {
		t.Fatal(err)
	}
	if got := valueOf(t, a, ref, "v", "1"); got != "thé" {
		t.Errorf("value %q", got)
	}
}

func TestIntegrationSaveCompositeKey(t *testing.T) {
	a, db := openIntegration(t)
	ref := newTable(t, a, db, "a INT, b VARCHAR(5), v INT, PRIMARY KEY (a, b)", "VALUES (1, 'x', 10), (1, 'y', 20)")
	m := describe(t, a, ref)
	cs := database.ChangeSet{Rows: []database.RowChange{{
		Key: []database.KeyValue{
			{Column: "a", Value: grid.Value{Kind: grid.KindNumber, Text: "1"}},
			{Column: "b", Value: grid.Value{Kind: grid.KindText, Text: "y"}},
		},
		Cells: []database.CellChange{{Column: "v", Original: grid.Value{Kind: grid.KindNumber, Text: "20"}, Current: grid.Value{Kind: grid.KindNumber, Text: "21"}}},
	}}}
	if err := a.SaveChanges(context.Background(), database.SaveRequest{Meta: m, Changes: cs}); err != nil {
		t.Fatal(err)
	}
	var v int
	if err := a.db.QueryRow("SELECT v FROM " + quoteTable(ref) + " WHERE a = 1 AND b = 'y'").Scan(&v); err != nil || v != 21 {
		t.Errorf("v %d %v", v, err)
	}
}

func TestIntegrationSaveNullAndTypedValues(t *testing.T) {
	a, db := openIntegration(t)
	ref := newTable(t, a, db, "id INT PRIMARY KEY, v VARCHAR(10) NULL, u BIGINT UNSIGNED, f FLOAT, d DECIMAL(10,2), dt DATETIME",
		"VALUES (1, NULL, 1, 0.1, 1.50, '2024-01-01 00:00:00')")
	m := describe(t, a, ref)
	steps := []struct{ col, from, to string }{
		{"v", "NULL", "x"}, {"v", "x", "NULL"},
		{"u", "1", "18446744073709551615"},
		{"f", "0.1", "0.2"}, {"f", "0.2", "0.3"},
		{"d", "1.50", "2.25"},
		{"dt", "2024-01-01 00:00:00", "2025-06-30 12:00:00"},
	}
	for _, s := range steps {
		cs := change("1", s.col, s.from, s.to)
		c, _ := m.Column(s.col)
		if c.Class == database.ClassNumber {
			for i := range cs.Rows[0].Cells {
				if cs.Rows[0].Cells[i].Current.Kind != grid.KindNull {
					cs.Rows[0].Cells[i].Current.Kind = grid.KindNumber
				}
			}
		}
		if err := a.SaveChanges(context.Background(), database.SaveRequest{Meta: m, Changes: cs}); err != nil {
			t.Fatalf("%s %s→%s: %v", s.col, s.from, s.to, err)
		}
		if got := valueOf(t, a, ref, s.col, "1"); got != s.to {
			t.Errorf("%s = %q, want %q", s.col, got, s.to)
		}
	}
	// One past the unsigned range is refused before anything is sent.
	err := a.SaveChanges(context.Background(), database.SaveRequest{Meta: m, Changes: change("1", "u", "18446744073709551615", "18446744073709551616")})
	if saveKind(err) != database.KindValidation {
		t.Errorf("out of range: %v", err)
	}
}

func TestIntegrationSaveRollsBackEverything(t *testing.T) {
	a, db := openIntegration(t)
	ref := newTable(t, a, db, "id INT PRIMARY KEY, v VARCHAR(10)", "VALUES (1, 'a'), (2, 'b')")
	m := describe(t, a, ref)
	cs := change("1", "v", "a", "first")
	second := change("2", "v", "stale", "second").Rows[0]
	second.RowID = 2
	cs.Rows = append(cs.Rows, second)
	err := a.SaveChanges(context.Background(), database.SaveRequest{Meta: m, Changes: cs})
	var se *database.SaveError
	if !errors.As(err, &se) || se.Err.Kind != database.KindConflict || !se.HasRow || se.RowID != 2 {
		t.Fatalf("got %v", err)
	}
	if valueOf(t, a, ref, "v", "1") != "a" {
		t.Error("the first row was written")
	}
}

func TestIntegrationSaveRejectsTruncationOnNonStrictServer(t *testing.T) {
	a, db := openIntegration(t)
	ref := newTable(t, a, db, "id INT PRIMARY KEY, v VARCHAR(3)", "VALUES (1, 'a')")
	var global string
	if err := a.db.QueryRow("SELECT @@GLOBAL.sql_mode").Scan(&global); err != nil {
		t.Fatal(err)
	}
	mustExec(t, a, "SET GLOBAL sql_mode = ''")
	t.Cleanup(func() { _, _ = a.db.Exec("SET GLOBAL sql_mode = ?", global) })
	p, pw := integrationProfile(t)
	lax := connectWith(t, p, pw) // new connections take the global mode
	m := describe(t, lax, ref)
	err := lax.SaveChanges(context.Background(), database.SaveRequest{Meta: m, Changes: change("1", "v", "a", "toolong")})
	if saveKind(err) != database.KindValidation {
		t.Errorf("got %v", err)
	}
	if valueOf(t, a, ref, "v", "1") != "a" {
		t.Error("the value was truncated and saved")
	}
	// The pooled connection is back in its own mode afterwards.
	var mode string
	if err := lax.db.QueryRow("SELECT @@SESSION.sql_mode").Scan(&mode); err != nil || mode != "" {
		t.Errorf("session mode after Save %q %v", mode, err)
	}
}

func TestIntegrationSaveCanceledBeforeCommit(t *testing.T) {
	a, db := openIntegration(t)
	ref := newTable(t, a, db, "id INT PRIMARY KEY, v VARCHAR(10)", "VALUES (1, 'a')")
	m := describe(t, a, ref)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := a.SaveChanges(ctx, database.SaveRequest{Meta: m, Changes: change("1", "v", "a", "z")})
	var se *database.SaveError
	if !errors.As(err, &se) || se.Err.Kind != database.KindCanceled || se.Outcome == database.SaveUnknown {
		t.Errorf("got %v", err)
	}
	if valueOf(t, a, ref, "v", "1") != "a" {
		t.Error("a canceled save wrote")
	}
}

// --- SQL tab ---

func openSession(t *testing.T, a *Adapter) *sqlSession {
	t.Helper()
	s, err := a.OpenSQLSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s.(*sqlSession)
}

func TestIntegrationSessionReadsServerStatus(t *testing.T) {
	a, _ := openIntegration(t)
	s := openSession(t, a)
	if _, err := s.status(); err != nil {
		t.Fatalf("the driver no longer exposes the server status: %v", err)
	}
	if _, err := s.Execute(context.Background(), "SET SESSION sql_mode = 'NO_BACKSLASH_ESCAPES'"); err != nil {
		t.Fatal(err)
	}
	sel, err := s.Select(database.SQLInput{Text: `SELECT 'x\'; SELECT 2`, Cursor: 0})
	if err != nil || sel.SQL != `SELECT 'x\';` {
		t.Errorf("selection under NO_BACKSLASH_ESCAPES: %q %v", sel.SQL, err)
	}
}

func TestIntegrationExecuteResultsAndAffectedRows(t *testing.T) {
	a, db := openIntegration(t)
	ref := newTable(t, a, db, "id INT PRIMARY KEY, v VARCHAR(10)", "VALUES (1, 'a'), (2, 'b'), (3, 'b')")
	s := openSession(t, a)
	r, err := s.Execute(context.Background(), "SELECT id, v FROM "+quoteTable(ref)+" ORDER BY id; UPDATE "+quoteTable(ref)+" SET v = 'b' WHERE v = 'b' OR id = 1")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Results) != 2 || len(r.Results[0].Rows) != 3 || r.Results[0].Columns[0].Kind != grid.KindNumber {
		t.Fatalf("results %+v", r.Results)
	}
	// Rows changed, as the mysql client reports them: two matched rows
	// already held 'b'.
	if u := r.Results[1]; !u.HasRowsAffected || u.RowsAffected != 1 || u.CommandTag != "UPDATE 1" {
		t.Errorf("update %+v", u)
	}
	if r.Transaction != database.SQLIdle || r.Stop != database.SQLCompleted {
		t.Errorf("state %v %v", r.Transaction, r.Stop)
	}
	r, err = s.Execute(context.Background(), "SELECT CAST('abc' AS SIGNED)")
	if err != nil || len(r.Notices) == 0 {
		t.Errorf("warnings: %v %v", r.Notices, err)
	}
	if _, err := s.Execute(context.Background(), "SELEC 1"); database.KindOf(err) != database.KindSyntax {
		t.Errorf("syntax: %v", err)
	}
}

func TestIntegrationTransactionState(t *testing.T) {
	a, db := openIntegration(t)
	ref := newTable(t, a, db, "id INT PRIMARY KEY, v VARCHAR(10)", "VALUES (1, 'a')")
	s := openSession(t, a)
	run := func(q string) *database.SQLExecution {
		t.Helper()
		r, err := s.Execute(context.Background(), q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return r
	}
	if r := run("BEGIN"); r.Transaction != database.SQLInTransaction {
		t.Errorf("after BEGIN: %v", r.Transaction)
	}
	if r := run("UPDATE " + quoteTable(ref) + " SET v = 'b'"); r.Transaction != database.SQLInTransaction {
		t.Errorf("after UPDATE: %v", r.Transaction)
	}
	// A failed statement leaves the transaction open.
	if r, _ := s.Execute(context.Background(), "SELECT * FROM dbl_no_such_table"); r.Transaction != database.SQLInTransaction {
		t.Errorf("after an error: %v", r.Transaction)
	}
	if r := run("COMMIT"); r.Transaction != database.SQLIdle {
		t.Errorf("after COMMIT: %v", r.Transaction)
	}
	// DDL commits on its own.
	run("START TRANSACTION")
	ddl := quoteIdent(db) + "." + quoteIdent(tableFor(t)+"_ddl")
	t.Cleanup(func() { _, _ = a.db.Exec("DROP TABLE IF EXISTS " + ddl) })
	if r := run("CREATE TABLE " + ddl + " (id INT)"); r.Transaction != database.SQLIdle {
		t.Errorf("after DDL: %v", r.Transaction)
	}
	// Autocommit off opens a transaction with the first statement.
	run("SET autocommit = 0")
	if r := run("UPDATE " + quoteTable(ref) + " SET v = 'c'"); r.Transaction != database.SQLInTransaction {
		t.Errorf("autocommit off: %v", r.Transaction)
	}
	run("ROLLBACK")
	run("SET autocommit = 1")
	if v := valueOf(t, a, ref, "v", "1"); v != "b" {
		t.Errorf("value %q", v)
	}
}

func TestIntegrationDeadlockEndsTransaction(t *testing.T) {
	a, db := openIntegration(t)
	ref := newTable(t, a, db, "id INT PRIMARY KEY, v INT", "VALUES (1, 0), (2, 0)")
	s1, s2 := openSession(t, a), openSession(t, a)
	upd := func(id int) string {
		return "UPDATE " + quoteTable(ref) + " SET v = v + 1 WHERE id = " + strconv.Itoa(id)
	}
	for _, q := range []struct {
		s *sqlSession
		q string
	}{{s1, "BEGIN"}, {s1, upd(1)}, {s2, "BEGIN"}, {s2, upd(2)}} {
		if _, err := q.s.Execute(context.Background(), q.q); err != nil {
			t.Fatal(err)
		}
	}
	type out struct {
		r   *database.SQLExecution
		err error
	}
	blocked := make(chan out, 1)
	go func() {
		r, err := s1.Execute(context.Background(), upd(2)) // waits for s2
		blocked <- out{r, err}
	}()
	time.Sleep(300 * time.Millisecond)
	r2, err2 := s2.Execute(context.Background(), upd(1)) // deadlock
	o := <-blocked
	victim, victimErr := r2, err2
	if err2 == nil {
		victim, victimErr = o.r, o.err
	}
	if database.KindOf(victimErr) != database.KindConflict || victim.Transaction != database.SQLIdle {
		t.Errorf("deadlock victim: %v %v", victimErr, victim.Transaction)
	}
	_, _ = s1.Execute(context.Background(), "ROLLBACK")
	_, _ = s2.Execute(context.Background(), "ROLLBACK")
}

func TestIntegrationCancelKeepsTheSession(t *testing.T) {
	a, _ := openIntegration(t)
	s := openSession(t, a)
	if _, err := s.Execute(context.Background(), "BEGIN"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	r, err := s.Execute(ctx, "SELECT SLEEP(30)")
	if time.Since(start) > 5*time.Second {
		t.Fatalf("cancel took %v", time.Since(start))
	}
	if err == nil || (r.Stop != database.SQLCanceled && r.Stop != database.SQLTimedOut) {
		t.Fatalf("stop %v err %v", r.Stop, err)
	}
	if r.Transaction != database.SQLInTransaction {
		t.Errorf("the session or its transaction was lost: %v", r.Transaction)
	}
	if r, err := s.Execute(context.Background(), "SELECT 1"); err != nil || r.Transaction != database.SQLInTransaction {
		t.Errorf("after cancel: %v %v", r, err)
	}
}

func TestIntegrationCloseRollsBack(t *testing.T) {
	a, db := openIntegration(t)
	ref := newTable(t, a, db, "id INT PRIMARY KEY, v VARCHAR(10)", "VALUES (1, 'a')")
	s := openSession(t, a)
	if _, err := s.Execute(context.Background(), "BEGIN; UPDATE "+quoteTable(ref)+" SET v = 'z'"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v := valueOf(t, a, ref, "v", "1"); v != "a" {
		t.Errorf("value %q after close", v)
	}
}

func TestIntegrationCreateProcedureAndCall(t *testing.T) {
	a, db := openIntegration(t)
	proc := quoteIdent(db) + "." + quoteIdent(tableFor(t)+"_p")
	t.Cleanup(func() { _, _ = a.db.Exec("DROP PROCEDURE IF EXISTS " + proc) })
	s := openSession(t, a)
	text := "DROP PROCEDURE IF EXISTS " + proc + ";\nCREATE PROCEDURE " + proc + "(IN n INT)\nBEGIN\n  DECLARE i INT DEFAULT 0;\n  WHILE i < n DO SET i = i + 1; END WHILE;\n  SELECT i AS total;\nEND;\nCALL " + proc + "(3);"
	sel, err := s.Select(database.SQLInput{Text: text, Cursor: strings.Index(text, "DECLARE")})
	if err != nil || !strings.HasPrefix(sel.SQL, "CREATE PROCEDURE") || !strings.HasSuffix(sel.SQL, "END;") {
		t.Fatalf("selection %q %v", sel.SQL, err)
	}
	r, err := s.Execute(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	var total string
	for _, res := range r.Results {
		if len(res.Columns) == 1 && res.Columns[0].Name == "total" {
			total = text0(res)
		}
	}
	if total != "3" {
		t.Errorf("CALL result %q; results %+v", total, r.Results)
	}
}

func text0(r database.SQLResult) string {
	if len(r.Rows) == 0 {
		return ""
	}
	return text(r.Rows[0][0])
}

// TestIntegrationTLSVerification needs the CA certificate of the server in
// DBL_TEST_MYSQL_CA (a MySQL container keeps it as /var/lib/mysql/ca.pem).
// The server's certificate names no IP address, so verify-full to an IP
// must fail, and never fall back to plaintext.
func TestIntegrationTLSVerification(t *testing.T) {
	p, pw := integrationProfile(t)
	ca := os.Getenv("DBL_TEST_MYSQL_CA")
	if ca == "" {
		t.Skip("DBL_TEST_MYSQL_CA is not set")
	}
	p.TLS = connection.TLSOptions{Mode: connection.TLSVerifyCA, RootCert: ca}
	a := connectWith(t, p, pw)
	var cipher string
	if err := a.db.QueryRow("SELECT VARIABLE_VALUE FROM performance_schema.session_status WHERE VARIABLE_NAME = 'Ssl_cipher'").Scan(&cipher); err != nil || cipher == "" {
		t.Errorf("verify-ca connection is not encrypted: %q %v", cipher, err)
	}
	p.TLS.Mode = connection.TLSVerifyFull
	_, err := Connect(context.Background(), p, pw)
	var de *database.Error
	if !errors.As(err, &de) || de.Kind != database.KindConnection || !strings.Contains(de.Message, "TLS handshake") {
		t.Errorf("verify-full to an address the certificate does not name: %v", err)
	}
	other := filepath.Join(t.TempDir(), "other.pem")
	if err := os.WriteFile(other, selfSignedPEM(t), 0o600); err != nil {
		t.Fatal(err)
	}
	p.TLS = connection.TLSOptions{Mode: connection.TLSVerifyCA, RootCert: other}
	if _, err := Connect(context.Background(), p, pw); database.KindOf(err) != database.KindConnection {
		t.Errorf("verify-ca with another CA: %v", err)
	}
}

// selfSignedPEM is a CA certificate that signed nothing the server has.
func selfSignedPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "DBL test other CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
