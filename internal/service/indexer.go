// Package service implements the service layer orchestrators.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"strings"
	"time"

	"github.com/kirovcaptain/FlashCodeGraph/internal/config"
	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/annotation"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/defparser"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/framework"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	resolverjava "github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver/java"
	resolvergo "github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver/golang"
	resolverpy "github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver/python"
	resolverts "github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver/typescript"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/typeinfer"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/crossindex"
)

// Indexer orchestrates the full indexing pipeline (Phase 0-8).
type Indexer struct {
	graphStore       storage.GraphStore
	fingerprintStore storage.FingerprintStore
	indexLock        storage.IndexLock
	config           *config.Config
	progress         *ProgressManager
	dump             DumpManager
	crossIndex       crossindex.CrossProjectIndex
	mu               sync.Mutex
}

// NewIndexer creates an Indexer.
func NewIndexer(
	graphStore storage.GraphStore,
	fingerprintStore storage.FingerprintStore,
	indexLock storage.IndexLock,
	cfg *config.Config,
	dump DumpManager,
	crossIndex crossindex.CrossProjectIndex,
) *Indexer {
	if dump == nil {
		dump = NopDumpManager{}
	}
	return &Indexer{
		graphStore:       graphStore,
		fingerprintStore: fingerprintStore,
		indexLock:        indexLock,
		config:           cfg,
		dump:             dump,
		crossIndex:       crossIndex,
	}
}

// Index runs the full indexing pipeline.
// scanContext holds shared data from project scanning phases.
type scanContext struct {
	absPath             string
	branch              string
	files               []scanner.ScannedFile
	projectInfo         *scanner.ProjectInfo
	annotationWhitelist map[string]annotation.AnnotationDef
	ormManager          *defparser.Manager
	schemaManager       *defparser.Manager
	result              *model.IndexResult
	startTime           time.Time
}

func (indexer *Indexer) Index(ctx context.Context, repoPath string, branch string, forceFullIndex bool, onProgress model.ProgressCallback) (*model.IndexResult, error) {
	indexer.mu.Lock()
	defer indexer.mu.Unlock()

	indexer.progress = NewProgressManager(onProgress)

	fullMode := indexer.shouldFullIndex(ctx, branch, forceFullIndex)
	if fullMode {
		indexer.progress.SetMode(fullPhaseList)
	} else {
		indexer.progress.SetMode(incrementalPhaseList)
	}

	scanCtx, err := indexer.scanProject(ctx, repoPath, branch)
	if err != nil {
		return nil, err
	}

	if fullMode {
		return indexer.fullIndex(ctx, scanCtx)
	}
	return indexer.incrementalIndex(ctx, scanCtx)
}

// shouldFullIndex determines whether a full rebuild is needed.
func (indexer *Indexer) shouldFullIndex(ctx context.Context, branch string, force bool) bool {
	if force {
		return true
	}
	stats, err := indexer.graphStore.GetStats(ctx)
	if err == nil && stats.NodeCount == 0 {
		return true
	}
	fps, err := indexer.fingerprintStore.Load(ctx, branch)
	if err != nil {
		return true
	}
	return len(fps) == 0
}

func (indexer *Indexer) scanProject(ctx context.Context, repoPath string, branch string) (*scanContext, error) {
	startTime := time.Now()
	result := &model.IndexResult{
		SymbolsByKind:   make(map[string]int),
		RelationsByKind: make(map[string]int),
		FilesByLanguage: make(map[string]int),
		EntriesByType:   make(map[string]int),
	}

	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("indexer: resolve path: %w", err)
	}

	// Phase 0: Project structure detection
	indexer.progress.Emit(PhaseProjectDetection, 0, 0, "")
	projectScanner := scanner.New(&scanner.Config{
		RootPath:     absPath,
		MaxFileSize:  indexer.config.Index.MaxFileSize,
		ExcludeTests: indexer.config.Index.ExcludeTests,
		IgnoreRules:  indexer.config.Index.Ignore,
	})
	projectInfo, err := projectScanner.DetectProject()
	if err != nil {
		return nil, fmt.Errorf("indexer: phase 0: %w", err)
	}
	// Framework detection
	detected := framework.Detect(absPath, projectInfo.BuildFiles)
	projectInfo.Frameworks = make([]string, 0, len(detected))
	for _, fw := range detected {
		projectInfo.Frameworks = append(projectInfo.Frameworks, fw.Name)
	}

	indexer.progress.Emit(PhaseProjectDetection, 1, 1, fmt.Sprintf("%s project, %d submodules, frameworks=%v", projectInfo.ProjectType, len(projectInfo.SubModules), projectInfo.Frameworks))

	annotationWhitelist := annotation.BuildWhitelist(projectInfo.Frameworks, indexer.config.Annotations.Include, indexer.config.Annotations.Exclude)

	// Build def parsers from detected frameworks
	ormManager, schemaManager := defparser.BuildManagers(projectInfo.Frameworks)

	// Phase 1: File scanning (def extensions drive non-source file collection)
	indexer.progress.Emit(PhaseFileScan, 0, 0, "")
	projectScanner.SetDefExtensions(ormManager.Extensions(), schemaManager.Extensions())
	files, skippedFiles, err := projectScanner.Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("indexer: phase 1: %w", err)
	}
	result.FilesSkipped = len(skippedFiles)
	result.SkippedFiles = skippedFiles
	for _, file := range files {
		if file.Category == constants.FileSource {
			result.FilesScanned++
			result.FilesByLanguage[file.Language]++
		}
	}

	// Determine primary language from file counts
	projectInfo.Language = determinePrimaryLanguage(result.FilesByLanguage)

	// Filter out non-primary-language source files
	if projectInfo.Language != "" {
		skippedByLanguage := make(map[string]int)
		var primaryFiles []scanner.ScannedFile
		for _, file := range files {
			if file.Category == constants.FileSource && file.Language != projectInfo.Language {
				// TS and JS are treated as the same language family
				if !isSameLanguageFamily(file.Language, projectInfo.Language) {
					skippedByLanguage[file.Language]++
					continue
				}
			}
			primaryFiles = append(primaryFiles, file)
		}
		if len(skippedByLanguage) > 0 {
			var parts []string
			for lang, count := range skippedByLanguage {
				parts = append(parts, fmt.Sprintf("%d %s", count, lang))
			}
			indexer.progress.EmitSub(PhaseFileScan, SubFilterLanguage, "")
			indexer.progress.EmitSub(PhaseFileScan, SubFilterLanguage, fmt.Sprintf("⚠ skipped %s files (project language: %s)", strings.Join(parts, ", "), projectInfo.Language))
		}
		files = primaryFiles
	}

	indexer.progress.Emit(PhaseFileScan, result.FilesScanned, result.FilesScanned, fmt.Sprintf("%d files, language=%s", result.FilesScanned, projectInfo.Language))

	return &scanContext{
		absPath:             absPath,
		branch:              branch,
		files:               files,
		projectInfo:         projectInfo,
		annotationWhitelist: annotationWhitelist,
		ormManager:          ormManager,
		schemaManager:       schemaManager,
		result:              result,
		startTime:           startTime,
	}, nil
}

func (indexer *Indexer) fullIndex(ctx context.Context, scanCtx *scanContext) (*model.IndexResult, error) {
	// Clean entire graph + parse cache
	indexer.progress.EmitSub(PhaseWriting, SubCleanGraph, "")
	indexer.graphStore.ClearAll(ctx)
	// Recreate indexes after graph deletion
	indexer.graphStore.Migrate(ctx)
	os.RemoveAll(filepath.Join(scanCtx.absPath, ".fcg", "cache"))
	indexer.progress.EmitSub(PhaseWriting, SubCleanGraph, "done")

	// Parse all files
	parseResults, symbolTable := indexer.parseAllFiles(ctx, scanCtx, scanCtx.files)

	// Write structural nodes: full rebuild from scratch (all CREATE)
	indexer.progress.Emit(PhaseWriting, 0, 0, "nodes and edges")
	indexer.progress.EmitSub(PhaseWriting, SubStructuralNodes, "")
	symbolEdges, err := indexer.writeFileSystemNodes(ctx, scanCtx.absPath, scanCtx.projectInfo.Frameworks, scanCtx.files, parseResults, scanCtx.result)
	if err != nil {
		return nil, fmt.Errorf("indexer: write structural: %w", err)
	}
	indexer.progress.EmitSub(PhaseWriting, SubStructuralNodes, fmt.Sprintf("%d files", scanCtx.result.FilesScanned))

	// Step 7.5: Resolve service name placeholders (before writeSemanticNodes so Step 6 uses resolved names)
	indexer.resolveServiceNamePlaceholders(parseResults)

	// Write all other nodes and edges
	if err := indexer.writeSemanticNodes(ctx, scanCtx, parseResults, symbolTable); err != nil {
		return nil, err
	}

	// Write symbol-related CONTAINS edges (File→Symbol, Class→Function) after symbol nodes exist
	if err := indexer.writeSymbolContainsEdges(ctx, symbolEdges); err != nil {
		return nil, err
	}

	// Resolve and write relationships
	if err := indexer.resolveAndWriteRelations(ctx, scanCtx, parseResults, symbolTable); err != nil {
		return nil, err
	}

	// Preload Route nodes and HANDLES edges once for Step 8 + Step 9
	allRoutes, _ := indexer.graphStore.QueryAllByKind(ctx, constants.KindRoute, 0)
	handlesEdges, _ := indexer.graphStore.QueryAllEdges(ctx, model.RelHandles, 0)
	routeToHandler := make(map[string]string, len(handlesEdges))
	for _, edge := range handlesEdges {
		routeToHandler[edge.TargetID] = edge.SourceID
	}

	// Flatten RemoteCalls for Step 8
	var allRemoteCalls []model.RawRemoteCall
	for _, parseResult := range parseResults {
		allRemoteCalls = append(allRemoteCalls, parseResult.RemoteCalls...)
	}

	// Step 8: Match consumer to provider (cross-service CALLS edges)
	if err := indexer.matchConsumerToProvider(ctx, scanCtx, allRemoteCalls, symbolTable, allRoutes, routeToHandler); err != nil {
		return nil, fmt.Errorf("indexer: match consumer to provider: %w", err)
	}

	// Step 9: Write cross-project index
	if err := indexer.writeCrossProjectIndex(ctx, scanCtx, symbolTable, allRoutes, routeToHandler); err != nil {
		return nil, fmt.Errorf("indexer: write cross-project index: %w", err)
	}

	indexer.progress.EmitSub(PhaseResolving, SubSaveFingerprints, "")
	indexer.saveFingerprints(ctx, scanCtx.branch, scanCtx.absPath, scanCtx.files)
	indexer.progress.EmitSub(PhaseResolving, SubSaveFingerprints, fmt.Sprintf("%d files", len(scanCtx.files)))
	scanCtx.result.FilesProcessed = scanCtx.result.FilesScanned
	scanCtx.result.DurationMs = time.Since(scanCtx.startTime).Milliseconds()
	indexer.progress.Emit(PhaseComplete, 1, 1, "done")
	return scanCtx.result, nil
}

