package crossindex

import (
	"fmt"
	"path/filepath"
)

// New creates a CrossProjectIndex based on the backend configuration.
// Supported backends: "json" (default), "sqlite".
func New(backend, sqlitePath, globalDir string) (CrossProjectIndex, error) {
	switch backend {
	case "sqlite":
		dbPath := sqlitePath
		if dbPath == "" {
			dbPath = DefaultSQLitePath(globalDir)
		}
		store := NewSQLiteStore(dbPath)
		if err := store.Load(); err != nil {
			return nil, fmt.Errorf("sqlite cross-index: %w", err)
		}
		return store, nil
	case "json", "":
		jsonPath := filepath.Join(globalDir, "cross_project_index.json")
		store := NewJSONStore(jsonPath)
		if err := store.Load(); err != nil {
			return nil, fmt.Errorf("json cross-index: %w", err)
		}
		return store, nil
	default:
		return nil, fmt.Errorf("unknown cross_project_index backend: %q", backend)
	}
}
