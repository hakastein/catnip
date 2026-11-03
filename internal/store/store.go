// Package store writes files atomically.
package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Write replaces path with data through a temporary file and rename in the same directory, so a reader never sees
// a partial file. An existing file keeps its permissions; a symlink keeps pointing at its target, which is
// written. A new file gets the default mode of the platform, and missing parent directories are created.
func Write(path string, data []byte) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var mode fs.FileMode
	switch info, err := os.Stat(path); {
	case err == nil:
		mode = info.Mode().Perm()
	case errors.Is(err, fs.ErrNotExist):
		mode = newFileMode()
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			return err
		}
	default:
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".catnip-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
