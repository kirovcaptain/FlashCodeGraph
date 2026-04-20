// Package scanner detects project structure and scans source files.
package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// DefaultMaxFileSize is the default threshold for skipping large files (512KB).
const DefaultMaxFileSize int64 = 512 * 1024

// Scanner scans project directories for source files.
type Scanner struct {
	config      *Config
	projectInfo *ProjectInfo // set after DetectProject
}

// Config holds scanner configuration.
type Config struct {
	RootPath          string
	MaxFileSize       int64
	SupportedLangs    []string
	IgnoreRules       []string
	ExcludeTests      bool
	QueryDefExts      []string // e.g. [".xml"] from ORM manager
	SchemaDefExts     []string // e.g. [".graphql", ".gql"] from Schema manager
}

// ScannedFile represents a discovered source file.
type ScannedFile struct {
	Path     string
	RelPath  string
	Size     int64
	ModTime  int64
	Language string
	Category string // "source" / "query_def" / "schema_def"
}

// ProjectInfo holds detected project metadata.
type ProjectInfo struct {
	ProjectType string
	Language    string                       // Primary language (java, go, python, typescript, javascript)
	BuildFiles  []string
	SubModules  []SubModule
	SourceDirs  map[string]string            // module name → src dir
	Frameworks  []string                     // framework names (e.g. "spring", "gin")
	IsGit       bool
	GitBranch   string
}

// SubModule represents a detected sub-module.
type SubModule struct {
	Name    string
	RootDir string
	SrcDir  string
}

// New creates a Scanner with the given config.
func New(config *Config) *Scanner {
	if config.MaxFileSize <= 0 {
		config.MaxFileSize = DefaultMaxFileSize
	}
	return &Scanner{config: config}
}

// SetDefExtensions sets the extra file extensions for query_def and schema_def categories.
func (scanner *Scanner) SetDefExtensions(queryDefExts, schemaDefExts []string) {
	scanner.config.QueryDefExts = queryDefExts
	scanner.config.SchemaDefExts = schemaDefExts
}

// DetectProject scans the root directory for build files and project structure.
func (scanner *Scanner) DetectProject() (*ProjectInfo, error) {
	root := scanner.config.RootPath
	info := &ProjectInfo{
		SourceDirs: make(map[string]string),
	}

	// Detect Git
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		info.IsGit = true
	}

	// Detect build files and project type
	buildFileDetectors := []struct {
		file        string
		projectType string
	}{
		{"pom.xml", "maven"},
		{"build.gradle", "gradle"},
		{"build.gradle.kts", "gradle"},
		{"settings.gradle", "gradle"},
		{"settings.gradle.kts", "gradle"},
		{"package.json", "npm"},
		{"go.mod", "go"},
		{"Cargo.toml", "cargo"},
		{"pyproject.toml", "python"},
		{"setup.py", "python"},
		{"requirements.txt", "python"},
		{"*.sln", "dotnet"},
		{"*.csproj", "dotnet"},
		{"Gemfile", "ruby"},
		{"composer.json", "php"},
		{"pubspec.yaml", "dart"},
		{"Package.swift", "swift"},
	}

	for _, detector := range buildFileDetectors {
		if strings.Contains(detector.file, "*") {
			matches, _ := filepath.Glob(filepath.Join(root, detector.file))
			if len(matches) > 0 {
				info.ProjectType = detector.projectType
				for _, match := range matches {
					info.BuildFiles = append(info.BuildFiles, filepath.Base(match))
				}
			}
		} else {
			path := filepath.Join(root, detector.file)
			if _, err := os.Stat(path); err == nil {
				if info.ProjectType == "" {
					info.ProjectType = detector.projectType
				}
				info.BuildFiles = append(info.BuildFiles, detector.file)
			}
		}
	}

	if info.ProjectType == "" {
		info.ProjectType = "unknown"
	}

	// Detect sub-modules based on project type
	switch info.ProjectType {
	case "maven":
		scanner.detectMavenModules(root, info)
	case "gradle":
		scanner.detectGradleModules(root, info)
	case "npm":
		scanner.detectNpmWorkspaces(root, info)
	case "go":
		scanner.detectGoModules(root, info)
	}

	scanner.projectInfo = info
	return info, nil
}

// Scan discovers all source files under the root path.
func (scanner *Scanner) Scan(ctx context.Context) ([]ScannedFile, []model.SkippedFile, error) {
	root := scanner.config.RootPath
	ignoreRules := buildIgnoreSet(scanner.config.IgnoreRules)
	validTopDirs := buildValidTopDirs(scanner.projectInfo)

	var files []ScannedFile
	var skipped []model.SkippedFile

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible files
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		relPath, _ := filepath.Rel(root, path)

		if info.IsDir() {
			if shouldIgnoreDirectory(relPath, ignoreRules) {
				return filepath.SkipDir
			}
			if scanner.config.ExcludeTests && isTestDirectory(relPath, "") {
				return filepath.SkipDir
			}
			// Multi-module: skip undeclared top-level directories
			if len(validTopDirs) > 0 && relPath != "." {
				topDir := strings.SplitN(relPath, string(os.PathSeparator), 2)[0]
				if !validTopDirs[topDir] {
					return filepath.SkipDir
				}
			}
			return nil
		}

		// Detect language before size check to avoid stat on non-source files
		language := detectLanguage(path)
		category := constants.FileSource
		if language == "" {
			ext := strings.ToLower(filepath.Ext(path))
			category = matchDefCategory(ext, scanner.config.QueryDefExts, scanner.config.SchemaDefExts)
			if category == "" {
				return nil
			}
		}

		// Exclude test files
		if scanner.config.ExcludeTests && language != "" && isTestFile(info.Name(), language) {
			return nil
		}

		// Check file size using Walk's FileInfo (no extra stat call)
		if info.Size() > scanner.config.MaxFileSize {
			skipped = append(skipped, model.SkippedFile{
				Path:   relPath,
				Reason: "exceeds_max_size",
				Detail: formatFileSize(info.Size()),
			})
			return nil
		}

		files = append(files, ScannedFile{
			Path:     path,
			RelPath:  relPath,
			Size:     info.Size(),
			ModTime:  info.ModTime().Unix(),
			Language: language,
			Category: category,
		})
		return nil
	})

	return files, skipped, err
}