func (indexer *Indexer) incrementalIndex(ctx context.Context, scanCtx *scanContext) (*model.IndexResult, error) {
	// Detect changes
	indexer.progress.Emit(PhaseIncremental, 0, 0, "")
	changedFiles, deletedFiles, err := indexer.filterChangedFiles(ctx, scanCtx.branch, scanCtx.files)
	if err != nil {
		return nil, fmt.Errorf("indexer: filter changes: %w", err)
	}
	indexer.progress.Emit(PhaseIncremental, len(changedFiles), len(changedFiles), fmt.Sprintf("%d changed files", len(changedFiles)))

	// Find affected importers BEFORE cleanup (DETACH DELETE removes IMPORTS edges)
	indexer.progress.EmitSub(PhaseWriting, SubFindAffected, "")
	affectedFiles := indexer.findImporters(ctx, scanCtx.files, changedFiles, deletedFiles)
	indexer.progress.EmitSub(PhaseWriting, SubFindAffected, fmt.Sprintf("%d importers", len(affectedFiles)))

	// Clean deleted files
	indexer.progress.EmitSub(PhaseWriting, SubCleanChanged, "")
	for _, path := range deletedFiles {
		indexer.graphStore.DeleteNodesByFile(ctx, path)
		indexer.graphStore.DeleteNodeByID(ctx, fmt.Sprintf("file:%s", path))
	}
	// Clean empty directory nodes left by deletions
	if len(deletedFiles) > 0 {
		indexer.cleanEmptyDirectories(ctx, deletedFiles)
	}

	// Clean changed files
	for _, file := range changedFiles {
		if err := indexer.graphStore.DeleteNodesByFile(ctx, file.RelPath); err != nil {
			scanCtx.result.Errors = append(scanCtx.result.Errors, model.IndexError{
				FilePath: file.RelPath,
				Message:  fmt.Sprintf("delete old nodes: %v", err),
			})
		}
		indexer.graphStore.DeleteNodeByID(ctx, fmt.Sprintf("file:%s", file.RelPath))
	}
	indexer.progress.EmitSub(PhaseWriting, SubCleanChanged, fmt.Sprintf("%d deleted, %d changed", len(deletedFiles), len(changedFiles)))

	if len(changedFiles) == 0 {
		if len(deletedFiles) > 0 {
			indexer.saveFingerprints(ctx, scanCtx.branch, scanCtx.absPath, scanCtx.files)
		}
		scanCtx.result.DurationMs = time.Since(scanCtx.startTime).Milliseconds()
		return scanCtx.result, nil
	}

	// Clean affected files
	for _, af := range affectedFiles {
		indexer.graphStore.DeleteNodesByFile(ctx, af.RelPath)
		indexer.graphStore.DeleteNodeByID(ctx, fmt.Sprintf("file:%s", af.RelPath))
	}
	filesToParse := append(changedFiles, affectedFiles...)

	// Parse changed + affected files
	parseResults, symbolTable := indexer.parseSourceFiles(ctx, scanCtx, filesToParse)
	// Also parse changed def files (XML mappers, GraphQL schemas)
	defFiles := filterNonSource(changedFiles)
	if len(defFiles) > 0 {
		parseResults = append(parseResults, indexer.parseDefFiles(scanCtx, defFiles)...)
	}

	// Load symbols from import targets to complete SymbolTable for cross-file resolution
	indexer.progress.EmitSub(PhaseWriting, SubLoadSymbols, "")
	indexer.loadAllSymbols(ctx, filesToParse, symbolTable)
	indexer.progress.EmitSub(PhaseWriting, SubLoadSymbols, fmt.Sprintf("%d symbols", len(symbolTable.All())))

	// Write structural nodes for changed + affected files only
	// Repository/Directory use MERGE (already exist), File uses CREATE (deleted in Phase 3)
	indexer.progress.EmitSub(PhaseWriting, SubStructuralNodes, "")
	symbolEdges, err := indexer.writeIncrementalFileSystemNodes(ctx, scanCtx.absPath, scanCtx.projectInfo.Frameworks, filesToParse, parseResults, scanCtx.result)
	if err != nil {
		return nil, fmt.Errorf("indexer: write structural: %w", err)
	}
	indexer.progress.EmitSub(PhaseWriting, SubStructuralNodes, fmt.Sprintf("%d files", len(filesToParse)))

	// Step 7.5: Resolve service name placeholders (before writeSemanticNodes so Step 6 uses resolved names)
	indexer.resolveServiceNamePlaceholders(parseResults)

	// Write all other nodes and edges
	if err := indexer.writeSemanticNodes(ctx, scanCtx, parseResults, symbolTable); err != nil {
		return nil, err
	}

	// Write symbol-related CONTAINS edges (File→Symbol, Class→Function) after symbol nodes exist
	if err := indexer.writeSymbolContainsEdges(ctx, symbolEdges); err != nil {
		return nil, err
	}

	// Resolve and write relationships
	if err := indexer.resolveAndWriteRelations(ctx, scanCtx, parseResults, symbolTable); err != nil {
		return nil, err
	}

	// Preload Route nodes and HANDLES edges once for Step 8 + Step 9
	allRoutes, _ := indexer.graphStore.QueryAllByKind(ctx, constants.KindRoute, 0)
	handlesEdges, _ := indexer.graphStore.QueryAllEdges(ctx, model.RelHandles, 0)
	routeToHandler := make(map[string]string, len(handlesEdges))
	for _, edge := range handlesEdges {
		routeToHandler[edge.TargetID] = edge.SourceID
	}

	// Flatten RemoteCalls for Step 8
	var allRemoteCalls []model.RawRemoteCall
	for _, parseResult := range parseResults {
		allRemoteCalls = append(allRemoteCalls, parseResult.RemoteCalls...)
	}

	// Step 8: Match consumer to provider (cross-service CALLS edges)
	if err := indexer.matchConsumerToProvider(ctx, scanCtx, allRemoteCalls, symbolTable, allRoutes, routeToHandler); err != nil {
		return nil, fmt.Errorf("indexer: match consumer to provider: %w", err)
	}

	// Step 9: Write cross-project index
	if err := indexer.writeCrossProjectIndex(ctx, scanCtx, symbolTable, allRoutes, routeToHandler); err != nil {
		return nil, fmt.Errorf("indexer: write cross-project index: %w", err)
	}

	indexer.progress.EmitSub(PhaseResolving, SubSaveFingerprints, "")
	indexer.saveFingerprints(ctx, scanCtx.branch, scanCtx.absPath, scanCtx.files)
	indexer.progress.EmitSub(PhaseResolving, SubSaveFingerprints, fmt.Sprintf("%d files", len(scanCtx.files)))
	scanCtx.result.FilesProcessed = len(filterByCategory(filesToParse, constants.FileSource))
	scanCtx.result.DurationMs = time.Since(scanCtx.startTime).Milliseconds()
	indexer.progress.Emit(PhaseComplete, 1, 1, "done")
	return scanCtx.result, nil
}

// parseSourceFiles parses source code files only (incremental mode).
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
		defResults := indexer.parseDefFiles(scanCtx, filesToParse)
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

