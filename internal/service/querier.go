package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
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
	var firstErr error
	seen := map[string]bool{}
	for _, annotationID := range matchedAnnotationIDs {
		for _, sourceKind := range []string{constants.KindClass, constants.KindInterface, constants.KindFunction} {
			edges, err := querier.graphStore.QueryEdges(ctx, annotationID, sourceKind, model.RelHasAnnotation, model.Incoming)
			if err != nil {
				if firstErr == nil { firstErr = err }
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
	if len(results) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

// QueryByLayer returns symbols annotated with a specific layer (controller/service/repository/model).
func (querier *Querier) QueryByLayer(ctx context.Context, layer string, limit int) ([]model.Node, error) {
	annotations, err := querier.graphStore.QueryNodesByProperty(ctx, constants.KindAnnotation, "layer", layer, storage.MatchExact, 0)
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
	annotations, err := querier.graphStore.QueryNodesByProperty(ctx, constants.KindAnnotation, "category", category, storage.MatchExact, 0)
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
	var firstErr error
	var results []model.Node
	seen := map[string]bool{}
	for _, annID := range annIDs {
		for _, srcKind := range []string{constants.KindClass, constants.KindInterface, constants.KindFunction} {
			edges, err := querier.graphStore.QueryEdges(ctx, annID, srcKind, model.RelHasAnnotation, model.Incoming)
			if err != nil {
				if firstErr == nil { firstErr = err }
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
	if len(results) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

// QueryCallChain returns the call chain from a symbol.
// DefaultMinConfidence is the minimum confidence threshold for call chain traversal.
// Filters out best_guess (0.25) and type_parent (0.65) edges, keeping only
// type_exact (0.95), arg_count (0.85), same_file (0.85), and name_unique (0.70).
const DefaultMinConfidence = constants.ConfidenceDefaultMin

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
		nodes, err := querier.graphStore.QueryNodesByName(ctx, shortName, model.QueryOpts{Kinds: []string{constants.KindFunction}})
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
// inheritedFrom is the child class qualified name (from graph data) when fallback was used.
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
	classNodes, err := querier.graphStore.QueryNodesByName(ctx, childClassShortName, model.QueryOpts{Kinds: []string{constants.KindClass, constants.KindInterface}, Limit: 10})
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
		// Multiple same-name classes and prefix doesn't disambiguate — return candidates
		// with qualified_name set to "classQN.methodName" for display and re-query.
		if len(classNodes) > 1 {
			var methodCandidates []model.Node
			for _, cn := range classNodes {
				qn, _ := cn.Properties["qualified_name"].(string)
				candidate := cn
				props := make(map[string]any, len(cn.Properties))
				for k, v := range cn.Properties {
					props[k] = v
				}
				props["qualified_name"] = qn + "." + methodName
				candidate.Properties = props
				methodCandidates = append(methodCandidates, candidate)
			}
			return nil, methodCandidates, "", nil
		}
		return nil, nil, "", nil
	}

	// Get the child class qualified name from graph data for precise declared_type filtering
	classQualifiedName, _ := classNode.Properties["qualified_name"].(string)

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
			return funcNode, nil, classQualifiedName, nil
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
			declaredType, _ := e.Properties["declared_type"].(string)
			if declaredType != "" && declaredType == className {
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
	// Build excluded set for truncated nodes filtering
	excludedByDeclType := map[string]bool{}
	for _, n := range sg.Nodes {
		if !reachable[n.ID] {
			excludedByDeclType[n.ID] = true
		}
	}
	return &model.Subgraph{Nodes: filteredNodes, Edges: filteredEdges, TruncatedNodes: filterTruncatedNodes(sg.TruncatedNodes, excludedByDeclType, sg.Nodes)}
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
		var names []string
		for _, candidate := range candidates {
			qualifiedName, _ := candidate.Properties["qualified_name"].(string)
			names = append(names, qualifiedName)
		}
		return nil, fmt.Errorf("ambiguous: %d matches for %q. Candidates:\n%s\nPlease ask the user which one to use", len(candidates), symbolName, strings.Join(names, "\n"))
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

// filterTruncatedNodes removes truncated entries whose qualified_name matches any excluded node.
func filterTruncatedNodes(truncated []string, excluded map[string]bool, nodes []model.Node) []string {
	if len(truncated) == 0 || len(excluded) == 0 {
		return truncated
	}
	excludedQN := map[string]bool{}
	for _, n := range nodes {
		if excluded[n.ID] {
			if qn, _ := n.Properties["qualified_name"].(string); qn != "" {
				excludedQN[qn] = true
			}
		}
	}
	var filtered []string
	for _, entry := range truncated {
		if idx := strings.Index(entry, " ("); idx > 0 {
			if excludedQN[entry[:idx]] {
				continue
			}
		}
		filtered = append(filtered, entry)
	}
	return filtered
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
		if filePath, _ := props["file_path"].(string); filePath == constants.FilePathExternal || filePath == "" {
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
	return &model.Subgraph{Nodes: nodes, Edges: edges, TruncatedNodes: filterTruncatedNodes(sg.TruncatedNodes, excluded, sg.Nodes)}
}

// FilterCoreRouteChain removes accessor and external nodes from a route chain.
func FilterCoreRouteChain(chain *model.RouteChain) *model.RouteChain {
	if chain == nil {
		return chain
	}
	var filtered []model.ChainNode
	for _, chainNode := range chain.Chain {
		if chainNode.FilePath == constants.FilePathExternal || chainNode.FilePath == constants.FilePathCrossProject || chainNode.FilePath == "" {
			continue
		}
		if chainNode.IsGetter || chainNode.IsSetter {
			continue
		}
		filtered = append(filtered, chainNode)
	}
	return &model.RouteChain{
		Route:   chain.Route,
		Method:  chain.Method,
		Chain:   filtered,
		Queries: chain.Queries,
	}
}

// PruneDeclaredTypeDispatches removes DISPATCHES edges whose target does not match
// the declared_type of the corresponding CALLS edge. When a CALLS edge has a declared_type
// (e.g. "com.example.SettlementDao"), only DISPATCHES branches from the same base target
// whose qualified_name starts with that declared_type are kept. This prevents unrelated
// subclass implementations (e.g. AbTestReportDao.insert) from polluting the call chain.
// Orphan nodes (no remaining edges) are removed afterward.
func PruneDeclaredTypeDispatches(sg *model.Subgraph) *model.Subgraph {
	if sg == nil {
		return sg
	}

	// Step 1: Collect DISPATCHES targets so we can exclude their CALLS edges from prefix collection.
	dispatchTargets := map[string]bool{}
	for _, e := range sg.Edges {
		if e.Kind == model.RelDispatches {
			dispatchTargets[e.TargetID] = true
		}
	}

	// Step 1b: For each CALLS edge with declared_type, record targetID → set of declared_type prefixes.
	// Skip CALLS edges whose source is a DISPATCHES target (these are super() callbacks, not real callers).
	declaredTypePrefixes := map[string]map[string]bool{} // targetID → set of "declared_type." prefixes
	for _, e := range sg.Edges {
		if e.Kind != model.RelCalls {
			continue
		}
		if dispatchTargets[e.SourceID] {
			continue
		}
		declaredType, _ := e.Properties["declared_type"].(string)
		if declaredType == "" {
			continue
		}
		if declaredTypePrefixes[e.TargetID] == nil {
			declaredTypePrefixes[e.TargetID] = map[string]bool{}
		}
		declaredTypePrefixes[e.TargetID][declaredType+"."] = true
	}

	nodeByID := make(map[string]*model.Node, len(sg.Nodes))
	for i := range sg.Nodes {
		nodeByID[sg.Nodes[i].ID] = &sg.Nodes[i]
	}

	prunedDispatchEdges := map[string]bool{} // "sourceID→targetID" of pruned DISPATCHES edges
	for _, e := range sg.Edges {
		if e.Kind != model.RelDispatches {
			continue
		}
		prefixes, hasPrefixes := declaredTypePrefixes[e.SourceID]
		if !hasPrefixes {
			continue
		}
		targetQN := ""
		if n, ok := nodeByID[e.TargetID]; ok {
			targetQN, _ = n.Properties["qualified_name"].(string)
		}
		matched := false
		for prefix := range prefixes {
			if strings.HasPrefix(targetQN, prefix) {
				matched = true
				break
			}
		}
		if !matched {
			prunedDispatchEdges[e.SourceID+"→"+e.TargetID] = true
		}
	}

	// Step 3: Build non-pruned incoming edges count per node.
	// A node is excludable only if ALL its incoming edges are pruned DISPATCHES edges.
	incomingNonPruned := map[string]int{}
	for _, e := range sg.Edges {
		key := e.SourceID + "→" + e.TargetID
		if !prunedDispatchEdges[key] {
			incomingNonPruned[e.TargetID]++
		}
	}

	// Mark nodes with zero non-pruned incoming edges as excluded (they are only reachable via pruned DISPATCHES).
	excludedNodes := map[string]bool{}
	for _, e := range sg.Edges {
		key := e.SourceID + "→" + e.TargetID
		if prunedDispatchEdges[key] && incomingNonPruned[e.TargetID] == 0 {
			excludedNodes[e.TargetID] = true
		}
	}

	// Step 4: Cascade — excluded nodes' outgoing edges may create new orphans.
	changed := true
	for changed {
		changed = false
		// Recount incoming edges excluding edges from excluded sources
		cascadeIncoming := map[string]int{}
		for _, e := range sg.Edges {
			if excludedNodes[e.SourceID] || excludedNodes[e.TargetID] {
				continue
			}
			cascadeIncoming[e.TargetID]++
		}
		for _, e := range sg.Edges {
			if excludedNodes[e.SourceID] && !excludedNodes[e.TargetID] {
				if cascadeIncoming[e.TargetID] == 0 {
					excludedNodes[e.TargetID] = true
					changed = true
				}
			}
		}
	}

	// Step 5: Filter edges and nodes
	var edges []model.Edge
	for _, e := range sg.Edges {
		if !excludedNodes[e.SourceID] && !excludedNodes[e.TargetID] {
			edges = append(edges, e)
		}
	}
	var nodes []model.Node
	for _, n := range sg.Nodes {
		if !excludedNodes[n.ID] {
			nodes = append(nodes, n)
		}
	}
	return &model.Subgraph{Nodes: nodes, Edges: edges, TruncatedNodes: filterTruncatedNodes(sg.TruncatedNodes, excludedNodes, sg.Nodes)}
}
// logMethodNames is the set of method names considered as logging calls for dry mode filtering.
var logMethodNames = map[string]bool{
	"info": true, "warn": true, "error": true,
	"log": true, "debug": true, "trace": true,
}

// FilterDrySubgraph applies dry mode filtering on top of core: removes log methods,
// exception constructors, and trims verbose properties from nodes and edges.
// Orphan nodes (unreferenced after removal) are cleaned up.
func FilterDrySubgraph(sg *model.Subgraph) *model.Subgraph {
	if sg == nil {
		return sg
	}

	excluded := map[string]bool{}
	for _, n := range sg.Nodes {
		name, _ := n.Properties["name"].(string)
		// Log methods
		if logMethodNames[name] {
			excluded[n.ID] = true
			continue
		}
		// Exception/Error constructors
		isCtor, _ := n.Properties["is_constructor"].(bool)
		if isCtor {
			nameLower := strings.ToLower(name)
			if strings.Contains(nameLower, "exception") || strings.Contains(nameLower, "error") {
				excluded[n.ID] = true
				continue
			}
		}
	}

	// Inherited base class methods: if a CALLS edge has declared_type and the target's
	// qualified_name class differs from declared_type, the target is a base class method
	// reached via inheritance. Exclude it in dry mode.
	nodeByID := map[string]*model.Node{}
	for i := range sg.Nodes {
		nodeByID[sg.Nodes[i].ID] = &sg.Nodes[i]
	}
	for _, e := range sg.Edges {
		if e.Kind != model.RelCalls {
			continue
		}
		declaredType, _ := e.Properties["declared_type"].(string)
		if declaredType == "" {
			continue
		}
		targetNode := nodeByID[e.TargetID]
		if targetNode == nil {
			continue
		}
		targetQN, _ := targetNode.Properties["qualified_name"].(string)
		lastDot := strings.LastIndex(targetQN, ".")
		if lastDot < 0 {
			continue
		}
		targetClass := targetQN[:lastDot]
		if targetClass != declaredType {
			excluded[e.TargetID] = true
		}
	}

	// Cascade: excluded nodes' callees may become orphans.
	// Repeatedly check until stable.
	changed := true
	for changed {
		changed = false
		incomingCount := map[string]int{}
		for _, e := range sg.Edges {
			if excluded[e.SourceID] || excluded[e.TargetID] {
				continue
			}
			incomingCount[e.TargetID]++
		}
		for _, e := range sg.Edges {
			if excluded[e.SourceID] && !excluded[e.TargetID] {
				if incomingCount[e.TargetID] == 0 {
					excluded[e.TargetID] = true
					changed = true
				}
			}
		}
	}

	// Filter edges
	var edges []model.Edge
	for _, e := range sg.Edges {
		if excluded[e.SourceID] || excluded[e.TargetID] {
			continue
		}
		// Trim edge properties
		if e.Properties != nil {
			delete(e.Properties, "flow_context")
			delete(e.Properties, "flow_line")
		}
		edges = append(edges, e)
	}

	// Collect referenced nodes
	referenced := map[string]bool{}
	for _, e := range edges {
		referenced[e.SourceID] = true
		referenced[e.TargetID] = true
	}

	// Filter nodes: keep non-excluded + still-referenced, trim properties
	var nodes []model.Node
	for _, n := range sg.Nodes {
		if excluded[n.ID] {
			continue
		}
		// Orphan check: node must be referenced by an edge OR be the only node (root)
		if len(edges) > 0 && !referenced[n.ID] {
			excluded[n.ID] = true
			continue
		}
		// Trim node properties
		if n.Properties != nil {
			delete(n.Properties, "is_getter")
			delete(n.Properties, "is_setter")
		}
		nodes = append(nodes, n)
	}
	return &model.Subgraph{Nodes: nodes, Edges: edges, TruncatedNodes: filterTruncatedNodes(sg.TruncatedNodes, excluded, sg.Nodes)}
}

// CompactSubgraphEdges merges duplicate edges (same source_id + target_id + kind) into
// a single edge with a "lines" array. The "line" property is replaced by "lines" (sorted, deduplicated).
// Confidence is set to the maximum value among merged edges. Other properties are taken from the first edge.
func CompactSubgraphEdges(sg *model.Subgraph) *model.Subgraph {
	if sg == nil {
		return sg
	}

	type edgeKey struct {
		sourceID string
		targetID string
		kind     model.RelationKind
	}

	groups := map[edgeKey][]model.Edge{}
	order := []edgeKey{} // preserve insertion order
	for _, e := range sg.Edges {
		key := edgeKey{e.SourceID, e.TargetID, e.Kind}
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], e)
	}

	var edges []model.Edge
	for _, key := range order {
		group := groups[key]
		if len(group) == 1 {
			// Single edge: convert line to lines array for consistency
			e := group[0]
			if line, ok := e.Properties["line"]; ok {
				if lineNum, ok := toInt(line); ok {
					e.Properties["lines"] = []int{lineNum}
				}
				delete(e.Properties, "line")
			}
			edges = append(edges, e)
			continue
		}

		// Merge multiple edges
		merged := group[0]
		linesSet := map[int]bool{}
		maxConfidence := 0.0
		for _, e := range group {
			if line, ok := e.Properties["line"]; ok {
				if lineNum, ok := toInt(line); ok {
					linesSet[lineNum] = true
				}
			}
			if conf, ok := e.Properties["confidence"].(float64); ok && conf > maxConfidence {
				maxConfidence = conf
			}
		}

		// Build sorted lines array
		lines := make([]int, 0, len(linesSet))
		for lineNum := range linesSet {
			lines = append(lines, lineNum)
		}
		sort.Ints(lines)

		if merged.Properties == nil {
			merged.Properties = map[string]any{}
		}
		delete(merged.Properties, "line")
		merged.Properties["lines"] = lines
		merged.Properties["confidence"] = maxConfidence
		edges = append(edges, merged)
	}

	return &model.Subgraph{Nodes: sg.Nodes, Edges: edges, TruncatedNodes: sg.TruncatedNodes}
}

// toInt converts a numeric value to int, handling float64 (from JSON) and int.
func toInt(value any) (int, bool) {
	switch num := value.(type) {
	case int:
		return num, true
	case float64:
		return int(num), true
	case int64:
		return int(num), true
	}
	return 0, false
}

// ImpactAnalysis finds all symbols affected by changes to a given symbol.
func (querier *Querier) ImpactAnalysis(ctx context.Context, symbolName string, depth int) (*model.Subgraph, error) {
	node, candidates, inheritedFrom, err := querier.ResolveFunctionWithInheritance(ctx, symbolName)
	if err != nil {
		return nil, fmt.Errorf("querier: find symbol %q: %w", symbolName, err)
	}
	if node == nil && len(candidates) > 0 {
		var names []string
		for _, candidate := range candidates {
			qualifiedName, _ := candidate.Properties["qualified_name"].(string)
			names = append(names, qualifiedName)
		}
		return nil, fmt.Errorf("ambiguous: %d matches for %q. Candidates:\n%s\nPlease ask the user which one to use", len(candidates), symbolName, strings.Join(names, "\n"))
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

// QueryClassMembers returns all methods belonging to a class.
// Supports short name ("Store") and qualified name ("falkor.Store").
// Returns (methods, nil, nil) on unique match.
// Returns (nil, candidates, nil) when multiple classes match.
func (querier *Querier) QueryClassMembers(ctx context.Context, className string, limit int) ([]model.Node, []model.Node, []model.FieldInfo, string, error) {
	searchName := className
	if strings.Contains(className, ".") {
		searchName = className[strings.LastIndex(className, ".")+1:]
	}

	classNodes, err := querier.graphStore.QueryNodesByName(ctx, searchName, model.QueryOpts{
		Kinds: []string{constants.KindClass, constants.KindInterface},
		Limit: 20,
	})
	if err != nil {
		return nil, nil, nil, "", err
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
		return nil, nil, nil, "", nil
	}
	if len(classNodes) > 1 {
		return nil, classNodes, nil, "", nil
	}

	edges, err := querier.graphStore.QueryEdges(ctx, classNodes[0].ID, classNodes[0].Kind, model.RelContains, model.Outgoing)
	if err != nil {
		return nil, nil, nil, "", err
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
	
	// Parse fields from Class node property
	var fields []model.FieldInfo
	if fieldsJSON, ok := classNodes[0].Properties["fields"].(string); ok && fieldsJSON != "" && fieldsJSON != "null" {
		_ = json.Unmarshal([]byte(fieldsJSON), &fields)
	}
	return methods, nil, fields, classNodes[0].Kind, nil
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
					SQLText:    propString(node.Properties, "sql_text"),
					QueryType:  propString(node.Properties, "query_type"),
					Tables:     propString(node.Properties, "tables"),
					Caller:     propString(node.Properties, "caller"),
					BaseSQL:    propString(node.Properties, "base_sql"),
					Conditions: propString(node.Properties, "conditions"),
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
	exactMatches, err := querier.graphStore.QueryNodesByProperty(ctx, constants.KindRoute, "path_pattern", routePath, storage.MatchExact, 0)
	if err != nil {
		return nil, err
	}
	if node := filterByMethod(exactMatches, method); node != nil {
		return node, nil
	}

	// 2. Contains fallback
	containsMatches, err := querier.graphStore.QueryNodesByProperty(ctx, constants.KindRoute, "path_pattern", routePath, storage.MatchContains, 0)
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

	// Load only reachable data via BFS from handler
	handlerID := handles[0].SourceID
	subgraph, _ := querier.graphStore.TraverseCallChain(ctx, handlerID, maxDepth, model.Outgoing, 0)

	// Build funcMap from subgraph nodes + handler itself
	funcMap := make(map[string]*model.Node, len(subgraph.Nodes)+1)
	nodeIDs := make([]string, 0, len(subgraph.Nodes)+1)
	nodeIDs = append(nodeIDs, handlerID)
	for i := range subgraph.Nodes {
		funcMap[subgraph.Nodes[i].ID] = &subgraph.Nodes[i]
		nodeIDs = append(nodeIDs, subgraph.Nodes[i].ID)
	}
	if funcMap[handlerID] == nil {
		handlerNode, _ := querier.graphStore.QueryNodeByID(ctx, handlerID)
		if handlerNode != nil {
			funcMap[handlerID] = handlerNode
		}
	}

	// Build childrenMap from subgraph edges
	childrenMap := make(map[string][]string)
	for _, edge := range subgraph.Edges {
		childrenMap[edge.SourceID] = append(childrenMap[edge.SourceID], edge.TargetID)
	}

	// Batch query EXECUTES edges for reachable nodes
	execEdges, _ := querier.graphStore.QueryEdgesByNodeIDs(ctx, nodeIDs, model.RelExecutes, model.Outgoing)
	execMap := make(map[string][]string)
	var queryNodeIDs []string
	for _, edge := range execEdges {
		execMap[edge.SourceID] = append(execMap[edge.SourceID], edge.TargetID)
		queryNodeIDs = append(queryNodeIDs, edge.TargetID)
	}

	// Batch query QueryNode properties
	queryMap := make(map[string]*model.Node)
	if len(queryNodeIDs) > 0 {
		queryNodes, _ := querier.graphStore.QueryNodesByIDs(ctx, queryNodeIDs)
		for i := range queryNodes {
			queryMap[queryNodes[i].ID] = &queryNodes[i]
		}
	}

	// Build layer map for reachable nodes only
	layerMap := querier.buildLayerMapForNodes(ctx, funcMap)

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
		StartLine:     propInt(node.Properties, "start_line"),
		EndLine:       propInt(node.Properties, "end_line"),
		IsGetter:      propBool(node.Properties, "is_getter"),
		IsSetter:      propBool(node.Properties, "is_setter"),
	})

	callerName := propString(node.Properties, "qualified_name")
	for _, targetID := range execMap[nodeID] {
		if queryNode := queryMap[targetID]; queryNode != nil {
			sqlText := propString(queryNode.Properties, "sql_text")
			queryType := propString(queryNode.Properties, "query_type")
			tablesStr := propString(queryNode.Properties, "tables")
			baseSQL := propString(queryNode.Properties, "base_sql")
			conditionsStr := propString(queryNode.Properties, "conditions")
			name := propString(queryNode.Properties, "name")
			if name == "" {
				name = sqlText
			}

			chainNode := model.ChainNode{
				ID:            queryNode.ID,
				Name:          name,
				Kind:          constants.KindQueryNode,
				FilePath:      propString(queryNode.Properties, "file_path"),
				QualifiedName: callerName,
				SQLText:       sqlText,
				QueryType:     queryType,
				BaseSQL:       baseSQL,
			}
			if tablesStr != "" {
				tablesArray := strings.Split(tablesStr, ",")
				if tablesJSON, err := json.Marshal(tablesArray); err == nil {
					chainNode.Tables = tablesJSON
				}
			}
			if conditionsStr != "" {
				chainNode.Conditions = json.RawMessage(conditionsStr)
			}
			chain.Queries = append(chain.Queries, chainNode)
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


// buildLayerMapForNodes builds a layer map for a specific set of function nodes.
func (querier *Querier) buildLayerMapForNodes(ctx context.Context, funcMap map[string]*model.Node) map[string]string {
	layerMap := map[string]string{}
	fileNodes := map[string][]string{} // filePath → []nodeID
	for id, node := range funcMap {
		filePath := propString(node.Properties, "file_path")
		if filePath != "" {
			fileNodes[filePath] = append(fileNodes[filePath], id)
		}
		edges, err := querier.graphStore.QueryEdges(ctx, id, constants.KindFunction, model.RelHasAnnotation, model.Outgoing)
		if err != nil {
			continue
		}
		for _, edge := range edges {
			ann, err := querier.graphStore.QueryNodeByID(ctx, edge.TargetID)
			if err != nil || ann == nil {
				continue
			}
			if layer := propString(ann.Properties, "layer"); layer != "" {
				layerMap[id] = layer
				break
			}
		}
	}
	for filePath, nodeIDs := range fileNodes {
		fileClasses, _ := querier.graphStore.QueryNodesByProperty(ctx, constants.KindClass, "file_path", filePath, storage.MatchExact, 0)
		for _, cls := range fileClasses {
			edges, _ := querier.graphStore.QueryEdges(ctx, cls.ID, constants.KindClass, model.RelHasAnnotation, model.Outgoing)
			for _, edge := range edges {
				ann, _ := querier.graphStore.QueryNodeByID(ctx, edge.TargetID)
				if ann == nil {
					continue
				}
				if layer := propString(ann.Properties, "layer"); layer != "" {
					for _, nodeID := range nodeIDs {
						if _, exists := layerMap[nodeID]; !exists {
							layerMap[nodeID] = layer
						}
					}
					break
				}
			}
		}
	}
	return layerMap
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

// CrossChainResult represents project-level cross-service call relationships.
type CrossChainResult struct {
	Entry             CrossChainEntry      `json:"entry"`
	CrossServiceCalls []CrossServiceCall   `json:"cross_service_calls"`
	Summary           CrossChainSummary    `json:"summary"`
}

type CrossChainEntry struct {
	Function string `json:"function"`
	Project  string `json:"project"`
	FilePath string `json:"file_path"`
}

type CrossServiceCall struct {
	TargetProject string               `json:"target_project,omitempty"`
	TargetBranch  string               `json:"target_branch,omitempty"`
	TargetService string               `json:"target_service,omitempty"`
	Protocol      string               `json:"protocol,omitempty"`
	Routes        []CrossServiceRoute  `json:"routes,omitempty"`
	Callers       []CrossServiceCaller `json:"callers"`
	Hint          string               `json:"hint,omitempty"`
}

type CrossServiceRoute struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Handler string `json:"handler"`
}

type CrossServiceCaller struct {
	Function string `json:"function"`
	Type     string `json:"type"` // feign / resttemplate / dubbo / grpc
}

type CrossChainSummary struct {
	TotalCrossService int      `json:"total_cross_service"`
	Resolved          int      `json:"resolved"`
	Unresolved        int      `json:"unresolved"`
	Protocols         []string `json:"protocols"`
}

// QueryCrossChain returns project-level cross-service call relationships for a function.
// Aggregates cross_service CALLS edges and REMOTE_CALLS_EXT edges by target project/service.
func (querier *Querier) QueryCrossChain(ctx context.Context, functionName string, depth int, projectPath string) (*CrossChainResult, error) {
	// Resolve function
	node, _, _, err := querier.ResolveFunctionWithInheritance(ctx, functionName)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, fmt.Errorf("function %q not found", functionName)
	}

	// Get call chain
	subgraph, err := querier.QueryCallChainByNodeID(ctx, node.ID, model.Outgoing, depth, 0)
	if err != nil {
		return nil, err
	}

	// Build entry
	entryName, _ := node.Properties["qualified_name"].(string)
	if entryName == "" {
		entryName, _ = node.Properties["name"].(string)
	}
	entryFilePath, _ := node.Properties["file_path"].(string)

	result := &CrossChainResult{
		Entry: CrossChainEntry{
			Function: entryName,
			Project:  filepath.Base(projectPath),
			FilePath: entryFilePath,
		},
	}

	// Aggregate cross-service calls by target project
	type aggregationKey struct {
		targetProject string
		targetService string
		protocol      string
	}
	aggregated := make(map[aggregationKey]*CrossServiceCall)

	// Build node map for lookups
	nodeMap := make(map[string]*model.Node)
	for i := range subgraph.Nodes {
		nodeMap[subgraph.Nodes[i].ID] = &subgraph.Nodes[i]
	}

	// Process CALLS edges with cross_service=true
	for _, edge := range subgraph.Edges {
		crossService, _ := edge.Properties["cross_service"].(bool)
		if !crossService {
			continue
		}

		targetProject, _ := edge.Properties["target_project"].(string)
		targetHandler, _ := edge.Properties["target_handler"].(string)
		viaRoute, _ := edge.Properties["via_route"].(string)
		protocol, _ := edge.Properties["protocol"].(string)
		if protocol == "" {
			protocol = "http"
		}

		targetService := targetHandler
		if targetProject == "" {
			targetService, _ = edge.Properties["target_service"].(string)
		}

		key := aggregationKey{targetProject: targetProject, targetService: targetService, protocol: protocol}
		call, exists := aggregated[key]
		if !exists {
			call = &CrossServiceCall{
				TargetProject: targetProject,
				TargetBranch:  "",
				TargetService: targetService,
				Protocol:      protocol,
			}
			if targetBranch, ok := edge.Properties["target_branch"].(string); ok {
				call.TargetBranch = targetBranch
			}
			if targetProject == "" {
				call.Hint = fmt.Sprintf("Target project unknown for %s (%s). Configure dependencies to enable cross-project matching.", targetService, protocol)
			}
			aggregated[key] = call
		}

		// Add route info
		if viaRoute != "" {
			parts := strings.SplitN(viaRoute, " ", 2)
			if len(parts) == 2 {
				call.Routes = append(call.Routes, CrossServiceRoute{
					Method:  parts[0],
					Path:    parts[1],
					Handler: targetHandler,
				})
			}
		}

		// Add caller info
		sourceNode := nodeMap[edge.SourceID]
		if sourceNode != nil {
			callerName, _ := sourceNode.Properties["qualified_name"].(string)
			if callerName == "" {
				callerName, _ = sourceNode.Properties["name"].(string)
			}
			callerType := protocol
			consumerInterface, _ := edge.Properties["consumer_interface"].(string)
			if strings.Contains(strings.ToLower(consumerInterface), "feign") {
				callerType = "feign"
			} else if strings.Contains(strings.ToLower(consumerInterface), "resttemplate") || strings.Contains(strings.ToLower(consumerInterface), "rest") {
				callerType = "resttemplate"
			}
			call.Callers = append(call.Callers, CrossServiceCaller{
				Function: callerName,
				Type:     callerType,
			})
		}
	}

	// Also check REMOTE_CALLS_EXT edges for protocol info on unresolved services
	allNodeIDs := make([]string, 0, len(subgraph.Nodes)+1)
	allNodeIDs = append(allNodeIDs, node.ID)
	for _, subNode := range subgraph.Nodes {
		allNodeIDs = append(allNodeIDs, subNode.ID)
	}

	// Build result list
	protocolSet := make(map[string]bool)
	resolved := 0
	unresolved := 0
	for _, call := range aggregated {
		result.CrossServiceCalls = append(result.CrossServiceCalls, *call)
		protocolSet[call.Protocol] = true
		if call.TargetProject != "" {
			resolved++
		} else {
			unresolved++
		}
	}

	var protocols []string
	for protocol := range protocolSet {
		protocols = append(protocols, protocol)
	}
	sort.Strings(protocols)

	result.Summary = CrossChainSummary{
		TotalCrossService: len(aggregated),
		Resolved:          resolved,
		Unresolved:        unresolved,
		Protocols:         protocols,
	}

	return result, nil
}
