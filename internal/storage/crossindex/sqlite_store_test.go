package crossindex

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSQLiteStore_RegisterAndLookup(t *testing.T) {
	store := newTestSQLiteStore(t)
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
			{Method: "POST", Path: "/seaPay/query", HandlerName: "SeaPayController.query", HandlerID: "handler-456", Framework: "spring", Role: RoleProvider},
		},
		UpdatedAt: 1000,
	}

	if err := store.RegisterProject(ctx, entry); err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	deps := []Dependency{{Path: "/mnt/d/codeSource/common-server", Branch: "master"}}
	matches := store.LookupSymbol(ctx, "com.dayu.pay.SeaPayApi", deps)
	if len(matches) != 1 {
		t.Fatalf("LookupSymbol exact: expected 1, got %d", len(matches))
	}
	if matches[0].Symbol.Name != "SeaPayApi" {
		t.Errorf("expected SeaPayApi, got %s", matches[0].Symbol.Name)
	}

	// Name match
	matches = store.LookupSymbol(ctx, "SeaPayApi", deps)
	if len(matches) != 1 {
		t.Fatalf("LookupSymbol name: expected 1, got %d", len(matches))
	}

	// Suffix match
	matches = store.LookupSymbol(ctx, "pay.SeaPayApi", deps)
	if len(matches) != 1 {
		t.Fatalf("LookupSymbol suffix: expected 1, got %d", len(matches))
	}

	// Outside dependency scope
	otherDeps := []Dependency{{Path: "/other", Branch: "master"}}
	if len(store.LookupSymbol(ctx, "com.dayu.pay.SeaPayApi", otherDeps)) != 0 {
		t.Error("expected 0 matches outside scope")
	}
}

func TestSQLiteStore_MatchRoute(t *testing.T) {
	store := newTestSQLiteStore(t)
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
	deps := []Dependency{{Path: "/project-b", Branch: "master"}}

	matches := store.MatchRoute(ctx, "POST", "/seaPay/query", deps)
	if len(matches) != 1 {
		t.Fatalf("MatchRoute: expected 1, got %d", len(matches))
	}
	if matches[0].Route.HandlerID != "h1" {
		t.Errorf("expected h1, got %s", matches[0].Route.HandlerID)
	}

	// Consumer routes excluded
	if len(store.MatchRoute(ctx, "POST", "/feign/call", deps)) != 0 {
		t.Error("consumer routes should be excluded")
	}

	// Method mismatch
	if len(store.MatchRoute(ctx, "GET", "/seaPay/query", deps)) != 0 {
		t.Error("method mismatch should return 0")
	}
}

func TestSQLiteStore_MatchRouteByService(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	entry := ProjectEntry{
		ProjectPath: "/project-c",
		Branch:      "master",
		Routes: []GlobalRoute{
			{Method: "", Path: "payment-service", HandlerName: "PaymentService", HandlerID: "h1", Framework: "dubbo", Role: RoleProvider},
		},
	}
	_ = store.RegisterProject(ctx, entry)
	deps := []Dependency{{Path: "/project-c", Branch: "master"}}

	matches := store.MatchRouteByService(ctx, "payment-service", "dubbo", deps)
	if len(matches) != 1 {
		t.Fatalf("MatchRouteByService: expected 1, got %d", len(matches))
	}
}

func TestSQLiteStore_UnregisterProject(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	entry := ProjectEntry{
		ProjectPath: "/project-b",
		Branch:      "master",
		Symbols:     []GlobalSymbol{{QualifiedName: "com.example.Foo", Name: "Foo", NodeID: "n1"}},
	}
	_ = store.RegisterProject(ctx, entry)

	deps := []Dependency{{Path: "/project-b", Branch: "master"}}
	if len(store.LookupSymbol(ctx, "com.example.Foo", deps)) != 1 {
		t.Fatal("expected 1 before unregister")
	}

	_ = store.UnregisterProject(ctx, "/project-b", "master")
	if len(store.LookupSymbol(ctx, "com.example.Foo", deps)) != 0 {
		t.Error("expected 0 after unregister")
	}
}