// writeSemanticNodes writes code-level semantic data to the graph:
// Function/Class/Interface symbols, Route endpoints, Query nodes (SQL/ORM),
// Annotation nodes, and RemoteCall edges. Does not handle file system structure.
func (indexer *Indexer) writeSemanticNodes(ctx context.Context, scanCtx *scanContext, parseResults []model.ParseResult, symbolTable *resolver.SymbolTable) error {

	indexer.progress.EmitSub(PhaseWriting, SubSymbolNodes, "")
	if err := indexer.writeSymbolNodes(ctx, parseResults, scanCtx.result); err != nil {
		return fmt.Errorf("indexer: write symbols: %w", err)
	}
	indexer.progress.EmitSub(PhaseWriting, SubSymbolNodes, fmt.Sprintf("%d symbols", scanCtx.result.SymbolsCreated))

	indexer.progress.EmitSub(PhaseWriting, SubRouteNodes, "")
	if err := indexer.writeRouteNodes(ctx, parseResults, symbolTable, scanCtx.result); err != nil {
		return fmt.Errorf("indexer: write routes: %w", err)
	}
	indexer.progress.EmitSub(PhaseWriting, SubRouteNodes, fmt.Sprintf("%d routes", scanCtx.result.SymbolsByKind["route"]))

	indexer.progress.EmitSub(PhaseWriting, SubQueryNodes, "")
	if err := indexer.writeQueryNodes(ctx, parseResults, symbolTable, scanCtx.result); err != nil {
		return fmt.Errorf("indexer: write queries: %w", err)
	}
	indexer.progress.EmitSub(PhaseWriting, SubQueryNodes, fmt.Sprintf("%d queries", scanCtx.result.SymbolsByKind["query"]))

	indexer.progress.EmitSub(PhaseWriting, SubAnnotationNodes, "")
	if err := indexer.writeAnnotationNodes(ctx, parseResults, scanCtx.annotationWhitelist, scanCtx.result); err != nil {
		return fmt.Errorf("indexer: write annotations: %w", err)
	}
	indexer.progress.EmitSub(PhaseWriting, SubAnnotationNodes, fmt.Sprintf("%d annotations", scanCtx.result.AnnotationCount))

	indexer.progress.EmitSub(PhaseWriting, SubRemoteCallEdges, "")
	if err := indexer.writeRemoteCallEdges(ctx, parseResults, symbolTable, scanCtx.result); err != nil {
		return fmt.Errorf("indexer: write remote calls: %w", err)
	}
	indexer.progress.EmitSub(PhaseWriting, SubRemoteCallEdges, fmt.Sprintf("%d calls", scanCtx.result.RelationsByKind["REMOTE_CALLS_ROUTE"]+scanCtx.result.RelationsByKind["REMOTE_CALLS_EXT"]))

	return nil
}

// resolveAndWriteRelations resolves cross-symbol relationships and writes them as graph edges.
//
// This function implements a multi-phase relationship resolution pipeline:
//
// Phase A — Import Resolution:
//   Matches raw import statements (e.g. "import com.example.UserService") against known
//   source file paths to create File→File IMPORTS edges. Uses path-based matching:
//   "com.example.UserService" → "src/main/java/com/example/UserService.java".
//
// Phase B — Type Inference + Call/Heritage/Override Resolution:
//   1. Local Type Inference (InferLocal):
//      For each file, builds a TypeEnv mapping variable names to type names using
//      constructor calls and type annotations within that file.
//      Example: "UserService svc = new UserService()" → svc:UserService
//
//   2. Multi-Return Value Inference (InferMultiReturn):
//      Resolves variables assigned from multi-return functions (e.g. Go's "store, err := New()").
//      Requires the complete SymbolTable to look up function return types.
//
//   3. Fixpoint Propagation (ResolveFixpoint):
//      Iteratively resolves copy/callResult/fieldAccess/methodCallResult assignment chains
//      until no new types can be inferred. Handles cases like "x = y" where y's type
//      was inferred in a previous step.
//
//   4. Call Resolution (ResolveCalls):
//      Matches raw calls (e.g. "svc.findById()") against the SymbolTable.
//      Resolution strategies (in priority order, each assigns a confidence score):
//        - type_exact (0.95): receiver type known from TypeEnv → exact match
//        - arg_count (0.85): unique function name + matching argument count
//        - same_file (0.85): unique function name within the same file
//        - name_unique (0.70): globally unique function name
//        - best_guess (0.25): multiple candidates, pick first (low confidence)
//
//   5. Heritage Resolution (ResolveHeritage):
//      Matches "class A extends B" / "class A implements I" against SymbolTable
//      to create EXTENDS and IMPLEMENTS edges.
//
//   6. Override Detection (DetectOverridesAndDispatches):
//      For each child class, finds methods that share the same name as parent class
//      methods, creating OVERRIDES edges.
//
// Phase C — Cross-File Type Propagation (conditional):
//   Triggered when too many calls are resolved as "best_guess" (low confidence),
//   indicating that local type inference was insufficient.
//
//   Trigger condition:
//     unresolvedRatio = count(best_guess calls) / count(total calls) > 3%
//     The 3% threshold (propagationThreshold) balances cost vs benefit:
//     - Below 3%: most calls resolved confidently, propagation overhead not justified
//     - Above 3%: significant unresolved calls, cross-file types likely help
//
//   Algorithm:
//     1. Build an import graph: file A imports file B → A depends on B's types
//     2. Propagate type information along import edges:
//        If B exports "class UserService", and A imports B,
//        then A's TypeEnv gains "UserService" as a known type
//     3. Re-resolve only the calls in files whose TypeEnv was updated
//     4. Merge improved results back, replacing best_guess with higher-confidence matches
//
//   This avoids the O(N²) cost of global type resolution by only propagating
//   along actual import dependencies, and only when the benefit is likely.
//
// Phase D — Write Results:
//   1. External Nodes: creates virtual Function nodes for external dependencies
//      (symbols with ID prefix "external:") that were referenced but not defined in source.
//   2. Relation Edges: writes all resolved CALLS + EXTENDS + IMPLEMENTS + OVERRIDES edges.
//   3. InferImplements: language helpers infer additional IMPLEMENTS edges
//      (e.g. Go struct satisfying an interface without explicit declaration).
//   4. Unresolved Hint Edges: writes UNRESOLVED_CALL edges for calls that could not be
//      confidently resolved, preserving candidate information for downstream analysis.
func (indexer *Indexer) resolveAndWriteRelations(ctx context.Context, scanCtx *scanContext, parseResults []model.ParseResult, symbolTable *resolver.SymbolTable) error {
	indexer.progress.Emit(PhaseResolving, 0, 0, "relations")

	// Build language helper for the project's primary language only
	langHelpers := buildLanguageHelpers(scanCtx.projectInfo.Language, symbolTable, scanCtx.projectInfo.Frameworks, scanCtx.absPath)
	resolverInstance := resolver.NewResolver(symbolTable, langHelpers)
	allImports, allCalls, allHeritage := collectRawRelations(parseResults)
	allFilePaths := collectSourceFilePaths(scanCtx.files)

	// Phase A: Import resolution
	importRelations, err := indexer.resolveImports(ctx, resolverInstance, allImports, allFilePaths, scanCtx.result)
	if err != nil {
		return err
	}

	// Phase B + C: Type inference, call/heritage/override resolution, cross-file propagation
	callRelations, heritageRelations, overrideRelations, callHints, err := indexer.resolveCallsAndHeritage(
		ctx, resolverInstance, parseResults, symbolTable, allCalls, allHeritage, importRelations)
	if err != nil {
		return err
	}

	// Phase D: Write all resolved data to graph
	return indexer.writeResolvedRelations(ctx, scanCtx, symbolTable, langHelpers,
		callRelations, heritageRelations, overrideRelations, callHints)
}

// resolveImports resolves raw import statements into File→File IMPORTS edges and writes them.
func (indexer *Indexer) resolveImports(ctx context.Context, resolverInstance *resolver.Resolver, allImports []model.RawImport, allFilePaths []string, result *model.IndexResult) ([]model.ResolvedRelation, error) {
	indexer.progress.EmitSub(PhaseResolving, SubImportEdges, "")
	importRelations := resolverInstance.ResolveImports(allImports, allFilePaths)
	if err := indexer.writeRelations(ctx, importRelations, result); err != nil {
		return nil, fmt.Errorf("indexer: write imports: %w", err)
	}
	indexer.progress.EmitSub(PhaseResolving, SubImportEdges, fmt.Sprintf("%d imports", len(importRelations)))
	return importRelations, nil
}

