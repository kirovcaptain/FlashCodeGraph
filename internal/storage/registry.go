package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// RegistryEntry maps a project to its data location.
type RegistryEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Database string `json:"database"`
	Branch   string `json:"branch,omitempty"`
}

// Registry manages the list of indexed projects (~/.fcg/registry.json).
type Registry struct {
	path    string
	entries []RegistryEntry
}

// NewRegistry loads or creates the registry.
func NewRegistry(fcgDir string) (*Registry, error) {
	registryPath := filepath.Join(fcgDir, "registry.json")
	registry := &Registry{path: registryPath}
	data, err := os.ReadFile(registryPath)
	if os.IsNotExist(err) {
		return registry, nil
	}
	if err != nil {
		return nil, fmt.Errorf("registry: read: %w", err)
	}
	if err := json.Unmarshal(data, &registry.entries); err != nil {
		return nil, fmt.Errorf("registry: parse: %w", err)
	}
	return registry, nil
}

// Register adds or updates a project entry, keyed by absolute path.
func (registry *Registry) Register(name, projectPath, database, branch string) error {
	for i, entry := range registry.entries {
		if entry.Path == projectPath {
			registry.entries[i] = RegistryEntry{Name: name, Path: projectPath, Database: database, Branch: branch}
			return registry.save()
		}
	}
	registry.entries = append(registry.entries, RegistryEntry{Name: name, Path: projectPath, Database: database, Branch: branch})
	return registry.save()
}

// List returns all registered projects.
func (registry *Registry) List() []RegistryEntry {
	return registry.entries
}

// Unregister removes a project from the registry by path.
func (registry *Registry) Unregister(projectPath string) error {
	for i, entry := range registry.entries {
		if entry.Path == projectPath {
			registry.entries = append(registry.entries[:i], registry.entries[i+1:]...)
			return registry.save()
		}
	}
	return nil
}

// FindByName returns the entry matching the given name.
func (registry *Registry) FindByName(name string) *RegistryEntry {
	for _, entry := range registry.entries {
		if entry.Name == name {
			return &entry
		}
	}
	return nil
}

// FindByPath returns the entry matching the given path.
func (registry *Registry) FindByPath(projectPath string) *RegistryEntry {
	for _, entry := range registry.entries {
		if entry.Path == projectPath {
			return &entry
		}
	}
	return nil
}


// DataDir returns the data directory for a project, using the project directory name.
// Example: ~/.fcg/data/java_maven/
func DataDir(fcgDir, projectPath string) string {
	projectName := filepath.Base(projectPath)
	return filepath.Join(fcgDir, "data", projectName)
}

func (registry *Registry) save() error {
	if err := os.MkdirAll(filepath.Dir(registry.path), model.DirectoryPermission); err != nil {
		return err
	}
	data, err := json.MarshalIndent(registry.entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(registry.path, data, model.FilePermission)
}
