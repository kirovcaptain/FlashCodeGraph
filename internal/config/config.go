// Package config handles two-level TOML configuration (global + project).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// Config is the merged configuration.
type Config struct {
	Project           ProjectConfig           `toml:"project"`
	Storage           StorageConfig           `toml:"storage"`
	System            SystemConfig            `toml:"system"`
	Index             IndexConfig             `toml:"index"`
	Embedding         EmbeddingConfig         `toml:"embedding"`
	Annotations       AnnotationsConfig       `toml:"annotations"`
	CrossProjectIndex CrossProjectIndexConfig `toml:"cross_project_index"`
	Dependencies      DependenciesConfig      `toml:"dependencies"`
}

// CrossProjectIndexConfig holds cross-project index settings (global config).
type CrossProjectIndexConfig struct {
	Backend    string `toml:"backend"`               // "json" (default) | "sqlite" (reserved)
	SQLitePath string `toml:"sqlite_path,omitempty"` // sqlite file path when backend=sqlite
}

// DependenciesConfig holds project dependency settings (project config).
type DependenciesConfig struct {
	Projects   []DependencyProject `toml:"projects"`
	Properties map[string]string   `toml:"properties"` // placeholder key → resolved value
}

// DependencyProject represents a dependent project.
type DependencyProject struct {
	Path   string `toml:"path"`
	Branch string `toml:"branch"`
}

// ProjectConfig holds project-level settings.
type ProjectConfig struct {
	Name string `toml:"name"`
	Type string `toml:"type"` // maven, gradle, npm, go, cargo, dotnet, unknown
}

// AnnotationsConfig holds annotation indexing settings.
type AnnotationsConfig struct {
	Include []string `toml:"include,omitempty"` // extra annotations to index
	Exclude []string `toml:"exclude,omitempty"` // annotations to skip
}

// StorageConfig holds storage backend settings.
type StorageConfig struct {
	Database      string `toml:"database"`        // kuzu, falkordb, neo4j
	Neo4jURI      string `toml:"neo4j_uri,omitempty"`
	FalkorDBURI   string `toml:"falkordb_uri,omitempty"`
	FalkorDBGraph string `toml:"falkordb_graph,omitempty"`
	KuzuPath      string `toml:"kuzu_path,omitempty"`
	Branch        string `toml:"-"` // runtime override, not persisted
}

// SystemConfig holds system-level settings.
type SystemConfig struct {
	Goroutines  int    `toml:"goroutines"`   // 0 = auto
	MemoryLimit string `toml:"memory_limit"` // e.g. "1GB"
	LogLevel    string `toml:"log_level"`    // debug, info, warn, error
}

// IndexConfig holds indexing settings.
type IndexConfig struct {
	Languages       []string `toml:"languages"`
	MaxFileSize     int64    `toml:"max_file_size"`
	ExcludeTests    bool     `toml:"exclude_tests"`
	Ignore          []string `toml:"ignore,omitempty"`
	AnnotationNodes []string `toml:"annotation_nodes,omitempty"`
}

// EmbeddingConfig holds embedding settings.
type EmbeddingConfig struct {
	Enabled bool `toml:"enabled"`
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Storage:           StorageConfig{Database: DetectDefaultDatabase()},
		System:            SystemConfig{Goroutines: 0, MemoryLimit: "1GB", LogLevel: "info"},
		Index:             IndexConfig{MaxFileSize: 512 * 1024, ExcludeTests: true},
		CrossProjectIndex: CrossProjectIndexConfig{Backend: "sqlite"},
	}
}

// GlobalDir returns the global FCG directory (~/.fcg/).
func GlobalDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".fcg")
}

// GlobalConfigPath returns the global config file path.
func GlobalConfigPath() string {
	return filepath.Join(GlobalDir(), "config.toml")
}

// ProjectConfigPath returns the project config file path.
func ProjectConfigPath(projectDir string) string {
	return filepath.Join(projectDir, ".fcg", "config.toml")
}

// Load reads and merges global + project config. Project overrides global.
func Load(projectDir string) (*Config, error) {
	cfg := DefaultConfig()

	// Load global
	if err := loadFile(GlobalConfigPath(), cfg); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("config: load global: %w", err)
	}

	// Load project (overrides global)
	if projectDir != "" {
		if err := loadFile(ProjectConfigPath(projectDir), cfg); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("config: load project: %w", err)
		}
	}

	return cfg, nil
}

