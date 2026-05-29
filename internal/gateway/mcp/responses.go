package mcp

import (
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/service"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
)

// listResponse wraps array results with branch metadata.
type listResponse[T any] struct {
	Branch string `json:"branch,omitempty"`
	Data   []T    `json:"data"`
}

// pagedResponse wraps paginated list results with total count metadata.
type pagedResponse[T any] struct {
	Branch string `json:"branch,omitempty"`
	Total  int    `json:"total"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
	Data   []T    `json:"data"`
}

// --- Index / Status ---

type indexRepositoryResponse struct {
	Branch string `json:"branch"`
	*model.IndexResult
}

type checkIndexStatusResponse struct {
	Branch string `json:"branch"`
	*service.IndexStatus
}

// --- Call chain ---

type callChainResponse struct {
	Branch            string            `json:"branch,omitempty"`
	Warning           string            `json:"warning,omitempty"`
	Hint              string            `json:"hint,omitempty"`
	Nodes             []model.Node      `json:"nodes"`
	Edges             []model.ChainEdge `json:"edges"`
	Queries           []model.ChainNode `json:"queries,omitempty"`
	TruncatedNodes    []string          `json:"truncated_nodes,omitempty"`
	CrossServiceHints []string          `json:"cross_service_hints,omitempty"`
	CrossProjectHints []string          `json:"cross_project_hints,omitempty"`
}

type compactCallChainResponse struct {
	Branch            string                   `json:"branch,omitempty"`
	Warning           string                   `json:"warning,omitempty"`
	Hint              string                   `json:"hint,omitempty"`
	Nodes             []model.Node             `json:"nodes"`
	Edges             []model.CompactChainEdge `json:"edges"`
	Queries           []model.ChainNode        `json:"queries,omitempty"`
	TruncatedNodes    []string                 `json:"truncated_nodes,omitempty"`
	CrossServiceHints []string                 `json:"cross_service_hints,omitempty"`
	CrossProjectHints []string                 `json:"cross_project_hints,omitempty"`
}

// --- Cross chain ---

type crossChainResponse struct {
	Branch string `json:"branch,omitempty"`
	*service.CrossChainResult
}

// --- Impact analysis ---

type impactAnalysisResponse struct {
	Branch            string                  `json:"branch,omitempty"`
	Warning           string                  `json:"warning,omitempty"`
	Nodes             []model.Node            `json:"nodes"`
	Edges             []model.ChainEdge       `json:"edges"`
	AffectedRoutes    []service.AffectedRoute `json:"affected_routes,omitempty"`
	Hint              string                  `json:"hint,omitempty"`
	CrossServiceHints []string                `json:"cross_service_hints,omitempty"`
}

// --- Class members (two schemas) ---

type classMembersResponse struct {
	Branch  string           `json:"branch,omitempty"`
	Kind    string           `json:"kind"`
	Methods []model.Node     `json:"methods"`
	Fields  []model.FieldInfo `json:"fields"`
}

type classMembersAmbiguousResponse struct {
	Branch     string       `json:"branch,omitempty"`
	Ambiguous  bool         `json:"ambiguous"`
	Message    string       `json:"message"`
	Candidates []model.Node `json:"candidates"`
}

// --- Overview ---

type overviewResponse struct {
	Branch     string            `json:"branch,omitempty"`
	Stats      *model.GraphStats `json:"stats"`
	IndexedAt  int64             `json:"indexed_at,omitempty"`
	AnalyzedAt int64             `json:"analyzed_at,omitempty"`
}

// --- Dependencies ---

type dependencyNodeInfo struct {
	Kind          string `json:"kind"`
	QualifiedName string `json:"qualified_name,omitempty"`
	FilePath      string `json:"file_path,omitempty"`
}

type dependenciesResponse struct {
	Branch string                        `json:"branch,omitempty"`
	Edges  []model.Edge                  `json:"edges"`
	Nodes  map[string]dependencyNodeInfo  `json:"nodes"`
}

// --- Routes ---

type routeEntry struct {
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Handler     string   `json:"handler"`
	Middlewares []string `json:"middlewares,omitempty"`
	Framework   string   `json:"framework"`
}

type routeChainResponse struct {
	Branch         string            `json:"branch,omitempty"`
	Warning        string            `json:"warning,omitempty"`
	Hint           string            `json:"hint,omitempty"`
	Route          string            `json:"route"`
	Method         string            `json:"method"`
	Middlewares    []middlewareEntry  `json:"middlewares,omitempty"`
	Nodes          []model.Node      `json:"nodes"`
	Edges          []model.ChainEdge `json:"edges"`
	Queries        []model.ChainNode `json:"queries,omitempty"`
	TruncatedNodes []string          `json:"truncated_nodes,omitempty"`
}

type compactRouteChainResponse struct {
	Branch         string                   `json:"branch,omitempty"`
	Warning        string                   `json:"warning,omitempty"`
	Hint           string                   `json:"hint,omitempty"`
	Route          string                   `json:"route"`
	Method         string                   `json:"method"`
	Middlewares    []middlewareEntry         `json:"middlewares,omitempty"`
	Nodes          []model.Node             `json:"nodes"`
	Edges          []model.CompactChainEdge `json:"edges"`
	Queries        []model.ChainNode        `json:"queries,omitempty"`
	TruncatedNodes []string                 `json:"truncated_nodes,omitempty"`
}

type middlewareEntry struct {
	Name     string `json:"name"`
	FilePath string `json:"file_path,omitempty"`
	Line     int    `json:"line,omitempty"`
}

// --- Analyze repository ---

type analyzeRepositoryResponse struct {
	Branch        string         `json:"branch"`
	Entries       int            `json:"entries"`
	EntriesByType map[string]int `json:"entries_by_type"`
	Processes     int            `json:"processes,omitempty"`
	TotalSteps    int            `json:"total_steps,omitempty"`
}

// --- Entry points ---

type entryPointEntry struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	FilePath      string `json:"file_path"`
	EntryType     string `json:"entry_type"`
	Score         any    `json:"score,omitempty"`
}

// --- Call forest ---

type callForestResponse struct {
	Branch string           `json:"branch,omitempty"`
	Hint   string           `json:"hint,omitempty"`
	Data   []map[string]any `json:"data"`
}

// Ensure imports are used.
var (
	_ = (*model.IndexResult)(nil)
	_ = (*service.IndexStatus)(nil)
	_ = (*storage.SearchResult)(nil)
)
