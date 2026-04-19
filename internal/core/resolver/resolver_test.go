package resolver

import (
	"strings"
	"testing"

	"github.com/liuymcn/flash-code-graph/internal/model"
)

func newTestResolver(table *SymbolTable) *Resolver {
	javaHelper := &testJavaHelper{symbolTable: table}
	goHelper := &testGenericHelper{}
	return NewResolver(table, map[string]LanguageHelper{
		"java":       javaHelper,
		"go":         goHelper,
		"python":     goHelper,
		"typescript": goHelper,
		"javascript": goHelper,
	})
}

// testJavaHelper is a minimal Java helper for unit tests.
// testGenericHelper is a passthrough helper for non-Java languages in tests.
type testGenericHelper struct{}

func (testGenericHelper) SetHeritage(_ []model.RawHeritage)                          {}
func (testGenericHelper) ShouldFallthrough() bool                                    { return true }
func (testGenericHelper) InferStringConcat(_ string) bool                            { return false }
func (testGenericHelper) LookupMethodReturn(_, _ string) (string, bool)              { return "", false }
func (testGenericHelper) IsTypeAssignable(a, b string) bool                          { return a == b }
func (testGenericHelper) ResolveOverload(_ []model.Symbol, _ []string) *model.Symbol { return nil }
func (testGenericHelper) FilterGenerated(c []model.Symbol) []model.Symbol            { return c }
func (testGenericHelper) IsConstructor(_ model.Symbol, _ string) bool                { return false }
func (testGenericHelper) IsOverrideMatch(child, parent model.Symbol) bool {
	return child.Name == parent.Name
}
func (testGenericHelper) InferImplements() []model.ResolvedRelation { return nil }
func (testGenericHelper) ResolveSuperCall(_ model.RawCall, _ []model.Symbol, _ []model.RawHeritage, _ map[string]*model.TypeEnv, _ string) ([]model.ResolvedRelation, bool) {
	return nil, false
}
func (testGenericHelper) NarrowByScope(m []model.Symbol, _ model.RawCall, _ *model.TypeEnv, _ *SymbolTable) []model.Symbol {
	return m
}
func (testGenericHelper) ResolveReceiverFallback(_ model.RawCall, _ []model.Symbol, _ map[string]*model.TypeEnv, _ string, _ *SymbolTable) ([]model.ResolvedRelation, bool) {
	return nil, false
}
func (testGenericHelper) ResolveImplicitSelfCall(_ model.RawCall, _ []model.Symbol, _ map[string]*model.TypeEnv, _ string, _ *SymbolTable) ([]model.ResolvedRelation, bool) {
	return nil, false
}

// It reuses the exported functions that java/ subpackage also uses.
type testJavaHelper struct {
	symbolTable *SymbolTable
	heritage    []model.RawHeritage
}

func (h *testJavaHelper) SetHeritage(heritage []model.RawHeritage) { h.heritage = heritage }
func (h *testJavaHelper) ShouldFallthrough() bool                  { return false }
func (h *testJavaHelper) InferStringConcat(expr string) bool       { return false }
func (h *testJavaHelper) LookupMethodReturn(_, _ string) (string, bool) {
	return "", false
}

func (h *testJavaHelper) ResolveSuperCall(call model.RawCall, funcCandidates []model.Symbol, heritage []model.RawHeritage, envs map[string]*model.TypeEnv, callerID string) ([]model.ResolvedRelation, bool) {
	if call.ReceiverExpr != "super" || len(heritage) == 0 {
		return nil, false
	}
	callerClass := call.CallerName
	if dotIdx := strings.LastIndex(callerClass, "."); dotIdx >= 0 {
		callerClass = callerClass[:dotIdx]
		if dotIdx2 := strings.LastIndex(callerClass, "."); dotIdx2 >= 0 {
			callerClass = callerClass[dotIdx2+1:]
		}
	}
	for _, her := range heritage {
		if her.ChildName == callerClass && her.Kind == "extends" && her.FilePath == call.FilePath {
			parentName := her.ParentName
			// Resolve parent QN via import/same-package
			var resolvedParentQN string
			env := envs[call.FilePath]
			callerPkg := ""
			for _, cs := range h.symbolTable.FindByFile(call.FilePath) {
				if cs.Kind == "class" || cs.Kind == "interface" || cs.Kind == "abstract_class" {
					if idx := strings.LastIndex(cs.QualifiedName, "."+cs.Name); idx > 0 {
						callerPkg = cs.QualifiedName[:idx]
						break
					}
				}
			}
			for _, sym := range h.symbolTable.FindByName(parentName) {
				if sym.Kind != "class" && sym.Kind != "abstract_class" {
					continue
				}
				symPkg := ""
				if idx := strings.LastIndex(sym.QualifiedName, "."+sym.Name); idx > 0 {
					symPkg = sym.QualifiedName[:idx]
				}
				if callerPkg != "" && symPkg == callerPkg {
					resolvedParentQN = sym.QualifiedName
					break
				}
				if env != nil {
					for _, imp := range env.Imports {
						if imp.SymbolName == parentName || strings.HasPrefix(sym.QualifiedName, imp.ModulePath+".") {
							resolvedParentQN = sym.QualifiedName
							break
						}
					}
				}
				if resolvedParentQN != "" {
					break
				}
			}
			var matched []model.Symbol
			if resolvedParentQN != "" {
				prefix := resolvedParentQN + "."
				for _, c := range funcCandidates {
					if strings.HasPrefix(c.QualifiedName, prefix) {
						matched = append(matched, c)
					}
				}
			} else {
				matched = filterByOwnerClass(funcCandidates, parentName)
			}
			if len(matched) == 1 {
				return []model.ResolvedRelation{makeRelation(callerID, matched[0].ID, call, ConfidenceTypeExact, "type_exact", 1)}, true
			}
			if len(matched) > 1 {
				argMatched := filterByArgCount(matched, call.ArgCount)
				if len(argMatched) == 1 {
					return []model.ResolvedRelation{makeRelation(callerID, argMatched[0].ID, call, ConfidenceArgCount, "arg_count", 1)}, true
				}
				return makeMultiRelations(callerID, matched, call, ConfidenceTypeParent, "type_multi"), true
			}
			r := &Resolver{symbolTable: h.symbolTable, heritage: heritage}
			if sym := r.FindMethodInHierarchy(her.ParentName, call.CalledName, heritage); sym != nil {
				return []model.ResolvedRelation{makeRelation(callerID, sym.ID, call, ConfidenceTypeExact, "type_hierarchy", 1)}, true
			}
			break
		}
	}
	return nil, false
}

func (h *testJavaHelper) NarrowByScope(matched []model.Symbol, call model.RawCall, env *model.TypeEnv, symbolTable *SymbolTable) []model.Symbol {
	if env == nil {
		return matched
	}
	callerPkg := ""
	for _, cs := range symbolTable.FindByFile(call.FilePath) {
		if cs.Kind != "class" && cs.Kind != "interface" && cs.Kind != "abstract_class" {
			continue
		}
		if idx := strings.LastIndex(cs.QualifiedName, "."+cs.Name); idx > 0 {
			callerPkg = cs.QualifiedName[:idx]
			break
		}
	}
	var narrowed []model.Symbol
	for _, m := range matched {
		symPkg := ""
		if idx := strings.LastIndex(m.QualifiedName, "."+m.Name); idx > 0 {
			parts := m.QualifiedName[:idx]
			if idx2 := strings.LastIndex(parts, "."); idx2 > 0 {
				symPkg = parts[:idx2]
			}
		}
		isSamePackage := callerPkg != "" && symPkg == callerPkg
		isImported := false
		for _, imp := range env.Imports {
			if strings.HasPrefix(m.QualifiedName, imp.ModulePath+".") {
				rest := m.QualifiedName[len(imp.ModulePath)+1:]
				if strings.Count(rest, ".") <= 1 {
					isImported = true
					break
				}
			}
		}
		if isSamePackage || isImported {
			narrowed = append(narrowed, m)
		}
	}
	if len(narrowed) > 0 {
		return narrowed
	}
	return matched
}

func (h *testJavaHelper) ResolveReceiverFallback(_ model.RawCall, _ []model.Symbol, _ map[string]*model.TypeEnv, _ string, _ *SymbolTable) ([]model.ResolvedRelation, bool) {
	return nil, false
}
func (h *testJavaHelper) ResolveOverload(_ []model.Symbol, _ []string) *model.Symbol {
	return nil
}
func (h *testJavaHelper) IsTypeAssignable(argType, paramType string) bool {
	return argType == paramType
}
func (h *testJavaHelper) FilterGenerated(candidates []model.Symbol) []model.Symbol {
	var result []model.Symbol
	for _, c := range candidates {
		if !c.IsSynthetic {
			result = append(result, c)
		}
	}
	return result
}
func (h *testJavaHelper) ResolveImplicitSelfCall(_ model.RawCall, _ []model.Symbol, _ map[string]*model.TypeEnv, _ string, _ *SymbolTable) ([]model.ResolvedRelation, bool) {
	return nil, false
}
func (h *testJavaHelper) IsConstructor(method model.Symbol, className string) bool {
	return method.IsConstructor || method.Name == className
}
func (h *testJavaHelper) IsOverrideMatch(child, parent model.Symbol) bool {
	return child.Name == parent.Name
}
func (h *testJavaHelper) InferImplements() []model.ResolvedRelation { return nil }

