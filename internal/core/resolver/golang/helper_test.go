package golang_test

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	gohelper "github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver/golang"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestGoHelper_NarrowByScope_SamePackageDir(t *testing.T) {
	helper := gohelper.NewHelper(nil)
	matched := []model.Symbol{
		{ID: "a", Name: "Process", QualifiedName: "pkg/service.Process", FilePath: "/project/pkg/service/process.go"},
		{ID: "b", Name: "Process", QualifiedName: "pkg/handler.Process", FilePath: "/project/pkg/handler/process.go"},
	}
	call := model.RawCall{FilePath: "/project/pkg/service/main.go"}

	result := helper.NarrowByScope(matched, call, nil, nil)
	if len(result) != 1 || result[0].ID != "a" {
		t.Fatalf("expected candidate 'a' (same dir), got %d candidates", len(result))
	}
	t.Log("✅ NarrowByScope picks same package directory")
}

func TestGoHelper_NarrowByScope_ImportMatch(t *testing.T) {
	helper := gohelper.NewHelper(nil)
	matched := []model.Symbol{
		{ID: "a", Name: "New", QualifiedName: "github.com/example/db.New", FilePath: "/project/vendor/db/db.go"},
		{ID: "b", Name: "New", QualifiedName: "github.com/example/cache.New", FilePath: "/project/vendor/cache/cache.go"},
	}
	call := model.RawCall{FilePath: "/project/cmd/main.go"}
	env := &model.TypeEnv{
		Imports: []model.RawImport{
			{ModulePath: "github.com/example/db"},
		},
	}

	result := helper.NarrowByScope(matched, call, env, nil)
	if len(result) != 1 || result[0].ID != "a" {
		t.Fatalf("expected candidate 'a' (import match), got %d candidates", len(result))
	}
	t.Log("✅ NarrowByScope picks import-matched candidate")
}

func TestGoHelper_NarrowByScope_SingleCandidate(t *testing.T) {
	helper := gohelper.NewHelper(nil)
	matched := []model.Symbol{
		{ID: "a", Name: "Run", FilePath: "/project/pkg/run.go"},
	}
	call := model.RawCall{FilePath: "/project/cmd/main.go"}

	result := helper.NarrowByScope(matched, call, nil, nil)
	if len(result) != 1 || result[0].ID != "a" {
		t.Fatal("single candidate should pass through unchanged")
	}
}

func TestGoHelper_NarrowByScope_NoMatch_ReturnsAll(t *testing.T) {
	helper := gohelper.NewHelper(nil)
	matched := []model.Symbol{
		{ID: "a", Name: "Run", FilePath: "/project/pkg/a/run.go"},
		{ID: "b", Name: "Run", FilePath: "/project/pkg/b/run.go"},
	}
	call := model.RawCall{FilePath: "/project/cmd/main.go"}

	result := helper.NarrowByScope(matched, call, nil, nil)
	if len(result) != 2 {
		t.Fatalf("no match should return all, got %d", len(result))
	}
}

