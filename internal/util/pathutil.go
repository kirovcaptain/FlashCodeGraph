// Package util provides shared utilities.
package util

import "strings"

// DerivePackage converts a file path to a dot-separated package name.
// Index files (index.ts/index.js) and Python __init__.py use their directory name.
//
// Examples:
//
//	"pkg-a/index.ts"         → "pkg-a"
//	"pkg-a/models/User.ts"  → "pkg-a.models.User"
//	"src/utils/index.tsx"    → "src.utils"
//	"index.ts"               → "index"
//	"models/__init__.py"     → "models"
//	"models/user.py"         → "models.user"
func DerivePackage(filePath string) string {
	base := filePath
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".py"} {
		base = strings.TrimSuffix(base, ext)
	}
	base = strings.ReplaceAll(base, "\\", "/")
	if strings.HasSuffix(base, "/index") || base == "index" {
		base = strings.TrimSuffix(base, "/index")
		if base == "" {
			return "index"
		}
	}
	if strings.HasSuffix(base, "/__init__") || base == "__init__" {
		base = strings.TrimSuffix(base, "/__init__")
		if base == "" {
			return "__init__"
		}
	}
	return strings.ReplaceAll(base, "/", ".")
}
