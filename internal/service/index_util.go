package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kirovcaptain/FlashCodeGraph/internal/config"
	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
)

func AutoInit(projectDir string) error {
	configPath := config.ProjectConfigPath(projectDir)
	if _, err := os.Stat(configPath); err == nil {
		return nil // already exists
	}

	scannerInstance := scanner.New(&scanner.Config{RootPath: projectDir})
	projectInfo, _ := scannerInstance.DetectProject()

	projectName := filepath.Base(projectDir)
	projectType := "unknown"
	var languages []string
	if projectInfo != nil {
		projectType = projectInfo.ProjectType
	}

	return config.GenerateDefault(configPath, projectName, projectType, languages)
}

// Internal methods

func (indexer *Indexer) saveFingerprints(ctx context.Context, branch string, repoPath string, allFiles []scanner.ScannedFile) {
	fingerprints, _ := indexer.fingerprintStore.Load(ctx, branch)
	if fingerprints == nil {
		fingerprints = make(map[string]model.Fingerprint)
	}
	// Remove deleted files from fingerprints
	currentPaths := make(map[string]bool, len(allFiles))
	for _, file := range allFiles {
		currentPaths[file.RelPath] = true
	}
	for path := range fingerprints {
		if !currentPaths[path] {
			delete(fingerprints, path)
		}
	}
	// Update all scanned files
	for _, file := range allFiles {
		fingerprints[file.RelPath] = model.Fingerprint{
			ModTime: file.ModTime,
			Size:    file.Size,
		}
	}
	meta := &storage.FingerprintMeta{
		LastIndexedAt: time.Now().Unix(),
		LastCommit:    readGitHeadCommit(repoPath),
	}
	indexer.fingerprintStore.Save(ctx, branch, fingerprints, meta)
}

// readGitHeadCommit reads the current HEAD commit SHA from the git repository.
// Returns empty string if not a git project or on any error.
func readGitHeadCommit(repoPath string) string {
	headPath := filepath.Join(repoPath, ".git", "HEAD")
	headContent, err := os.ReadFile(headPath)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(headContent))

	// Detached HEAD — line is the commit SHA itself
	if !strings.HasPrefix(line, "ref: ") {
		return line
	}

	// Symbolic ref — read the referenced file
	refPath := filepath.Join(repoPath, ".git", strings.TrimPrefix(line, "ref: "))
	commitContent, err := os.ReadFile(refPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(commitContent))
}

// findImporters finds files that import any of the changed/deleted files.
// These files need their edges rebuilt because their call targets may have changed.
// loadAllSymbols loads all existing symbols from the graph into SymbolTable.
// In incremental mode, changed files' nodes are already deleted from the graph,
// so this loads only unchanged files' symbols. Combined with parseResults' symbols,
// this gives a complete SymbolTable for accurate cross-file call resolution.
func (indexer *Indexer) loadAllSymbols(ctx context.Context, filesToParse []scanner.ScannedFile, symbolTable *resolver.SymbolTable) {
	parsedFiles := make(map[string]bool, len(filesToParse))
	for _, f := range filesToParse {
		parsedFiles[f.RelPath] = true
	}

	for _, kind := range constants.BaseSymbolKinds {
		nodes, err := indexer.graphStore.QueryAllByKind(ctx, kind, 0)
		if err != nil {
			continue
		}
		for _, node := range nodes {
			filePath, _ := node.Properties["file_path"].(string)
			if parsedFiles[filePath] {
				continue // already in symbolTable from parseResults
			}
			name, _ := node.Properties["name"].(string)
			qualifiedName, _ := node.Properties["qualified_name"].(string)
			if name == "" {
				continue
			}
			paramsStr, _ := node.Properties["params"].(string)
			isExported, _ := node.Properties["is_exported"].(bool)
			classType, _ := node.Properties["class_type"].(string)
			annotationsStr, _ := node.Properties["annotations"].(string)
			isGetter, _ := node.Properties["is_getter"].(bool)
			isSetter, _ := node.Properties["is_setter"].(bool)
			returnTypes := toStringSlice(node.Properties["return_types"])
			var parsedAnnotations []model.StructuredAnnotation
			if annotationsStr != "" && annotationsStr != "[]" {
				json.Unmarshal([]byte(annotationsStr), &parsedAnnotations)
			}
			symbolTable.Add(model.Symbol{
				ID:            node.ID,
				Name:          name,
				QualifiedName: qualifiedName,
				Kind:          kind,
				FilePath:      filePath,
				Params:        unmarshalParams(paramsStr),
				IsExported:    isExported,
				ClassType:     classType,
				Annotations:   parsedAnnotations,
				IsGetter:      isGetter,
				IsSetter:      isSetter,
				ReturnTypes:   model.StringsToReturnTypes(returnTypes),
			})
		}
	}
}

