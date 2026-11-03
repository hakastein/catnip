package store

import "io/fs"

// newFileMode is a writable file: Windows keeps only the read-only attribute of a mode.
func newFileMode() fs.FileMode { return 0o666 }
