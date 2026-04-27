// Package mcp implements the MCP Server for AI agent integration.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/kirovcaptain/FlashCodeGraph/internal/config"
	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/service"
	"github.com/kirovcaptain/FlashCodeGraph/internal/status"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/branch"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/crossindex"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/falkor"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/lock"
)

// StoreFactory creates a GraphStore for a given project path.
// This allows the MCP Server to work with multiple projects.
type StoreFactory func(cfg *config.Config, repoPath string) (storage.GraphStore, error)

// Server wraps the MCP server with FCG services.
type Server struct {
	mcpServer    *server.MCPServer
	storeFactory StoreFactory
}

// NewServer creates an MCP server with all FCG tools registered.
func NewServer(storeFactory StoreFactory) *Server {
	srv := &Server{
		storeFactory: storeFactory,
	}

	srv.mcpServer = server.NewMCPServer(
		"FlashCodeGraph",
		"0.1.0",
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(true, false),
	)

	srv.registerTools()
	srv.registerResources()

	return srv
}

// ServeStdio starts the MCP server on stdio transport.
func (srv *Server) ServeStdio() error {
	return server.ServeStdio(srv.mcpServer)
}

// createIndexer creates an Indexer for a specific project path.
func (srv *Server) createIndexer(repoPath string) (*service.Indexer, storage.GraphStore, error) {
	if !filepath.IsAbs(repoPath) {
		return nil, nil, fmt.Errorf("path must be absolute, got: %s", repoPath)
	}

	cfg, err := config.Load(repoPath)
	if err != nil {
		cfg = config.DefaultConfig()
	}

	store, err := srv.storeFactory(cfg, repoPath)
	if err != nil {
		return nil, nil, fmt.Errorf("create store: %w", err)
	}

	dataDir := filepath.Join(repoPath, ".fcg", "fingerprints")
	branchName := branch.DetectBranch(repoPath)
	branchManager := branch.NewManager(dataDir)
	branchManager.EnsureBranchDir(branchName)

	fingerprintStore := branchManager.FingerprintStore()
	indexLock := lock.NewNoopLock()

	// Create cross-project index
	crossIndexPath := filepath.Join(config.GlobalDir(), "cross_project_index.json")
	crossIndex := crossindex.NewJSONStore(crossIndexPath)
	if err := crossIndex.Load(); err != nil {
		return nil, nil, fmt.Errorf("load cross-project index: %w", err)
	}

	indexer := service.NewIndexer(store, fingerprintStore, indexLock, cfg, nil, crossIndex)

	return indexer, store, nil
}

// createQuerier creates a Querier for the given project path and optional branch.
// If branch is empty, the current git branch is auto-detected.
func (srv *Server) createQuerier(path, branchName string) (*service.Querier, storage.GraphStore, error) {
	if path == "" {
		return nil, nil, fmt.Errorf("path is required, specify the project to query")
	}
	if !filepath.IsAbs(path) {
		return nil, nil, fmt.Errorf("path must be absolute, got: %s", path)
	}
	cfg, _ := config.Load(path)
	if branchName != "" {
		cfg.Storage.Branch = branchName
	}
	store, err := srv.storeFactory(cfg, path)
	if err != nil {
		return nil, nil, fmt.Errorf("open store for %s: %w", path, err)
	}
	// Check if graph exists to prevent FalkorDB from auto-creating empty graphs
	if falkorStore, ok := store.(*falkor.Store); ok {
		if !falkorStore.GraphExists(context.Background()) {
			store.Close()
			detectedBranch := branchName
			if detectedBranch == "" {
				detectedBranch = branch.DetectBranch(path)
			}
			return nil, nil, fmt.Errorf("no index found for %s (branch: %s). Run index_repository first", path, detectedBranch)
		}
	}
	return service.NewQuerier(store), store, nil
}

