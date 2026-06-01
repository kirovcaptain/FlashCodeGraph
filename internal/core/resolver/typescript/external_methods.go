package typescript

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
	"react":   "react.json",
	"vue":     "vue.json",
	"express": "express.json",
	"axios":   "axios.json",
}

// methodEntry represents a parsed method declaration from JSON.
type methodEntry struct {
	ClassName  string
	MethodName string
	ParamTypes []string
	ReturnType string
}

// ExternalMethodManager provides method/function return type lookups for TS/JS external libraries.
type ExternalMethodManager struct {
	methods         map[string]methodEntry // full key → entry
	shortNameIndex  map[string][]string    // short key (ShortClass.method) → full keys
	classTypeParams map[string][]string    // full class name → TypeParams
	shortClassIndex map[string][]string    // short class name → full class names
}

// NewExternalMethodManager loads method mappings from builtin JSONs, embedded framework JSONs
// (filtered by frameworks), and user-defined JSONs in {projectRoot}/.fcg/external/*.json.
func NewExternalMethodManager(frameworks []string, projectRoot string) *ExternalMethodManager {
	manager := &ExternalMethodManager{
		methods:         make(map[string]methodEntry),
		shortNameIndex:  make(map[string][]string),
		classTypeParams: make(map[string][]string),
		shortClassIndex: make(map[string][]string),
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
		fmt.Printf("  ⚠ skip invalid method mapping: %s (%v)\n", source, err)
		return
	}
	for key, value := range entries {
		if strings.Contains(key, "(") {
			// Method declaration
			entry := parseMethodKey(key, value)
			fullKey := entry.ClassName + "." + entry.MethodName + "(" + strings.Join(entry.ParamTypes, ",") + ")"
			manager.methods[fullKey] = entry
			// Short name index
			shortKey := shortClassName(entry.ClassName) + "." + entry.MethodName
			manager.shortNameIndex[shortKey] = appendUnique(manager.shortNameIndex[shortKey], fullKey)
		} else {
			// Class type params declaration
			if value == "" {
				manager.classTypeParams[key] = nil
			} else {
				manager.classTypeParams[key] = strings.Split(value, ",")
			}
			shortName := shortClassName(key)
			manager.shortClassIndex[shortName] = appendUnique(manager.shortClassIndex[shortName], key)
		}
	}
}

// Lookup returns the return type for a method on a given class/module.
// argTypes is used for overload disambiguation when multiple candidates match.
func (manager *ExternalMethodManager) Lookup(className, methodName string, argTypes []string) (model.ReturnType, bool) {
	if manager == nil {
		return model.ReturnType{}, false
	}
	// Normalize TS primitive types to their wrapper class names
	switch className {
	case "string":
		className = "String"
	case "number":
		className = "Number"
	case "boolean":
		className = "Boolean"
	}
	// Try full qualified name match first
	candidates := manager.findMethods(className, methodName)
	if len(candidates) == 0 {
		// Fallback to short name
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

// findMethods finds all method entries matching className.methodName by full qualified prefix.
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

// resolveOverloadFromEntries selects the best matching method entry.
func resolveOverloadFromEntries(candidates []methodEntry, argTypes []string) methodEntry {
	if len(argTypes) == 0 {
		return candidates[0]
	}
	var matched []methodEntry
	for _, candidate := range candidates {
		if len(candidate.ParamTypes) == len(argTypes) {
			matched = append(matched, candidate)
		}
	}
	if len(matched) == 1 {
		return matched[0]
	}
	if len(matched) == 0 {
		return candidates[0]
	}
	return matched[0]
}

// parseMethodKey parses a method key into a methodEntry.
func parseMethodKey(key, value string) methodEntry {
	parenIndex := strings.Index(key, "(")
	prefix := key[:parenIndex]
	dotIndex := strings.LastIndex(prefix, ".")
	if dotIndex < 0 {
		// Standalone function (no class prefix), e.g. "useContext()"
		paramString := key[parenIndex+1 : len(key)-1]
		var paramTypes []string
		if paramString != "" {
			paramTypes = splitParams(paramString)
		}
		return methodEntry{
			ClassName:  "",
			MethodName: prefix,
			ParamTypes: paramTypes,
			ReturnType: value,
		}
	}
	className := prefix[:dotIndex]
	methodName := prefix[dotIndex+1:]

	paramString := key[parenIndex+1 : len(key)-1]
	var paramTypes []string
	if paramString != "" {
		paramTypes = splitParams(paramString)
	}
	return methodEntry{
		ClassName:  className,
		MethodName: methodName,
		ParamTypes: paramTypes,
		ReturnType: value,
	}
}

// splitParams splits parameter types by top-level commas, not splitting inside generics.
func splitParams(paramString string) []string {
	var result []string
	depth := 0
	start := 0
	for i, ch := range paramString {
		switch ch {
		case '<':
			depth++
		case '>':
			depth--
		case ',':
			if depth == 0 {
				result = append(result, paramString[start:i])
				start = i + 1
			}
		}
	}
	result = append(result, paramString[start:])
	return result
}

// shortClassName extracts the short class name from a fully qualified name.
func shortClassName(fullName string) string {
	if index := strings.LastIndex(fullName, "."); index >= 0 {
		return fullName[index+1:]
	}
	return fullName
}

// appendUnique appends value to slice only if not already present.
func appendUnique(slice []string, value string) []string {
	for _, existing := range slice {
		if existing == value {
			return slice
		}
	}
	return append(slice, value)
}
