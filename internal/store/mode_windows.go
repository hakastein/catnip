package store

import "io/fs"

// newFileMode is a writable file: Windows keeps only the read-only attribute of a mode.
func newFileMode() fs.FileMode { return 0o666 }

// links is 1: FileInfo on Windows carries no link count.
func links(fs.FileInfo) uint64 { return 1 }