func buildTestTable() *SymbolTable {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "f1", Name: "findById", QualifiedName: "com.example.UserService.findById", Kind: "function", FilePath: "service.go", Params: `[{"name":"id","type":"int"}]`},
		{ID: "f2", Name: "findById", QualifiedName: "com.example.OrderService.findById", Kind: "function", FilePath: "order.go", Params: `[{"name":"id","type":"int"}]`},
		{ID: "f3", Name: "findById", QualifiedName: "com.example.AdminService.findById", Kind: "function", FilePath: "admin.go", Params: `[{"name":"id","type":"int"},{"name":"role","type":"string"}]`},
		{ID: "f4", Name: "helper", QualifiedName: "com.example.utils.helper", Kind: "function", FilePath: "utils.go", Params: `[{"name":"x"}]`},
		{ID: "c1", Name: "UserService", QualifiedName: "com.example.UserService", Kind: "class", FilePath: "service.go"},
		{ID: "c2", Name: "OrderService", QualifiedName: "com.example.OrderService", Kind: "class", FilePath: "order.go"},
	})
	return table
}

func TestSymbolTable_FindByName(t *testing.T) {
	table := buildTestTable()

	results := table.FindByName("findById")
	if len(results) != 3 {
		t.Fatalf("expected 3 findById, got %d", len(results))
	}

	results = table.FindByName("helper")
	if len(results) != 1 {
		t.Fatalf("expected 1 helper, got %d", len(results))
	}

	results = table.FindByName("nonexistent")
	if len(results) != 0 {
		t.Fatalf("expected 0, got %d", len(results))
	}
	t.Log("✅ SymbolTable FindByName works")
}

func TestSymbolTable_FindByQualifiedName(t *testing.T) {
	table := buildTestTable()

	results := table.FindByQualifiedName("com.example.UserService.findById")
	if len(results) != 1 {
		t.Fatalf("expected 1, got %d", len(results))
	}
	if results[0].ID != "f1" {
		t.Fatalf("expected f1, got %s", results[0].ID)
	}
	t.Log("✅ SymbolTable FindByQualifiedName works")
}

func TestResolveCalls_TypeExact(t *testing.T) {
	table := buildTestTable()
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "findById", CallerName: "pkg.main", FilePath: "main.go", ReceiverExpr: "userService", ArgCount: 1, Line: 10},
	}
	envs := map[string]*model.TypeEnv{
		"main.go": {Bindings: map[string]*model.TypeInfo{
			"pkg.main:userService": {TypeName: "UserService", Tier: 0},
		}},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "f1" {
		t.Fatalf("expected target f1, got %s", relations[0].TargetID)
	}
	if relations[0].Confidence != ConfidenceTypeExact {
		t.Fatalf("expected confidence %f, got %f", ConfidenceTypeExact, relations[0].Confidence)
	}
	if relations[0].ResolvedBy != "type_exact" {
		t.Fatalf("expected type_exact, got %s", relations[0].ResolvedBy)
	}
	t.Log("✅ ResolveCalls type_exact works")
}

func TestResolveCalls_ArgCount(t *testing.T) {
	table := buildTestTable()
	resolver := newTestResolver(table)

	// findById with 2 args → should match AdminService.findById (2 params)
	calls := []model.RawCall{
		{CalledName: "findById", CallerName: "main", FilePath: "main.go", ArgCount: 2, Line: 10},
	}
	envs := map[string]*model.TypeEnv{}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation (arg_count match), got %d", len(relations))
	}
	if relations[0].TargetID != "f3" {
		t.Fatalf("expected target f3 (AdminService), got %s", relations[0].TargetID)
	}
	if relations[0].ResolvedBy != "arg_count" {
		t.Fatalf("expected arg_count, got %s", relations[0].ResolvedBy)
	}
	t.Log("✅ ResolveCalls arg_count works")
}

func TestResolveCalls_NameUnique(t *testing.T) {
	table := buildTestTable()
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "helper", CallerName: "main", FilePath: "main.go", Line: 5},
	}
	envs := map[string]*model.TypeEnv{}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].Confidence != ConfidenceNameUnique {
		t.Fatalf("expected confidence %f, got %f", ConfidenceNameUnique, relations[0].Confidence)
	}
	if relations[0].ResolvedBy != "name_unique" {
		t.Fatalf("expected name_unique, got %s", relations[0].ResolvedBy)
	}
	t.Log("✅ ResolveCalls name_unique works")
}

func TestResolveCalls_UnresolvedHint(t *testing.T) {
	table := buildTestTable()
	resolver := newTestResolver(table)

	// findById with 1 arg, no receiver → 2 candidates with 1 param each → hint
	calls := []model.RawCall{
		{CalledName: "findById", CallerName: "main", FilePath: "main.go", ArgCount: 1, Line: 10},
	}
	envs := map[string]*model.TypeEnv{}

	relations, hints := resolver.ResolveCalls(calls, envs)
	if len(relations) != 0 {
		t.Fatalf("expected 0 relations (no best_guess), got %d", len(relations))
	}
	if len(hints) != 1 {
		t.Fatalf("expected 1 hint, got %d", len(hints))
	}
	if hints[0].HintType != "ambiguous_project_call" {
		t.Fatalf("expected hint_type ambiguous_project_call, got %s", hints[0].HintType)
	}
	if hints[0].Method != "findById" {
		t.Fatalf("expected method findById, got %s", hints[0].Method)
	}
	if hints[0].CandidateCount < 2 {
		t.Fatalf("expected candidate_count >= 2, got %d", hints[0].CandidateCount)
	}
	t.Logf("✅ Unresolved hint: %s with %d candidates", hints[0].HintType, hints[0].CandidateCount)
}

func TestResolveHeritage(t *testing.T) {
	table := buildTestTable()
	resolver := newTestResolver(table)

	heritage := []model.RawHeritage{
		{ChildName: "UserService", ParentName: "OrderService", Kind: "extends", FilePath: "service.go"},
	}

	relations := resolver.ResolveHeritage(heritage)
	if len(relations) != 1 {
		t.Fatalf("expected 1 heritage relation, got %d", len(relations))
	}
	if relations[0].Kind != model.RelExtends {
		t.Fatalf("expected EXTENDS, got %s", relations[0].Kind)
	}
	t.Log("✅ ResolveHeritage works")
}

func TestResolveCalls_SelfReceiver(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "m1", Name: "save", QualifiedName: "UserService.save", Kind: "function", FilePath: "service.py"},
		{ID: "m2", Name: "findById", QualifiedName: "UserService.findById", Kind: "function", FilePath: "service.py"},
	})
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "save", CallerName: "UserService.findById", FilePath: "service.py", ReceiverExpr: "self", Line: 10},
	}
	envs := map[string]*model.TypeEnv{
		"service.py": {Bindings: map[string]*model.TypeInfo{
			"UserService.findById:self": {TypeName: "UserService", Tier: 0},
		}},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "m1" {
		t.Fatalf("expected m1, got %s", relations[0].TargetID)
	}
	if relations[0].ResolvedBy != "type_exact" {
		t.Fatalf("expected type_exact, got %s", relations[0].ResolvedBy)
	}
	t.Log("✅ ResolveCalls self receiver works")
}

func TestResolveImports_PythonFromImport(t *testing.T) {
	table := NewSymbolTable()
	resolver := newTestResolver(table)

	imports := []model.RawImport{
		{ModulePath: "pkg.util", SymbolName: "helper", FilePath: "app.py", Line: 1},
	}
	allFiles := []string{"app.py", "pkg/util.py"}

	relations := resolver.ResolveImports(imports, allFiles)
	if len(relations) != 1 {
		t.Fatalf("expected 1 IMPORTS relation, got %d", len(relations))
	}
	if relations[0].SourceID != "file:app.py" {
		t.Fatalf("SourceID expected file:app.py, got %s", relations[0].SourceID)
	}
	if relations[0].TargetID != "file:pkg/util.py" {
		t.Fatalf("TargetID expected file:pkg/util.py, got %s", relations[0].TargetID)
	}
	t.Log("✅ Python from-import resolved")
}

func TestResolveImports_JavaImport(t *testing.T) {
	table := NewSymbolTable()
	resolver := newTestResolver(table)

	imports := []model.RawImport{
		{ModulePath: "com.example.UserService", SymbolName: "UserService", FilePath: "src/main/java/com/example/App.java", Line: 3},
	}
	allFiles := []string{
		"src/main/java/com/example/App.java",
		"src/main/java/com/example/UserService.java",
	}

	relations := resolver.ResolveImports(imports, allFiles)
	if len(relations) != 1 {
		t.Fatalf("expected 1 IMPORTS relation, got %d", len(relations))
	}
	if relations[0].TargetID != "file:src/main/java/com/example/UserService.java" {
		t.Fatalf("TargetID expected UserService.java, got %s", relations[0].TargetID)
	}
	t.Log("✅ Java import resolved")
}

func TestResolveImports_GoImport(t *testing.T) {
	table := NewSymbolTable()
	resolver := newTestResolver(table)

	imports := []model.RawImport{
		{ModulePath: "myapp/pkg/service", SymbolName: "", FilePath: "main.go", Line: 3},
	}
	allFiles := []string{"main.go", "pkg/service/service.go"}

	relations := resolver.ResolveImports(imports, allFiles)
	if len(relations) != 1 {
		t.Fatalf("expected 1 IMPORTS relation, got %d", len(relations))
	}
	if relations[0].TargetID != "file:pkg/service/service.go" {
		t.Fatalf("TargetID expected service.go, got %s", relations[0].TargetID)
	}
	t.Log("✅ Go import resolved")
}

func TestResolveImports_NoMatch(t *testing.T) {
	table := NewSymbolTable()
	resolver := newTestResolver(table)

	imports := []model.RawImport{
		{ModulePath: "external.lib", SymbolName: "Foo", FilePath: "app.py", Line: 1},
	}
	allFiles := []string{"app.py", "util.py"}

	relations := resolver.ResolveImports(imports, allFiles)
	if len(relations) != 0 {
		t.Fatalf("expected 0 relations for external import, got %d", len(relations))
	}
	t.Log("✅ External import produces no relation")
}

