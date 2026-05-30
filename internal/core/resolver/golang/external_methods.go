package golang

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
	"gin":   "gin.json",
	"echo":  "echo.json",
	"fiber": "fiber.json",
	"gorm":  "gorm.json",
	"grpc":  "grpc.json",
}

// ExternalMethodManager provides method/function return type lookups for Go external packages.
type ExternalMethodManager struct {
	methods  map[string]string // "strings.Index" → "int"
	packages map[string]bool   // known external package names
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
		fmt.Printf("  ⚠ skip invalid Go external method mapping: %s (%v)\n", source, err)
		return
	}
	for key, returnType := range entries {
		manager.methods[key] = returnType
		// Extract package name from key (e.g. "strings.Index" → "strings", "http.Client.Do" → "http")
		if dotIndex := strings.Index(key, "."); dotIndex > 0 {
			manager.packages[key[:dotIndex]] = true
		}
	}
}

// Lookup returns the return type for a Go package function or method.
// key format: "packageName.FunctionName" (e.g. "strings.Index", "http.Get")
func (manager *ExternalMethodManager) Lookup(packageName, methodName string) (model.ReturnType, bool) {
	if manager == nil {
		return model.ReturnType{}, false
	}
	key := packageName + "." + methodName
	if returnType, found := manager.methods[key]; found {
		return model.ParseReturnType(returnType), true
	}
	return model.ReturnType{}, false
}

// HasPackage returns true if the given name is a known external package.
func (manager *ExternalMethodManager) HasPackage(packageName string) bool {
	if manager == nil {
		return false
	}
	return manager.packages[packageName]
}
