package service

import (
	"path/filepath"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/config"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// ImportFileMap maps (filePath, symbolName) → resolved target file path.
type ImportFileMap map[resolver.ImportFileKey]string

// ImportPathIndex maps alias import paths (e.g., "@models/User") to real file paths.
type ImportPathIndex map[string]string

// derivePackage converts a file path to a dot-separated package name.
// Index files (index.ts/index.js) use their directory name instead of "index".
// Examples:
//
//	"pkg-a/index.ts"       → "pkg-a"
//	"pkg-a/models/User.ts" → "pkg-a.models.User"
//	"src/utils/index.tsx"  → "src.utils"
//	"index.ts"             → "index"
func derivePackage(filePath string) string {
	base := filePath
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx"} {
		base = strings.TrimSuffix(base, ext)
	}
	base = strings.ReplaceAll(base, "\\", "/")
	if strings.HasSuffix(base, "/index") || base == "index" {
		base = strings.TrimSuffix(base, "/index")
		if base == "" {
			return "index"
		}
	}
	return strings.ReplaceAll(base, "/", ".")
}

// buildImportPathIndex creates a mapping from tsconfig path aliases to real file paths.
// Only triggered for TS/JS projects. Returns nil if no tsconfig paths are configured.
func buildImportPathIndex(tsconfigPaths *config.TsconfigPaths, allFiles []string) ImportPathIndex {
	if tsconfigPaths == nil {
		return nil
	}
	index := make(ImportPathIndex)
	for _, file := range allFiles {
		normalizedFile := strings.ReplaceAll(file, "\\", "/")
		for _, alias := range tsconfigPaths.Aliases {
			for _, target := range alias.Targets {
				baseDir := target
				if tsconfigPaths.BaseUrl != "." && tsconfigPaths.BaseUrl != "" {
					baseDir = tsconfigPaths.BaseUrl + "/" + target
				}
				baseDir = strings.ReplaceAll(baseDir, "\\", "/")
				if strings.HasPrefix(normalizedFile, baseDir) {
					remainder := normalizedFile[len(baseDir):]
					remainder = stripTypeScriptExtension(remainder)
					aliasPath := alias.Prefix + remainder
					if _, exists := index[aliasPath]; !exists {
						index[aliasPath] = file
					}
				}
			}
		}
	}
	if len(index) == 0 {
		return nil
	}
	return index
}

func stripTypeScriptExtension(filePath string) string {
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx"} {
		if strings.HasSuffix(filePath, ext) {
			return filePath[:len(filePath)-len(ext)]
		}
	}
	return filePath
}

// resolveTargetFile resolves an import module path to an actual file path.
// Tries importPathIndex first (for alias paths), then fileIndex (for relative paths).
func resolveTargetFile(modulePath, fromFile string, fileIndex map[string]string, importPathIndex ImportPathIndex) string {
	// Try alias index first
	if importPathIndex != nil {
		if resolved, exists := importPathIndex[modulePath]; exists {
			return resolved
		}
	}

	// Resolve relative path
	if strings.HasPrefix(modulePath, ".") {
		dir := filepath.Dir(fromFile)
		resolved := filepath.Join(dir, modulePath)
		resolved = strings.ReplaceAll(resolved, "\\", "/")
		// Try with extensions
		for _, ext := range []string{".ts", ".tsx", ".js", ".jsx"} {
			if _, exists := fileIndex[resolved+ext]; exists {
				return resolved + ext
			}
		}
		// Try as directory with index file
		for _, ext := range []string{".ts", ".tsx", ".js", ".jsx"} {
			indexPath := resolved + "/index" + ext
			if _, exists := fileIndex[indexPath]; exists {
				return indexPath
			}
		}
	}

	// Fallback: basename matching (existing logic)
	lastSeg := modulePath
	if idx := strings.LastIndex(modulePath, "/"); idx >= 0 {
		lastSeg = modulePath[idx+1:]
	}
	if path, exists := fileIndex[lastSeg]; exists {
		return path
	}
	return ""
}

// findSymbolInFile finds an exported symbol by name in the given file.
// For "default" lookups, matches symbols with IsDefaultExport=true.
func findSymbolInFile(symbolTable *resolver.SymbolTable, filePath, name string) *model.Symbol {
	symbols := symbolTable.FindByFile(filePath)
	for i := range symbols {
		if symbols[i].IsExported {
			if symbols[i].Name == name {
				return &symbols[i]
			}
			if name == "default" && symbols[i].IsDefaultExport {
				return &symbols[i]
			}
		}
	}
	return nil
}

