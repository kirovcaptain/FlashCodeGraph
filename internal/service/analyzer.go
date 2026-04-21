package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/annotation"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
)

// Analyzer performs graph analysis: entry point detection, process tracing.
type Analyzer struct {
	graphStore storage.GraphStore
	progress   *ProgressManager
}

// NewAnalyzer creates an Analyzer.
func NewAnalyzer(graphStore storage.GraphStore, callback ...model.ProgressCallback) *Analyzer {
	a := &Analyzer{graphStore: graphStore}
	if len(callback) > 0 && callback[0] != nil {
		a.progress = NewProgressManager(callback[0])
	}
	return a
}

// SetAnalyzeMode configures progress phases for the given scope.
func (analyzer *Analyzer) SetAnalyzeMode(entriesOnly bool) {
	if analyzer.progress == nil {
		return
	}
	if entriesOnly {
		analyzer.progress.SetMode(analyzeEntriesOnlyPhaseList)
	} else {
		analyzer.progress.SetMode(analyzePhaseList)
	}
}

// CallForest represents the call graph as a forest of trees rooted at entry points.
type CallForest struct {
	InDegree  map[string]int
	OutDegree map[string]int
	Children  map[string][]CallEdge // nodeID → outgoing calls
	Nodes     map[string]model.Node // nodeID → node
}

// CallEdge represents a call relationship with confidence.
type CallEdge struct {
	TargetID   string
	Confidence float64
}

// EntryPoint represents a detected entry point.
type EntryPoint struct {
	NodeID        string
	Name          string
	QualifiedName string
	FilePath      string
	EntryType     string
	Score         float64
	RouteMethod   string // HTTP method (GET/POST/...) for http_endpoint
	RoutePath     string // URL path for http_endpoint
}

// ProcessStep represents a step in an execution process.
type ProcessStep struct {
	Order      int
	NodeID     string
	Name       string
	FilePath   string
	Layer      string
	Confidence float64
	Depth      int
	IsGetter   bool // accessor getter (for CLI core mode folding)
	IsSetter   bool // accessor setter (for CLI core mode folding)
	Children   []*ProcessStep
}

// AnalyzeResult holds the results of analysis.
type AnalyzeResult struct {
	Entries    []EntryPoint
	Processes  []*ProcessStep
	ForestInfo struct {
		TotalEdges    int
		TotalRoots    int
		SuspectedDead int
	}
}