// registerTools registers all MCP tools.
func (srv *Server) registerTools() {
	srv.mcpServer.AddTool(mcp.NewTool("list_projects",
		mcp.WithDescription("List all indexed projects with their paths, branches, and database backends. Use this to discover which projects are available for querying."),
	), srv.handleListProjects)

	srv.mcpServer.AddTool(mcp.NewTool("index_repository",
		mcp.WithDescription("Index a code repository to build a knowledge graph. Run this first before using any query tools. Supports Java, Go, Python, TypeScript, JavaScript projects. Use force=true to rebuild from scratch."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the repository")),
		mcp.WithString("branch", mcp.Description("Git branch to index (optional, auto-detected if omitted)")),
		mcp.WithBoolean("force", mcp.Description("Force full re-index")),
	), srv.handleIndexRepository)

	srv.mcpServer.AddTool(mcp.NewTool("check_index_status",
		mcp.WithDescription("Check if the project index is up-to-date with source files. Call this once before starting analysis tasks. Returns counts of added/modified/deleted files since last index. If stale, ask the user whether to re-index (full or incremental)."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the project")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional, auto-detected if omitted)")),
	), srv.handleCheckIndexStatus)

	srv.mcpServer.AddTool(mcp.NewTool("query_symbol",
		mcp.WithDescription("Find a symbol (function, class, interface) by exact name. Use this when you know the precise symbol name. Returns symbol details including file path and kind."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Symbol name to search")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 20)")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the project")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional, auto-detected from current branch if omitted)")),
	), srv.handleQuerySymbol)

	srv.mcpServer.AddTool(mcp.NewTool("query_call_chain",
		mcp.WithDescription("Query the call chain of a function — what it calls (callees) or who calls it (callers with reverse=true). Use this to understand code flow, trace execution paths, or find dependencies."),
		mcp.WithString("function", mcp.Required(), mcp.Description("Function name")),
		mcp.WithNumber("depth", mcp.Description("Max traversal depth (default 3)")),
		mcp.WithNumber("min_confidence", mcp.Description("Min confidence filter (default 0)")),
		mcp.WithBoolean("reverse", mcp.Description("Show callers instead of callees")),
		mcp.WithBoolean("include_unresolved", mcp.Description("Include UNRESOLVED_CALL hint edges (default false)")),
		mcp.WithString("mode", mcp.Description("Display mode: 'dry' (default, core + remove log/exception, trim properties), 'core' (prune DISPATCHES/accessors/externals), 'compact' (dry + merge duplicate edges), 'full' (show all)")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the project")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional, auto-detected from current branch if omitted)")),
	), srv.handleQueryCallChain)

	srv.mcpServer.AddTool(mcp.NewTool("query_cross_chain",
		mcp.WithDescription("Query cross-service call relationships for a function. Returns project-level view: which services are called, via what protocol (http/dubbo/grpc), and through which callers. Use this to understand service dependencies and cross-service architecture."),
		mcp.WithString("function", mcp.Required(), mcp.Description("Entry function name")),
		mcp.WithNumber("depth", mcp.Description("Max traversal depth (default 3)")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the project")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional)")),
	), srv.handleQueryCrossChain)

	srv.mcpServer.AddTool(mcp.NewTool("impact_analysis",
		mcp.WithDescription("Analyze the impact of changing a symbol — find all direct and indirect callers that would be affected. Use this before refactoring to assess risk."),
		mcp.WithString("symbol", mcp.Required(), mcp.Description("Symbol name to analyze")),
		mcp.WithNumber("depth", mcp.Description("Max traversal depth (default 3)")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the project")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional, auto-detected from current branch if omitted)")),
	), srv.handleImpactAnalysis)

	srv.mcpServer.AddTool(mcp.NewTool("search",
		mcp.WithDescription("Search symbols by partial or fuzzy name match. Use this when you don't know the exact name — e.g. 'find anything related to order processing'."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 20)")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the project")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional, auto-detected from current branch if omitted)")),
	), srv.handleSearch)

	srv.mcpServer.AddTool(mcp.NewTool("query_class_methods",
		mcp.WithDescription("List all methods of a class. Use this to understand class structure and responsibilities."),
		mcp.WithString("class_name", mcp.Required(), mcp.Description("Class name to query")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 50)")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the project")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional)")),
	), srv.handleQueryClassMembers)

	srv.mcpServer.AddTool(mcp.NewTool("overview",
		mcp.WithDescription("Get project overview — total functions, classes, interfaces, edges, and file counts. Use this to understand project scale and structure."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the project")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional, auto-detected from current branch if omitted)")),
	), srv.handleOverview)

	srv.mcpServer.AddTool(mcp.NewTool("query_dependencies",
		mcp.WithDescription("Query inheritance and dependency relationships for a symbol — IMPORTS, EXTENDS, IMPLEMENTS edges. Use this to understand class hierarchy and module dependencies."),
		mcp.WithString("symbol", mcp.Required(), mcp.Description("Symbol name")),
		mcp.WithString("kind", mcp.Description("Relation kind: IMPORTS, EXTENDS, IMPLEMENTS, CALLS (default CALLS)")),
		mcp.WithBoolean("reverse", mcp.Description("Show incoming instead of outgoing")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the project")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional, auto-detected from current branch if omitted)")),
	), srv.handleQueryDependencies)

	srv.mcpServer.AddTool(mcp.NewTool("query_by_annotation",
		mcp.WithDescription("Find symbols annotated with a specific annotation (e.g. Service, RestController, XxlJob)"),
		mcp.WithString("annotation", mcp.Required(), mcp.Description("Annotation name")),
		mcp.WithString("params", mcp.Description("Filter by annotation params (substring match, e.g. 'completeSettlementJob')")),
		mcp.WithString("kind", mcp.Description("Filter by symbol kind (Function, Class, Interface)")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 50)")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the project")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional)")),
	), srv.handleQueryByAnnotation)

	srv.mcpServer.AddTool(mcp.NewTool("query_by_layer",
		mcp.WithDescription("Find symbols by architectural layer (controller/service/repository/model)"),
		mcp.WithString("layer", mcp.Required(), mcp.Description("Layer name: controller, service, repository, model, component, config")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 50)")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the project")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional)")),
	), srv.handleQueryByLayer)

	srv.mcpServer.AddTool(mcp.NewTool("query_route_chain",
		mcp.WithDescription("Trace an HTTP route through its full processing chain — from controller to service to repository. Use this to understand API request handling flow."),
		mcp.WithString("route", mcp.Required(), mcp.Description("Route path (e.g. /api/users)")),
		mcp.WithString("method", mcp.Description("HTTP method filter (GET/POST/etc)")),
		mcp.WithNumber("max_depth", mcp.Description("Max traversal depth (default 10)")),
		mcp.WithString("mode", mcp.Description("Display mode: 'dry' (default, core + remove log/exception, trim properties), 'core' (prune DISPATCHES/accessors/externals), 'compact' (dry + merge duplicate edges), 'full' (show all)")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the project")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional)")),
	), srv.handleQueryRouteChain)

	srv.mcpServer.AddTool(mcp.NewTool("analyze_repository",
		mcp.WithDescription("Run graph analysis to detect entry points (HTTP endpoints, CLI commands, remote clients) and classify them. Must run after index_repository."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the repository")),
		mcp.WithString("scope", mcp.Description("Analysis scope: entries, process, all (default: all)")),
	), srv.handleAnalyzeRepository)

	srv.mcpServer.AddTool(mcp.NewTool("query_entry_points",
		mcp.WithDescription("List all detected entry points — HTTP endpoints, CLI commands, remote clients, dead code. Run analyze_repository first. Use type filter to narrow results."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the repository")),
		mcp.WithString("type", mcp.Description("Filter by type: http_endpoint, cli_command, suspected_dead, unknown_entry")),
		mcp.WithNumber("limit", mcp.Description("Max results (default: 50)")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional, auto-detected if omitted)")),
	), srv.handleQueryEntryPoints)

	srv.mcpServer.AddTool(mcp.NewTool("query_call_forest",
		mcp.WithDescription("Query call forest from entry points with tree structure"),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the repository")),
		mcp.WithString("type", mcp.Description("Filter entry type")),
		mcp.WithNumber("depth", mcp.Description("Max tree depth (default: 5)")),
		mcp.WithNumber("min_confidence", mcp.Description("Min confidence filter (default: 0.0)")),
		mcp.WithBoolean("include_unresolved", mcp.Description("Include UNRESOLVED_CALL hint edges (default false)")),
		mcp.WithString("mode", mcp.Description("Display mode: 'dry' (default, core + remove log/exception, trim properties), 'core' (prune DISPATCHES/accessors/externals), 'compact' (dry + merge duplicate edges), 'full' (show all)")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional, auto-detected if omitted)")),
	), srv.handleQueryCallForest)

	srv.mcpServer.AddTool(mcp.NewTool("locate_function",
		mcp.WithDescription("Map file+line locations to their enclosing function or class. Use after grep to convert line numbers into symbol names for impact analysis. Returns the innermost enclosing symbol (Function > Class > Interface > File)."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the project")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional)")),
		mcp.WithString("locations", mcp.Required(), mcp.Description("JSON array of {\"file\":\"relative/path\",\"line\":N} objects")),
	), srv.handleLocateFunction)
}