func TestGoHelper_InferStringConcat(t *testing.T) {
	helper := gohelper.NewHelper(nil)
	cases := []struct {
		expr string
		want bool
	}{
		{`"hello" + name`, true},
		{`a + "world"`, true},
		{`a + b`, false},
		{`"literal"`, false},
		{`fmt.Sprintf("%s", x)`, false},
	}
	for _, tc := range cases {
		got := helper.InferStringConcat(tc.expr)
		if got != tc.want {
			t.Errorf("InferStringConcat(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestGoHelper_IsTypeAssignable_ExactOnly(t *testing.T) {
	helper := gohelper.NewHelper(nil)
	if !helper.IsTypeAssignable("int", "int") {
		t.Error("same type should be assignable")
	}
	if helper.IsTypeAssignable("int", "int64") {
		t.Error("Go has no implicit type conversion")
	}
}

func TestGoHelper_ShouldFallthrough(t *testing.T) {
	helper := gohelper.NewHelper(nil)
	if !helper.ShouldFallthrough() {
		t.Error("Go should fallthrough (package-level functions)")
	}
}

func TestGoHelper_ResolveOverload_Nil(t *testing.T) {
	helper := gohelper.NewHelper(nil)
	result := helper.ResolveOverload([]model.Symbol{{ID: "a"}, {ID: "b"}}, []string{"int"})
	if result != nil {
		t.Error("Go has no overloading, should return nil")
	}
}

func TestGoHelper_ResolveSuperCall_AlwaysFalse(t *testing.T) {
	helper := gohelper.NewHelper(nil)
	_, ok := helper.ResolveSuperCall(
		model.RawCall{ReceiverExpr: "super"},
		[]model.Symbol{{ID: "a"}},
		[]model.RawHeritage{{ChildName: "A", ParentName: "B", Kind: "extends"}},
		nil, "caller",
	)
	if ok {
		t.Error("Go has no super, should return false")
	}
}

func newGoResolver(table *resolver.SymbolTable) *resolver.Resolver {
	return resolver.NewResolver(table, map[string]resolver.LanguageHelper{
		"go": gohelper.NewHelper(table),
	})
}

func TestGoHelper_Integration_SameFileResolve(t *testing.T) {
	table := resolver.NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "f1", Name: "Handle", QualifiedName: "api.Handle", Kind: "function", FilePath: "/project/api/handler.go"},
		{ID: "f2", Name: "Handle", QualifiedName: "internal.Handle", Kind: "function", FilePath: "/project/internal/handler.go"},
		{ID: "caller", Name: "main", QualifiedName: "api.main", Kind: "function", FilePath: "/project/api/handler.go"},
	})

	r := newGoResolver(table)
	calls := []model.RawCall{{
		CalledName: "Handle",
		CallerName: "main",
		FilePath:   "/project/api/handler.go",
		Language:   "go",
	}}

	// same_file path: caller and f1 are in the same file
	relations, _ := r.ResolveCalls(calls, nil)
	if len(relations) != 1 || relations[0].TargetID != "f1" {
		for _, rel := range relations {
			t.Logf("  → %s resolved_by=%s", rel.TargetID, rel.ResolvedBy)
		}
		t.Fatalf("expected f1 (same file), got %d relations", len(relations))
	}
	t.Log("✅ Go integration: same-file function resolved")
}

func TestInferImplements(t *testing.T) {
	table := resolver.NewSymbolTable()
	table.AddBatch([]model.Symbol{
		// Interface with 2 methods
		{ID: "iface", Name: "Store", QualifiedName: "storage.Store", Kind: "interface", FilePath: "storage.go"},
		{ID: "iface_write", Name: "Write", QualifiedName: "storage.Store.Write", Kind: "function", FilePath: "storage.go", Params: `[{"name":"ctx"},{"name":"data"}]`},
		{ID: "iface_close", Name: "Close", QualifiedName: "storage.Store.Close", Kind: "function", FilePath: "storage.go", Params: `[]`},
		// Struct implementing both methods
		{ID: "impl_class", Name: "FileStore", QualifiedName: "file.FileStore", Kind: "class", ClassType: "struct", FilePath: "file.go"},
		{ID: "impl_write", Name: "Write", QualifiedName: "file.FileStore.Write", Kind: "function", FilePath: "file.go", Params: `[{"name":"ctx"},{"name":"data"}]`},
		{ID: "impl_close", Name: "Close", QualifiedName: "file.FileStore.Close", Kind: "function", FilePath: "file.go", Params: `[]`},
		// Struct missing one method — should NOT match
		{ID: "partial_class", Name: "PartialStore", QualifiedName: "partial.PartialStore", Kind: "class", ClassType: "struct", FilePath: "partial.go"},
		{ID: "partial_write", Name: "Write", QualifiedName: "partial.PartialStore.Write", Kind: "function", FilePath: "partial.go", Params: `[{"name":"ctx"},{"name":"data"}]`},
	})

	helper := gohelper.NewHelper(table)
	relations := helper.InferImplements()

	// FileStore should implement Store
	found := false
	for _, r := range relations {
		if r.SourceID == "impl_class" && r.TargetID == "iface" {
			found = true
			if r.ResolvedBy != "inferred_implements" {
				t.Fatalf("expected inferred_implements, got %s", r.ResolvedBy)
			}
		}
		// PartialStore should NOT implement Store
		if r.SourceID == "partial_class" && r.TargetID == "iface" {
			t.Fatal("PartialStore should not implement Store (missing Close)")
		}
	}
	if !found {
		t.Fatal("FileStore should implement Store")
	}
	t.Logf("✅ InferImplements: %d relations, FileStore→Store matched, PartialStore excluded", len(relations))
}
