package typeinfer

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestInferLocal_Tier0_TypeAnnotations(t *testing.T) {
	infer := New()
	result := &model.ParseResult{
		TypeHints: []model.TypeBinding{
			{VarName: "dao", TypeName: "UserDao", Tier: 0, Scope: "UserService"},
			{VarName: "id", TypeName: "int", Tier: 0, Scope: "UserService.findById"},
		},
	}

	env := infer.InferLocal(result)

	if env.Bindings["UserService:dao"] == nil {
		t.Fatal("missing dao binding")
	}
	if env.Bindings["UserService:dao"].TypeName != "UserDao" {
		t.Fatalf("expected UserDao, got %s", env.Bindings["UserService:dao"].TypeName)
	}
	if env.Bindings["UserService.findById:id"].TypeName != "int" {
		t.Fatalf("expected int, got %s", env.Bindings["UserService.findById:id"].TypeName)
	}
	t.Log("✅ Tier 0 type annotations work")
}

func TestInferLocal_Tier1_Constructor(t *testing.T) {
	infer := New()
	result := &model.ParseResult{
		Calls: []model.RawCall{
			{CalledName: "UserService", CallerName: "main", ReceiverExpr: ""},
			{CalledName: "NewUserDao", CallerName: "init", ReceiverExpr: ""},
		},
	}

	env := infer.InferLocal(result)

	// UserService() → type UserService
	if env.Bindings["main:userService"] == nil {
		t.Fatal("missing constructor inference for UserService")
	}
	if env.Bindings["main:userService"].TypeName != "UserService" {
		t.Fatalf("expected UserService, got %s", env.Bindings["main:userService"].TypeName)
	}

	// NewUserDao() → type UserDao
	if env.Bindings["init:userDao"] == nil {
		t.Fatal("missing constructor inference for NewUserDao")
	}
	if env.Bindings["init:userDao"].TypeName != "UserDao" {
		t.Fatalf("expected UserDao, got %s", env.Bindings["init:userDao"].TypeName)
	}

	t.Log("✅ Tier 1 constructor inference works")
}

func TestInferLocal_SelfReceiver(t *testing.T) {
	infer := New()
	result := &model.ParseResult{
		Symbols: []model.Symbol{
			{Name: "findById", QualifiedName: "UserService.findById", Kind: "Function"},
		},
	}

	env := infer.InferLocal(result)

	selfKey := "UserService.findById:self"
	if env.Bindings[selfKey] == nil {
		t.Fatal("missing self binding")
	}
	if env.Bindings[selfKey].TypeName != "UserService" {
		t.Fatalf("expected UserService, got %s", env.Bindings[selfKey].TypeName)
	}

	thisKey := "UserService.findById:this"
	if env.Bindings[thisKey] == nil {
		t.Fatal("missing this binding")
	}

	t.Log("✅ self/this receiver inference works")
}

func TestInferConstructorType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"UserService", "UserService"},
		{"NewUserDao", "UserDao"},
		{"NewDB", "DB"},
		{"helper", ""},
		{"new", ""},
		{"", ""},
		{"New", ""},
	}

	for _, test := range tests {
		result := inferConstructorType(test.input)
		if result != test.expected {
			t.Errorf("inferConstructorType(%q) = %q, want %q", test.input, result, test.expected)
		}
	}
}

func TestTopologicalSort(t *testing.T) {
	graph := map[string][]string{
		"a.go": {"b.go", "c.go"},
		"b.go": {"c.go"},
		"c.go": {},
	}

	order := topologicalSort(graph)

	// c.go should come before b.go, b.go before a.go
	indexOf := func(s string) int {
		for i, v := range order {
			if v == s {
				return i
			}
		}
		return -1
	}

	if indexOf("c.go") > indexOf("b.go") {
		t.Fatal("c.go should come before b.go")
	}
	if indexOf("b.go") > indexOf("a.go") {
		t.Fatal("b.go should come before a.go")
	}
	t.Log("✅ Topological sort works")
}

