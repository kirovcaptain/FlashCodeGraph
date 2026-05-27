package resolver

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// testListJDKHelper provides List.get→T for chain resolution tests.
type testListJDKHelper struct {
	testJavaHelper
}

func (h *testListJDKHelper) LookupMethodReturn(typeName, methodName string) (model.ReturnType, bool) {
	if typeName == "List" && methodName == "get" {
		return model.ReturnType{Name: "T"}, true
	}
	if typeName == "List" && methodName == "stream" {
		return model.ReturnType{Name: "Stream", Args: []model.TypeArg{{Name: "T"}}}, true
	}
	return model.ReturnType{}, false
}

func (h *testListJDKHelper) BuildExternalQualifiedName(typeName, methodName string) string {
	return typeName + "." + methodName
}

// testNestedMapHelper provides Map.get→V and List.get→T for multi-nested generic tests.
type testNestedMapHelper struct {
	testJavaHelper
}

func (h *testNestedMapHelper) LookupMethodReturn(typeName, methodName string) (model.ReturnType, bool) {
	switch typeName + "." + methodName {
	case "Map.get":
		return model.ReturnType{Name: "V"}, true
	case "List.get":
		return model.ReturnType{Name: "T"}, true
	}
	return model.ReturnType{}, false
}

func (h *testNestedMapHelper) BuildExternalQualifiedName(typeName, methodName string) string {
	return typeName + "." + methodName
}

func TestSubstituteGenericParam_NestedMapChainedTypeArgs(t *testing.T) {
	// Scenario: Map<String, Map<Integer, List<Order>>>.get("a").get(1).get(0)
	// After nested() returns, chainedTypeArgs["Map"] = [String, Map<Integer,List<Order>>]
	// Step 1: Map.get → V → matched={Name:"Map", Args:[Integer, List<Order>]}
	//   should cache chainedTypeArgs["Map"] = [Integer, List<Order>] (overwrite)
	// Step 2: Map.get → V → matched={Name:"List", Args:[Order]}
	//   should cache chainedTypeArgs["List"] = [Order]
	// Step 3: List.get → T → matched={Name:"Order"}
	//   return "Order"

	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "map_class", Name: "Map", QualifiedName: "java.util.Map", Kind: "Class", FilePath: "__external__", TypeParams: []string{"K", "V"}},
		{ID: "list_class", Name: "List", QualifiedName: "java.util.List", Kind: "Class", FilePath: "__external__", TypeParams: []string{"T"}},
		{ID: "repo_class", Name: "Repository", QualifiedName: "com.example.Repository", Kind: "Class", FilePath: "Repository.java"},
		{ID: "nested_fn", Name: "nested", QualifiedName: "com.example.Repository.nested", Kind: "Function", FilePath: "Repository.java",
			ReturnTypes: []model.ReturnType{{Name: "Map", Args: []model.TypeArg{{Name: "String"}, {Name: "Map", Args: []model.TypeArg{{Name: "Integer"}, {Name: "List", Args: []model.TypeArg{{Name: "Order"}}}}}}}}},
		{ID: "order_class", Name: "Order", QualifiedName: "com.example.Order", Kind: "Class", FilePath: "Order.java"},
		{ID: "get_total", Name: "getTotal", QualifiedName: "com.example.Order.getTotal", Kind: "Function", FilePath: "Order.java",
			ReturnTypes: []model.ReturnType{{Name: "double"}}},
	})

	envs := map[string]*model.TypeEnv{
		"Consumer.java": {
			Bindings: map[string]*model.TypeInfo{
				"com.example.Consumer.multiNestedTest:repo": {TypeName: "Repository", Tier: 1},
			},
		},
	}

	call := model.RawCall{
		CallerName:  "com.example.Consumer.multiNestedTest",
		CallerScope: "com.example.Consumer.multiNestedTest",
		FilePath:    "Consumer.java",
	}

	helper := &testNestedMapHelper{testJavaHelper: testJavaHelper{symbolTable: table}}
	resolver := NewResolver(table, map[string]LanguageHelper{"java": helper})

	// Test: resolve "repo.nested().get(\"a\").get(1).get(0)" should return "Order"
	result := resolver.resolveChainedReceiverInternal("repo.nested().get(\"a\").get(1).get(0)", call, envs, helper)
	if result != "Order" {
		t.Fatalf("resolveChainedReceiver = %q, want \"Order\"", result)
	}

	// Test: resolveCall for getTotal should resolve to Order.getTotal
	getTotalCall := model.RawCall{
		CalledName:   "getTotal",
		ReceiverExpr: "repo.nested().get(\"a\").get(1).get(0)",
		CallerName:   "com.example.Consumer.multiNestedTest",
		CallerScope:  "com.example.Consumer.multiNestedTest",
		FilePath:     "Consumer.java",
		Language:     "java",
	}
	relations, _ := resolver.resolveCall(getTotalCall, envs)
	if len(relations) == 0 {
		t.Fatal("getTotal not resolved")
	}
	if relations[0].TargetID != "get_total" {
		t.Fatalf("getTotal resolved to %q, want \"get_total\"", relations[0].TargetID)
	}
}