func TestResolveImports_FileIndexStripsExtension(t *testing.T) {
	table := NewSymbolTable()
	resolver := newTestResolver(table)

	// Regression: fileIndex must strip extension BEFORE lastSegment,
	// otherwise "util.py" splits to ["util","py"] and key becomes "py"
	imports := []model.RawImport{
		{ModulePath: "util", SymbolName: "helper", FilePath: "app.py", Line: 1},
	}
	allFiles := []string{"app.py", "util.py"}

	relations := resolver.ResolveImports(imports, allFiles)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation (fileIndex extension bug), got %d", len(relations))
	}
	t.Log("✅ fileIndex correctly strips extension before lastSegment")
}

func TestCountParams_NoFalseCount(t *testing.T) {
	// Regression: countParams must not count "name" appearing in param values
	paramsWithNameInValue := `[{"name":"query","type":"string"},{"name":"nameFilter","type":"string"}]`
	count := countParams(paramsWithNameInValue)
	if count != 2 {
		t.Fatalf("expected 2 params, got %d", count)
	}

	if countParams("") != 0 {
		t.Fatal("expected 0 for empty")
	}
	if countParams("null") != 0 {
		t.Fatal("expected 0 for null")
	}
	if countParams("invalid json") != 0 {
		t.Fatal("expected 0 for invalid json")
	}

	t.Log("✅ countParams uses JSON deserialization, no false counts")
}

func TestResolveCalls_TypeParent(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "caller", Name: "process", QualifiedName: "main.process", Kind: "function", FilePath: "main.go"},
		// Two "run" methods in different classes
		{ID: "dog_run", Name: "run", QualifiedName: "Dog.run", Kind: "function", FilePath: "dog.go", ClassType: "Dog"},
		{ID: "cat_run", Name: "run", QualifiedName: "Cat.run", Kind: "function", FilePath: "cat.go", ClassType: "Cat"},
	})
	resolver := newTestResolver(table)

	// Receiver type matches Dog → should resolve to dog_run with type_exact
	envs := map[string]*model.TypeEnv{
		"main.go": {Bindings: map[string]*model.TypeInfo{"main.process:animal": {TypeName: "Dog"}}},
	}
	calls := []model.RawCall{
		{CallerName: "main.process", CalledName: "run", ReceiverExpr: "animal", FilePath: "main.go", ArgCount: 0},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "dog_run" {
		t.Fatalf("expected dog_run, got %s", relations[0].TargetID)
	}
	if relations[0].ResolvedBy != "type_exact" {
		t.Fatalf("expected type_exact, got %s", relations[0].ResolvedBy)
	}
	t.Log("✅ TypeEnv receiver match → type_exact")
}

func TestResolveCalls_TypeMulti(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "caller", Name: "process", QualifiedName: "main.process", Kind: "function", FilePath: "main.go"},
		// Two "run" methods both in Animal class (overloaded)
		{ID: "run1", Name: "run", QualifiedName: "Animal.run", Kind: "function", FilePath: "animal.go", ClassType: "Animal"},
		{ID: "run2", Name: "run", QualifiedName: "Animal.run2", Kind: "function", FilePath: "animal.go", ClassType: "Animal"},
	})
	resolver := newTestResolver(table)

	envs := map[string]*model.TypeEnv{
		"main.go": {Bindings: map[string]*model.TypeInfo{"main.process:a": {TypeName: "Animal"}}},
	}
	calls := []model.RawCall{
		{CallerName: "main.process", CalledName: "run", ReceiverExpr: "a", FilePath: "main.go", ArgCount: 0},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	// Multiple matches in same class → type_multi with ConfidenceTypeParent
	if len(relations) < 2 {
		t.Fatalf("expected 2 relations (type_multi), got %d", len(relations))
	}
	if relations[0].ResolvedBy != "type_multi" {
		t.Fatalf("expected type_multi, got %s", relations[0].ResolvedBy)
	}
	if relations[0].Confidence > ConfidenceTypeParent {
		t.Fatalf("expected confidence <= %.2f (split among candidates), got %.2f", ConfidenceTypeParent, relations[0].Confidence)
	}
	t.Log("✅ Multiple receiver matches → type_multi")
}

func TestResolveHeritage_UniqueParentHighConfidence(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "child", Name: "Dog", Kind: "class", FilePath: "dog.py"},
		{ID: "parent", Name: "Animal", Kind: "class", FilePath: "animal.py"},
	})
	resolver := newTestResolver(table)

	heritage := []model.RawHeritage{
		{ChildName: "Dog", ParentName: "Animal", Kind: "extends", FilePath: "dog.py"},
	}

	relations := resolver.ResolveHeritage(heritage)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	// Unique parent candidate → ConfidenceArgCount (0.85)
	if relations[0].Confidence != ConfidenceArgCount {
		t.Fatalf("unique parent expected confidence %.2f, got %.2f", ConfidenceArgCount, relations[0].Confidence)
	}
	if relations[0].Kind != model.RelExtends {
		t.Fatalf("expected EXTENDS, got %s", relations[0].Kind)
	}
	t.Logf("✅ Unique parent → confidence %.2f", relations[0].Confidence)
}

func TestResolveHeritage_MultipleParentCandidatesLowerConfidence(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "child", Name: "Dog", Kind: "class", FilePath: "dog.py"},
		// Two classes named "Animal" in different files
		{ID: "parent1", Name: "Animal", Kind: "class", FilePath: "animal_v1.py"},
		{ID: "parent2", Name: "Animal", Kind: "class", FilePath: "animal_v2.py"},
	})
	resolver := newTestResolver(table)

	heritage := []model.RawHeritage{
		{ChildName: "Dog", ParentName: "Animal", Kind: "extends", FilePath: "dog.py"},
	}

	relations := resolver.ResolveHeritage(heritage)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	// Multiple parent candidates → ConfidenceNameUnique (0.70)
	if relations[0].Confidence != ConfidenceNameUnique {
		t.Fatalf("multiple parents expected confidence %.2f, got %.2f", ConfidenceNameUnique, relations[0].Confidence)
	}
	t.Logf("✅ Multiple parent candidates → confidence %.2f", relations[0].Confidence)
}

func TestResolveHeritage_ImplementsKind(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "child", Name: "UserService", Kind: "class", FilePath: "svc.java"},
		{ID: "parent", Name: "Serializable", Kind: "interface", FilePath: "serial.java"},
	})
	resolver := newTestResolver(table)

	heritage := []model.RawHeritage{
		{ChildName: "UserService", ParentName: "Serializable", Kind: "implements", FilePath: "svc.java"},
	}

	relations := resolver.ResolveHeritage(heritage)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].Kind != model.RelImplements {
		t.Fatalf("expected IMPLEMENTS, got %s", relations[0].Kind)
	}
	t.Log("✅ implements → IMPLEMENTS relation")
}

func TestResolveCall_TypeHierarchy(t *testing.T) {
	// BaseRepository has save(), UserDao extends BaseRepository but has no save()
	// Calling dao.save() where dao is UserDao should resolve to BaseRepository.save via hierarchy
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "base_class", Name: "BaseRepository", QualifiedName: "com.example.BaseRepository", Kind: "class", FilePath: "base.java"},
		{ID: "base_save", Name: "save", QualifiedName: "com.example.BaseRepository.save", Kind: "function", FilePath: "base.java"},
		{ID: "dao_class", Name: "UserDao", QualifiedName: "com.example.UserDao", Kind: "class", FilePath: "dao.java"},
		{ID: "dao_find", Name: "findById", QualifiedName: "com.example.UserDao.findById", Kind: "function", FilePath: "dao.java"},
		{ID: "other_save", Name: "save", QualifiedName: "com.example.OtherService.save", Kind: "function", FilePath: "other.java"},
		{ID: "caller", Name: "createUser", QualifiedName: "com.example.UserService.createUser", Kind: "function", FilePath: "service.java"},
	})

	resolver := newTestResolver(table)
	resolver.SetHeritage([]model.RawHeritage{
		{ChildName: "UserDao", ParentName: "BaseRepository", Kind: "extends", FilePath: "dao.java", ChildQualified: "com.example.UserDao"},
	})

	envs := map[string]*model.TypeEnv{
		"service.java": {Bindings: map[string]*model.TypeInfo{"com.example.UserService:dao": {TypeName: "UserDao"}}},
	}

	calls := []model.RawCall{
		{CalledName: "save", CallerName: "com.example.UserService.createUser", FilePath: "service.java", ReceiverExpr: "dao"},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) == 0 {
		t.Fatal("expected hierarchy resolution, got none")
	}
	if relations[0].TargetID != "base_save" {
		t.Fatalf("expected target base_save, got %s", relations[0].TargetID)
	}
	if relations[0].ResolvedBy != "type_hierarchy" {
		t.Fatalf("expected resolved_by type_hierarchy, got %s", relations[0].ResolvedBy)
	}
	t.Log("✅ resolveCall: inheritance chain lookup works (type_hierarchy)")
}

