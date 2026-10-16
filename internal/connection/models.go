// Package connection holds the saved connection profiles and folders, and
// the manager that opens and closes their sessions. It does not import any
// UI package.
package connection

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DriverType names a database engine (spec 6.1).
type DriverType string

// Driver types. Only PostgreSQL can connect in this version; the others are
// kept so a profile written for them is preserved, not lost.
const (
	DriverPostgres  DriverType = "postgres"
	DriverMySQL     DriverType = "mysql"
	DriverSQLServer DriverType = "sqlserver"
	DriverSQLite    DriverType = "sqlite"
	DriverRedis     DriverType = "redis"
)

// Supported reports whether this version can connect with the driver.
func (d DriverType) Supported() bool { return d == DriverPostgres }

// Label is the name shown to the user.
func (d DriverType) Label() string {
	switch d {
	case DriverPostgres:
		return "PostgreSQL"
	case DriverMySQL:
		return "MySQL"
	case DriverSQLServer:
		return "SQL Server"
	case DriverSQLite:
		return "SQLite"
	case DriverRedis:
		return "Redis"
	}
	return string(d)
}

// TLSMode is how a network connection is secured. There is no "prefer" or
// "allow": a connection never falls back to plaintext on its own.
type TLSMode string

// TLS modes.
const (
	TLSDisable    TLSMode = "disable"
	TLSRequire    TLSMode = "require"     // encrypted, certificate not verified
	TLSVerifyCA   TLSMode = "verify-ca"   // certificate chain verified
	TLSVerifyFull TLSMode = "verify-full" // chain and host name verified
)

// DefaultTLSMode is the mode of a new profile.
const DefaultTLSMode = TLSRequire

// TLSModes lists the modes in the order the form offers them.
var TLSModes = []TLSMode{TLSDisable, TLSRequire, TLSVerifyCA, TLSVerifyFull}

// Valid reports whether m is a known mode.
func (m TLSMode) Valid() bool {
	switch m {
	case TLSDisable, TLSRequire, TLSVerifyCA, TLSVerifyFull:
		return true
	}
	return false
}

// Verifies reports whether the mode checks the server certificate, which
// is when a root certificate file may be given.
func (m TLSMode) Verifies() bool { return m == TLSVerifyCA || m == TLSVerifyFull }

// Defaults and limits of a profile.
const (
	DefaultPort              = 5432
	DefaultConnectTimeoutSec = 10
	MaxConnectTimeoutSec     = 300
	DefaultMaxOpen           = 2
	DefaultMaxIdle           = 1
	MaxNameLen               = 128
)

// TLSOptions are the typed transport options of a profile.
type TLSOptions struct {
	Mode     TLSMode `json:"mode"`
	RootCert string  `json:"rootCert,omitempty"` // PEM file with the CA, for verify-*
}

// PoolOptions bound the connection pool of a profile.
type PoolOptions struct {
	MaxOpen int `json:"maxOpen"`
	MaxIdle int `json:"maxIdle"`
}

// Profile is one saved connection (spec 6.1). It never holds the password:
// SecretRef names it in the credential store, and an empty SecretRef means
// the profile connects without a password.
type Profile struct {
	ID                string      `json:"id"`
	Name              string      `json:"name"`
	FolderID          string      `json:"folderId,omitempty"`
	Driver            DriverType  `json:"driver"`
	Host              string      `json:"host"`
	Port              int         `json:"port"`
	Database          string      `json:"database"`
	Username          string      `json:"username"`
	SecretRef         string      `json:"secretRef,omitempty"`
	TLS               TLSOptions  `json:"tls"`
	ConnectTimeoutSec int         `json:"connectTimeoutSec"`
	Pool              PoolOptions `json:"pool"`
}