func loadFile(path string, cfg *Config) error {
	_, err := toml.DecodeFile(path, cfg)
	return err
}

// Get returns a config value by dotted key (e.g. "storage.database").
func Get(cfg *Config, key string) (string, error) {
	switch key {
	case "project.name":
		return cfg.Project.Name, nil
	case "project.type":
		return cfg.Project.Type, nil
	case "storage.database":
		return cfg.Storage.Database, nil
	case "storage.neo4j_uri":
		return cfg.Storage.Neo4jURI, nil
	case "system.goroutines":
		return fmt.Sprintf("%d", cfg.System.Goroutines), nil
	case "system.memory_limit":
		return cfg.System.MemoryLimit, nil
	case "system.log_level":
		return cfg.System.LogLevel, nil
	case "index.max_file_size":
		return fmt.Sprintf("%d", cfg.Index.MaxFileSize), nil
	case "embedding.enabled":
		return fmt.Sprintf("%t", cfg.Embedding.Enabled), nil
	case "index.languages":
		return strings.Join(cfg.Index.Languages, ","), nil
	default:
		return "", fmt.Errorf("unknown config key: %s", key)
	}
}

// Set updates a config value by dotted key and writes to project config.
func Set(projectDir, key, value string) error {
	cfg := DefaultConfig()
	path := ProjectConfigPath(projectDir)
	_ = loadFile(path, cfg) // ignore if not exists

	switch key {
	case "project.name":
		cfg.Project.Name = value
	case "project.type":
		cfg.Project.Type = value
	case "storage.database":
		cfg.Storage.Database = value
	case "storage.neo4j_uri":
		cfg.Storage.Neo4jURI = value
	case "system.goroutines":
		if _, err := fmt.Sscanf(value, "%d", &cfg.System.Goroutines); err != nil {
			return fmt.Errorf("invalid integer for %s: %q", key, value)
		}
	case "system.memory_limit":
		cfg.System.MemoryLimit = value
	case "system.log_level":
		cfg.System.LogLevel = value
	case "index.max_file_size":
		if _, err := fmt.Sscanf(value, "%d", &cfg.Index.MaxFileSize); err != nil {
			return fmt.Errorf("invalid integer for %s: %q", key, value)
		}
	case "embedding.enabled":
		cfg.Embedding.Enabled = value == "true"
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}

	return WriteConfig(path, cfg)
}

// WriteConfig writes config to a TOML file.
func WriteConfig(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), model.DirectoryPermission); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

// GenerateDefault creates a default config.toml with comments.
func GenerateDefault(path string, projectName, projectType string, languages []string) error {
	if err := os.MkdirAll(filepath.Dir(path), model.DirectoryPermission); err != nil {
		return err
	}

	defaultDB := DetectDefaultDatabase()
	content := fmt.Sprintf(`[project]
name = %q       # Project name (auto-detected)
type = %q       # Project type: maven | gradle | npm | go | cargo | dotnet | unknown

[storage]
database = "%s"     # Storage backend: kuzu (local embedded) | falkordb | neo4j (remote)
# neo4j_uri = "bolt://team-server:7687"  # Remote mode connection

[system]
goroutines = 0        # Parallel goroutines, 0 = auto (runtime.NumCPU())
memory_limit = "1GB"  # Peak memory limit during indexing
log_level = "info"    # Log level: debug | info | warn | error

[index]
languages = [%s]
max_file_size = 524288          # Skip files larger than this (bytes), default 512KB
# annotation_nodes = [          # Annotations to create as graph nodes (supports graph queries)
#     "@RestController", "@Service", "@Repository",
#     "@Transactional", "@Autowired", "@Bean",
#     "@GetMapping", "@PostMapping", "@RequestMapping",
# ]

# Sub-module config (auto-detected, can override manually)
# [modules.api]
# src_dir = "api/src/main/java"

[cross_project_index]
backend = "sqlite"    # Cross-project index backend: sqlite (recommended) | json
# sqlite_path = ""    # SQLite file path, default: ~/.fcg/cross_project_index.db
`, projectName, projectType, defaultDB, formatStringSlice(languages))

	return os.WriteFile(path, []byte(content), model.FilePermission)
}

func formatStringSlice(ss []string) string {
	quoted := make([]string, len(ss))
	for i, s := range ss {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(quoted, ", ")
}
