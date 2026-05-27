package resolver

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestInferLambdaParamTypes_ChainedReceiver(t *testing.T) {
	// Scenario: repo.findAll().stream().map(user -> user.getName())
	// LambdaOwnerReceiver = "repo.findAll().stream()"
	// TypeEnv only has repo=Repository (no TypeArgs on repo itself)
	// Must resolve chain: repo→Repository→findAll()→List<User>→stream()→Stream<User>
	// Then infer lambda param 'user' = User from Stream's TypeArgs

	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "repo_class", Name: "Repository", QualifiedName: "com.example.Repository", Kind: "Class", FilePath: "Repository.java"},
		{ID: "find_all", Name: "findAll", QualifiedName: "com.example.Repository.findAll", Kind: "Function", FilePath: "Repository.java",
			ReturnTypes: []model.ReturnType{{Name: "List", Args: []model.TypeArg{{Name: "User"}}}}},
		{ID: "user_class", Name: "User", QualifiedName: "com.example.User", Kind: "Class", FilePath: "User.java"},
		{ID: "get_name", Name: "getName", QualifiedName: "com.example.User.getName", Kind: "Function", FilePath: "User.java",
			ReturnTypes: []model.ReturnType{{Name: "String"}}},
		{ID: "consumer_class", Name: "Consumer", QualifiedName: "com.example.Consumer", Kind: "Class", FilePath: "Consumer.java"},
		{ID: "test_method", Name: "lambdaStreamTest", QualifiedName: "com.example.Consumer.lambdaStreamTest", Kind: "Function", FilePath: "Consumer.java"},
		{ID: "lambda1", Name: "lambda$1", QualifiedName: "com.example.Consumer.lambdaStreamTest.lambda$1", Kind: "Function", FilePath: "Consumer.java",
			IsLambda: true, Params: []model.ParamInfo{{Name: "user", Type: ""}}},
	})

	helper := &testListJDKHelper{testJavaHelper: testJavaHelper{symbolTable: table}}
	resolver := NewResolver(table, map[string]LanguageHelper{"java": helper})

	calls := []model.RawCall{
		{CalledName: "com.example.Consumer.lambdaStreamTest.lambda$1",
			CallerName: "com.example.Consumer.lambdaStreamTest", CallerScope: "com.example.Consumer.lambdaStreamTest",
			FilePath: "Consumer.java", Language: "java", IsPreResolved: true,
			LambdaOwnerMethod: "map", LambdaOwnerReceiver: "repo.findAll().stream()"},
		{CalledName: "getName", ReceiverExpr: "user",
			CallerName: "com.example.Consumer.lambdaStreamTest.lambda$1", CallerScope: "com.example.Consumer.lambdaStreamTest.lambda$1",
			FilePath: "Consumer.java", Language: "java"},
	}

	envs := map[string]*model.TypeEnv{
		"Consumer.java": {Bindings: map[string]*model.TypeInfo{
			"com.example.Consumer.lambdaStreamTest:repo": {TypeName: "Repository", Tier: 1},
		}},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)

	var foundGetName bool
	for _, relation := range relations {
		if relation.TargetID == "get_name" {
			foundGetName = true
			break
		}
	}
	if !foundGetName {
		t.Fatal("lambda param 'user' not inferred from chained receiver — user.getName() not resolved to User.getName")
	}
	t.Log("✅ inferLambdaParamTypes resolves chained LambdaOwnerReceiver via resolveChainedReceiver")
}

