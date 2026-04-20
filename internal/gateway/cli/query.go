package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/config"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/service"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
	"github.com/spf13/cobra"
)

var (
	queryKinds      string
	queryLimit      int
	queryAnnotation string
	queryLayer      string
	queryCategory   string
	queryMethods    bool
	callchainDepth   int
	callchainMinConf float64
	callchainReverse bool
	callchainFlow    bool
	impactDepth      int
	impactMinConf    float64
	traceMethod      string
	traceDepth       int
)

func init() {
	// fcg query [symbol]
	queryCmd := &cobra.Command{
		Use:   "query [symbol]",
		GroupID: "query",
		Short: "Query symbols by name, annotation, layer, or category",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runQuery,
	}
	queryCmd.Flags().StringVar(&queryKinds, "kinds", "", "Filter by kinds (comma-separated: Function,Class,Interface)")
	queryCmd.Flags().IntVar(&queryLimit, "limit", 20, "Max results")
	queryCmd.Flags().StringVar(&queryAnnotation, "annotation", "", "Filter by annotation name (e.g. Service)")
	queryCmd.Flags().StringVar(&queryLayer, "layer", "", "Filter by layer (controller/service/repository/model)")
	queryCmd.Flags().StringVar(&queryCategory, "category", "", "Filter by annotation category (security/behavior/etc)")
	queryCmd.Flags().BoolVar(&queryMethods, "methods", false, "List methods of a class")
	rootCmd.AddCommand(queryCmd)

	// fcg routes
	routesCmd := &cobra.Command{
		Use:     "routes",
		GroupID: "query",
		Short:   "List all HTTP routes",
		RunE:    runRoutes,
	}
	rootCmd.AddCommand(routesCmd)

	// fcg trace <route>
	traceCmd := &cobra.Command{
		Use:   "trace <route-path>",
		GroupID: "query",
		Short: "Trace call chain from a route entry point",
		Args:  cobra.ExactArgs(1),
		RunE:  runTrace,
	}
	traceCmd.Flags().StringVar(&traceMethod, "method", "", "HTTP method filter (GET/POST/etc)")
	traceCmd.Flags().IntVar(&traceDepth, "depth", 10, "Max traversal depth")
	rootCmd.AddCommand(traceCmd)

	// fcg callchain <function>
	callchainCmd := &cobra.Command{
		Use:   "callchain <function>",
		GroupID: "query",
		Short: "Query call chain for a function",
		Args:  cobra.ExactArgs(1),
		RunE:  runCallchain,
	}
	callchainCmd.Flags().IntVar(&callchainDepth, "depth", 3, "Max traversal depth")
	callchainCmd.Flags().Float64Var(&callchainMinConf, "min-confidence", 0, "Min confidence filter")
	callchainCmd.Flags().BoolVar(&callchainReverse, "reverse", false, "Show callers instead of callees")
	callchainCmd.Flags().BoolVar(&callchainFlow, "flow", false, "Show control flow context (if/else/loop/defer)")
	rootCmd.AddCommand(callchainCmd)

	// fcg impact <function>
	impactCmd := &cobra.Command{
		Use:     "impact <function>",
		GroupID: "query",
		Short:   "Analyze impact — which callers are affected by changes to a function",
		Args:    cobra.ExactArgs(1),
		RunE:    runImpact,
	}
	impactCmd.Flags().IntVar(&impactDepth, "depth", 3, "Max traversal depth")
	impactCmd.Flags().Float64Var(&impactMinConf, "min-confidence", 0, "Min confidence filter")
	rootCmd.AddCommand(impactCmd)

	// fcg search <query>
	searchCmd := &cobra.Command{
		Use:   "search <query>",
		GroupID: "query",
		Short: "Search symbols by name",
		Args:  cobra.ExactArgs(1),
		RunE:  runSearch,
	}
	searchCmd.Flags().IntVar(&queryLimit, "limit", 20, "Max results")
	rootCmd.AddCommand(searchCmd)
}

// warnIfIndexStale checks if the project index is outdated and prints a warning to stderr.
// Never blocks or fails — silently returns on any error.
func warnIfIndexStale() {
	repoPath := projectDir()
	indexStatus, err := service.CheckProjectStaleness(context.Background(), repoPath, "")
	if err != nil {
		return
	}
	warning := service.FormatStalenessWarning(indexStatus)
	if warning != "" {
		fmt.Fprintf(os.Stderr, "⚠️  %s\n", warning)
	}
}

