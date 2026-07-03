package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	resolvergo "github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver/golang"
	resolverjava "github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver/java"
	resolverkotlin "github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver/kotlin"
	resolverpy "github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver/python"
	resolverts "github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver/typescript"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/typeinfer"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// resolvedRelations groups all resolved relation results from the resolve phase.
type resolvedRelations struct {
	Calls      []model.ResolvedRelation
	Heritage   []model.ResolvedRelation
	Overrides  []model.ResolvedRelation
	Implements []model.ResolvedRelation
	Uses       []model.ResolvedRelation
	References []model.ResolvedRelation
	Hints      []model.UnresolvedHint
}

func (indexer *Indexer) resolveAndWriteRelations(ctx context.Context, scanCtx *scanContext, parseResults []model.ParseResult, symbolTable *resolver.SymbolTable, crossProjectNodes map[string]model.Node) ([]model.ResolvedRelation, error) {
	indexer.progress.Emit(PhaseResolving, 0, 0, "")

	// Build language helper for the project's primary language only
	langHelpers := buildLanguageHelpers(scanCtx.projectInfo.Language, symbolTable, scanCtx.projectInfo.Frameworks, scanCtx.absPath)
	resolverInstance := resolver.NewResolver(symbolTable, langHelpers)
	allImports, allCalls, allHeritage := collectRawRelations(parseResults)
	allFilePaths := collectSourceFilePaths(scanCtx.files)

	// Phase A: Import resolution
	importRelations, err := indexer.resolveImports(ctx, resolverInstance, allImports, allFilePaths, scanCtx.result)
	if err != nil {
		return nil, err
	}

	// Phase A2: Export propagation — build reExportIndex and ImportFileMap for barrel resolution
	importFileMap := indexer.propagateExports(parseResults, symbolTable, allFilePaths, scanCtx.absPath, scanCtx.projectInfo.Language)
	resolverInstance.SetImportFileMap(importFileMap)

	// Phase B + C: Type inference, call/heritage/override/uses resolution, cross-file propagation
	relations, err := indexer.resolveCallsAndHeritage(
		ctx, resolverInstance, parseResults, symbolTable, allCalls, allHeritage, importRelations, langHelpers, scanCtx.projectInfo.Language)
	if err != nil {
		return nil, err
	}

	// Phase D: Write results to graph

	// D-1: Infer implicit interface implementations (e.g. Go structs satisfying interfaces)
	for _, helper := range langHelpers {
		relations.Implements = append(relations.Implements, helper.InferImplements()...)
	}

	// D-2: Write cross-project nodes referenced by call/heritage/override/implements relations
	if err := indexer.writeReferencedCrossProjectNodes(ctx, symbolTable, crossProjectNodes, &relations); err != nil {
		return nil, err
	}

	// D-3: Write external nodes, all relation edges (calls/heritage/overrides/implements/uses), and unresolved hints
	if err := indexer.writeResolvedRelations(ctx, scanCtx, symbolTable, &relations); err != nil {
		return nil, err
	}
	indexer.progress.Emit(PhaseResolving, 0, 0, "relations")
	return relations.Calls, nil
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
	langHelpers map[string]resolver.LanguageHelper,
	language string,
) (resolvedRelations, error) {

	// Step 1: Local type inference — build per-file TypeEnv from constructor calls and type annotations.
	// Example: "UserService svc = new UserService()" → svc maps to type UserService.
	indexer.progress.EmitSub(PhaseResolving, SubInferLocal, "")
	typeInfer := typeinfer.New()

	// Set ExternalLookup for Go to resolve external package return types
	if language == constants.LangGo {
		if goHelper, exists := langHelpers[constants.LangGo]; exists {
			typeInfer.ExternalLookup = func(qualifiedCall string) (model.ReturnType, bool) {
				dotIndex := strings.LastIndex(qualifiedCall, ".")
				if dotIndex <= 0 {
					return model.ReturnType{}, false
				}
				packageOrType := qualifiedCall[:dotIndex]
				methodName := qualifiedCall[dotIndex+1:]
				return goHelper.LookupMethodReturn(packageOrType, methodName, nil)
			}
		}
	}
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
	heritageRelations := resolverInstance.ResolveHeritage(allHeritage, envs)
	indexer.progress.EmitSub(PhaseResolving, SubResolveHeritage, fmt.Sprintf("%d heritage", len(heritageRelations)))

	// Step 6: Override and dispatch detection — find child methods overriding parent methods.
	// Produces OVERRIDES (child→parent) and DISPATCHES (parent→child) edges for polymorphic dispatch.
	indexer.progress.EmitSub(PhaseResolving, SubDetectOverridesAndDispatches, "")
	overrideRelations := resolverInstance.DetectOverridesAndDispatches(allHeritage)
	indexer.progress.EmitSub(PhaseResolving, SubDetectOverridesAndDispatches, fmt.Sprintf("%d overrides", len(overrideRelations)))

	// Step 6.5: Event dispatch detection — match event publishers to event listeners.
	eventDispatchRelations := resolverInstance.ResolveEventDispatches(allCalls, envs)
	callRelations = append(callRelations, eventDispatchRelations...)

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

	// Step 8: Resolve static constant references into USES edges.
	var allConstRefs []model.RawConstRef
	for _, parseResult := range parseResults {
		allConstRefs = append(allConstRefs, parseResult.ConstRefs...)
	}
	var usesRelations []model.ResolvedRelation
	if len(allConstRefs) > 0 {
		usesRelations = resolverInstance.ResolveConstRefs(allConstRefs, envs)
	}

	resourceReferences := resolverInstance.ResolveResourceReferences(allCalls)
	indexer.dump.OnResourceReferences(resourceReferences)

	return resolvedRelations{
		Calls:      callRelations,
		Heritage:   heritageRelations,
		Overrides:  overrideRelations,
		Uses:       usesRelations,
		References: resourceReferences,
		Hints:      callHints,
	}, nil
}

// writeResolvedRelations writes external nodes and all relation edges to the graph.
// writeReferencedCrossProjectNodes writes only the cross-project nodes that are actually
// referenced by resolved relations or unresolved hints. This avoids writing thousands of

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
		helpers[constants.LangGo] = resolvergo.NewHelper(symbolTable, resolvergo.NewExternalMethodManager(frameworks, projectRoot))
	case constants.LangPython:
		helpers[constants.LangPython] = resolverpy.NewHelper(resolverpy.NewExternalMethodManager(frameworks, projectRoot))
	case constants.LangTypeScript, constants.LangJavaScript:
		tsExternalMethods := resolverts.NewExternalMethodManager(frameworks, projectRoot)
		helpers[constants.LangTypeScript] = resolverts.NewHelper(tsExternalMethods)
		helpers[constants.LangJavaScript] = resolverts.NewHelper(tsExternalMethods)
	case constants.LangKotlin:
		helpers[constants.LangKotlin] = resolverkotlin.NewHelper(symbolTable, resolverkotlin.NewExternalMethodManager(frameworks, projectRoot))
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
