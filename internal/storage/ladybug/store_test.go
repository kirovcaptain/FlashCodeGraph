package ladybug

import (
	"context"
	"fmt"
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestLadybugAvailable(t *testing.T) {
	store, err := New("", 0)
	if err != nil {
		t.Fatalf("LadybugDB unavailable (native library missing?): %v", err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("LadybugDB Migrate failed: %v", err)
	}
}


func setupTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := New("", 0) // in-memory
	if err != nil {
		t.Fatal("open:", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal("migrate:", err)
	}
	return store
}

func TestStoreBasicCRUD(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// Write nodes
	nodes := []model.Node{
		{ID: "f1", Kind: "Function", Properties: map[string]any{
			"name": "main", "file_path": "main.go", "start_line": 1,
		}},
		{ID: "f2", Kind: "Function", Properties: map[string]any{
			"name": "helper", "file_path": "util.go", "start_line": 10,
		}},
	}
	if err := store.WriteNodes(ctx, nodes); err != nil {
		t.Fatal("write nodes:", err)
	}

	// Query by name
	found, err := store.QueryNodesByName(ctx, "main", model.QueryOpts{Kinds: []string{"Function"}})
	if err != nil {
		t.Fatal("query:", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 node, got %d", len(found))
	}
	if found[0].ID != "f1" {
		t.Fatalf("expected id f1, got %s", found[0].ID)
	}

	// Write edge
	edges := []model.Edge{
		{SourceID: "f1", TargetID: "f2", Kind: model.RelCalls, Properties: map[string]any{
			"confidence": 0.85, "resolved_by": "type_exact", "candidates": 1, "line": 5,
		}},
	}
	if err := store.WriteEdges(ctx, edges); err != nil {
		t.Fatal("write edges:", err)
	}

	// Traverse call chain
	sg, err := store.TraverseCallChain(ctx, "f1", 3, model.Outgoing, 0.5)
	if err != nil {
		t.Fatal("traverse:", err)
	}
	if len(sg.Nodes) != 1 {
		t.Fatalf("expected 1 callee, got %d", len(sg.Nodes))
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

	t.Log("✅ All LadybugDB CRUD tests passed")
}

func TestStoreEmptyOperations(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// Write empty node list
	if err := store.WriteNodes(ctx, []model.Node{}); err != nil {
		t.Fatal("write empty nodes:", err)
	}

	// Write empty edge list
	if err := store.WriteEdges(ctx, []model.Edge{}); err != nil {
		t.Fatal("write empty edges:", err)
	}

	// Query non-existent node
	node, err := store.QueryNodeByID(ctx, "does-not-exist")
	if err != nil {
		t.Fatal("query missing node:", err)
	}
	if node != nil {
		t.Fatal("expected nil for missing node")
	}

	// Query non-existent name
	nodes, err := store.QueryNodesByName(ctx, "nothing", model.QueryOpts{})
	if err != nil {
		t.Fatal("query missing name:", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("expected 0 nodes, got %d", len(nodes))
	}

	// Search empty database
	results, err := store.SearchFTS(ctx, "anything", 10)
	if err != nil {
		t.Fatal("search empty:", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}

	// Traverse empty database
	sg, err := store.TraverseCallChain(ctx, "missing", 3, model.Outgoing, 0)
	if err != nil {
		t.Fatal("traverse empty:", err)
	}
	if len(sg.Nodes) != 0 {
		t.Fatalf("expected 0 nodes, got %d", len(sg.Nodes))
	}

	// Stats on empty database
	stats, err := store.GetStats(ctx)
	if err != nil {
		t.Fatal("stats empty:", err)
	}
	if stats.NodeCount != 0 {
		t.Fatalf("expected 0 total nodes, got %d", stats.NodeCount)
	}
}

func TestStoreDuplicateWrite(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	node := model.Node{ID: "f1", Kind: "Function", Properties: map[string]any{
		"name": "original", "file_path": "a.go", "start_line": 1,
	}}

	// Write once
	if err := store.WriteNodes(ctx, []model.Node{node}); err != nil {
		t.Fatal("first write:", err)
	}

	// Write again with updated properties (MERGE should update, not duplicate)
	node.Properties["name"] = "updated"
	if err := store.WriteNodes(ctx, []model.Node{node}); err != nil {
		t.Fatal("second write:", err)
	}

	// Should still be 1 node, with updated name
	found, err := store.QueryNodesByName(ctx, "updated", model.QueryOpts{Kinds: []string{"Function"}})
	if err != nil {
		t.Fatal("query:", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 node after duplicate write, got %d", len(found))
	}

	// Old name should not exist
	old, err := store.QueryNodesByName(ctx, "original", model.QueryOpts{Kinds: []string{"Function"}})
	if err != nil {
		t.Fatal("query old:", err)
	}
	if len(old) != 0 {
		t.Fatalf("expected 0 nodes with old name, got %d", len(old))
	}

	stats, err := store.GetStats(ctx)
	if err != nil {
		t.Fatal("stats:", err)
	}
	if stats.NodesByKind["Function"] != 1 {
		t.Fatalf("expected 1 function after duplicate write, got %d", stats.NodesByKind["Function"])
	}
}

func TestStoreDeleteAndQuery(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// Create nodes and edge
	nodes := []model.Node{
		{ID: "f1", Kind: "Function", Properties: map[string]any{"name": "caller", "file_path": "a.go", "start_line": 1}},
		{ID: "f2", Kind: "Function", Properties: map[string]any{"name": "callee", "file_path": "a.go", "start_line": 10}},
	}
	store.WriteNodes(ctx, nodes)
	store.WriteEdges(ctx, []model.Edge{
		{SourceID: "f1", TargetID: "f2", Kind: model.RelCalls},
	})

	// Delete edges by source
	if err := store.DeleteEdgesBySource(ctx, "f1"); err != nil {
		t.Fatal("delete edges:", err)
	}

	// Traverse should return empty
	sg, err := store.TraverseCallChain(ctx, "f1", 3, model.Outgoing, 0)
	if err != nil {
		t.Fatal("traverse after delete:", err)
	}
	if len(sg.Nodes) != 0 {
		t.Fatalf("expected 0 callees after edge delete, got %d", len(sg.Nodes))
	}

	// Delete nodes by file
	if err := store.DeleteNodesByFile(ctx, "a.go"); err != nil {
		t.Fatal("delete nodes:", err)
	}

	// Nodes should be gone
	stats, err := store.GetStats(ctx)
	if err != nil {
		t.Fatal("stats:", err)
	}
	if stats.NodesByKind["Function"] != 0 {
		t.Fatalf("expected 0 functions after delete, got %d", stats.NodesByKind["Function"])
	}
}

func TestStoreConfidenceFilter(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// Create chain: f1 → f2 (high conf) → f3 (low conf)
	nodes := []model.Node{
		{ID: "f1", Kind: "Function", Properties: map[string]any{"name": "entry", "file_path": "a.go", "start_line": 1}},
		{ID: "f2", Kind: "Function", Properties: map[string]any{"name": "middle", "file_path": "a.go", "start_line": 10}},
		{ID: "f3", Kind: "Function", Properties: map[string]any{"name": "leaf", "file_path": "a.go", "start_line": 20}},
	}
	store.WriteNodes(ctx, nodes)
	store.WriteEdges(ctx, []model.Edge{
		{SourceID: "f1", TargetID: "f2", Kind: model.RelCalls, Properties: map[string]any{"confidence": 0.9}},
		{SourceID: "f2", TargetID: "f3", Kind: model.RelCalls, Properties: map[string]any{"confidence": 0.2}},
	})

	// No filter: should get both f2 and f3
	sg, err := store.TraverseCallChain(ctx, "f1", 3, model.Outgoing, 0)
	if err != nil {
		t.Fatal("traverse no filter:", err)
	}
	if len(sg.Nodes) != 2 {
		t.Fatalf("expected 2 callees without filter, got %d", len(sg.Nodes))
	}

	// High confidence filter: f3 has only low-confidence incoming edge, should be filtered
	sg, err = store.TraverseCallChain(ctx, "f1", 3, model.Outgoing, 0.5)
	if err != nil {
		t.Fatal("traverse with filter:", err)
	}
	if len(sg.Nodes) != 1 {
		t.Fatalf("expected 1 callee with confidence >= 0.5, got %d", len(sg.Nodes))
	}
	if sg.Nodes[0].ID != "f2" {
		t.Fatalf("expected f2, got %s", sg.Nodes[0].ID)
	}
}

func TestQueryAllByKind_MultipleKinds(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
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

	t.Log("✅ QueryAllByKind works for all node kinds")
}

func TestDeleteNodesByFile_RemovesCorrectNodes(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
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
	if funcs[0].Properties["name"] != "keep" {
		t.Fatalf("expected 'keep' to survive, got %v", funcs[0].Properties["name"])
	}

	t.Log("✅ DeleteNodesByFile removes correct nodes, preserves others")
}

func TestDeleteNodesByFile_WithEdges(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	store.WriteNodes(ctx, []model.Node{
		{ID: "file:a.go", Kind: "File", Properties: map[string]any{"path": "a.go", "language": "go"}},
		{ID: "f1", Kind: "Function", Properties: map[string]any{"name": "hello", "file_path": "/tmp/a.go", "start_line": 1}},
		{ID: "f2", Kind: "Function", Properties: map[string]any{"name": "world", "file_path": "/tmp/a.go", "start_line": 5}},
		{ID: "f3", Kind: "Function", Properties: map[string]any{"name": "keep", "file_path": "/tmp/b.go", "start_line": 1}},
	})
	// Create edges so nodes have relationships
	store.WriteEdges(ctx, []model.Edge{
		{SourceID: "f1", TargetID: "f2", Kind: model.RelCalls},
		{SourceID: "file:a.go", TargetID: "f1", Kind: model.RelContains, SourceKind: "File"},
	})

	// Delete should work even with edges attached (DETACH DELETE)
	err := store.DeleteNodesByFile(ctx, "/tmp/a.go")
	if err != nil {
		t.Fatalf("DeleteNodesByFile with edges: %v", err)
	}

	funcs, _ := store.QueryAllByKind(ctx, "Function", 100)
	if len(funcs) != 1 {
		t.Fatalf("expected 1 function after delete, got %d", len(funcs))
	}
	if funcs[0].Properties["name"] != "keep" {
		t.Fatalf("expected 'keep', got %v", funcs[0].Properties["name"])
	}

	t.Log("✅ DeleteNodesByFile with edges (DETACH DELETE) works")
}

func TestSchemaRoundtrip_WriteAndQueryAllProperties(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// Write a Function with all properties
	store.WriteNodes(ctx, []model.Node{{
		ID:   "f1",
		Kind: "Function",
		Properties: map[string]any{
			"name":           "findById",
			"qualified_name": "com.example.UserService.findById",
			"file_path":      "src/UserService.java",
			"start_line":     10,
			"end_line":       20,
			"params":         `[{"name":"id","type":"Long"}]`,
			"return_types":   []string{"User"},
			"is_exported":    true,
			"is_static":      false,
			"annotations":    `["@Override"]`,
		},
	}})

	// QueryNodesByName should return all properties
	nodes, err := store.QueryNodesByName(ctx, "findById", model.QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}

	n := nodes[0]
	checks := map[string]any{
		"name":           "findById",
		"qualified_name": "com.example.UserService.findById",
		"file_path":      "src/UserService.java",
		"return_types":   []string{"User"},
	}
	for key, expected := range checks {
		val := fmt.Sprint(n.Properties[key])
		if val != fmt.Sprint(expected) {
			t.Fatalf("property %s: expected %v, got %v", key, expected, val)
		}
	}

	// QueryAllByKind should also return all properties
	allNodes, _ := store.QueryAllByKind(ctx, "Function", 100)
	if len(allNodes) < 1 {
		t.Fatal("QueryAllByKind returned 0 functions")
	}
	qn := allNodes[0]
	if fmt.Sprint(qn.Properties["qualified_name"]) != "com.example.UserService.findById" {
		t.Fatalf("QueryAllByKind missing qualified_name: %v", qn.Properties["qualified_name"])
	}
	if fmt.Sprint(qn.Properties["return_types"]) == "" {
		t.Fatalf("QueryAllByKind missing return_typess: %v", qn.Properties["return_types"])
	}

	t.Log("✅ Schema roundtrip: all properties saved and queryable")
}

func TestSchemaRoundtrip_ClassProperties(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	store.WriteNodes(ctx, []model.Node{{
		ID:   "c1",
		Kind: "Class",
		Properties: map[string]any{
			"name":           "UserService",
			"qualified_name": "com.example.UserService",
			"file_path":      "src/UserService.java",
			"class_type":     "class",
			"is_abstract":    false,
			"annotations":    `["@Service"]`,
		},
	}})

	nodes, _ := store.QueryNodesByName(ctx, "UserService", model.QueryOpts{Kinds: []string{"Class"}})
	if len(nodes) != 1 {
		t.Fatalf("expected 1 class, got %d", len(nodes))
	}

	n := nodes[0]
	if fmt.Sprint(n.Properties["qualified_name"]) != "com.example.UserService" {
		t.Fatalf("missing qualified_name: %v", n.Properties)
	}
	if fmt.Sprint(n.Properties["class_type"]) != "class" {
		t.Fatalf("missing class_type: %v", n.Properties)
	}

	t.Log("✅ Class schema roundtrip: qualified_name + class_type queryable")
}

func TestQueryAllEdgesMultiTable(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// Setup: Function, Class, Interface + Annotation nodes
	store.CreateNodes(ctx, []model.Node{
		{ID: "func1", Kind: "Function", Properties: map[string]any{"name": "getUser", "file_path": "a.java"}},
		{ID: "cls1", Kind: "Class", Properties: map[string]any{"name": "UserService", "file_path": "a.java"}},
		{ID: "iface1", Kind: "Interface", Properties: map[string]any{"name": "IUserService", "file_path": "a.java"}},
		{ID: "ann1", Kind: "Annotation", Properties: map[string]any{"name": "Service", "layer": "service"}},
		{ID: "ann2", Kind: "Annotation", Properties: map[string]any{"name": "GetMapping", "layer": "controller"}},
		{ID: "ann3", Kind: "Annotation", Properties: map[string]any{"name": "Repository", "layer": "repository"}},
	})

	// Write HAS_ANNOTATION edges from all 3 source types
	store.WriteEdges(ctx, []model.Edge{
		{SourceID: "func1", TargetID: "ann1", Kind: model.RelHasAnnotation, SourceKind: "Function"},
		{SourceID: "cls1", TargetID: "ann2", Kind: model.RelHasAnnotation, SourceKind: "Class"},
		{SourceID: "iface1", TargetID: "ann3", Kind: model.RelHasAnnotation, SourceKind: "Interface"},
	})

	// QueryAllEdges should return all 3 edges across the 3 physical tables
	edges, err := store.QueryAllEdges(ctx, model.RelHasAnnotation, 0)
	if err != nil {
		t.Fatalf("QueryAllEdges HAS_ANNOTATION: %v", err)
	}
	if len(edges) != 3 {
		t.Fatalf("expected 3 HAS_ANNOTATION edges, got %d", len(edges))
	}

	// Verify edge sources
	sources := map[string]bool{}
	for _, e := range edges {
		sources[e.SourceID] = true
	}
	for _, expected := range []string{"func1", "cls1", "iface1"} {
		if !sources[expected] {
			t.Errorf("missing edge from %s", expected)
		}
	}

	t.Log("✅ QueryAllEdges multi-table: HAS_ANNOTATION returns edges from all 3 physical tables")
}

func TestTraverseCallChainBFSConfidence(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// Build a call chain: A -0.95-> B -0.30-> C -0.95-> D
	store.CreateNodes(ctx, []model.Node{
		{ID: "a", Kind: "Function", Properties: map[string]any{"name": "A", "file_path": "a.go"}},
		{ID: "b", Kind: "Function", Properties: map[string]any{"name": "B", "file_path": "a.go"}},
		{ID: "c", Kind: "Function", Properties: map[string]any{"name": "C", "file_path": "a.go"}},
		{ID: "d", Kind: "Function", Properties: map[string]any{"name": "D", "file_path": "a.go"}},
	})
	store.WriteEdges(ctx, []model.Edge{
		{SourceID: "a", TargetID: "b", Kind: model.RelCalls, SourceKind: "Function", Properties: map[string]any{"confidence": 0.95}},
		{SourceID: "b", TargetID: "c", Kind: model.RelCalls, SourceKind: "Function", Properties: map[string]any{"confidence": 0.30}},
		{SourceID: "c", TargetID: "d", Kind: model.RelCalls, SourceKind: "Function", Properties: map[string]any{"confidence": 0.95}},
	})

	// minConfidence=0: recursive mode, should return all 3 nodes (B, C, D)
	sg, err := store.TraverseCallChain(ctx, "a", 5, model.Outgoing, 0)
	if err != nil {
		t.Fatalf("recursive: %v", err)
	}
	if len(sg.Nodes) != 3 {
		t.Fatalf("recursive: expected 3 nodes, got %d", len(sg.Nodes))
	}

	// minConfidence=0.7: BFS mode, should only return B (stops at B→C which is 0.30)
	sg, err = store.TraverseCallChain(ctx, "a", 5, model.Outgoing, 0.7)
	if err != nil {
		t.Fatalf("bfs: %v", err)
	}
	if len(sg.Nodes) != 1 {
		t.Fatalf("bfs: expected 1 node (B only), got %d", len(sg.Nodes))
	}
	if sg.Nodes[0].ID != "b" {
		t.Fatalf("bfs: expected node b, got %s", sg.Nodes[0].ID)
	}

	t.Log("✅ TraverseCallChain BFS: low-confidence hop correctly blocks further traversal")
}

func TestSourceKindConstants(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// Create File, Function, Class, Interface
	store.CreateNodes(ctx, []model.Node{
		{ID: "file:a.java", Kind: "File", Properties: map[string]any{"path": "a.java", "language": "java"}},
		{ID: "func1", Kind: "Function", Properties: map[string]any{"name": "getUser", "file_path": "a.java"}},
		{ID: "cls1", Kind: "Class", Properties: map[string]any{"name": "UserCtrl", "file_path": "a.java"}},
		{ID: "iface1", Kind: "Interface", Properties: map[string]any{"name": "IUser", "file_path": "a.java"}},
	})

	// Write CONTAINS edges using SourceKind constants
	store.WriteEdges(ctx, []model.Edge{
		{SourceID: "file:a.java", TargetID: "func1", Kind: model.RelContains, SourceKind: constants.SourceKindFile},
		{SourceID: "file:a.java", TargetID: "cls1", Kind: model.RelContains, SourceKind: constants.SourceKindFileClass},
		{SourceID: "file:a.java", TargetID: "iface1", Kind: model.RelContains, SourceKind: constants.SourceKindFileInterface},
	})

	// Verify each edge was written to the correct table
	edges, _ := store.QueryEdges(ctx, "file:a.java", constants.SourceKindFile, model.RelContains, model.Outgoing)
	if len(edges) != 1 || edges[0].TargetID != "func1" {
		t.Fatalf("FILE_CONTAINS: expected func1, got %v", edges)
	}
	edges, _ = store.QueryEdges(ctx, "file:a.java", constants.SourceKindFileClass, model.RelContains, model.Outgoing)
	if len(edges) != 1 || edges[0].TargetID != "cls1" {
		t.Fatalf("FILE_CONTAINS_CLASS: expected cls1, got %v", edges)
	}
	edges, _ = store.QueryEdges(ctx, "file:a.java", constants.SourceKindFileInterface, model.RelContains, model.Outgoing)
	if len(edges) != 1 || edges[0].TargetID != "iface1" {
		t.Fatalf("FILE_CONTAINS_IFACE: expected iface1, got %v", edges)
	}

	t.Log("✅ SourceKind constants: CONTAINS edges routed to correct physical tables")
}


// TestLadybug_MultiRelTypeRecursive verifies Ladybug supports [:CALLS|DISPATCHES*1..N] syntax.
// Result: ✅ Supported. Used to decide D-01 in iteration 1.2.
func TestLadybug_MultiRelTypeRecursive(t *testing.T) {
	store, err := New("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	store.Migrate(ctx)

	// Create DISPATCHES table (not yet in schema)
	store.execNoParams("CREATE REL TABLE IF NOT EXISTS DISPATCHES (FROM Function TO Function, confidence DOUBLE, resolved_by STRING, MANY_MANY)")

	// Create nodes: A → B (CALLS), B → C (DISPATCHES), C → D (CALLS)
	store.CreateNodes(ctx, []model.Node{
		{ID: "a", Kind: "Function", Properties: map[string]any{"name": "A", "file_path": "a.go"}},
		{ID: "b", Kind: "Function", Properties: map[string]any{"name": "B", "file_path": "b.go"}},
		{ID: "c", Kind: "Function", Properties: map[string]any{"name": "C", "file_path": "c.go"}},
		{ID: "d", Kind: "Function", Properties: map[string]any{"name": "D", "file_path": "d.go"}},
	})
	store.WriteEdges(ctx, []model.Edge{
		{SourceID: "a", TargetID: "b", Kind: model.RelCalls, SourceKind: "Function"},
		{SourceID: "b", TargetID: "c", Kind: model.RelCalls, SourceKind: "Function"},
		{SourceID: "c", TargetID: "d", Kind: model.RelCalls, SourceKind: "Function"},
	})

	// Test: recursive CALLS*1..3 from A should reach B, C, D
	sub, err := store.TraverseCallChain(ctx, "a", 3, model.Outgoing, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("CALLS only: %d nodes", len(sub.Nodes))
	for _, n := range sub.Nodes {
		t.Logf("  %s", n.Properties["name"])
	}
	if len(sub.Nodes) != 3 {
		t.Fatalf("expected 3 nodes via CALLS, got %d", len(sub.Nodes))
	}

	// Now test multi-type: try Ladybug syntax [r:CALLS|DISPATCHES*1..3]
	// This is what we need to verify
	result, err := store.exec("MATCH (a:Function)-[r:CALLS|DISPATCHES*1..3]->(b:Function) WHERE a.id = $id RETURN DISTINCT b.id, b.name", map[string]any{"id": "a"})
	if err != nil {
		t.Logf("❌ Ladybug does NOT support multi-rel recursive: %v", err)
		t.Log("→ Need to use BFS approach for Ladybug")
	} else {
		count := 0
		for result.HasNext() {
			result.Next()
			count++
		}
		result.Close()
		t.Logf("✅ Ladybug supports multi-rel recursive: %d results", count)
	}
}

// TestLadybug_MultiRelTypeSingleHop verifies that [r:CALLS|DISPATCHES] (single hop, no *)
// works correctly in Ladybug. This is the syntax used by traverseBFS for per-hop BFS.
// Scenario: A -CALLS-> B, A -DISPATCHES-> C. Single hop from A should find both B and C.
func TestLadybug_MultiRelTypeSingleHop(t *testing.T) {
	store, err := New("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	store.Migrate(ctx)

	store.execNoParams("CREATE REL TABLE IF NOT EXISTS DISPATCHES (FROM Function TO Function, confidence DOUBLE, resolved_by STRING, MANY_MANY)")

	store.CreateNodes(ctx, []model.Node{
		{ID: "sh_a", Kind: "Function", Properties: map[string]any{"name": "A", "file_path": "a.go"}},
		{ID: "sh_b", Kind: "Function", Properties: map[string]any{"name": "B", "file_path": "b.go"}},
		{ID: "sh_c", Kind: "Function", Properties: map[string]any{"name": "C", "file_path": "c.go"}},
	})
	store.WriteEdges(ctx, []model.Edge{
		{SourceID: "sh_a", TargetID: "sh_b", Kind: model.RelCalls, SourceKind: "Function", Properties: map[string]any{"confidence": 0.9}},
		{SourceID: "sh_a", TargetID: "sh_c", Kind: model.RelDispatches, SourceKind: "Function", Properties: map[string]any{"confidence": 0.8}},
	})

	// Test 1: single hop query — the exact syntax used in traverseBFS
	result, err := store.exec(
		"MATCH (a:Function)-[r:CALLS|DISPATCHES]->(b:Function) WHERE a.id = $id AND r.confidence >= $minConf RETURN b.id, b.name, b.file_path, r.confidence",
		map[string]any{"id": "sh_a", "minConf": 0.0},
	)
	if err != nil {
		t.Fatalf("❌ single hop multi-rel query failed: %v", err)
	}
	found := map[string]bool{}
	for result.HasNext() {
		row, _ := result.Next()
		id, _ := row.GetValue(0)
		found[fmt.Sprint(id)] = true
	}
	result.Close()

	if !found["sh_b"] {
		t.Fatal("expected sh_b (via CALLS) in single hop results")
	}
	if !found["sh_c"] {
		t.Fatal("expected sh_c (via DISPATCHES) in single hop results")
	}
	t.Logf("✅ single hop [r:CALLS|DISPATCHES] returns both CALLS and DISPATCHES targets: %d results", len(found))

	// Test 2: confidence filtering — only CALLS edge (0.9) should pass minConf=0.85
	result2, err := store.exec(
		"MATCH (a:Function)-[r:CALLS|DISPATCHES]->(b:Function) WHERE a.id = $id AND r.confidence >= $minConf RETURN b.id",
		map[string]any{"id": "sh_a", "minConf": 0.85},
	)
	if err != nil {
		t.Fatalf("confidence filter query failed: %v", err)
	}
	count := 0
	for result2.HasNext() {
		result2.Next()
		count++
	}
	result2.Close()
	if count != 1 {
		t.Fatalf("expected 1 result with minConf=0.85, got %d", count)
	}
	t.Log("✅ confidence filtering works on multi-rel single hop")

	// Test 3: traverseBFS integration — depth=2 with mixed edge types
	// A -CALLS-> B -DISPATCHES-> D (add D)
	store.CreateNodes(ctx, []model.Node{
		{ID: "sh_d", Kind: "Function", Properties: map[string]any{"name": "D", "file_path": "d.go"}},
	})
	store.WriteEdges(ctx, []model.Edge{
		{SourceID: "sh_b", TargetID: "sh_d", Kind: model.RelDispatches, SourceKind: "Function", Properties: map[string]any{"confidence": 0.9}},
	})

	sub, err := store.TraverseCallChain(ctx, "sh_a", 2, model.Outgoing, 0)
	if err != nil {
		t.Fatalf("traverseBFS failed: %v", err)
	}
	nodeIDs := map[string]bool{}
	for _, n := range sub.Nodes {
		nodeIDs[n.ID] = true
	}
	if !nodeIDs["sh_b"] {
		t.Fatal("depth=1 should find B (CALLS)")
	}
	if nodeIDs["sh_c"] {
		t.Fatal("C (DISPATCHES from root) should NOT appear in call chain")
	}
	if !nodeIDs["sh_d"] {
		t.Fatal("D (reached via DISPATCHES from B, no declared_type) SHOULD appear in call chain")
	}
	t.Logf("✅ traverseBFS follows CALLS + DISPATCHES: %d nodes", len(sub.Nodes))
}

func TestLadybug_CreateEdgeInlineProps(t *testing.T) {
	store, err := New("", 0)
	if err != nil { t.Fatal(err) }
	defer store.Close()
	ctx := context.Background()
	store.Migrate(ctx)

	store.CreateNodes(ctx, []model.Node{
		{ID: "ce_a", Kind: "Function", Properties: map[string]any{"name": "A", "file_path": "a.go"}},
		{ID: "ce_b", Kind: "Function", Properties: map[string]any{"name": "B", "file_path": "b.go"}},
	})

	// Test CREATE with inline properties
	result, err := store.exec(
		"MATCH (a:Function), (b:Function) WHERE a.id = $id1 AND b.id = $id2 CREATE (a)-[r:CALLS {confidence: $conf, resolved_by: $rb}]->(b)",
		map[string]any{"id1": "ce_a", "id2": "ce_b", "conf": 0.95, "rb": "test_create"},
	)
	if err != nil {
		t.Fatalf("❌ CREATE inline props failed: %v", err)
	}
	result.Close()

	// Verify properties
	result2, err := store.exec(
		"MATCH (a:Function)-[r:CALLS]->(b:Function) WHERE a.id = $id RETURN r.confidence, r.resolved_by",
		map[string]any{"id": "ce_a"},
	)
	if err != nil { t.Fatal(err) }
	if !result2.HasNext() { t.Fatal("no edge found") }
	row, _ := result2.Next()
	conf, _ := row.GetValue(0)
	rb, _ := row.GetValue(1)
	result2.Close()
	t.Logf("✅ CREATE inline props: confidence=%v, resolved_by=%v", conf, rb)
}

func TestQueryNodesByProperty(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	nodes := []model.Node{
		{ID: "r1", Kind: "Route", Properties: map[string]any{"method": "GET", "path_pattern": "/api/users", "file_path": "a.java"}},
		{ID: "r2", Kind: "Route", Properties: map[string]any{"method": "POST", "path_pattern": "/api/users", "file_path": "a.java"}},
		{ID: "r3", Kind: "Route", Properties: map[string]any{"method": "GET", "path_pattern": "/api/orders/{id}", "file_path": "b.java"}},
		{ID: "r4", Kind: "Route", Properties: map[string]any{"method": "GET", "path_pattern": "/api/register/free", "file_path": "c.java"}},
	}
	if err := store.WriteNodes(ctx, nodes); err != nil {
		t.Fatal(err)
	}

	// Exact match
	results, err := store.QueryNodesByProperty(ctx, "Route", "path_pattern", "/api/users", "exact", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Errorf("exact match: expected 2, got %d", len(results))
	}

	// Contains match
	results, err = store.QueryNodesByProperty(ctx, "Route", "path_pattern", "/register/free", "contains", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("contains match: expected 1, got %d", len(results))
	}

	// No match
	results, err = store.QueryNodesByProperty(ctx, "Route", "path_pattern", "/nonexistent", "exact", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("no match: expected 0, got %d", len(results))
	}

	// Single quote escape
	results, err = store.QueryNodesByProperty(ctx, "Route", "path_pattern", "/api/user's", "exact", 0)
	if err != nil {
		t.Fatalf("single quote should not cause error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("single quote: expected 0, got %d", len(results))
	}

	// Limit
	results, err = store.QueryNodesByProperty(ctx, "Route", "path_pattern", "/api", "contains", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("limit: expected 1, got %d", len(results))
	}
}