// resolveCallsAndHeritage performs type inference and resolves call/heritage/override relationships.
func (indexer *Indexer) resolveCallsAndHeritage(
	ctx context.Context,
	resolverInstance *resolver.Resolver,
	parseResults []model.ParseResult,
	symbolTable *resolver.SymbolTable,
	allCalls []model.RawCall,
	allHeritage []model.RawHeritage,
	importRelations []model.ResolvedRelation,
) ([]model.ResolvedRelation, []model.ResolvedRelation, []model.ResolvedRelation, []model.UnresolvedHint, error) {

	// Step 1: Local type inference — build per-file TypeEnv from constructor calls and type annotations.
	// Example: "UserService svc = new UserService()" → svc maps to type UserService.
	indexer.progress.EmitSub(PhaseResolving, SubInferLocal, "")
	typeInfer := typeinfer.New()
	envs := make(map[string]*model.TypeEnv)
	for _, parseResult := range parseResults {
		envs[parseResult.FilePath] = typeInfer.InferLocal(&parseResult)
	}
	indexer.progress.EmitSub(PhaseResolving, SubInferLocal, fmt.Sprintf("%d files", len(parseResults)))

	// Step 2: Multi-return value type inference — resolve variables from multi-return functions.
	// Example (Go): "store, err := kuzu.New()" → store maps to *kuzu.Store.
	indexer.progress.EmitSub(PhaseResolving, SubInferMultiReturn, "")
	for _, env := range envs {
		typeInfer.InferMultiReturn(env, symbolTable.FindByName)
	}
	indexer.progress.EmitSub(PhaseResolving, SubInferMultiReturn, fmt.Sprintf("%d files", len(envs)))

	// Step 3: Fixpoint propagation — iteratively resolve copy/callResult/fieldAccess/methodCallResult
	// chains until no new types can be inferred. Handles transitive assignments like "x = y".
	indexer.progress.EmitSub(PhaseResolving, SubResolveFixpoint, "")
	for _, parseResult := range parseResults {
		if len(parseResult.PendingAssignments) > 0 {
			typeInfer.ResolveFixpoint(envs[parseResult.FilePath], parseResult.PendingAssignments, symbolTable.FindByName, symbolTable.FindFieldByOwner)
		}
	}
	indexer.progress.EmitSub(PhaseResolving, SubResolveFixpoint, fmt.Sprintf("%d files", len(parseResults)))

	resolverInstance.SetHeritage(allHeritage)

	// Step 4: Call resolution — match raw calls against SymbolTable using TypeEnv for receiver types.
	// Produces CALLS edges with confidence scores (type_exact=0.95, arg_count=0.85, etc.).
	indexer.progress.EmitSub(PhaseResolving, SubResolveCalls, "")
	callRelations, callHints := resolverInstance.ResolveCalls(allCalls, envs)
	indexer.progress.EmitSub(PhaseResolving, SubResolveCalls, fmt.Sprintf("%d calls, %d hints", len(callRelations), len(callHints)))

	// Step 5: Heritage resolution — match "extends"/"implements" declarations against SymbolTable.
	// Produces EXTENDS and IMPLEMENTS edges.
	indexer.progress.EmitSub(PhaseResolving, SubResolveHeritage, "")
	heritageRelations := resolverInstance.ResolveHeritage(allHeritage)
	indexer.progress.EmitSub(PhaseResolving, SubResolveHeritage, fmt.Sprintf("%d heritage", len(heritageRelations)))

	// Step 6: Override and dispatch detection — find child methods overriding parent methods.
	// Produces OVERRIDES (child→parent) and DISPATCHES (parent→child) edges for polymorphic dispatch.
	indexer.progress.EmitSub(PhaseResolving, SubDetectOverridesAndDispatches, "")
	overrideRelations := resolverInstance.DetectOverridesAndDispatches(allHeritage)
	indexer.progress.EmitSub(PhaseResolving, SubDetectOverridesAndDispatches, fmt.Sprintf("%d overrides", len(overrideRelations)))

	// Dump debug data
	indexer.dump.OnRawCalls(allCalls)
	indexer.dump.OnResolved(callRelations, callHints)
	if _, ok := indexer.dump.(*FileDumpManager); ok {
		indexer.progress.EmitSub(PhaseResolving, SubDebugDump, fmt.Sprintf("%d calls, %d resolved", len(allCalls), len(callRelations)))
	}

	// Step 7: Cross-file type propagation (conditional) — triggered when >3% of calls are best_guess.
	// Propagates types along import edges, then re-resolves affected calls for higher confidence.
	unresolvedWithReceiver := 0
	for _, relation := range callRelations {
		if relation.ResolvedBy == "best_guess" {
			unresolvedWithReceiver++
		}
	}
	const propagationThreshold = 0.03
	if len(allCalls) > 0 {
		ratio := float64(unresolvedWithReceiver) / float64(len(allCalls))
		if ratio > propagationThreshold {
			indexer.progress.EmitSub(PhaseResolving, SubCrossFilePropagation, "")
			importGraph := buildImportGraphFromRelations(importRelations)
			updatedEnvs, affectedFiles := typeInfer.Propagate(parseResults, importGraph, envs)
			if len(affectedFiles) > 0 {
				affectedSet := make(map[string]bool, len(affectedFiles))
				for _, f := range affectedFiles {
					affectedSet[f] = true
				}
				var affectedCalls []model.RawCall
				for _, call := range allCalls {
					if affectedSet[call.FilePath] {
						affectedCalls = append(affectedCalls, call)
					}
				}
				if len(affectedCalls) > 0 {
					reResolved, reHints := resolverInstance.ResolveCalls(affectedCalls, updatedEnvs)
					callRelations = mergeCallRelations(callRelations, reResolved, affectedFiles)
					callHints = append(callHints, reHints...)
				}
			}
			indexer.progress.EmitSub(PhaseResolving, SubCrossFilePropagation, fmt.Sprintf("%d best_guess, %d affected files", unresolvedWithReceiver, len(affectedFiles)))
		}
	}

	return callRelations, heritageRelations, overrideRelations, callHints, nil
}

// writeResolvedRelations writes external nodes and all relation edges to the graph.
func (indexer *Indexer) writeResolvedRelations(
	ctx context.Context,
	scanCtx *scanContext,
	symbolTable *resolver.SymbolTable,
	langHelpers map[string]resolver.LanguageHelper,
	callRelations, heritageRelations, overrideRelations []model.ResolvedRelation,
	callHints []model.UnresolvedHint,
) error {
	// Step 1: Write external dependency virtual nodes — creates Function nodes for symbols
	// referenced but not defined in source (e.g. third-party library calls like "spring.JdbcTemplate.query").
	indexer.progress.EmitSub(PhaseResolving, SubExternalNodes, "")
	var externalNodes []model.Node
	for _, sym := range symbolTable.All() {
		if strings.HasPrefix(sym.ID, "external:") {
			externalNodes = append(externalNodes, model.Node{
				ID:   sym.ID,
				Kind: constants.KindFunction,
				Properties: map[string]interface{}{
					"name":           sym.Name,
					"qualified_name": sym.QualifiedName,
					"file_path":      "[external]",
				},
			})
		}
	}
	if len(externalNodes) > 0 {
		if err := indexer.graphStore.CreateNodes(ctx, externalNodes); err != nil {
			return fmt.Errorf("indexer: write external nodes: %w", err)
		}
	}
	indexer.progress.EmitSub(PhaseResolving, SubExternalNodes, fmt.Sprintf("%d nodes", len(externalNodes)))

	// Step 2: Write all resolved relation edges (CALLS + EXTENDS + IMPLEMENTS + OVERRIDES).
	// Also invokes InferImplements on language helpers for implicit interface implementations
	// (e.g. Go structs satisfying interfaces without explicit "implements" keyword).
	allRelations := append(callRelations, heritageRelations...)
	allRelations = append(allRelations, overrideRelations...)
	var implementsRelations []model.ResolvedRelation
	for _, helper := range langHelpers {
		implementsRelations = append(implementsRelations, helper.InferImplements()...)
	}
	allRelations = append(allRelations, implementsRelations...)
	indexer.dump.OnAllRelations(heritageRelations, overrideRelations, implementsRelations)
	indexer.progress.EmitSub(PhaseResolving, SubRelationEdges, "")
	if err := indexer.writeRelations(ctx, allRelations, scanCtx.result); err != nil {
		return fmt.Errorf("indexer: write relations: %w", err)
	}
	indexer.progress.EmitSub(PhaseResolving, SubRelationEdges, fmt.Sprintf("%d edges", len(allRelations)))

	// Step 3: Write UNRESOLVED_CALL hint edges — preserves candidate information for calls
	// that could not be confidently resolved, enabling downstream tools to show possible targets.
	if len(callHints) > 0 {
		indexer.progress.EmitSub(PhaseResolving, SubUnresolvedHints, "")
		var hintEdges []model.Edge
		for _, hint := range callHints {
			for _, candidateQN := range hint.Candidates {
				candidateSymbols := symbolTable.FindByQualifiedName(candidateQN)
				if len(candidateSymbols) == 0 {
					continue
				}
				hintEdges = append(hintEdges, model.Edge{
					SourceID:   hint.CallerID,
					TargetID:   candidateSymbols[0].ID,
					Kind:       model.RelUnresolvedCall,
					SourceKind: constants.KindFunction,
					Properties: map[string]any{
						"hint_type":       hint.HintType,
						"line":            hint.Line,
						"receiver_expr":   hint.ReceiverExpr,
						"candidate_count": hint.CandidateCount,
					},
				})
			}
		}
		if len(hintEdges) > 0 {
			if err := indexer.graphStore.CreateEdges(ctx, hintEdges); err != nil {
				return fmt.Errorf("indexer: write unresolved hints: %w", err)
			}
		}
		scanCtx.result.RelationsByKind["UNRESOLVED_CALL"] = len(hintEdges)
		indexer.progress.EmitSub(PhaseResolving, SubUnresolvedHints, fmt.Sprintf("%d hints → %d edges", len(callHints), len(hintEdges)))
	}

	// Count low-confidence calls for reporting
	for _, relation := range callRelations {
		if relation.Confidence < 0.5 {
			scanCtx.result.LowConfidenceCount++
		}
	}
	return nil
}

// AutoInit creates default config if not exists.
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
		results    []model.ParseResult
		errors     []model.IndexError
		mutex      sync.Mutex
		waitGroup  sync.WaitGroup
		fileChannel = make(chan scanner.ScannedFile, goroutineCount*2)
		processed  int
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