func TestInferLambdaParamTypes_SimpleVariableWithChainedMethod(t *testing.T) {
	// Scenario: orders.stream().map(order -> order.getName()) inside an if block
	// LambdaOwnerReceiver = "orders.stream()"
	// TypeEnv has orders=List with TypeArgs=[Order] at METHOD scope
	// Lambda CallerScope is at BLOCK scope (if block)
	// This is the java_block_scope scenario that must NOT regress

	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "order_class", Name: "Order", QualifiedName: "com.example.Order", Kind: "Class", FilePath: "Order.java"},
		{ID: "get_name", Name: "getName", QualifiedName: "com.example.Order.getName", Kind: "Function", FilePath: "Order.java",
			ReturnTypes: []model.ReturnType{{Name: "String"}}},
		{ID: "svc_class", Name: "Svc", QualifiedName: "com.example.Svc", Kind: "Class", FilePath: "Svc.java"},
		{ID: "process", Name: "lambdaInIfBlock", QualifiedName: "com.example.Svc.lambdaInIfBlock", Kind: "Function", FilePath: "Svc.java"},
		{ID: "lambda1", Name: "lambda$1", QualifiedName: "com.example.Svc.lambdaInIfBlock.lambda$1", Kind: "Function", FilePath: "Svc.java",
			IsLambda: true, Params: []model.ParamInfo{{Name: "order", Type: ""}}},
	})

	helper := &testListJDKHelper{testJavaHelper: testJavaHelper{symbolTable: table}}
	resolver := NewResolver(table, map[string]LanguageHelper{"java": helper})

	calls := []model.RawCall{
		{CalledName: "com.example.Svc.lambdaInIfBlock.lambda$1",
			CallerName:  "com.example.Svc.lambdaInIfBlock",
			CallerScope: "com.example.Svc.lambdaInIfBlock#if_2", // block scope!
			FilePath:    "Svc.java", Language: "java", IsPreResolved: true,
			LambdaOwnerMethod: "map", LambdaOwnerReceiver: "orders.stream()"},
		{CalledName: "getName", ReceiverExpr: "order",
			CallerName:  "com.example.Svc.lambdaInIfBlock.lambda$1",
			CallerScope: "com.example.Svc.lambdaInIfBlock.lambda$1",
			FilePath:    "Svc.java", Language: "java"},
	}

	envs := map[string]*model.TypeEnv{
		"Svc.java": {
			Bindings: map[string]*model.TypeInfo{
				"com.example.Svc.lambdaInIfBlock:orders": {TypeName: "List", TypeArgs: []model.TypeArg{{Name: "Order"}}, Scope: "com.example.Svc.lambdaInIfBlock"},
			},
			ScopeParents: map[string]string{
				"com.example.Svc.lambdaInIfBlock#if_2": "com.example.Svc.lambdaInIfBlock",
			},
		},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)

	var foundGetName bool
	for _, relation := range relations {
		if relation.TargetID == "get_name" {
			foundGetName = true
			break
		}
	}
	if !foundGetName {
		t.Fatal("REGRESSION: lambda param 'order' not inferred from simple variable with chained method in if block")
	}
	t.Log("✅ inferLambdaParamTypes works for simple variable with chained method in if block (no regression)")
}

func TestLookupFieldInHierarchy_LambdaCapturingOuterField(t *testing.T) {
	// Scenario: repo.findById(1L).flatMap(user -> repo.findAddress(user))
	// Lambda body calls repo.findAddress(user) — "repo" is a field of the outer class Consumer.
	// CallerName inside lambda = "com.example.Consumer.optionalFlatMapTest.lambda$1"
	// lookupFieldInHierarchy must walk up from lambda → method → class to find "repo" field.

	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "repo_class", Name: "Repository", QualifiedName: "com.example.Repository", Kind: "Class", FilePath: "Repository.java"},
		{ID: "find_address", Name: "findAddress", QualifiedName: "com.example.Repository.findAddress", Kind: "Function", FilePath: "Repository.java",
			ReturnTypes: []model.ReturnType{{Name: "Optional", Args: []model.TypeArg{{Name: "String"}}}}},
		{ID: "consumer_class", Name: "Consumer", QualifiedName: "com.example.Consumer", Kind: "Class", FilePath: "Consumer.java"},
		{ID: "test_method", Name: "optionalFlatMapTest", QualifiedName: "com.example.Consumer.optionalFlatMapTest", Kind: "Function", FilePath: "Consumer.java"},
		{ID: "lambda1", Name: "lambda$1", QualifiedName: "com.example.Consumer.optionalFlatMapTest.lambda$1", Kind: "Function", FilePath: "Consumer.java",
			IsLambda: true, Params: []model.ParamInfo{{Name: "user", Type: "User"}}},
	})

	helper := &testListJDKHelper{testJavaHelper: testJavaHelper{symbolTable: table}}
	resolver := NewResolver(table, map[string]LanguageHelper{"java": helper})

	calls := []model.RawCall{
		// Lambda body: repo.findAddress(user)
		{CalledName: "findAddress", ReceiverExpr: "repo",
			CallerName:  "com.example.Consumer.optionalFlatMapTest.lambda$1",
			CallerScope: "com.example.Consumer.optionalFlatMapTest.lambda$1",
			FilePath:    "Consumer.java", Language: "java", ArgCount: 1},
	}

	envs := map[string]*model.TypeEnv{
		"Consumer.java": {Bindings: map[string]*model.TypeInfo{
			// Field "repo" belongs to class Consumer
			"com.example.Consumer:repo": {TypeName: "Repository", Tier: 1},
		}},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)

	var foundFindAddress bool
	for _, relation := range relations {
		if relation.TargetID == "find_address" {
			foundFindAddress = true
			break
		}
	}
	if !foundFindAddress {
		t.Fatal("lambda body calling outer class field method: repo.findAddress() not resolved — lookupFieldInHierarchy fails to walk up from lambda to class")
	}
	t.Log("✅ lookupFieldInHierarchy correctly walks up from lambda CallerName to find outer class field type")
}