func createQuerier() (*service.Querier, storage.GraphStore, error) {
	repoPath := projectDir()
	cfg, err := config.Load(repoPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	store, err := openGraphStore(cfg, repoPath)
	if err != nil {
		return nil, nil, err
	}
	return service.NewQuerier(store), store, nil
}

func runQuery(cmd *cobra.Command, args []string) error {
	warnIfIndexStale()
	querier, store, err := createQuerier()
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()

	// Annotation-based queries
	if queryAnnotation != "" {
		nodes, err := querier.QueryByAnnotation(ctx, queryAnnotation, queryKinds, queryLimit)
		if err != nil {
			return err
		}
		printFilteredSymbols("annotation", queryAnnotation, nodes)
		return nil
	}
	if queryLayer != "" {
		nodes, err := querier.QueryByLayer(ctx, queryLayer, queryLimit)
		if err != nil {
			return err
		}
		printFilteredSymbols("layer", queryLayer, nodes)
		return nil
	}
	if queryCategory != "" {
		nodes, err := querier.QueryByAnnotationCategory(ctx, queryCategory, queryLimit)
		if err != nil {
			return err
		}
		printFilteredSymbols("category", queryCategory, nodes)
		return nil
	}

	// Standard symbol query
	if len(args) == 0 {
		return fmt.Errorf("provide a symbol name or use --annotation/--layer/--category")
	}

	// --methods: list methods of a class
	if queryMethods {
		methods, candidates, err := querier.QueryClassMethods(ctx, args[0], queryLimit)
		if err != nil {
			return err
		}
		if methods == nil && candidates == nil {
			fmt.Printf("No class found for %q\n", args[0])
			return nil
		}
		if candidates != nil {
			fmt.Printf("Multiple classes match %q:\n\n", args[0])
			for _, candidate := range candidates {
				qualifiedName, _ := candidate.Properties["qualified_name"].(string)
				filePath, _ := candidate.Properties["file_path"].(string)
				fmt.Printf("  %-40s %s\n", qualifiedName, filePath)
			}
			exampleName, _ := candidates[0].Properties["qualified_name"].(string)
			fmt.Printf("\nUse qualified name for exact match, e.g.:\n")
			fmt.Printf("  fcg query %s --methods\n", exampleName)
			return nil
		}
		fmt.Printf("Methods of %q (%d):\n\n", args[0], len(methods))
		for _, method := range methods {
			qualifiedName, _ := method.Properties["qualified_name"].(string)
			filePath, _ := method.Properties["file_path"].(string)
			startLine := method.Properties["start_line"]
			params := formatParamsSummary(method)
			returnTypes, _ := method.Properties["return_types"].(string)
			if returnTypes == "" || returnTypes == "null" || returnTypes == "[]" {
				returnTypes = ""
			}
			sig := qualifiedName + " " + params
			if returnTypes != "" {
				fmt.Printf("  %-55s → %-15s %s:%v\n", sig, returnTypes, filePath, startLine)
			} else {
				fmt.Printf("  %-55s %s:%v\n", sig, filePath, startLine)
			}
		}
		return nil
	}

	opts := model.QueryOpts{Limit: queryLimit}
	if queryKinds != "" {
		opts.Kinds = strings.Split(queryKinds, ",")
	}

	nodes, err := querier.QuerySymbol(ctx, args[0], opts)
	if err != nil {
		return err
	}

	if len(nodes) == 0 {
		fmt.Printf("No symbols found for %q\n", args[0])
		return nil
	}

	fmt.Printf("Found %d symbols for %q:\n\n", len(nodes), args[0])
	for _, node := range nodes {
		name, _ := node.Properties["name"].(string)
		filePath, _ := node.Properties["file_path"].(string)
		startLine := node.Properties["start_line"]
		fmt.Printf("  %-12s %-30s %s:%v\n", node.Kind, name, filePath, startLine)
	}
	return nil
}

func printFilteredSymbols(filterType, filterValue string, nodes []model.Node) {
	if len(nodes) == 0 {
		fmt.Printf("No symbols found for %s=%q\n", filterType, filterValue)
		return
	}
	fmt.Printf("Found %d symbols with %s=%q:\n\n", len(nodes), filterType, filterValue)
	for _, node := range nodes {
		name, _ := node.Properties["name"].(string)
		filePath, _ := node.Properties["file_path"].(string)
		startLine := node.Properties["start_line"]
		fmt.Printf("  %-12s %-30s %s:%v\n", node.Kind, name, filePath, startLine)
	}
}

func runCallchain(cmd *cobra.Command, args []string) error {
	warnIfIndexStale()
	querier, store, err := createQuerier()
	if err != nil {
		return err
	}
	defer store.Close()

	direction := model.Outgoing
	dirLabel := "callees"
	if callchainReverse {
		direction = model.Incoming
		dirLabel = "callers"
	}

	ctx := context.Background()
	node, candidates, err := querier.ResolveFunction(ctx, args[0])
	if err != nil {
		return err
	}

	// Handle ambiguous matches: interactive selection
	if node == nil && len(candidates) > 0 {
		node = promptSelectFunction(candidates, args[0])
		if node == nil {
			return nil
		}
	}

	if node == nil {
		fmt.Printf("No function found for %q\n", args[0])
		return nil
	}

	subgraph, err := querier.QueryCallChainByNodeID(ctx, node.ID, direction, callchainDepth, callchainMinConf)
	if err != nil {
		return err
	}

	if len(subgraph.Nodes) == 0 {
		fmt.Printf("No %s found for %q\n", dirLabel, args[0])
		return nil
	}

	fmt.Printf("%s of %q (depth=%d, minConfidence=%.2f):\n\n", strings.ToUpper(dirLabel[:1])+dirLabel[1:], args[0], callchainDepth, callchainMinConf)

	// Add root node to subgraph for tree rendering
	subgraph.Nodes = append([]model.Node{*node}, subgraph.Nodes...)

	printCallTree(subgraph, args[0])
	fmt.Printf("\nTotal: %d %s\n", len(subgraph.Nodes), dirLabel)
	return nil
}


func runImpact(cmd *cobra.Command, args []string) error {
	warnIfIndexStale()
	querier, store, err := createQuerier()
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()
	node, candidates, err := querier.ResolveFunction(ctx, args[0])
	if err != nil {
		return err
	}
	if node == nil && len(candidates) > 0 {
		node = promptSelectFunction(candidates, args[0])
		if node == nil {
			return nil
		}
	}
	if node == nil {
		fmt.Printf("No function found for %q\n", args[0])
		return nil
	}

	subgraph, err := querier.QueryCallChainByNodeID(ctx, node.ID, model.Incoming, impactDepth, impactMinConf)
	if err != nil {
		return err
	}

	if len(subgraph.Nodes) == 0 {
		fmt.Printf("No callers affected by changes to %q\n", args[0])
		return nil
	}

	fmt.Printf("Impact analysis for %q (depth=%d, minConfidence=%.2f):\n\n", args[0], impactDepth, impactMinConf)
	subgraph.Nodes = append([]model.Node{*node}, subgraph.Nodes...)
	printCallTree(subgraph, args[0])
	fmt.Printf("\nTotal: %d affected callers\n", len(subgraph.Nodes)-1)
	return nil
}
// promptSelectFunction displays candidates and prompts user to select one.
// Returns nil if user quits.
func promptSelectFunction(candidates []model.Node, symbolName string) *model.Node {
	fmt.Printf("Multiple functions match %q:\n\n", symbolName)
	for i, candidate := range candidates {
		qualifiedName, _ := candidate.Properties["qualified_name"].(string)
		filePath, _ := candidate.Properties["file_path"].(string)
		params := formatParamsSummary(candidate)
		if params != "" {
			fmt.Printf("  [%d] %-40s %-20s %s\n", i+1, qualifiedName, params, filePath)
		} else {
			fmt.Printf("  [%d] %-40s %s\n", i+1, qualifiedName, filePath)
		}
	}
	fmt.Println("  [q] quit")
	fmt.Print("\nSelect: ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return nil
	}
	input := strings.TrimSpace(scanner.Text())
	if input == "q" || input == "" {
		return nil
	}
	index, err := strconv.Atoi(input)
	if err != nil || index < 1 || index > len(candidates) {
		fmt.Println("Invalid selection.")
		return nil
	}
	return &candidates[index-1]
}

// formatParamsSummary extracts a short param summary from node properties.
func formatParamsSummary(node model.Node) string {
	paramsJSON, _ := node.Properties["params"].(string)
	if paramsJSON == "" || paramsJSON == "null" {
		return "()"
	}
	var params []map[string]any
	if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
		return ""
	}
	var parts []string
	for _, param := range params {
		typeName, _ := param["type"].(string)
		paramName, _ := param["name"].(string)
		if typeName != "" {
			parts = append(parts, typeName)
		} else if paramName != "" {
			parts = append(parts, paramName)
		}
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

type foldedChild struct {
	callChild
	implCount int
	others    []string // other candidate target IDs
}

type callChild struct {
	targetID     string
	line         int
	name         string
	flowContext  string
	flowLine     int
	declaredType string
}

func printCallTree(subgraph *model.Subgraph, rootName string) {
	nodeMap := make(map[string]*model.Node)
	for i := range subgraph.Nodes {
		nodeMap[subgraph.Nodes[i].ID] = &subgraph.Nodes[i]
	}

	children := make(map[string][]callChild)
	childSet := make(map[string]bool)
	for _, edge := range subgraph.Edges {
		line := 0
		if l, ok := edge.Properties["line"]; ok {
			switch v := l.(type) {
			case int:
				line = v
			case float64:
				line = int(v)
			}
		}
		name := ""
		if n := nodeMap[edge.TargetID]; n != nil {
			name, _ = n.Properties["name"].(string)
		}
		flowCtx, _ := edge.Properties["flow_context"].(string)
		flowLine := 0
		if fl, ok := edge.Properties["flow_line"]; ok {
			switch v := fl.(type) {
			case int:
				flowLine = v
			case float64:
				flowLine = int(v)
			}
		}
		declaredType, _ := edge.Properties["declared_type"].(string)
		children[edge.SourceID] = append(children[edge.SourceID], callChild{edge.TargetID, line, name, flowCtx, flowLine, declaredType})
		childSet[edge.TargetID] = true
	}

	// Sort by line number
	for k := range children {
		c := children[k]
		sort.Slice(c, func(i, j int) bool { return c[i].line < c[j].line })
		children[k] = c
	}

	var roots []string
	// First node is always the queried root (prepended by caller)
	if len(subgraph.Nodes) > 0 {
		roots = append(roots, subgraph.Nodes[0].ID)
	}
	for _, node := range subgraph.Nodes[1:] {
		if !childSet[node.ID] && node.ID != subgraph.Nodes[0].ID {
			roots = append(roots, node.ID)
		}
	}

	if len(subgraph.Edges) == 0 {
		for _, node := range subgraph.Nodes {
			qn, _ := node.Properties["qualified_name"].(string)
			fp, _ := node.Properties["file_path"].(string)
			if qn == "" {
				qn, _ = node.Properties["name"].(string)
			}
			fmt.Printf("  → %-35s %s\n", qn, fp)
		}
		return
	}

	visited := make(map[string]bool)
	for _, rootID := range roots {
		printCallNode(rootID, nodeMap, children, visited, "", true, "")
	}
}

func printCallNode(nodeID string, nodeMap map[string]*model.Node, children map[string][]callChild, visited map[string]bool, prefix string, isLast bool, declaredType string) {
	if visited[nodeID] {
		// Already shown elsewhere — print reference marker
		node := nodeMap[nodeID]
		name := nodeID
		if node != nil {
			if qn, ok := node.Properties["qualified_name"].(string); ok && qn != "" {
				name = qn
			} else {
				name, _ = node.Properties["name"].(string)
			}
		}
		connector := "├── "
		if isLast {
			connector = "└── "
		}
		fmt.Printf("%s%s%-40s (↑ see above)\n", prefix, connector, name)
		return
	}
	visited[nodeID] = true

	node := nodeMap[nodeID]
	name := nodeID
	filePath := ""
	if node != nil {
		name, _ = node.Properties["name"].(string)
		if qn, ok := node.Properties["qualified_name"].(string); ok && qn != "" {
			name = qn
		}
		filePath, _ = node.Properties["file_path"].(string)
	}
	if filePath == "[external]" {
		name = name + " [external]"
		filePath = ""
	}

	connector := "├── "
	if isLast {
		connector = "└── "
	}
	via := ""
	if declaredType != "" {
		// Only show (via X) when declared type differs from target's owner class
		shortType := declaredType
		if idx := strings.LastIndex(declaredType, "."); idx >= 0 {
			shortType = declaredType[idx+1:]
		}
		ownerClass := ""
		if qn, _ := node.Properties["qualified_name"].(string); qn != "" {
			// Extract owner class from "pkg.OwnerClass.method"
			parts := strings.Split(qn, ".")
			if len(parts) >= 2 {
				ownerClass = parts[len(parts)-2]
			}
		}
		if shortType != ownerClass {
			via = fmt.Sprintf(" (via %s)", shortType)
		}
	}
	fmt.Printf("%s%s%s%s %s\n", prefix, connector, name, via, filePath)

	// Fold: group children by short name, keep first, fold duplicates
	var folded []foldedChild
	seen := make(map[string]int) // "name:line" → index
	for _, cc := range children[nodeID] {
		key := fmt.Sprintf("%s:%d", cc.name, cc.line)
		if idx, exists := seen[key]; exists {
			folded[idx].implCount++
			folded[idx].others = append(folded[idx].others, cc.targetID)
		} else {
			seen[key] = len(folded)
			folded = append(folded, foldedChild{cc, 0, nil})
		}
	}

	childPrefix := prefix + "│   "
	if isLast {
		childPrefix = prefix + "    "
	}

	if callchainFlow {
		renderWithFlow(folded, nodeMap, children, visited, childPrefix)
	} else {
		for i, fc := range folded {
			last := i == len(folded)-1
			if fc.implCount > 0 {
				printFoldedNode(fc, nodeMap, childPrefix, last)
			} else {
				printCallNode(fc.targetID, nodeMap, children, visited, childPrefix, last, fc.declaredType)
			}
		}
	}
}

func printFoldedNode(fc foldedChild, nodeMap map[string]*model.Node, prefix string, isLast bool) {
	// Collect all candidate qualified names
	var names []string
	for _, id := range append([]string{fc.targetID}, fc.others...) {
		if n := nodeMap[id]; n != nil {
			if qn, ok := n.Properties["qualified_name"].(string); ok && qn != "" {
				// Extract class name: "com.dayu.common.ResponseResult.e" → "ResponseResult"
				parts := strings.Split(qn, ".")
				if len(parts) >= 2 {
					names = append(names, parts[len(parts)-2])
				} else {
					names = append(names, parts[0])
				}
			}
		}
	}

	fNode := nodeMap[fc.targetID]
	fPath := ""
	if fNode != nil {
		fPath, _ = fNode.Properties["file_path"].(string)
	}

	// Deduplicate names
	seen := make(map[string]bool)
	var unique []string
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			unique = append(unique, n)
		}
	}

	conn := "├── "
	if isLast {
		conn = "└── "
	}
	label := fmt.Sprintf("(%s).%s", strings.Join(unique, ","), fc.name)
	fmt.Printf("%s%s%-40s %s [candidate]\n", prefix, conn, label, fPath)
}

