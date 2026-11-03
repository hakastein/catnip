// Package store writes files atomically.
package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrPartial marks a failed in-place write: the file may hold part of the new content.
var ErrPartial = errors.New("file written in part")

// Write replaces path with data through a temporary file and rename in the same directory, so a reader never sees
// a partial file. An existing file keeps its permissions; a symlink keeps pointing at its target, which is
// written, and a symlink to a missing file is an error. A new file gets the default mode of the platform, and
// missing parent directories are created. A file with several hard links is overwritten in place, not atomically,
// so every link sees the new content; a failure after it was opened is ErrPartial.
func Write(path string, data []byte) error {
	switch real, err := filepath.EvalSymlinks(path); {
	case err == nil:
		path = real
	case !errors.Is(err, fs.ErrNotExist):
		return err
	default:
		if info, err := os.Lstat(path); err == nil && info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s: symlink to a missing file", path)
		}
	}
	var mode fs.FileMode
	switch info, err := os.Stat(path); {
	case err == nil:
		mode = info.Mode() & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)
		if links(info) > 1 { // a rename would detach the other names of the file from the new content
			return overwrite(path, data, mode)
		}
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

// overwrite writes data over the file in place and restores mode: writing clears setuid and setgid.
func overwrite(path string, data []byte, mode fs.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(mode)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPartial, err)
	}
	return nil
}
