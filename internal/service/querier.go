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

// QuerySymbol finds symbols by name with pagination.
func (querier *Querier) QuerySymbol(ctx context.Context, name string, opts model.QueryOpts) ([]model.Node, int, error) {
	allNodes, err := querier.graphStore.QueryNodesByName(ctx, name, model.QueryOpts{
		Kinds:         opts.Kinds,
		MinConfidence: opts.MinConfidence,
	})
	if err != nil {
		return nil, 0, err
	}
	page, total := paginate(allNodes, opts.Offset, opts.Limit)
	return page, total, nil
}

// QueryByAnnotation returns symbols that have a specific annotation.
// When params is non-empty, only annotations whose params contain the given substring are matched.
func (querier *Querier) QueryByAnnotation(ctx context.Context, annotation string, params string, kind string, limit, offset int) ([]model.Node, int, error) {
	annotationNodes, err := querier.graphStore.QueryNodesByName(ctx, annotation, model.QueryOpts{Kinds: []string{constants.KindAnnotation}})
	if err != nil {
		return nil, 0, err
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
		return []model.Node{}, 0, nil
	}
	allNodes, err := querier.resolveAnnotatedNodes(ctx, matchedAnnotationIDs, 0)
	if err != nil {
		return nil, 0, err
	}
	if kind != "" {
		filtered := allNodes[:0]
		for _, node := range allNodes {
			if node.Kind == kind {
				filtered = append(filtered, node)
			}
		}
		allNodes = filtered
	}
	page, total := paginate(allNodes, offset, limit)
	return page, total, nil
}

