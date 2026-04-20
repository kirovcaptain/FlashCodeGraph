package typescript_test

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	tshelper "github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver/typescript"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestTSHelper_ResolveSuperCall_Method(t *testing.T) {
	helper := tshelper.NewHelper()
	funcCandidates := []model.Symbol{
		{ID: "parent_render", Name: "render", QualifiedName: "Component.render", Kind: "function"},
	}
	heritage := []model.RawHeritage{
		{ChildName: "App", ParentName: "Component", Kind: "extends", FilePath: "app.ts"},
	}
	call := model.RawCall{
		CalledName:   "render",
		CallerName:   "App.componentDidMount",
		FilePath:     "app.ts",
		ReceiverExpr: "super",
	}

	relations, ok := helper.ResolveSuperCall(call, funcCandidates, heritage, nil, "caller")
	if !ok || len(relations) != 1 {
		t.Fatalf("expected 1 relation, got ok=%v len=%d", ok, len(relations))
	}
	if relations[0].TargetID != "parent_render" {
		t.Fatalf("expected parent_render, got %s", relations[0].TargetID)
	}
	t.Log("✅ TS super.render() resolves to parent class method")
}

func TestTSHelper_ResolveSuperCall_Constructor(t *testing.T) {
	helper := tshelper.NewHelper()
	funcCandidates := []model.Symbol{
		{ID: "parent_ctor", Name: "constructor", QualifiedName: "Base.constructor", Kind: "function"},
	}
	heritage := []model.RawHeritage{
		{ChildName: "Child", ParentName: "Base", Kind: "extends", FilePath: "child.ts"},
	}
	// super() — CalledName is empty for constructor calls
	call := model.RawCall{
		CalledName:   "",
		CallerName:   "Child.constructor",
		FilePath:     "child.ts",
		ReceiverExpr: "super",
	}

	relations, ok := helper.ResolveSuperCall(call, funcCandidates, heritage, nil, "caller")
	if !ok || len(relations) != 1 {
		t.Fatalf("expected 1 relation for super(), got ok=%v len=%d", ok, len(relations))
	}
	if relations[0].TargetID != "parent_ctor" {
		t.Fatalf("expected parent_ctor, got %s", relations[0].TargetID)
	}
	t.Log("✅ TS super() constructor resolves to parent constructor")
}

func TestTSHelper_ResolveSuperCall_NoHeritage(t *testing.T) {
	helper := tshelper.NewHelper()
	call := model.RawCall{ReceiverExpr: "super"}
	_, ok := helper.ResolveSuperCall(call, nil, nil, nil, "caller")
	if ok {
		t.Error("no heritage should return false")
	}
}

func TestTSHelper_NarrowByScope_SameFile(t *testing.T) {
	helper := tshelper.NewHelper()
	matched := []model.Symbol{
		{ID: "a", Name: "render", FilePath: "app.ts"},
		{ID: "b", Name: "render", FilePath: "other.ts"},
	}
	call := model.RawCall{FilePath: "app.ts"}
	env := &model.TypeEnv{}

	result := helper.NarrowByScope(matched, call, env, nil)
	if len(result) != 1 || result[0].ID != "a" {
		t.Fatalf("expected same-file candidate 'a', got %d candidates", len(result))
	}
	t.Log("✅ NarrowByScope picks same-file candidate")
}

func TestTSHelper_NarrowByScope_ImportMatch(t *testing.T) {
	helper := tshelper.NewHelper()
	matched := []model.Symbol{
		{ID: "a", Name: "connect", FilePath: "src/db/pool.ts"},
		{ID: "b", Name: "connect", FilePath: "src/cache/redis.ts"},
	}
	call := model.RawCall{FilePath: "src/app.ts"}
	env := &model.TypeEnv{
		Imports: []model.RawImport{
			{ModulePath: "db/pool", SymbolName: "connect"},
		},
	}

	result := helper.NarrowByScope(matched, call, env, nil)
	if len(result) != 1 || result[0].ID != "a" {
		t.Fatalf("expected import-matched candidate 'a', got %d candidates", len(result))
	}
	t.Log("✅ NarrowByScope picks import-matched candidate")
}