func TestPropagate_CrossFile(t *testing.T) {
	infer := New()

	results := []model.ParseResult{
		{
			FilePath: "dao.go",
			Symbols: []model.Symbol{
				{Name: "FindById", QualifiedName: "dao.FindById", Kind: "Function", ReturnTypes: []model.ReturnType{{Name: "User"}}, IsExported: true},
			},
		},
		{
			FilePath: "service.go",
			Calls: []model.RawCall{
				{CalledName: "FindById", CallerName: "GetUser", FilePath: "service.go"},
			},
		},
	}

	importGraph := map[string][]string{
		"service.go": {"dao.go"},
		"dao.go":     {},
	}

	envs := map[string]*model.TypeEnv{
		"dao.go":     {Bindings: make(map[string]*model.TypeInfo)},
		"service.go": {Bindings: make(map[string]*model.TypeInfo)},
	}

	updatedEnvs, affectedFiles := infer.Propagate(results, importGraph, envs)

	if len(affectedFiles) != 1 || affectedFiles[0] != "service.go" {
		t.Fatalf("expected service.go affected, got %v", affectedFiles)
	}

	key := "GetUser:FindById_result"
	if updatedEnvs["service.go"].Bindings[key] == nil {
		t.Fatal("missing cross-file type propagation")
	}
	if updatedEnvs["service.go"].Bindings[key].TypeName != "User" {
		t.Fatalf("expected User, got %s", updatedEnvs["service.go"].Bindings[key].TypeName)
	}

	t.Log("✅ Cross-file type propagation works")
}

func TestInferLocal_Tier2a_CopyPropagation(t *testing.T) {
	infer := New()

	// "svc" is typed at global scope (e.g., from Tier 1 constructor).
	// Inside "handler", svc.findById() should get svc's type via copy propagation.
	result := &model.ParseResult{
		FilePath: "main.py",
		Symbols:  []model.Symbol{{Name: "handler", Kind: "Function", FilePath: "main.py"}},
		Calls: []model.RawCall{
			{CallerName: "handler", CalledName: "findById", ReceiverExpr: "svc", FilePath: "main.py"},
		},
		TypeHints: []model.TypeBinding{
			// Tier 0: explicit annotation "svc: UserService"
			{VarName: "svc", TypeName: "UserService", FilePath: "main.py"},
		},
	}

	env := infer.InferLocal(result)

	// Global "svc" should be typed from Tier 0 annotation
	if info, ok := env.Bindings["svc"]; !ok || info.TypeName != "UserService" {
		t.Fatalf("global svc expected UserService, got %v", env.Bindings["svc"])
	}

	// Tier 2a: "handler:svc" should be copied from global "svc"
	scopedSvc := "handler:svc"
	info, ok := env.Bindings[scopedSvc]
	if !ok {
		t.Fatalf("Tier 2a failed: %s not in bindings (keys: %v)", scopedSvc, keys(env.Bindings))
	}
	if info.TypeName != "UserService" {
		t.Fatalf("expected UserService, got %s", info.TypeName)
	}
	if info.Tier != 2 {
		t.Fatalf("expected Tier 2, got %d", info.Tier)
	}
	t.Log("✅ Tier 2a: global svc (Tier 0) → handler:svc (Tier 2) = UserService")
}

func keys(m map[string]*model.TypeInfo) []string {
	var result []string
	for k := range m {
		result = append(result, k)
	}
	return result
}

func TestInferLocal_ImportResolveQualifiedName(t *testing.T) {
	infer := New()

	result := &model.ParseResult{
		FilePath: "OrderService.java",
		Imports: []model.RawImport{
			{ModulePath: "org.springframework.web.client.RestTemplate", SymbolName: "RestTemplate", FilePath: "OrderService.java"},
		},
		TypeHints: []model.TypeBinding{
			{VarName: "restTemplate", TypeName: "RestTemplate", Tier: 0, Scope: "OrderService", FilePath: "OrderService.java"},
		},
	}

	env := infer.InferLocal(result)

	key := "OrderService:restTemplate"
	info, ok := env.Bindings[key]
	if !ok {
		t.Fatalf("missing binding for %s", key)
	}
	if info.TypeName != "org.springframework.web.client.RestTemplate" {
		t.Fatalf("expected fully qualified name, got %s", info.TypeName)
	}
	t.Log("✅ Tier 0.5: RestTemplate → org.springframework.web.client.RestTemplate")
}