func (indexer *Indexer) writeSymbolNodes(ctx context.Context, parseResults []model.ParseResult, result *model.IndexResult) error {
	// Build field map: ownerQualifiedName → []FieldInfo
	fieldsByOwner := make(map[string][]model.FieldInfo)
	for _, parseResult := range parseResults {
		for _, fieldDeclaration := range parseResult.Fields {
			fieldsByOwner[fieldDeclaration.OwnerQualifiedName] = append(
				fieldsByOwner[fieldDeclaration.OwnerQualifiedName], fieldDeclaration.FieldInfo)
		}
	}

	var nodes []model.Node
	for _, parseResult := range parseResults {
		for _, symbol := range parseResult.Symbols {
			kind := constants.ParserKindToNodeKind(symbol.Kind)

			var props map[string]any
			switch kind {
			case constants.KindFunction:
				props = map[string]any{
					"name":           symbol.Name,
					"qualified_name": symbol.QualifiedName,
					"file_path":      symbol.FilePath,
					"start_line":     symbol.StartLine,
					"end_line":       symbol.EndLine,
					"params":         symbol.Params,
					"return_types":   symbol.ReturnTypes,
					"annotations":    symbol.Annotations,
					"is_exported":    symbol.IsExported,
					"is_abstract":    symbol.IsAbstract,
					"is_static":      symbol.IsStatic,
					"is_constructor": symbol.IsConstructor,
					"is_lambda":      symbol.IsLambda,
					"is_getter":      symbol.IsGetter,
					"is_setter":      symbol.IsSetter,
					"complexity":     symbol.Complexity,
					"class_type":     symbol.ClassType,
				}
			case constants.KindClass:
				fieldsJSON, _ := json.Marshal(fieldsByOwner[symbol.QualifiedName])
				props = map[string]any{
					"name":           symbol.Name,
					"qualified_name": symbol.QualifiedName,
					"file_path":      symbol.FilePath,
					"start_line":     symbol.StartLine,
					"end_line":       symbol.EndLine,
					"class_type":     symbol.ClassType,
					"is_abstract":    symbol.IsAbstract,
					"is_exported":    symbol.IsExported,
					"annotations":    symbol.Annotations,
					"complexity":     symbol.Complexity,
					"params":         symbol.Params,
					"fields":         string(fieldsJSON),
				}
			case constants.KindInterface:
				fieldsJSON, _ := json.Marshal(fieldsByOwner[symbol.QualifiedName])
				props = map[string]any{
					"name":           symbol.Name,
					"qualified_name": symbol.QualifiedName,
					"file_path":      symbol.FilePath,
					"start_line":     symbol.StartLine,
					"end_line":       symbol.EndLine,
					"is_exported":    symbol.IsExported,
					"class_type":     symbol.ClassType,
					"annotations":    symbol.Annotations,
					"fields":         string(fieldsJSON),
				}
			default:
				props = map[string]any{
					"name":           symbol.Name,
					"qualified_name": symbol.QualifiedName,
					"file_path":      symbol.FilePath,
					"start_line":     symbol.StartLine,
					"end_line":       symbol.EndLine,
				}
			}

			nodes = append(nodes, model.Node{
				ID:         symbol.ID,
				Kind:       kind,
				Properties: props,
			})
			result.SymbolsByKind[symbol.Kind]++
			// Supplement class_type breakdown (abstract_class, enum, struct)
			if symbol.ClassType != "" && symbol.ClassType != constants.ClassTypeClass && symbol.ClassType != constants.ClassTypeInterface {
				result.SymbolsByKind[symbol.ClassType]++
			}
			result.SymbolsCreated++
		}
	}

	if len(nodes) > 0 {
		return indexer.graphStore.CreateNodes(ctx, nodes)
	}
	return nil
}

