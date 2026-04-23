// Package skills embeds skill files and platform config for distribution.
package skills

import (
	"embed"
	"io/fs"
)

//go:embed platforms.json kiro.md
var content embed.FS

// FS returns the embedded skill files as an fs.FS.
func FS() fs.FS {
	return content
}