type vnode struct {
	label    string
	fc       *foldedChild
	children []*vnode
}

func renderWithFlow(folded []foldedChild, nodeMap map[string]*model.Node, children map[string][]callChild, visited map[string]bool, prefix string) {
	// Build a virtual tree: flow context segments become virtual nodes
	root := &vnode{}

	// Insert each folded child into the virtual tree based on flow context path
	for i := range folded {
		fc := &folded[i]
		segments := splitFlowContext(fc.flowContext)
		parent := root
		for _, seg := range segments {
			// Find or create virtual child
			var found *vnode
			for _, ch := range parent.children {
				if ch.label == seg && ch.fc == nil {
					found = ch
					break
				}
			}
			if found == nil {
				found = &vnode{label: seg}
				parent.children = append(parent.children, found)
			}
			parent = found
		}
		// Add real node as leaf
		parent.children = append(parent.children, &vnode{fc: fc})
	}

	// Render the virtual tree
	renderVNode(root, nodeMap, children, visited, prefix)
}

func splitFlowContext(ctx string) []string {
	if ctx == "" {
		return nil
	}
	return strings.Split(ctx, " > ")
}

func renderVNode(node *vnode, nodeMap map[string]*model.Node, children map[string][]callChild, visited map[string]bool, prefix string) {
	for i, ch := range node.children {
		last := i == len(node.children)-1
		if ch.fc != nil {
			// Real node
			if ch.fc.implCount > 0 {
				printFoldedNode(*ch.fc, nodeMap, prefix, last)
			} else {
				printCallNode(ch.fc.targetID, nodeMap, children, visited, prefix, last, ch.fc.declaredType)
			}
		} else {
			// Virtual flow node
			conn := "├── "
			if last {
				conn = "└── "
			}
			fmt.Printf("%s%s[%s]\n", prefix, conn, ch.label)
			childPrefix := prefix + "│   "
			if last {
				childPrefix = prefix + "    "
			}
			renderVNode(ch, nodeMap, children, visited, childPrefix)
		}
	}
}


