// Package storage defines interfaces for graph, fingerprint, lock, and vector storage.
package storage

import (
	"context"
	"time"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// Match modes for QueryNodesByProperty.
const (
	MatchExact    = "exact"
	MatchContains = "contains"
	MatchNotEmpty = "not_empty"
)

// GraphStore is the interface for graph data persistence.
type GraphStore interface {
	// Write
	WriteNodes(ctx context.Context, nodes []model.Node) error   // MERGE: create or update
	CreateNodes(ctx context.Context, nodes []model.Node) error  // CREATE: insert only (faster, requires no duplicates)
	WriteEdges(ctx context.Context, edges []model.Edge) error
	CreateEdges(ctx context.Context, edges []model.Edge) error  // CREATE: insert only (faster, no duplicate check)

	// Delete (incremental indexing)
	DeleteNodesByFile(ctx context.Context, filePath string) error
	DeleteNodeByID(ctx context.Context, id string) error
	DeleteEdgesBySource(ctx context.Context, sourceID string) error
	DeleteEdgesByTarget(ctx context.Context, targetID string) error
	DeleteAllByKind(ctx context.Context, kind string) error
	ClearAll(ctx context.Context) error

	// Query
	QueryNodeByID(ctx context.Context, id string) (*model.Node, error)
	QueryNodeByQualifiedName(ctx context.Context, qualifiedName string) (*model.Node, error)
	QueryNodesByName(ctx context.Context, name string, opts model.QueryOpts) ([]model.Node, error)
	QueryEdges(ctx context.Context, nodeID string, nodeKind string, relKind model.RelationKind, dir model.Direction) ([]model.Edge, error)
	QueryAllEdges(ctx context.Context, relKind model.RelationKind, limit int) ([]model.Edge, error)

	// Graph traversal
	TraverseCallChain(ctx context.Context, nodeID string, depth int, dir model.Direction, minConfidence float64) (*model.Subgraph, error)
	TraverseImpact(ctx context.Context, nodeID string, depth int) (*model.Subgraph, error)

	// Query functions with no callers (dead code detection, single query)

	// Query all nodes of a specific kind
	QueryAllByKind(ctx context.Context, kind string, limit int) ([]model.Node, error)

	// QueryNodesByProperty returns nodes of a specific kind where the given property matches the value.
	// matchMode: MatchExact, MatchContains, or MatchNotEmpty.
	QueryNodesByProperty(ctx context.Context, kind string, key string, value string, matchMode string, limit int) ([]model.Node, error)

	// QueryNodesByFile returns Function, Class, and Interface nodes in a file (for locate_function).
	QueryNodesByFile(ctx context.Context, filePath string) ([]model.Node, error)

	// Full-text search
	SearchFTS(ctx context.Context, query string, limit int) ([]SearchResult, error)

	// Batch update properties on existing nodes
	BatchUpdateNodeProperties(ctx context.Context, kind string, updates []PropertyUpdate) error

	// Stats
	GetStats(ctx context.Context) (*model.GraphStats, error)

	// Lifecycle
	Migrate(ctx context.Context) error
	Close() error
}

// PropertyUpdate holds a node ID and properties to update.
type PropertyUpdate struct {
	NodeID string
	Props  map[string]any
}

// SearchResult is a full-text search hit.
type SearchResult struct {
	NodeID        string  `json:"node_id"`
	Name          string  `json:"name"`
	QualifiedName string  `json:"qualified_name"`
	Kind          string  `json:"kind"`
	Path          string  `json:"path"`
	Score         float64 `json:"score"`
}

// FingerprintMeta holds metadata about the last indexing run.
type FingerprintMeta struct {
	LastIndexedAt int64  `json:"last_indexed_at"`
	LastCommit    string `json:"last_commit"`
}

// FingerprintStore persists file fingerprints for incremental indexing.
type FingerprintStore interface {
	Load(ctx context.Context, branch string) (map[string]model.Fingerprint, error)
	Save(ctx context.Context, branch string, fps map[string]model.Fingerprint, meta *FingerprintMeta) error
	LoadMeta(ctx context.Context, branch string) (*FingerprintMeta, error)
}

// IndexLock provides mutual exclusion for indexing (remote mode only).
type IndexLock interface {
	Acquire(ctx context.Context, repo, branch, holder string, ttl time.Duration) error
	Release(ctx context.Context, repo, branch string) error
	Query(ctx context.Context, repo, branch string) (*model.LockInfo, error)
	ForceRelease(ctx context.Context, repo, branch string) error
}

// VectorStore persists embedding vectors for semantic search.
type VectorStore interface {
	Store(ctx context.Context, nodeID, branch, nodeKind, textRepr string, embedding []float32, modelName string) error
	Search(ctx context.Context, branch string, query []float32, topK int) ([]VectorResult, error)
	Delete(ctx context.Context, nodeID, branch string) error
}

// VectorResult is a semantic search hit.
type VectorResult struct {
	NodeID     string  `json:"node_id"`
	Similarity float64 `json:"similarity"`
}
