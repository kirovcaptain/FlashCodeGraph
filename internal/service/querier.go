package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/status"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
)

// Querier orchestrates graph queries.
type Querier struct {
	graphStore storage.GraphStore
}

// NewQuerier creates a Querier.
func NewQuerier(graphStore storage.GraphStore) *Querier {
	return &Querier{graphStore: graphStore}
}

// QuerySymbol finds symbols by name.
func (querier *Querier) QuerySymbol(ctx context.Context, name string, opts model.QueryOpts) ([]model.Node, error) {
	return querier.graphStore.QueryNodesByName(ctx, name, opts)
}

// QueryByAnnotation returns symbols that have a specific annotation.
// When params is non-empty, only annotations whose params contain the given substring are matched.
func (querier *Querier) QueryByAnnotation(ctx context.Context, annotation string, params string, kind string, limit int) ([]model.Node, error) {
	annotationNodes, err := querier.graphStore.QueryNodesByName(ctx, annotation, model.QueryOpts{Kinds: []string{constants.KindAnnotation}})
	if err != nil {
		return nil, err
	}
	var matchedAnnotationIDs []string
	for _, annotationNode := range annotationNodes {
		if params != "" {
			annotationParams := propString(annotationNode.Properties, "params")
			if !strings.Contains(strings.ToLower(annotationParams), strings.ToLower(params)) {
				continue
			}
		}
		matchedAnnotationIDs = append(matchedAnnotationIDs, annotationNode.ID)
	}
	if len(matchedAnnotationIDs) == 0 {
		return nil, nil
	}
	// Find source nodes via HAS_ANNOTATION edges
	var results []model.Node
	seen := map[string]bool{}
	for _, annotationID := range matchedAnnotationIDs {
		for _, sourceKind := range []string{constants.KindClass, constants.KindInterface, constants.KindFunction} {
			edges, err := querier.graphStore.QueryEdges(ctx, annotationID, sourceKind, model.RelHasAnnotation, model.Incoming)
			if err != nil {
				continue
			}
			for _, edge := range edges {
				if seen[edge.SourceID] {
					continue
				}
				seen[edge.SourceID] = true
				node, err := querier.graphStore.QueryNodeByID(ctx, edge.SourceID)
				if err != nil || node == nil {
					continue
				}
				if kind != "" && node.Kind != kind {
					continue
				}
				results = append(results, *node)
				if limit > 0 && len(results) >= limit {
					return results, nil
				}
			}
		}
	}
	return results, nil
}

// QueryByLayer returns symbols annotated with a specific layer (controller/service/repository/model).
func (querier *Querier) QueryByLayer(ctx context.Context, layer string, limit int) ([]model.Node, error) {
	annotations, err := querier.graphStore.QueryNodesByProperty(ctx, constants.KindAnnotation, "layer", layer, "exact", 0)
	if err != nil {
		return nil, err
	}
	annotationIDs := make([]string, len(annotations))
	for idx, annotation := range annotations {
		annotationIDs[idx] = annotation.ID
	}
	return querier.resolveAnnotatedNodes(ctx, annotationIDs, limit)
}

// QueryByAnnotationCategory returns symbols annotated with a specific category (security/behavior/etc).
func (querier *Querier) QueryByAnnotationCategory(ctx context.Context, category string, limit int) ([]model.Node, error) {
	annotations, err := querier.graphStore.QueryNodesByProperty(ctx, constants.KindAnnotation, "category", category, "exact", 0)
	if err != nil {
		return nil, err
	}
	annotationIDs := make([]string, len(annotations))
	for idx, annotation := range annotations {
		annotationIDs[idx] = annotation.ID
	}
	return querier.resolveAnnotatedNodes(ctx, annotationIDs, limit)
}

func (querier *Querier) resolveAnnotatedNodes(ctx context.Context, annIDs []string, limit int) ([]model.Node, error) {
	var results []model.Node
	seen := map[string]bool{}
	for _, annID := range annIDs {
		for _, srcKind := range []string{constants.KindClass, constants.KindInterface, constants.KindFunction} {
			edges, err := querier.graphStore.QueryEdges(ctx, annID, srcKind, model.RelHasAnnotation, model.Incoming)
			if err != nil {
				continue
			}
			for _, e := range edges {
				if seen[e.SourceID] {
					continue
				}
				seen[e.SourceID] = true
				node, err := querier.graphStore.QueryNodeByID(ctx, e.SourceID)
				if err != nil || node == nil {
					continue
				}
				results = append(results, *node)
				if limit > 0 && len(results) >= limit {
					return results, nil
				}
			}
		}
	}
	return results, nil
}