// Folder groups connections. Folders do not nest.
type Folder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Normalized returns p with surrounding spaces removed and the defaults of
// missing options filled in.
func (p Profile) Normalized() Profile {
	p.Name = strings.TrimSpace(p.Name)
	p.Host = strings.TrimSpace(p.Host)
	p.Database = strings.TrimSpace(p.Database)
	p.Username = strings.TrimSpace(p.Username)
	p.TLS.RootCert = strings.TrimSpace(p.TLS.RootCert)
	if p.TLS.Mode == "" {
		p.TLS.Mode = DefaultTLSMode
	}
	if p.ConnectTimeoutSec == 0 {
		p.ConnectTimeoutSec = DefaultConnectTimeoutSec
	}
	if p.Pool.MaxOpen == 0 && p.Pool.MaxIdle == 0 {
		p.Pool = PoolOptions{MaxOpen: DefaultMaxOpen, MaxIdle: DefaultMaxIdle}
	}
	return p
}

// Field names used by FieldError.
const (
	FieldName     = "name"
	FieldDriver   = "driver"
	FieldHost     = "host"
	FieldPort     = "port"
	FieldDatabase = "database"
	FieldUsername = "username"
	FieldTimeout  = "timeout"
	FieldPool     = "pool"
	FieldTLS      = "tls"
	FieldRootCert = "rootCert"
	FieldPassword = "password"
)

// MaxPasswordBytes is the longest password that can be saved: MyGo's
// credential store keeps at most 2560 bytes per secret.
const MaxPasswordBytes = 2000

// ValidatePassword checks a password typed in the form.
func ValidatePassword(pw string) *FieldError {
	if len(pw) > MaxPasswordBytes {
		return &FieldError{FieldPassword, fmt.Sprintf("Password is longer than %d bytes", MaxPasswordBytes)}
	}
	return nil
}

// FieldError is one problem found in a form field.
type FieldError struct {
	Field   string
	Message string
}

// ValidationError is the error of a rejected profile or name.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		parts[i] = f.Field + ": " + f.Message
	}
	return "invalid connection: " + strings.Join(parts, "; ")
}

// Errors of the catalog.
var (
	ErrNotFound       = errors.New("connection not found")
	ErrFolderNotFound = errors.New("folder not found")
)

// ValidateName checks a profile or folder name that is already trimmed.
func ValidateName(name string) *FieldError {
	switch {
	case name == "":
		return &FieldError{FieldName, "Name is required"}
	case utf8.RuneCountInString(name) > MaxNameLen:
		return &FieldError{FieldName, fmt.Sprintf("Name is longer than %d characters", MaxNameLen)}
	}
	return nil
}

// validHost reports whether h can be a host name or IP address. A comma
// would make several hosts, and a path a Unix socket; neither is supported.
func validHost(h string) bool {
	for _, r := range h {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
		case strings.ContainsRune(".-_:[]%", r):
		default:
			return false
		}
	}
	return true
}

// Validate checks the profile as the form must: required fields and ranges.
// It reads no file; ValidateFiles checks the certificate file.
func (p Profile) Validate() []FieldError {
	var errs []FieldError
	if fe := ValidateName(p.Name); fe != nil {
		errs = append(errs, *fe)
	}
	if !p.Driver.Supported() {
		errs = append(errs, FieldError{FieldDriver, fmt.Sprintf("%s connections are not supported yet", p.Driver.Label())})
	}
	switch {
	case p.Host == "":
		errs = append(errs, FieldError{FieldHost, "Host is required"})
	case !validHost(p.Host):
		errs = append(errs, FieldError{FieldHost, "Host must be one host name or IP address"})
	}
	if p.Port < 1 || p.Port > 65535 {
		errs = append(errs, FieldError{FieldPort, "Port must be between 1 and 65535"})
	}
	if p.Database == "" {
		errs = append(errs, FieldError{FieldDatabase, "Database is required"})
	}
	if p.Username == "" {
		errs = append(errs, FieldError{FieldUsername, "Username is required"})
	}
	if p.ConnectTimeoutSec < 1 || p.ConnectTimeoutSec > MaxConnectTimeoutSec {
		errs = append(errs, FieldError{FieldTimeout, fmt.Sprintf("Timeout must be between 1 and %d seconds", MaxConnectTimeoutSec)})
	}
	if p.Pool.MaxOpen < 1 || p.Pool.MaxIdle < 0 || p.Pool.MaxIdle > p.Pool.MaxOpen {
		errs = append(errs, FieldError{FieldPool, "Pool needs at least one connection and no more idle than open"})
	}
	if !p.TLS.Mode.Valid() {
		errs = append(errs, FieldError{FieldTLS, "Unknown TLS mode"})
	}
	if p.TLS.RootCert != "" && !p.TLS.Mode.Verifies() {
		errs = append(errs, FieldError{FieldRootCert, "A CA certificate is used only with verify-ca or verify-full"})
	}
	return errs
}