// registerResources registers MCP resources.
func (srv *Server) registerResources() {
	srv.mcpServer.AddResource(mcp.NewResource(
		"fcg://overview",
		"Project overview statistics",
		mcp.WithResourceDescription("Node counts, edge counts, language distribution"),
		mcp.WithMIMEType("application/json"),
	), srv.handleResourceOverview)
}

// Tool handlers

func (srv *Server) handleListProjects(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	fcgDir := config.GlobalDir()
	registry, err := storage.NewRegistry(fcgDir)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("load registry: %v", err)), nil
	}
	entries := registry.List()
	if len(entries) == 0 {
		return mcp.NewToolResultText("[]"), nil
	}
	type projectInfo struct {
		Name     string `json:"name"`
		Path     string `json:"path"`
		Database string `json:"database"`
		Branch   string `json:"branch,omitempty"`
		Status   *status.Status `json:"status,omitempty"`
	}
	results := make([]projectInfo, 0, len(entries))
	for _, e := range entries {
		s := status.Read(e.Path)
		var sp *status.Status
		if s.IndexTimestamp > 0 {
			sp = &s
		}
		results = append(results, projectInfo{
			Name: e.Name, Path: e.Path, Database: e.Database, Branch: e.Branch, Status: sp,
		})
	}
	data, _ := json.Marshal(results)
	return mcp.NewToolResultText(string(data)), nil
}