func TestInferLocal_ParamTypeHints(t *testing.T) {
	infer := New()

	// Simulate: void process(RestTemplate rt) { rt.getForObject(...) }
	result := &model.ParseResult{
		FilePath: "Service.java",
		TypeHints: []model.TypeBinding{
			{VarName: "rt", TypeName: "RestTemplate", Tier: 0, Scope: "process", FilePath: "Service.java"},
		},
		Calls: []model.RawCall{
			{CallerName: "process", CalledName: "getForObject", ReceiverExpr: "rt", FilePath: "Service.java"},
		},
	}

	env := infer.InferLocal(result)

	key := "process:rt"
	info, ok := env.Bindings[key]
	if !ok {
		t.Fatalf("missing binding for %s (param type not in env)", key)
	}
	if info.TypeName != "RestTemplate" {
		t.Fatalf("expected RestTemplate, got %s", info.TypeName)
	}
	t.Log("✅ Param type hint: process(RestTemplate rt) → process:rt = RestTemplate")
}

func TestInferLocal_LocalReturnType(t *testing.T) {
	infer := New()

	result := &model.ParseResult{
		FilePath: "main.go",
		Symbols: []model.Symbol{
			{Name: "getUser", Kind: "Function", ReturnTypes: []model.ReturnType{{Name: "User"}}, FilePath: "main.go"},
			{Name: "process", Kind: "Function", FilePath: "main.go"},
		},
		Calls: []model.RawCall{
			{CallerName: "process", CalledName: "getUser", FilePath: "main.go"},
		},
	}

	env := infer.InferLocal(result)

	key := "process:getUser_result"
	info, ok := env.Bindings[key]
	if !ok {
		t.Fatalf("missing binding for %s (local return type not inferred)", key)
	}
	if info.TypeName != "User" {
		t.Fatalf("expected User, got %s", info.TypeName)
	}
	if info.Tier != 2 {
		t.Fatalf("expected Tier 2, got %d", info.Tier)
	}
	t.Log("✅ Tier 2c: process calls getUser() → process:getUser_result = User")
}

func TestInferMultiReturn_StructField(t *testing.T) {
	infer := New()

	env := &model.TypeEnv{Bindings: map[string]*model.TypeInfo{
		"openGraphStore:store": {
			MultiReturnOf: "kuzu.New",
			ReturnIndex:   0,
			Scope:         "openGraphStore",
		},
		"openGraphStore:err": {
			MultiReturnOf: "kuzu.New",
			ReturnIndex:   1,
			Scope:         "openGraphStore",
		},
	}}

	findByName := func(name string) []model.Symbol {
		if name == "New" {
			return []model.Symbol{
				{Name: "New", QualifiedName: "kuzu.New", ReturnTypes: []model.ReturnType{{Name: "*kuzu.Store"}, {Name: "error"}}},
				{Name: "New", QualifiedName: "falkor.New", ReturnTypes: []model.ReturnType{{Name: "*falkor.Store"}, {Name: "error"}}},
			}
		}
		return nil
	}

	infer.InferMultiReturn(env, findByName)

	storeInfo := env.Bindings["openGraphStore:store"]
	if storeInfo.TypeName != "*kuzu.Store" {
		t.Fatalf("store: expected *kuzu.Store, got %q", storeInfo.TypeName)
	}
	if storeInfo.MultiReturnOf != "" {
		t.Fatal("MultiReturnOf should be cleared after resolution")
	}

	errInfo := env.Bindings["openGraphStore:err"]
	if errInfo.TypeName != "error" {
		t.Fatalf("err: expected error, got %q", errInfo.TypeName)
	}

	t.Log("✅ InferMultiReturn: package.Function resolved, MultiReturnOf cleared")
}

func TestInferMultiReturn_ReceiverMethod(t *testing.T) {
	infer := New()

	env := &model.TypeEnv{Bindings: map[string]*model.TypeInfo{
		"run:db": {
			TypeName: "Database",
			Scope:    "run",
		},
		"run:conn": {
			MultiReturnOf: "db.Connect",
			ReturnIndex:   0,
			Scope:         "run",
		},
	}}

	findByName := func(name string) []model.Symbol {
		if name == "Connect" {
			return []model.Symbol{
				{Name: "Connect", QualifiedName: "Database.Connect", ReturnTypes: []model.ReturnType{{Name: "*Connection"}, {Name: "error"}}},
			}
		}
		return nil
	}

	infer.InferMultiReturn(env, findByName)

	connInfo := env.Bindings["run:conn"]
	if connInfo.TypeName != "*Connection" {
		t.Fatalf("conn: expected *Connection, got %q", connInfo.TypeName)
	}

	t.Log("✅ InferMultiReturn: receiver.Method resolved via TypeEnv lookup")
}

