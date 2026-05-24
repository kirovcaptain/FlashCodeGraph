package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/config"
	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/annotation"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/service"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/branch"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/falkor"
	"github.com/spf13/cobra"
)

var (
	queryKinds      string
	queryLimit      int
	queryAnnotation string
	queryParams     string
	queryLayer      string
	queryCategory   string
	listCategories  bool
	queryMembers    bool
	callchainDepth   int
	callchainMinConf float64
	callchainReverse bool
	callchainFlow    bool
	callchainMode    string
	impactDepth      int
	impactMinConf    float64
	usagesLimit      int
	traceMethod      string
	traceDepth       int
	traceMode        string
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
	queryCmd.Flags().IntVar(&queryLimit, "limit", 0, "Max results (0 = no limit)")
	queryCmd.Flags().StringVar(&queryAnnotation, "annotation", "", "Filter by annotation name (e.g. Service)")
	queryCmd.Flags().StringVar(&queryParams, "params", "", "Filter by annotation params (substring match)")
	queryCmd.Flags().StringVar(&queryLayer, "layer", "", "Filter by layer (controller/service/repository/model)")
	queryCmd.Flags().StringVar(&queryCategory, "category", "", "Filter by annotation category (use --list-categories to see values)")
	queryCmd.Flags().BoolVar(&listCategories, "list-categories", false, "List all available annotation categories")
	queryCmd.Flags().BoolVar(&queryMembers, "members", false, "List fields and methods of a class")
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
	traceCmd.Flags().StringVar(&traceMode, "mode", "dry", "Display mode: dry (default, core + remove log/exception), core (prune DISPATCHES/accessors), compact (dry + merge edges), full (show all)")
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
	callchainCmd.Flags().StringVar(&callchainMode, "mode", "dry", "Display mode: dry (default, core + remove log/exception), core (prune DISPATCHES/accessors), compact (dry + merge edges), full (show all)")
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
	searchCmd.Flags().IntVar(&queryLimit, "limit", 0, "Max results (0 = no limit)")
	rootCmd.AddCommand(searchCmd)

	// fcg usages <constant-qualified-name>
	usagesCmd := &cobra.Command{
		Use:     "usages <constant-qualified-name>",
		GroupID: "query",
		Short:   "Query all references to a static constant (enum/interface/class constant)",
		Args:    cobra.ExactArgs(1),
		RunE:    runUsages,
	}
	usagesCmd.Flags().IntVar(&usagesLimit, "limit", 0, "Max results (0 = no limit)")
	rootCmd.AddCommand(usagesCmd)
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
	absPath, _ := filepath.Abs(repoPath)
	cfg, err := config.Load(repoPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}

	// Check if multiple branches are indexed for this project
	fcgDir := config.GlobalDir()
	registry, _ := storage.NewRegistry(fcgDir)
	if registry != nil {
		entries := registry.FindByPath(absPath)
		if len(entries) > 1 {
			currentBranch := branch.DetectBranch(absPath)
			// Check if current branch is indexed
			found := false
			for _, e := range entries {
				if e.Branch == currentBranch {
					found = true
					break
				}
			}
			if !found {
				selectedBranch := promptBranchSelection(entries, currentBranch)
				cfg.Storage.Branch = selectedBranch
			}
		}
	}

	store, err := openGraphStore(cfg, repoPath)
	if err != nil {
		return nil, nil, err
	}
	// Check if graph exists to prevent FalkorDB from auto-creating empty graphs
	if falkorStore, ok := store.(*falkor.Store); ok {
		if !falkorStore.GraphExists(context.Background()) {
			store.Close()
			return nil, nil, fmt.Errorf("no index found. Run 'fcg index' first")
		}
	}
	return service.NewQuerier(store), store, nil
}

// promptBranchSelection asks the user to pick a branch when the current branch is not indexed.
// In non-TTY mode, returns the first available branch.
func promptBranchSelection(entries []storage.RegistryEntry, currentBranch string) string {
	// Non-TTY: use first entry
	stat, _ := os.Stdin.Stat()
	if stat == nil || (stat.Mode()&os.ModeCharDevice) == 0 {
		return entries[0].Branch
	}

	fmt.Fprintf(os.Stderr, "⚠ Branch '%s' is not indexed. Available branches:\n", currentBranch)
	for i, e := range entries {
		fmt.Fprintf(os.Stderr, "  %d) %s\n", i+1, e.Branch)
	}
	fmt.Fprintf(os.Stderr, "Select branch [1]: ")

	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			return entries[0].Branch
		}
		if idx, err := strconv.Atoi(input); err == nil && idx >= 1 && idx <= len(entries) {
			return entries[idx-1].Branch
		}
	}
	return entries[0].Branch
}