// BuildCallForest loads all CALLS edges and builds in-memory call forest.
func (analyzer *Analyzer) BuildCallForest(ctx context.Context) (*CallForest, error) {
	analyzer.progress.Emit(PhaseCallForest, 0, 0, "")
	edges, err := analyzer.graphStore.QueryAllEdges(ctx, model.RelCalls, 0)
	if err != nil {
		return nil, fmt.Errorf("analyzer: query CALLS edges: %w", err)
	}

	forest := &CallForest{
		InDegree:  make(map[string]int),
		OutDegree: make(map[string]int),
		Children:  make(map[string][]CallEdge),
		Nodes:     make(map[string]model.Node),
	}

	for _, edge := range edges {
		forest.InDegree[edge.TargetID]++
		forest.OutDegree[edge.SourceID]++
		confidence := 0.0
		if c, ok := edge.Properties["confidence"]; ok {
			if cf, ok := c.(float64); ok {
				confidence = cf
			}
		}
		forest.Children[edge.SourceID] = append(forest.Children[edge.SourceID], CallEdge{
			TargetID:   edge.TargetID,
			Confidence: confidence,
		})
		// Ensure all nodes appear in maps
		if _, ok := forest.InDegree[edge.SourceID]; !ok {
			forest.InDegree[edge.SourceID] = 0
		}
		if _, ok := forest.OutDegree[edge.TargetID]; !ok {
			forest.OutDegree[edge.TargetID] = 0
		}
	}

	// Preload all Function nodes for fast lookup in traceDFS
	funcs, err := analyzer.graphStore.QueryAllByKind(ctx, constants.KindFunction, 0)
	if err == nil {
		for _, f := range funcs {
			forest.Nodes[f.ID] = f
		}
	}

	totalEdges := 0
	for _, children := range forest.Children {
		totalEdges += len(children)
	}
	analyzer.progress.Emit(PhaseCallForest, 0, 0, fmt.Sprintf("%d edges, %d nodes", totalEdges, len(forest.InDegree)))

	return forest, nil
}
func (analyzer *Analyzer) ClassifyRoots(ctx context.Context, forest *CallForest) ([]EntryPoint, error) {
	// Phase: Load metadata
	analyzer.progress.Emit(PhaseLoadMetadata, 0, 0, "")

	// Load HANDLES edges + batch-load Route nodes
	analyzer.progress.EmitSub(PhaseLoadMetadata, SubRouteHandlers, "")
	handlesEdges, _ := analyzer.graphStore.QueryAllEdges(ctx, model.RelHandles, 0)
	handlerSet := make(map[string]bool, len(handlesEdges))
	type routeInfo struct{ Method, Path, Framework string }
	handlerRoute := make(map[string]routeInfo)

	// Batch-load all Route nodes to avoid N+1 queries
	routeNodes, _ := analyzer.graphStore.QueryAllByKind(ctx, "Route", 0)
	routeNodeMap := make(map[string]*model.Node, len(routeNodes))
	for i := range routeNodes {
		routeNodeMap[routeNodes[i].ID] = &routeNodes[i]
	}

	// Build set of @RestController class names
	allClasses, _ := analyzer.graphStore.QueryAllByKind(ctx, constants.KindClass, 0)
	restControllerClasses := make(map[string]bool)
	for _, cls := range allClasses {
		anns, _ := cls.Properties["annotations"].(string)
		if strings.Contains(anns, "RestController") || strings.Contains(anns, "Controller") {
			restControllerClasses[nodeProperty(&cls, "name")] = true
		}
	}

	// Single pass over handlesEdges: build handlerSet, handlerRoute, remoteClientSet
	remoteClientSet := make(map[string]bool)
	for _, edge := range handlesEdges {
		handlerSet[edge.SourceID] = true
		routeNode := routeNodeMap[edge.TargetID]
		if routeNode == nil {
			continue
		}
		method, _ := routeNode.Properties["method"].(string)
		path, _ := routeNode.Properties["path_pattern"].(string)
		fw, _ := routeNode.Properties["framework"].(string)
		handlerRoute[edge.SourceID] = routeInfo{method, path, fw}

		if fw == "feign" || fw == "grpc" {
			// Check if handler's class is a RestController (then it's an endpoint, not a client)
			handlerNode := forest.Nodes[edge.SourceID]
			isRestController := false
			if handlerNode.ID != "" {
				qn := nodeProperty(&handlerNode, "qualified_name")
				parts := strings.Split(qn, ".")
				if len(parts) >= 2 {
					isRestController = restControllerClasses[parts[len(parts)-2]]
				}
			}
			if !isRestController {
				remoteClientSet[edge.SourceID] = true
			}
		}
	}
	analyzer.progress.EmitSub(PhaseLoadMetadata, SubRouteHandlers, fmt.Sprintf("%d handlers", len(handlerSet)))

	// Load HAS_ANNOTATION edges for annotation-based classification
	analyzer.progress.EmitSub(PhaseLoadMetadata, SubAnnotations, "")
	annotationEdges, _ := analyzer.graphStore.QueryAllEdges(ctx, model.RelHasAnnotation, 0)
	nodeAnnotations := make(map[string][]string)
	for _, e := range annotationEdges {
		nodeAnnotations[e.SourceID] = append(nodeAnnotations[e.SourceID], e.TargetID)
	}
	analyzer.progress.EmitSub(PhaseLoadMetadata, SubAnnotations, fmt.Sprintf("%d edges", len(annotationEdges)))

	// Load IMPLEMENTS edges to identify interface implementations
	analyzer.progress.EmitSub(PhaseLoadMetadata, SubImplementations, "")
	implEdges, _ := analyzer.graphStore.QueryAllEdges(ctx, model.RelImplements, 0)
	implStructNames := make(map[string]bool)
	// Build class ID → name map from already-loaded classes
	classNameByID := make(map[string]string, len(allClasses))
	for _, cls := range allClasses {
		classNameByID[cls.ID] = nodeProperty(&cls, "name")
	}
	for _, edge := range implEdges {
		if name, ok := classNameByID[edge.SourceID]; ok {
			implStructNames[name] = true
		}
	}
	analyzer.progress.EmitSub(PhaseLoadMetadata, SubImplementations, fmt.Sprintf("%d edges", len(implEdges)))
	analyzer.progress.Emit(PhaseLoadMetadata, 0, 0, "routes, annotations, inheritance")

	// Find roots: in-degree=0 from forest + handler functions not in forest
	analyzer.progress.Emit(PhaseClassifyRoots, 0, 0, "")
	rootIDs := make(map[string]bool)
	for nodeID, inDeg := range forest.InDegree {
		if inDeg == 0 {
			rootIDs[nodeID] = true
		}
	}
	for nodeID := range handlerSet {
		if _, inForest := forest.InDegree[nodeID]; !inForest {
			rootIDs[nodeID] = true
		}
	}

	var entries []EntryPoint
	for nodeID := range rootIDs {
		node, ok := forest.Nodes[nodeID]
		if !ok {
			continue
		}
		if node.Kind != constants.KindFunction {
			continue
		}

		entry := EntryPoint{
			NodeID:        nodeID,
			Name:          nodeProperty(&node, "name"),
			QualifiedName: nodeProperty(&node, "qualified_name"),
			FilePath:      nodeProperty(&node, "file_path"),
		}

		outDeg := forest.OutDegree[nodeID]

		// Classify by priority (top to bottom, first match wins)
		switch {
		case remoteClientSet[nodeID]:
			entry.EntryType = "remote_client"
			entry.Score = 0.90
			if ri, ok := handlerRoute[nodeID]; ok {
				entry.RouteMethod = ri.Method
				entry.RoutePath = ri.Path
			}
		case handlerSet[nodeID]:
			entry.EntryType = "http_endpoint"
			entry.Score = 0.95
			if ri, ok := handlerRoute[nodeID]; ok {
				entry.RouteMethod = ri.Method
				entry.RoutePath = ri.Path
			}
		case hasAnnotationEntryType(nodeAnnotations[nodeID]) != "":
			entry.EntryType = hasAnnotationEntryType(nodeAnnotations[nodeID])
			entry.Score = 0.85
		case nodeProperty(&node, "name") == "main" || nodeProperty(&node, "name") == "Main":
			entry.EntryType = "cli_command"
			entry.Score = 0.80
		case isInterfaceImpl(entry.QualifiedName, implStructNames):
			entry.EntryType = "interface_impl"
			entry.Score = 0.70
		case outDeg == 0:
			entry.EntryType = "suspected_dead"
			entry.Score = 0.30
		case outDeg > 0:
			entry.EntryType = "unknown_entry"
			entry.Score = 0.50
		}

		entries = append(entries, entry)
	}

	// Emit classification summary
	typeCounts := make(map[string]int)
	for _, e := range entries {
		typeCounts[e.EntryType]++
	}
	summary := fmt.Sprintf("%d entry points", len(entries))
	for entryType, count := range typeCounts {
		summary += fmt.Sprintf(", %s=%d", entryType, count)
	}
	analyzer.progress.Emit(PhaseClassifyRoots, 0, 0, summary)

	return entries, nil
}

