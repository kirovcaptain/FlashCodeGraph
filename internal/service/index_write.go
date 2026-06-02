package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/annotation"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

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
//   2. InferImplements: language helpers infer additional IMPLEMENTS edges
//      (e.g. Go struct satisfying an interface without explicit declaration).
//   3. Cross-Project Nodes: writes only the cross-project dependency nodes that are
//      actually referenced by resolved relations, avoiding thousands of unused nodes.
//   4. Relation Edges: writes all resolved CALLS + EXTENDS + IMPLEMENTS + OVERRIDES edges.
//   5. Unresolved Hint Edges: writes UNRESOLVED_CALL edges for calls that could not be

func (indexer *Indexer) writeReferencedCrossProjectNodes(
	ctx context.Context,
	symbolTable *resolver.SymbolTable,
	crossProjectNodes map[string]model.Node,
	callRelations, heritageRelations, overrideRelations, implementsRelations []model.ResolvedRelation,
	callHints []model.UnresolvedHint,
) error {
	if len(crossProjectNodes) == 0 {
		return nil
	}

	referencedIDs := make(map[string]bool)

	// Collect from all relation types
	allRelations := make([]model.ResolvedRelation, 0, len(callRelations)+len(heritageRelations)+len(overrideRelations)+len(implementsRelations))
	allRelations = append(allRelations, callRelations...)
	allRelations = append(allRelations, heritageRelations...)
	allRelations = append(allRelations, overrideRelations...)
	allRelations = append(allRelations, implementsRelations...)
	for _, relation := range allRelations {
		if strings.HasPrefix(relation.SourceID, "cross-project:") {
			referencedIDs[relation.SourceID] = true
		}
		if strings.HasPrefix(relation.TargetID, "cross-project:") {
			referencedIDs[relation.TargetID] = true
		}
	}

	// Collect from unresolved call hints
	for _, hint := range callHints {
		for _, candidateQualifiedName := range hint.Candidates {
			candidateSymbols := symbolTable.FindByQualifiedName(candidateQualifiedName)
			for _, candidate := range candidateSymbols {
				if strings.HasPrefix(candidate.ID, "cross-project:") {
					referencedIDs[candidate.ID] = true
				}
			}
		}
	}

	var referencedNodes []model.Node
	for referencedID := range referencedIDs {
		if node, exists := crossProjectNodes[referencedID]; exists {
			referencedNodes = append(referencedNodes, node)
		}
	}
	if len(referencedNodes) > 0 {
		if err := indexer.graphStore.CreateNodes(ctx, referencedNodes); err != nil {
			return fmt.Errorf("indexer: write cross-project nodes: %w", err)
		}
	}
	indexer.progress.EmitSub(PhaseResolving, SubCrossProjectNodes,
		fmt.Sprintf("%d nodes (of %d prepared)", len(referencedNodes), len(crossProjectNodes)))
	indexer.dump.OnCrossProjectSymbols(len(crossProjectNodes), len(referencedNodes))
	return nil
}