// QueryByLayer returns symbols annotated with a specific layer (controller/service/repository/model).
func (querier *Querier) QueryByLayer(ctx context.Context, layer string, limit, offset int) ([]model.Node, int, error) {
	annotations, err := querier.graphStore.QueryNodesByProperty(ctx, constants.KindAnnotation, "layer", layer, storage.MatchExact, 0)
	if err != nil {
		return nil, 0, err
	}
	annotationIDs := make([]string, len(annotations))
	for idx, annotation := range annotations {
		annotationIDs[idx] = annotation.ID
	}
	allNodes, err := querier.resolveAnnotatedNodes(ctx, annotationIDs, 0)
	if err != nil {
		return nil, 0, err
	}
	page, total := paginate(allNodes, offset, limit)
	return page, total, nil
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

// CollectQueries finds SQL queries executed by the given nodes via EXECUTES edges.
// Must be called before mode filtering to capture all queries (queries are not mode-filtered).
func (querier *Querier) CollectQueries(ctx context.Context, nodes []model.Node) ([]model.ChainNode, error) {
	nodeIDs := make([]string, len(nodes))
	nodeMap := make(map[string]*model.Node, len(nodes))
	for i := range nodes {
		nodeIDs[i] = nodes[i].ID
		nodeMap[nodes[i].ID] = &nodes[i]
	}

	execEdges, err := querier.graphStore.QueryEdgesByNodeIDs(ctx, nodeIDs, model.RelExecutes, model.Outgoing)
	if err != nil || len(execEdges) == 0 {
		return nil, err
	}

	var queryNodeIDs []string
	executesMap := make(map[string][]string)
	for _, edge := range execEdges {
		executesMap[edge.SourceID] = append(executesMap[edge.SourceID], edge.TargetID)
		queryNodeIDs = append(queryNodeIDs, edge.TargetID)
	}

	queryNodes, err := querier.graphStore.QueryNodesByIDs(ctx, queryNodeIDs)
	if err != nil {
		return nil, err
	}
	queryNodeMap := make(map[string]*model.Node, len(queryNodes))
	for i := range queryNodes {
		queryNodeMap[queryNodes[i].ID] = &queryNodes[i]
	}

	var queries []model.ChainNode
	for callerID, targetIDs := range executesMap {
		callerName := ""
		if caller := nodeMap[callerID]; caller != nil {
			callerName = propString(caller.Properties, "qualified_name")
		}
		for _, targetID := range targetIDs {
			queryNode := queryNodeMap[targetID]
			if queryNode == nil {
				continue
			}
			sqlText := propString(queryNode.Properties, "sql_text")
			name := propString(queryNode.Properties, "name")
			if name == "" {
				name = sqlText
			}
			entry := model.ChainNode{
				ID:            queryNode.ID,
				Name:          name,
				Kind:          constants.KindQueryNode,
				FilePath:      propString(queryNode.Properties, "file_path"),
				QualifiedName: callerName,
				SQLText:       sqlText,
				QueryType:     propString(queryNode.Properties, "query_type"),
				BaseSQL:       propString(queryNode.Properties, "base_sql"),
			}
			if tablesStr := propString(queryNode.Properties, "tables"); tablesStr != "" {
				tablesArray := strings.Split(tablesStr, ",")
				if tablesJSON, err := json.Marshal(tablesArray); err == nil {
					entry.Tables = tablesJSON
				}
			}
			if conditionsStr := propString(queryNode.Properties, "conditions"); conditionsStr != "" {
				entry.Conditions = json.RawMessage(conditionsStr)
			}
			queries = append(queries, entry)
		}
	}
	return queries, nil
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


// AssembleChainNodes groups chained call edges (chain_id > 0) into Chain virtual nodes.
// Only used in mode=full. Replaces multiple caller→chainStep edges with a single
// caller→chain:N edge, and creates a Chain node with steps/qualified_steps/lambda_bindings.
func AssembleChainNodes(sg *model.Subgraph) *model.Subgraph {
	if sg == nil {
		return sg
	}

	// Build node lookup for qualified_name
	nodeByID := map[string]model.Node{}
	for _, node := range sg.Nodes {
		nodeByID[node.ID] = node
	}

	// Group edges by (source_id, chain_id) — each group is one chain from one caller
	type chainKey struct {
		sourceID string
		chainID  int
	}
	chainGroups := map[chainKey][]model.Edge{}
	var nonChainEdges []model.Edge

	for _, edge := range sg.Edges {
		chainID, _ := model.ToInt(edge.Properties["chain_id"])
		if chainID > 0 {
			// Skip lambda pre-resolved edges — they belong to the lambda, not the chain
			targetNode, exists := nodeByID[edge.TargetID]
			isLambdaTarget := false
			if exists {
				qualifiedName, _ := targetNode.Properties["qualified_name"].(string)
				isLambdaTarget = strings.Contains(qualifiedName, ".lambda$")
			}
			if !isLambdaTarget {
				key := chainKey{sourceID: edge.SourceID, chainID: chainID}
				chainGroups[key] = append(chainGroups[key], edge)
			} else {
				nonChainEdges = append(nonChainEdges, edge)
			}
		} else {
			nonChainEdges = append(nonChainEdges, edge)
		}
	}

	if len(chainGroups) == 0 {
		return sg
	}

	// For each chain group, create a Chain virtual node
	var chainNodes []model.Node
	var chainEdges []model.Edge
	consumedNodeIDs := map[string]bool{}

	for key, edges := range chainGroups {
		// Sort by chain_depth ascending (innermost first)
		sortEdgesByChainDepth(edges)

		// Build steps and qualified_steps
		var steps []string
		var qualifiedSteps []string
		lambdaBindings := map[string]string{}

		for _, edge := range edges {
			targetNode, exists := nodeByID[edge.TargetID]
			if !exists {
				continue
			}
			name, _ := targetNode.Properties["name"].(string)
			qualifiedName, _ := targetNode.Properties["qualified_name"].(string)
			steps = append(steps, name)
			qualifiedSteps = append(qualifiedSteps, qualifiedName)
			consumedNodeIDs[edge.TargetID] = true
		}

		// Find lambda bindings: lambda edges from same source with matching chain_depth
		for _, edge := range nonChainEdges {
			if edge.SourceID != key.sourceID {
				continue
			}
			targetNode, exists := nodeByID[edge.TargetID]
			if !exists {
				continue
			}
			qualifiedName, _ := targetNode.Properties["qualified_name"].(string)
			if !strings.Contains(qualifiedName, ".lambda$") {
				continue
			}
			// Match lambda to chain step by chain_depth
			lambdaChainDepth, hasDepth := model.ToInt(edge.Properties["chain_depth"])
			if !hasDepth {
				continue
			}
			for _, chainEdge := range edges {
				chainEdgeDepth, _ := model.ToInt(chainEdge.Properties["chain_depth"])
				if lambdaChainDepth == chainEdgeDepth {
					chainTargetNode := nodeByID[chainEdge.TargetID]
					stepName, _ := chainTargetNode.Properties["name"].(string)
					lambdaName, _ := targetNode.Properties["name"].(string)
					if stepName != "" && lambdaName != "" {
						lambdaBindings[stepName] = lambdaName
					}
					break
				}
			}
		}

		// Create Chain virtual node
		chainNodeID := fmt.Sprintf("chain:%d", key.chainID)
		chainNodeProps := map[string]any{
			"steps":           steps,
			"qualified_steps": qualifiedSteps,
		}
		if len(lambdaBindings) > 0 {
			chainNodeProps["lambda_bindings"] = lambdaBindings
		}
		chainNodes = append(chainNodes, model.Node{
			ID:         chainNodeID,
			Kind:       "Chain",
			Properties: chainNodeProps,
		})

		// Create single edge from caller to Chain node
		chainEdges = append(chainEdges, model.Edge{
			SourceID:   key.sourceID,
			TargetID:   chainNodeID,
			Kind:       model.RelCalls,
			Properties: map[string]any{"line": key.chainID},
		})
	}

	// Rebuild nodes: keep non-consumed nodes + add chain nodes
	var finalNodes []model.Node
	for _, node := range sg.Nodes {
		if !consumedNodeIDs[node.ID] {
			finalNodes = append(finalNodes, node)
		}
	}
	finalNodes = append(finalNodes, chainNodes...)

	// Rebuild edges: non-chain edges (excluding edges to consumed nodes) + chain edges
	var finalEdges []model.Edge
	for _, edge := range nonChainEdges {
		if !consumedNodeIDs[edge.TargetID] && !consumedNodeIDs[edge.SourceID] {
			finalEdges = append(finalEdges, edge)
		}
	}
	finalEdges = append(finalEdges, chainEdges...)

	return &model.Subgraph{Nodes: finalNodes, Edges: finalEdges, TruncatedNodes: sg.TruncatedNodes}
}

// sortEdgesByChainDepth sorts edges by chain_depth ascending (innermost=0 first).
func sortEdgesByChainDepth(edges []model.Edge) {
	for i := 1; i < len(edges); i++ {
		for j := i; j > 0; j-- {
			depthJ, _ := model.ToInt(edges[j].Properties["chain_depth"])
			depthJMinus1, _ := model.ToInt(edges[j-1].Properties["chain_depth"])
			if depthJ < depthJMinus1 {
				edges[j], edges[j-1] = edges[j-1], edges[j]
			}
		}
	}
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
	// reached via inheritance. Only exclude it if the subgraph already contains a
	// DISPATCHES edge from this target to an override matching the declared_type,
	// meaning the real implementation is already present.
	nodeByID := map[string]*model.Node{}
	for i := range sg.Nodes {
		nodeByID[sg.Nodes[i].ID] = &sg.Nodes[i]
	}
	dispatchTargetsBySource := map[string][]string{}
	for _, e := range sg.Edges {
		if e.Kind == model.RelDispatches {
			dispatchTargetsBySource[e.SourceID] = append(dispatchTargetsBySource[e.SourceID], e.TargetID)
		}
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
		if targetClass == declaredType {
			continue
		}
		hasOverrideInSubgraph := false
		for _, dispatchTargetID := range dispatchTargetsBySource[e.TargetID] {
			if dispatchNode := nodeByID[dispatchTargetID]; dispatchNode != nil {
				if dispatchQN, _ := dispatchNode.Properties["qualified_name"].(string); strings.HasPrefix(dispatchQN, declaredType+".") {
					hasOverrideInSubgraph = true
					break
				}
			}
		}
		if hasOverrideInSubgraph {
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

// SearchFTS performs full-text search with pagination.
func (querier *Querier) SearchFTS(ctx context.Context, query string, limit, offset int) ([]storage.SearchResult, int, error) {
	allResults, err := querier.graphStore.SearchFTS(ctx, query, 0)
	if err != nil {
		return nil, 0, err
	}
	page, total := paginate(allResults, offset, limit)
	return page, total, nil
}

// Overview returns project statistics.
func (querier *Querier) Overview(ctx context.Context) (*model.GraphStats, error) {
	return querier.graphStore.GetStats(ctx)
}

// QueryEdges returns edges connected to a node, filtered by kind and direction.
func (querier *Querier) QueryEdges(ctx context.Context, nodeID, nodeKind string, relKind model.RelationKind, direction model.Direction) ([]model.Edge, error) {
	return querier.graphStore.QueryEdges(ctx, nodeID, nodeKind, relKind, direction)
}

// paginate slices a full result set into a single page.
// offset: number of items to skip; limit: max items to return (0 = no limit).
func paginate[T any](all []T, offset, limit int) (page []T, total int) {
	total = len(all)
	if offset >= total {
		return []T{}, total
	}
	end := total
	if limit > 0 && offset+limit < total {
		end = offset + limit
	}
	return all[offset:end], total
}

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
// Returns a graph structure (nodes + edges) instead of a flat chain, with layer injected into Node.Properties.
func (querier *Querier) QueryRouteChain(ctx context.Context, routePath string, method string, maxDepth int) (*model.RouteChain, error) {
	if maxDepth <= 0 {
		maxDepth = 10
	}

	routeNode, err := querier.findRouteNode(ctx, routePath, method)
	if err != nil {
		return nil, err
	}

	result := &model.RouteChain{
		Route:  propString(routeNode.Properties, "path_pattern"),
		Method: propString(routeNode.Properties, "method"),
	}

	// HANDLES edges: Route <- Function (may be multiple with handler_order)
	handles, err := querier.graphStore.QueryEdges(ctx, routeNode.ID, constants.KindRoute, model.RelHandles, model.Incoming)
	if err != nil || len(handles) == 0 {
		return result, nil
	}

	// Sort by handler_order ascending; last one is the business handler
	sort.Slice(handles, func(i, j int) bool {
		return propInt(handles[i].Properties, "handler_order") < propInt(handles[j].Properties, "handler_order")
	})

	// Business handler = last HANDLES edge
	handlerID := handles[len(handles)-1].SourceID

	// Middleware nodes = all HANDLES edges except the last
	for _, handle := range handles[:len(handles)-1] {
		middlewareNode, _ := querier.graphStore.QueryNodeByID(ctx, handle.SourceID)
		if middlewareNode != nil {
			result.Middlewares = append(result.Middlewares, *middlewareNode)
		}
	}

	subgraph, _ := querier.graphStore.TraverseCallChain(ctx, handlerID, maxDepth, model.Outgoing, 0)

	// Ensure handler node is in subgraph (TraverseCallChain doesn't include the start node)
	handlerInSubgraph := false
	for _, node := range subgraph.Nodes {
		if node.ID == handlerID {
			handlerInSubgraph = true
			break
		}
	}
	if !handlerInSubgraph {
		handlerNode, _ := querier.graphStore.QueryNodeByID(ctx, handlerID)
		if handlerNode != nil {
			subgraph.Nodes = append([]model.Node{*handlerNode}, subgraph.Nodes...)
		}
	}

	// Inject layer into Node.Properties
	funcMap := make(map[string]*model.Node, len(subgraph.Nodes))
	for i := range subgraph.Nodes {
		funcMap[subgraph.Nodes[i].ID] = &subgraph.Nodes[i]
	}
	layerMap := querier.buildLayerMapForNodes(ctx, funcMap)
	for i := range subgraph.Nodes {
		if layer, ok := layerMap[subgraph.Nodes[i].ID]; ok {
			if subgraph.Nodes[i].Properties == nil {
				subgraph.Nodes[i].Properties = make(map[string]any)
			}
			subgraph.Nodes[i].Properties["layer"] = layer
		}
	}

	// Collect SQL queries via EXECUTES edges
	queries, _ := querier.CollectQueries(ctx, subgraph.Nodes)

	result.Nodes = subgraph.Nodes
	result.Edges = subgraph.Edges
	result.Queries = queries
	return result, nil
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

// QueryUsages returns all references to the given enum constant via USES edges.
func (querier *Querier) QueryUsages(ctx context.Context, symbolQualifiedName string, limit, offset int) ([]model.UsageResult, int, error) {
	// Step 1: Find the Variable node by qualified_name
	variableNode, err := querier.graphStore.QueryNodeByQualifiedName(ctx, symbolQualifiedName)
	if err != nil {
		return nil, 0, err
	}
	if variableNode == nil {
		return nil, 0, fmt.Errorf("constant not found: %s", symbolQualifiedName)
	}
	if variableNode.Kind != constants.KindVariable {
		return nil, 0, fmt.Errorf("symbol is not a variable: %s (kind: %s)", symbolQualifiedName, variableNode.Kind)
	}

	// Step 2: Query incoming USES edges from Function nodes
	edges, err := querier.graphStore.QueryEdges(ctx, variableNode.ID, constants.KindFunction, model.RelUses, model.Incoming)
	if err != nil {
		return nil, 0, err
	}
	if len(edges) == 0 {
		return []model.UsageResult{}, 0, nil
	}

	// Step 3: Extract source node IDs and query Function nodes
	sourceIDs := make([]string, 0, len(edges))
	edgeMap := make(map[string]model.Edge)
	for _, edge := range edges {
		sourceIDs = append(sourceIDs, edge.SourceID)
		edgeMap[edge.SourceID] = edge
	}

	functionNodes, err := querier.graphStore.QueryNodesByIDs(ctx, sourceIDs)
	if err != nil {
		return nil, 0, err
	}

	// Step 4: Assemble UsageResult with edge properties
	allResults := make([]model.UsageResult, 0, len(functionNodes))
	for _, functionNode := range functionNodes {
		edge, ok := edgeMap[functionNode.ID]
		if !ok {
			continue
		}
		refLine, _ := model.ToInt(edge.Properties["line"])
		refKind, _ := edge.Properties["ref_kind"].(string)
		allResults = append(allResults, model.UsageResult{
			Function: functionNode,
			RefLine:  refLine,
			RefKind:  refKind,
		})
	}

	// Step 5: In-memory pagination
	page, total := paginate(allResults, offset, limit)
	return page, total, nil
}