func runQuery(cmd *cobra.Command, args []string) error {
	if listCategories {
		categories := annotation.ListCategories()
		fmt.Println("Available annotation categories:")
		for _, c := range categories {
			fmt.Printf("  %s\n", c)
		}
		return nil
	}

	warnIfIndexStale()
	querier, store, err := createQuerier()
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()

	// Annotation-based queries
	if queryAnnotation != "" {
		nodes, _, err := querier.QueryByAnnotation(ctx, queryAnnotation, queryParams, queryKinds, queryLimit, 0)
		if err != nil {
			return err
		}
		printFilteredSymbols("annotation", queryAnnotation, nodes)
		return nil
	}
	if queryLayer != "" {
		nodes, _, err := querier.QueryByLayer(ctx, queryLayer, queryLimit, 0)
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

	// --members: list fields and methods of a class
	if queryMembers {
		methods, candidates, _, _, err := querier.QueryClassMembers(ctx, args[0], queryLimit)
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

	nodes, _, err := querier.QuerySymbol(ctx, args[0], opts)
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

	node, candidates, inheritedFrom, err := querier.ResolveFunctionWithInheritance(ctx, args[0])
	if err != nil {
		return err
	}

	// Handle ambiguous matches: interactive selection
	if node == nil && len(candidates) > 0 {
		selected := promptSelectFunction(candidates, args[0])
		if selected == nil {
			return nil
		}
		selectedQN, _ := selected.Properties["qualified_name"].(string)
		// Re-query with fully qualified name to resolve to a unique class
		node, _, inheritedFrom, err = querier.ResolveFunctionWithInheritance(ctx, selectedQN)
		if err != nil {
			return err
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

	// Filter by declared_type when resolved via inheritance fallback (reverse only)
	if inheritedFrom != "" && direction == model.Incoming {
		subgraph = service.FilterSubgraphByDeclaredType(subgraph, node.ID, inheritedFrom)
	}

	if len(subgraph.Nodes) == 0 {
		fmt.Printf("No %s found for %q\n", dirLabel, args[0])
		return nil
	}

	// Collect SQL queries before mode filtering (queries are not mode-filtered)
	queries, _ := querier.CollectQueries(ctx, subgraph.Nodes)

	fmt.Printf("%s of %q (depth=%d, minConfidence=%.2f):\n\n", strings.ToUpper(dirLabel[:1])+dirLabel[1:], args[0], callchainDepth, callchainMinConf)

	// Apply mode filters before rendering
	if callchainMode == "dry" || callchainMode == "compact" {
		subgraph = service.FilterCoreSubgraph(subgraph)
		subgraph = service.FilterDrySubgraph(subgraph)
	}
	if callchainMode == "compact" {
		subgraph = service.CompactSubgraphEdges(subgraph)
	}
	if callchainMode == "full" {
		subgraph = service.AssembleChainNodes(subgraph)
	}

	// Add root node to subgraph for tree rendering
	subgraph.Nodes = append([]model.Node{*node}, subgraph.Nodes...)

	printCallTree(subgraph, args[0], callchainDepth)
	fmt.Printf("\nTotal: %d %s\n", len(subgraph.Nodes), dirLabel)
	printQueries(queries)
	var modeHint string
	switch callchainMode {
	case "full":
		// no hint
	case "compact":
		modeHint = "ℹ [mode=compact] Showing compact call chain (edges merged, log/exception removed). Use --mode=full to see all nodes."
	case "dry":
		modeHint = "ℹ [mode=dry] Showing dry call chain (log/exception removed, properties trimmed). Use --mode=full to see all nodes."
	default:
		modeHint = "ℹ [mode=core] Showing core call chain (DISPATCHES pruned). Use --mode=full to see all nodes including accessors and externals."
	}
	if modeHint != "" {
		fmt.Println(modeHint)
	}
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
	node, candidates, inheritedFrom, err := querier.ResolveFunctionWithInheritance(ctx, args[0])
	if err != nil {
		return err
	}
	if node == nil && len(candidates) > 0 {
		selected := promptSelectFunction(candidates, args[0])
		if selected == nil {
			return nil
		}
		selectedQN, _ := selected.Properties["qualified_name"].(string)
		// Re-query with fully qualified name to resolve to a unique class
		node, _, inheritedFrom, err = querier.ResolveFunctionWithInheritance(ctx, selectedQN)
		if err != nil {
			return err
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

	if inheritedFrom != "" {
		subgraph = service.FilterSubgraphByDeclaredType(subgraph, node.ID, inheritedFrom)
	}

	if len(subgraph.Nodes) == 0 {
		fmt.Printf("No callers affected by changes to %q\n", args[0])
		return nil
	}

	fmt.Printf("Impact analysis for %q (depth=%d, minConfidence=%.2f):\n\n", args[0], impactDepth, impactMinConf)
	subgraph.Nodes = append([]model.Node{*node}, subgraph.Nodes...)
	printCallTree(subgraph, args[0], impactDepth)
	fmt.Printf("\nTotal: %d affected callers\n", len(subgraph.Nodes)-1)

	// Affected entry points from analyze cache
	nodeIDs := make([]string, 0, len(subgraph.Nodes))
	for _, n := range subgraph.Nodes {
		nodeIDs = append(nodeIDs, n.ID)
	}
	repoPath := projectDir()
	affectedRoutes, hint := querier.QueryAffectedRoutes(ctx, nodeIDs, repoPath)
	if len(affectedRoutes) > 0 {
		fmt.Printf("\nAffected entry points (%d):\n", len(affectedRoutes))
		for _, r := range affectedRoutes {
			label := r.EntryFunction
			if r.Route != "" {
				fmt.Printf("  %-6s %-35s ← %-40s [%s]\n", r.Method, r.Route, label, r.EntryType)
			} else {
				fmt.Printf("  %-42s ← %-40s [%s]\n", r.EntryFunction, r.FilePath, r.EntryType)
			}
		}
	}
	if hint != "" {
		fmt.Printf("\nℹ %s\n", hint)
	}

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
func runRoutes(cmd *cobra.Command, args []string) error {
	_, store, err := createQuerier()
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()
	routes, err := store.QueryAllByKind(ctx, constants.KindRoute, 0)
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
	warnIfIndexStale()
	querier, store, err := createQuerier()
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()
	chain, err := querier.QueryRouteChain(ctx, args[0], traceMethod, traceDepth)
	if err != nil {
		return err
	}

	subgraph := &model.Subgraph{Nodes: chain.Nodes, Edges: chain.Edges}

	// Apply mode filters
	if traceMode == "dry" || traceMode == "compact" {
		subgraph = service.FilterCoreSubgraph(subgraph)
		subgraph = service.FilterDrySubgraph(subgraph)
	}
	if traceMode == "compact" {
		subgraph = service.CompactSubgraphEdges(subgraph)
	}

	fmt.Printf("Route: %s %s\n", chain.Method, chain.Route)
	fmt.Printf("Chain (%d nodes):\n\n", len(subgraph.Nodes))

	// Set display mode for tree rendering
	callchainMode = traceMode
	printCallTree(subgraph, chain.Route, traceDepth)
	fmt.Printf("\nTotal: %d nodes\n", len(subgraph.Nodes))

	printQueries(chain.Queries)

	var modeHint string
	switch traceMode {
	case "full":
		// no hint
	case "compact":
		modeHint = "ℹ [mode=compact] Showing compact call chain (edges merged, log/exception removed). Use --mode=full to see all nodes."
	case "dry":
		modeHint = "ℹ [mode=dry] Showing dry call chain (log/exception removed, properties trimmed). Use --mode=full to see all nodes."
	default:
		modeHint = "ℹ [mode=core] Showing core call chain (DISPATCHES pruned). Use --mode=full to see all nodes including accessors and externals."
	}
	if modeHint != "" {
		fmt.Println(modeHint)
	}
	return nil
}

func runSearch(cmd *cobra.Command, args []string) error {
	querier, store, err := createQuerier()
	if err != nil {
		return err
	}
	defer store.Close()

	results, _, err := querier.SearchFTS(context.Background(), args[0], queryLimit, 0)
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

func runUsages(cmd *cobra.Command, args []string) error {
	querier, store, err := createQuerier()
	if err != nil {
		return err
	}
	defer store.Close()

	symbolQualifiedName := args[0]
	results, _, err := querier.QueryUsages(context.Background(), symbolQualifiedName, usagesLimit, 0)
	if err != nil {
		return err
	}

	if len(results) == 0 {
		fmt.Printf("No usages found for constant: %s\n", symbolQualifiedName)
		return nil
	}

	fmt.Printf("Usages of constant %s:\n\n", symbolQualifiedName)
	for _, usage := range results {
		functionName, _ := usage.Function.Properties["qualified_name"].(string)
		if functionName == "" {
			functionName, _ = usage.Function.Properties["name"].(string)
		}
		filePath, _ := usage.Function.Properties["file_path"].(string)
		refKind := usage.RefKind
		if refKind == "" {
			refKind = "unknown"
		}
		fmt.Printf("  %-50s %s:%d [%s]\n", functionName, filePath, usage.RefLine, refKind)
	}
	fmt.Printf("\nTotal: %d usage(s)\n", len(results))
	return nil
}
