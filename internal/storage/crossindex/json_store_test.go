package crossindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestJSONStore_RegisterAndLookup(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "cross_project_index.json")
	store := NewJSONStore(storePath)

	ctx := context.Background()
	entry := ProjectEntry{
		ProjectPath: "/mnt/d/codeSource/common-server",
		Branch:      "master",
		Symbols: []GlobalSymbol{
			{
				QualifiedName: "com.dayu.pay.SeaPayApi",
				Name:          "SeaPayApi",
				Kind:          "Interface",
				NodeID:        "node-123",
				Annotations:   []string{"FeignClient"},
			},
		},
		Routes: []GlobalRoute{
			{
				Method:      "POST",
				Path:        "/seaPay/query",
				HandlerName: "SeaPayController.query",
				HandlerID:   "handler-456",
				Framework:   "spring",
				Role:        RoleProvider,
			},
		},
		UpdatedAt: 1000,
	}

	if err := store.RegisterProject(ctx, entry); err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	// Lookup symbol within dependency scope
	dependencies := []Dependency{{Path: "/mnt/d/codeSource/common-server", Branch: "master"}}
	matches := store.LookupSymbol(ctx, "com.dayu.pay.SeaPayApi", dependencies)
	if len(matches) != 1 {
		t.Fatalf("LookupSymbol: expected 1 match, got %d", len(matches))
	}
	if matches[0].Symbol.Name != "SeaPayApi" {
		t.Errorf("LookupSymbol: expected SeaPayApi, got %s", matches[0].Symbol.Name)
	}

	// Lookup outside dependency scope returns nothing
	otherDependencies := []Dependency{{Path: "/mnt/d/other-project", Branch: "master"}}
	noMatches := store.LookupSymbol(ctx, "com.dayu.pay.SeaPayApi", otherDependencies)
	if len(noMatches) != 0 {
		t.Errorf("LookupSymbol outside scope: expected 0, got %d", len(noMatches))
	}
}

func TestJSONStore_MatchRoute(t *testing.T) {
	tempDir := t.TempDir()
	store := NewJSONStore(filepath.Join(tempDir, "index.json"))
	ctx := context.Background()

	entry := ProjectEntry{
		ProjectPath: "/project-b",
		Branch:      "master",
		Routes: []GlobalRoute{
			{Method: "POST", Path: "/seaPay/query", HandlerName: "SeaPayController.query", HandlerID: "h1", Framework: "spring", Role: RoleProvider},
			{Method: "GET", Path: "/users", HandlerName: "UserController.list", HandlerID: "h2", Framework: "spring", Role: RoleProvider},
			{Method: "POST", Path: "/feign/call", HandlerName: "FeignApi.call", HandlerID: "h3", Framework: "feign", Role: RoleConsumer},
		},
	}
	_ = store.RegisterProject(ctx, entry)

	dependencies := []Dependency{{Path: "/project-b", Branch: "master"}}

	// Match provider route
	matches := store.MatchRoute(ctx, "POST", "/seaPay/query", dependencies)
	if len(matches) != 1 {
		t.Fatalf("MatchRoute POST /seaPay/query: expected 1, got %d", len(matches))
	}
	if matches[0].Route.HandlerID != "h1" {
		t.Errorf("MatchRoute: expected h1, got %s", matches[0].Route.HandlerID)
	}

	// Consumer routes should NOT be returned
	consumerMatches := store.MatchRoute(ctx, "POST", "/feign/call", dependencies)
	if len(consumerMatches) != 0 {
		t.Errorf("MatchRoute should skip consumer routes, got %d", len(consumerMatches))
	}

	// Method mismatch
	noMatch := store.MatchRoute(ctx, "GET", "/seaPay/query", dependencies)
	if len(noMatch) != 0 {
		t.Errorf("MatchRoute GET /seaPay/query: expected 0, got %d", len(noMatch))
	}
}

func TestJSONStore_UnregisterProject(t *testing.T) {
	tempDir := t.TempDir()
	store := NewJSONStore(filepath.Join(tempDir, "index.json"))
	ctx := context.Background()

	entry := ProjectEntry{
		ProjectPath: "/project-b",
		Branch:      "master",
		Symbols:     []GlobalSymbol{{QualifiedName: "com.example.Foo", Name: "Foo", NodeID: "n1"}},
	}
	_ = store.RegisterProject(ctx, entry)

	dependencies := []Dependency{{Path: "/project-b", Branch: "master"}}
	if len(store.LookupSymbol(ctx, "com.example.Foo", dependencies)) != 1 {
		t.Fatal("expected 1 match before unregister")
	}

	_ = store.UnregisterProject(ctx, "/project-b", "master")
	if len(store.LookupSymbol(ctx, "com.example.Foo", dependencies)) != 0 {
		t.Error("expected 0 matches after unregister")
	}
}

func TestJSONStore_AtomicSave(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "index.json")
	store := NewJSONStore(storePath)
	ctx := context.Background()

	entry := ProjectEntry{
		ProjectPath: "/project-a",
		Branch:      "main",
		Symbols:     []GlobalSymbol{{QualifiedName: "com.example.Bar", Name: "Bar", NodeID: "n2"}},
	}
	_ = store.RegisterProject(ctx, entry)

	// Reload from disk
	store2 := NewJSONStore(storePath)
	if err := store2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	dependencies := []Dependency{{Path: "/project-a", Branch: "main"}}
	matches := store2.LookupSymbol(ctx, "com.example.Bar", dependencies)
	if len(matches) != 1 {
		t.Fatalf("After reload: expected 1 match, got %d", len(matches))
	}

	// Verify no temp file left
	if _, err := os.Stat(storePath + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file should not exist after save")
	}
}

func TestDetermineRouteRole(t *testing.T) {
	tests := []struct {
		framework string
		expected  string
	}{
		{"feign", RoleConsumer},
		{"spring", RoleProvider},
		{"gin", RoleProvider},
		{"express", RoleProvider},
		{"nestjs", RoleProvider},
		{"grpc", RoleProvider},
		{"", RoleProvider},
	}
	for _, testCase := range tests {
		result := DetermineRouteRole(testCase.framework)
		if result != testCase.expected {
			t.Errorf("DetermineRouteRole(%q): expected %s, got %s", testCase.framework, testCase.expected, result)
		}
	}
}
