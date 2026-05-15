package mcp

import (
	"encoding/json"
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
)

func TestListResponse_MarshalJSON(t *testing.T) {
	resp := listResponse[model.Node]{
		Branch: "main",
		Data: []model.Node{
			{ID: "n1", Kind: "Function", Properties: map[string]any{"name": "foo"}},
		},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["branch"]; !ok {
		t.Error("missing 'branch' field")
	}
	if _, ok := m["data"]; !ok {
		t.Error("missing 'data' field")
	}
}

func TestListResponse_EmptyData(t *testing.T) {
	resp := listResponse[storage.SearchResult]{Branch: "dev", Data: nil}
	data, _ := json.Marshal(resp)
	var m map[string]any
	json.Unmarshal(data, &m)
	if m["branch"] != "dev" {
		t.Errorf("branch = %v, want dev", m["branch"])
	}
	if m["data"] != nil {
		t.Errorf("data = %v, want nil", m["data"])
	}
}

func TestCallChainResponse_OmitEmpty(t *testing.T) {
	resp := callChainResponse{
		Branch: "main",
		Nodes:  []model.Node{{ID: "n1", Kind: "Function"}},
		Edges:  []model.ChainEdge{{SourceID: "n1", TargetID: "n2", Kind: "CALLS"}},
	}
	data, _ := json.Marshal(resp)
	var m map[string]json.RawMessage
	json.Unmarshal(data, &m)

	if _, ok := m["branch"]; !ok {
		t.Error("missing 'branch'")
	}
	if _, ok := m["warning"]; ok {
		t.Error("empty 'warning' should be omitted")
	}
	if _, ok := m["hint"]; ok {
		t.Error("empty 'hint' should be omitted")
	}
	if _, ok := m["truncated_nodes"]; ok {
		t.Error("nil 'truncated_nodes' should be omitted")
	}
	if _, ok := m["cross_service_hints"]; ok {
		t.Error("nil 'cross_service_hints' should be omitted")
	}
}

func TestCompactCallChainResponse_EdgesType(t *testing.T) {
	resp := compactCallChainResponse{
		Branch: "feat",
		Nodes:  []model.Node{{ID: "n1", Kind: "Function"}},
		Edges:  []model.CompactChainEdge{{SourceID: "n1", TargetID: "n2", Kind: "CALLS"}},
	}
	data, _ := json.Marshal(resp)
	var m map[string]json.RawMessage
	json.Unmarshal(data, &m)

	var edges []map[string]any
	json.Unmarshal(m["edges"], &edges)
	if len(edges) != 1 {
		t.Fatalf("edges len = %d, want 1", len(edges))
	}
	if _, ok := edges[0]["source_id"]; !ok {
		t.Error("CompactChainEdge should have 'source_id' field")
	}
}

func TestEmbeddedStructFlatten_IndexResult(t *testing.T) {
	resp := indexRepositoryResponse{
		Branch: "main",
		IndexResult: &model.IndexResult{
			FilesScanned:   10,
			FilesProcessed: 8,
		},
	}
	data, _ := json.Marshal(resp)
	var m map[string]any
	json.Unmarshal(data, &m)

	if m["branch"] != "main" {
		t.Errorf("branch = %v", m["branch"])
	}
	if m["files_scanned"] != float64(10) {
		t.Errorf("files_scanned = %v, want 10 (should be flattened)", m["files_scanned"])
	}
}

func TestEmbeddedStructFlatten_RouteChain(t *testing.T) {
	resp := routeChainResponse{
		Branch: "main",
		Hint:   "test hint",
		RouteChain: &model.RouteChain{
			Route:  "/api/users",
			Method: "GET",
		},
	}
	data, _ := json.Marshal(resp)
	var m map[string]any
	json.Unmarshal(data, &m)

	if m["route"] != "/api/users" {
		t.Errorf("route = %v, should be flattened from RouteChain", m["route"])
	}
	if m["hint"] != "test hint" {
		t.Errorf("hint = %v", m["hint"])
	}
}

func TestClassMembersDualSchema(t *testing.T) {
	normal := classMembersResponse{
		Branch:  "main",
		Kind:    "Class",
		Methods: []model.Node{{ID: "m1", Kind: "Function"}},
		Fields:  []model.FieldInfo{{Name: "id", Type: "int"}},
	}
	ambiguous := classMembersAmbiguousResponse{
		Branch:     "main",
		Ambiguous:  true,
		Message:    "Multiple matches",
		Candidates: []model.Node{{ID: "c1", Kind: "Class"}},
	}

	normalData, _ := json.Marshal(normal)
	ambiguousData, _ := json.Marshal(ambiguous)

	var normalMap, ambiguousMap map[string]any
	json.Unmarshal(normalData, &normalMap)
	json.Unmarshal(ambiguousData, &ambiguousMap)

	if _, ok := normalMap["ambiguous"]; ok {
		t.Error("normal response should not have 'ambiguous' field")
	}
	if _, ok := ambiguousMap["kind"]; ok {
		t.Error("ambiguous response should not have 'kind' field")
	}
	if ambiguousMap["ambiguous"] != true {
		t.Error("ambiguous should be true")
	}
}

func TestDependencyNodeInfo(t *testing.T) {
	resp := dependenciesResponse{
		Branch: "main",
		Edges:  []model.Edge{{SourceID: "a", TargetID: "b", Kind: "CALLS"}},
		Nodes: map[string]dependencyNodeInfo{
			"a": {Kind: "Function", QualifiedName: "pkg.Foo", FilePath: "foo.go"},
			"b": {Kind: "Function"},
		},
	}
	data, _ := json.Marshal(resp)
	var m map[string]any
	json.Unmarshal(data, &m)

	nodes, ok := m["nodes"].(map[string]any)
	if !ok {
		t.Fatal("nodes should be a map")
	}
	nodeA, ok := nodes["a"].(map[string]any)
	if !ok {
		t.Fatal("node 'a' should be a map")
	}
	if nodeA["kind"] != "Function" {
		t.Errorf("node a kind = %v", nodeA["kind"])
	}
	if nodeA["qualified_name"] != "pkg.Foo" {
		t.Errorf("node a qualified_name = %v", nodeA["qualified_name"])
	}

	nodeB := nodes["b"].(map[string]any)
	if _, ok := nodeB["qualified_name"]; ok {
		t.Error("node b should omit empty qualified_name")
	}
}