// QueryCallChain returns the call chain from a symbol.
// DefaultMinConfidence is the minimum confidence threshold for call chain traversal.
// Filters out best_guess (0.25) and type_parent (0.65) edges, keeping only
// type_exact (0.95), arg_count (0.85), same_file (0.85), and name_unique (0.70).
const DefaultMinConfidence = 0.70

// ResolveFunction finds a function by name or qualified name.
// Returns (node, candidates, error). If multiple matches, node is nil and candidates are returned.
func (querier *Querier) ResolveFunction(ctx context.Context, name string) (*model.Node, []model.Node, error) {
	// Try qualified name match: direct lookup by qualified_name
	if strings.Contains(name, ".") {
		node, err := querier.graphStore.QueryNodeByQualifiedName(ctx, name)
		if err != nil {
			return nil, nil, err
		}
		if node != nil {
			return node, nil, nil
		}
		// Fallback: extract short name, query, then filter by qualified_name
		shortName := name[strings.LastIndex(name, ".")+1:]
		nodes, err := querier.graphStore.QueryNodesByName(ctx, shortName, model.QueryOpts{Kinds: []string{constants.KindFunction}, Limit: 20})
		if err != nil {
			return nil, nil, err
		}
		var exact []model.Node
		for _, n := range nodes {
			qn, _ := n.Properties["qualified_name"].(string)
			if qn == name || strings.HasSuffix(qn, name) {
				exact = append(exact, n)
			}
		}
		if len(exact) == 1 {
			return &exact[0], nil, nil
		}
		if len(exact) > 1 {
			return nil, exact, nil
		}
		// No qualified match — fall through to short name search
	}

	// Short name search
	nodes, err := querier.graphStore.QueryNodesByName(ctx, name, model.QueryOpts{Kinds: []string{constants.KindFunction}, Limit: 10})
	if err != nil {
		return nil, nil, err
	}
	if len(nodes) == 0 {
		return nil, nil, nil
	}
	if len(nodes) == 1 {
		return &nodes[0], nil, nil
	}
	return nil, nodes, nil
}

const maxInheritanceDepth = 5

// ResolveFunctionWithInheritance resolves a function symbol, falling back to parent class
// methods via EXTENDS chain when the symbol is not found directly.
// Returns (node, candidates, inheritedFrom, error).
// inheritedFrom is the original child class short name when fallback was used.
func (querier *Querier) ResolveFunctionWithInheritance(ctx context.Context, name string) (*model.Node, []model.Node, string, error) {
	node, candidates, err := querier.ResolveFunction(ctx, name)
	if err != nil || node != nil || len(candidates) > 0 {
		return node, candidates, "", err
	}

	// Fallback: requires "ClassName.methodName" format
	if !strings.Contains(name, ".") {
		return nil, nil, "", nil
	}

	// Extract class name and method name from qualified name
	// "com.example.ChildService.save" → className="ChildService", methodName="save"
	lastDot := strings.LastIndex(name, ".")
	methodName := name[lastDot+1:]
	prefix := name[:lastDot] // "com.example.ChildService"
	childClassShortName := prefix
	if idx := strings.LastIndex(prefix, "."); idx >= 0 {
		childClassShortName = prefix[idx+1:]
	}

	// Find the child class node
	classNodes, err := querier.graphStore.QueryNodesByName(ctx, childClassShortName, model.QueryOpts{Kinds: []string{constants.KindClass}, Limit: 10})
	if err != nil || len(classNodes) == 0 {
		return nil, nil, "", err
	}

	// Pick the class node matching the qualified prefix
	var classNode *model.Node
	for i, cn := range classNodes {
		qn, _ := cn.Properties["qualified_name"].(string)
		if qn == prefix || strings.HasSuffix(qn, prefix) {
			classNode = &classNodes[i]
			break
		}
	}
	if classNode == nil && len(classNodes) == 1 {
		classNode = &classNodes[0]
	}
	if classNode == nil {
		return nil, nil, "", nil
	}

	// Walk up EXTENDS chain
	currentClassID := classNode.ID
	visited := map[string]bool{currentClassID: true}
	for depth := 0; depth < maxInheritanceDepth; depth++ {
		edges, err := querier.graphStore.QueryEdges(ctx, currentClassID, constants.KindClass, model.RelExtends, model.Outgoing)
		if err != nil || len(edges) == 0 {
			break
		}
		parentID := edges[0].TargetID
		if visited[parentID] {
			break
		}
		visited[parentID] = true

		parentNode, err := querier.graphStore.QueryNodeByID(ctx, parentID)
		if err != nil || parentNode == nil {
			break
		}
		parentQN, _ := parentNode.Properties["qualified_name"].(string)
		if parentQN == "" {
			break
		}

		// Check if parent class has the method
		candidateQN := parentQN + "." + methodName
		funcNode, err := querier.graphStore.QueryNodeByQualifiedName(ctx, candidateQN)
		if err != nil {
			break
		}
		if funcNode != nil {
			return funcNode, nil, childClassShortName, nil
		}

		currentClassID = parentID
	}

	return nil, nil, "", nil
}