func TestResolveCall_TypeHierarchy_DirectMatchTakesPriority(t *testing.T) {
	// If UserDao has its own save(), should resolve to it directly (type_exact), not walk hierarchy
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "base_class", Name: "BaseRepository", QualifiedName: "com.example.BaseRepository", Kind: "class", FilePath: "base.java"},
		{ID: "base_save", Name: "save", QualifiedName: "com.example.BaseRepository.save", Kind: "function", FilePath: "base.java"},
		{ID: "dao_class", Name: "UserDao", QualifiedName: "com.example.UserDao", Kind: "class", FilePath: "dao.java"},
		{ID: "dao_save", Name: "save", QualifiedName: "com.example.UserDao.save", Kind: "function", FilePath: "dao.java"},
		{ID: "caller", Name: "createUser", QualifiedName: "com.example.UserService.createUser", Kind: "function", FilePath: "service.java"},
	})

	resolver := newTestResolver(table)
	resolver.SetHeritage([]model.RawHeritage{
		{ChildName: "UserDao", ParentName: "BaseRepository", Kind: "extends", FilePath: "dao.java", ChildQualified: "com.example.UserDao"},
	})

	envs := map[string]*model.TypeEnv{
		"service.java": {Bindings: map[string]*model.TypeInfo{"com.example.UserService:dao": {TypeName: "UserDao"}}},
	}

	calls := []model.RawCall{
		{CalledName: "save", CallerName: "com.example.UserService.createUser", FilePath: "service.java", ReceiverExpr: "dao"},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) == 0 {
		t.Fatal("expected resolution")
	}
	if relations[0].TargetID != "dao_save" {
		t.Fatalf("expected dao_save (direct match), got %s", relations[0].TargetID)
	}
	if relations[0].ResolvedBy != "type_exact" {
		t.Fatalf("expected type_exact, got %s", relations[0].ResolvedBy)
	}
	t.Log("✅ direct match takes priority over hierarchy lookup")
}

func TestResolveCall_TypeHierarchy_NoHeritageFallback(t *testing.T) {
	// Without heritage data, receiverType=UserDao is known but not in SymbolTable
	// and no import match — should return nil (treated as external), not best_guess
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "base_save", Name: "save", QualifiedName: "com.example.BaseRepository.save", Kind: "function", FilePath: "base.java"},
		{ID: "other_save", Name: "save", QualifiedName: "com.example.OtherService.save", Kind: "function", FilePath: "other.java"},
		{ID: "caller", Name: "createUser", QualifiedName: "com.example.UserService.createUser", Kind: "function", FilePath: "service.java"},
	})

	resolver := newTestResolver(table)
	// No SetHeritage — heritage is nil

	envs := map[string]*model.TypeEnv{
		"service.java": {Bindings: map[string]*model.TypeInfo{"UserService.createUser:dao": {TypeName: "UserDao"}}, Imports: []model.RawImport{}},
	}

	calls := []model.RawCall{
		{CalledName: "save", CallerName: "com.example.UserService.createUser", FilePath: "service.java", ReceiverExpr: "dao"},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 0 {
		t.Fatalf("expected 0 relations (UserDao not in SymbolTable, no import), got %d with resolved_by=%s", len(relations), relations[0].ResolvedBy)
	}
	t.Log("✅ without heritage and no UserDao in SymbolTable, returns nil (no best_guess explosion)")
}

func TestResolveCall_ClassScopeFieldType(t *testing.T) {
	// Field declared at class level: private OrderClient orderClient
	// Method calls orderClient.getOrder() — should resolve via class-level scope
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "oc_getOrder", Name: "getOrder", QualifiedName: "OrderClient.getOrder", Kind: "function", FilePath: "client.java"},
		{ID: "ctrl_getOrder", Name: "getOrder", QualifiedName: "OrderController.getOrder", Kind: "function", FilePath: "controller.java"},
	})

	resolver := newTestResolver(table)

	// TypeHint scope is class name "OrderController", not method name
	envs := map[string]*model.TypeEnv{
		"controller.java": {Bindings: map[string]*model.TypeInfo{
			"OrderController:orderClient": {TypeName: "OrderClient"},
		}},
	}

	calls := []model.RawCall{
		{CalledName: "getOrder", CallerName: "OrderController.getOrder", FilePath: "controller.java", ReceiverExpr: "orderClient"},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) == 0 {
		t.Fatal("expected resolution")
	}
	if relations[0].TargetID != "oc_getOrder" {
		t.Fatalf("expected OrderClient.getOrder, got %s (resolved_by: %s)", relations[0].TargetID, relations[0].ResolvedBy)
	}
	t.Logf("✅ class-level field type resolved: %s", relations[0].ResolvedBy)
}

func TestFilterByOwnerClass_ExactSegment(t *testing.T) {
	candidates := []model.Symbol{
		{QualifiedName: "kuzu.Store.Migrate"},
		{QualifiedName: "kuzu.UserStore.save"},
		{QualifiedName: "storage.GraphStore.Close"},
		{QualifiedName: "falkor.Store.Close"},
	}

	// "Store" should match kuzu.Store and falkor.Store, NOT UserStore
	matched := filterByOwnerClass(candidates, "Store")
	names := make(map[string]bool)
	for _, m := range matched {
		names[m.QualifiedName] = true
	}
	if names["kuzu.UserStore.save"] {
		t.Fatal("UserStore should not match Store")
	}
	if !names["kuzu.Store.Migrate"] {
		t.Fatal("kuzu.Store.Migrate should match Store")
	}
	if !names["falkor.Store.Close"] {
		t.Fatal("falkor.Store.Close should match Store")
	}

	// "GraphStore" should only match storage.GraphStore.Close
	matched = filterByOwnerClass(candidates, "GraphStore")
	if len(matched) != 1 || matched[0].QualifiedName != "storage.GraphStore.Close" {
		t.Fatalf("GraphStore: expected 1 match, got %d", len(matched))
	}

	// "*Store" should trim * and match same as "Store"
	matched = filterByOwnerClass(candidates, "*Store")
	if len(matched) != 2 {
		t.Fatalf("*Store: expected 2 matches, got %d", len(matched))
	}

	t.Log("✅ filterByOwnerClass: exact segment matching, no substring false positives")
}


