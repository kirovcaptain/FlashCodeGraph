package typescript

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
	"react":   "react.json",
	"vue":     "vue.json",
	"express": "express.json",
	"axios":   "axios.json",
}

// ExternalMethodManager provides method/function return type lookups for TS/JS external libraries.
type ExternalMethodManager struct {
	methods map[string]string
}

// NewExternalMethodManager loads method mappings from embedded JSONs
// (filtered by frameworks) and user-defined JSONs in {projectRoot}/.fcg/external/*.json.
func NewExternalMethodManager(frameworks []string, projectRoot string) *ExternalMethodManager {
	manager := &ExternalMethodManager{methods: make(map[string]string)}

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

// Lookup returns the return type for a method on a given class/module, or a function's return type.
// Key format: "ClassName.methodName" or "functionName.fieldIndex"
func (manager *ExternalMethodManager) Lookup(className, methodName string) (string, bool) {
	if manager == nil {
		return "", false
	}
	returnType, found := manager.methods[className+"."+methodName]
	return returnType, found
}

// LookupFunction returns the return type for a standalone function call.
func (manager *ExternalMethodManager) LookupFunction(functionName string) (string, bool) {
	if manager == nil {
		return "", false
	}
	returnType, found := manager.methods[functionName]
	return returnType, found
}
