package connection

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func validProfile() Profile {
	return Profile{
		Name: "Local PG", Driver: DriverPostgres, Host: "localhost", Port: 5432,
		Database: "app", Username: "postgres",
	}
}

func fieldsOf(errs []FieldError) []string {
	var out []string
	for _, e := range errs {
		out = append(out, e.Field)
	}
	return out
}

func TestNormalizedFillsDefaults(t *testing.T) {
	p := Profile{Name: "  x  ", Host: " h ", Database: " d ", Username: " u "}.Normalized()
	if p.Name != "x" || p.Host != "h" || p.Database != "d" || p.Username != "u" {
		t.Errorf("fields not trimmed: %+v", p)
	}
	if p.TLS.Mode != TLSRequire || p.ConnectTimeoutSec != DefaultConnectTimeoutSec {
		t.Errorf("defaults: tls=%q timeout=%d", p.TLS.Mode, p.ConnectTimeoutSec)
	}
	if p.Pool != (PoolOptions{MaxOpen: 2, MaxIdle: 1}) {
		t.Errorf("pool = %+v", p.Pool)
	}
	// An explicit pool of one open connection and no idle one is kept.
	q := Profile{Pool: PoolOptions{MaxOpen: 1}}.Normalized()
	if q.Pool != (PoolOptions{MaxOpen: 1, MaxIdle: 0}) {
		t.Errorf("explicit pool changed: %+v", q.Pool)
	}
}

func TestValidate(t *testing.T) {
	ok := validProfile().Normalized()
	if errs := ok.Validate(); len(errs) != 0 {
		t.Fatalf("valid profile rejected: %v", errs)
	}
	cases := []struct {
		name  string
		edit  func(*Profile)
		field string
	}{
		{"empty name", func(p *Profile) { p.Name = "" }, FieldName},
		{"long name", func(p *Profile) { p.Name = strings.Repeat("é", MaxNameLen+1) }, FieldName},
		{"unsupported driver", func(p *Profile) { p.Driver = DriverMySQL }, FieldDriver},
		{"empty driver", func(p *Profile) { p.Driver = "" }, FieldDriver},
		{"empty host", func(p *Profile) { p.Host = "" }, FieldHost},
		{"comma host", func(p *Profile) { p.Host = "a,b" }, FieldHost},
		{"socket path host", func(p *Profile) { p.Host = "/var/run/postgresql" }, FieldHost},
		{"windows path host", func(p *Profile) { p.Host = `C:\sock` }, FieldHost},
		{"host with space", func(p *Profile) { p.Host = "my host" }, FieldHost},
		{"host with slash", func(p *Profile) { p.Host = "h/x" }, FieldHost},
		{"port zero", func(p *Profile) { p.Port = 0 }, FieldPort},
		{"port too big", func(p *Profile) { p.Port = 65536 }, FieldPort},
		{"empty database", func(p *Profile) { p.Database = "" }, FieldDatabase},
		{"empty username", func(p *Profile) { p.Username = "" }, FieldUsername},
		{"timeout too long", func(p *Profile) { p.ConnectTimeoutSec = MaxConnectTimeoutSec + 1 }, FieldTimeout},
		{"negative timeout", func(p *Profile) { p.ConnectTimeoutSec = -1 }, FieldTimeout},
		{"idle above open", func(p *Profile) { p.Pool = PoolOptions{MaxOpen: 1, MaxIdle: 2} }, FieldPool},
		{"no open connections", func(p *Profile) { p.Pool = PoolOptions{MaxOpen: 0, MaxIdle: 0} }, FieldPool},
		{"unknown tls", func(p *Profile) { p.TLS.Mode = "prefer" }, FieldTLS},
		{"ca with require", func(p *Profile) { p.TLS.RootCert = "ca.pem" }, FieldRootCert},
	}
	for _, c := range cases {
		p := ok
		c.edit(&p)
		errs := p.Validate()
		found := false
		for _, e := range errs {
			if e.Field == c.field && e.Message != "" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want an error on %q, got %v", c.name, c.field, fieldsOf(errs))
		}
	}
}