func (indexer *Indexer) writeRelations(ctx context.Context, relations []model.ResolvedRelation, result *model.IndexResult) error {
	var edges []model.Edge
	for _, relation := range relations {
		edge := model.Edge{
			SourceID:   relation.SourceID,
			TargetID:   relation.TargetID,
			Kind:       relation.Kind,
			SourceKind: relation.SourceKind,
			Properties: map[string]any{},
		}
		if relation.Kind == model.RelImports {
			// IMPORTS has its own schema: symbol_name, alias
			if v, ok := relation.Metadata["symbol"]; ok {
				edge.Properties["symbol_name"] = v
			}
			if v, ok := relation.Metadata["module"]; ok {
				edge.Properties["alias"] = v
			}
		} else {
			if relation.Confidence > 0 {
				edge.Properties["confidence"] = relation.Confidence
			}
			if relation.ResolvedBy != "" {
				edge.Properties["resolved_by"] = relation.ResolvedBy
			}
			if relation.Candidates > 0 {
				edge.Properties["candidates"] = relation.Candidates
			}
			if relation.Line > 0 {
				edge.Properties["line"] = relation.Line
			}
			if relation.FlowContext != "" {
				edge.Properties["flow_context"] = relation.FlowContext
				edge.Properties["flow_line"] = relation.FlowLine
			}
			if v, ok := relation.Metadata["declared_type"]; ok && v != "" {
				edge.Properties["declared_type"] = v
			}
			if v, ok := relation.Metadata["polymorphic"]; ok && v == "true" {
				edge.Properties["polymorphic"] = true
			}
		}
		edges = append(edges, edge)
		result.RelationsByKind[string(relation.Kind)]++
		result.RelationsCreated++
	}

	if len(edges) > 0 {
		return indexer.graphStore.CreateEdges(ctx, edges)
	}
	return nil
}

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
			params, _ := node.Properties["params"].(string)
			symbolTable.Add(model.Symbol{
				ID:            node.ID,
				Name:          name,
				QualifiedName: qualifiedName,
				Kind:          kind,
				FilePath:      filePath,
				Params:        params,
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

// writeSymbolContainsEdges writes CONTAINS edges between File→Symbol and Class→Function.
// These edges must be written after symbol nodes exist in the graph.
func (indexer *Indexer) writeSymbolContainsEdges(ctx context.Context, symbolEdges []model.Edge) error {
	indexer.progress.EmitSub(PhaseWriting, SubContainsEdges, "")
	if len(symbolEdges) > 0 {
		if err := indexer.graphStore.CreateEdges(ctx, symbolEdges); err != nil {
			return fmt.Errorf("indexer: write symbol contains edges: %w", err)
		}
	}
	indexer.progress.EmitSub(PhaseWriting, SubContainsEdges, fmt.Sprintf("%d edges", len(symbolEdges)))
	return nil
}

// writeFileSystemNodes creates Repository, Directory, and File nodes along with their
// CONTAINS edges (repo→dir→file hierarchy). Returns symbolEdges (File→Symbol, Class→Function)
// that must be written later after symbol nodes exist.
func (indexer *Indexer) writeFileSystemNodes(ctx context.Context, absPath string, frameworks []string, files []scanner.ScannedFile, parseResults []model.ParseResult, result *model.IndexResult) ([]model.Edge, error) {
	repoName := filepath.Base(absPath)
	repoID := fmt.Sprintf("repo:%s", repoName)

	nodes, edges, symbolEdges := buildStructuralData(repoID, repoName, absPath, frameworks, files, parseResults, buildClassMap(parseResults))

	if len(nodes) > 0 {
		if err := indexer.graphStore.CreateNodes(ctx, nodes); err != nil {
			return nil, err
		}
	}
	if len(edges) > 0 {
		if err := indexer.graphStore.CreateEdges(ctx, edges); err != nil {
			return nil, err
		}
	}
	return symbolEdges, nil
}

// writeIncrementalFileSystemNodes writes file system nodes for changed files only.
// Repository/Directory use MERGE (already exist), File uses CREATE (deleted earlier in incremental flow).
// Returns symbolEdges that must be written later after symbol nodes exist.
func (indexer *Indexer) writeIncrementalFileSystemNodes(ctx context.Context, absPath string, frameworks []string, files []scanner.ScannedFile, parseResults []model.ParseResult, result *model.IndexResult) ([]model.Edge, error) {
	repoName := filepath.Base(absPath)
	repoID := fmt.Sprintf("repo:%s", repoName)

	// Incremental: build class map from parseResults, then supplement with existing classes from graph
	classIDByQualifiedName := buildClassMap(parseResults)
	existingClasses, _ := indexer.graphStore.QueryAllByKind(ctx, constants.KindClass, 0)
	for _, classNode := range existingClasses {
		qualifiedName, _ := classNode.Properties["qualified_name"].(string)
		if qualifiedName != "" {
			if _, exists := classIDByQualifiedName[qualifiedName]; !exists {
				classIDByQualifiedName[qualifiedName] = classNode.ID
			}
		}
	}

	nodes, edges, symbolEdges := buildStructuralData(repoID, repoName, absPath, frameworks, files, parseResults, classIDByQualifiedName)

	var mergeNodes, createNodes []model.Node
	for _, n := range nodes {
		if n.Kind == constants.KindRepository || n.Kind == constants.KindDirectory {
			mergeNodes = append(mergeNodes, n)
		} else {
			createNodes = append(createNodes, n)
		}
	}
	if len(mergeNodes) > 0 {
		if err := indexer.graphStore.WriteNodes(ctx, mergeNodes); err != nil {
			return nil, err
		}
	}
	if len(createNodes) > 0 {
		if err := indexer.graphStore.CreateNodes(ctx, createNodes); err != nil {
			return nil, err
		}
	}
	if len(edges) > 0 {
		if err := indexer.graphStore.CreateEdges(ctx, edges); err != nil {
			return nil, err
		}
	}
	return symbolEdges, nil
}

func buildStructuralData(repoID, repoName, absPath string, frameworks []string, files []scanner.ScannedFile, parseResults []model.ParseResult, classIDByQualifiedName map[string]string) ([]model.Node, []model.Edge, []model.Edge) {
	var nodes []model.Node
	var edges []model.Edge

	// Repository node
	nodes = append(nodes, model.Node{
		ID: repoID, Kind: constants.KindRepository,
		Properties: map[string]any{"name": repoName, "path": absPath, "index_timestamp": time.Now().Unix(), "frameworks": strings.Join(frameworks, ",")},
	})

	// File nodes + Directory nodes + CONTAINS edges (source files only)
	dirs := make(map[string]bool)
	dirFiles := make(map[string][]string)
	for _, file := range files {
		if file.Category != constants.FileSource {
			continue
		}
		fileID := fmt.Sprintf("file:%s", file.RelPath)
		nodes = append(nodes, model.Node{
			ID: fileID, Kind: constants.KindFile,
			Properties: map[string]any{"path": file.RelPath, "language": file.Language},
		})
		edges = append(edges, model.Edge{
			SourceID: repoID, TargetID: fileID,
			Kind: model.RelContains, SourceKind: constants.KindRepository,
		})
		dir := filepath.Dir(file.RelPath)
		if dir != "." && dir != "" {
			dirs[dir] = true
			dirFiles[dir] = append(dirFiles[dir], fileID)
		}
	}
	for dir := range dirs {
		dirID := fmt.Sprintf("dir:%s", dir)
		nodes = append(nodes, model.Node{
			ID: dirID, Kind: constants.KindDirectory,
			Properties: map[string]any{"path": dir},
		})
		for _, fileID := range dirFiles[dir] {
			edges = append(edges, model.Edge{
				SourceID: dirID, TargetID: fileID,
				Kind: model.RelContains, SourceKind: constants.KindDirectory,
			})
		}
	}

	// File → Symbol CONTAINS edges (returned separately, must be written after symbol nodes exist)
	var symbolEdges []model.Edge
	for _, parseResult := range parseResults {
		fileID := fmt.Sprintf("file:%s", parseResult.FilePath)
		for _, symbol := range parseResult.Symbols {
			sourceKind := constants.SourceKindFile
			switch symbol.Kind {
			case constants.KindClass:
				sourceKind = constants.SourceKindFileClass
			case constants.KindInterface:
				sourceKind = constants.SourceKindFileInterface
			}
			symbolEdges = append(symbolEdges, model.Edge{
				SourceID: fileID, TargetID: symbol.ID,
				Kind: model.RelContains, SourceKind: sourceKind,
			})
		}
	}

	// Class → Function CONTAINS edges
	for _, parseResult := range parseResults {
		for _, symbol := range parseResult.Symbols {
			if symbol.Kind != constants.KindFunction {
				continue
			}
			lastDot := strings.LastIndex(symbol.QualifiedName, ".")
			if lastDot <= 0 {
				continue
			}
			classQualifiedName := symbol.QualifiedName[:lastDot]
			if classID, exists := classIDByQualifiedName[classQualifiedName]; exists {
				symbolEdges = append(symbolEdges, model.Edge{
					SourceID:   classID,
					TargetID:   symbol.ID,
					Kind:       model.RelContains,
					SourceKind: constants.SourceKindClassFunc,
				})
			}
		}
	}

	return nodes, edges, symbolEdges
}

// buildClassMap collects qualifiedName → ID mapping for all Class nodes from parseResults.
func buildClassMap(parseResults []model.ParseResult) map[string]string {
	classIDByQualifiedName := make(map[string]string)
	for _, parseResult := range parseResults {
		for _, symbol := range parseResult.Symbols {
			switch symbol.Kind {
			case constants.KindClass, constants.KindInterface:
				classIDByQualifiedName[symbol.QualifiedName] = symbol.ID
			}
		}
	}
	return classIDByQualifiedName
}

func (indexer *Indexer) writeRouteNodes(ctx context.Context, parseResults []model.ParseResult, symbolTable *resolver.SymbolTable, result *model.IndexResult) error {
	var nodes []model.Node
	var edges []model.Edge
	for _, parseResult := range parseResults {
		for _, route := range parseResult.Routes {
			routeID := fmt.Sprintf("route:%s:%s:%s", route.Method, route.PathPattern, route.FilePath)
			nodes = append(nodes, model.Node{
				ID:   routeID,
				Kind: constants.KindRoute,
				Properties: map[string]any{
					"method":         route.Method,
					"path_pattern":   route.PathPattern,
					"handler_method": route.HandlerName,
					"framework":      route.Framework,
					"file_path":      route.FilePath,
				},
			})
			result.SymbolsByKind["route"]++
			result.SymbolsCreated++

			// Resolve handler function and create HANDLES edge
			handlerID := resolveHandlerFunction(symbolTable, route.HandlerName, route.FilePath)
			if handlerID != "" {
				edges = append(edges, model.Edge{
					SourceID:   handlerID,
					TargetID:   routeID,
					Kind:       model.RelHandles,
					SourceKind: constants.KindFunction,
				})
				result.RelationsByKind["HANDLES"]++
				result.RelationsCreated++
			}
		}
	}
	if len(nodes) > 0 {
		indexer.dump.OnRoutes(nodes)
		if err := indexer.graphStore.CreateNodes(ctx, nodes); err != nil {
			return err
		}
	}
	if len(edges) > 0 {
		if err := indexer.graphStore.CreateEdges(ctx, edges); err != nil {
			return err
		}
	}

	// Propagate FeignClient routes to implementing @RestController classes
	var feignEdges []model.Edge
	propagateFeignRoutes(parseResults, symbolTable, nodes, &feignEdges, result)
	if len(feignEdges) > 0 {
		return indexer.graphStore.CreateEdges(ctx, feignEdges)
	}
	return nil
}

// propagateFeignRoutes creates HANDLES edges from implementing class methods to FeignClient routes.
func propagateFeignRoutes(parseResults []model.ParseResult, symbolTable *resolver.SymbolTable, routeNodes []model.Node, edges *[]model.Edge, result *model.IndexResult) {
	feignRoutes := make(map[string]string) // "OrderClient.getOrder" → routeID
	for _, rn := range routeNodes {
		fw, _ := rn.Properties["framework"].(string)
		handler, _ := rn.Properties["handler_method"].(string)
		if fw == "feign" && handler != "" {
			feignRoutes[handler] = rn.ID
		}
	}
	if len(feignRoutes) == 0 {
		return
	}
	for _, pr := range parseResults {
		for _, h := range pr.Heritage {
			if h.Kind != "implements" {
				continue
			}
			var matched []struct{ method, routeID string }
			for handler, routeID := range feignRoutes {
				parts := strings.SplitN(handler, ".", 2)
				if len(parts) == 2 && parts[0] == h.ParentName {
					matched = append(matched, struct{ method, routeID string }{parts[1], routeID})
				}
			}
			if len(matched) == 0 {
				continue
			}
			hasRestController := false
			for _, sym := range pr.Symbols {
				if sym.Name == h.ChildName && strings.Contains(sym.Annotations, "RestController") {
					hasRestController = true
					break
				}
			}
			if !hasRestController {
				continue
			}
			for _, mr := range matched {
				handlerID := resolveHandlerFunction(symbolTable, h.ChildName+"."+mr.method, pr.FilePath)
				if handlerID != "" {
					*edges = append(*edges, model.Edge{
						SourceID: handlerID, TargetID: mr.routeID, Kind: model.RelHandles, SourceKind: constants.KindFunction,
					})
					result.RelationsByKind["HANDLES"]++
					result.RelationsCreated++
				}
			}
		}
	}
}
// Handler names come in various formats: "ClassName.method", "method", "pkg.ClassName.method".
// Tries qualified name, then class.method suffix, then simple method name within same file.
func resolveHandlerFunction(symbolTable *resolver.SymbolTable, handlerName, filePath string) string {
	// Try exact qualified name
	if symbols := symbolTable.FindByQualifiedName(handlerName); len(symbols) > 0 {
		return symbols[0].ID
	}

	// Extract method name (last part after dot)
	methodName := handlerName
	if idx := strings.LastIndex(handlerName, "."); idx >= 0 {
		methodName = handlerName[idx+1:]
	}

	// Try method name, prefer same file
	candidates := symbolTable.FindByName(methodName)
	for _, candidate := range candidates {
		if candidate.FilePath == filePath {
			return candidate.ID
		}
	}

	// Try suffix match on qualified name (e.g., "UserController.getById" matches "com.example.UserController.getById")
	for _, candidate := range candidates {
		if strings.HasSuffix(candidate.QualifiedName, handlerName) {
			return candidate.ID
		}
	}

	if len(candidates) > 0 {
		return candidates[0].ID
	}
	return ""
}

func (indexer *Indexer) writeQueryNodes(ctx context.Context, parseResults []model.ParseResult, symbolTable *resolver.SymbolTable, result *model.IndexResult) error {
	var nodes []model.Node
	var edges []model.Edge
	for _, parseResult := range parseResults {
		for _, query := range parseResult.Queries {
			queryID := fmt.Sprintf("query:%s:%d", query.CallerName, query.Line)
			nodes = append(nodes, model.Node{
				ID:   queryID,
				Kind: constants.KindQueryNode,
				Properties: map[string]any{
					"sql_text":   query.SQLText,
					"query_type": query.QueryType,
					"tables":     strings.Join(query.Tables, ","),
					"file_path":  query.FilePath,
					"caller":     query.CallerName,
				},
			})
			result.SymbolsByKind["query"]++

			// Resolve caller function and create EXECUTES edge
			callerID := resolveHandlerFunction(symbolTable, query.CallerName, query.FilePath)
			if callerID != "" {
				edges = append(edges, model.Edge{
					SourceID:   callerID,
					TargetID:   queryID,
					Kind:       model.RelExecutes,
					SourceKind: constants.KindFunction,
				})
				result.RelationsByKind["EXECUTES"]++
				result.RelationsCreated++
			}
		}
	}
	if len(nodes) > 0 {
		if err := indexer.graphStore.CreateNodes(ctx, nodes); err != nil {
			return err
		}
	}
	if len(edges) > 0 {
		return indexer.graphStore.CreateEdges(ctx, edges)
	}
	return nil
}

// Helpers

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

func (indexer *Indexer) writeAnnotationNodes(ctx context.Context, parseResults []model.ParseResult, whitelist map[string]annotation.AnnotationDef, result *model.IndexResult) error {
	if len(whitelist) == 0 {
		return nil
	}
	var nodes []model.Node
	var edges []model.Edge

	for _, pr := range parseResults {
		for _, symbol := range pr.Symbols {
			if symbol.Annotations == "" || symbol.Annotations == "[]" || symbol.Annotations == "null" {
				continue
			}
			var annList []model.StructuredAnnotation
			if err := json.Unmarshal([]byte(symbol.Annotations), &annList); err != nil {
				continue
			}
			for _, structuredAnnotation := range annList {
				def, ok := whitelist[structuredAnnotation.Name]
				if !ok {
					continue
				}
				annID := symbol.ID + "::" + structuredAnnotation.Name
				// Build params string from structured params
				paramsStr := ""
				for paramKey, paramValue := range structuredAnnotation.Params {
					if paramsStr != "" {
						paramsStr += ", "
					}
					paramsStr += paramKey + "=" + paramValue
				}
				nodes = append(nodes, model.Node{
					ID:   annID,
					Kind: constants.KindAnnotation,
					Properties: map[string]any{
						"name":      structuredAnnotation.Name,
						"category":  def.Category,
						"layer":     def.Layer,
						"framework": def.Framework,
						"params":    paramsStr,
						"file_path": symbol.FilePath,
						"line":      symbol.StartLine,
					},
				})
				sourceKind := constants.KindFunction
				switch symbol.Kind {
				case constants.KindClass:
					sourceKind = constants.KindClass
				case constants.KindInterface:
					sourceKind = constants.KindInterface
				}
				edges = append(edges, model.Edge{
					SourceID:   symbol.ID,
					TargetID:   annID,
					Kind:       model.RelHasAnnotation,
					SourceKind: sourceKind,
				})
			}
		}
	}

	if len(nodes) > 0 {
		indexer.dump.OnAnnotations(nodes, edges)
		if err := indexer.graphStore.CreateNodes(ctx, nodes); err != nil {
			return err
		}
		if err := indexer.graphStore.CreateEdges(ctx, edges); err != nil {
			return err
		}
		result.AnnotationCount = len(nodes)
	}
	return nil
}

func (indexer *Indexer) writeRemoteCallEdges(ctx context.Context, parseResults []model.ParseResult, symbolTable *resolver.SymbolTable, result *model.IndexResult) error {
	// Collect all Route nodes for matching
	allRoutes, _ := indexer.graphStore.QueryAllByKind(ctx, constants.KindRoute, 100000)

	var nodes []model.Node
	var edges []model.Edge
	seenExtIDs := make(map[string]bool)

	for _, pr := range parseResults {
		for _, rc := range pr.RemoteCalls {
			callerID := resolveHandlerFunction(symbolTable, rc.CallerName, rc.FilePath)
			if callerID == "" {
				continue
			}

			// Try matching to a known Route
			matched := resolver.FindMatchingRoutes(rc.TargetURL, rc.Method, allRoutes)
			if len(matched) > 0 {
				edges = append(edges, model.Edge{
					SourceID: callerID, TargetID: matched[0],
					Kind: model.RelRemoteCallsRoute, SourceKind: constants.KindFunction,
					Properties: map[string]any{
						"target_url":     rc.TargetURL,
						"target_service": rc.TargetService,
						"protocol":       rc.Protocol,
						"confidence":     rc.ServiceConfidence,
					},
				})
				result.RelationsByKind["REMOTE_CALLS_ROUTE"]++
				result.RelationsCreated++
			} else if rc.TargetService != "" {
				// No Route match → create ExternalService + REMOTE_CALLS_EXT
				extID := "ext:" + rc.TargetService
				if !seenExtIDs[extID] {
					seenExtIDs[extID] = true
					nodes = append(nodes, model.Node{
						ID: extID, Kind: constants.KindExternalService,
						Properties: map[string]any{
							"name":          rc.TargetService,
							"discovered_by": rc.CallerName,
							"file_path":     rc.FilePath,
						},
					})
				}
				edges = append(edges, model.Edge{
					SourceID: callerID, TargetID: extID,
					Kind: model.RelRemoteCallsExt, SourceKind: constants.KindFunction,
					Properties: map[string]any{
						"target_url":     rc.TargetURL,
						"target_service": rc.TargetService,
						"protocol":       rc.Protocol,
						"confidence":     rc.ServiceConfidence,
					},
				})
				result.RelationsByKind["REMOTE_CALLS_EXT"]++
				result.RelationsCreated++
			}
		}
	}

	if len(nodes) > 0 {
		if err := indexer.graphStore.CreateNodes(ctx, nodes); err != nil {
			return err
		}
	}
	if len(edges) > 0 {
		return indexer.graphStore.CreateEdges(ctx, edges)
	}
	return nil
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

// buildImportGraphFromRelations builds a file→[]file import graph from resolved IMPORTS edges.
// Uses resolved file paths (not raw ModulePath) so Propagate can match ParseResult.FilePath.
func buildImportGraphFromRelations(relations []model.ResolvedRelation) map[string][]string {
	graph := make(map[string][]string)
	for _, rel := range relations {
		if rel.Kind != model.RelImports {
			continue
		}
		source := strings.TrimPrefix(rel.SourceID, "file:")
		target := strings.TrimPrefix(rel.TargetID, "file:")
		graph[source] = append(graph[source], target)
	}
	return graph
}

func mergeCallRelations(original, reResolved []model.ResolvedRelation, affectedFiles []string) []model.ResolvedRelation {
	affected := make(map[string]bool)
	for _, file := range affectedFiles {
		affected[file] = true
	}

	// Keep relations from non-affected files, replace affected files' relations
	var merged []model.ResolvedRelation
	for _, relation := range original {
		filePath := ""
		if relation.Metadata != nil {
			filePath = relation.Metadata["file_path"]
		}
		if !affected[filePath] {
			merged = append(merged, relation)
		}
	}
	merged = append(merged, reResolved...)
	return merged
}

func capitalizeFirst(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// determinePrimaryLanguage returns the language with the most source files.
// Returns empty string if no source files exist.
func determinePrimaryLanguage(filesByLanguage map[string]int) string {
	maxCount := 0
	primaryLanguage := ""
	for lang, count := range filesByLanguage {
		if count > maxCount {
			maxCount = count
			primaryLanguage = lang
		}
	}
	return primaryLanguage
}

// isSameLanguageFamily returns true if two languages share the same parser/helper.
// TypeScript and JavaScript are treated as the same family.
func isSameLanguageFamily(lang1, lang2 string) bool {
	tsFamily := map[string]bool{"typescript": true, "javascript": true}
	return tsFamily[lang1] && tsFamily[lang2]
}
// buildLanguageHelpers creates resolver helpers only for the project's primary language.
// TS and JS share the same helper, so both are registered when either is the primary language.
func buildLanguageHelpers(language string, symbolTable *resolver.SymbolTable, frameworks []string, projectRoot string) map[string]resolver.LanguageHelper {
	helpers := make(map[string]resolver.LanguageHelper)
	switch language {
	case constants.LangJava:
		helpers[constants.LangJava] = resolverjava.NewHelper(symbolTable, resolverjava.NewExternalMethodManager(frameworks, projectRoot))
	case constants.LangGo:
		helpers[constants.LangGo] = resolvergo.NewHelper(symbolTable)
	case constants.LangPython:
		helpers[constants.LangPython] = resolverpy.NewHelper()
	case constants.LangTypeScript, constants.LangJavaScript:
		helpers[constants.LangTypeScript] = resolverts.NewHelper()
		helpers[constants.LangJavaScript] = resolverts.NewHelper()
	}
	return helpers
}

// unresolvedPlaceholder records a service name placeholder that could not be resolved from config.
type unresolvedPlaceholder struct {
	Key        string
	CallerName string
	FilePath   string
}

// resolveServiceNamePlaceholders replaces ${...} placeholders in RawRemoteCall.TargetService
// with human-readable service names from config. Does NOT affect route matching or call chain building.
func (indexer *Indexer) resolveServiceNamePlaceholders(parseResults []model.ParseResult) []unresolvedPlaceholder {
	properties := indexer.config.Dependencies.Properties
	if len(properties) == 0 {
		return nil
	}

	var unresolved []unresolvedPlaceholder
	for i := range parseResults {
		for j := range parseResults[i].RemoteCalls {
			remoteCall := &parseResults[i].RemoteCalls[j]
			if !strings.HasPrefix(remoteCall.TargetService, "${") {
				continue
			}
			key := strings.TrimPrefix(strings.TrimSuffix(remoteCall.TargetService, "}"), "${")
			if resolved, ok := properties[key]; ok {
				remoteCall.TargetService = resolved
				remoteCall.ServiceResolvedBy = "config_mapping"
			} else {
				unresolved = append(unresolved, unresolvedPlaceholder{
					Key:        key,
					CallerName: remoteCall.CallerName,
					FilePath:   remoteCall.FilePath,
				})
			}
		}
	}
	return unresolved
}

// matchConsumerToProvider connects RPC consumer methods to provider handler Functions
// by matching HTTP method+path against Route nodes (same-repo) or CrossProjectIndex (cross-repo).
// Creates Function→Function CALLS edges so TraverseCallChain can traverse cross-service calls.
func (indexer *Indexer) matchConsumerToProvider(ctx context.Context, scanCtx *scanContext,
	remoteCalls []model.RawRemoteCall, symbolTable *resolver.SymbolTable,
	allRoutes []model.Node, routeToHandler map[string]string) error {

	indexer.progress.EmitSub(PhaseWriting, SubMatchConsumerToProvider, "")

	if len(allRoutes) == 0 && indexer.crossIndex == nil {
		return nil
	}

	// Pre-bucket routes by HTTP method to reduce per-RemoteCall scan range.
	// Exclude consumer routes (e.g. feign) — only match against provider routes.
	routesByMethod := make(map[string][]model.Node)
	for _, route := range allRoutes {
		framework, _ := route.Properties["framework"].(string)
		if crossindex.DetermineRouteRole(framework) == crossindex.RoleConsumer {
			continue
		}
		method, _ := route.Properties["method"].(string)
		upperMethod := strings.ToUpper(method)
		routesByMethod[upperMethod] = append(routesByMethod[upperMethod], route)
	}

	dependencies := toCrossIndexDeps(indexer.config.Dependencies.Projects)

	var newNodes []model.Node
	var newEdges []model.Edge
	seenPlaceholders := make(map[string]bool)

	for _, remoteCall := range remoteCalls {
		if remoteCall.TargetURL == "" {
			continue
		}
		callerID := resolveHandlerFunction(symbolTable, remoteCall.CallerName, remoteCall.FilePath)
		if callerID == "" {
			continue
		}

		// 1. Match against local Route nodes (use method bucket to narrow scan)
		candidateRoutes := routesByMethod[strings.ToUpper(remoteCall.Method)]
		matched := resolver.FindMatchingRoutes(remoteCall.TargetURL, remoteCall.Method, candidateRoutes)
		if len(matched) > 0 {
			handlerID := routeToHandler[matched[0]]
			if handlerID != "" && handlerID != callerID {
				newEdges = append(newEdges, model.Edge{
					SourceID:   callerID,
					TargetID:   handlerID,
					Kind:       model.RelCalls,
					SourceKind: constants.KindFunction,
					Properties: map[string]any{
						"via_route":          remoteCall.Method + " " + remoteCall.TargetURL,
						"cross_service":      false,
						"consumer_interface": remoteCall.CallerName,
						"target_service":     remoteCall.TargetService,
						"confidence":         0.9,
					},
				})
				scanCtx.result.RelationsByKind["CALLS_VIA_ROUTE"]++
			}
			continue
		}

		// 2. Match against CrossProjectIndex (only when dependencies configured)
		if len(dependencies) > 0 && indexer.crossIndex != nil {
			routeMatches := indexer.crossIndex.MatchRoute(ctx, remoteCall.Method, remoteCall.TargetURL, dependencies)
			if len(routeMatches) > 0 {
				match := routeMatches[0]
				placeholderID := match.Route.HandlerID
				if !seenPlaceholders[placeholderID] {
					seenPlaceholders[placeholderID] = true
					newNodes = append(newNodes, model.Node{
						ID:   placeholderID,
						Kind: constants.KindFunction,
						Properties: map[string]any{
							"name":           match.Route.HandlerName,
							"file_path":      "[cross-service]",
							"qualified_name": match.Route.HandlerName,
							"cross_service":  true,
							"target_project": match.ProjectPath,
							"target_branch":  match.Branch,
						},
					})
				}
				newEdges = append(newEdges, model.Edge{
					SourceID:   callerID,
					TargetID:   placeholderID,
					Kind:       model.RelCalls,
					SourceKind: constants.KindFunction,
					Properties: map[string]any{
						"via_route":          remoteCall.Method + " " + remoteCall.TargetURL,
						"cross_service":      true,
						"consumer_interface": remoteCall.CallerName,
						"target_service":     remoteCall.TargetService,
						"target_project":     match.ProjectPath,
						"target_branch":      match.Branch,
						"target_handler":     match.Route.HandlerName,
						"confidence":         0.85,
					},
				})
				scanCtx.result.RelationsByKind["CALLS_CROSS_SERVICE"]++
			}
		}
		// 3. No match → REMOTE_CALLS_EXT already created by writeRemoteCallEdges (Step 6)
	}

	if len(newNodes) > 0 {
		if err := indexer.graphStore.CreateNodes(ctx, newNodes); err != nil {
			return fmt.Errorf("create cross-service placeholder nodes: %w", err)
		}
	}
	if len(newEdges) > 0 {
		if err := indexer.graphStore.CreateEdges(ctx, newEdges); err != nil {
			return fmt.Errorf("create cross-service CALLS edges: %w", err)
		}
	}

	indexer.progress.EmitSub(PhaseWriting, SubMatchConsumerToProvider,
		fmt.Sprintf("%d via_route, %d cross_service",
			scanCtx.result.RelationsByKind["CALLS_VIA_ROUTE"],
			scanCtx.result.RelationsByKind["CALLS_CROSS_SERVICE"]))
	return nil
}

// toCrossIndexDeps converts config dependencies to crossindex.Dependency slice.
func toCrossIndexDeps(projects []config.DependencyProject) []crossindex.Dependency {
	dependencies := make([]crossindex.Dependency, len(projects))
	for i, project := range projects {
		dependencies[i] = crossindex.Dependency{Path: project.Path, Branch: project.Branch}
	}
	return dependencies
}

// writeCrossProjectIndex collects exported symbols and routes from the current project
// and registers them in the CrossProjectIndex for cross-service discovery.
func (indexer *Indexer) writeCrossProjectIndex(ctx context.Context, scanCtx *scanContext,
	symbolTable *resolver.SymbolTable,
	allRoutes []model.Node, routeToHandler map[string]string) error {

	if indexer.crossIndex == nil {
		return nil
	}

	indexer.progress.EmitSub(PhaseWriting, SubWriteCrossProjectIndex, "")

	var symbols []crossindex.GlobalSymbol
	var routes []crossindex.GlobalRoute

	// Collect exported classes/interfaces from symbolTable (skip functions, test files, unexported)
	for _, symbol := range symbolTable.All() {
		if !symbol.IsExported {
			continue
		}
		if isTestFilePath(symbol.FilePath) {
			continue
		}
		if symbol.Kind == constants.KindFunction {
			continue
		}

		globalSymbol := crossindex.GlobalSymbol{
			QualifiedName: symbol.QualifiedName,
			Name:          symbol.Name,
			Kind:          symbol.Kind,
			ClassType:     symbol.ClassType,
			NodeID:        symbol.ID,
			Annotations:   parseAnnotationNames(symbol.Annotations),
			FilePath:      symbol.FilePath,
		}

		// Collect methods of this class/interface
		methods := symbolTable.FindMethodsByQualifiedName(symbol.QualifiedName)
		for _, method := range methods {
			globalMethod := crossindex.GlobalMethod{
				Name:        method.Name,
				NodeID:      method.ID,
				Params:      parseParamTypes(method.Params),
				ReturnType:  firstReturnType(method.ReturnTypes),
				Annotations: parseAnnotationNames(method.Annotations),
			}
			globalMethod.RouteMethod, globalMethod.RoutePath = extractRouteFromAnnotations(method.Annotations)
			globalSymbol.Methods = append(globalSymbol.Methods, globalMethod)
		}

		symbols = append(symbols, globalSymbol)
	}

	// Collect routes with role (provider/consumer) using preloaded routeToHandler map
	for _, route := range allRoutes {
		framework, _ := route.Properties["framework"].(string)
		handlerMethod, _ := route.Properties["handler_method"].(string)
		routes = append(routes, crossindex.GlobalRoute{
			Method:      fmt.Sprint(route.Properties["method"]),
			Path:        fmt.Sprint(route.Properties["path_pattern"]),
			HandlerName: handlerMethod,
			HandlerID:   routeToHandler[route.ID],
			Framework:   framework,
			Role:        crossindex.DetermineRouteRole(framework),
		})
	}

	entry := crossindex.ProjectEntry{
		ProjectPath: scanCtx.absPath,
		Branch:      scanCtx.branch,
		Symbols:     symbols,
		Routes:      routes,
		UpdatedAt:   time.Now().Unix(),
	}
	if err := indexer.crossIndex.RegisterProject(ctx, entry); err != nil {
		return err
	}
	indexer.progress.EmitSub(PhaseWriting, SubWriteCrossProjectIndex,
		fmt.Sprintf("%d symbols, %d routes", len(symbols), len(routes)))
	return nil
}

// isTestFilePath checks if a file path looks like a test file.
func isTestFilePath(filePath string) bool {
	lower := strings.ToLower(filePath)
	return strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/") ||
		strings.Contains(lower, "_test.") || strings.Contains(lower, ".test.")
}

// parseAnnotationNames extracts annotation names from JSON structured annotation array.
func parseAnnotationNames(annotationsJSON string) []string {
	if annotationsJSON == "" || annotationsJSON == "null" {
		return nil
	}
	var annotations []model.StructuredAnnotation
	if err := json.Unmarshal([]byte(annotationsJSON), &annotations); err != nil {
		return nil
	}
	names := make([]string, 0, len(annotations))
	for _, annotation := range annotations {
		names = append(names, annotation.Name)
	}
	return names
}

// parseParamTypes extracts parameter type names from JSON params string.
func parseParamTypes(paramsJSON string) []string {
	if paramsJSON == "" {
		return nil
	}
	var params []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
		return nil
	}
	types := make([]string, 0, len(params))
	for _, param := range params {
		types = append(types, param.Type)
	}
	return types
}

// firstReturnType returns the first return type or empty string.
func firstReturnType(returnTypes []string) string {
	if len(returnTypes) > 0 {
		return returnTypes[0]
	}
	return ""
}

// extractRouteFromAnnotations parses route method and path from structured annotation JSON.
func extractRouteFromAnnotations(annotationsJSON string) (string, string) {
	if annotationsJSON == "" || annotationsJSON == "null" {
		return "", ""
	}
	var annotations []model.StructuredAnnotation
	if err := json.Unmarshal([]byte(annotationsJSON), &annotations); err != nil {
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
