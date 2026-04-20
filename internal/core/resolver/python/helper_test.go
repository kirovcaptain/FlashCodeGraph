package python_test

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	pyhelper "github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver/python"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestPythonHelper_ResolveSuperCall(t *testing.T) {
	helper := pyhelper.NewHelper()
	funcCandidates := []model.Symbol{
		{ID: "parent_init", Name: "__init__", QualifiedName: "Animal.__init__", Kind: "function"},
		{ID: "other_init", Name: "__init__", QualifiedName: "Vehicle.__init__", Kind: "function"},
	}
	heritage := []model.RawHeritage{
		{ChildName: "Dog", ParentName: "Animal", Kind: "extends", FilePath: "dog.py"},
	}
	call := model.RawCall{
		CalledName:   "__init__",
		CallerName:   "Dog.__init__",
		FilePath:     "dog.py",
		ReceiverExpr: "super",
		Language:     "python",
	}

	relations, ok := helper.ResolveSuperCall(call, funcCandidates, heritage, nil, "caller")
	if !ok || len(relations) != 1 {
		t.Fatalf("expected 1 relation, got ok=%v len=%d", ok, len(relations))
	}
	if relations[0].TargetID != "parent_init" {
		t.Fatalf("expected parent_init, got %s", relations[0].TargetID)
	}
	t.Log("✅ Python super().__init__() resolves to parent class")
}

func TestPythonHelper_ResolveSuperCall_NoHeritage(t *testing.T) {
	helper := pyhelper.NewHelper()
	call := model.RawCall{ReceiverExpr: "super"}
	_, ok := helper.ResolveSuperCall(call, nil, nil, nil, "caller")
	if ok {
		t.Error("no heritage should return false")
	}
}

func TestPythonHelper_ResolveSuperCall_NotSuper(t *testing.T) {
	helper := pyhelper.NewHelper()
	call := model.RawCall{ReceiverExpr: "self"}
	_, ok := helper.ResolveSuperCall(call, nil, []model.RawHeritage{{ChildName: "A", ParentName: "B", Kind: "extends"}}, nil, "caller")
	if ok {
		t.Error("non-super receiver should return false")
	}
}

func TestPythonHelper_NarrowByScope_SameFile(t *testing.T) {
	helper := pyhelper.NewHelper()
	matched := []model.Symbol{
		{ID: "a", Name: "process", FilePath: "app.py"},
		{ID: "b", Name: "process", FilePath: "utils.py"},
	}
	call := model.RawCall{FilePath: "app.py"}
	env := &model.TypeEnv{}

	result := helper.NarrowByScope(matched, call, env, nil)
	if len(result) != 1 || result[0].ID != "a" {
		t.Fatalf("expected same-file candidate 'a', got %d candidates", len(result))
	}
	t.Log("✅ NarrowByScope picks same-file candidate")
}

func TestPythonHelper_NarrowByScope_ImportMatch(t *testing.T) {
	helper := pyhelper.NewHelper()
	matched := []model.Symbol{
		{ID: "a", Name: "connect", QualifiedName: "db.pool.connect", FilePath: "db/pool.py"},
		{ID: "b", Name: "connect", QualifiedName: "cache.redis.connect", FilePath: "cache/redis.py"},
	}
	call := model.RawCall{FilePath: "app.py"}
	env := &model.TypeEnv{
		Imports: []model.RawImport{
			{ModulePath: "db.pool", SymbolName: "connect"},
		},
	}

	result := helper.NarrowByScope(matched, call, env, nil)
	if len(result) != 1 || result[0].ID != "a" {
		t.Fatalf("expected import-matched candidate 'a', got %d candidates", len(result))
	}
	t.Log("✅ NarrowByScope picks import-matched candidate")
}

func TestPythonHelper_NarrowByScope_NoEnv(t *testing.T) {
	helper := pyhelper.NewHelper()
	matched := []model.Symbol{
		{ID: "a", Name: "run", FilePath: "a.py"},
		{ID: "b", Name: "run", FilePath: "b.py"},
	}
	call := model.RawCall{FilePath: "main.py"}

	result := helper.NarrowByScope(matched, call, nil, nil)
	if len(result) != 2 {
		t.Fatalf("no env should return all, got %d", len(result))
	}
}

func TestPythonHelper_InferStringConcat(t *testing.T) {
	helper := pyhelper.NewHelper()
	cases := []struct {
		expr string
		want bool
	}{
		{`"hello" + name`, true},
		{`name + 'world'`, true},
		{`a + b`, false},
		{`f"hello {name}"`, true},
		{`f'value: {x}'`, true},
		{`some_func()`, false},
	}
	for _, tc := range cases {
		got := helper.InferStringConcat(tc.expr)
		if got != tc.want {
			t.Errorf("InferStringConcat(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestPythonHelper_IsTypeAssignable_ExactOnly(t *testing.T) {
	helper := pyhelper.NewHelper()
	if !helper.IsTypeAssignable("str", "str") {
		t.Error("same type should be assignable")
	}
	if helper.IsTypeAssignable("int", "float") {
		t.Error("Python duck typing — only exact match statically")
	}
}

func TestPythonHelper_ShouldFallthrough(t *testing.T) {
	helper := pyhelper.NewHelper()
	if !helper.ShouldFallthrough() {
		t.Error("Python should fallthrough (module-level functions)")
	}
}

func newPythonResolver(table *resolver.SymbolTable) *resolver.Resolver {
	return resolver.NewResolver(table, map[string]resolver.LanguageHelper{
		"python": pyhelper.NewHelper(),
	})
}

func TestPythonHelper_Integration_SuperCall(t *testing.T) {
	table := resolver.NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "parent_save", Name: "save", QualifiedName: "BaseModel.save", Kind: "function", FilePath: "base.py"},
		{ID: "child_save", Name: "save", QualifiedName: "User.save", Kind: "function", FilePath: "user.py"},
		{ID: "caller", Name: "save", QualifiedName: "User.save", Kind: "function", FilePath: "user.py"},
	})

	r := newPythonResolver(table)
	r.SetHeritage([]model.RawHeritage{
		{ChildName: "User", ParentName: "BaseModel", Kind: "extends", FilePath: "user.py"},
	})

	calls := []model.RawCall{{
		CalledName:   "save",
		CallerName:   "User.save",
		FilePath:     "user.py",
		ReceiverExpr: "super",
		Language:     "python",
	}}

	relations, _ := r.ResolveCalls(calls, nil)
	if len(relations) != 1 || relations[0].TargetID != "parent_save" {
		t.Fatalf("expected parent_save, got %v", relations)
	}
	t.Log("✅ Python integration: super().save() resolves to parent class")
}