// FilterSubgraphByDeclaredType filters a subgraph to only include first-level edges whose
// declared_type contains the given class name. First-level edges are those with TargetID == rootNodeID.
// After filtering first-level edges, only upper-level edges reachable from kept first-level callers
// are preserved. This prevents orphaned caller chains from filtered-out first-level nodes.
func FilterSubgraphByDeclaredType(sg *model.Subgraph, rootNodeID string, className string) *model.Subgraph {
	if sg == nil {
		return sg
	}

	// Step 1: Filter first-level edges, collect kept first-level caller IDs
	keptFirstLevelCallers := map[string]bool{}
	var upperEdges []model.Edge
	var filteredEdges []model.Edge
	for _, e := range sg.Edges {
		if e.TargetID == rootNodeID {
			dt, _ := e.Properties["declared_type"].(string)
			if dt != "" && strings.Contains(dt, className) {
				filteredEdges = append(filteredEdges, e)
				keptFirstLevelCallers[e.SourceID] = true
			}
		} else {
			upperEdges = append(upperEdges, e)
		}
	}

	// Step 2: Walk upper edges to find all reachable nodes from kept first-level callers
	reachable := map[string]bool{}
	for id := range keptFirstLevelCallers {
		reachable[id] = true
	}
	// Build reverse adjacency: targetID → edges
	targetToEdges := map[string][]model.Edge{}
	for _, e := range upperEdges {
		targetToEdges[e.TargetID] = append(targetToEdges[e.TargetID], e)
	}
	// BFS from kept first-level callers upward
	queue := make([]string, 0, len(keptFirstLevelCallers))
	for id := range keptFirstLevelCallers {
		queue = append(queue, id)
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, e := range targetToEdges[current] {
			filteredEdges = append(filteredEdges, e)
			if !reachable[e.SourceID] {
				reachable[e.SourceID] = true
				queue = append(queue, e.SourceID)
			}
		}
	}

	// Step 3: Collect nodes
	reachable[rootNodeID] = true
	var filteredNodes []model.Node
	for _, n := range sg.Nodes {
		if reachable[n.ID] {
			filteredNodes = append(filteredNodes, n)
		}
	}
	return &model.Subgraph{Nodes: filteredNodes, Edges: filteredEdges}
}

func (querier *Querier) QueryCallChain(ctx context.Context, symbolName string, direction model.Direction, depth int, minConfidence float64) (*model.Subgraph, error) {
	return querier.QueryCallChainEx(ctx, symbolName, direction, depth, minConfidence, false)
}

// QueryCallChainEx queries call chain with optional UNRESOLVED_CALL edges.
// When the symbol is not found directly, it falls back to parent class methods
// via EXTENDS chain and filters results by declared_type for reverse queries.
func (querier *Querier) QueryCallChainEx(ctx context.Context, symbolName string, direction model.Direction, depth int, minConfidence float64, includeUnresolved bool) (*model.Subgraph, error) {
	if minConfidence < 0 {
		minConfidence = 0
	}
	node, candidates, inheritedFrom, err := querier.ResolveFunctionWithInheritance(ctx, symbolName)
	if err != nil {
		return nil, fmt.Errorf("querier: find symbol %q: %w", symbolName, err)
	}
	if node == nil && len(candidates) > 0 {
		return nil, fmt.Errorf("ambiguous: %d functions match %q, use qualified name", len(candidates), symbolName)
	}
	if node == nil {
		return &model.Subgraph{}, nil
	}

	subgraph, err := querier.graphStore.TraverseCallChain(ctx, node.ID, depth, direction, minConfidence)
	if err != nil {
		return nil, err
	}

	// Filter by declared_type when resolved via inheritance fallback (reverse only)
	if inheritedFrom != "" && direction == model.Incoming {
		subgraph = FilterSubgraphByDeclaredType(subgraph, node.ID, inheritedFrom)
	}

	if includeUnresolved {
		// Collect all node IDs in the subgraph
		nodeIDs := make(map[string]bool)
		nodeIDs[node.ID] = true
		for _, n := range subgraph.Nodes {
			nodeIDs[n.ID] = true
		}
		// Query UNRESOLVED_CALL edges from these nodes
		for nodeID := range nodeIDs {
			edges, err := querier.graphStore.QueryEdges(ctx, nodeID, constants.KindFunction, model.RelUnresolvedCall, direction)
			if err != nil {
				continue
			}
			for _, e := range edges {
				subgraph.Edges = append(subgraph.Edges, e)
				// Add target node if not already in subgraph
				targetID := e.TargetID
				if direction == model.Incoming {
					targetID = e.SourceID
				}
				if !nodeIDs[targetID] {
					nodeIDs[targetID] = true
					targetNode, err := querier.graphStore.QueryNodeByID(ctx, targetID)
					if err == nil && targetNode != nil {
						subgraph.Nodes = append(subgraph.Nodes, *targetNode)
					}
				}
			}
		}
	}

	return subgraph, nil
}

