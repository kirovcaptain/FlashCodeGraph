package java_test

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	java "github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver/java"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func newJavaResolver(table *resolver.SymbolTable) *resolver.Resolver {
	return resolver.NewResolver(table, map[string]resolver.LanguageHelper{
		"java": java.NewHelper(table, nil),
	})
}

func TestJavaHelper_LoggerOverload(t *testing.T) {
	// log.error("msg", e) where e is Exception
	// error(String, Object) vs error(String, Throwable) — should pick Throwable (more specific)
	table := resolver.NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "err_obj", Name: "error", QualifiedName: "com.example.LoggerUtil.error", Kind: "Function", FilePath: "LoggerUtil.java", Params: []model.ParamInfo{{Name: "format", Type: "String"}, {Name: "arg", Type: "Object"}}},
		{ID: "err_thr", Name: "error", QualifiedName: "com.example.LoggerUtil.error", Kind: "Function", FilePath: "LoggerUtil.java", Params: []model.ParamInfo{{Name: "msg", Type: "String"}, {Name: "t", Type: "Throwable"}}},
		{ID: "c_log", Name: "LoggerUtil", QualifiedName: "com.example.LoggerUtil", Kind: "Class", FilePath: "LoggerUtil.java"},
		{ID: "caller1", Name: "handle", QualifiedName: "com.example.Controller.handle", Kind: "Function", FilePath: "Controller.java"},
		{ID: "c_ctrl", Name: "Controller", QualifiedName: "com.example.Controller", Kind: "Class", FilePath: "Controller.java"},
	})

	r := newJavaResolver(table)
	envs := map[string]*model.TypeEnv{
		"Controller.java": {
			Bindings: map[string]*model.TypeInfo{
				"Controller:loggerUtil": {TypeName: "LoggerUtil"},
				"Controller.handle:e":  {TypeName: "Exception"},
			},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "error",
		CallerName:   "Controller.handle",
		FilePath:     "Controller.java",
		ReceiverExpr: "loggerUtil",
		ArgCount:     2,
		ArgTypes:     []string{"String", ""},
		ArgExprs:     []string{`"查询异常"`, "e"},
		Language:     "java",
	}}

	relations, _ := r.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "err_thr" {
		t.Fatalf("expected err_thr (Throwable overload), got %s", relations[0].TargetID)
	}
	t.Logf("✅ log.error('msg', e) → error(String, Throwable) via JDK hierarchy (resolved_by: %s)", relations[0].ResolvedBy)
}

func TestJavaHelper_CatchChainedExpr(t *testing.T) {
	// logger.error(e.getMessage(), e) inside catch block
	// arg0: e.getMessage() → JDK "String", arg1: e → "Exception"
	// Should resolve to error(String, Throwable) via JDK hierarchy
	table := resolver.NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "err_obj", Name: "error", QualifiedName: "com.example.LoggerUtil.error", Kind: "Function", FilePath: "LoggerUtil.java", Params: []model.ParamInfo{{Name: "format", Type: "String"}, {Name: "arg", Type: "Object"}}},
		{ID: "err_thr", Name: "error", QualifiedName: "com.example.LoggerUtil.error", Kind: "Function", FilePath: "LoggerUtil.java", Params: []model.ParamInfo{{Name: "msg", Type: "String"}, {Name: "t", Type: "Throwable"}}},
		{ID: "err_var", Name: "error", QualifiedName: "com.example.LoggerUtil.error", Kind: "Function", FilePath: "LoggerUtil.java", Params: []model.ParamInfo{{Name: "format", Type: "String"}, {Name: "arguments", Type: "Object..."}}},
		{ID: "logger_cls", Name: "LoggerUtil", QualifiedName: "com.example.LoggerUtil", Kind: "Class", FilePath: "LoggerUtil.java"},
		{ID: "caller1", Name: "zCard", QualifiedName: "com.example.RedisUtil.zCard", Kind: "Function", FilePath: "RedisUtil.java"},
	})

	r := newJavaResolver(table)
	envs := map[string]*model.TypeEnv{
		"RedisUtil.java": {
			Bindings: map[string]*model.TypeInfo{
				"RedisUtil:logger":  {TypeName: "LoggerUtil"},
				"RedisUtil.zCard:e": {TypeName: "Exception"},
			},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "error",
		CallerName:   "RedisUtil.zCard",
		FilePath:     "RedisUtil.java",
		ReceiverExpr: "logger",
		ArgCount:     2,
		ArgTypes:     []string{"", ""},
		ArgExprs:     []string{"e.getMessage()", "e"},
		Language:     "java",
	}}

	relations, _ := r.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation (error(String,Throwable)), got %d", len(relations))
	}
	if relations[0].TargetID != "err_thr" {
		t.Fatalf("expected err_thr, got %s", relations[0].TargetID)
	}
	t.Log("✅ logger.error(e.getMessage(), e) resolved to error(String, Throwable)")
}

