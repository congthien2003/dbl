package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"dbl/internal/connection"
)

// ConnectionsVersion is the connections.json format this build reads and
// writes.
const ConnectionsVersion = 1

const connectionsFile = "connections.json"

// Errors of Load. Each of them except ErrConnectionsCorrupt leaves the
// store read-only, so the file is never overwritten.
var (
	// ErrConnectionsCorrupt means connections.json could not be parsed; the
	// file was moved aside to connections.json.bad.
	ErrConnectionsCorrupt = errors.New("connections file is corrupt")
	// ErrConnectionsVersion means connections.json was written by a newer
	// version.
	ErrConnectionsVersion = errors.New("connections file has an unsupported version")
	// ErrConnectionsUnreadable means connections.json exists but could not be
	// read, so its content is unknown.
	ErrConnectionsUnreadable = errors.New("connections file could not be read")
	// ErrReadOnly is what Save returns when the file must not be written.
	ErrReadOnly = errors.New("connections are read-only")
)

// ConnectionsFile is the content of connections.json (spec 11.2).
type ConnectionsFile struct {
	Version     int                  `json:"version"`
	Folders     []connection.Folder  `json:"folders"`
	Connections []connection.Profile `json:"connections"`
}

// ConnectionsStore reads and writes connections.json in one directory.
//
// Unlike the settings, a missing catalog and an unknown one must not look
// the same: when the file exists but cannot be understood, the store turns
// read-only. Otherwise the first profile the user created would replace
// every profile already saved.
type ConnectionsStore struct {
	dir string

	mu     sync.Mutex
	reason string // why the store is read-only, "" when it is not
}

// NewConnectionsStore returns a store for the file in dir.
func NewConnectionsStore(dir string) *ConnectionsStore {
	return &ConnectionsStore{dir: dir}
}

func (st *ConnectionsStore) path() string { return filepath.Join(st.dir, connectionsFile) }

func (st *ConnectionsStore) lock(reason string) {
	st.mu.Lock()
	st.reason = reason
	st.mu.Unlock()
}

// ReadOnly reports whether Save refuses to write, and why.
func (st *ConnectionsStore) ReadOnly() (reason string, readOnly bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.reason, st.reason != ""
}

// Load reads the catalog.
//
//   - A missing file gives an empty catalog and no error.
//   - A file that is not valid JSON is moved to connections.json.bad and
//     gives an empty catalog and ErrConnectionsCorrupt; saving is allowed.
//   - A newer version, or any other failure to read the file, gives an
//     empty catalog and an error, and the store becomes read-only.
//
// The warnings describe repairs made to a readable file.
func (st *ConnectionsStore) Load() (connection.Catalog, []string, error) {
	data, err := os.ReadFile(st.path())
	if errors.Is(err, fs.ErrNotExist) {
		return connection.Catalog{}, nil, nil
	}
	if err != nil {
		st.lock("connections.json could not be read")
		return connection.Catalog{}, nil, fmt.Errorf("%w: %v", ErrConnectionsUnreadable, err)
	}
	var f ConnectionsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return connection.Catalog{}, nil, st.quarantine(err)
	}
	if f.Version > ConnectionsVersion {
		st.lock("connections.json was written by a newer version")
		return connection.Catalog{}, nil, fmt.Errorf("%w: file is version %d, this build supports %d",
			ErrConnectionsVersion, f.Version, ConnectionsVersion)
	}
	cat := connection.Catalog{Folders: f.Folders, Profiles: f.Connections}
	warnings := cat.Normalize()
	return cat, warnings, nil
}

// quarantine moves the unparsable file aside so the next Save does not
// destroy it.
func (st *ConnectionsStore) quarantine(cause error) error {
	bad := st.path() + backupSuffix
	_ = os.Remove(bad)
	if err := os.Rename(st.path(), bad); err != nil {
		// The file cannot be kept, and saving would destroy it.
		st.lock("connections.json is damaged and could not be set aside")
		return fmt.Errorf("%w: %v (could not keep a backup: %v)", ErrConnectionsCorrupt, cause, err)
	}
	return fmt.Errorf("%w: %v (kept as %s)", ErrConnectionsCorrupt, cause, filepath.Base(bad))
}

// Save writes the catalog atomically. It refuses while the store is
// read-only.
func (st *ConnectionsStore) Save(c connection.Catalog) error {
	if reason, ro := st.ReadOnly(); ro {
		return fmt.Errorf("%w: %s", ErrReadOnly, reason)
	}
	f := ConnectionsFile{Version: ConnectionsVersion, Folders: c.Folders, Connections: c.Profiles}
	if f.Folders == nil {
		f.Folders = []connection.Folder{}
	}
	if f.Connections == nil {
		f.Connections = []connection.Profile{}
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	st.mu.Lock()
	defer st.mu.Unlock()
	return writeFileAtomic(st.dir, connectionsFile, data)
}