func TestResolveCalls_TypeSameFile(t *testing.T) {
	// Simulate: falkor.Store and kuzu.Store both have TraverseCallChain,
	// plus the interface method. Caller is in falkor/store.go.
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "iface-tc", Name: "TraverseCallChain", QualifiedName: "storage.GraphStore.TraverseCallChain", Kind: "function", FilePath: "storage/storage.go",
			Params: `[{"name":"ctx","type":"context.Context"},{"name":"nodeID","type":"string"},{"name":"depth","type":"int"},{"name":"dir","type":"Direction"},{"name":"minConf","type":"float64"}]`},
		{ID: "falkor-tc", Name: "TraverseCallChain", QualifiedName: "falkor.Store.TraverseCallChain", Kind: "function", FilePath: "storage/falkor/store.go",
			Params: `[{"name":"ctx","type":"context.Context"},{"name":"nodeID","type":"string"},{"name":"depth","type":"int"},{"name":"dir","type":"Direction"},{"name":"minConf","type":"float64"}]`},
		{ID: "kuzu-tc", Name: "TraverseCallChain", QualifiedName: "kuzu.Store.TraverseCallChain", Kind: "function", FilePath: "storage/kuzu/store.go",
			Params: `[{"name":"ctx","type":"context.Context"},{"name":"nodeID","type":"string"},{"name":"depth","type":"int"},{"name":"dir","type":"Direction"},{"name":"minConf","type":"float64"}]`},
		// Caller
		{ID: "falkor-ti", Name: "TraverseImpact", QualifiedName: "falkor.Store.TraverseImpact", Kind: "function", FilePath: "storage/falkor/store.go"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"storage/falkor/store.go": {
			Bindings: map[string]*model.TypeInfo{
				"falkor.Store.TraverseImpact:store": {TypeName: "Store", Tier: 0},
			},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "TraverseCallChain",
		CallerName:   "falkor.Store.TraverseImpact",
		FilePath:     "storage/falkor/store.go",
		ReceiverExpr: "store",
		ArgCount:     5,
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	r := relations[0]
	if r.TargetID != "falkor-tc" {
		t.Fatalf("expected target falkor-tc, got %s", r.TargetID)
	}
	if r.ResolvedBy != "type_same_file" {
		t.Fatalf("expected resolved_by type_same_file, got %s", r.ResolvedBy)
	}
	if r.Confidence != ConfidenceSameFile {
		t.Fatalf("expected confidence %f, got %f", ConfidenceSameFile, r.Confidence)
	}
	t.Logf("✅ type_same_file: resolved to falkor.Store.TraverseCallChain (confidence %.2f)", r.Confidence)
}

func TestResolveCalls_ExternalViaReceiverType(t *testing.T) {
	// days.get(0) where days is List<String> — receiverType=List, not in SymbolTable
	// Should create external node via import match, not produce N best_guess edges
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "f1", Name: "get", QualifiedName: "com.example.DaoA.get", Kind: "function", FilePath: "DaoA.java", Params: `[{"name":"sql","type":"String"}]`},
		{ID: "f2", Name: "get", QualifiedName: "com.example.DaoB.get", Kind: "function", FilePath: "DaoB.java", Params: `[{"name":"sql","type":"String"}]`},
		{ID: "f3", Name: "get", QualifiedName: "com.example.DaoC.get", Kind: "function", FilePath: "DaoC.java", Params: `[{"name":"sql","type":"String"}]`},
		{ID: "caller1", Name: "refresh", QualifiedName: "com.example.Service.refresh", Kind: "function", FilePath: "Service.java"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"Service.java": {
			Bindings: map[string]*model.TypeInfo{
				"Service.refresh:days": {TypeName: "List", Tier: 0},
			},
			Imports: []model.RawImport{
				{ModulePath: "java.util.List", SymbolName: "List"},
			},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "get",
		CallerName:   "Service.refresh",
		FilePath:     "Service.java",
		ReceiverExpr: "days",
		ArgCount:     1,
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 external relation, got %d", len(relations))
	}
	if relations[0].ResolvedBy != "external" {
		t.Fatalf("expected resolved_by external, got %s", relations[0].ResolvedBy)
	}
	if relations[0].TargetID != "external:java.util.List.get" {
		t.Fatalf("expected target external:java.util.List.get, got %s", relations[0].TargetID)
	}
	t.Log("✅ List.get() resolved as external via import match, no best_guess explosion")
}

func TestResolveCalls_ReceiverTypeKnown_NoImport_ReturnNil(t *testing.T) {
	// String.length() — receiverType=String, no import (java.lang.*), not in SymbolTable
	// Should return nil, not best_guess
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "f1", Name: "length", QualifiedName: "com.example.Rope.length", Kind: "function", FilePath: "Rope.java"},
		{ID: "f2", Name: "length", QualifiedName: "com.example.Cable.length", Kind: "function", FilePath: "Cable.java"},
		{ID: "caller1", Name: "test", QualifiedName: "com.example.App.test", Kind: "function", FilePath: "App.java"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"App.java": {
			Bindings: map[string]*model.TypeInfo{
				"App.test:name": {TypeName: "String", Tier: 0},
			},
			Imports: []model.RawImport{},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "length",
		CallerName:   "App.test",
		FilePath:     "App.java",
		ReceiverExpr: "name",
		ArgCount:     0,
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 0 {
		t.Fatalf("expected 0 relations (external with no import), got %d with resolved_by=%s", len(relations), relations[0].ResolvedBy)
	}
	t.Log("✅ String.length() returns nil — no import, no SymbolTable match, no best_guess")
}

func TestResolveCalls_WildcardImport(t *testing.T) {
	// req.getRules() where req type is UpdateRequest, imported via com.example.request.*
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "f1", Name: "getRules", QualifiedName: "com.example.request.UpdateRequest.getRules", Kind: "function", FilePath: "UpdateRequest.java"},
		{ID: "c1", Name: "UpdateRequest", QualifiedName: "com.example.request.UpdateRequest", Kind: "class", FilePath: "UpdateRequest.java"},
		{ID: "caller1", Name: "handle", QualifiedName: "com.example.service.Handler.handle", Kind: "function", FilePath: "Handler.java"},
		{ID: "cc1", Name: "Handler", QualifiedName: "com.example.service.Handler", Kind: "class", FilePath: "Handler.java"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"Handler.java": {
			Bindings: map[string]*model.TypeInfo{
				"Handler.handle:req": {TypeName: "UpdateRequest", Tier: 0},
			},
			Imports: []model.RawImport{
				{ModulePath: "com.example.request", SymbolName: "request"},
			},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "getRules",
		CallerName:   "Handler.handle",
		FilePath:     "Handler.java",
		ReceiverExpr: "req",
		ArgCount:     0,
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "f1" {
		t.Fatalf("expected target f1, got %s", relations[0].TargetID)
	}
	if relations[0].ResolvedBy != "type_exact" {
		t.Fatalf("expected resolved_by type_exact, got %s", relations[0].ResolvedBy)
	}
	t.Log("✅ Wildcard import: UpdateRequest.getRules() resolved via QualifiedName prefix match")
}

func TestResolveCalls_SamePackageNoImport(t *testing.T) {
	// Same package class — no import needed
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "f1", Name: "query", QualifiedName: "com.example.service.ReportDao.query", Kind: "function", FilePath: "ReportDao.java"},
		{ID: "c1", Name: "ReportDao", QualifiedName: "com.example.service.ReportDao", Kind: "class", FilePath: "ReportDao.java"},
		{ID: "caller1", Name: "generate", QualifiedName: "com.example.service.ReportService.generate", Kind: "function", FilePath: "ReportService.java"},
		{ID: "cc1", Name: "ReportService", QualifiedName: "com.example.service.ReportService", Kind: "class", FilePath: "ReportService.java"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"ReportService.java": {
			Bindings: map[string]*model.TypeInfo{
				"ReportService.generate:dao": {TypeName: "ReportDao", Tier: 0},
			},
			Imports: []model.RawImport{}, // no import — same package
		},
	}

	calls := []model.RawCall{{
		CalledName:   "query",
		CallerName:   "ReportService.generate",
		FilePath:     "ReportService.java",
		ReceiverExpr: "dao",
		ArgCount:     0,
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "f1" {
		t.Fatalf("expected target f1, got %s", relations[0].TargetID)
	}
	if relations[0].ResolvedBy != "type_exact" {
		t.Fatalf("expected resolved_by type_exact, got %s", relations[0].ResolvedBy)
	}
	t.Log("✅ Same package: ReportDao.query() resolved without import")
}

func TestResolveCalls_SuperCall(t *testing.T) {
	// super.get() in ChildDao extends BaseDao — should resolve to BaseDao.get via heritage
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "base_get", Name: "get", QualifiedName: "com.example.dao.BaseDao.get", Kind: "function", FilePath: "BaseDao.java", Params: `[{"name":"sql","type":"String"},{"name":"params","type":"Object[]"},{"name":"clazz","type":"Class"}]`},
		{ID: "other_get", Name: "get", QualifiedName: "com.other.OtherDao.get", Kind: "function", FilePath: "OtherDao.java", Params: `[{"name":"sql","type":"String"},{"name":"params","type":"Object[]"},{"name":"clazz","type":"Class"}]`},
		{ID: "caller1", Name: "getById", QualifiedName: "com.example.dao.ChildDao.getById", Kind: "function", FilePath: "ChildDao.java"},
		{ID: "c_child", Name: "ChildDao", QualifiedName: "com.example.dao.ChildDao", Kind: "class", FilePath: "ChildDao.java"},
		{ID: "c_base", Name: "BaseDao", QualifiedName: "com.example.dao.BaseDao", Kind: "class", FilePath: "BaseDao.java"},
		{ID: "c_other", Name: "BaseDao", QualifiedName: "com.other.BaseDao", Kind: "class", FilePath: "OtherBaseDao.java"},
	})

	resolver := newTestResolver(table)
	resolver.SetHeritage([]model.RawHeritage{
		{ChildName: "ChildDao", ParentName: "BaseDao", Kind: "extends", FilePath: "ChildDao.java"},
	})

	envs := map[string]*model.TypeEnv{
		"ChildDao.java": {Bindings: map[string]*model.TypeInfo{}, Imports: []model.RawImport{}},
	}

	calls := []model.RawCall{{
		CalledName:   "get",
		CallerName:   "ChildDao.getById",
		FilePath:     "ChildDao.java",
		ReceiverExpr: "super",
		ArgCount:     3,
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "base_get" {
		t.Fatalf("expected target base_get (same package BaseDao), got %s", relations[0].TargetID)
	}
	t.Logf("✅ super.get() resolved to BaseDao.get via heritage + same-package (resolved_by: %s)", relations[0].ResolvedBy)
}

func TestResolveCalls_NoReceiverInheritedMethod(t *testing.T) {
	// get(sql, params, clazz) in PrmTeamDao — no receiver, method defined in BaseDao
	// Multiple candidates with same arg count to ensure arg_count can't disambiguate
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "base_get", Name: "get", QualifiedName: "com.example.dao.BaseDao.get", Kind: "function", FilePath: "BaseDao.java", Params: `[{"name":"sql","type":"String"},{"name":"params","type":"Object[]"},{"name":"clazz","type":"Class"}]`},
		{ID: "other_get", Name: "get", QualifiedName: "com.example.dao.OtherBaseDao.get", Kind: "function", FilePath: "OtherBaseDao.java", Params: `[{"name":"sql","type":"String"},{"name":"params","type":"Object[]"},{"name":"clazz","type":"Class"}]`},
		{ID: "third_get", Name: "get", QualifiedName: "com.example.dao.ThirdDao.get", Kind: "function", FilePath: "ThirdDao.java", Params: `[{"name":"sql","type":"String"},{"name":"params","type":"Object[]"},{"name":"clazz","type":"Class"}]`},
		{ID: "caller1", Name: "getByTeamName", QualifiedName: "com.example.dao.PrmTeamDao.getByTeamName", Kind: "function", FilePath: "PrmTeamDao.java"},
		{ID: "c_child", Name: "PrmTeamDao", QualifiedName: "com.example.dao.PrmTeamDao", Kind: "class", FilePath: "PrmTeamDao.java"},
		{ID: "c_base", Name: "BaseDao", QualifiedName: "com.example.dao.BaseDao", Kind: "class", FilePath: "BaseDao.java"},
	})

	resolver := newTestResolver(table)
	resolver.SetHeritage([]model.RawHeritage{
		{ChildName: "PrmTeamDao", ParentName: "BaseDao", Kind: "extends", FilePath: "PrmTeamDao.java", ChildQualified: "com.example.dao.PrmTeamDao"},
	})

	envs := map[string]*model.TypeEnv{
		"PrmTeamDao.java": {Bindings: map[string]*model.TypeInfo{}, Imports: []model.RawImport{}},
	}

	calls := []model.RawCall{{
		CalledName:   "get",
		CallerName:   "com.example.dao.PrmTeamDao.getByTeamName",
		FilePath:     "PrmTeamDao.java",
		ReceiverExpr: "",
		ArgCount:     3,
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "base_get" {
		t.Fatalf("expected target base_get, got %s (resolved_by: %s)", relations[0].TargetID, relations[0].ResolvedBy)
	}
	if relations[0].ResolvedBy != "type_hierarchy" {
		t.Fatalf("expected resolved_by type_hierarchy, got %s", relations[0].ResolvedBy)
	}
	t.Log("✅ No-receiver get() resolved to BaseDao.get via heritage chain")
}

func TestResolveCalls_EnrichArgTypes_Variable(t *testing.T) {
	// setFail(code) where code is ExceptionCode — should disambiguate overloads
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "sf1", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "function", FilePath: "ApiResult.java", Params: `[{"name":"code","type":"ExceptionCode"}]`},
		{ID: "sf2", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "function", FilePath: "ApiResult.java", Params: `[{"name":"msg","type":"String"}]`},
		{ID: "sf3", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "function", FilePath: "ApiResult.java", Params: `[{"name":"e","type":"Exception"}]`},
		{ID: "caller1", Name: "handle", QualifiedName: "com.example.Controller.handle", Kind: "function", FilePath: "Controller.java"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"Controller.java": {
			Bindings: map[string]*model.TypeInfo{
				"Controller.handle:code": {TypeName: "ExceptionCode"},
			},
			Imports: []model.RawImport{
				{ModulePath: "com.example.ApiResult", SymbolName: "ApiResult"},
			},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "setFail",
		CallerName:   "Controller.handle",
		FilePath:     "Controller.java",
		ReceiverExpr: "ApiResult",
		ArgCount:     1,
		ArgTypes:     []string{""},       // parser couldn't infer
		ArgExprs:     []string{"code"},   // variable name
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation (disambiguated), got %d", len(relations))
	}
	if relations[0].TargetID != "sf1" {
		t.Fatalf("expected target sf1 (ExceptionCode overload), got %s", relations[0].TargetID)
	}
	t.Logf("✅ setFail(code) disambiguated to ExceptionCode overload via TypeEnv (resolved_by: %s)", relations[0].ResolvedBy)
}

func TestResolveCalls_EnrichArgTypes_MethodCall(t *testing.T) {
	// setFail(getCode()) where getCode returns ExceptionCode
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "sf1", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "function", FilePath: "ApiResult.java", Params: `[{"name":"code","type":"ExceptionCode"}]`},
		{ID: "sf2", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "function", FilePath: "ApiResult.java", Params: `[{"name":"msg","type":"String"}]`},
		{ID: "gc", Name: "getCode", QualifiedName: "com.example.Controller.getCode", Kind: "function", FilePath: "Controller.java", ReturnTypes: []string{"ExceptionCode"}},
		{ID: "caller1", Name: "handle", QualifiedName: "com.example.Controller.handle", Kind: "function", FilePath: "Controller.java"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"Controller.java": {
			Bindings: map[string]*model.TypeInfo{},
			Imports: []model.RawImport{
				{ModulePath: "com.example.ApiResult", SymbolName: "ApiResult"},
			},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "setFail",
		CallerName:   "Controller.handle",
		FilePath:     "Controller.java",
		ReceiverExpr: "ApiResult",
		ArgCount:     1,
		ArgTypes:     []string{""},
		ArgExprs:     []string{"getCode()"},
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation (disambiguated), got %d", len(relations))
	}
	if relations[0].TargetID != "sf1" {
		t.Fatalf("expected target sf1 (ExceptionCode overload), got %s", relations[0].TargetID)
	}
	t.Logf("✅ setFail(getCode()) disambiguated via return type inference (resolved_by: %s)", relations[0].ResolvedBy)
}

func TestResolveCalls_EnrichArgTypes_StaticImport(t *testing.T) {
	// setFail(Safety) where Safety is static-imported from ExceptionCode
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "sf1", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "function", FilePath: "ApiResult.java", Params: `[{"name":"code","type":"ExceptionCode"}]`},
		{ID: "sf2", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "function", FilePath: "ApiResult.java", Params: `[{"name":"msg","type":"String"}]`},
		{ID: "safety", Name: "Safety", QualifiedName: "com.example.ExceptionCode.Safety", Kind: "function", FilePath: "ExceptionCode.java"},
		{ID: "caller1", Name: "handle", QualifiedName: "com.example.Controller.handle", Kind: "function", FilePath: "Controller.java"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"Controller.java": {
			Bindings: map[string]*model.TypeInfo{},
			Imports: []model.RawImport{
				{ModulePath: "com.example.ApiResult", SymbolName: "ApiResult"},
				{ModulePath: "com.example.ExceptionCode", SymbolName: "ExceptionCode"},
			},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "setFail",
		CallerName:   "Controller.handle",
		FilePath:     "Controller.java",
		ReceiverExpr: "ApiResult",
		ArgCount:     1,
		ArgTypes:     []string{""},
		ArgExprs:     []string{"Safety"},
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation (disambiguated), got %d", len(relations))
	}
	if relations[0].TargetID != "sf1" {
		t.Fatalf("expected target sf1 (ExceptionCode overload), got %s", relations[0].TargetID)
	}
	t.Logf("✅ setFail(Safety) disambiguated via static import (resolved_by: %s)", relations[0].ResolvedBy)
}

func TestResolveCalls_EnrichArgTypes_NoEffect(t *testing.T) {
	// When ArgTypes already has type, enrichment should not change anything
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "sf1", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "function", FilePath: "ApiResult.java", Params: `[{"name":"msg","type":"String"}]`},
		{ID: "sf2", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "function", FilePath: "ApiResult.java", Params: `[{"name":"code","type":"int"}]`},
		{ID: "caller1", Name: "handle", QualifiedName: "com.example.Controller.handle", Kind: "function", FilePath: "Controller.java"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"Controller.java": {
			Bindings: map[string]*model.TypeInfo{},
			Imports: []model.RawImport{
				{ModulePath: "com.example.ApiResult", SymbolName: "ApiResult"},
			},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "setFail",
		CallerName:   "Controller.handle",
		FilePath:     "Controller.java",
		ReceiverExpr: "ApiResult",
		ArgCount:     1,
		ArgTypes:     []string{"String"},       // already inferred
		ArgExprs:     []string{"\"error\""},
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "sf1" {
		t.Fatalf("expected target sf1 (String overload), got %s", relations[0].TargetID)
	}
	t.Logf("✅ Already-typed arg not overridden by enrichment (resolved_by: %s)", relations[0].ResolvedBy)
}

func TestResolveCalls_SuperCall_MultipleBaseDao(t *testing.T) {
	// 5 BaseDao classes in different packages, each with get() method
	// CoinRechargeDao in biz-core extends BaseDao — super.get() should resolve to biz-core's BaseDao.get only
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		// biz-core BaseDao
		{ID: "biz_get1", Name: "get", QualifiedName: "com.weijin.chatting.biz.core.dao.BaseDao.get", Kind: "function", FilePath: "chatting-biz-core/src/main/java/com/weijin/chatting/biz/core/dao/BaseDao.java", Params: `[{"name":"sql","type":"String"},{"name":"params","type":"Object[]"},{"name":"clazz","type":"Class"}]`},
		{ID: "biz_get2", Name: "get", QualifiedName: "com.weijin.chatting.biz.core.dao.BaseDao.get", Kind: "function", FilePath: "chatting-biz-core/src/main/java/com/weijin/chatting/biz/core/dao/BaseDao.java", Params: `[{"name":"id","type":"Object"},{"name":"clazz","type":"Class"}]`},
		{ID: "biz_base", Name: "BaseDao", QualifiedName: "com.weijin.chatting.biz.core.dao.BaseDao", Kind: "class", FilePath: "chatting-biz-core/src/main/java/com/weijin/chatting/biz/core/dao/BaseDao.java"},
		// admin BaseDao
		{ID: "admin_get1", Name: "get", QualifiedName: "com.weijin.chatting.admin.dao.base.BaseDao.get", Kind: "function", FilePath: "admin/src/main/java/com/weijin/chatting/admin/dao/base/BaseDao.java", Params: `[{"name":"sql","type":"String"},{"name":"params","type":"Object[]"},{"name":"clazz","type":"Class"}]`},
		{ID: "admin_get2", Name: "get", QualifiedName: "com.weijin.chatting.admin.dao.base.BaseDao.get", Kind: "function", FilePath: "admin/src/main/java/com/weijin/chatting/admin/dao/base/BaseDao.java", Params: `[{"name":"id","type":"Object"},{"name":"clazz","type":"Class"}]`},
		{ID: "admin_base", Name: "BaseDao", QualifiedName: "com.weijin.chatting.admin.dao.base.BaseDao", Kind: "class", FilePath: "admin/src/main/java/com/weijin/chatting/admin/dao/base/BaseDao.java"},
		// prm-core BaseDao
		{ID: "prm_get1", Name: "get", QualifiedName: "com.weijin.chatting.prm.core.dao.BaseDao.get", Kind: "function", FilePath: "chatting-prm-core/src/main/java/com/weijin/chatting/prm/core/dao/BaseDao.java", Params: `[{"name":"sql","type":"String"},{"name":"params","type":"Object[]"},{"name":"clazz","type":"Class"}]`},
		{ID: "prm_base", Name: "BaseDao", QualifiedName: "com.weijin.chatting.prm.core.dao.BaseDao", Kind: "class", FilePath: "chatting-prm-core/src/main/java/com/weijin/chatting/prm/core/dao/BaseDao.java"},
		// CoinRechargeDao in biz-core
		{ID: "caller1", Name: "getByOrderNo", QualifiedName: "com.weijin.chatting.biz.core.dao.CoinRechargeDao.getByOrderNo", Kind: "function", FilePath: "chatting-biz-core/src/main/java/com/weijin/chatting/biz/core/dao/CoinRechargeDao.java"},
		{ID: "c_coin", Name: "CoinRechargeDao", QualifiedName: "com.weijin.chatting.biz.core.dao.CoinRechargeDao", Kind: "class", FilePath: "chatting-biz-core/src/main/java/com/weijin/chatting/biz/core/dao/CoinRechargeDao.java"},
	})

	resolver := newTestResolver(table)
	resolver.SetHeritage([]model.RawHeritage{
		{ChildName: "CoinRechargeDao", ParentName: "BaseDao", Kind: "extends", FilePath: "chatting-biz-core/src/main/java/com/weijin/chatting/biz/core/dao/CoinRechargeDao.java"},
	})

	envs := map[string]*model.TypeEnv{
		"chatting-biz-core/src/main/java/com/weijin/chatting/biz/core/dao/CoinRechargeDao.java": {
			Bindings: map[string]*model.TypeInfo{},
			Imports:  []model.RawImport{},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "get",
		CallerName:   "CoinRechargeDao.getByOrderNo",
		FilePath:     "chatting-biz-core/src/main/java/com/weijin/chatting/biz/core/dao/CoinRechargeDao.java",
		ReceiverExpr: "super",
		ArgCount:     3,
	}}

	relations, hints := resolver.ResolveCalls(calls, envs)
	t.Logf("Relations: %d, Hints: %d", len(relations), len(hints))
	for _, r := range relations {
		t.Logf("  %s → %s (resolved_by: %s, confidence: %.3f)", r.SourceID, r.TargetID, r.ResolvedBy, r.Confidence)
	}

	// Should resolve to exactly 1 — biz-core's BaseDao.get with 3 params
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation (biz-core BaseDao.get), got %d", len(relations))
	}
	if relations[0].TargetID != "biz_get1" {
		t.Fatalf("expected target biz_get1, got %s (resolved_by: %s)", relations[0].TargetID, relations[0].ResolvedBy)
	}
	if relations[0].ResolvedBy != "type_exact" && relations[0].ResolvedBy != "arg_count" {
		t.Fatalf("expected type_exact or arg_count, got %s", relations[0].ResolvedBy)
	}
	t.Log("✅ super.get() with multiple BaseDaos resolved to correct package's BaseDao.get")
}

