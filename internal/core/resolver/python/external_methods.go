package python

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

//go:embed external/*.json external/builtin/*.json
var embeddedExternal embed.FS

// frameworkFileMap maps detected framework names to their JSON filenames.
var frameworkFileMap = map[string]string{
	"flask":      "flask.json",
	"django":     "django.json",
	"fastapi":    "fastapi.json",
	"requests":   "requests.json",
	"httpx":      "httpx.json",
	"sqlalchemy": "sqlalchemy.json",
}

// ExternalMethodManager provides method/function return type lookups for Python external packages.
type ExternalMethodManager struct {
	methods  map[string]string // "os.path.join" → "str"
	packages map[string]bool   // known external module names
}

// NewExternalMethodManager loads method mappings from builtin JSONs, framework JSONs,
// and user-defined JSONs in {projectRoot}/.fcg/external/*.json.
func NewExternalMethodManager(frameworks []string, projectRoot string) *ExternalMethodManager {
	manager := &ExternalMethodManager{
		methods:  make(map[string]string),
		packages: make(map[string]bool),
	}

	// 1. Always load all builtin JSONs
	builtinEntries, err := embeddedExternal.ReadDir("external/builtin")
	if err == nil {
		for _, entry := range builtinEntries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			data, readErr := embeddedExternal.ReadFile("external/builtin/" + entry.Name())
			if readErr != nil {
				continue
			}
			manager.loadJSON(data, "builtin/"+entry.Name())
		}
	}

	// 2. Load embedded JSONs matching detected frameworks
	for _, framework := range frameworks {
		if filename, found := frameworkFileMap[framework]; found {
			data, readErr := embeddedExternal.ReadFile("external/" + filename)
			if readErr != nil {
				continue
			}
			manager.loadJSON(data, filename)
		}
	}

	// 3. Load user-defined JSONs from .fcg/external/
	customDir := filepath.Join(projectRoot, ".fcg", "external")
	entries, err := os.ReadDir(customDir)
	if err != nil {
		return manager
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		filePath := filepath.Join(customDir, entry.Name())
		data, readErr := os.ReadFile(filePath)
		if readErr != nil {
			continue
		}
		manager.loadJSON(data, filePath)
	}

	return manager
}

func (manager *ExternalMethodManager) loadJSON(data []byte, source string) {
	var entries map[string]string
	if err := json.Unmarshal(data, &entries); err != nil {
		fmt.Printf("  ⚠ skip invalid Python external method mapping: %s (%v)\n", source, err)
		return
	}
	for key, returnType := range entries {
		manager.methods[key] = returnType
		// Extract module name from key (e.g. "os.path.join" → "os", "json.loads" → "json")
		if dotIndex := strings.Index(key, "."); dotIndex > 0 {
			manager.packages[key[:dotIndex]] = true
		}
	}
}

// Lookup returns the return type for a Python module function or method.
func (manager *ExternalMethodManager) Lookup(moduleName, methodName string) (model.ReturnType, bool) {
	if manager == nil {
		return model.ReturnType{}, false
	}
	key := moduleName + "." + methodName
	if returnType, found := manager.methods[key]; found {
		return model.ParseReturnType(returnType), true
	}
	return model.ReturnType{}, false
}

// HasPackage returns true if the given name is a known external module.
func (manager *ExternalMethodManager) HasPackage(moduleName string) bool {
	if manager == nil {
		return false
	}
	return manager.packages[moduleName]
}