// QueryCallChainByNodeID traverses call chain starting from a pre-resolved node ID.
func (querier *Querier) QueryCallChainByNodeID(ctx context.Context, nodeID string, direction model.Direction, depth int, minConfidence float64) (*model.Subgraph, error) {
	return querier.graphStore.TraverseCallChain(ctx, nodeID, depth, direction, minConfidence)
}

// FilterCoreSubgraph removes accessor (is_getter/is_setter) and external (file_path=="[external]") nodes
// and their associated edges from a subgraph, returning a simplified core call chain.
func FilterCoreSubgraph(sg *model.Subgraph) *model.Subgraph {
	if sg == nil {
		return sg
	}
	excluded := map[string]bool{}
	var nodes []model.Node
	for _, n := range sg.Nodes {
		props := n.Properties
		if props["is_getter"] == true || props["is_setter"] == true {
			excluded[n.ID] = true
			continue
		}
		if fp, _ := props["file_path"].(string); fp == "[external]" || fp == "" {
			excluded[n.ID] = true
			continue
		}
		nodes = append(nodes, n)
	}
	var edges []model.Edge
	for _, e := range sg.Edges {
		if !excluded[e.SourceID] && !excluded[e.TargetID] {
			edges = append(edges, e)
		}
	}
	return &model.Subgraph{Nodes: nodes, Edges: edges}
}

// FilterCoreRouteChain removes accessor and external nodes from a route chain.
func FilterCoreRouteChain(chain *model.RouteChain) *model.RouteChain {
	if chain == nil {
		return chain
	}
	var filtered []model.ChainNode
	for _, cn := range chain.Chain {
		if cn.FilePath == "[external]" || cn.FilePath == "" {
			continue
		}
		if cn.IsGetter || cn.IsSetter {
			continue
		}
		filtered = append(filtered, cn)
	}
	return &model.RouteChain{
		Route:   chain.Route,
		Method:  chain.Method,
		Chain:   filtered,
		Queries: chain.Queries,
	}
}

// ImpactAnalysis finds all symbols affected by changes to a given symbol.
func (querier *Querier) ImpactAnalysis(ctx context.Context, symbolName string, depth int) (*model.Subgraph, error) {
	node, _, inheritedFrom, err := querier.ResolveFunctionWithInheritance(ctx, symbolName)
	if err != nil {
		return nil, fmt.Errorf("querier: find symbol %q: %w", symbolName, err)
	}
	if node == nil {
		return &model.Subgraph{}, nil
	}

	subgraph, err := querier.graphStore.TraverseImpact(ctx, node.ID, depth)
	if err != nil {
		return nil, err
	}

	if inheritedFrom != "" {
		subgraph = FilterSubgraphByDeclaredType(subgraph, node.ID, inheritedFrom)
	}
	return subgraph, nil
}

