package storage

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

const SQLFileMaxBytes = 8 << 20

type FileRevision struct {
	Identity FileIdentity
	Exists   bool
	Digest   [32]byte
}

func (r FileRevision) Same(other FileRevision) bool { return sameRevision(r, other) }

type SQLFile struct {
	Path       string
	Revision   FileRevision
	Raw        []byte
	Text       string
	BOM        bool
	LineEnding string
}
type SQLWriteRequest struct {
	Path     string
	Data     []byte
	Expected FileRevision
}
type SQLFiles interface {
	Read(string) (*SQLFile, error)
	Inspect(string) (FileRevision, error)
	Write(SQLWriteRequest) (*SQLFile, error)
}
type FileConflict struct{ Current FileRevision }

func (*FileConflict) Error() string {
	return "The SQL file changed outside this tab. Confirm overwrite or cancel."
}

type FileWriteError struct {
	Err       error
	Uncertain bool
}

func (e *FileWriteError) Error() string {
	if e.Uncertain {
		return "The file replacement could not be confirmed. Check the file before saving again: " + e.Err.Error()
	}
	return e.Err.Error()
}
func (e *FileWriteError) Unwrap() error { return e.Err }

type SQLStore struct {
	dir     string
	mu      sync.Mutex
	replace func(string, string, bool) (bool, error)
}

func NewSQLStore(dir string) *SQLStore { return &SQLStore{dir: dir} }
func DefaultQueriesDir() (string, error) {
	dir, err := DefaultDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "queries"), nil
}

func decodeSQL(path string, data []byte, info os.FileInfo) (*SQLFile, error) {
	if len(data) > SQLFileMaxBytes || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, errors.New("SQL files must be UTF-8 without NUL and at most 8 MiB.")
	}
	f := &SQLFile{Path: path, Raw: data, LineEnding: "\n", Revision: FileRevision{Identity: FileIdentity{Path: path, info: info}, Exists: true, Digest: sha256.Sum256(data)}}
	text := data
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		f.BOM = true
		text = data[3:]
	}
	crlf := bytes.Count(text, []byte("\r\n"))
	lf := bytes.Count(text, []byte("\n"))
	if crlf > 0 && crlf*2 >= lf {
		f.LineEnding = "\r\n"
	}
	f.Text = strings.ReplaceAll(string(text), "\r\n", "\n")
	return f, nil
}
func readSQL(path string) (*SQLFile, error) {
	before, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("The SQL path must point to a regular file.")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("The SQL path must point to a regular file.")
	}
	if info.Size() > SQLFileMaxBytes {
		return nil, errors.New("The SQL file exceeds 8 MiB.")
	}
	data, err := io.ReadAll(io.LimitReader(f, SQLFileMaxBytes+1))
	if err != nil {
		return nil, err
	}
	return decodeSQL(path, data, info)
}
func (s *SQLStore) Read(path string) (*SQLFile, error) {
	canonical, err := canonicalSQLPath(s.dir, path)
	if err != nil {
		return nil, err
	}
	return readSQL(canonical)
}
func inspectSQL(path string) (FileRevision, error) {
	f, err := readSQL(path)
	if errors.Is(err, os.ErrNotExist) {
		return FileRevision{Identity: FileIdentity{Path: path}}, nil
	}
	if err != nil {
		return FileRevision{}, err
	}
	return f.Revision, nil
}
func (s *SQLStore) Inspect(path string) (FileRevision, error) {
	canonical, err := canonicalSQLPath(s.dir, path)
	if err != nil {
		return FileRevision{}, err
	}
	return inspectSQL(canonical)
}
func EncodeSQL(file *SQLFile, text string) ([]byte, error) {
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return nil, errors.New("SQL must be valid UTF-8 without NUL.")
	}
	var data []byte
	if file != nil && text == file.Text {
		data = append([]byte(nil), file.Raw...)
	} else {
		if file != nil && file.LineEnding == "\r\n" {
			text = strings.ReplaceAll(text, "\n", "\r\n")
		}
		if file != nil && file.BOM {
			data = append(data, 0xef, 0xbb, 0xbf)
		}
		data = append(data, []byte(text)...)
	}
	if len(data) > SQLFileMaxBytes {
		return nil, errors.New("The SQL document exceeds 8 MiB.")
	}
	return data, nil
}
func sameRevision(a, b FileRevision) bool {
	return a.Exists == b.Exists && a.Identity.Same(b.Identity) && (!a.Exists || a.Digest == b.Digest && (a.Identity.info == nil || b.Identity.info == nil || os.SameFile(a.Identity.info, b.Identity.info)))
}
func (s *SQLStore) Write(req SQLWriteRequest) (*SQLFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := decodeSQL(req.Path, req.Data, nil); err != nil {
		return nil, err
	}
	path, err := canonicalSQLPath(s.dir, req.Path)
	if err != nil {
		return nil, err
	}
	current, err := inspectSQL(path)
	if err != nil {
		return nil, err
	}
	if !sameRevision(current, req.Expected) {
		return nil, &FileConflict{Current: current}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sbdb-query-*.tmp")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	defer tmp.Close()
	if current.Identity.info != nil {
		if err = tmp.Chmod(current.Identity.info.Mode().Perm()); err != nil {
			return nil, err
		}
	}
	if _, err = tmp.Write(req.Data); err != nil {
		return nil, err
	}
	if err = tmp.Sync(); err != nil {
		return nil, err
	}
	if err = tmp.Close(); err != nil {
		return nil, err
	}
	latest, err := inspectSQL(path)
	if err != nil {
		return nil, err
	}
	if !sameRevision(latest, req.Expected) {
		return nil, &FileConflict{Current: latest}
	}
	replace := s.replace
	if replace == nil {
		replace = replaceSQLFile
	}
	replaced, err := replace(tmpPath, path, current.Exists)
	if err != nil {
		out, readErr := readSQL(path)
		if readErr == nil && bytes.Equal(out.Raw, req.Data) {
			return out, nil
		}
		return nil, &FileWriteError{Err: fmt.Errorf("replace SQL file: %w", err), Uncertain: replaced || current.Exists}
	}
	out, err := readSQL(path)
	if err != nil {
		return nil, &FileWriteError{Err: err, Uncertain: true}
	}
	if !bytes.Equal(out.Raw, req.Data) {
		return nil, &FileConflict{Current: out.Revision}
	}
	return out, nil
}