func TestTSHelper_NarrowByScope_NoEnv(t *testing.T) {
	helper := tshelper.NewHelper()
	matched := []model.Symbol{
		{ID: "a", Name: "run", FilePath: "a.ts"},
		{ID: "b", Name: "run", FilePath: "b.ts"},
	}
	call := model.RawCall{FilePath: "main.ts"}

	result := helper.NarrowByScope(matched, call, nil, nil)
	if len(result) != 2 {
		t.Fatalf("no env should return all, got %d", len(result))
	}
}

func TestTSHelper_InferStringConcat(t *testing.T) {
	helper := tshelper.NewHelper()
	cases := []struct {
		expr string
		want bool
	}{
		{`"hello" + name`, true},
		{`a + "world"`, true},
		{"a + b", false},
		{"`hello ${name}`", true},
		{"`template`", true},
		{"someFunc()", false},
	}
	for _, tc := range cases {
		got := helper.InferStringConcat(tc.expr)
		if got != tc.want {
			t.Errorf("InferStringConcat(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestTSHelper_IsTypeAssignable_ExactOnly(t *testing.T) {
	helper := tshelper.NewHelper()
	if !helper.IsTypeAssignable("string", "string") {
		t.Error("same type should be assignable")
	}
	if helper.IsTypeAssignable("number", "string") {
		t.Error("TS static analysis — only exact match")
	}
}

func TestTSHelper_ShouldFallthrough(t *testing.T) {
	helper := tshelper.NewHelper()
	if !helper.ShouldFallthrough() {
		t.Error("TS should fallthrough (module imports as receivers)")
	}
}

func newTSResolver(table *resolver.SymbolTable) *resolver.Resolver {
	return resolver.NewResolver(table, map[string]resolver.LanguageHelper{
		"typescript": tshelper.NewHelper(),
		"javascript": tshelper.NewHelper(),
	})
}

func TestTSHelper_Integration_SuperMethod(t *testing.T) {
	table := resolver.NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "parent_next", Name: "next", QualifiedName: "Middleware.next", Kind: "function", FilePath: "middleware.ts"},
		{ID: "child_handle", Name: "handle", QualifiedName: "AuthMiddleware.handle", Kind: "function", FilePath: "auth.ts"},
	})

	r := newTSResolver(table)
	r.SetHeritage([]model.RawHeritage{
		{ChildName: "AuthMiddleware", ParentName: "Middleware", Kind: "extends", FilePath: "auth.ts"},
	})

	calls := []model.RawCall{{
		CalledName:   "next",
		CallerName:   "AuthMiddleware.handle",
		FilePath:     "auth.ts",
		ReceiverExpr: "super",
		Language:     "typescript",
	}}

	relations, _ := r.ResolveCalls(calls, nil)
	if len(relations) != 1 || relations[0].TargetID != "parent_next" {
		t.Fatalf("expected parent_next, got %v", relations)
	}
	t.Log("✅ TS integration: super.next() resolves to parent class method")
}

func TestTSHelper_Integration_SuperConstructor(t *testing.T) {
	table := resolver.NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "base_ctor", Name: "constructor", QualifiedName: "EventEmitter.constructor", Kind: "function", FilePath: "emitter.ts"},
		{ID: "child_ctor", Name: "constructor", QualifiedName: "Server.constructor", Kind: "function", FilePath: "server.ts"},
	})

	r := newTSResolver(table)
	r.SetHeritage([]model.RawHeritage{
		{ChildName: "Server", ParentName: "EventEmitter", Kind: "extends", FilePath: "server.ts"},
	})

	calls := []model.RawCall{{
		CalledName:   "constructor",
		CallerName:   "Server.constructor",
		FilePath:     "server.ts",
		ReceiverExpr: "super",
		Language:     "typescript",
	}}

	relations, _ := r.ResolveCalls(calls, nil)
	if len(relations) != 1 || relations[0].TargetID != "base_ctor" {
		t.Fatalf("expected base_ctor, got %v", relations)
	}
	t.Log("✅ TS integration: super() resolves to parent constructor")
}