func TestResolveCalls_EnrichArgTypes_ChainedExpr(t *testing.T) {
	// coinAccountDao.get(command.getUserId()) — getUserId returns Long, disambiguates get(String) vs get(Long)
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "get_str", Name: "get", QualifiedName: "com.example.CoinAccountDao.get", Kind: "function", FilePath: "CoinAccountDao.java", Params: `[{"name":"id","type":"String"}]`, ReturnTypes: []string{"CoinAccount"}},
		{ID: "get_long", Name: "get", QualifiedName: "com.example.CoinAccountDao.get", Kind: "function", FilePath: "CoinAccountDao.java", Params: `[{"name":"userId","type":"Long"}]`, ReturnTypes: []string{"CoinAccount"}},
		{ID: "getUserId", Name: "getUserId", QualifiedName: "com.example.Command.getUserId", Kind: "function", FilePath: "Command.java", ReturnTypes: []string{"Long"}},
		{ID: "c_dao", Name: "CoinAccountDao", QualifiedName: "com.example.CoinAccountDao", Kind: "class", FilePath: "CoinAccountDao.java"},
		{ID: "c_cmd", Name: "Command", QualifiedName: "com.example.Command", Kind: "class", FilePath: "Command.java"},
		{ID: "caller1", Name: "recharge", QualifiedName: "com.example.Service.recharge", Kind: "function", FilePath: "Service.java"},
		{ID: "c_svc", Name: "Service", QualifiedName: "com.example.Service", Kind: "class", FilePath: "Service.java"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"Service.java": {
			Bindings: map[string]*model.TypeInfo{
				"Service:coinAccountDao":  {TypeName: "CoinAccountDao"},
				"Service.recharge:command": {TypeName: "Command"},
			},
			Imports: []model.RawImport{},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "get",
		CallerName:   "Service.recharge",
		FilePath:     "Service.java",
		ReceiverExpr: "coinAccountDao",
		ArgCount:     1,
		ArgTypes:     []string{""},                      // parser couldn't infer
		ArgExprs:     []string{"command.getUserId()"},   // chained expression
	}}

	relations, hints := resolver.ResolveCalls(calls, envs)
	if len(hints) > 0 {
		t.Fatalf("expected 0 hints, got %d", len(hints))
	}
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "get_long" {
		t.Fatalf("expected target get_long (Long overload), got %s (resolved_by: %s)", relations[0].TargetID, relations[0].ResolvedBy)
	}
	t.Logf("✅ get(command.getUserId()) disambiguated to get(Long) via chained expr type inference (resolved_by: %s)", relations[0].ResolvedBy)
}