func TestJavaHelper_IsTypeCompatible(t *testing.T) {
	cases := []struct {
		arg, param string
		want       bool
	}{
		{"int", "int", true},
		{"int", "Integer", true},
		{"Integer", "int", true},
		{"int", "Object", true},
		{"Exception", "Throwable", true},
		{"String", "Long", false},
		{"String", "Object", true},
	}
	for _, tc := range cases {
		got := java.IsTypeCompatible(tc.arg, tc.param)
		if got != tc.want {
			t.Errorf("IsTypeCompatible(%q, %q) = %v, want %v", tc.arg, tc.param, got, tc.want)
		}
	}
}

func TestJavaHelper_SelectMostSpecific(t *testing.T) {
	candidates := []model.Symbol{
		{ID: "obj", Name: "error", Params: []model.ParamInfo{{Name: "msg", Type: "String"}, {Name: "arg", Type: "Object"}}},
		{ID: "thr", Name: "error", Params: []model.ParamInfo{{Name: "msg", Type: "String"}, {Name: "t", Type: "Throwable"}}},
	}
	result := java.SelectMostSpecific(candidates, []string{"String", "Exception"})
	if result == nil {
		t.Fatal("expected a result")
	}
	if result.ID != "thr" {
		t.Fatalf("expected thr, got %s", result.ID)
	}
	t.Log("✅ SelectMostSpecific picks Throwable over Object for Exception arg")
}

func TestJavaHelper_FilterByArgTypes_Exclusion(t *testing.T) {
	candidates := []model.Symbol{
		{Name: "e", Params: []model.ParamInfo{{Name: "code", Type: "ResponseCode"}, {Name: "data", Type: "T"}}},
		{Name: "e", Params: []model.ParamInfo{{Name: "status", Type: "Integer"}, {Name: "msg", Type: "String"}}},
	}
	helper := java.NewHelper(resolver.NewSymbolTable(), nil)

	// e(200, "msg") → ArgTypes=["int", "String"] → exclude ResponseCode, int→Integer via boxing
	result := resolver.FilterByArgTypesWithHelper(candidates, []string{"int", "String"}, helper)
	if len(result) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(result))
	}
	t.Log("✅ filterByArgTypes: int→Integer boxing excludes ResponseCode")

	// e(ResponseCode.FAIL.code, body) → ArgTypes=["!ResponseCode", ""]
	result = resolver.FilterByArgTypesWithHelper(candidates, []string{"!ResponseCode", ""}, helper)
	if len(result) != 1 {
		t.Fatalf("expected 1 candidate via exclusion, got %d", len(result))
	}
	t.Log("✅ filterByArgTypes: !ResponseCode exclusion hint works")

	// e(someCode, someData) → ArgTypes=["", ""] → no exclusion
	result = resolver.FilterByArgTypesWithHelper(candidates, []string{"", ""}, helper)
	if len(result) != 2 {
		t.Fatalf("expected 2 candidates (no exclusion), got %d", len(result))
	}
	t.Log("✅ filterByArgTypes: empty args → no exclusion")
}
