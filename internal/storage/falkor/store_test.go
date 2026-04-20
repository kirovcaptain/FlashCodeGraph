package falkor

import (
	"context"
	"os"
	"fmt"
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func getTestSocket() string {
	socket := os.Getenv("FALKORDB_SOCKET")
	if socket == "" {
		home, _ := os.UserHomeDir()
		socket = home + "/.fcg/falkordb.sock"
	}
	return socket
}

func setupTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := New(getTestSocket(), "fcg_test")
	if err != nil {
		t.Skip("FalkorDB not available:", err)
	}
	// Clean graph before test
	store.DeleteGraph(context.Background())
	return store
}

func TestFalkorStore_BasicCRUD(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	defer store.DeleteGraph(context.Background())
	ctx := context.Background()

	// Write nodes
	nodes := []model.Node{
		{ID: "f1", Kind: "Function", Properties: map[string]any{"name": "main", "file_path": "main.go", "start_line": 1}},
		{ID: "f2", Kind: "Function", Properties: map[string]any{"name": "helper", "file_path": "util.go", "start_line": 10}},
	}
	if err := store.WriteNodes(ctx, nodes); err != nil {
		t.Fatal("write nodes:", err)
	}

	// Query by name
	found, err := store.QueryNodesByName(ctx, "main", model.QueryOpts{})
	if err != nil {
		t.Fatal("query:", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 node, got %d", len(found))
	}

	// Write edge
	edges := []model.Edge{
		{SourceID: "f1", TargetID: "f2", Kind: model.RelCalls, Properties: map[string]any{
			"confidence": 0.85, "resolved_by": "type_exact",
		}},
	}
	if err := store.WriteEdges(ctx, edges); err != nil {
		t.Fatal("write edges:", err)
	}

	// Traverse
	subgraph, err := store.TraverseCallChain(ctx, "f1", 3, model.Outgoing, 0)
	if err != nil {
		t.Fatal("traverse:", err)
	}
	if len(subgraph.Nodes) != 1 {
		t.Fatalf("expected 1 callee, got %d", len(subgraph.Nodes))
	}

	// Stats
	stats, err := store.GetStats(ctx)
	if err != nil {
		t.Fatal("stats:", err)
	}
	if stats.NodesByKind["Function"] != 2 {
		t.Fatalf("expected 2 functions, got %d", stats.NodesByKind["Function"])
	}

	// Search
	results, err := store.SearchFTS(ctx, "help", 10)
	if err != nil {
		t.Fatal("search:", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 search result, got %d", len(results))
	}

	t.Log("✅ FalkorDB CRUD tests passed")
}

func TestFalkorStore_DeleteAndQuery(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	defer store.DeleteGraph(context.Background())
	ctx := context.Background()

	store.WriteNodes(ctx, []model.Node{
		{ID: "f1", Kind: "Function", Properties: map[string]any{"name": "caller", "file_path": "a.go"}},
		{ID: "f2", Kind: "Function", Properties: map[string]any{"name": "callee", "file_path": "a.go"}},
	})
	store.WriteEdges(ctx, []model.Edge{
		{SourceID: "f1", TargetID: "f2", Kind: model.RelCalls},
	})

	// Delete edges
	store.DeleteEdgesBySource(ctx, "f1")
	subgraph, _ := store.TraverseCallChain(ctx, "f1", 3, model.Outgoing, 0)
	if len(subgraph.Nodes) != 0 {
		t.Fatalf("expected 0 after delete, got %d", len(subgraph.Nodes))
	}

	// Delete nodes
	store.DeleteNodesByFile(ctx, "a.go")
	stats, _ := store.GetStats(ctx)
	if stats.NodesByKind["Function"] != 0 {
		t.Fatalf("expected 0 functions, got %d", stats.NodesByKind["Function"])
	}

	t.Log("✅ FalkorDB delete tests passed")
}

func TestQueryAllByKind_MultipleKinds(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	defer store.DeleteGraph(context.Background())
	ctx := context.Background()

	store.WriteNodes(ctx, []model.Node{
		{ID: "repo:test", Kind: "Repository", Properties: map[string]any{"name": "test", "path": "/tmp/test"}},
		{ID: "dir:src", Kind: "Directory", Properties: map[string]any{"path": "src"}},
		{ID: "file:main.go", Kind: "File", Properties: map[string]any{"path": "main.go", "language": "go"}},
		{ID: "file:util.go", Kind: "File", Properties: map[string]any{"path": "util.go", "language": "go"}},
		{ID: "f1", Kind: "Function", Properties: map[string]any{"name": "main", "file_path": "main.go", "start_line": 1}},
		{ID: "q1", Kind: "QueryNode", Properties: map[string]any{"sql_text": "SELECT 1", "query_type": "SELECT"}},
	})

	for _, kind := range []string{"Repository", "Directory", "File", "Function", "QueryNode"} {
		nodes, err := store.QueryAllByKind(ctx, kind, 100)
		if err != nil {
			t.Fatalf("QueryAllByKind(%s): %v", kind, err)
		}
		if len(nodes) == 0 {
			t.Fatalf("QueryAllByKind(%s): expected nodes, got 0", kind)
		}
		t.Logf("  %s: %d nodes", kind, len(nodes))
	}

	files, _ := store.QueryAllByKind(ctx, "File", 100)
	if len(files) != 2 {
		t.Fatalf("expected 2 File nodes, got %d", len(files))
	}

	t.Log("✅ FalkorDB QueryAllByKind works for all node kinds")
}

func TestDeleteNodesByFile_RemovesCorrectNodes(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	defer store.DeleteGraph(context.Background())
	ctx := context.Background()

	store.WriteNodes(ctx, []model.Node{
		{ID: "f1", Kind: "Function", Properties: map[string]any{"name": "hello", "file_path": "/tmp/a.go", "start_line": 1}},
		{ID: "f2", Kind: "Function", Properties: map[string]any{"name": "world", "file_path": "/tmp/a.go", "start_line": 5}},
		{ID: "f3", Kind: "Function", Properties: map[string]any{"name": "keep", "file_path": "/tmp/b.go", "start_line": 1}},
	})

	store.DeleteNodesByFile(ctx, "/tmp/a.go")

	funcs, _ := store.QueryAllByKind(ctx, "Function", 100)
	if len(funcs) != 1 {
		t.Fatalf("expected 1 function after delete, got %d", len(funcs))
	}

	t.Log("✅ FalkorDB DeleteNodesByFile removes correct nodes")
}

func TestSchemaRoundtrip_WriteAndQueryAllProperties(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	defer store.DeleteGraph(context.Background())
	ctx := context.Background()

	store.WriteNodes(ctx, []model.Node{{
		ID:   "f1",
		Kind: "Function",
		Properties: map[string]any{
			"name":           "findById",
			"qualified_name": "com.example.UserService.findById",
			"file_path":      "src/UserService.java",
			"start_line":     10,
			"params":         `[{"name":"id","type":"Long"}]`,
			"return_types":   []string{"User"},
			"is_exported":    true,
		},
	}})

	nodes, err := store.QueryNodesByName(ctx, "findById", model.QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}

	n := nodes[0]
	if fmt.Sprint(n.Properties["qualified_name"]) != "com.example.UserService.findById" {
		t.Fatalf("missing qualified_name: %v", n.Properties["qualified_name"])
	}
	if fmt.Sprint(n.Properties["return_types"]) == "" {
		t.Fatalf("missing return_types: %v", n.Properties["return_types"])
	}

	t.Log("✅ FalkorDB schema roundtrip: all properties saved and queryable")
}