func (srv *Server) handleIndexRepository(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	if path == "" {
		return mcp.NewToolResultError("path is required"), nil
	}
	force, _ := request.GetArguments()["force"].(bool)

	indexer, store, err := srv.createIndexer(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create indexer: %v", err)), nil
	}
	defer store.Close()

	branchName, _ := request.GetArguments()["branch"].(string)
	if branchName == "" {
		branchName = branch.DetectBranch(path)
	}
	result, err := indexer.Index(ctx, path, branchName, force, nil)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("indexing failed: %v", err)), nil
	}
	status.MarkIndexed(path)

	resultJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (srv *Server) handleCheckIndexStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	if path == "" {
		return mcp.NewToolResultError("path is required"), nil
	}
	branchName, _ := request.GetArguments()["branch"].(string)

	indexStatus, err := service.CheckProjectStaleness(ctx, path, branchName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("check index status: %v", err)), nil
	}

	resultJSON, _ := json.Marshal(indexStatus)
	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (srv *Server) handleQuerySymbol(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, _ := request.GetArguments()["name"].(string)
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}
	limit := intArg(request, "limit", 20)

	path, _ := request.GetArguments()["path"].(string)
	branchName, _ := request.GetArguments()["branch"].(string)
	querier, store, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()
	nodes, err := querier.QuerySymbol(ctx, name, model.QueryOpts{Limit: limit})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	resultJSON, _ := json.Marshal(nodes)
	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (srv *Server) handleQueryCallChain(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	funcName, _ := request.GetArguments()["function"].(string)
	if funcName == "" {
		return mcp.NewToolResultError("function is required"), nil
	}
	depth := intArg(request, "depth", 3)
	minConfidence := floatArg(request, "min_confidence", 0)
	reverse, _ := request.GetArguments()["reverse"].(bool)
	includeUnresolved, _ := request.GetArguments()["include_unresolved"].(bool)
	mode := stringArg(request, "mode", "dry")

	direction := model.Outgoing
	if reverse {
		direction = model.Incoming
	}

	path, _ := request.GetArguments()["path"].(string)
	branchName, _ := request.GetArguments()["branch"].(string)
	querier, store, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()
	subgraph, err := querier.QueryCallChainEx(ctx, funcName, direction, depth, minConfidence, includeUnresolved)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if mode != "full" {
		subgraph = service.FilterCoreSubgraph(subgraph)
		subgraph = service.PruneDeclaredTypeDispatches(subgraph)
	}
	if mode == "dry" || mode == "compact" {
		subgraph = service.FilterDrySubgraph(subgraph)
	}
	if mode == "compact" {
		subgraph = service.CompactSubgraphEdges(subgraph)
	}

	warning := checkStalenessWarning(ctx, path, branchName)
	resultJSON, _ := json.Marshal(subgraph)
	result := injectWarning(resultJSON, warning)
	var hint string
	switch mode {
	case "full":
		// no hint
	case "compact":
		hint = "[mode=compact] Showing compact call chain (edges merged, log/exception removed). Use mode='full' to see all nodes."
	case "dry":
		hint = "[mode=dry] Showing dry call chain (log/exception/accessors removed, properties trimmed). Use mode='full' to see all nodes."
	default:
		hint = "[mode=core] Showing core call chain (getters/setters and external dependencies are hidden, DISPATCHES pruned). Use mode='full' to see all nodes."
	}
	if hint != "" {
		result = injectHint([]byte(result), hint)
	}

	// Cross-service hints: detect cross_service=true edges and append follow-up commands
	var crossServiceHints []string
	for _, edge := range subgraph.Edges {
		crossService, _ := edge.Properties["cross_service"].(bool)
		if !crossService {
			continue
		}
		targetProject, _ := edge.Properties["target_project"].(string)
		targetHandler, _ := edge.Properties["target_handler"].(string)
		viaRoute, _ := edge.Properties["via_route"].(string)
		protocol, _ := edge.Properties["protocol"].(string)
		if targetProject != "" && targetHandler != "" {
			crossServiceHint := fmt.Sprintf("Cross-service call detected: %s (%s). To trace the target handler, run: query_call_chain(function=\"%s\", path=\"%s\")",
				viaRoute, protocol, targetHandler, targetProject)
			crossServiceHints = append(crossServiceHints, crossServiceHint)
		}
	}
	if len(crossServiceHints) > 0 {
		// Deduplicate hints
		seen := make(map[string]bool)
		var uniqueHints []string
		for _, crossServiceHint := range crossServiceHints {
			if !seen[crossServiceHint] {
				seen[crossServiceHint] = true
				uniqueHints = append(uniqueHints, crossServiceHint)
			}
		}
		hintsJSON, _ := json.Marshal(uniqueHints)
		result = injectField([]byte(result), "cross_service_hints", hintsJSON)
	}

	return mcp.NewToolResultText(result), nil
}