// QueryClassMethods returns all methods belonging to a class.
// Supports short name ("Store") and qualified name ("falkor.Store").
// Returns (methods, nil, nil) on unique match.
// Returns (nil, candidates, nil) when multiple classes match.
func (querier *Querier) QueryClassMethods(ctx context.Context, className string, limit int) ([]model.Node, []model.Node, error) {
	searchName := className
	if strings.Contains(className, ".") {
		searchName = className[strings.LastIndex(className, ".")+1:]
	}

	classNodes, err := querier.graphStore.QueryNodesByName(ctx, searchName, model.QueryOpts{
		Kinds: []string{constants.KindClass},
		Limit: 20,
	})
	if err != nil {
		return nil, nil, err
	}

	if strings.Contains(className, ".") {
		var filtered []model.Node
		for _, classNode := range classNodes {
			qualifiedName, _ := classNode.Properties["qualified_name"].(string)
			if qualifiedName == className || strings.HasSuffix(qualifiedName, className) {
				filtered = append(filtered, classNode)
			}
		}
		classNodes = filtered
	}

	if len(classNodes) == 0 {
		return nil, nil, nil
	}
	if len(classNodes) > 1 {
		return nil, classNodes, nil
	}

	edges, err := querier.graphStore.QueryEdges(ctx, classNodes[0].ID, classNodes[0].Kind, model.RelContains, model.Outgoing)
	if err != nil {
		return nil, nil, err
	}
	var methods []model.Node
	for _, edge := range edges {
		node, err := querier.graphStore.QueryNodeByID(ctx, edge.TargetID)
		if err != nil || node == nil {
			continue
		}
		methods = append(methods, *node)
		if limit > 0 && len(methods) >= limit {
			break
		}
	}
	return methods, nil, nil
}

// SearchFTS performs full-text search.
func (querier *Querier) SearchFTS(ctx context.Context, query string, limit int) ([]storage.SearchResult, error) {
	return querier.graphStore.SearchFTS(ctx, query, limit)
}

// Overview returns project statistics.
func (querier *Querier) Overview(ctx context.Context) (*model.GraphStats, error) {
	return querier.graphStore.GetStats(ctx)
}

// QueryEdges returns edges connected to a node, filtered by kind and direction.
func (querier *Querier) QueryEdges(ctx context.Context, nodeID, nodeKind string, relKind model.RelationKind, direction model.Direction) ([]model.Edge, error) {
	return querier.graphStore.QueryEdges(ctx, nodeID, nodeKind, relKind, direction)
}

