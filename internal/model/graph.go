// Package model defines shared data types for FlashCodeGraph.
package model

import (
	"encoding/json"
	"time"
)

// RelationKind represents the type of relationship between nodes.
type RelationKind string

const (
	RelCalls       RelationKind = "CALLS"
	RelExtends     RelationKind = "EXTENDS"
	RelImplements  RelationKind = "IMPLEMENTS"
	RelImports     RelationKind = "IMPORTS"
	RelOverrides   RelationKind = "OVERRIDES"
	RelDispatches  RelationKind = "DISPATCHES"
	RelContains    RelationKind = "CONTAINS"
	RelMemberOf    RelationKind = "MEMBER_OF"
	RelHandles     RelationKind = "HANDLES"
	RelInjects     RelationKind = "INJECTS"
	RelDependsOn   RelationKind = "DEPENDS_ON"
	RelRemoteCallsRoute RelationKind = "REMOTE_CALLS_ROUTE"
	RelRemoteCallsExt   RelationKind = "REMOTE_CALLS_EXT"
	RelExecutes    RelationKind = "EXECUTES"
	RelFetches     RelationKind = "FETCHES"
	RelMiddleware  RelationKind = "MIDDLEWARE"
	RelStep        RelationKind = "STEP"
	RelHasAnnotation RelationKind = "HAS_ANNOTATION"
	RelUnresolvedCall RelationKind = "UNRESOLVED_CALL"
)

// Direction for graph traversal.
type Direction int

const (
	Outgoing Direction = iota
	Incoming
	Both
)

// Node is a generic graph node.
type Node struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Properties map[string]any    `json:"properties"`
}

// Edge is a generic graph edge.
type Edge struct {
	ID         string            `json:"id,omitempty"`
	SourceID   string            `json:"source_id"`
	TargetID   string            `json:"target_id"`
	Kind       RelationKind      `json:"kind"`
	SourceKind string            `json:"source_kind,omitempty"` // Source node type (Function/Class/Interface)
	Properties map[string]any    `json:"properties,omitempty"`
}

// QueryOpts controls query filtering.
type QueryOpts struct {
	Kinds         []string `json:"kinds,omitempty"`
	MinConfidence float64  `json:"min_confidence,omitempty"`
	Limit         int      `json:"limit,omitempty"`
}

// Subgraph is a partial graph result.
type Subgraph struct {
	Nodes          []Node   `json:"nodes"`
	Edges          []Edge   `json:"edges"`
	TruncatedNodes []string `json:"truncated_nodes,omitempty"`
}

// GraphStats holds aggregate statistics.
type GraphStats struct {
	NodeCount   int            `json:"node_count"`
	EdgeCount   int            `json:"edge_count"`
	FileCount   int            `json:"file_count"`
	NodesByKind map[string]int `json:"nodes_by_kind"`
	EdgesByKind map[string]int `json:"edges_by_kind"`
	FilesByLang map[string]int `json:"files_by_lang"`
	Frameworks  []string       `json:"frameworks,omitempty"`
}

// Fingerprint tracks file state for incremental indexing.
type Fingerprint struct {
	ModTime     int64  `json:"mod_time"`
	Size        int64  `json:"size"`
	ContentHash string `json:"content_hash"`
}

// LockInfo describes an active index lock.
type LockInfo struct {
	Holder     string    `json:"holder"`
	AcquiredAt time.Time `json:"acquired_at"`
	Repo       string    `json:"repo"`
	Branch     string    `json:"branch"`
	TTL        time.Duration `json:"ttl"`
}

// GraphReport contains data quality diagnostics for the graph.
type GraphReport struct {
	NodeCounts      map[string]int    `json:"node_counts"`
	EdgeCounts      map[string]int    `json:"edge_counts"`
	DuplicateNodes  []string          `json:"duplicate_nodes,omitempty"`
	OrphanNodes     []string          `json:"orphan_nodes,omitempty"`
	MissingFilePath []string          `json:"missing_file_path,omitempty"`
	EmptyNames      []string          `json:"empty_names,omitempty"`
	RouteDetails    []RouteDetail     `json:"route_details,omitempty"`
	QueryDetails    []QueryDetail     `json:"query_details,omitempty"`
	Functions       []SymbolDetail    `json:"functions,omitempty"`
	Classes         []SymbolDetail    `json:"classes,omitempty"`
	Interfaces      []SymbolDetail    `json:"interfaces,omitempty"`
	Edges           []EdgeDetail      `json:"edges,omitempty"`
	Issues          []string          `json:"issues,omitempty"`
}

// SymbolDetail describes a node for the report.
type SymbolDetail struct {
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name,omitempty"`
	FilePath      string `json:"file_path"`
	Line          int    `json:"line,omitempty"`
}

// EdgeDetail describes a single edge for the report.
type EdgeDetail struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

// RouteDetail describes a single route node.
type RouteDetail struct {
	Method      string `json:"method"`
	PathPattern string `json:"path_pattern"`
	Handler     string `json:"handler"`
}

