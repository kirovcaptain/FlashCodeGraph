package java

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed external/*.json
var embeddedExternal embed.FS

// frameworkFileMap maps detected framework name to embedded JSON filename.
var frameworkFileMap = map[string]string{
	"spring":  "spring.json",
	"guava":   "guava.json",
	"commons": "apache-commons.json",
	"hutool":  "hutool.json",
}

// ExternalMethodManager provides method return type lookups for external frameworks.
type ExternalMethodManager struct {
	methods map[string]string
}

// NewExternalMethodManager loads method mappings from JDK built-in table, embedded JSONs
// (filtered by frameworks), and user-defined JSONs in {projectRoot}/.fcg/external/*.json.
// Load order: JDK → embedded → user-defined (later overrides earlier).
func NewExternalMethodManager(frameworks []string, projectRoot string) *ExternalMethodManager {
	manager := &ExternalMethodManager{methods: make(map[string]string, len(jdkMethodReturns))}

	// Load JDK built-in table first
	for key, returnType := range jdkMethodReturns {
		manager.methods[key] = returnType
	}

	// Load embedded JSONs matching detected frameworks
	for _, framework := range frameworks {
		if filename, found := frameworkFileMap[framework]; found {
			data, err := embeddedExternal.ReadFile("external/" + filename)
			if err != nil {
				continue
			}
			manager.loadJSON(data, filename)
		}
	}

	// Load user-defined JSONs from .fcg/external/
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
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		manager.loadJSON(data, filePath)
	}

	return manager
}

func (manager *ExternalMethodManager) loadJSON(data []byte, source string) {
	var methods map[string]string
	if err := json.Unmarshal(data, &methods); err != nil {
		fmt.Printf("  ⚠ skip invalid method mapping: %s (%v)\n", source, err)
		return
	}
	for key, returnType := range methods {
		manager.methods[key] = returnType
	}
}

// Lookup returns the return type for a method on a given class.
// Return type conventions (same as jdkMethodReturns):
//   - concrete type (e.g. "Stream"): returns that type, chain continues
//   - "SELF": returns receiver type (builder/fluent pattern)
//   - "T": returns container's first generic type arg
//   - "V": returns Map's value type arg
//   - "": terminal operation (void/boolean/int), chain ends
func (manager *ExternalMethodManager) Lookup(className, methodName string) (string, bool) {
	if manager == nil {
		return "", false
	}
	returnType, found := manager.methods[className+"."+methodName]
	return returnType, found
}