// TraceProcess traces execution flow from an entry point via DFS.
// BuildLayerMap builds a map from nodeID to architectural layer using annotations.
func (analyzer *Analyzer) BuildLayerMap(ctx context.Context) map[string]string {
	m := map[string]string{}

	// Load all annotations
	anns, err := analyzer.graphStore.QueryAllByKind(ctx, "Annotation", 0)
	if err != nil {
		return m
	}
	annLayerByID := make(map[string]string)
	for _, ann := range anns {
		layer := propString(ann.Properties, "layer")
		if layer != "" {
			annLayerByID[ann.ID] = layer
		}
	}
	if len(annLayerByID) == 0 {
		return m
	}

	// Load all HAS_ANNOTATION edges in one query
	annotationEdges, _ := analyzer.graphStore.QueryAllEdges(ctx, model.RelHasAnnotation, 0)

	// Batch-load all classes/interfaces for file_path lookup
	fileLayerMap := map[string]string{}
	classMap := make(map[string]*model.Node)
	for _, kind := range []string{constants.KindClass, constants.KindInterface} {
		nodes, _ := analyzer.graphStore.QueryAllByKind(ctx, kind, 0)
		for i := range nodes {
			classMap[nodes[i].ID] = &nodes[i]
		}
	}

	// Single pass: map annotated nodes to layers
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

	// Propagate layer to all functions in the same file
	if len(fileLayerMap) > 0 {
		funcs, _ := analyzer.graphStore.QueryAllByKind(ctx, constants.KindFunction, 0)
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

func (analyzer *Analyzer) TraceProcess(forest *CallForest, entryID string, entryName string, maxDepth int, layerMap map[string]string) *ProcessStep {
	visited := make(map[string]bool)
	root := &ProcessStep{
		Order:  1,
		NodeID: entryID,
		Name:   entryName,
		Layer:  layerMap[entryID],
		Depth:  0,
	}
	visited[entryID] = true
	analyzer.traceDFS(forest, root, visited, maxDepth, layerMap)
	return root
}

func (analyzer *Analyzer) traceDFS(forest *CallForest, step *ProcessStep, visited map[string]bool, maxDepth int, layerMap map[string]string) {
	if step.Depth >= maxDepth {
		return
	}
	for _, edge := range forest.Children[step.NodeID] {
		if visited[edge.TargetID] || edge.Confidence < 0.5 {
			continue
		}
		visited[edge.TargetID] = true
		name := edge.TargetID
		filePath := ""
		isGetter := false
		isSetter := false
		if node, ok := forest.Nodes[edge.TargetID]; ok {
			name = nodeProperty(&node, "name")
			filePath = nodeProperty(&node, "file_path")
			isGetter, _ = node.Properties["is_getter"].(bool)
			isSetter, _ = node.Properties["is_setter"].(bool)
		}
		child := &ProcessStep{
			NodeID:     edge.TargetID,
			Name:       name,
			FilePath:   filePath,
			Layer:      layerMap[edge.TargetID],
			Confidence: edge.Confidence,
			Depth:      step.Depth + 1,
			IsGetter:   isGetter,
			IsSetter:   isSetter,
		}
		step.Children = append(step.Children, child)
		analyzer.traceDFS(forest, child, visited, maxDepth, layerMap)
	}
}

// WriteEntryPoints writes entry_type and entry_point_score to Function nodes.
func (analyzer *Analyzer) WriteEntryPoints(ctx context.Context, entries []EntryPoint) error {
	if len(entries) == 0 {
		return nil
	}
	updates := make([]storage.PropertyUpdate, 0, len(entries))
	for _, e := range entries {
		updates = append(updates, storage.PropertyUpdate{
			NodeID: e.NodeID,
			Props: map[string]any{
				"entry_type":        e.EntryType,
				"entry_point_score": e.Score,
			},
		})
	}
	return analyzer.graphStore.BatchUpdateNodeProperties(ctx, constants.KindFunction, updates)
}

// ClearAnalysisData removes old Process nodes and STEP edges.
func (analyzer *Analyzer) ClearAnalysisData(ctx context.Context) {
	analyzer.graphStore.DeleteAllByKind(ctx, "Process")
}

// WriteProcesses persists Process nodes and STEP edges to the graph.
func (analyzer *Analyzer) WriteProcesses(ctx context.Context, entries []EntryPoint, forest *CallForest, maxDepth int, layerMap map[string]string) (int, int) {
	analyzer.progress.Emit(PhaseTraceProcesses, 0, 0, "")
	var nodes []model.Node
	var edges []model.Edge

	processCount := 0
	stepCount := 0

	for _, entry := range entries {
		if entry.EntryType == "suspected_dead" || entry.EntryType == "unknown_entry" || entry.EntryType == "interface_impl" {
			continue
		}
		root := analyzer.TraceProcess(forest, entry.NodeID, entry.Name, maxDepth, layerMap)
		steps := flattenSteps(root, nil)
		if len(steps) == 0 {
			continue
		}

		processID := fmt.Sprintf("process:%s", entry.NodeID)
		nodes = append(nodes, model.Node{
			ID:   processID,
			Kind: "Process",
			Properties: map[string]any{
				"name":         entry.Name,
				"entry_point":  entry.NodeID,
				"step_count":   len(steps),
				"entry_type":   entry.EntryType,
				"file_path":    entry.FilePath,
				"route_method": entry.RouteMethod,
				"route_path":   entry.RoutePath,
			},
		})
		processCount++

		for _, step := range steps {
			edges = append(edges, model.Edge{
				SourceID: processID,
				TargetID: step.NodeID,
				Kind:     model.RelStep,
				Properties: map[string]any{
					"order":      step.Order,
					"depth":      step.Depth,
					"layer":      step.Layer,
					"confidence": step.Confidence,
				},
			})
			stepCount++
		}
	}
	analyzer.progress.Emit(PhaseTraceProcesses, 0, 0, fmt.Sprintf("%d processes, %d steps", processCount, stepCount))

	analyzer.progress.Emit(PhasePersist, 0, 0, "")
	if len(nodes) > 0 {
		analyzer.graphStore.CreateNodes(ctx, nodes)
	}
	if len(edges) > 0 {
		analyzer.graphStore.WriteEdges(ctx, edges)
	}
	analyzer.progress.Emit(PhasePersist, 0, 0, fmt.Sprintf("%d processes written", processCount))
	return processCount, stepCount
}

// flattenSteps converts a tree of ProcessSteps into a flat ordered list.
func flattenSteps(step *ProcessStep, result []ProcessStep) []ProcessStep {
	step.Order = len(result) + 1
	result = append(result, *step)
	for _, child := range step.Children {
		result = flattenSteps(child, result)
	}
	return result
}

func isInterfaceImpl(qualifiedName string, implStructNames map[string]bool) bool {
	// qualifiedName like "falkor.Store.DeleteAllByKind" → struct name "Store"
	parts := strings.Split(qualifiedName, ".")
	if len(parts) >= 2 {
		structName := parts[len(parts)-2]
		return implStructNames[structName]
	}
	return false
}

func nodeProperty(node *model.Node, key string) string {
	if node == nil || node.Properties == nil {
		return ""
	}
	if v, ok := node.Properties[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func hasAnnotation(annotations []string, names ...string) bool {
	for _, ann := range annotations {
		for _, name := range names {
			if ann == name || containsSuffix(ann, name) {
				return true
			}
		}
	}
	return false
}

// hasAnnotationEntryType returns the EntryType if any annotation has one defined in DefaultAnnotations.
// annotations are Annotation node IDs in the format "hash::Name", so we extract the name part.
func hasAnnotationEntryType(annotationIDs []string) string {
	for _, id := range annotationIDs {
		name := id
		if idx := strings.LastIndex(id, "::"); idx >= 0 {
			name = id[idx+2:]
		}
		if et := annotation.LookupEntryType(name); et != "" {
			return et
		}
	}
	return ""
}

func containsSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
