package storage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type FileIdentity struct {
	Path string
	info os.FileInfo
}

func (id FileIdentity) Same(other FileIdentity) bool {
	if id.Path == "" || other.Path == "" {
		return false
	}
	if id.Path == other.Path {
		return true
	}
	if id.info != nil && other.info != nil {
		return os.SameFile(id.info, other.info)
	}
	return runtime.GOOS == "windows" && strings.EqualFold(id.Path, other.Path)
}

func canonicalSQLPath(root, path string) (string, error) {
	if path == "" || strings.ContainsRune(path, 0) {
		return "", errors.New("Choose a valid SQL filename.")
	}
	if !filepath.IsAbs(path) {
		if root == "" || path == "." || path == ".." || filepath.Base(path) != path || strings.ContainsAny(path, "/\\") {
			return "", errors.New("Choose an absolute path, or a filename inside the queries directory.")
		}
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		lower := strings.ToLower(path)
		if strings.HasPrefix(lower, `\\.\`) || strings.Contains(lower, "globalroot") || strings.Contains(strings.TrimPrefix(path, filepath.VolumeName(path)), ":") {
			return "", errors.New("Devices and alternate file streams are not SQL files.")
		}
		name := strings.ToUpper(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
		if name == "CON" || name == "PRN" || name == "AUX" || name == "NUL" || len(name) == 4 && (strings.HasPrefix(name, "COM") || strings.HasPrefix(name, "LPT")) && name[3] >= '1' && name[3] <= '9' {
			return "", errors.New("Reserved device filenames are not SQL files.")
		}
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}