func (indexer *Indexer) writeResolvedRelations(
	ctx context.Context,
	scanCtx *scanContext,
	symbolTable *resolver.SymbolTable,
	callRelations, heritageRelations, overrideRelations, implementsRelations, usesRelations []model.ResolvedRelation,
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
					"file_path":      constants.FilePathExternal,
				},
			})
		}
	}
	if len(externalNodes) > 0 {
		indexer.dump.OnExternalNodes(externalNodes)
		if err := indexer.graphStore.CreateNodes(ctx, externalNodes); err != nil {
			return fmt.Errorf("indexer: write external nodes: %w", err)
		}
	}
	indexer.progress.EmitSub(PhaseResolving, SubExternalNodes, fmt.Sprintf("%d nodes", len(externalNodes)))

	// Step 2: Write all resolved relation edges (CALLS + EXTENDS + IMPLEMENTS + OVERRIDES + USES).
	allRelations := append(callRelations, heritageRelations...)
	allRelations = append(allRelations, overrideRelations...)
	allRelations = append(allRelations, implementsRelations...)
	allRelations = append(allRelations, usesRelations...)
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
		if relation.Confidence < constants.ConfidenceLowThreshold {
			scanCtx.result.LowConfidenceCount++
		}
	}
	return nil
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
					"params":         marshalParams(symbol.Params),
					"return_types":   model.FormatReturnTypes(symbol.ReturnTypes),
					"type_params":    symbol.TypeParams,
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
					"params":         marshalParams(symbol.Params),
					"type_params":    symbol.TypeParams,
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
			case constants.KindVariable:
				props = map[string]any{
					"name":           symbol.Name,
					"qualified_name": symbol.QualifiedName,
					"file_path":      symbol.FilePath,
					"line":           symbol.StartLine,
					"var_type":       "",
					"visibility":     symbol.Visibility,
					"is_final":       symbol.IsFinal,
					"is_static":      symbol.IsStatic,
					"is_exported":    symbol.IsExported,
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
		indexer.dump.OnSymbolNodes(nodes)
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
			if v, ok := relation.Metadata["ref_kind"]; ok && v != "" {
				edge.Properties["ref_kind"] = v
			}
			if v, ok := relation.Metadata["event_type"]; ok && v != "" {
				edge.Properties["event_type"] = v
			}
			if relation.ChainID > 0 {
				edge.Properties["chain_id"] = relation.ChainID
				edge.Properties["chain_depth"] = relation.ChainDepth
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

	indexer.dump.OnStructuralNodes(nodes, edges)
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
	for _, kind := range []string{constants.KindClass, constants.KindInterface} {
		existing, _ := indexer.graphStore.QueryAllByKind(ctx, kind, 0)
		for _, node := range existing {
			qualifiedName, _ := node.Properties["qualified_name"].(string)
			if qualifiedName != "" {
				if _, exists := classIDByQualifiedName[qualifiedName]; !exists {
					classIDByQualifiedName[qualifiedName] = classInfo{ID: node.ID, Kind: kind}
				}
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

func buildStructuralData(repoID, repoName, absPath string, frameworks []string, files []scanner.ScannedFile, parseResults []model.ParseResult, classIDByQualifiedName map[string]classInfo) ([]model.Node, []model.Edge, []model.Edge) {
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
			if info, exists := classIDByQualifiedName[classQualifiedName]; exists {
				sourceKind := constants.SourceKindClassFunc
				if info.Kind == constants.KindInterface {
					sourceKind = constants.SourceKindInterfaceFunc
				}
				symbolEdges = append(symbolEdges, model.Edge{
					SourceID:   info.ID,
					TargetID:   symbol.ID,
					Kind:       model.RelContains,
					SourceKind: sourceKind,
				})
			}
		}
	}

	return nodes, edges, symbolEdges
}

// buildClassMap collects qualifiedName → ID mapping for all Class nodes from parseResults.
type classInfo struct {
	ID   string
	Kind string
}

func buildClassMap(parseResults []model.ParseResult) map[string]classInfo {
	classIDByQualifiedName := make(map[string]classInfo)
	for _, parseResult := range parseResults {
		for _, symbol := range parseResult.Symbols {
			switch symbol.Kind {
			case constants.KindClass, constants.KindInterface:
				classIDByQualifiedName[symbol.QualifiedName] = classInfo{ID: symbol.ID, Kind: symbol.Kind}
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

			handlerMethod := ""
			var middlewareNames []string
			if len(route.Handlers) > 0 {
				handlerMethod = route.Handlers[len(route.Handlers)-1]
				middlewareNames = route.Handlers[:len(route.Handlers)-1]
			}
			middlewaresValue := ""
			if len(middlewareNames) > 0 {
				middlewaresValue = strings.Join(middlewareNames, ",")
			}

			nodes = append(nodes, model.Node{
				ID:   routeID,
				Kind: constants.KindRoute,
				Properties: map[string]any{
					"method":         route.Method,
					"path_pattern":   route.PathPattern,
					"handler_method": handlerMethod,
					"middlewares":    middlewaresValue,
					"framework":      route.Framework,
					"file_path":      route.FilePath,
				},
			})
			result.SymbolsByKind["route"]++
			result.SymbolsCreated++

			// Resolve each handler and create HANDLES edges with order
			for order, handlerName := range route.Handlers {
				handlerID := resolveHandlerFunction(symbolTable, handlerName, route.FilePath)
				if handlerID != "" {
					edges = append(edges, model.Edge{
						SourceID:   handlerID,
						TargetID:   routeID,
						Kind:       model.RelHandles,
						SourceKind: constants.KindFunction,
						Properties: map[string]any{"handler_order": order},
					})
					result.RelationsByKind["HANDLES"]++
					result.RelationsCreated++
				}
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

	// Propagate FeignClient routes: build CALLS edges from interface methods to implementations
	var feignEdges []model.Edge
	propagateFeignRoutes(parseResults, symbolTable, nodes, edges, &feignEdges, result)
	if len(feignEdges) > 0 {
		return indexer.graphStore.CreateEdges(ctx, feignEdges)
	}
	return nil
}

// propagateFeignRoutes creates CALLS edges from Feign interface methods to their @RestController implementation methods.
// This allows query_route_chain to traverse from the Feign interface method to the actual implementation.
func propagateFeignRoutes(parseResults []model.ParseResult, symbolTable *resolver.SymbolTable, routeNodes []model.Node, handlesEdges []model.Edge, resultEdges *[]model.Edge, result *model.IndexResult) {
	// Build map: routeID → Feign interface method handler name
	feignRouteHandlers := make(map[string]string) // routeID → "OrderClient.getOrder"
	for _, routeNode := range routeNodes {
		framework, _ := routeNode.Properties["framework"].(string)
		handlerMethod, _ := routeNode.Properties["handler_method"].(string)
		if framework == "feign" && handlerMethod != "" {
			feignRouteHandlers[routeNode.ID] = handlerMethod
		}
	}
	if len(feignRouteHandlers) == 0 {
		return
	}

	// Build map: routeID → Feign interface method symbol ID (from HANDLES edges)
	feignMethodIDs := make(map[string]string) // routeID → symbolID of interface method
	for _, edge := range handlesEdges {
		if _, isFeignRoute := feignRouteHandlers[edge.TargetID]; isFeignRoute {
			feignMethodIDs[edge.TargetID] = edge.SourceID
		}
	}

	for _, parseResult := range parseResults {
		for _, heritage := range parseResult.Heritage {
			if heritage.Kind != "implements" {
				continue
			}
			var matchedMethods []struct{ methodName, routeID string }
			for routeID, handlerName := range feignRouteHandlers {
				parts := strings.SplitN(handlerName, ".", 2)
				if len(parts) == 2 && parts[0] == heritage.ParentName {
					matchedMethods = append(matchedMethods, struct{ methodName, routeID string }{parts[1], routeID})
				}
			}
			if len(matchedMethods) == 0 {
				continue
			}
			hasRestController := false
			for _, symbol := range parseResult.Symbols {
				if symbol.Name == heritage.ChildName && strings.Contains(symbol.Annotations, "RestController") {
					hasRestController = true
					break
				}
			}
			if !hasRestController {
				continue
			}
			for _, matched := range matchedMethods {
				implementationID := resolveHandlerFunction(symbolTable, heritage.ChildName+"."+matched.methodName, parseResult.FilePath)
				feignInterfaceMethodID := feignMethodIDs[matched.routeID]
				if implementationID != "" && feignInterfaceMethodID != "" {
					*resultEdges = append(*resultEdges, model.Edge{
						SourceID:   feignInterfaceMethodID,
						TargetID:   implementationID,
						Kind:       model.RelCalls,
						SourceKind: constants.KindFunction,
						Properties: map[string]any{
							"confidence": 1.0,
							"feign_impl": true,
						},
					})
					result.RelationsByKind["CALLS"]++
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
					"base_sql":   query.BaseSQL,
					"conditions": marshalConditions(query.Conditions),
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