func (srv *Server) handleQueryCrossChain(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	functionName, _ := request.GetArguments()["function"].(string)
	if functionName == "" {
		return mcp.NewToolResultError("function is required"), nil
	}
	depth := intArg(request, "depth", 3)
	projectPath, _ := request.GetArguments()["path"].(string)
	branchName, _ := request.GetArguments()["branch"].(string)

	querier, store, err := srv.createQuerier(projectPath, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()

	result, err := querier.QueryCrossChain(ctx, functionName, depth, projectPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	resultJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (srv *Server) handleImpactAnalysis(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	symbolName, _ := request.GetArguments()["symbol"].(string)
	if symbolName == "" {
		return mcp.NewToolResultError("symbol is required"), nil
	}
	depth := intArg(request, "depth", 3)

	path, _ := request.GetArguments()["path"].(string)
	branchName, _ := request.GetArguments()["branch"].(string)
	querier, store, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()
	subgraph, err := querier.ImpactAnalysis(ctx, symbolName, depth)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Collect all node IDs for affected routes lookup
	nodeIDs := make([]string, 0, len(subgraph.Nodes)+1)
	for _, n := range subgraph.Nodes {
		nodeIDs = append(nodeIDs, n.ID)
	}

	repoPath, _ := filepath.Abs(path)
	affectedRoutes, routeHint := querier.QueryAffectedRoutes(ctx, nodeIDs, repoPath)

	result := map[string]any{
		"nodes": subgraph.Nodes,
		"edges": subgraph.Edges,
	}
	if len(affectedRoutes) > 0 {
		result["affected_routes"] = affectedRoutes
	}
	if routeHint != "" {
		result["hint"] = routeHint
	}

	// Cross-service hints for impact analysis
	var impactCrossServiceHints []string
	for _, node := range subgraph.Nodes {
		filePath, _ := node.Properties["file_path"].(string)
		if filePath == "[cross-service]" {
			nodeName, _ := node.Properties["name"].(string)
			targetProject, _ := node.Properties["target_project"].(string)
			if targetProject != "" {
				impactCrossServiceHints = append(impactCrossServiceHints,
					fmt.Sprintf("Impact reaches cross-service boundary: %s in %s. Run impact_analysis on target project to continue.", nodeName, targetProject))
			}
		}
	}
	if len(impactCrossServiceHints) > 0 {
		result["cross_service_hints"] = impactCrossServiceHints
	}

	warning := checkStalenessWarning(ctx, path, branchName)
	resultJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(injectWarning(resultJSON, warning)), nil
}

func (srv *Server) handleSearch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	queryText, _ := request.GetArguments()["query"].(string)
	if queryText == "" {
		return mcp.NewToolResultError("query is required"), nil
	}
	limit := intArg(request, "limit", 20)

	path, _ := request.GetArguments()["path"].(string)
	branchName, _ := request.GetArguments()["branch"].(string)
	querier, store, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()
	results, err := querier.SearchFTS(ctx, queryText, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	resultJSON, _ := json.Marshal(results)
	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (srv *Server) handleQueryClassMembers(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	className, _ := request.GetArguments()["class_name"].(string)
	if className == "" {
		return mcp.NewToolResultError("class_name is required"), nil
	}
	limit := intArg(request, "limit", 50)

	path, _ := request.GetArguments()["path"].(string)
	branchName, _ := request.GetArguments()["branch"].(string)
	querier, store, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()

	methods, candidates, fields, err := querier.QueryClassMembers(ctx, className, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if methods == nil && candidates == nil {
		return mcp.NewToolResultError(fmt.Sprintf("class %q not found", className)), nil
	}
	if candidates != nil {
		result := map[string]any{
			"ambiguous":  true,
			"message":    fmt.Sprintf("Multiple classes match %q, use qualified name", className),
			"candidates": candidates,
		}
		resultJSON, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(resultJSON)), nil
	}

	result := map[string]any{
		"methods": methods,
		"fields":  fields,
	}
	resultJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (srv *Server) handleOverview(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	branchName, _ := request.GetArguments()["branch"].(string)
	querier, store, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()
	stats, err := querier.Overview(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	result := map[string]any{
		"stats": stats,
	}
	s := status.Read(path)
	if s.IndexTimestamp > 0 {
		result["indexed_at"] = s.IndexTimestamp
	}
	if s.AnalyzeTimestamp > 0 {
		result["analyzed_at"] = s.AnalyzeTimestamp
	}

	resultJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (srv *Server) handleQueryDependencies(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	symbolName, _ := request.GetArguments()["symbol"].(string)
	if symbolName == "" {
		return mcp.NewToolResultError("symbol is required"), nil
	}
	kindStr, _ := request.GetArguments()["kind"].(string)
	if kindStr == "" {
		kindStr = "CALLS"
	}
	reverse, _ := request.GetArguments()["reverse"].(bool)

	relKind := model.RelationKind(kindStr)
	direction := model.Outgoing
	if reverse {
		direction = model.Incoming
	}

	path, _ := request.GetArguments()["path"].(string)
	branchName, _ := request.GetArguments()["branch"].(string)
	querier, store, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()
	// Choose query kinds based on relation kind
	var queryKinds []string
	switch relKind {
	case model.RelCalls:
		queryKinds = []string{constants.KindFunction}
	case model.RelImports, model.RelExtends, model.RelImplements:
		queryKinds = []string{constants.KindClass, constants.KindInterface}
	}
	nodes, err := querier.QuerySymbol(ctx, symbolName, model.QueryOpts{Kinds: queryKinds, Limit: 10})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(nodes) == 0 {
		return mcp.NewToolResultText("[]"), nil
	}
	target := &nodes[0]

	edges, err := querier.QueryEdges(ctx, target.ID, target.Kind, relKind, direction)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	resultJSON, _ := json.Marshal(edges)
	return mcp.NewToolResultText(string(resultJSON)), nil
}

// Resource handlers

func (srv *Server) handleResourceOverview(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      "fcg://overview",
			MIMEType: "application/json",
			Text:     `{"hint": "Use the 'overview' tool with a 'path' parameter to get project statistics."}`,
		},
	}, nil
}

func (srv *Server) handleQueryByAnnotation(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	annotation, _ := request.GetArguments()["annotation"].(string)
	if annotation == "" {
		return mcp.NewToolResultError("annotation is required"), nil
	}
	params, _ := request.GetArguments()["params"].(string)
	kind, _ := request.GetArguments()["kind"].(string)
	limit := intArg(request, "limit", 50)
	path, _ := request.GetArguments()["path"].(string)
	branchName, _ := request.GetArguments()["branch"].(string)

	querier, store, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()
	nodes, err := querier.QueryByAnnotation(ctx, annotation, params, kind, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, _ := json.Marshal(nodes)
	return mcp.NewToolResultText(string(data)), nil
}

func (srv *Server) handleQueryByLayer(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	layer, _ := request.GetArguments()["layer"].(string)
	if layer == "" {
		return mcp.NewToolResultError("layer is required"), nil
	}
	limit := intArg(request, "limit", 50)
	path, _ := request.GetArguments()["path"].(string)
	branch, _ := request.GetArguments()["branch"].(string)

	querier, store, err := srv.createQuerier(path, branch)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()
	nodes, err := querier.QueryByLayer(ctx, layer, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, _ := json.Marshal(nodes)
	return mcp.NewToolResultText(string(data)), nil
}

func (srv *Server) handleQueryRouteChain(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	route, _ := request.GetArguments()["route"].(string)
	if route == "" {
		return mcp.NewToolResultError("route is required"), nil
	}
	method, _ := request.GetArguments()["method"].(string)
	maxDepth := intArg(request, "max_depth", 10)
	mode := stringArg(request, "mode", "dry")
	path, _ := request.GetArguments()["path"].(string)
	branch, _ := request.GetArguments()["branch"].(string)

	querier, store, err := srv.createQuerier(path, branch)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()
	chain, err := querier.QueryRouteChain(ctx, route, method, maxDepth)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if mode != "full" {
		chain = service.FilterCoreRouteChain(chain)
	}
	data, _ := json.Marshal(chain)
	result := string(data)
	if mode != "full" {
		result = injectHint(data, "Showing core route chain (getters/setters and external dependencies are hidden). Use mode='full' to see all nodes.")
	}
	return mcp.NewToolResultText(result), nil
}

// Helpers

func intArg(request mcp.CallToolRequest, key string, defaultValue int) int {
	if value, ok := request.GetArguments()[key].(float64); ok {
		return int(value)
	}
	return defaultValue
}

func floatArg(request mcp.CallToolRequest, key string, defaultValue float64) float64 {
	if value, ok := request.GetArguments()[key].(float64); ok {
		return value
	}
	return defaultValue
}

// stringArg extracts a string argument from an MCP request with a default value.
func stringArg(request mcp.CallToolRequest, key string, defaultValue string) string {
	if value, ok := request.GetArguments()[key].(string); ok && value != "" {
		return value
	}
	return defaultValue
}

func (srv *Server) handleAnalyzeRepository(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	if path == "" || !filepath.IsAbs(path) {
		return mcp.NewToolResultError("absolute path is required"), nil
	}
	scope, _ := request.GetArguments()["scope"].(string)
	if scope == "" {
		scope = "all"
	}

	_, store, err := srv.createQuerier(path, "")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create store: %v", err)), nil
	}
	defer store.Close()

	analyzer := service.NewAnalyzer(store)
	forest, err := analyzer.BuildCallForest(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("build forest: %v", err)), nil
	}

	entries, err := analyzer.ClassifyRoots(ctx, forest)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("classify: %v", err)), nil
	}
	analyzer.WriteEntryPoints(ctx, entries)

	byType := map[string]int{}
	for _, e := range entries {
		byType[e.EntryType]++
	}
	result := map[string]any{"entries": len(entries), "entries_by_type": byType}
	if scope == "all" || scope == "process" {
		layerMap := analyzer.BuildLayerMap(ctx)
		analyzer.ClearAnalysisData(ctx)
		processCount, stepCount := analyzer.WriteProcesses(ctx, entries, forest, 10, layerMap)
		result["processes"] = processCount
		result["total_steps"] = stepCount
	}
	status.MarkAnalyzed(path)

	data, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(data)), nil
}

func (srv *Server) handleQueryEntryPoints(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	if path == "" || !filepath.IsAbs(path) {
		return mcp.NewToolResultError("absolute path is required"), nil
	}
	entryType, _ := request.GetArguments()["type"].(string)
	limit := intArg(request, "limit", 50)
	branchName, _ := request.GetArguments()["branch"].(string)

	_, store, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create store: %v", err)), nil
	}
	defer store.Close()

	nodes, err := store.QueryAllByKind(ctx, constants.KindFunction, 0)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var results []map[string]any
	for _, n := range nodes {
		et, _ := n.Properties["entry_type"].(string)
		if et == "" {
			continue
		}
		if entryType != "" && et != entryType {
			continue
		}
		results = append(results, map[string]any{
			"id":             n.ID,
			"name":           n.Properties["name"],
			"qualified_name": n.Properties["qualified_name"],
			"file_path":      n.Properties["file_path"],
			"entry_type":     et,
			"score":          n.Properties["entry_point_score"],
		})
		if limit > 0 && len(results) >= limit {
			break
		}
	}

	data, _ := json.Marshal(results)
	return mcp.NewToolResultText(string(data)), nil
}

func (srv *Server) handleQueryCallForest(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	if path == "" || !filepath.IsAbs(path) {
		return mcp.NewToolResultError("absolute path is required"), nil
	}
	entryType, _ := request.GetArguments()["type"].(string)
	depth := intArg(request, "depth", 5)
	mode := stringArg(request, "mode", "dry")
	branchName, _ := request.GetArguments()["branch"].(string)

	_, store, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create store: %v", err)), nil
	}
	defer store.Close()

	analyzer := service.NewAnalyzer(store)
	forest, err := analyzer.BuildCallForest(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("build forest: %v", err)), nil
	}

	// Read persisted entry points instead of re-classifying
	funcs, err := store.QueryAllByKind(ctx, constants.KindFunction, 0)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	layerMap := analyzer.BuildLayerMap(ctx)
	var trees []map[string]any
	for _, f := range funcs {
		et, _ := f.Properties["entry_type"].(string)
		if et == "" || et == "suspected_dead" {
			continue
		}
		if entryType != "" && et != entryType {
			continue
		}
		name, _ := f.Properties["name"].(string)
		process := analyzer.TraceProcess(forest, f.ID, name, depth, layerMap)
		tree := processStepToMap(process)
		if mode != "full" {
			tree = filterCoreForestTree(tree)
		}
		trees = append(trees, tree)
	}

	data, _ := json.Marshal(trees)
	result := string(data)
	if mode != "full" {
		result = injectHint(data, "Showing core call forest (getters/setters and external dependencies are hidden). Use mode='full' to see all nodes.")
	}
	return mcp.NewToolResultText(result), nil
}

