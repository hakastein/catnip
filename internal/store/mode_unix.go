//go:build unix

package store

import (
	"io/fs"
	"syscall"
)

// newFileMode is 0666 minus the umask, as the shell would create the file.
func newFileMode() fs.FileMode {
	mask := syscall.Umask(0)
	syscall.Umask(mask)
	return 0o666 &^ fs.FileMode(mask)
}

// links is the number of hard links to the file.
func links(info fs.FileInfo) uint64 { return uint64(info.Sys().(*syscall.Stat_t).Nlink) }
