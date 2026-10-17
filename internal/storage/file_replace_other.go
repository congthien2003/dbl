//go:build !windows

package storage

import "os"

func replaceSQLFile(tmp, target string, exists bool) (bool, error) {
	if exists {
		err := os.Rename(tmp, target)
		return err == nil, err
	}
	if err := os.Link(tmp, target); err != nil {
		return false, err
	}
	if err := os.Remove(tmp); err != nil {
		return true, err
	}
	return true, nil
}