func TestResolveCalls_EnrichArgTypes_ChainedExpr_NoSideEffect(t *testing.T) {
	// When chained expr type can't be resolved, should not change behavior
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "proc1", Name: "process", QualifiedName: "com.example.ServiceA.process", Kind: "function", FilePath: "ServiceA.java", Params: `[{"name":"data","type":"String"}]`},
		{ID: "proc2", Name: "process", QualifiedName: "com.example.ServiceA.process", Kind: "function", FilePath: "ServiceA.java", Params: `[{"name":"data","type":"Integer"}]`},
		{ID: "c_a", Name: "ServiceA", QualifiedName: "com.example.ServiceA", Kind: "class", FilePath: "ServiceA.java"},
		{ID: "caller1", Name: "run", QualifiedName: "com.example.App.run", Kind: "function", FilePath: "App.java"},
		{ID: "c_app", Name: "App", QualifiedName: "com.example.App", Kind: "class", FilePath: "App.java"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"App.java": {
			Bindings: map[string]*model.TypeInfo{
				"App:svc": {TypeName: "ServiceA"},
			},
			Imports: []model.RawImport{},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "process",
		CallerName:   "App.run",
		FilePath:     "App.java",
		ReceiverExpr: "svc",
		ArgCount:     1,
		ArgTypes:     []string{""},
		ArgExprs:     []string{"unknown.getData()"},  // can't resolve unknown
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	// Should still produce type_multi (2 candidates), not crash or change to wrong result
	if len(relations) != 2 {
		t.Fatalf("expected 2 relations (type_multi, unresolvable arg), got %d", len(relations))
	}
	for _, r := range relations {
		if r.ResolvedBy != "type_multi" {
			t.Fatalf("expected type_multi, got %s", r.ResolvedBy)
		}
	}
	t.Log("✅ Unresolvable chained arg doesn't change behavior — still type_multi")
}

func TestResolveCalls_EnrichArgTypes_ChainedExpr_MethodParam(t *testing.T) {
	// userInfoDao.get(reqs.getBroadcasterId()) — getBroadcasterId returns Long, disambiguates get(Integer) vs get(Long)
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "get_int", Name: "get", QualifiedName: "com.example.UserInfoDao.get", Kind: "function", FilePath: "UserInfoDao.java", Params: `[{"name":"id","type":"Integer"}]`},
		{ID: "get_long", Name: "get", QualifiedName: "com.example.UserInfoDao.get", Kind: "function", FilePath: "UserInfoDao.java", Params: `[{"name":"userId","type":"Long"}]`},
		{ID: "getBroadcasterId", Name: "getBroadcasterId", QualifiedName: "com.example.Reqs.getBroadcasterId", Kind: "function", FilePath: "Reqs.java", ReturnTypes: []string{"Long"}},
		{ID: "c_dao", Name: "UserInfoDao", QualifiedName: "com.example.UserInfoDao", Kind: "class", FilePath: "UserInfoDao.java"},
		{ID: "c_reqs", Name: "Reqs", QualifiedName: "com.example.Reqs", Kind: "class", FilePath: "Reqs.java"},
		{ID: "caller1", Name: "handle", QualifiedName: "com.example.Controller.handle", Kind: "function", FilePath: "Controller.java"},
		{ID: "c_ctrl", Name: "Controller", QualifiedName: "com.example.Controller", Kind: "class", FilePath: "Controller.java"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"Controller.java": {
			Bindings: map[string]*model.TypeInfo{
				"Controller:userInfoDao":  {TypeName: "UserInfoDao"},
				"Controller.handle:reqs":  {TypeName: "Reqs"},
			},
			Imports: []model.RawImport{},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "get",
		CallerName:   "Controller.handle",
		FilePath:     "Controller.java",
		ReceiverExpr: "userInfoDao",
		ArgCount:     1,
		ArgTypes:     []string{""},
		ArgExprs:     []string{"reqs.getBroadcasterId()"},
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	t.Logf("Relations: %d", len(relations))
	for _, r := range relations {
		t.Logf("  → %s (resolved_by: %s, confidence: %.3f)", r.TargetID, r.ResolvedBy, r.Confidence)
	}

	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "get_long" {
		t.Fatalf("expected get_long, got %s", relations[0].TargetID)
	}
	t.Log("✅ get(reqs.getBroadcasterId()) disambiguated to get(Long)")
}

func TestResolveCalls_EnrichArgTypes_RealCase_BroadcasterController(t *testing.T) {
	// Real case: this.userInfoDao.get(reqs.getBroadcasterId())
	// CallerName = "BroadcasterController.getLastCycleIncome"
	// reqs is method param with type GetLastCycleIncomeReqs
	// getBroadcasterId is Lombok-generated getter returning Long
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "get_int", Name: "get", QualifiedName: "com.weijin.chatting.biz.core.dao.UserInfoDao.get", Kind: "function", FilePath: "chatting-biz-core/src/main/java/com/weijin/chatting/biz/core/dao/UserInfoDao.java", Params: `[{"name":"id","type":"Integer"}]`},
		{ID: "get_long", Name: "get", QualifiedName: "com.weijin.chatting.biz.core.dao.UserInfoDao.get", Kind: "function", FilePath: "chatting-biz-core/src/main/java/com/weijin/chatting/biz/core/dao/UserInfoDao.java", Params: `[{"name":"userId","type":"Long"}]`},
		// Lombok-generated getter
		{ID: "getBroadcasterId", Name: "getBroadcasterId", QualifiedName: "com.weijin.chatting.admin.model.request.broadcaster.GetLastCycleIncomeReqs.getBroadcasterId", Kind: "function",
			FilePath: "admin/src/main/java/com/weijin/chatting/admin/model/request/broadcaster/GetLastCycleIncomeReqs.java",
			ReturnTypes: []string{"Long"}, IsSynthetic: true},
		{ID: "c_dao", Name: "UserInfoDao", QualifiedName: "com.weijin.chatting.biz.core.dao.UserInfoDao", Kind: "class", FilePath: "chatting-biz-core/src/main/java/com/weijin/chatting/biz/core/dao/UserInfoDao.java"},
		{ID: "c_reqs", Name: "GetLastCycleIncomeReqs", QualifiedName: "com.weijin.chatting.admin.model.request.broadcaster.GetLastCycleIncomeReqs", Kind: "class",
			FilePath: "admin/src/main/java/com/weijin/chatting/admin/model/request/broadcaster/GetLastCycleIncomeReqs.java"},
		{ID: "caller1", Name: "getLastCycleIncome", QualifiedName: "com.weijin.chatting.admin.controller.BroadcasterController.getLastCycleIncome", Kind: "function",
			FilePath: "admin/src/main/java/com/weijin/chatting/admin/controller/BroadcasterController.java"},
		{ID: "c_ctrl", Name: "BroadcasterController", QualifiedName: "com.weijin.chatting.admin.controller.BroadcasterController", Kind: "class",
			FilePath: "admin/src/main/java/com/weijin/chatting/admin/controller/BroadcasterController.java"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"admin/src/main/java/com/weijin/chatting/admin/controller/BroadcasterController.java": {
			Bindings: map[string]*model.TypeInfo{
				"BroadcasterController:userInfoDao": {TypeName: "com.weijin.chatting.biz.core.dao.UserInfoDao"},
				// Method param — parser generates this key format
				"BroadcasterController.getLastCycleIncome:reqs": {TypeName: "GetLastCycleIncomeReqs"},
			},
			Imports: []model.RawImport{},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "get",
		CallerName:   "BroadcasterController.getLastCycleIncome",
		FilePath:     "admin/src/main/java/com/weijin/chatting/admin/controller/BroadcasterController.java",
		ReceiverExpr: "userInfoDao",
		ArgCount:     1,
		ArgTypes:     []string{""},
		ArgExprs:     []string{"reqs.getBroadcasterId()"},
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	t.Logf("Relations: %d", len(relations))
	for _, r := range relations {
		t.Logf("  → %s (resolved_by: %s, confidence: %.3f)", r.TargetID, r.ResolvedBy, r.Confidence)
	}

	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d (enrichArgTypes may have failed to resolve reqs.getBroadcasterId())", len(relations))
	}
	if relations[0].TargetID != "get_long" {
		t.Fatalf("expected get_long, got %s", relations[0].TargetID)
	}
	t.Log("✅ Real case: userInfoDao.get(reqs.getBroadcasterId()) → get(Long)")
}

func TestResolveCalls_EnrichArgTypes_RealCase_ShortScope(t *testing.T) {
	// TypeEnv uses fully qualified scope (after 1.4 unification)
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "get_int", Name: "get", QualifiedName: "com.weijin.UserInfoDao.get", Kind: "function", FilePath: "UserInfoDao.java", Params: `[{"name":"id","type":"Integer"}]`},
		{ID: "get_long", Name: "get", QualifiedName: "com.weijin.UserInfoDao.get", Kind: "function", FilePath: "UserInfoDao.java", Params: `[{"name":"userId","type":"Long"}]`},
		{ID: "getBId", Name: "getBroadcasterId", QualifiedName: "com.weijin.Reqs.getBroadcasterId", Kind: "function", FilePath: "Reqs.java", ReturnTypes: []string{"Long"}, IsSynthetic: true},
		{ID: "c_dao", Name: "UserInfoDao", QualifiedName: "com.weijin.UserInfoDao", Kind: "class", FilePath: "UserInfoDao.java"},
		{ID: "c_reqs", Name: "Reqs", QualifiedName: "com.weijin.Reqs", Kind: "class", FilePath: "Reqs.java"},
		{ID: "caller1", Name: "getLastCycleIncome", QualifiedName: "com.weijin.Controller.getLastCycleIncome", Kind: "function", FilePath: "Controller.java"},
		{ID: "c_ctrl", Name: "Controller", QualifiedName: "com.weijin.Controller", Kind: "class", FilePath: "Controller.java"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"Controller.java": {
			Bindings: map[string]*model.TypeInfo{
				"com.weijin.Controller:userInfoDao":                      {TypeName: "com.weijin.UserInfoDao"},
				"com.weijin.Controller.getLastCycleIncome:reqs": {TypeName: "Reqs"},
			},
			Imports: []model.RawImport{},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "get",
		CallerName:   "com.weijin.Controller.getLastCycleIncome",
		FilePath:     "Controller.java",
		ReceiverExpr: "userInfoDao",
		ArgCount:     1,
		ArgTypes:     []string{""},
		ArgExprs:     []string{"reqs.getBroadcasterId()"},
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	t.Logf("Relations: %d", len(relations))
	for _, r := range relations {
		t.Logf("  → %s (resolved_by: %s)", r.TargetID, r.ResolvedBy)
	}

	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d — short scope key may not match", len(relations))
	}
	if relations[0].TargetID != "get_long" {
		t.Fatalf("expected get_long, got %s", relations[0].TargetID)
	}
	t.Log("✅ Short scope key: getLastCycleIncome:reqs matched correctly")
}

func TestResolveCalls_TypeMulti_CrossModuleSameClass(t *testing.T) {
	// UserInfo.getUserId() exists in 3 modules — should narrow to caller's module via import/same-package
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		// biz-core UserInfo
		{ID: "biz_getUserId", Name: "getUserId", QualifiedName: "com.weijin.biz.core.domain.UserInfo.getUserId", Kind: "function",
			FilePath: "chatting-biz-core/src/main/java/com/weijin/biz/core/domain/UserInfo.java", ReturnTypes: []string{"Long"}},
		{ID: "biz_userinfo", Name: "UserInfo", QualifiedName: "com.weijin.biz.core.domain.UserInfo", Kind: "class",
			FilePath: "chatting-biz-core/src/main/java/com/weijin/biz/core/domain/UserInfo.java"},
		// admin UserInfo
		{ID: "admin_getUserId", Name: "getUserId", QualifiedName: "com.weijin.admin.entity.UserInfo.getUserId", Kind: "function",
			FilePath: "admin/src/main/java/com/weijin/admin/entity/UserInfo.java", ReturnTypes: []string{"Long"}},
		{ID: "admin_userinfo", Name: "UserInfo", QualifiedName: "com.weijin.admin.entity.UserInfo", Kind: "class",
			FilePath: "admin/src/main/java/com/weijin/admin/entity/UserInfo.java"},
		// analysis UserInfo
		{ID: "analysis_getUserId", Name: "getUserId", QualifiedName: "com.weijin.analysis.domain.UserInfo.getUserId", Kind: "function",
			FilePath: "chatting-analysis-core/src/main/java/com/weijin/analysis/domain/UserInfo.java", ReturnTypes: []string{"Long"}},
		// Caller in biz-core service
		{ID: "caller1", Name: "process", QualifiedName: "com.weijin.biz.core.service.RechargeService.process", Kind: "function",
			FilePath: "chatting-biz-core/src/main/java/com/weijin/biz/core/service/RechargeService.java"},
		{ID: "c_svc", Name: "RechargeService", QualifiedName: "com.weijin.biz.core.service.RechargeService", Kind: "class",
			FilePath: "chatting-biz-core/src/main/java/com/weijin/biz/core/service/RechargeService.java"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"chatting-biz-core/src/main/java/com/weijin/biz/core/service/RechargeService.java": {
			Bindings: map[string]*model.TypeInfo{
				"RechargeService.process:userInfo": {TypeName: "UserInfo"},
			},
			Imports: []model.RawImport{
				{ModulePath: "com.weijin.biz.core.domain.UserInfo", SymbolName: "UserInfo"},
			},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "getUserId",
		CallerName:   "RechargeService.process",
		FilePath:     "chatting-biz-core/src/main/java/com/weijin/biz/core/service/RechargeService.java",
		ReceiverExpr: "userInfo",
		ArgCount:     0,
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "biz_getUserId" {
		t.Fatalf("expected biz_getUserId, got %s (resolved_by: %s)", relations[0].TargetID, relations[0].ResolvedBy)
	}
	t.Logf("✅ Cross-module UserInfo.getUserId() narrowed to biz-core via import (resolved_by: %s)", relations[0].ResolvedBy)
}


func TestResolveCalls_JDKHierarchy_NoFalsePositive(t *testing.T) {
	// process(String) vs process(Integer) — String and Integer are not related
	// Should keep both (no hierarchy relationship)
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "p_str", Name: "process", QualifiedName: "com.example.Svc.process", Kind: "function", FilePath: "Svc.java", Params: `[{"name":"data","type":"String"}]`},
		{ID: "p_int", Name: "process", QualifiedName: "com.example.Svc.process", Kind: "function", FilePath: "Svc.java", Params: `[{"name":"data","type":"Integer"}]`},
		{ID: "c_svc", Name: "Svc", QualifiedName: "com.example.Svc", Kind: "class", FilePath: "Svc.java"},
		{ID: "caller1", Name: "run", QualifiedName: "com.example.App.run", Kind: "function", FilePath: "App.java"},
		{ID: "c_app", Name: "App", QualifiedName: "com.example.App", Kind: "class", FilePath: "App.java"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"App.java": {
			Bindings: map[string]*model.TypeInfo{
				"App:svc": {TypeName: "Svc"},
			},
			Imports: []model.RawImport{},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "process",
		CallerName:   "App.run",
		FilePath:     "App.java",
		ReceiverExpr: "svc",
		ArgCount:     1,
		ArgTypes:     []string{""},
		ArgExprs:     []string{"data"},
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	// Should still be type_multi (2) — can't disambiguate without knowing data's type
	if len(relations) != 2 {
		t.Fatalf("expected 2 relations (type_multi), got %d", len(relations))
	}
	t.Log("✅ No false positive: unrelated types stay as type_multi")
}

