// Package fsutil writes files safely: into a hidden temp file next to the
// target first, then renamed over it in one step.
package fsutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrExists reports that Commit refused to replace an existing file.
var ErrExists = errors.New("already exists")

// TempPath creates an empty hidden temp file next to path and returns its name.
// The name keeps path's extension so tools can infer the output format from it.
func TempPath(path string) (string, error) {
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "." // CreateTemp would fall back to the system temp dir
	}
	ext := filepath.Ext(base)
	f, err := os.CreateTemp(dir, "."+strings.TrimSuffix(base, ext)+".*.part"+ext)
	if err != nil {
		return "", err
	}
	return f.Name(), f.Close()
}

// Commit moves tmp to path. An existing file is replaced only when overwrite
// is set, and then keeps its permissions; new files get 0644.
func Commit(tmp, path string, overwrite bool) error {
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		if !overwrite {
			return fmt.Errorf("%s %w", path, ErrExists)
		}
		mode = fi.Mode().Perm()
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	if overwrite {
		return os.Rename(tmp, path)
	}
	// A hard link fails if path appeared meanwhile, so nothing is ever clobbered.
	err := os.Link(tmp, path)
	switch {
	case err == nil:
		return os.Remove(tmp)
	case errors.Is(err, fs.ErrExist):
		return fmt.Errorf("%s %w", path, ErrExists)
	}
	// shortcut: filesystems without hard links fall back to check-then-rename, which has a tiny race window.
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%s %w", path, ErrExists)
	}
	return os.Rename(tmp, path)
}

// WriteFile atomically replaces path with data.
func WriteFile(path string, data []byte) (err error) {
	tmp, err := TempPath(path)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(tmp)
		}
	}()
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return Commit(tmp, path, true)
}