func processStepToMap(step *service.ProcessStep) map[string]any {
	m := map[string]any{
		"name":       step.Name,
		"id":         step.NodeID,
		"file_path":  step.FilePath,
		"confidence": step.Confidence,
		"depth":      step.Depth,
	}
	if step.Layer != "" {
		m["layer"] = step.Layer
	}
	if step.IsGetter {
		m["is_getter"] = true
	}
	if step.IsSetter {
		m["is_setter"] = true
	}
	if len(step.Children) > 0 {
		children := make([]map[string]any, 0, len(step.Children))
		for _, child := range step.Children {
			children = append(children, processStepToMap(child))
		}
		m["children"] = children
	}
	return m
}

// filterCoreForestTree removes accessor/external nodes from a forest tree map.
// Skipped nodes' children are promoted to the parent level to preserve connectivity.
func filterCoreForestTree(tree map[string]any) map[string]any {
	result := make(map[string]any)
	for k, v := range tree {
		if k != "children" {
			result[k] = v
		}
	}
	children, _ := tree["children"].([]map[string]any)
	if len(children) > 0 {
		filtered := collectCoreChildren(children)
		if len(filtered) > 0 {
			result["children"] = filtered
		}
	}
	return result
}

// collectCoreChildren filters child nodes for core mode. Excluded nodes (accessor/external)
// are removed and their children promoted recursively, so chains of excluded nodes are fully collapsed.
func collectCoreChildren(children []map[string]any) []map[string]any {
	var result []map[string]any
	for _, child := range children {
		if isCoreExcluded(child) {
			if grandchildren, ok := child["children"].([]map[string]any); ok {
				result = append(result, collectCoreChildren(grandchildren)...)
			}
		} else {
			result = append(result, filterCoreForestTree(child))
		}
	}
	return result
}