func TestResolveFixpoint_MethodCallResult(t *testing.T) {
	infer := New()
	env := &model.TypeEnv{Bindings: map[string]*model.TypeInfo{
		"process:user": {TypeName: "com.example.UserService", Scope: "process"},
	}}
	pendings := []model.PendingAssignment{
		{Kind: "method_call_result", LHS: "addr", Scope: "process", Receiver: "user", Method: "getAddress"},
	}
	findByName := func(name string) []model.Symbol {
		if name == "getAddress" {
			return []model.Symbol{{Name: "getAddress", QualifiedName: "com.example.UserService.getAddress", Kind: "Function", ReturnTypes: []model.ReturnType{{Name: "Address"}}}}
		}
		return nil
	}
	infer.ResolveFixpoint(env, pendings, findByName)
	info := env.Bindings["process:addr"]
	if info == nil || info.TypeName != "Address" {
		t.Fatalf("expected addr=Address, got %v", info)
	}
	t.Log("✅ Fixpoint: method_call_result resolved")
}

func TestResolveFixpoint_Chain(t *testing.T) {
	infer := New()
	env := &model.TypeEnv{Bindings: map[string]*model.TypeInfo{}}
	pendings := []model.PendingAssignment{
		{Kind: "call_result", LHS: "user", Scope: "run", Callee: "getUser"},
		{Kind: "method_call_result", LHS: "addr", Scope: "run", Receiver: "user", Method: "getAddress"},
		{Kind: "copy", LHS: "alias", Scope: "run", RHS: "addr"},
	}
	findByName := func(name string) []model.Symbol {
		switch name {
		case "getUser":
			return []model.Symbol{{Name: "getUser", Kind: "Function", ReturnTypes: []model.ReturnType{{Name: "User"}}}}
		case "getAddress":
			return []model.Symbol{{Name: "getAddress", QualifiedName: "User.getAddress", Kind: "Function", ReturnTypes: []model.ReturnType{{Name: "Address"}}}}
		}
		return nil
	}
	infer.ResolveFixpoint(env, pendings, findByName)
	if env.Bindings["run:user"] == nil || env.Bindings["run:user"].TypeName != "User" {
		t.Fatal("user not resolved")
	}
	if env.Bindings["run:addr"] == nil || env.Bindings["run:addr"].TypeName != "Address" {
		t.Fatal("addr not resolved")
	}
	if env.Bindings["run:alias"] == nil || env.Bindings["run:alias"].TypeName != "Address" {
		t.Fatal("alias not resolved")
	}
	t.Log("✅ Fixpoint: 3-step chain resolved in multiple iterations")
}

func TestResolveFixpoint_ContainerGeneric(t *testing.T) {
	infer := New()
	env := &model.TypeEnv{Bindings: map[string]*model.TypeInfo{
		"process:users": {TypeName: "List", TypeArgs: []model.TypeArg{{Name: "User"}}, Scope: "process"},
	}}
	pendings := []model.PendingAssignment{
		{Kind: "method_call_result", LHS: "first", Scope: "process", Receiver: "users", Method: "get"},
	}
	findByName := func(name string) []model.Symbol { return nil }
	infer.ResolveFixpoint(env, pendings, findByName)
	info := env.Bindings["process:first"]
	if info == nil || info.TypeName != "User" {
		t.Fatalf("expected first=User, got %v", info)
	}
	t.Log("✅ Fixpoint: List<User>.get → User via container descriptor")
}

func TestResolveFixpoint_MapGeneric(t *testing.T) {
	infer := New()
	env := &model.TypeEnv{Bindings: map[string]*model.TypeInfo{
		"run:map": {TypeName: "HashMap", TypeArgs: []model.TypeArg{{Name: "String"}, {Name: "Order"}}, Scope: "run"},
	}}
	pendings := []model.PendingAssignment{
		{Kind: "method_call_result", LHS: "order", Scope: "run", Receiver: "map", Method: "get"},
	}
	findByName := func(name string) []model.Symbol { return nil }
	infer.ResolveFixpoint(env, pendings, findByName)
	info := env.Bindings["run:order"]
	if info == nil || info.TypeName != "Order" {
		t.Fatalf("expected order=Order, got %v", info)
	}
	t.Log("✅ Fixpoint: HashMap<String,Order>.get → Order via container descriptor")
}