// ValidateFiles checks that the CA certificate file, when the mode uses
// one, can be opened.
func (p Profile) ValidateFiles() []FieldError {
	if p.TLS.RootCert == "" || !p.TLS.Mode.Verifies() {
		return nil
	}
	f, err := os.Open(p.TLS.RootCert)
	if err != nil {
		return []FieldError{{FieldRootCert, "CA certificate cannot be read: " + pathError(err)}}
	}
	_ = f.Close()
	return nil
}

// pathError describes a file error without repeating the path.
func pathError(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// NewID returns a random version 4 UUID.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // never fails since Go 1.24
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Catalog is the saved folders and profiles, in display order. The zero
// value is an empty catalog. Rev grows with every change so views can tell
// when to rebuild.
type Catalog struct {
	Folders  []Folder
	Profiles []Profile
	Rev      int
}

// Snapshot returns a copy that shares nothing with c, for a background
// writer.
func (c *Catalog) Snapshot() Catalog {
	return Catalog{
		Folders:  append([]Folder(nil), c.Folders...),
		Profiles: append([]Profile(nil), c.Profiles...),
		Rev:      c.Rev,
	}
}

// Folder returns the folder with the given ID.
func (c *Catalog) Folder(id string) (Folder, bool) {
	for _, f := range c.Folders {
		if f.ID == id {
			return f, true
		}
	}
	return Folder{}, false
}

// Profile returns the profile with the given ID.
func (c *Catalog) Profile(id string) (Profile, bool) {
	if i := c.profileIndex(id); i >= 0 {
		return c.Profiles[i], true
	}
	return Profile{}, false
}

func (c *Catalog) profileIndex(id string) int {
	for i := range c.Profiles {
		if c.Profiles[i].ID == id {
			return i
		}
	}
	return -1
}

func (c *Catalog) folderIndex(id string) int {
	for i := range c.Folders {
		if c.Folders[i].ID == id {
			return i
		}
	}
	return -1
}

// ProfilesIn returns the profiles of a folder, or those at the root for "".
func (c *Catalog) ProfilesIn(folderID string) []Profile {
	var out []Profile
	for _, p := range c.Profiles {
		if p.FolderID == folderID {
			out = append(out, p)
		}
	}
	return out
}

// AddFolder adds a folder at the end.
func (c *Catalog) AddFolder(name string) (Folder, error) {
	name = strings.TrimSpace(name)
	if fe := ValidateName(name); fe != nil {
		return Folder{}, &ValidationError{Fields: []FieldError{*fe}}
	}
	f := Folder{ID: NewID(), Name: name}
	c.Folders = append(c.Folders, f)
	c.Rev++
	return f, nil
}

// RenameFolder changes the name of a folder.
func (c *Catalog) RenameFolder(id, name string) error {
	i := c.folderIndex(id)
	if i < 0 {
		return ErrFolderNotFound
	}
	name = strings.TrimSpace(name)
	if fe := ValidateName(name); fe != nil {
		return &ValidationError{Fields: []FieldError{*fe}}
	}
	c.Folders[i].Name = name
	c.Rev++
	return nil
}

// DeleteFolder removes a folder and moves its profiles to the root. It
// returns how many profiles moved.
func (c *Catalog) DeleteFolder(id string) (moved int, err error) {
	i := c.folderIndex(id)
	if i < 0 {
		return 0, ErrFolderNotFound
	}
	for j := range c.Profiles {
		if c.Profiles[j].FolderID == id {
			c.Profiles[j].FolderID = ""
			moved++
		}
	}
	c.Folders = append(c.Folders[:i:i], c.Folders[i+1:]...)
	c.Rev++
	return moved, nil
}

func (c *Catalog) checkFolder(id string) error {
	if id != "" && c.folderIndex(id) < 0 {
		return ErrFolderNotFound
	}
	return nil
}

// AddProfile validates p and adds it at the end. A missing ID is generated.
// The stored, normalized profile is returned.
func (c *Catalog) AddProfile(p Profile) (Profile, error) {
	p = p.Normalized()
	if p.ID == "" {
		p.ID = NewID()
	}
	if c.profileIndex(p.ID) >= 0 {
		return Profile{}, fmt.Errorf("connection %s already exists", p.ID)
	}
	if errs := p.Validate(); len(errs) > 0 {
		return Profile{}, &ValidationError{Fields: errs}
	}
	if err := c.checkFolder(p.FolderID); err != nil {
		return Profile{}, err
	}
	c.Profiles = append(c.Profiles, p)
	c.Rev++
	return p, nil
}

// UpdateProfile replaces the profile with the same ID by the validated p.
func (c *Catalog) UpdateProfile(p Profile) (Profile, error) {
	i := c.profileIndex(p.ID)
	if i < 0 {
		return Profile{}, ErrNotFound
	}
	p = p.Normalized()
	if errs := p.Validate(); len(errs) > 0 {
		return Profile{}, &ValidationError{Fields: errs}
	}
	if err := c.checkFolder(p.FolderID); err != nil {
		return Profile{}, err
	}
	c.Profiles[i] = p
	c.Rev++
	return p, nil
}

// RenameProfile changes only the name of a profile, whatever else it holds.
func (c *Catalog) RenameProfile(id, name string) error {
	i := c.profileIndex(id)
	if i < 0 {
		return ErrNotFound
	}
	name = strings.TrimSpace(name)
	if fe := ValidateName(name); fe != nil {
		return &ValidationError{Fields: []FieldError{*fe}}
	}
	c.Profiles[i].Name = name
	c.Rev++
	return nil
}

// MoveProfile moves a profile into a folder, or to the root for "".
func (c *Catalog) MoveProfile(id, folderID string) error {
	i := c.profileIndex(id)
	if i < 0 {
		return ErrNotFound
	}
	if err := c.checkFolder(folderID); err != nil {
		return err
	}
	c.Profiles[i].FolderID = folderID
	c.Rev++
	return nil
}

// DeleteProfile removes a profile and returns it, so the caller can remove
// its secret.
func (c *Catalog) DeleteProfile(id string) (Profile, error) {
	i := c.profileIndex(id)
	if i < 0 {
		return Profile{}, ErrNotFound
	}
	p := c.Profiles[i]
	c.Profiles = append(c.Profiles[:i:i], c.Profiles[i+1:]...)
	c.Rev++
	return p, nil
}

// Normalize repairs a catalog read from disk: entries without an ID or with
// a repeated one are dropped, a folder that does not exist becomes the root,
// and each profile is normalized. Profiles that fail validation (an
// unsupported driver, say) are kept so the file does not lose them. It
// returns a warning for each repair.
func (c *Catalog) Normalize() (warnings []string) {
	seen := map[string]bool{}
	folders := c.Folders[:0:0]
	for _, f := range c.Folders {
		f.Name = strings.TrimSpace(f.Name)
		switch {
		case f.ID == "":
			warnings = append(warnings, "a folder without an ID was dropped")
		case seen[f.ID]:
			warnings = append(warnings, fmt.Sprintf("folder %q was listed twice", f.Name))
		default:
			seen[f.ID] = true
			folders = append(folders, f)
		}
	}
	c.Folders = folders

	seenProfile := map[string]bool{}
	profiles := c.Profiles[:0:0]
	for _, p := range c.Profiles {
		p = p.Normalized()
		switch {
		case p.ID == "":
			warnings = append(warnings, fmt.Sprintf("connection %q has no ID and was dropped", p.Name))
			continue
		case seenProfile[p.ID]:
			warnings = append(warnings, fmt.Sprintf("connection %q was listed twice", p.Name))
			continue
		}
		seenProfile[p.ID] = true
		if p.FolderID != "" && !seen[p.FolderID] {
			warnings = append(warnings, fmt.Sprintf("connection %q was in a folder that no longer exists and moved to the root", p.Name))
			p.FolderID = ""
		}
		profiles = append(profiles, p)
	}
	c.Profiles = profiles
	return warnings
}