// isCoreExcluded returns true if a forest tree node should be excluded in core mode (accessor or external).
func isCoreExcluded(node map[string]any) bool {
	if node["is_getter"] == true || node["is_setter"] == true {
		return true
	}
	fp, _ := node["file_path"].(string)
	return fp == "[external]" || fp == ""
}

func (srv *Server) handleLocateFunction(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repoPath, _ := request.GetArguments()["path"].(string)
	branchName, _ := request.GetArguments()["branch"].(string)
	locationsJSON, _ := request.GetArguments()["locations"].(string)

	querier, store, err := srv.createQuerier(repoPath, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()

	var requests []model.LocateRequest
	if err := json.Unmarshal([]byte(locationsJSON), &requests); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid locations JSON: %v", err)), nil
	}

	results, err := querier.LocateFunction(ctx, repoPath, requests)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	out, _ := json.MarshalIndent(results, "", "  ")
	return mcp.NewToolResultText(string(out)), nil
}

// checkStalenessWarning checks if the project index is stale and returns a warning message.
// Returns empty string if the index is up-to-date or on any error (fail-open: never block queries).
func checkStalenessWarning(ctx context.Context, repoPath string, branchName string) string {
	indexStatus, err := service.CheckProjectStaleness(ctx, repoPath, branchName)
	if err != nil {
		return ""
	}
	return service.FormatStalenessWarning(indexStatus)
}

