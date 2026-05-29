package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func (indexer *Indexer) parseSourceFiles(ctx context.Context, scanCtx *scanContext, filesToParse []scanner.ScannedFile) ([]model.ParseResult, *resolver.SymbolTable) {
	sourceFiles := filterByCategory(filesToParse, constants.FileSource)
	indexer.progress.Emit(PhaseParsing, 0, len(sourceFiles), "")
	cacheDir := filepath.Join(scanCtx.absPath, ".fcg", "cache", "parse")
	parseResults, symbolTable, parseErrors := indexer.parseFiles(ctx, sourceFiles, cacheDir)
	scanCtx.result.Errors = append(scanCtx.result.Errors, parseErrors...)
	indexer.progress.Emit(PhaseParsing, len(sourceFiles), len(sourceFiles), fmt.Sprintf("%d/%d files", len(sourceFiles), len(sourceFiles)))
	return parseResults, symbolTable
}

// parseAllFiles parses all file categories: source + query_def + schema_def (full mode).
func (indexer *Indexer) parseAllFiles(ctx context.Context, scanCtx *scanContext, filesToParse []scanner.ScannedFile) ([]model.ParseResult, *resolver.SymbolTable) {
	parseResults, symbolTable := indexer.parseSourceFiles(ctx, scanCtx, filesToParse)
	defFiles := filterNonSource(filesToParse)
	if len(defFiles) > 0 {
		indexer.progress.EmitSub(PhaseParsing, SubParseDefFiles, "")
		defResults := indexer.parseDefFiles(scanCtx, defFiles)
		parseResults = append(parseResults, defResults...)
		indexer.progress.EmitSub(PhaseParsing, SubParseDefFiles, fmt.Sprintf("%d files", len(defResults)))
	}
	return parseResults, symbolTable
}

// parseDefFiles parses def-category files using ORM and Schema managers.
func (indexer *Indexer) parseDefFiles(scanCtx *scanContext, files []scanner.ScannedFile) []model.ParseResult {
	var results []model.ParseResult
	for _, file := range files {
		content, err := os.ReadFile(file.Path)
		if err != nil {
			continue
		}
		var result *model.ParseResult
		switch file.Category {
		case constants.FileQueryDef:
			result = scanCtx.ormManager.Parse(content, file.RelPath)
		case constants.FileSchemaDef:
			result = scanCtx.schemaManager.Parse(content, file.RelPath)
		}
		if result != nil {
			results = append(results, *result)
		}
	}
	return results
}

func filterByCategory(files []scanner.ScannedFile, category string) []scanner.ScannedFile {
	var filtered []scanner.ScannedFile
	for _, f := range files {
		if f.Category == category {
			filtered = append(filtered, f)
		}
	}
	return filtered
}

func filterNonSource(files []scanner.ScannedFile) []scanner.ScannedFile {
	var filtered []scanner.ScannedFile
	for _, f := range files {
		if f.Category == constants.FileQueryDef || f.Category == constants.FileSchemaDef {
			filtered = append(filtered, f)
		}
	}
	return filtered
}

func (indexer *Indexer) filterChangedFiles(ctx context.Context, branch string, files []scanner.ScannedFile) (changed []scanner.ScannedFile, deleted []string, err error) {
	fingerprints, err := indexer.fingerprintStore.Load(ctx, branch)
	if err != nil {
		return files, nil, err
	}

	currentPaths := make(map[string]bool, len(files))
	for _, file := range files {
		currentPaths[file.RelPath] = true
		existing, exists := fingerprints[file.RelPath]
		if !exists || existing.ModTime != file.ModTime || existing.Size != file.Size {
			changed = append(changed, file)
		}
	}

	// Detect deleted files: in fingerprints but not in current scan
	for path := range fingerprints {
		if !currentPaths[path] {
			deleted = append(deleted, path)
		}
	}
	return changed, deleted, nil
}

func (indexer *Indexer) parseFiles(ctx context.Context, files []scanner.ScannedFile, cacheDir string) ([]model.ParseResult, *resolver.SymbolTable, []model.IndexError) {
	goroutineCount := runtime.NumCPU()
	if indexer.config.System.Goroutines > 0 {
		goroutineCount = indexer.config.System.Goroutines
	}

	symbolTable := resolver.NewSymbolTable()
	var (
		results     []model.ParseResult
		errors      []model.IndexError
		mutex       sync.Mutex
		waitGroup   sync.WaitGroup
		fileChannel = make(chan scanner.ScannedFile, goroutineCount*2)
		processed   int
	)

	// Worker goroutines
	for i := 0; i < goroutineCount; i++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			fileParser := parser.New(cacheDir)
			defer fileParser.Close()

			for file := range fileChannel {
				content, err := os.ReadFile(file.Path)
				if err != nil {
					mutex.Lock()
					errors = append(errors, model.IndexError{FilePath: file.Path, Phase: "parse", Message: err.Error()})
					mutex.Unlock()
					continue
				}

				parseResult, err := fileParser.ParseFile(ctx, file, content)
				if err != nil {
					mutex.Lock()
					errors = append(errors, model.IndexError{FilePath: file.Path, Phase: "parse", Message: err.Error()})
					mutex.Unlock()
					continue
				}

				// Build SymbolTable concurrently
				symbolTable.AddBatch(parseResult.Symbols)
				// Register fields in SymbolTable for type inference
				for _, fieldDeclaration := range parseResult.Fields {
					symbolTable.AddField(fieldDeclaration.OwnerQualifiedName, fieldDeclaration.FieldInfo)
				}

				mutex.Lock()
				results = append(results, *parseResult)
				processed++
				mutex.Unlock()
			}
		}()
	}

	// Feed files
	for _, file := range files {
		fileChannel <- file
	}
	close(fileChannel)
	waitGroup.Wait()

	return results, symbolTable, errors
}

func collectRawRelations(results []model.ParseResult) ([]model.RawImport, []model.RawCall, []model.RawHeritage) {
	var imports []model.RawImport
	var calls []model.RawCall
	var heritage []model.RawHeritage
	for _, result := range results {
		imports = append(imports, result.Imports...)
		calls = append(calls, result.Calls...)
		heritage = append(heritage, result.Heritage...)
	}
	return imports, calls, heritage
}

func collectSourceFilePaths(files []scanner.ScannedFile) []string {
	var paths []string
	for _, file := range files {
		if file.Category == constants.FileSource {
			paths = append(paths, file.RelPath)
		}
	}
	return paths
}

func clearParseResultsPhase1(parseResults []model.ParseResult) {
	for i := range parseResults {
		parseResults[i].Fields = nil
		parseResults[i].Routes = nil
		parseResults[i].Queries = nil
	}
	runtime.GC()
}

// clearParseResultsPhase2 nils fields consumed by resolveAndWriteRelations and resolvePendingRemoteCalls.
// Call after flattening allRemoteCalls/allPendingCalls, before matchConsumerToProvider.
func clearParseResultsPhase2(parseResults []model.ParseResult) {
	for i := range parseResults {
		parseResults[i].Symbols = nil
		parseResults[i].Calls = nil
		parseResults[i].Imports = nil
		parseResults[i].Heritage = nil
		parseResults[i].TypeHints = nil
		parseResults[i].PendingAssignments = nil
		parseResults[i].RemoteCalls = nil
		parseResults[i].PendingRemoteCalls = nil
	}
	runtime.GC()
}
