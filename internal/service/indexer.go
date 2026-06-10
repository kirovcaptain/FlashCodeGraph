// Package service implements the service layer orchestrators.
package service

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/kirovcaptain/FlashCodeGraph/internal/config"
	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/annotation"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/defparser"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/framework"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
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

	if limit, err := config.ParseMemoryLimit(indexer.config.System.MemoryLimit); err == nil && limit != math.MaxInt64 {
		debug.SetMemoryLimit(limit)
		defer debug.SetMemoryLimit(math.MaxInt64)
	}

	gcPercent := indexer.config.System.GCPercent
	if gcPercent == 0 {
		gcPercent = 300
	}
	previousGCPercent := debug.SetGCPercent(gcPercent)
	defer debug.SetGCPercent(previousGCPercent)

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
	// ClearAll may fail on empty/nonexistent graphs (e.g., first index) — safe to ignore.
	_ = indexer.graphStore.ClearAll(ctx)
	// Recreate indexes after graph deletion
	if err := indexer.graphStore.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("indexer: migrate schema: %w", err)
	}
	os.RemoveAll(filepath.Join(scanCtx.absPath, ".fcg", "cache"))
	indexer.progress.EmitSub(PhaseWriting, SubCleanGraph, "done")

	// Parse all files
	parseResults, symbolTable := indexer.parseAllFiles(ctx, scanCtx, scanCtx.files)

	// Write structural nodes: full rebuild from scratch (all CREATE)
	indexer.progress.Emit(PhaseWriting, 0, 0, "")
	indexer.progress.EmitSub(PhaseWriting, SubStructuralNodes, "")
	symbolEdges, err := indexer.writeFileSystemNodes(ctx, scanCtx.absPath, scanCtx.projectInfo.Frameworks, scanCtx.files, parseResults, scanCtx.result)
	if err != nil {
		return nil, fmt.Errorf("indexer: write structural: %w", err)
	}
	indexer.progress.EmitSub(PhaseWriting, SubStructuralNodes, fmt.Sprintf("%d files", scanCtx.result.FilesScanned))

	// Resolve service name placeholders (before writeSemanticNodes so Step 6 uses resolved names)
	indexer.resolveServiceNamePlaceholders(parseResults)

	// Write all other nodes and edges
	if err := indexer.writeSemanticNodes(ctx, scanCtx, parseResults, symbolTable); err != nil {
		return nil, err
	}

	// Write symbol-related CONTAINS edges (File→Symbol, Class→Function) after symbol nodes exist
	if err := indexer.writeSymbolContainsEdges(ctx, symbolEdges); err != nil {
		return nil, err
	}

	clearParseResultsPhase1(parseResults)

	indexer.progress.Emit(PhaseWriting, 0, 0, "nodes and edges")

	// Inject cross-project symbols into symbolTable for resolver
	crossProjectNodes, err := indexer.injectCrossProjectSymbols(ctx, scanCtx, symbolTable)
	if err != nil {
		return nil, fmt.Errorf("indexer: inject cross-project symbols: %w", err)
	}
	// Resolve and write relationships
	callRelations, err := indexer.resolveAndWriteRelations(ctx, scanCtx, parseResults, symbolTable, crossProjectNodes)
	if err != nil {
		return nil, err
	}

	// Resolve pending remote calls (field-level @DubboReference, @GrpcClient)
	if err := indexer.resolvePendingRemoteCalls(ctx, scanCtx, parseResults, callRelations, symbolTable); err != nil {
		return nil, fmt.Errorf("indexer: resolve pending remote calls: %w", err)
	}

	// Preload Route nodes and HANDLES edges once for Step 8 + Step 9
	allRoutes, _ := indexer.graphStore.QueryAllByKind(ctx, constants.KindRoute, 0)
	handlesEdges, _ := indexer.graphStore.QueryAllEdges(ctx, model.RelHandles, 0)
	routeToHandler := make(map[string]string, len(handlesEdges))
	for _, edge := range handlesEdges {
		routeToHandler[edge.TargetID] = edge.SourceID
	}

	// Flatten RemoteCalls and PendingRemoteCalls for Step 8
	var allRemoteCalls []model.RawRemoteCall
	var allPendingCalls []model.PendingRemoteCall
	for _, parseResult := range parseResults {
		allRemoteCalls = append(allRemoteCalls, parseResult.RemoteCalls...)
		allPendingCalls = append(allPendingCalls, parseResult.PendingRemoteCalls...)
	}
	clearParseResultsPhase2(parseResults)
	indexer.dump.OnRemoteCalls(allRemoteCalls, allPendingCalls)
	// Match consumer to provider (cross-service CALLS edges)
	if err := indexer.matchConsumerToProvider(ctx, scanCtx, allRemoteCalls, allPendingCalls, symbolTable, allRoutes, routeToHandler); err != nil {
		return nil, fmt.Errorf("indexer: match consumer to provider: %w", err)
	}

	//  Write cross-project index
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
	indexer.progress.Emit(PhaseWriting, 0, 0, "")
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

	clearParseResultsPhase1(parseResults)

	indexer.progress.Emit(PhaseWriting, 0, 0, "nodes and edges")

	// Inject cross-project symbols into symbolTable for resolver (clean old nodes first for incremental)
	crossProjectNodes, err := indexer.injectCrossProjectSymbols(ctx, scanCtx, symbolTable)
	if err != nil {
		return nil, fmt.Errorf("indexer: inject cross-project symbols: %w", err)
	}
	// Resolve and write relationships
	callRelations, err := indexer.resolveAndWriteRelations(ctx, scanCtx, parseResults, symbolTable, crossProjectNodes)
	if err != nil {
		return nil, err
	}

	// Preload Route nodes and HANDLES edges once for Step 8 + Step 9

	// Resolve pending remote calls (field-level @DubboReference, @GrpcClient)
	if err := indexer.resolvePendingRemoteCalls(ctx, scanCtx, parseResults, callRelations, symbolTable); err != nil {
		return nil, fmt.Errorf("indexer: resolve pending remote calls: %w", err)
	}
	allRoutes, _ := indexer.graphStore.QueryAllByKind(ctx, constants.KindRoute, 0)
	handlesEdges, _ := indexer.graphStore.QueryAllEdges(ctx, model.RelHandles, 0)
	routeToHandler := make(map[string]string, len(handlesEdges))
	for _, edge := range handlesEdges {
		routeToHandler[edge.TargetID] = edge.SourceID
	}

	// Flatten RemoteCalls and PendingRemoteCalls for Step 8
	var allRemoteCalls []model.RawRemoteCall
	var allPendingCalls []model.PendingRemoteCall
	for _, parseResult := range parseResults {
		allRemoteCalls = append(allRemoteCalls, parseResult.RemoteCalls...)
		allPendingCalls = append(allPendingCalls, parseResult.PendingRemoteCalls...)
	}
	clearParseResultsPhase2(parseResults)
	indexer.dump.OnRemoteCalls(allRemoteCalls, allPendingCalls)
	// Step 8: Match consumer to provider (cross-service CALLS edges)
	if err := indexer.matchConsumerToProvider(ctx, scanCtx, allRemoteCalls, allPendingCalls, symbolTable, allRoutes, routeToHandler); err != nil {
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