func runRoutes(cmd *cobra.Command, args []string) error {
	_, store, err := createQuerier()
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()
	routes, err := store.QueryAllByKind(ctx, "Route", 0)
	if err != nil {
		return err
	}
	if len(routes) == 0 {
		fmt.Println("No routes found.")
		return nil
	}

	fmt.Printf("Routes (%d):\n\n", len(routes))
	fmt.Printf("  %-8s %-35s %s\n", "METHOD", "PATH", "HANDLER")
	fmt.Printf("  %-8s %-35s %s\n", "------", "----", "-------")
	for _, r := range routes {
		method, _ := r.Properties["method"].(string)
		path, _ := r.Properties["path_pattern"].(string)
		handler, _ := r.Properties["handler_method"].(string)
		framework, _ := r.Properties["framework"].(string)
		if framework != "" {
			handler = handler + " [" + framework + "]"
		}
		fmt.Printf("  %-8s %-35s %s\n", method, path, handler)
	}
	return nil
}

func runTrace(cmd *cobra.Command, args []string) error {
	querier, store, err := createQuerier()
	if err != nil {
		return err
	}
	defer store.Close()

	chain, err := querier.QueryRouteChain(context.Background(), args[0], traceMethod, traceDepth)
	if err != nil {
		return err
	}

	fmt.Printf("Route: %s %s\n", chain.Method, chain.Route)
	fmt.Printf("Chain (%d nodes):\n\n", len(chain.Chain))
	fmt.Printf("  %-4s %-12s %-45s %s\n", "#", "KIND", "NAME", "FILE")
	fmt.Printf("  %-4s %-12s %-45s %s\n", "-", "----", "----", "----")
	for i, cn := range chain.Chain {
		name := cn.Name
		if cn.QualifiedName != "" {
			name = cn.QualifiedName
		}
		layer := ""
		if cn.Layer != "" {
			layer = " [" + cn.Layer + "]"
		}
		fmt.Printf("  %-4d %-12s %-45s %s%s\n", i+1, cn.Kind, name, cn.FilePath, layer)
	}
	if len(chain.Queries) > 0 {
		// Deduplicate by sql + caller
		type queryKey struct{ sql, caller string }
		seen := map[queryKey]bool{}
		var unique []model.ChainNode
		for _, q := range chain.Queries {
			key := queryKey{q.Name, q.QualifiedName}
			if !seen[key] {
				seen[key] = true
				unique = append(unique, q)
			}
		}
		fmt.Printf("\nQueries (%d):\n", len(unique))
		for _, q := range unique {
			sql := q.Name
			if len(sql) > 55 {
				sql = sql[:55] + "..."
			}
			tables := ""
			if q.FilePath != "" {
				tables = " [" + q.FilePath + "]"
			}
			// Short caller: last segment
			caller := q.QualifiedName
			if idx := strings.LastIndex(caller, "."); idx >= 0 {
				caller = caller[idx+1:]
			}
			fmt.Printf("  %-8s %-60s ← %s%s\n", q.Kind, sql, caller, tables)
		}
	}
	return nil
}

func runSearch(cmd *cobra.Command, args []string) error {
	querier, store, err := createQuerier()
	if err != nil {
		return err
	}
	defer store.Close()

	results, err := querier.SearchFTS(context.Background(), args[0], queryLimit)
	if err != nil {
		return err
	}

	if len(results) == 0 {
		fmt.Printf("No results for %q\n", args[0])
		return nil
	}

	fmt.Printf("Search results for %q:\n\n", args[0])
	for _, result := range results {
		name := result.QualifiedName
		if name == "" || name == "<nil>" {
			name = result.Name
		}
		fmt.Printf("  %-12s %-40s %s\n", result.Kind, name, result.Path)
	}
	return nil
}