func TestSQLiteStore_GetDependencySymbols(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	_ = store.RegisterProject(ctx, ProjectEntry{
		ProjectPath: "/project-a", Branch: "master",
		Symbols: []GlobalSymbol{
			{QualifiedName: "com.a.Foo", Name: "Foo", Kind: "Class"},
			{QualifiedName: "com.a.Bar", Name: "Bar", Kind: "Interface"},
		},
	})
	_ = store.RegisterProject(ctx, ProjectEntry{
		ProjectPath: "/project-b", Branch: "main",
		Symbols: []GlobalSymbol{
			{QualifiedName: "com.b.Baz", Name: "Baz", Kind: "Class"},
		},
	})

	depsA := []Dependency{{Path: "/project-a", Branch: "master"}}
	if len(store.GetDependencySymbols(ctx, depsA)) != 2 {
		t.Fatal("expected 2 from project-a")
	}

	depsBoth := []Dependency{
		{Path: "/project-a", Branch: "master"},
		{Path: "/project-b", Branch: "main"},
	}
	if len(store.GetDependencySymbols(ctx, depsBoth)) != 3 {
		t.Fatal("expected 3 from both")
	}

	depsNone := []Dependency{{Path: "/no-such", Branch: "master"}}
	if len(store.GetDependencySymbols(ctx, depsNone)) != 0 {
		t.Fatal("expected 0 for non-existent")
	}
}

func TestSQLiteStore_ListProjects(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	_ = store.RegisterProject(ctx, ProjectEntry{
		ProjectPath: "/project-a", Branch: "master",
		Symbols: []GlobalSymbol{{QualifiedName: "com.a.Foo", Name: "Foo"}},
		Routes:  []GlobalRoute{{Method: "GET", Path: "/foo", HandlerName: "FooCtrl.get", Role: RoleProvider}},
	})

	entries := store.ListProjects(ctx)
	if len(entries) != 1 {
		t.Fatalf("expected 1 project, got %d", len(entries))
	}
	if len(entries[0].Symbols) != 1 || len(entries[0].Routes) != 1 {
		t.Errorf("expected 1 symbol and 1 route, got %d/%d", len(entries[0].Symbols), len(entries[0].Routes))
	}
}

func TestSQLiteStore_Persistence(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	store1 := NewSQLiteStore(dbPath)
	if err := store1.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	ctx := context.Background()
	_ = store1.RegisterProject(ctx, ProjectEntry{
		ProjectPath: "/project-a", Branch: "main",
		Symbols: []GlobalSymbol{{QualifiedName: "com.a.Bar", Name: "Bar", NodeID: "n2"}},
	})
	store1.Close()

	// Reopen
	store2 := NewSQLiteStore(dbPath)
	if err := store2.Load(); err != nil {
		t.Fatalf("Load after reopen: %v", err)
	}
	defer store2.Close()

	deps := []Dependency{{Path: "/project-a", Branch: "main"}}
	if len(store2.LookupSymbol(ctx, "com.a.Bar", deps)) != 1 {
		t.Fatal("data should persist after close/reopen")
	}
}

func TestSQLiteStore_RegisterProjectReplace(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	_ = store.RegisterProject(ctx, ProjectEntry{
		ProjectPath: "/project-a", Branch: "master",
		Symbols: []GlobalSymbol{{QualifiedName: "com.a.Old", Name: "Old"}},
	})
	_ = store.RegisterProject(ctx, ProjectEntry{
		ProjectPath: "/project-a", Branch: "master",
		Symbols: []GlobalSymbol{{QualifiedName: "com.a.New", Name: "New"}},
	})

	deps := []Dependency{{Path: "/project-a", Branch: "master"}}
	if len(store.LookupSymbol(ctx, "com.a.Old", deps)) != 0 {
		t.Error("old symbol should be replaced")
	}
	if len(store.LookupSymbol(ctx, "com.a.New", deps)) != 1 {
		t.Error("new symbol should exist")
	}
}

func newTestSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store := NewSQLiteStore(dbPath)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}