func TestValidHostAcceptsRealHosts(t *testing.T) {
	for _, h := range []string{"localhost", "db-1.example.com", "10.0.0.5", "::1", "[::1]", "fe80::1%eth0", "dữ-liệu.example"} {
		p := validProfile().Normalized()
		p.Host = h
		for _, e := range p.Validate() {
			if e.Field == FieldHost {
				t.Errorf("host %q rejected: %s", h, e.Message)
			}
		}
	}
}

func TestValidateFilesReadsTheCertificate(t *testing.T) {
	p := validProfile().Normalized()
	p.TLS = TLSOptions{Mode: TLSVerifyFull, RootCert: filepath.Join(t.TempDir(), "missing.pem")}
	errs := p.ValidateFiles()
	if len(errs) != 1 || errs[0].Field != FieldRootCert {
		t.Fatalf("missing file: %v", errs)
	}
	if strings.Contains(errs[0].Message, p.TLS.RootCert) {
		t.Errorf("the message repeats the path: %q", errs[0].Message)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	p.TLS.RootCert = path
	if errs := p.ValidateFiles(); len(errs) != 0 {
		t.Errorf("readable file rejected: %v", errs)
	}
	p.TLS = TLSOptions{Mode: TLSRequire}
	if errs := p.ValidateFiles(); len(errs) != 0 {
		t.Errorf("no certificate needed: %v", errs)
	}
}

func TestNewIDIsUUIDv4AndUnique(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for i := 0; i < 10000; i++ {
		id := NewID()
		if !re.MatchString(id) {
			t.Fatalf("%q is not a version 4 UUID", id)
		}
		if seen[id] {
			t.Fatalf("%q generated twice", id)
		}
		seen[id] = true
	}
}

func TestFolderOperations(t *testing.T) {
	var c Catalog
	work, err := c.AddFolder("  Work ")
	if err != nil || work.Name != "Work" || work.ID == "" {
		t.Fatalf("AddFolder: %+v, %v", work, err)
	}
	if _, err := c.AddFolder("   "); err == nil {
		t.Error("an empty folder name was accepted")
	}
	var ve *ValidationError
	if _, err := c.AddFolder(""); !errors.As(err, &ve) {
		t.Errorf("error %v is not a ValidationError", err)
	}
	rev := c.Rev
	if err := c.RenameFolder(work.ID, "Job"); err != nil || c.Folders[0].Name != "Job" || c.Rev == rev {
		t.Errorf("RenameFolder: %v %+v rev %d->%d", err, c.Folders, rev, c.Rev)
	}
	if err := c.RenameFolder("nope", "x"); !errors.Is(err, ErrFolderNotFound) {
		t.Errorf("rename of unknown folder: %v", err)
	}
	if err := c.RenameFolder(work.ID, ""); err == nil {
		t.Error("rename to an empty name was accepted")
	}
	if c.Folders[0].Name != "Job" {
		t.Error("a rejected rename changed the folder")
	}
}

func TestDeleteFolderMovesProfilesToRoot(t *testing.T) {
	var c Catalog
	f, _ := c.AddFolder("Work")
	g, _ := c.AddFolder("Other")
	in := validProfile()
	in.FolderID = f.ID
	a, err := c.AddProfile(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Name = "b"
	b, _ := c.AddProfile(in)
	in.Name = "c"
	in.FolderID = g.ID
	cc, _ := c.AddProfile(in)

	moved, err := c.DeleteFolder(f.ID)
	if err != nil || moved != 2 {
		t.Fatalf("DeleteFolder: moved %d, %v", moved, err)
	}
	if _, ok := c.Folder(f.ID); ok || len(c.Folders) != 1 {
		t.Error("the folder is still there")
	}
	for _, id := range []string{a.ID, b.ID} {
		if p, ok := c.Profile(id); !ok || p.FolderID != "" {
			t.Errorf("profile %s: %+v %v, want at the root", id, p, ok)
		}
	}
	if p, _ := c.Profile(cc.ID); p.FolderID != g.ID {
		t.Error("a profile of another folder moved")
	}
	if _, err := c.DeleteFolder(f.ID); !errors.Is(err, ErrFolderNotFound) {
		t.Errorf("second delete: %v", err)
	}
	if got := c.ProfilesIn(""); len(got) != 2 {
		t.Errorf("ProfilesIn(root) = %d, want 2", len(got))
	}
}

func TestProfileOperations(t *testing.T) {
	var c Catalog
	p, err := c.AddProfile(validProfile())
	if err != nil || p.ID == "" || p.TLS.Mode != TLSRequire {
		t.Fatalf("AddProfile: %+v, %v", p, err)
	}
	bad := validProfile()
	bad.Host = ""
	var ve *ValidationError
	if _, err := c.AddProfile(bad); !errors.As(err, &ve) || len(ve.Fields) != 1 || ve.Fields[0].Field != FieldHost {
		t.Errorf("invalid profile: %v", err)
	}
	if len(c.Profiles) != 1 {
		t.Errorf("an invalid profile was added")
	}
	dup := validProfile()
	dup.ID = p.ID
	if _, err := c.AddProfile(dup); err == nil {
		t.Error("a repeated ID was accepted")
	}
	nested := validProfile()
	nested.FolderID = "nope"
	if _, err := c.AddProfile(nested); !errors.Is(err, ErrFolderNotFound) {
		t.Errorf("unknown folder: %v", err)
	}

	p.Host = "db.example.com"
	if _, err := c.UpdateProfile(p); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Profile(p.ID); got.Host != "db.example.com" {
		t.Errorf("update lost: %+v", got)
	}
	if _, err := c.UpdateProfile(Profile{ID: "nope"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update of unknown profile: %v", err)
	}
	if err := c.RenameProfile(p.ID, " Renamed "); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Profile(p.ID); got.Name != "Renamed" || got.Host != "db.example.com" {
		t.Errorf("rename changed more than the name: %+v", got)
	}
	if err := c.RenameProfile(p.ID, ""); err == nil {
		t.Error("empty rename accepted")
	}
	f, _ := c.AddFolder("F")
	if err := c.MoveProfile(p.ID, f.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.MoveProfile(p.ID, "nope"); !errors.Is(err, ErrFolderNotFound) {
		t.Errorf("move to unknown folder: %v", err)
	}
	if got, _ := c.Profile(p.ID); got.FolderID != f.ID {
		t.Error("a rejected move changed the folder")
	}
	if err := c.MoveProfile(p.ID, ""); err != nil {
		t.Fatal(err)
	}
	removed, err := c.DeleteProfile(p.ID)
	if err != nil || removed.ID != p.ID || len(c.Profiles) != 0 {
		t.Errorf("DeleteProfile: %+v %v", removed, err)
	}
	if _, err := c.DeleteProfile(p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
}

func TestEveryChangeBumpsRev(t *testing.T) {
	var c Catalog
	f, _ := c.AddFolder("F")
	p, _ := c.AddProfile(validProfile())
	steps := map[string]func(){
		"rename folder":  func() { _ = c.RenameFolder(f.ID, "G") },
		"rename profile": func() { _ = c.RenameProfile(p.ID, "N") },
		"move":           func() { _ = c.MoveProfile(p.ID, f.ID) },
		"update":         func() { _, _ = c.UpdateProfile(p) },
		"delete folder":  func() { _, _ = c.DeleteFolder(f.ID) },
		"delete profile": func() { _, _ = c.DeleteProfile(p.ID) },
	}
	for _, name := range []string{"rename folder", "rename profile", "move", "update", "delete folder", "delete profile"} {
		before := c.Rev
		steps[name]()
		if c.Rev <= before {
			t.Errorf("%s did not bump Rev", name)
		}
	}
}

func TestSnapshotSharesNothing(t *testing.T) {
	var c Catalog
	c.AddFolder("F")
	c.AddProfile(validProfile())
	s := c.Snapshot()
	c.Profiles[0].Name = "changed"
	c.Folders[0].Name = "changed"
	if s.Profiles[0].Name == "changed" || s.Folders[0].Name == "changed" || s.Rev != c.Rev {
		t.Errorf("snapshot follows the catalog: %+v", s)
	}
}

func TestNormalizeRepairsACatalog(t *testing.T) {
	good := validProfile()
	good.ID, good.FolderID = "a", "f1"
	orphan := validProfile()
	orphan.ID, orphan.FolderID, orphan.Name = "b", "gone", "Orphan"
	noID := validProfile()
	noID.ID, noID.Name = "", "NoID"
	dup := good
	dup.Name = "Dup"
	unsupported := Profile{ID: "u", Name: "Old MySQL", Driver: DriverMySQL}
	c := Catalog{
		Folders:  []Folder{{ID: "f1", Name: " Work "}, {ID: "", Name: "x"}, {ID: "f1", Name: "again"}},
		Profiles: []Profile{good, orphan, noID, dup, unsupported},
	}
	warnings := c.Normalize()
	if len(c.Folders) != 1 || c.Folders[0].Name != "Work" {
		t.Errorf("folders: %+v", c.Folders)
	}
	var ids []string
	for _, p := range c.Profiles {
		ids = append(ids, p.ID)
	}
	if strings.Join(ids, ",") != "a,b,u" {
		t.Errorf("profiles kept: %v", ids)
	}
	if p, _ := c.Profile("b"); p.FolderID != "" {
		t.Errorf("orphan profile is still in %q", p.FolderID)
	}
	if p, _ := c.Profile("u"); p.Driver != DriverMySQL {
		t.Error("a profile with an unsupported driver was changed")
	}
	if len(warnings) != 5 {
		t.Errorf("%d warnings, want 5: %q", len(warnings), warnings)
	}
}

func TestDriverAndTLSHelpers(t *testing.T) {
	if !DriverPostgres.Supported() || DriverMySQL.Supported() || DriverRedis.Supported() {
		t.Error("only PostgreSQL is supported in this version")
	}
	if DriverPostgres.Label() != "PostgreSQL" || DriverType("x").Label() != "x" {
		t.Error("labels")
	}
	for _, m := range TLSModes {
		if !m.Valid() {
			t.Errorf("%q listed but not valid", m)
		}
	}
	if TLSMode("prefer").Valid() || TLSMode("allow").Valid() {
		t.Error("prefer/allow must not be offered: they fall back to plaintext")
	}
	if !TLSVerifyCA.Verifies() || !TLSVerifyFull.Verifies() || TLSRequire.Verifies() || TLSDisable.Verifies() {
		t.Error("Verifies")
	}
}

func TestSecretRefUsesIDNotName(t *testing.T) {
	if got := SecretRefFor("abc"); got != "dbl/abc" {
		t.Errorf("SecretRefFor = %q", got)
	}
}

func TestDriversListsEveryDriverOnce(t *testing.T) {
	want := []DriverType{DriverPostgres, DriverSQLite, DriverMySQL, DriverSQLServer, DriverRedis}
	if len(Drivers) != len(want) {
		t.Fatalf("Drivers = %v", Drivers)
	}
	seen := map[DriverType]bool{}
	for i, d := range Drivers {
		if seen[d] {
			t.Errorf("%s is listed twice", d)
		}
		seen[d] = true
		if d != want[i] {
			t.Errorf("Drivers[%d] = %s, want %s", i, d, want[i])
		}
	}
	if Drivers[0] != DriverPostgres {
		t.Errorf("first driver %s", Drivers[0])
	}
}

func sqliteProfile() Profile {
	return Profile{Name: "Notes", Driver: DriverSQLite, SQLitePath: filepath.Join(os.TempDir(), "notes.db")}
}

func TestSQLiteProfileValidates(t *testing.T) {
	p := sqliteProfile().Normalized()
	if errs := p.Validate(); len(errs) != 0 {
		t.Errorf("errors: %+v", errs)
	}
}

func TestSQLiteProfileRequiresPath(t *testing.T) {
	p := sqliteProfile()
	p.SQLitePath = "  "
	errs := p.Normalized().Validate()
	if len(errs) != 1 || errs[0].Field != FieldPath {
		t.Errorf("errors: %+v", errs)
	}
	p = sqliteProfile()
	p.Name = ""
	errs = p.Normalized().Validate()
	if len(errs) != 1 || errs[0].Field != FieldName {
		t.Errorf("errors: %+v", errs)
	}
}

func TestSQLiteNormalizedClearsNetworkFields(t *testing.T) {
	p := sqliteProfile()
	p.Host, p.Port, p.Database, p.Username, p.SecretRef = "h", 5432, "d", "u", "dbl/x"
	p.TLS = TLSOptions{Mode: TLSRequire, RootCert: "ca.pem"}
	p.Pool = PoolOptions{MaxOpen: 4, MaxIdle: 2}
	p.SQLitePath = "  " + p.SQLitePath + " "
	got := p.Normalized()
	if got.Host != "" || got.Port != 0 || got.Database != "" || got.Username != "" || got.SecretRef != "" ||
		got.TLS != (TLSOptions{}) || got.Pool != (PoolOptions{}) {
		t.Errorf("kept network fields: %+v", got)
	}
	if got.SQLitePath != sqliteProfile().SQLitePath || got.ConnectTimeoutSec != DefaultConnectTimeoutSec {
		t.Errorf("path %q timeout %d", got.SQLitePath, got.ConnectTimeoutSec)
	}
}

func TestPostgresValidationUnchanged(t *testing.T) {
	p := Profile{Name: "x", Driver: DriverPostgres}.Normalized()
	fields := map[string]bool{}
	for _, fe := range p.Validate() {
		fields[fe.Field] = true
	}
	for _, want := range []string{FieldHost, FieldDatabase, FieldUsername} {
		if !fields[want] {
			t.Errorf("no error for %s", want)
		}
	}
	if fields[FieldPath] {
		t.Error("PostgreSQL asks for a file")
	}
}

func TestTargetForEachDriver(t *testing.T) {
	pg := Profile{Driver: DriverPostgres, Host: "db.internal", Database: "app", Username: "me"}
	if got := pg.Target(); got != "app (me@db.internal)" {
		t.Errorf("PostgreSQL target %q", got)
	}
	lite := Profile{Driver: DriverSQLite, SQLitePath: filepath.Join("data", "notes.db")}
	if got := lite.Target(); got != "notes.db" {
		t.Errorf("SQLite target %q", got)
	}
}

func TestValidateFilesSQLitePathMissing(t *testing.T) {
	p := Profile{Name: "n", Driver: DriverSQLite, SQLitePath: filepath.Join(t.TempDir(), "absent.db")}
	errs := p.ValidateFiles()
	if len(errs) != 1 || errs[0].Field != FieldPath {
		t.Errorf("errors: %+v", errs)
	}
	if _, err := os.Stat(p.SQLitePath); err == nil {
		t.Error("validating created the file")
	}
}

func TestValidateFilesSQLitePathIsDirectory(t *testing.T) {
	p := Profile{Name: "n", Driver: DriverSQLite, SQLitePath: t.TempDir()}
	errs := p.ValidateFiles()
	if len(errs) != 1 || errs[0].Field != FieldPath {
		t.Errorf("errors: %+v", errs)
	}
	file := filepath.Join(t.TempDir(), "ok.db")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p.SQLitePath = file
	if errs := p.ValidateFiles(); len(errs) != 0 {
		t.Errorf("an existing file was refused: %+v", errs)
	}
}