func TestResolveFixpoint_UserDefinedGeneric(t *testing.T) {
	infer := New()
	env := &model.TypeEnv{Bindings: map[string]*model.TypeInfo{
		"process:callback": {TypeName: "TKPayResponseWrapper", TypeArgs: []model.TypeArg{{Name: "TKPayPayOutOrderCallback"}}, Scope: "process"},
	}}
	pendings := []model.PendingAssignment{
		{Kind: "method_call_result", LHS: "data", Scope: "process", Receiver: "callback", Method: "getData"},
	}
	findByName := func(name string) []model.Symbol {
		switch name {
		case "getData":
			return []model.Symbol{{Name: "getData", QualifiedName: "TKPayResponseWrapper.getData", Kind: "Function", ReturnTypes: []model.ReturnType{{Name: "T"}}, IsSynthetic: true}}
		case "TKPayResponseWrapper":
			return []model.Symbol{{Name: "TKPayResponseWrapper", Kind: "Class", TypeParams: []string{"T"}}}
		}
		return nil
	}
	infer.ResolveFixpoint(env, pendings, findByName)
	info := env.Bindings["process:data"]
	if info == nil || info.TypeName != "TKPayPayOutOrderCallback" {
		t.Fatalf("expected data=TKPayPayOutOrderCallback, got %v", info)
	}
	t.Log("✅ Fixpoint: TKPayResponseWrapper<TKPayPayOutOrderCallback>.getData → T → TKPayPayOutOrderCallback")
}

func TestLookupInEnv_ScopeParentsMultiLevel(t *testing.T) {
	env := &model.TypeEnv{
		Bindings: map[string]*model.TypeInfo{
			"app.setup:db":              {TypeName: "Database"},
			"app.setup#L3:localVar":     {TypeName: "String"},
			"app.setup#L3#L5:innerVar":  {TypeName: "Number"},
		},
		ScopeParents: map[string]string{
			"app.setup#L3#L5": "app.setup#L3",
			"app.setup#L3":    "app.setup",
		},
	}

	// From innermost block, should find variable in outer function scope
	result := lookupInEnv(env, "app.setup#L3#L5", "db")
	if result != "Database" {
		t.Fatalf("expected Database from outer scope, got %q", result)
	}

	// From innermost block, should find variable in parent block
	result = lookupInEnv(env, "app.setup#L3#L5", "localVar")
	if result != "String" {
		t.Fatalf("expected String from parent block, got %q", result)
	}

	// From innermost block, should find own variable
	result = lookupInEnv(env, "app.setup#L3#L5", "innerVar")
	if result != "Number" {
		t.Fatalf("expected Number from own scope, got %q", result)
	}

	// Variable not found anywhere
	result = lookupInEnv(env, "app.setup#L3#L5", "notExist")
	if result != "" {
		t.Fatalf("expected empty for non-existent var, got %q", result)
	}

	t.Log("✅ lookupInEnv multi-level ScopeParents traversal works")
}

func TestLookupInEnv_ModuleLevelFallback(t *testing.T) {
	env := &model.TypeEnv{
		Bindings: map[string]*model.TypeInfo{
			"globalConfig": {TypeName: "Config"},
		},
		ScopeParents: map[string]string{
			"app.setup#L3": "app.setup",
		},
	}

	// From block scope, should fall through to module-level
	result := lookupInEnv(env, "app.setup#L3", "globalConfig")
	if result != "Config" {
		t.Fatalf("expected Config from module fallback, got %q", result)
	}
	t.Log("✅ lookupInEnv module-level fallback works")
}

