// Package mcp implements the MCP Server for AI agent integration.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

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
	crossIndex, err := crossindex.New(cfg.CrossProjectIndex.Backend, cfg.CrossProjectIndex.SQLitePath, config.GlobalDir())
	if err != nil {
		store.Close()
		return nil, nil, fmt.Errorf("load cross-project index: %w", err)
	}

	indexer := service.NewIndexer(store, fingerprintStore, indexLock, cfg, nil, crossIndex)

	return indexer, store, nil
}

// createQuerier creates a Querier for the given project path and optional branch.
// If branch is empty, the current git branch is auto-detected.
// Returns the resolved branch name alongside the querier and store.
func (srv *Server) createQuerier(path, branchName string) (*service.Querier, storage.GraphStore, string, error) {
	if path == "" {
		return nil, nil, "", fmt.Errorf("path is required, specify the project to query")
	}
	if !filepath.IsAbs(path) {
		return nil, nil, "", fmt.Errorf("path must be absolute, got: %s", path)
	}
	cfg, err := config.Load(path)
	if err != nil {
		cfg = config.DefaultConfig()
	}
	resolvedBranch := branchName
	if resolvedBranch == "" {
		resolvedBranch = branch.DetectBranch(path)
	}
	cfg.Storage.Branch = resolvedBranch
	store, err := srv.storeFactory(cfg, path)
	if err != nil {
		return nil, nil, "", fmt.Errorf("open store for %s: %w", path, err)
	}
	// Check if graph exists to prevent FalkorDB from auto-creating empty graphs
	if falkorStore, ok := store.(*falkor.Store); ok {
		if !falkorStore.GraphExists(context.Background()) {
			store.Close()
			return nil, nil, "", fmt.Errorf("no index found for %s (branch: %s). Run index_repository first", path, resolvedBranch)
		}
	}
	return service.NewQuerier(store), store, resolvedBranch, nil
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
		mcp.WithNumber("offset", mcp.Description("Skip first N results for pagination (default 0)")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the project")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional, auto-detected from current branch if omitted)")),
	), srv.handleQuerySymbol)

	srv.mcpServer.AddTool(mcp.NewTool("query_call_chain",
		mcp.WithDescription("Query the call chain of a function — what it calls (callees) or who calls it (callers with reverse=true). Use this to understand code flow, trace execution paths, or find dependencies. When depth truncates the traversal, the response includes 'truncated_nodes' listing boundary nodes with their unexpanded callee count — use this to decide whether to query deeper."),
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
		mcp.WithString("query", mcp.Required(), mcp.Description("Symbol name or keyword to search for (e.g. 'OrderService', 'createUser'). NOT a file path.")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 20)")),
		mcp.WithNumber("offset", mcp.Description("Skip first N results for pagination (default 0)")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the project root directory")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional, auto-detected from current branch if omitted)")),
	), srv.handleSearch)

	srv.mcpServer.AddTool(mcp.NewTool("query_class_members",
		mcp.WithDescription("List all fields and methods of a class. Use this to understand class structure, dependencies, and responsibilities."),
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
		mcp.WithDescription("Query relationships for a symbol — CALLS, IMPORTS, EXTENDS, IMPLEMENTS edges. Use this to understand class hierarchy, module dependencies, and direct call relationships."),
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
		mcp.WithNumber("offset", mcp.Description("Skip first N results for pagination (default 0)")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the project")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional)")),
	), srv.handleQueryByAnnotation)

	srv.mcpServer.AddTool(mcp.NewTool("query_by_layer",
		mcp.WithDescription("Find symbols by architectural layer (controller/service/repository/model)"),
		mcp.WithString("layer", mcp.Required(), mcp.Description("Layer name: controller, service, repository, model, component, config")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 50)")),
		mcp.WithNumber("offset", mcp.Description("Skip first N results for pagination (default 0)")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the project")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional)")),
	), srv.handleQueryByLayer)

	srv.mcpServer.AddTool(mcp.NewTool("query_routes",
		mcp.WithDescription("List all routes in a project. Returns method, path, handler function, and framework for each route. Defaults to HTTP routes only; use type parameter to include CLI or MCP tool routes."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the project root directory")),
		mcp.WithString("method", mcp.Description("Filter by HTTP method (GET/POST/PUT/DELETE)")),
		mcp.WithString("type", mcp.Description("Filter by route type: http (default), cli, mcp, all")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional)")),
	), srv.handleQueryRoutes)

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
		mcp.WithString("type", mcp.Description("Filter by type: http_endpoint, cli_command, mcp_tool, suspected_dead, unknown_entry")),
		mcp.WithNumber("limit", mcp.Description("Max results (default: 50)")),
		mcp.WithNumber("offset", mcp.Description("Skip first N results for pagination (default 0)")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional, auto-detected if omitted)")),
	), srv.handleQueryEntryPoints)

	srv.mcpServer.AddTool(mcp.NewTool("query_call_forest",
		mcp.WithDescription("Query call forest from entry points with tree structure. Use this to visualize full call trees rooted at HTTP endpoints, CLI commands, or other entry points detected by analyze_repository."),
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

	srv.mcpServer.AddTool(mcp.NewTool("query_usages",
		mcp.WithDescription("Query all references to a static constant (enum constant, interface constant, or class static final field). Returns the functions that reference it, along with line numbers and reference kinds (field_access or switch_case)."),
		mcp.WithString("symbol", mcp.Required(), mcp.Description("Qualified name of the constant (e.g. 'com.example.Status.ACTIVE')")),
		mcp.WithNumber("limit", mcp.Description("Max results (default: no limit)")),
		mcp.WithNumber("offset", mcp.Description("Skip first N results for pagination (default 0)")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the project")),
		mcp.WithString("branch", mcp.Description("Git branch name (optional, auto-detected if omitted)")),
	), srv.handleQueryUsages)
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
		Name     string         `json:"name"`
		Path     string         `json:"path"`
		Database string         `json:"database"`
		Branch   string         `json:"branch,omitempty"`
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

	resp := indexRepositoryResponse{Branch: branchName, IndexResult: result}
	resultJSON, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (srv *Server) handleCheckIndexStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	if path == "" {
		return mcp.NewToolResultError("path is required"), nil
	}
	branchName, _ := request.GetArguments()["branch"].(string)
	if branchName == "" {
		branchName = branch.DetectBranch(path)
	}

	indexStatus, err := service.CheckProjectStaleness(ctx, path, branchName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("check index status: %v", err)), nil
	}

	resp := checkIndexStatusResponse{Branch: branchName, IndexStatus: indexStatus}
	resultJSON, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (srv *Server) handleQuerySymbol(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, _ := request.GetArguments()["name"].(string)
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}
	limit := intArg(request, "limit", 20)
	offset := intArg(request, "offset", 0)

	path, _ := request.GetArguments()["path"].(string)
	branchName, _ := request.GetArguments()["branch"].(string)
	querier, store, resolvedBranch, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()
	nodes, total, err := querier.QuerySymbol(ctx, name, model.QueryOpts{Limit: limit, Offset: offset})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	resp := pagedResponse[model.Node]{Branch: resolvedBranch, Total: total, Offset: offset, Limit: limit, Data: nodes}
	resultJSON, _ := json.Marshal(resp)
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
	querier, store, resolvedBranch, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()
	subgraph, err := querier.QueryCallChainEx(ctx, funcName, direction, depth, minConfidence, includeUnresolved)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Collect queries before mode filtering (queries are not mode-filtered)
	queries, _ := querier.CollectQueries(ctx, subgraph.Nodes)

	if mode != "full" {
		subgraph = service.FilterCoreSubgraph(subgraph)
	}
	if mode == "dry" || mode == "compact" {
		subgraph = service.FilterDrySubgraph(subgraph)
	}
	if mode == "compact" {
		subgraph = service.CompactSubgraphEdges(subgraph)
	}
	if mode == "full" {
		subgraph = service.AssembleChainNodes(subgraph)
	}

	warning := checkStalenessWarning(ctx, path, branchName)
	normalizeQueryNodeProperties(subgraph)

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

	// Cross-service hints
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
		seen := make(map[string]bool)
		var uniqueHints []string
		for _, crossServiceHint := range crossServiceHints {
			if !seen[crossServiceHint] {
				seen[crossServiceHint] = true
				uniqueHints = append(uniqueHints, crossServiceHint)
			}
		}
		crossServiceHints = uniqueHints
	}

	// Cross-project hints
	var crossProjectHints []string
	for _, node := range subgraph.Nodes {
		filePath, _ := node.Properties["file_path"].(string)
		if filePath != constants.FilePathCrossProject {
			continue
		}
		qualifiedName, _ := node.Properties["qualified_name"].(string)
		sourceProject, _ := node.Properties["source_project"].(string)
		nodeName, _ := node.Properties["name"].(string)
		if sourceProject != "" && qualifiedName != "" {
			crossProjectHint := fmt.Sprintf("Cross-project call detected: %s (from dependency project). To trace the implementation, run: query_call_chain(function=\"%s\", path=\"%s\")",
				qualifiedName, nodeName, sourceProject)
			crossProjectHints = append(crossProjectHints, crossProjectHint)
		}
	}

	var resultJSON []byte
	if mode == "compact" {
		resp := compactCallChainResponse{
			Branch:            resolvedBranch,
			Warning:           warning,
			Hint:              hint,
			Nodes:             subgraph.Nodes,
			Edges:             model.EdgesToCompactChainEdges(subgraph.Edges),
			Queries:           queries,
			TruncatedNodes:    subgraph.TruncatedNodes,
			CrossServiceHints: crossServiceHints,
			CrossProjectHints: crossProjectHints,
		}
		resultJSON, _ = json.Marshal(resp)
	} else {
		resp := callChainResponse{
			Branch:            resolvedBranch,
			Warning:           warning,
			Hint:              hint,
			Nodes:             subgraph.Nodes,
			Edges:             model.EdgesToChainEdges(subgraph.Edges),
			Queries:           queries,
			TruncatedNodes:    subgraph.TruncatedNodes,
			CrossServiceHints: crossServiceHints,
			CrossProjectHints: crossProjectHints,
		}
		resultJSON, _ = json.Marshal(resp)
	}

	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (srv *Server) handleQueryCrossChain(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	functionName, _ := request.GetArguments()["function"].(string)
	if functionName == "" {
		return mcp.NewToolResultError("function is required"), nil
	}
	depth := intArg(request, "depth", 3)
	projectPath, _ := request.GetArguments()["path"].(string)
	branchName, _ := request.GetArguments()["branch"].(string)

	querier, store, resolvedBranch, err := srv.createQuerier(projectPath, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()

	result, err := querier.QueryCrossChain(ctx, functionName, depth, projectPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	resp := crossChainResponse{Branch: resolvedBranch, CrossChainResult: result}
	resultJSON, _ := json.Marshal(resp)
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
	querier, store, resolvedBranch, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()
	subgraph, targetID, err := querier.ImpactAnalysis(ctx, symbolName, depth)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Collect all node IDs for affected routes lookup (include target function itself)
	nodeIDs := make([]string, 0, len(subgraph.Nodes)+1)
	if targetID != "" {
		nodeIDs = append(nodeIDs, targetID)
	}
	for _, n := range subgraph.Nodes {
		nodeIDs = append(nodeIDs, n.ID)
	}

	repoPath, _ := filepath.Abs(path)
	affectedRoutes, routeHint := querier.QueryAffectedRoutes(ctx, nodeIDs, repoPath)

	// Cross-service hints for impact analysis
	var impactCrossServiceHints []string
	for _, node := range subgraph.Nodes {
		filePath, _ := node.Properties["file_path"].(string)
		if filePath == constants.FilePathCrossService {
			nodeName, _ := node.Properties["name"].(string)
			targetProject, _ := node.Properties["target_project"].(string)
			if targetProject != "" {
				impactCrossServiceHints = append(impactCrossServiceHints,
					fmt.Sprintf("Impact reaches cross-service boundary: %s in %s. Run impact_analysis on target project to continue.", nodeName, targetProject))
			}
		}
	}

	warning := checkStalenessWarning(ctx, path, branchName)
	resp := impactAnalysisResponse{
		Branch:            resolvedBranch,
		Warning:           warning,
		Nodes:             subgraph.Nodes,
		Edges:             model.EdgesToChainEdges(subgraph.Edges),
		AffectedRoutes:    affectedRoutes,
		Hint:              routeHint,
		CrossServiceHints: impactCrossServiceHints,
	}
	resultJSON, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (srv *Server) handleSearch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	queryText, _ := request.GetArguments()["query"].(string)
	if queryText == "" {
		return mcp.NewToolResultError("query is required"), nil
	}
	limit := intArg(request, "limit", 20)
	offset := intArg(request, "offset", 0)

	path, _ := request.GetArguments()["path"].(string)
	branchName, _ := request.GetArguments()["branch"].(string)
	querier, store, resolvedBranch, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()
	results, total, err := querier.SearchFTS(ctx, queryText, limit, offset)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	resp := pagedResponse[storage.SearchResult]{Branch: resolvedBranch, Total: total, Offset: offset, Limit: limit, Data: results}
	resultJSON, _ := json.Marshal(resp)
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
	querier, store, resolvedBranch, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()

	methods, candidates, fields, kind, err := querier.QueryClassMembers(ctx, className, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if methods == nil && candidates == nil {
		return mcp.NewToolResultError(fmt.Sprintf("class %q not found", className)), nil
	}
	if candidates != nil {
		resp := classMembersAmbiguousResponse{
			Branch:     resolvedBranch,
			Ambiguous:  true,
			Message:    fmt.Sprintf("Multiple classes match %q, use qualified name", className),
			Candidates: candidates,
		}
		resultJSON, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(resultJSON)), nil
	}

	resp := classMembersResponse{
		Branch:  resolvedBranch,
		Kind:    kind,
		Methods: methods,
		Fields:  fields,
	}
	resultJSON, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (srv *Server) handleOverview(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	branchName, _ := request.GetArguments()["branch"].(string)
	querier, store, resolvedBranch, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()
	stats, err := querier.Overview(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	s := status.Read(path)
	resp := overviewResponse{
		Branch:     resolvedBranch,
		Stats:      stats,
		IndexedAt:  s.IndexTimestamp,
		AnalyzedAt: s.AnalyzeTimestamp,
	}
	resultJSON, _ := json.Marshal(resp)
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
	querier, store, resolvedBranch, err := srv.createQuerier(path, branchName)
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
	nodes, _, err := querier.QuerySymbol(ctx, symbolName, model.QueryOpts{Kinds: queryKinds, Limit: 10})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(nodes) == 0 {
		resp := dependenciesResponse{Branch: resolvedBranch}
		resultJSON, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(resultJSON)), nil
	}
	target := &nodes[0]

	edges, err := querier.QueryEdges(ctx, target.ID, target.Kind, relKind, direction)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Enrich edges with node info
	nodeIDs := map[string]bool{}
	for _, e := range edges {
		nodeIDs[e.SourceID] = true
		nodeIDs[e.TargetID] = true
	}
	nodeMap := map[string]dependencyNodeInfo{}
	for id := range nodeIDs {
		node, err := store.QueryNodeByID(ctx, id)
		if err == nil && node != nil {
			info := dependencyNodeInfo{Kind: node.Kind}
			if qn, ok := node.Properties["qualified_name"].(string); ok {
				info.QualifiedName = qn
			}
			if fp, ok := node.Properties["file_path"].(string); ok {
				info.FilePath = fp
			}
			nodeMap[id] = info
		}
	}
	resp := dependenciesResponse{Branch: resolvedBranch, Edges: edges, Nodes: nodeMap}
	resultJSON, _ := json.Marshal(resp)
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
	offset := intArg(request, "offset", 0)
	path, _ := request.GetArguments()["path"].(string)
	branchName, _ := request.GetArguments()["branch"].(string)

	querier, store, resolvedBranch, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()
	nodes, total, err := querier.QueryByAnnotation(ctx, annotation, params, kind, limit, offset)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := pagedResponse[model.Node]{Branch: resolvedBranch, Total: total, Offset: offset, Limit: limit, Data: nodes}
	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func (srv *Server) handleQueryByLayer(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	layer, _ := request.GetArguments()["layer"].(string)
	if layer == "" {
		return mcp.NewToolResultError("layer is required"), nil
	}
	limit := intArg(request, "limit", 50)
	offset := intArg(request, "offset", 0)
	path, _ := request.GetArguments()["path"].(string)
	branchName, _ := request.GetArguments()["branch"].(string)

	querier, store, resolvedBranch, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()
	nodes, total, err := querier.QueryByLayer(ctx, layer, limit, offset)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := pagedResponse[model.Node]{Branch: resolvedBranch, Total: total, Offset: offset, Limit: limit, Data: nodes}
	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func (srv *Server) handleQueryRoutes(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	methodFilter, _ := request.GetArguments()["method"].(string)
	typeFilter, _ := request.GetArguments()["type"].(string)
	branchName, _ := request.GetArguments()["branch"].(string)

	_, store, resolvedBranch, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create store: %v", err)), nil
	}
	defer store.Close()

	routes, err := store.QueryAllByKind(ctx, constants.KindRoute, 0)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Default to http for backward compatibility
	if typeFilter == "" {
		typeFilter = "http"
	}
	typeFilter = strings.ToLower(typeFilter)

	var results []routeEntry
	for _, r := range routes {
		method, _ := r.Properties["method"].(string)
		framework, _ := r.Properties["framework"].(string)

		// Type filter
		if typeFilter != "all" {
			switch typeFilter {
			case "http":
				if method == "CLI" || method == "TOOL" {
					continue
				}
			case "cli":
				if method != "CLI" && framework != "cobra" {
					continue
				}
			case "mcp":
				if method != "TOOL" && framework != "mcp" {
					continue
				}
			}
		}

		if methodFilter != "" && !strings.EqualFold(method, methodFilter) {
			continue
		}
		pathPattern, _ := r.Properties["path_pattern"].(string)
		handlerMethod, _ := r.Properties["handler_method"].(string)
		middlewaresRaw, _ := r.Properties["middlewares"].(string)
		var middlewares []string
		if middlewaresRaw != "" {
			middlewares = strings.Split(middlewaresRaw, ",")
		}
		results = append(results, routeEntry{
			Method:      method,
			Path:        pathPattern,
			Handler:     handlerMethod,
			Middlewares: middlewares,
			Framework:   framework,
		})
	}

	resp := listResponse[routeEntry]{Branch: resolvedBranch, Data: results}
	data, _ := json.Marshal(resp)
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
	branchName, _ := request.GetArguments()["branch"].(string)

	querier, store, resolvedBranch, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()
	chain, err := querier.QueryRouteChain(ctx, route, method, maxDepth)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Mode filtering on the subgraph portion (same pipeline as handleQueryCallChain)
	routeSubgraph := &model.Subgraph{Nodes: chain.Nodes, Edges: chain.Edges}
	if mode != "full" {
		routeSubgraph = service.FilterCoreSubgraph(routeSubgraph)
	}
	if mode == "dry" || mode == "compact" {
		routeSubgraph = service.FilterDrySubgraph(routeSubgraph)
	}

	warning := checkStalenessWarning(ctx, path, branchName)

	var hint string
	switch mode {
	case "full":
		// no hint
	case "compact":
		hint = "[mode=compact] Showing compact route chain (edges merged, log/exception removed). Use mode='full' to see all nodes."
	case "dry":
		hint = "[mode=dry] Showing dry route chain (log/exception/accessors removed, properties trimmed). Use mode='full' to see all nodes."
	default:
		hint = "[mode=core] Showing core route chain (getters/setters and external dependencies are hidden, DISPATCHES pruned). Use mode='full' to see all nodes."
	}

	// Convert middleware nodes to response entries
	var middlewareEntries []middlewareEntry
	for _, middlewareNode := range chain.Middlewares {
		name, _ := middlewareNode.Properties["name"].(string)
		filePath, _ := middlewareNode.Properties["file_path"].(string)
		line, _ := middlewareNode.Properties["start_line"].(int)
		if lineFloat, ok := middlewareNode.Properties["start_line"].(float64); ok {
			line = int(lineFloat)
		}
		middlewareEntries = append(middlewareEntries, middlewareEntry{
			Name:     name,
			FilePath: filePath,
			Line:     line,
		})
	}

	var resultJSON []byte
	if mode == "compact" {
		routeSubgraph = service.CompactSubgraphEdges(routeSubgraph)
		resp := compactRouteChainResponse{
			Branch:         resolvedBranch,
			Warning:        warning,
			Hint:           hint,
			Route:          chain.Route,
			Method:         chain.Method,
			Middlewares:    middlewareEntries,
			Nodes:          routeSubgraph.Nodes,
			Edges:          model.EdgesToCompactChainEdges(routeSubgraph.Edges),
			Queries:        chain.Queries,
			TruncatedNodes: routeSubgraph.TruncatedNodes,
		}
		resultJSON, _ = json.Marshal(resp)
	} else {
		resp := routeChainResponse{
			Branch:         resolvedBranch,
			Warning:        warning,
			Hint:           hint,
			Route:          chain.Route,
			Method:         chain.Method,
			Middlewares:    middlewareEntries,
			Nodes:          routeSubgraph.Nodes,
			Edges:          model.EdgesToChainEdges(routeSubgraph.Edges),
			Queries:        chain.Queries,
			TruncatedNodes: routeSubgraph.TruncatedNodes,
		}
		resultJSON, _ = json.Marshal(resp)
	}
	return mcp.NewToolResultText(string(resultJSON)), nil
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

	_, store, resolvedBranch, err := srv.createQuerier(path, "")
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
	resp := analyzeRepositoryResponse{
		Branch:        resolvedBranch,
		Entries:       len(entries),
		EntriesByType: byType,
	}
	if scope == "all" || scope == "process" {
		layerMap := analyzer.BuildLayerMap(ctx)
		analyzer.ClearAnalysisData(ctx)
		processCount, stepCount := analyzer.WriteProcesses(ctx, entries, forest, 10, layerMap)
		resp.Processes = processCount
		resp.TotalSteps = stepCount
	}
	status.MarkAnalyzed(path)

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func (srv *Server) handleQueryEntryPoints(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	if path == "" || !filepath.IsAbs(path) {
		return mcp.NewToolResultError("absolute path is required"), nil
	}
	entryType, _ := request.GetArguments()["type"].(string)
	limit := intArg(request, "limit", 50)
	offset := intArg(request, "offset", 0)
	branchName, _ := request.GetArguments()["branch"].(string)

	_, store, resolvedBranch, err := srv.createQuerier(path, branchName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create store: %v", err)), nil
	}
	defer store.Close()

	var nodes []model.Node
	if entryType != "" {
		nodes, err = store.QueryNodesByProperty(ctx, constants.KindFunction, "entry_type", entryType, storage.MatchExact, 0)
	} else {
		nodes, err = store.QueryNodesByProperty(ctx, constants.KindFunction, "entry_type", "", storage.MatchNotEmpty, 0)
	}
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	allResults := make([]entryPointEntry, 0, len(nodes))
	for _, node := range nodes {
		name, _ := node.Properties["name"].(string)
		qualifiedName, _ := node.Properties["qualified_name"].(string)
		filePath, _ := node.Properties["file_path"].(string)
		entryTypeValue, _ := node.Properties["entry_type"].(string)
		allResults = append(allResults, entryPointEntry{
			ID:            node.ID,
			Name:          name,
			QualifiedName: qualifiedName,
			FilePath:      filePath,
			EntryType:     entryTypeValue,
			Score:         node.Properties["entry_point_score"],
		})
	}

	total := len(allResults)
	pageResults := allResults
	if offset < total {
		end := total
		if limit > 0 && offset+limit < total {
			end = offset + limit
		}
		pageResults = allResults[offset:end]
	} else {
		pageResults = []entryPointEntry{}
	}

	resp := pagedResponse[entryPointEntry]{Branch: resolvedBranch, Total: total, Offset: offset, Limit: limit, Data: pageResults}
	data, _ := json.Marshal(resp)
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

	_, store, resolvedBranch, err := srv.createQuerier(path, branchName)
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
	var funcs []model.Node
	if entryType != "" {
		funcs, err = store.QueryNodesByProperty(ctx, constants.KindFunction, "entry_type", entryType, storage.MatchExact, 0)
	} else {
		funcs, err = store.QueryNodesByProperty(ctx, constants.KindFunction, "entry_type", "", storage.MatchNotEmpty, 0)
	}
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	layerMap := analyzer.BuildLayerMap(ctx)
	var trees []map[string]any
	for _, f := range funcs {
		et, _ := f.Properties["entry_type"].(string)
		if et == "suspected_dead" {
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

	resp := callForestResponse{Branch: resolvedBranch, Data: trees}
	if mode != "full" {
		resp.Hint = "Showing core call forest (getters/setters and external dependencies are hidden). Use mode='full' to see all nodes."
	}
	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
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
	filePath, _ := node["file_path"].(string)
	return filePath == constants.FilePathExternal || filePath == ""
}

func (srv *Server) handleLocateFunction(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repoPath, _ := request.GetArguments()["path"].(string)
	branchName, _ := request.GetArguments()["branch"].(string)
	locationsJSON, _ := request.GetArguments()["locations"].(string)

	querier, store, resolvedBranch, err := srv.createQuerier(repoPath, branchName)
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

	resp := listResponse[model.LocateResult]{Branch: resolvedBranch, Data: results}
	out, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(out)), nil
}

func (srv *Server) handleQueryUsages(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	symbolQualifiedName, _ := request.GetArguments()["symbol"].(string)
	repoPath, _ := request.GetArguments()["path"].(string)
	branchName, _ := request.GetArguments()["branch"].(string)
	limitFloat, _ := request.GetArguments()["limit"].(float64)
	limit := int(limitFloat)
	offset := intArg(request, "offset", 0)

	querier, store, resolvedBranch, err := srv.createQuerier(repoPath, branchName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer store.Close()

	results, total, err := querier.QueryUsages(ctx, symbolQualifiedName, limit, offset)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	resp := pagedResponse[model.UsageResult]{Branch: resolvedBranch, Total: total, Offset: offset, Limit: limit, Data: results}
	out, _ := json.Marshal(resp)
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

func normalizeQueryNodeProperties(subgraph *model.Subgraph) {
	for index := range subgraph.Nodes {
		node := &subgraph.Nodes[index]
		if node.Kind != constants.KindQueryNode {
			continue
		}
		if tablesStr, ok := node.Properties["tables"].(string); ok && tablesStr != "" {
			node.Properties["tables"] = strings.Split(tablesStr, ",")
		}
		if conditionsStr, ok := node.Properties["conditions"].(string); ok && conditionsStr != "" {
			node.Properties["conditions"] = json.RawMessage(conditionsStr)
		}
	}
}