// Report generates a data quality report for the graph.
// propString safely extracts a string property, returning "" for nil/non-string values.
func propString(props map[string]any, key string) string {
	v, ok := props[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// propBool extracts a boolean property from a node properties map.
func propBool(props map[string]any, key string) bool {
	v, _ := props[key].(bool)
	return v
}

// propInt safely extracts an int property from various numeric types.
func propInt(props map[string]any, key string) int {
	v, ok := props[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case int32:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

// LocateFunction maps file+line pairs to their enclosing symbol (Function > Class > Interface > File).
func (querier *Querier) LocateFunction(ctx context.Context, repoPath string, requests []model.LocateRequest) ([]model.LocateResult, error) {
	// Normalize file paths: absolute → relative
	for i := range requests {
		requests[i].FilePath = filepath.ToSlash(requests[i].FilePath)
		if filepath.IsAbs(requests[i].FilePath) {
			normalizedRepo := filepath.ToSlash(repoPath)
			if rel, err := filepath.Rel(normalizedRepo, requests[i].FilePath); err == nil && !strings.HasPrefix(rel, "..") {
				requests[i].FilePath = filepath.ToSlash(rel)
			}
		}
	}

	// Query nodes grouped by file_path
	fileNodes := make(map[string][]model.Node)
	for _, req := range requests {
		if _, ok := fileNodes[req.FilePath]; !ok {
			nodes, err := querier.graphStore.QueryNodesByFile(ctx, req.FilePath)
			if err != nil {
				fileNodes[req.FilePath] = nil
			} else {
				fileNodes[req.FilePath] = nodes
			}
		}
	}

	results := make([]model.LocateResult, len(requests))
	for i, req := range requests {
		results[i] = model.LocateResult{FilePath: req.FilePath, Line: req.Line, Kind: constants.KindFile}
		bestSpan := int(^uint(0) >> 1)
		for _, node := range fileNodes[req.FilePath] {
			startLine := propInt(node.Properties, "start_line")
			endLine := propInt(node.Properties, "end_line")
			if startLine > 0 && endLine > 0 && startLine <= req.Line && req.Line <= endLine {
				span := endLine - startLine
				if span < bestSpan {
					bestSpan = span
					results[i].Symbol = propString(node.Properties, "qualified_name")
					results[i].Kind = node.Kind
					results[i].StartLine = startLine
					results[i].EndLine = endLine
				}
			}
		}
	}
	return results, nil
}

func (querier *Querier) Report(ctx context.Context) (*model.GraphReport, error) {
	report := &model.GraphReport{
		NodeCounts: make(map[string]int),
		EdgeCounts: make(map[string]int),
	}

	nodeKinds := constants.AllNodeKinds
	seenIDs := make(map[string]bool)
	

	for _, kind := range nodeKinds {
		nodes, err := querier.graphStore.QueryAllByKind(ctx, kind, 100000)
		if err != nil {
			continue
		}
		report.NodeCounts[kind] = len(nodes)
		

		for _, node := range nodes {
			if seenIDs[node.ID] {
				report.DuplicateNodes = append(report.DuplicateNodes, node.ID)
			}
			seenIDs[node.ID] = true

			name := propString(node.Properties, "name")
			qname := propString(node.Properties, "qualified_name")
			fp := propString(node.Properties, "file_path")
			line := propInt(node.Properties, "start_line")

			// Quality checks
			if (kind == constants.KindFunction || kind == constants.KindClass) && fp == "" {
				report.MissingFilePath = append(report.MissingFilePath, node.ID)
			}
			if (kind == constants.KindFunction || kind == constants.KindClass || kind == constants.KindInterface) && name == "" {
				report.EmptyNames = append(report.EmptyNames, node.ID)
			}

			// Symbol details
			detail := model.SymbolDetail{Name: name, QualifiedName: qname, FilePath: fp, Line: line}
			switch kind {
			case constants.KindFunction:
				report.Functions = append(report.Functions, detail)
			case constants.KindClass:
				report.Classes = append(report.Classes, detail)
			case constants.KindInterface:
				report.Interfaces = append(report.Interfaces, detail)
			case constants.KindRoute:
				report.RouteDetails = append(report.RouteDetails, model.RouteDetail{
					Method:      propString(node.Properties, "method"),
					PathPattern: propString(node.Properties, "path_pattern"),
					Handler:     propString(node.Properties, "handler_method"),
				})
			case constants.KindQueryNode:
				report.QueryDetails = append(report.QueryDetails, model.QueryDetail{
					SQLText:   propString(node.Properties, "sql_text"),
					QueryType: propString(node.Properties, "query_type"),
					Tables:    propString(node.Properties, "tables"),
					Caller:    propString(node.Properties, "caller"),
				})
			}
		}
	}

	// Collect edges: single query per relation kind (avoids N+1)
	edgeKinds := []model.RelationKind{model.RelCalls, model.RelExtends, model.RelImplements, model.RelOverrides, model.RelImports}
	for _, relKind := range edgeKinds {
		edges, err := querier.graphStore.QueryAllEdges(ctx, relKind, 100000)
		if err != nil {
			continue
		}
		for _, edge := range edges {
			report.Edges = append(report.Edges, model.EdgeDetail{
				Source: edge.SourceID,
				Target: edge.TargetID,
				Kind:   string(relKind),
			})
			report.EdgeCounts[string(relKind)]++
		}
	}

	// Summary issues
	if len(report.DuplicateNodes) > 0 {
		report.Issues = append(report.Issues, fmt.Sprintf("%d duplicate node IDs", len(report.DuplicateNodes)))
	}
	if len(report.MissingFilePath) > 0 {
		report.Issues = append(report.Issues, fmt.Sprintf("%d nodes missing file_path", len(report.MissingFilePath)))
	}
	if len(report.EmptyNames) > 0 {
		report.Issues = append(report.Issues, fmt.Sprintf("%d nodes with empty name", len(report.EmptyNames)))
	}

	return report, nil
}

// findRouteNode finds a route node by path_pattern. Tries exact match first, then contains fallback.
// Returns error with candidate list if multiple routes match via contains.
func (querier *Querier) findRouteNode(ctx context.Context, routePath string, method string) (*model.Node, error) {
	// 1. Exact match
	exactMatches, err := querier.graphStore.QueryNodesByProperty(ctx, constants.KindRoute, "path_pattern", routePath, "exact", 0)
	if err != nil {
		return nil, err
	}
	if node := filterByMethod(exactMatches, method); node != nil {
		return node, nil
	}

	// 2. Contains fallback
	containsMatches, err := querier.graphStore.QueryNodesByProperty(ctx, constants.KindRoute, "path_pattern", routePath, "contains", 0)
	if err != nil {
		return nil, err
	}
	filtered := filterAllByMethod(containsMatches, method)
	if len(filtered) == 1 {
		return &filtered[0], nil
	}
	if len(filtered) > 1 {
		var candidates []string
		for _, r := range filtered {
			candidates = append(candidates, fmt.Sprintf("  %s %s", propString(r.Properties, "method"), propString(r.Properties, "path_pattern")))
		}
		return nil, fmt.Errorf("multiple routes match \"%s\":\n%s\nPlease specify the full route path", routePath, strings.Join(candidates, "\n"))
	}

	return nil, fmt.Errorf("route not found: %s %s", method, routePath)
}

// filterByMethod returns the first node matching the method filter, or nil if none match.
func filterByMethod(nodes []model.Node, method string) *model.Node {
	for i := range nodes {
		if method == "" || propString(nodes[i].Properties, "method") == method {
			return &nodes[i]
		}
	}
	return nil
}

// filterAllByMethod returns all nodes matching the method filter.
func filterAllByMethod(nodes []model.Node, method string) []model.Node {
	if method == "" {
		return nodes
	}
	var result []model.Node
	for _, node := range nodes {
		if propString(node.Properties, "method") == method {
			result = append(result, node)
		}
	}
	return result
}

// QueryRouteChain traces a route through HANDLES → BFS CALLS → EXECUTES, annotating each node with its layer.
func (querier *Querier) QueryRouteChain(ctx context.Context, routePath string, method string, maxDepth int) (*model.RouteChain, error) {
	if maxDepth <= 0 {
		maxDepth = 10
	}

	// Find matching route: exact match first, then contains fallback
	routeNode, err := querier.findRouteNode(ctx, routePath, method)
	if err != nil {
		return nil, err
	}

	chain := &model.RouteChain{
		Route:  propString(routeNode.Properties, "path_pattern"),
		Method: propString(routeNode.Properties, "method"),
	}

	// HANDLES edges: Route ← Function
	handles, err := querier.graphStore.QueryEdges(ctx, routeNode.ID, constants.KindRoute, model.RelHandles, model.Incoming)
	if err != nil || len(handles) == 0 {
		return chain, nil
	}

	// Batch-load all data into memory for fast traversal
	funcs, _ := querier.graphStore.QueryAllByKind(ctx, constants.KindFunction, 0)
	funcMap := make(map[string]*model.Node, len(funcs))
	for i := range funcs {
		funcMap[funcs[i].ID] = &funcs[i]
	}

	callEdges, _ := querier.graphStore.QueryAllEdges(ctx, model.RelCalls, 0)
	childrenMap := make(map[string][]string)
	for _, edge := range callEdges {
		childrenMap[edge.SourceID] = append(childrenMap[edge.SourceID], edge.TargetID)
	}

	execEdges, _ := querier.graphStore.QueryAllEdges(ctx, model.RelExecutes, 0)
	execMap := make(map[string][]string)
	for _, edge := range execEdges {
		execMap[edge.SourceID] = append(execMap[edge.SourceID], edge.TargetID)
	}

	queryNodes, _ := querier.graphStore.QueryAllByKind(ctx, constants.KindQueryNode, 0)
	queryMap := make(map[string]*model.Node, len(queryNodes))
	for i := range queryNodes {
		queryMap[queryNodes[i].ID] = &queryNodes[i]
	}

	// Build layer map using batch-loaded data
	layerMap := querier.buildLayerMapBatch(ctx, funcs)

	// DFS traversal in memory
	visited := map[string]bool{}
	for _, h := range handles {
		querier.traceCallChainMem(h.SourceID, maxDepth, funcMap, childrenMap, execMap, queryMap, layerMap, visited, chain)
	}
	return chain, nil
}

func (querier *Querier) traceCallChainMem(nodeID string, maxDepth int, funcMap map[string]*model.Node, childrenMap map[string][]string, execMap map[string][]string, queryMap map[string]*model.Node, layerMap map[string]string, visited map[string]bool, chain *model.RouteChain) {
	if visited[nodeID] || maxDepth <= 0 {
		return
	}
	visited[nodeID] = true

	node := funcMap[nodeID]
	if node == nil {
		return
	}

	chain.Chain = append(chain.Chain, model.ChainNode{
		ID:            node.ID,
		Name:          propString(node.Properties, "name"),
		QualifiedName: propString(node.Properties, "qualified_name"),
		Kind:          node.Kind,
		FilePath:      propString(node.Properties, "file_path"),
		Layer:         layerMap[node.ID],
		IsGetter:      propBool(node.Properties, "is_getter"),
		IsSetter:      propBool(node.Properties, "is_setter"),
	})

	callerName := propString(node.Properties, "qualified_name")
	for _, targetID := range execMap[nodeID] {
		if qn := queryMap[targetID]; qn != nil {
			sqlText := propString(qn.Properties, "sql_text")
			queryType := propString(qn.Properties, "query_type")
			tables := propString(qn.Properties, "tables")
			name := propString(qn.Properties, "name")
			if name == "" {
				name = sqlText
			}
			chain.Queries = append(chain.Queries, model.ChainNode{
				ID:            qn.ID,
				Name:          name,
				Kind:          queryType,
				FilePath:      tables,
				QualifiedName: callerName,
			})
		}
	}

	for _, targetID := range childrenMap[nodeID] {
		querier.traceCallChainMem(targetID, maxDepth-1, funcMap, childrenMap, execMap, queryMap, layerMap, visited, chain)
	}
}

func (querier *Querier) buildLayerMapBatch(ctx context.Context, funcs []model.Node) map[string]string {
	m := map[string]string{}
	anns, err := querier.graphStore.QueryAllByKind(ctx, constants.KindAnnotation, 0)
	if err != nil {
		return m
	}
	annLayerByID := make(map[string]string)
	for _, ann := range anns {
		if layer := propString(ann.Properties, "layer"); layer != "" {
			annLayerByID[ann.ID] = layer
		}
	}
	if len(annLayerByID) == 0 {
		return m
	}

	annotationEdges, _ := querier.graphStore.QueryAllEdges(ctx, model.RelHasAnnotation, 0)
	classMap := make(map[string]*model.Node)
	for _, kind := range []string{constants.KindClass, constants.KindInterface} {
		nodes, _ := querier.graphStore.QueryAllByKind(ctx, kind, 0)
		for i := range nodes {
			classMap[nodes[i].ID] = &nodes[i]
		}
	}

	fileLayerMap := map[string]string{}
	for _, edge := range annotationEdges {
		layer, ok := annLayerByID[edge.TargetID]
		if !ok {
			continue
		}
		m[edge.SourceID] = layer
		if cls, ok := classMap[edge.SourceID]; ok {
			if fp := propString(cls.Properties, "file_path"); fp != "" {
				fileLayerMap[fp] = layer
			}
		}
	}

	if len(fileLayerMap) > 0 {
		for _, fn := range funcs {
			if _, exists := m[fn.ID]; exists {
				continue
			}
			if fp := propString(fn.Properties, "file_path"); fp != "" {
				if layer, ok := fileLayerMap[fp]; ok {
					m[fn.ID] = layer
				}
			}
		}
	}
	return m
}

// AffectedRoute represents an entry point affected by a code change.
type AffectedRoute struct {
	Method        string `json:"method,omitempty"`
	Route         string `json:"route,omitempty"`
	EntryFunction string `json:"entry_function"`
	FilePath      string `json:"file_path"`
	EntryType     string `json:"entry_type"`
}

// QueryAffectedRoutes finds entry points (API routes, scheduled tasks, etc.) affected by changes to the given functions.
// Returns affected routes and an optional hint message. Requires analyze data (Process/STEP).
func (querier *Querier) QueryAffectedRoutes(ctx context.Context, nodeIDs []string, repoPath string) ([]AffectedRoute, string) {
	// Load all Process nodes
	processes, err := querier.graphStore.QueryAllByKind(ctx, constants.KindProcess, 0)
	if err != nil || len(processes) == 0 {
		return nil, "Run analyze_repository to see affected entry points."
	}

	// Load all STEP edges
	stepEdges, err := querier.graphStore.QueryAllEdges(ctx, model.RelStep, 0)
	if err != nil {
		return nil, "Run analyze_repository to see affected entry points."
	}

	// Build reverse map: functionID → set of processIDs
	funcToProcesses := make(map[string]map[string]bool)
	for _, edge := range stepEdges {
		if funcToProcesses[edge.TargetID] == nil {
			funcToProcesses[edge.TargetID] = make(map[string]bool)
		}
		funcToProcesses[edge.TargetID][edge.SourceID] = true
	}

	// Build process lookup
	processMap := make(map[string]*model.Node, len(processes))
	for i := range processes {
		processMap[processes[i].ID] = &processes[i]
	}

	// Collect affected processes
	seen := make(map[string]bool)
	var routes []AffectedRoute
	for _, nodeID := range nodeIDs {
		for processID := range funcToProcesses[nodeID] {
			if seen[processID] {
				continue
			}
			seen[processID] = true
			p := processMap[processID]
			if p == nil {
				continue
			}
			routes = append(routes, AffectedRoute{
				Method:        propString(p.Properties, "route_method"),
				Route:         propString(p.Properties, "route_path"),
				EntryFunction: propString(p.Properties, "name"),
				FilePath:      propString(p.Properties, "file_path"),
				EntryType:     propString(p.Properties, "entry_type"),
			})
		}
	}

	// Check if analyze data is outdated
	hint := ""
	if status.NeedsAnalyze(repoPath) {
		hint = "Analyze data may be outdated, consider re-analyzing."
	}

	return routes, hint
}