func (indexer *Indexer) findImporters(ctx context.Context, allFiles []scanner.ScannedFile, changedFiles []scanner.ScannedFile, deletedFiles []string) []scanner.ScannedFile {
	// Collect changed file paths
	changedPaths := make(map[string]bool)
	for _, f := range changedFiles {
		changedPaths[f.RelPath] = true
	}
	for _, p := range deletedFiles {
		changedPaths[p] = true
	}

	// Query all IMPORTS edges to find importers
	allImports, err := indexer.graphStore.QueryAllEdges(ctx, model.RelImports, 100000)
	if err != nil {
		return nil
	}

	importerPaths := make(map[string]bool)
	for _, edge := range allImports {
		targetPath := strings.TrimPrefix(edge.TargetID, "file:")
		if changedPaths[targetPath] {
			sourcePath := strings.TrimPrefix(edge.SourceID, "file:")
			if !changedPaths[sourcePath] {
				importerPaths[sourcePath] = true
			}
		}
	}

	// Look up full ScannedFile info from allFiles
	var affected []scanner.ScannedFile
	for _, f := range allFiles {
		if importerPaths[f.RelPath] {
			affected = append(affected, f)
		}
	}
	return affected
}

func (indexer *Indexer) cleanEmptyDirectories(ctx context.Context, deletedFiles []string) {
	// Collect directories of deleted files
	dirs := make(map[string]bool)
	for _, path := range deletedFiles {
		dir := filepath.Dir(path)
		for dir != "." && dir != "" {
			dirs[dir] = true
			dir = filepath.Dir(dir)
		}
	}
	// Check each directory — if no CONTAINS edges remain, delete it
	for dir := range dirs {
		dirID := fmt.Sprintf("dir:%s", dir)
		edges, err := indexer.graphStore.QueryEdges(ctx, dirID, constants.KindDirectory, model.RelContains, model.Outgoing)
		if err != nil || len(edges) == 0 {
			indexer.graphStore.DeleteNodeByID(ctx, dirID)
		}
	}
}

func capitalizeFirst(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func isTestFilePath(filePath string) bool {
	lower := strings.ToLower(filePath)
	return strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/") ||
		strings.Contains(lower, "_test.") || strings.Contains(lower, ".test.")
}

// parseAnnotationNames extracts annotation names from structured annotation slice.
func parseAnnotationNames(annotations []model.StructuredAnnotation) []string {
	if len(annotations) == 0 {
		return nil
	}
	names := make([]string, 0, len(annotations))
	for _, annotation := range annotations {
		names = append(names, annotation.Name)
	}
	return names
}

// extractParamTypeNames extracts type names from a ParamInfo slice.
func extractParamTypeNames(params []model.ParamInfo) []string {
	types := make([]string, 0, len(params))
	for _, p := range params {
		types = append(types, p.Type)
	}
	return types
}

// marshalParams serializes ParamInfo slice to JSON string for storage.
func marshalConditions(conditions []model.ConditionalFragment) string {
	if len(conditions) == 0 {
		return ""
	}
	data, _ := json.Marshal(conditions)
	return string(data)
}

func marshalParams(params []model.ParamInfo) string {
	if len(params) == 0 {
		return "[]"
	}
	data, _ := json.Marshal(params)
	return string(data)
}

// marshalAnnotations serializes structured annotations to JSON string for storage.
func marshalAnnotations(annotations []model.StructuredAnnotation) string {
	if len(annotations) == 0 {
		return "[]"
	}
	data, _ := json.Marshal(annotations)
	return string(data)
}

// hasAnnotationNamed checks if annotations slice contains an annotation with the given name.
func hasAnnotationNamed(annotations []model.StructuredAnnotation, name string) bool {
	for _, annotation := range annotations {
		if annotation.Name == name {
			return true
		}
	}
	return false
}

// unmarshalParams deserializes a JSON params string to ParamInfo slice.
// Handles legacy format where "default" is a string "true" instead of bool.
func unmarshalParams(s string) []model.ParamInfo {
	if s == "" || s == "null" || s == "[]" {
		return nil
	}
	var raw []map[string]any
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil
	}
	params := make([]model.ParamInfo, 0, len(raw))
	for _, r := range raw {
		name, _ := r["name"].(string)
		typ, _ := r["type"].(string)
		hasDefault := r["default"] == "true" || r["default"] == true
		params = append(params, model.ParamInfo{Name: name, Type: typ, HasDefault: hasDefault})
	}
	return params
}

// firstReturnType returns the first return type name or empty string.
func firstReturnType(returnTypes []model.ReturnType) string {
	if len(returnTypes) > 0 {
		return returnTypes[0].Name
	}
	return ""
}

// toStringSlice converts []interface{} (from FalkorDB array column) to []string.
func toStringSlice(value interface{}) []string {
	if value == nil {
		return nil
	}
	if stringSlice, ok := value.([]string); ok {
		return stringSlice
	}
	interfaceSlice, ok := value.([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(interfaceSlice))
	for _, element := range interfaceSlice {
		if stringValue, ok := element.(string); ok {
			result = append(result, stringValue)
		}
	}
	return result
}

// extractRouteFromAnnotations parses route method and path from structured annotations.
func extractRouteFromAnnotations(annotations []model.StructuredAnnotation) (string, string) {
	if len(annotations) == 0 {
		return "", ""
	}
	routeAnnotations := map[string]string{
		"GetMapping": "GET", "PostMapping": "POST",
		"PutMapping": "PUT", "DeleteMapping": "DELETE", "PatchMapping": "PATCH",
	}
	for _, annotation := range annotations {
		if httpMethod, ok := routeAnnotations[annotation.Name]; ok {
			return httpMethod, annotation.Params["value"]
		}
	}
	return "", ""
}