// injectWarning prepends a "warning" field into a JSON object string.
// If warning is empty, returns the original JSON unchanged.
func injectWarning(originalJSON []byte, warning string) string {
	if warning == "" {
		return string(originalJSON)
	}
	// Insert "warning":"..." right after the opening brace
	if len(originalJSON) > 0 && originalJSON[0] == '{' {
		warningJSON, _ := json.Marshal(warning)
		return "{\"warning\":" + string(warningJSON) + "," + string(originalJSON[1:])
	}
	return string(originalJSON)
}

// injectHint prepends a "hint" field into a JSON object string.
func injectHint(originalJSON []byte, hint string) string {
	if hint == "" {
		return string(originalJSON)
	}
	hintJSON, _ := json.Marshal(hint)
	if len(originalJSON) > 0 && originalJSON[0] == '{' {
		return "{\"hint\":" + string(hintJSON) + "," + string(originalJSON[1:])
	}
	if len(originalJSON) > 0 && originalJSON[0] == '[' {
		return "{\"hint\":" + string(hintJSON) + ",\"items\":" + string(originalJSON) + "}"
	}
	return string(originalJSON)
}

// injectField adds a JSON field to the beginning of a JSON object.
func injectField(originalJSON []byte, fieldName string, fieldValue []byte) string {
	if len(originalJSON) > 0 && originalJSON[0] == '{' {
		return "{\"" + fieldName + "\":" + string(fieldValue) + "," + string(originalJSON[1:])
	}
	return string(originalJSON)
}
