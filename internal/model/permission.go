package model

import "os"

const (
	// DirectoryPermission is the default permission for created directories.
	// On Windows this value is ignored by the OS.
	DirectoryPermission os.FileMode = 0o755

	// FilePermission is the default permission for created files.
	// On Windows this value is ignored by the OS.
	FilePermission os.FileMode = 0o644
)