func TestResolveChainedReceiver_ReturnTypeArgs_SubstitutesGeneric(t *testing.T) {
	// Real scenario: List is NOT in SymbolTable (JDK class, not in project source)
	// Only Repository, User, findAll, getName are in SymbolTable
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "repo_class", Name: "Repository", QualifiedName: "com.example.Repository", Kind: "Class", FilePath: "Repository.java"},
		{ID: "find_all", Name: "findAll", QualifiedName: "com.example.Repository.findAll", Kind: "Function", FilePath: "Repository.java",
			ReturnTypes: []model.ReturnType{{Name: "List", Args: []model.TypeArg{{Name: "User"}}}}},
		{ID: "repo_get_name", Name: "getName", QualifiedName: "com.example.Repository.getName", Kind: "Function", FilePath: "Repository.java",
			ReturnTypes: []model.ReturnType{{Name: "String"}}},
		{ID: "user_class", Name: "User", QualifiedName: "com.example.User", Kind: "Class", FilePath: "User.java"},
		{ID: "get_name", Name: "getName", QualifiedName: "com.example.User.getName", Kind: "Function", FilePath: "User.java",
			ReturnTypes: []model.ReturnType{{Name: "String"}}},
		// NOTE: No "List" class Symbol — simulates real environment where JDK classes are not indexed
	})

	envs := map[string]*model.TypeEnv{
		"Consumer.java": {
			Bindings: map[string]*model.TypeInfo{
				"com.example.Consumer.directChainTest:repo": {TypeName: "Repository", Tier: 1},
			},
		},
	}

	call := model.RawCall{
		CallerName:  "com.example.Consumer.directChainTest",
		CallerScope: "com.example.Consumer.directChainTest",
		FilePath:    "Consumer.java",
	}

	helper := &testListJDKHelper{testJavaHelper: testJavaHelper{symbolTable: table}}
	resolver := NewResolver(table, map[string]LanguageHelper{"java": helper})

	// Simulate prior resolve of "get" (creates external:java.util.List.get + external:java.util.List class)
	getCall := model.RawCall{
		CalledName:   "get",
		ReceiverExpr: "repo.findAll()",
		CallerName:   "com.example.Consumer.directChainTest",
		CallerScope:  "com.example.Consumer.directChainTest",
		FilePath:     "Consumer.java",
		Language:     "java",
	}
	resolver.resolveCall(getCall, envs)

	// Test: resolveChainedReceiver for "repo.findAll().get(0)" should return "User"
	result := resolver.resolveChainedReceiverInternal("repo.findAll().get(0)", call, envs, helper)
	if result != "User" {
		t.Fatalf("resolveChainedReceiver = %q, want \"User\"", result)
	}
	t.Log("✅ resolveChainedReceiver returns User (without List in SymbolTable)")

	// Test: full resolveCall for getName
	getNameCall := model.RawCall{
		CalledName:   "getName",
		ReceiverExpr: "repo.findAll().get(0)",
		CallerName:   "com.example.Consumer.directChainTest",
		CallerScope:  "com.example.Consumer.directChainTest",
		FilePath:     "Consumer.java",
		Language:     "java",
	}
	relations, _ := resolver.resolveCall(getNameCall, envs)
	if len(relations) == 0 {
		t.Fatal("getName not resolved")
	}
	if relations[0].TargetID != "get_name" {
		t.Fatalf("getName resolved to %q, want \"get_name\"", relations[0].TargetID)
	}
	t.Log("✅ resolveCall resolves getName → com.example.User.getName")
}