func TestLookupInEnv_NoScopeParents_LegacyBehavior(t *testing.T) {
	env := &model.TypeEnv{
		Bindings: map[string]*model.TypeInfo{
			"Controller.handle:dao": {TypeName: "UserDao"},
			"Controller:status":     {TypeName: "String"},
		},
		// No ScopeParents — Java style
	}

	// Direct scope lookup
	result := lookupInEnv(env, "Controller.handle", "dao")
	if result != "UserDao" {
		t.Fatalf("expected UserDao, got %q", result)
	}

	// Class scope fallback (one level only)
	result = lookupInEnv(env, "Controller.handle", "status")
	if result != "String" {
		t.Fatalf("expected String from class scope, got %q", result)
	}

	// Should NOT traverse beyond class scope
	result = lookupInEnv(env, "Controller.handle", "notExist")
	if result != "" {
		t.Fatalf("expected empty, got %q", result)
	}

	t.Log("✅ lookupInEnv legacy behavior (no ScopeParents) preserved")
}

func TestResolveFixpoint_DestructureObject(t *testing.T) {
	infer := New()
	result := &model.ParseResult{
		PendingAssignments: []model.PendingAssignment{
			{Kind: "destructure", LHS: "data", Scope: "app.setup", Callee: "useQuery", DestructuredKey: "data"},
			{Kind: "destructure", LHS: "error", Scope: "app.setup", Callee: "useQuery", DestructuredKey: "error"},
		},
	}

	env := infer.InferLocal(result)

	// Simulate findByName returning useQuery with return type
	findByName := func(name string) []model.Symbol {
		if name == "useQuery" {
			return []model.Symbol{{Kind: "Function", ReturnTypes: []model.ReturnType{{Name: "QueryResult"}}}}
		}
		return nil
	}

	// Simulate findFieldByOwner
	findFieldByOwner := func(ownerType, fieldName string) *model.FieldInfo {
		if ownerType == "QueryResult" {
			switch fieldName {
			case "data":
				return &model.FieldInfo{Type: "User[]"}
			case "error":
				return &model.FieldInfo{Type: "Error"}
			}
		}
		return nil
	}

	infer.ResolveFixpoint(env, result.PendingAssignments, findByName, findFieldByOwner)

	dataInfo := env.Bindings["app.setup:data"]
	if dataInfo == nil || dataInfo.TypeName != "User[]" {
		t.Fatalf("expected data→User[], got %v", dataInfo)
	}
	errorInfo := env.Bindings["app.setup:error"]
	if errorInfo == nil || errorInfo.TypeName != "Error" {
		t.Fatalf("expected error→Error, got %v", errorInfo)
	}
	t.Log("✅ Fixpoint destructure object: data=User[], error=Error")
}

func TestResolveFixpoint_DestructureArray(t *testing.T) {
	infer := New()
	result := &model.ParseResult{
		PendingAssignments: []model.PendingAssignment{
			{Kind: "destructure", LHS: "count", Scope: "app.setup", Callee: "useState", DestructuredKey: "0"},
			{Kind: "destructure", LHS: "setCount", Scope: "app.setup", Callee: "useState", DestructuredKey: "1"},
		},
	}

	env := infer.InferLocal(result)

	findByName := func(name string) []model.Symbol {
		if name == "useState" {
			return []model.Symbol{{Kind: "Function", ReturnTypes: []model.ReturnType{{Name: "number"}, {Name: "Function"}}}}
		}
		return nil
	}

	infer.ResolveFixpoint(env, result.PendingAssignments, findByName)

	countInfo := env.Bindings["app.setup:count"]
	if countInfo == nil || countInfo.TypeName != "number" {
		t.Fatalf("expected count→number, got %v", countInfo)
	}
	setCountInfo := env.Bindings["app.setup:setCount"]
	if setCountInfo == nil || setCountInfo.TypeName != "Function" {
		t.Fatalf("expected setCount→Function, got %v", setCountInfo)
	}
	t.Log("✅ Fixpoint destructure array: count=number, setCount=Function")
}

func TestInferLocal_ScopeParentsPassed(t *testing.T) {
	infer := New()
	result := &model.ParseResult{
		ScopeParents: map[string]string{
			"app.setup#L3": "app.setup",
			"app.setup.inner": "app.setup",
		},
	}

	env := infer.InferLocal(result)

	if env.ScopeParents == nil {
		t.Fatal("ScopeParents should be passed to TypeEnv")
	}
	if env.ScopeParents["app.setup#L3"] != "app.setup" {
		t.Fatalf("expected parent app.setup, got %q", env.ScopeParents["app.setup#L3"])
	}
	if env.ScopeParents["app.setup.inner"] != "app.setup" {
		t.Fatalf("expected parent app.setup, got %q", env.ScopeParents["app.setup.inner"])
	}
	t.Log("✅ InferLocal passes ScopeParents to TypeEnv")
}