// propagateWaiters cascades re-export registration to all entries waiting on the resolved name.
func propagateWaiters(resolvedReExportName, symbolID string, symbolTable *resolver.SymbolTable, waitingFor map[string][]string) {
	waiters, exists := waitingFor[resolvedReExportName]
	if !exists {
		return
	}
	delete(waitingFor, resolvedReExportName)
	for _, waiterReExportName := range waiters {
		symbolTable.AddReExport(waiterReExportName, symbolID)
		propagateWaiters(waiterReExportName, symbolID, symbolTable, waitingFor)
	}
}

// propagateExports builds the reExportIndex and ImportFileMap by traversing all parse results.
// Uses a single pass with dependency-triggered cascading for multi-layer barrel chains.
func (indexer *Indexer) propagateExports(parseResults []model.ParseResult, symbolTable *resolver.SymbolTable, allFiles []string, importPathIndex ImportPathIndex) ImportFileMap {
	// Build file index for relative path resolution
	fileIndex := make(map[string]string) // filePath → filePath (for existence check)
	for _, filePath := range allFiles {
		fileIndex[filePath] = filePath
		// Also index by normalized path
		normalized := strings.ReplaceAll(filePath, "\\", "/")
		fileIndex[normalized] = filePath
	}

	importFileMap := make(ImportFileMap)
	waitingFor := make(map[string][]string) // awaited reExportQN → []waiter reExportQNs

	// Pass 1: Register local exported symbols
	for _, parseResult := range parseResults {
		if !isTypeScriptFile(parseResult.FilePath) {
			continue
		}
		barrelPackage := derivePackage(parseResult.FilePath)
		for _, symbol := range parseResult.Symbols {
			if symbol.IsExported {
				localReExportName := barrelPackage + "." + symbol.Name
				if !symbolTable.HasReExport(localReExportName) {
					symbolTable.AddReExport(localReExportName, symbol.ID)
				}
			}
		}
	}

	// Pass 2+3: Process named re-exports and wildcards with dependency triggering
	for _, parseResult := range parseResults {
		if !isTypeScriptFile(parseResult.FilePath) {
			continue
		}
		for _, importEntry := range parseResult.Imports {
			targetFile := resolveTargetFile(importEntry.ModulePath, importEntry.FilePath, fileIndex, importPathIndex)
			if targetFile != "" && importEntry.SymbolName != "" {
				importFileMap[resolver.ImportFileKey{FilePath: importEntry.FilePath, SymbolName: importEntry.SymbolName}] = targetFile
			}

			if !importEntry.IsReexport || targetFile == "" {
				continue
			}

			if importEntry.IsWildcard {
				// Wildcard: register all exported symbols from target file
				barrelPackage := derivePackage(importEntry.FilePath)
				exportedSymbols := symbolTable.FindExportedByFile(targetFile)
				for _, symbol := range exportedSymbols {
					reExportName := barrelPackage + "." + symbol.Name
					if !symbolTable.HasReExport(reExportName) {
						symbolTable.AddReExport(reExportName, symbol.ID)
					}
				}
				continue
			}

			// Named re-export
			localName := importEntry.LocalName
			if localName == "" {
				localName = importEntry.SymbolName
			}
			reExportName := derivePackage(importEntry.FilePath) + "." + localName

			if symbolTable.HasReExport(reExportName) {
				continue
			}

			// Try direct resolution: target file defines the symbol
			targetSymbol := findSymbolInFile(symbolTable, targetFile, importEntry.SymbolName)
			if targetSymbol != nil {
				symbolTable.AddReExport(reExportName, targetSymbol.ID)
				propagateWaiters(reExportName, targetSymbol.ID, symbolTable, waitingFor)
				continue
			}

			// Target file is also a barrel → check its reExportIndex
			targetReExportName := derivePackage(targetFile) + "." + importEntry.SymbolName
			if targetID, exists := symbolTable.GetReExport(targetReExportName); exists {
				symbolTable.AddReExport(reExportName, targetID)
				propagateWaiters(reExportName, targetID, symbolTable, waitingFor)
				continue
			}

			// Dependency not yet resolved → register as waiting
			waitingFor[targetReExportName] = append(waitingFor[targetReExportName], reExportName)
		}
	}

	// Remaining entries in waitingFor are unresolvable (target outside project or broken chain)
	return importFileMap
}

// isTypeScriptFile checks if a file is a TypeScript or JavaScript file.
func isTypeScriptFile(filePath string) bool {
	return strings.HasSuffix(filePath, ".ts") ||
		strings.HasSuffix(filePath, ".tsx") ||
		strings.HasSuffix(filePath, ".js") ||
		strings.HasSuffix(filePath, ".jsx")
}