// Language detection by file extension.
var extensionToLanguage = map[string]string{
	".java":   "java",
	".py":     "python",
	".pyi":    "python",
	".go":     "go",
	".rs":     "rust",
	".c":      "c",
	".h":      "c",
	".cpp":    "cpp",
	".cc":     "cpp",
	".cxx":    "cpp",
	".hpp":    "cpp",
	".hxx":    "cpp",
	".cs":     "csharp",
	".rb":     "ruby",
	".swift":  "swift",
	".php":    "php",
	".dart":   "dart",
	".ts":     "typescript",
	".tsx":    "typescript",
	".js":     "javascript",
	".jsx":    "javascript",
	".mjs":    "javascript",
	".cjs":    "javascript",
}

func detectLanguage(path string) string {
	extension := strings.ToLower(filepath.Ext(path))
	return extensionToLanguage[extension]
}

// Directories to always ignore.
// DefaultIgnoredDirectories contains directory names to skip during scanning.
var DefaultIgnoredDirectories = map[string]bool{
	"node_modules":  true,
	".git":          true,
	"__pycache__":   true,
	"vendor":        true,
	"dist":          true,
	"build":         true,
	".gradle":       true,
	".idea":         true,
	".vscode":       true,
	".fcg":          true,
	"target":        true,
	".next":         true,
	".nuxt":         true,
	"out":           true,
	"bin":           true,
	"obj":           true,
	".cache":        true,
	"coverage":      true,
	".tox":          true,
	".mypy_cache":   true,
	".pytest_cache": true,
	"venv":          true,
	".venv":         true,
}

var testDirectories = map[string]bool{
	"test":       true,
	"tests":      true,
	"__tests__":  true,
	"spec":       true,
	"specs":      true,
	"__mocks__":  true,
}

// isTestFile checks if a file is a test file based on language conventions.
func isTestFile(fileName, language string) bool {
	switch language {
	case "go":
		return strings.HasSuffix(fileName, "_test.go")
	case "java":
		base := strings.TrimSuffix(fileName, ".java")
		return strings.HasSuffix(base, "Test") || strings.HasSuffix(base, "Tests") || strings.HasSuffix(base, "Spec")
	case "python":
		return strings.HasPrefix(fileName, "test_") || strings.HasSuffix(strings.TrimSuffix(fileName, ".py"), "_test")
	case "typescript", "javascript":
		for _, suffix := range []string{".test.ts", ".test.js", ".test.tsx", ".test.jsx", ".spec.ts", ".spec.js", ".spec.tsx", ".spec.jsx"} {
			if strings.HasSuffix(fileName, suffix) {
				return true
			}
		}
	}
	return false
}

// isTestDirectory checks if a directory path contains a test directory segment.
func isTestDirectory(relPath, language string) bool {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	for _, part := range parts {
		if testDirectories[part] {
			return true
		}
	}
	// Java: src/test/ convention
	if language == "java" || language == "" {
		normalized := filepath.ToSlash(relPath)
		if strings.Contains(normalized, "src/test/") {
			return true
		}
	}
	return false
}

func shouldIgnoreDirectory(relPath string, customRules map[string]bool) bool {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	for _, part := range parts {
		if DefaultIgnoredDirectories[part] || customRules[part] {
			return true
		}
	}
	return false
}

func buildIgnoreSet(rules []string) map[string]bool {
	ignoreSet := make(map[string]bool)
	for _, rule := range rules {
		rule = strings.TrimSpace(rule)
		if rule != "" && !strings.HasPrefix(rule, "#") {
			ignoreSet[rule] = true
		}
	}
	return ignoreSet
}

func formatFileSize(bytes int64) string {
	const (
		kilobyte int64 = 1024
		megabyte int64 = 1024 * kilobyte
	)
	switch {
	case bytes >= megabyte:
		return fmt.Sprintf("%.1fMB", float64(bytes)/float64(megabyte))
	default:
		return fmt.Sprintf("%.0fKB", float64(bytes)/float64(kilobyte))
	}
}

// buildValidTopDirs returns the set of valid top-level directories for multi-module projects.
// Returns nil for single-module projects (no restriction).
func buildValidTopDirs(info *ProjectInfo) map[string]bool {
	if info == nil || len(info.SubModules) == 0 {
		return nil
	}
	dirs := make(map[string]bool)
	for _, mod := range info.SubModules {
		topDir := strings.SplitN(mod.Name, string(os.PathSeparator), 2)[0]
		dirs[topDir] = true
	}
	dirs["src"] = true // root module may have its own source
	return dirs
}


func matchDefCategory(ext string, queryDefExts, schemaDefExts []string) string {
	for _, e := range queryDefExts {
		if ext == e {
			return constants.FileQueryDef
		}
	}
	for _, e := range schemaDefExts {
		if ext == e {
			return constants.FileSchemaDef
		}
	}
	return ""
}