// QueryDetail describes a single query node.
type QueryDetail struct {
	SQLText    string `json:"sql_text"`
	QueryType  string `json:"query_type"`
	Tables     string `json:"tables"`
	Caller     string `json:"caller"`
	BaseSQL    string `json:"base_sql,omitempty"`
	Conditions string `json:"conditions,omitempty"`
}

// ChainNode represents a node in a route chain with layer annotation.
type ChainNode struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name,omitempty"`
	Kind          string `json:"kind"`
	FilePath      string `json:"file_path"`
	Layer         string `json:"layer,omitempty"`
	StartLine     int    `json:"start_line,omitempty"`
	EndLine       int    `json:"end_line,omitempty"`
	IsGetter      bool   `json:"is_getter,omitempty"`
	IsSetter      bool   `json:"is_setter,omitempty"`
	// Query-specific fields (only populated for QueryNode entries in Queries slice)
	SQLText    string          `json:"sql_text,omitempty"`
	QueryType  string          `json:"query_type,omitempty"`
	Tables     json.RawMessage `json:"tables,omitempty"`
	BaseSQL    string          `json:"base_sql,omitempty"`
	Conditions json.RawMessage `json:"conditions,omitempty"`
}

// ChainEdge is a structured edge for MCP call chain output.
type ChainEdge struct {
	SourceID     string       `json:"source_id"`
	TargetID     string       `json:"target_id"`
	Kind         RelationKind `json:"kind"`
	Line         int          `json:"line,omitempty"`
	Confidence   float64      `json:"confidence,omitempty"`
	DeclaredType string       `json:"declared_type,omitempty"`
	FlowContext  string       `json:"flow_context,omitempty"`
	FlowLine     int          `json:"flow_line,omitempty"`
}

// CompactChainEdge is a ChainEdge with merged lines for compact mode.
type CompactChainEdge struct {
	SourceID     string       `json:"source_id"`
	TargetID     string       `json:"target_id"`
	Kind         RelationKind `json:"kind"`
	Lines        []int        `json:"lines,omitempty"`
	Confidence   float64      `json:"confidence,omitempty"`
	DeclaredType string       `json:"declared_type,omitempty"`
}

// RouteChain represents the full call chain from a route entry point.
type RouteChain struct {
	Route   string      `json:"route"`
	Method  string      `json:"method"`
	Nodes   []Node      `json:"nodes"`
	Edges   []Edge      `json:"edges"`
	Queries []ChainNode `json:"queries,omitempty"`
}

// LocateRequest is a single file+line pair for locate_function.
type LocateRequest struct {
	FilePath string `json:"file"`
	Line     int    `json:"line"`
}

// LocateResult is the resolved symbol for a file+line pair.
type LocateResult struct {
	FilePath  string `json:"file"`
	Line      int    `json:"line"`
	Symbol    string `json:"symbol"`
	Kind      string `json:"kind"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// ToInt converts a numeric value to int, handling float64 (from JSON) and int64 (from DB drivers).
func ToInt(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case float64:
		return int(number), true
	case int64:
		return int(number), true
	}
	return 0, false
}

// EdgesToChainEdges converts storage Edge slice to structured ChainEdge slice for MCP output.
func EdgesToChainEdges(edges []Edge) []ChainEdge {
	chainEdges := make([]ChainEdge, 0, len(edges))
	for _, edge := range edges {
		chainEdge := ChainEdge{
			SourceID: edge.SourceID,
			TargetID: edge.TargetID,
			Kind:     edge.Kind,
		}
		if value, exists := edge.Properties["line"]; exists {
			if lineNumber, ok := ToInt(value); ok {
				chainEdge.Line = lineNumber
			}
		}
		if value, ok := edge.Properties["confidence"].(float64); ok {
			chainEdge.Confidence = value
		}
		if value, ok := edge.Properties["declared_type"].(string); ok {
			chainEdge.DeclaredType = value
		}
		if value, ok := edge.Properties["flow_context"].(string); ok {
			chainEdge.FlowContext = value
		}
		if value, exists := edge.Properties["flow_line"]; exists {
			if lineNumber, ok := ToInt(value); ok {
				chainEdge.FlowLine = lineNumber
			}
		}
		chainEdges = append(chainEdges, chainEdge)
	}
	return chainEdges
}

// EdgesToCompactChainEdges converts storage Edge slice (after CompactSubgraphEdges) to CompactChainEdge slice.
func EdgesToCompactChainEdges(edges []Edge) []CompactChainEdge {
	compactEdges := make([]CompactChainEdge, 0, len(edges))
	for _, edge := range edges {
		compactEdge := CompactChainEdge{
			SourceID: edge.SourceID,
			TargetID: edge.TargetID,
			Kind:     edge.Kind,
		}
		if value, ok := edge.Properties["confidence"].(float64); ok {
			compactEdge.Confidence = value
		}
		if value, ok := edge.Properties["declared_type"].(string); ok {
			compactEdge.DeclaredType = value
		}
		// CompactSubgraphEdges stores merged lines as []int in Properties["lines"]
		if value, ok := edge.Properties["lines"].([]int); ok {
			compactEdge.Lines = value
		}
		compactEdges = append(compactEdges, compactEdge)
	}
	return compactEdges
}
