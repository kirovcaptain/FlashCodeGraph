package kotlin

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

// frameworkFileMap maps detected framework name to embedded JSON filename.
var frameworkFileMap = map[string]string{
	"android": "android.json",
	"compose": "compose.json",
}

// methodEntry represents a parsed method declaration from JSON.
type methodEntry struct {
	ClassName  string
	MethodName string
	ParamTypes []string
	ReturnType string
}

// ExternalMethodManager provides method return type lookups for external Kotlin/Android frameworks.
type ExternalMethodManager struct {
	methods         map[string]methodEntry
	shortNameIndex  map[string][]string
	classTypeParams map[string][]string
	shortClassIndex map[string][]string
	knownPackages   map[string]bool
}

// NewExternalMethodManager loads method mappings from builtin JSONs, framework JSONs,
// and user-defined JSONs in {projectRoot}/.fcg/external/*.json.
func NewExternalMethodManager(frameworks []string, projectRoot string) *ExternalMethodManager {
	manager := &ExternalMethodManager{
		methods:         make(map[string]methodEntry),
		shortNameIndex:  make(map[string][]string),
		classTypeParams: make(map[string][]string),
		shortClassIndex: make(map[string][]string),
		knownPackages:   make(map[string]bool),
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
	// Android framework always loads android.json + compose.json
	if containsFramework(frameworks, "android") {
		for _, filename := range []string{"android.json", "compose.json"} {
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
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			data, readErr := os.ReadFile(filepath.Join(customDir, entry.Name()))
			if readErr != nil {
				continue
			}
			manager.loadJSON(data, "user/"+entry.Name())
		}
	}

	return manager
}

func containsFramework(frameworks []string, target string) bool {
	for _, framework := range frameworks {
		if framework == target {
			return true
		}
	}
	return false
}

func (manager *ExternalMethodManager) loadJSON(data []byte, source string) {
	var entries map[string]string
	if err := json.Unmarshal(data, &entries); err != nil {
		fmt.Printf("  ⚠ skip invalid method mapping: %s (%v)\n", source, err)
		return
	}
	for key, value := range entries {
		if strings.Contains(key, "(") {
			entry := parseMethodKey(key, value)
			fullKey := entry.ClassName + "." + entry.MethodName + "(" + strings.Join(entry.ParamTypes, ",") + ")"
			manager.methods[fullKey] = entry
			shortKey := shortClassName(entry.ClassName) + "." + entry.MethodName
			manager.shortNameIndex[shortKey] = appendUnique(manager.shortNameIndex[shortKey], fullKey)
			// Register package
			if dotIndex := strings.LastIndex(entry.ClassName, "."); dotIndex > 0 {
				manager.knownPackages[entry.ClassName[:dotIndex]] = true
			}
		} else {
			if value == "" {
				manager.classTypeParams[key] = nil
			} else {
				manager.classTypeParams[key] = strings.Split(value, ",")
			}
			shortName := shortClassName(key)
			manager.shortClassIndex[shortName] = appendUnique(manager.shortClassIndex[shortName], key)
			// Register package
			if dotIndex := strings.LastIndex(key, "."); dotIndex > 0 {
				manager.knownPackages[key[:dotIndex]] = true
			}
		}
	}
}

// Lookup returns the return type for a method on a given class.
func (manager *ExternalMethodManager) Lookup(className, methodName string, argTypes []string) (model.ReturnType, bool) {
	if manager == nil {
		return model.ReturnType{}, false
	}
	candidates := manager.findMethods(className, methodName)
	if len(candidates) == 0 {
		shortKey := shortClassName(className) + "." + methodName
		if fullKeys, ok := manager.shortNameIndex[shortKey]; ok {
			for _, fullKey := range fullKeys {
				if entry, exists := manager.methods[fullKey]; exists {
					candidates = append(candidates, entry)
				}
			}
		}
	}
	if len(candidates) == 0 {
		return model.ReturnType{}, false
	}
	if len(candidates) == 1 {
		return model.ParseReturnType(candidates[0].ReturnType), true
	}
	best := resolveOverloadFromEntries(candidates, argTypes)
	return model.ParseReturnType(best.ReturnType), true
}

// LookupClassTypeParams returns the generic type parameters for an external class.
func (manager *ExternalMethodManager) LookupClassTypeParams(className string) []string {
	if manager == nil {
		return nil
	}
	if params, ok := manager.classTypeParams[className]; ok {
		return params
	}
	shortName := shortClassName(className)
	if fullNames, ok := manager.shortClassIndex[shortName]; ok && len(fullNames) > 0 {
		return manager.classTypeParams[fullNames[0]]
	}
	return nil
}

// HasPackage returns true if the receiver name matches a known external package prefix.
func (manager *ExternalMethodManager) HasPackage(receiverName string) bool {
	if manager == nil {
		return false
	}
	return manager.knownPackages[receiverName]
}

func (manager *ExternalMethodManager) findMethods(className, methodName string) []methodEntry {
	prefix := className + "." + methodName + "("
	var results []methodEntry
	for key, entry := range manager.methods {
		if strings.HasPrefix(key, prefix) {
			results = append(results, entry)
		}
	}
	return results
}

// parseMethodKey parses "com.example.Class.method(param1,param2)" into methodEntry.
func parseMethodKey(key, returnType string) methodEntry {
	parenIdx := strings.Index(key, "(")
	if parenIdx < 0 {
		return methodEntry{ReturnType: returnType}
	}
	fullName := key[:parenIdx]
	paramStr := key[parenIdx+1 : len(key)-1]

	dotIdx := strings.LastIndex(fullName, ".")
	if dotIdx < 0 {
		return methodEntry{MethodName: fullName, ReturnType: returnType}
	}

	className := fullName[:dotIdx]
	methodName := fullName[dotIdx+1:]

	var paramTypes []string
	if paramStr != "" {
		paramTypes = strings.Split(paramStr, ",")
	}

	return methodEntry{
		ClassName:  className,
		MethodName: methodName,
		ParamTypes: paramTypes,
		ReturnType: returnType,
	}
}

func shortClassName(fullName string) string {
	if dotIdx := strings.LastIndex(fullName, "."); dotIdx >= 0 {
		return fullName[dotIdx+1:]
	}
	return fullName
}

func appendUnique(slice []string, item string) []string {
	for _, existing := range slice {
		if existing == item {
			return slice
		}
	}
	return append(slice, item)
}

func resolveOverloadFromEntries(candidates []methodEntry, argTypes []string) methodEntry {
	if len(argTypes) > 0 {
		for _, candidate := range candidates {
			if len(candidate.ParamTypes) == len(argTypes) {
				return candidate
			}
		}
	}
	return candidates[0]
}
