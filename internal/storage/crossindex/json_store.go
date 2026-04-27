package crossindex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// jsonIndex is the on-disk JSON format.
type jsonIndex struct {
	Version  int                     `json:"version"`
	Projects map[string]ProjectEntry `json:"projects"` // key: "projectPath::branch"
}

// JSONStore implements CrossProjectIndex using a JSON file.
type JSONStore struct {
	filePath string
	data     jsonIndex
	mutex    sync.RWMutex
}

// NewJSONStore creates a JSONStore at the given file path.
// Call Load() to read existing data from disk.
func NewJSONStore(filePath string) *JSONStore {
	return &JSONStore{
		filePath: filePath,
		data: jsonIndex{
			Version:  1,
			Projects: make(map[string]ProjectEntry),
		},
	}
}

// RegisterProject registers or replaces all symbols and routes for a project+branch.
func (store *JSONStore) RegisterProject(_ context.Context, entry ProjectEntry) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	key := projectKey(entry.ProjectPath, entry.Branch)
	store.data.Projects[key] = entry
	return store.saveLocked()
}

// UnregisterProject removes all data for a project+branch.
func (store *JSONStore) UnregisterProject(_ context.Context, projectPath, branch string) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	key := projectKey(projectPath, branch)
	delete(store.data.Projects, key)
	return store.saveLocked()
}

// LookupSymbol finds symbols by qualified name, scoped to the given dependencies.
// Supports exact match and suffix match (e.g. "PaymentDubboService" matches "com.example.api.PaymentDubboService").
func (store *JSONStore) LookupSymbol(_ context.Context, qualifiedName string, dependencies []Dependency) []SymbolMatch {
	store.mutex.RLock()
	defer store.mutex.RUnlock()

	depSet := buildDependencySet(dependencies)
	var matches []SymbolMatch
	suffixPattern := "." + qualifiedName

	for key, entry := range store.data.Projects {
		if !depSet[key] {
			continue
		}
		for _, symbol := range entry.Symbols {
			if symbol.QualifiedName == qualifiedName ||
				symbol.Name == qualifiedName ||
				strings.HasSuffix(symbol.QualifiedName, suffixPattern) {
				matches = append(matches, SymbolMatch{
					Symbol:      symbol,
					ProjectPath: entry.ProjectPath,
					Branch:      entry.Branch,
				})
			}
		}
	}
	return matches
}

// MatchRoute finds provider routes matching HTTP method+path, scoped to the given dependencies.
func (store *JSONStore) MatchRoute(_ context.Context, method, path string, dependencies []Dependency) []RouteMatch {
	store.mutex.RLock()
	defer store.mutex.RUnlock()

	depSet := buildDependencySet(dependencies)
	normalizedPath := normalizeRoutePath(path)
	upperMethod := strings.ToUpper(method)
	var matches []RouteMatch

	for key, entry := range store.data.Projects {
		if !depSet[key] {
			continue
		}
		for _, route := range entry.Routes {
			if route.Role != RoleProvider {
				continue
			}
			if strings.ToUpper(route.Method) != upperMethod {
				continue
			}
			if normalizeRoutePath(route.Path) != normalizedPath {
				continue
			}
			matches = append(matches, RouteMatch{
				Route:       route,
				ProjectPath: entry.ProjectPath,
				Branch:      entry.Branch,
			})
		}
	}
	return matches
}


// MatchRouteByService finds provider routes matching a service name and framework.
// Uses case-insensitive comparison on the route path (service name).
func (store *JSONStore) MatchRouteByService(_ context.Context, serviceName, framework string, dependencies []Dependency) []RouteMatch {
	store.mutex.RLock()
	defer store.mutex.RUnlock()

	depSet := buildDependencySet(dependencies)
	lowerService := strings.ToLower(serviceName)
	var matches []RouteMatch

	for key, entry := range store.data.Projects {
		if !depSet[key] {
			continue
		}
		for _, route := range entry.Routes {
			if route.Role != RoleProvider {
				continue
			}
			if route.Framework != framework {
				continue
			}
			if strings.ToLower(route.Path) != lowerService {
				continue
			}
			matches = append(matches, RouteMatch{
				Route:       route,
				ProjectPath: entry.ProjectPath,
				Branch:      entry.Branch,
			})
			break // one match per project is enough
		}
	}
	return matches
}

// ListProjects returns all registered project entries.
func (store *JSONStore) ListProjects(_ context.Context) []ProjectEntry {
	store.mutex.RLock()
	defer store.mutex.RUnlock()

	entries := make([]ProjectEntry, 0, len(store.data.Projects))
	for _, entry := range store.data.Projects {
		entries = append(entries, entry)
	}
	return entries
}

// Load reads the index from the JSON file into memory.
func (store *JSONStore) Load() error {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	content, err := os.ReadFile(store.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // fresh start
		}
		return err
	}

	var data jsonIndex
	if err := json.Unmarshal(content, &data); err != nil {
		return err
	}
	if data.Projects == nil {
		data.Projects = make(map[string]ProjectEntry)
	}
	store.data = data
	return nil
}

// Save writes the in-memory index to the JSON file using atomic write (temp file + rename).
func (store *JSONStore) Save() error {
	store.mutex.RLock()
	defer store.mutex.RUnlock()
	return store.saveLocked()
}

// Close is a no-op for JSONStore.
func (store *JSONStore) Close() error {
	return nil
}

// saveLocked writes to disk. Caller must hold at least a read lock.
func (store *JSONStore) saveLocked() error {
	content, err := json.MarshalIndent(store.data, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(store.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Atomic write: write to temp file, then rename
	tempFile := store.filePath + ".tmp"
	if err := os.WriteFile(tempFile, content, 0644); err != nil {
		return err
	}
	return os.Rename(tempFile, store.filePath)
}

// buildDependencySet creates a set of "projectPath::branch" keys for fast lookup.
func buildDependencySet(dependencies []Dependency) map[string]bool {
	depSet := make(map[string]bool, len(dependencies))
	for _, dependency := range dependencies {
		depSet[projectKey(dependency.Path, dependency.Branch)] = true
	}
	return depSet
}