func TestSubstituteTypeParam_MethodLevelGeneric(t *testing.T) {
	// Setup: method getBean<T>(Class<T> clazz): T
	findByName := func(name string) []model.Symbol {
		switch name {
		case "getBean":
			return []model.Symbol{{
				Name:        "getBean",
				Kind:        "Function",
				TypeParams:  []string{"T"},
				ReturnTypes: []model.ReturnType{{Name: "T"}},
				Params:      []model.ParamInfo{{Name: "clazz", Type: "Class", TypeArgs: []model.TypeArg{{Name: "T"}}}},
			}}
		case "identity":
			return []model.Symbol{{
				Name:        "identity",
				Kind:        "Function",
				TypeParams:  []string{"T"},
				ReturnTypes: []model.ReturnType{{Name: "T"}},
				Params:      []model.ParamInfo{{Name: "value", Type: "T"}},
			}}
		case "transform":
			return []model.Symbol{{
				Name:        "transform",
				Kind:        "Function",
				TypeParams:  []string{"K", "V"},
				ReturnTypes: []model.ReturnType{{Name: "V"}},
				Params:      []model.ParamInfo{{Name: "key", Type: "K"}, {Name: "val", Type: "V"}},
			}}
		case "noGeneric":
			return []model.Symbol{{
				Name:        "noGeneric",
				Kind:        "Function",
				ReturnTypes: []model.ReturnType{{Name: "String"}},
				Params:      []model.ParamInfo{{Name: "input", Type: "String"}},
			}}
		case "Container":
			return []model.Symbol{{
				Name:       "Container",
				Kind:       "Class",
				TypeParams: []string{"T"},
			}}
		}
		return nil
	}

	tests := []struct {
		name       string
		methodName string
		argTypes   []string
		retType    string
		expected   string
	}{
		{"U-1: Class<T> infer", "getBean", []string{"User"}, "T", "User"},
		{"U-2: direct generic param", "identity", []string{"User"}, "T", "User"},
		{"U-3: multi-generic first", "transform", []string{"String", "User"}, "K", "String"},
		{"U-4: multi-generic second", "transform", []string{"String", "User"}, "V", "User"},
		{"U-5: retType not in TypeParams", "identity", []string{"User"}, "String", "String"},
		{"U-6: argTypes empty", "identity", []string{}, "T", "T"},
		{"U-7: no TypeParams", "noGeneric", []string{"hello"}, "String", "String"},
		{"U-8: method not found", "unknownMethod", []string{"User"}, "T", "T"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := &model.TypeEnv{Bindings: map[string]*model.TypeInfo{}}
			result := substituteTypeParam(tt.retType, "Container", "container", env, "run", findByName, tt.methodName, tt.argTypes)
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestSubstituteTypeParam_ClassLevelTakesPriority(t *testing.T) {
	// U-9: receiver class has TypeParams + TypeEnv has TypeArgs → path 1 wins over path 3
	findByName := func(name string) []model.Symbol {
		switch name {
		case "Container":
			return []model.Symbol{{
				Name:       "Container",
				Kind:       "Class",
				TypeParams: []string{"T"},
			}}
		case "get":
			return []model.Symbol{{
				Name:        "get",
				Kind:        "Function",
				TypeParams:  []string{"T"},
				ReturnTypes: []model.ReturnType{{Name: "T"}},
				Params:      []model.ParamInfo{{Name: "index", Type: "int"}},
			}}
		}
		return nil
	}

	env := &model.TypeEnv{
		Bindings: map[string]*model.TypeInfo{
			"run:container": {TypeName: "Container", TypeArgs: []model.TypeArg{{Name: "User"}}},
		},
	}

	// Path 1 should resolve T → User via class-level TypeParams
	result := substituteTypeParam("T", "Container", "container", env, "run", findByName, "get", []string{})
	if result != "User" {
		t.Errorf("U-9: expected User (path 1 priority), got %q", result)
	}
}
