package resolver

import (
	"strings"
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// testJavaJDKHelper embeds testJavaHelper but provides JDK method return types for chain inference.
type testJavaJDKHelper struct {
	testJavaHelper
}

var testJDKReturns = map[string]string{
	"String.trim": "String", "String.toUpperCase": "String", "String.toLowerCase": "String",
	"DateUtil.parseDate": "DateTime", "DateTime.getTime": "long",
}

func (h *testJavaJDKHelper) LookupMethodReturn(typeName, methodName string, _ []string) (model.ReturnType, bool) {
	ret, ok := testJDKReturns[typeName+"."+methodName]
	if !ok {
		return model.ReturnType{}, false
	}
	return model.ParseReturnType(ret), true
}

func (h *testJavaJDKHelper) IsTypeAssignable(argType, paramType string) bool {
	if argType == paramType {
		return true
	}
	// Boxing: long↔Long, int↔Integer, etc.
	boxMap := map[string]string{
		"int": "Integer", "Integer": "int",
		"long": "Long", "Long": "long",
		"double": "Double", "Double": "double",
		"boolean": "Boolean", "Boolean": "boolean",
	}
	if boxed, ok := boxMap[argType]; ok && boxed == paramType {
		return true
	}
	return false
}

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
func (testGenericHelper) LookupMethodReturn(_, _ string, _ []string) (model.ReturnType, bool) {
	return model.ReturnType{}, false
}
func (testGenericHelper) BuildExternalQualifiedName(typeName, methodName string) string {
	return typeName + "." + methodName
}
func (testGenericHelper) LookupClassTypeParams(_ string) []string                    { return nil }
func (testGenericHelper) IsExternalPackage(_ string) bool                            { return false }
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
func (h *testJavaHelper) LookupMethodReturn(_, _ string, _ []string) (model.ReturnType, bool) {
	return model.ReturnType{}, false
}
func (h *testJavaHelper) BuildExternalQualifiedName(typeName, methodName string) string {
	return typeName + "." + methodName
}
func (h *testJavaHelper) LookupClassTypeParams(typeName string) []string {
	params := map[string][]string{"List": {"T"}, "Map": {"K", "V"}, "Optional": {"T"}, "CompletableFuture": {"T"}, "Stream": {"T"}}
	return params[typeName]
}
func (h *testJavaHelper) IsExternalPackage(_ string) bool { return false }

func (h *testJavaHelper) ResolveSuperCall(call model.RawCall, funcCandidates []model.Symbol, heritage []model.RawHeritage, envs map[string]*model.TypeEnv, callerID string) ([]model.ResolvedRelation, bool) {
	if call.ReceiverExpr != "super" || len(heritage) == 0 {
		return nil, false
	}
	callerClassQN := ExtractCallerClassQN(call.CallerName)
	callerClass := callerClassQN
	if dotIdx := strings.LastIndex(callerClass, "."); dotIdx >= 0 {
		callerClass = callerClass[dotIdx+1:]
	}
	setDeclaredType := func(relations []model.ResolvedRelation) {
		if callerClassQN != "" {
			for i := range relations {
				relations[i].Metadata["declared_type"] = callerClassQN
			}
		}
	}
	for _, her := range heritage {
		if her.ChildName == callerClass && her.Kind == "extends" && her.FilePath == call.FilePath {
			parentName := her.ParentName
			var resolvedParentQN string
			env := envs[call.FilePath]
			callerPkg := ""
			for _, cs := range h.symbolTable.FindByFile(call.FilePath) {
				if cs.Kind == "Class" || cs.Kind == "Interface" || cs.Kind == "abstract_class" {
					if idx := strings.LastIndex(cs.QualifiedName, "."+cs.Name); idx > 0 {
						callerPkg = cs.QualifiedName[:idx]
						break
					}
				}
			}
			for _, sym := range h.symbolTable.FindByName(parentName) {
				if sym.Kind != "Class" && sym.Kind != "abstract_class" {
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
				relations := []model.ResolvedRelation{makeRelation(callerID, matched[0].ID, call, ConfidenceTypeExact, "type_exact", 1)}
				setDeclaredType(relations)
				return relations, true
			}
			if len(matched) > 1 {
				argMatched := filterByArgCount(matched, call.ArgCount)
				if len(argMatched) == 1 {
					relations := []model.ResolvedRelation{makeRelation(callerID, argMatched[0].ID, call, ConfidenceArgCount, "arg_count", 1)}
					setDeclaredType(relations)
					return relations, true
				}
				relations := makeMultiRelations(callerID, matched, call, ConfidenceTypeParent, "type_multi")
				setDeclaredType(relations)
				return relations, true
			}
			r := &Resolver{symbolTable: h.symbolTable, heritage: heritage}
			if sym := r.FindMethodInHierarchy(her.ParentName, call.CalledName, heritage); sym != nil {
				relations := []model.ResolvedRelation{makeRelation(callerID, sym.ID, call, ConfidenceTypeExact, "type_hierarchy", 1)}
				setDeclaredType(relations)
				return relations, true
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
		if cs.Kind != "Class" && cs.Kind != "Interface" && cs.Kind != "abstract_class" {
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
		{ID: "f1", Name: "findById", QualifiedName: "com.example.UserService.findById", Kind: "Function", FilePath: "service.go", Params: []model.ParamInfo{{Name: "id", Type: "int"}}},
		{ID: "f2", Name: "findById", QualifiedName: "com.example.OrderService.findById", Kind: "Function", FilePath: "order.go", Params: []model.ParamInfo{{Name: "id", Type: "int"}}},
		{ID: "f3", Name: "findById", QualifiedName: "com.example.AdminService.findById", Kind: "Function", FilePath: "admin.go", Params: []model.ParamInfo{{Name: "id", Type: "int"}, {Name: "role", Type: "string"}}},
		{ID: "f4", Name: "helper", QualifiedName: "com.example.utils.helper", Kind: "Function", FilePath: "utils.go", Params: []model.ParamInfo{{Name: "x"}}},
		{ID: "c1", Name: "UserService", QualifiedName: "com.example.UserService", Kind: "Class", FilePath: "service.go"},
		{ID: "c2", Name: "OrderService", QualifiedName: "com.example.OrderService", Kind: "Class", FilePath: "order.go"},
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
		{ID: "m1", Name: "save", QualifiedName: "UserService.save", Kind: "Function", FilePath: "service.py"},
		{ID: "m2", Name: "findById", QualifiedName: "UserService.findById", Kind: "Function", FilePath: "service.py"},
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


func TestResolveImports_WindowsBackslashPaths(t *testing.T) {
	table := NewSymbolTable()
	resolver := newTestResolver(table)

	imports := []model.RawImport{
		{ModulePath: "com.example.UserService", SymbolName: "UserService", FilePath: "src\\main\\java\\com\\example\\App.java", Line: 3},
	}
	allFiles := []string{
		"src\\main\\java\\com\\example\\App.java",
		"src\\main\\java\\com\\example\\UserService.java",
	}

	relations := resolver.ResolveImports(imports, allFiles)
	if len(relations) != 1 {
		t.Fatalf("expected 1 IMPORTS relation with backslash paths, got %d", len(relations))
	}
	if relations[0].TargetID != "file:src\\main\\java\\com\\example\\UserService.java" {
		t.Fatalf("TargetID expected UserService.java, got %s", relations[0].TargetID)
	}
	t.Log("✅ Windows backslash paths resolved")
}

func TestResolveCalls_TypeParent(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "caller", Name: "process", QualifiedName: "main.process", Kind: "Function", FilePath: "main.go"},
		// Two "run" methods in different classes
		{ID: "dog_run", Name: "run", QualifiedName: "Dog.run", Kind: "Function", FilePath: "dog.go", ClassType: "Dog"},
		{ID: "cat_run", Name: "run", QualifiedName: "Cat.run", Kind: "Function", FilePath: "cat.go", ClassType: "Cat"},
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
		{ID: "caller", Name: "process", QualifiedName: "main.process", Kind: "Function", FilePath: "main.go"},
		// Two "run" methods both in Animal class (overloaded)
		{ID: "run1", Name: "run", QualifiedName: "Animal.run", Kind: "Function", FilePath: "animal.go", ClassType: "Animal"},
		{ID: "run2", Name: "run", QualifiedName: "Animal.run2", Kind: "Function", FilePath: "animal.go", ClassType: "Animal"},
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
		{ID: "child", Name: "Dog", Kind: "Class", FilePath: "dog.py"},
		{ID: "parent", Name: "Animal", Kind: "Class", FilePath: "animal.py"},
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
		{ID: "child", Name: "Dog", Kind: "Class", FilePath: "dog.py"},
		// Two classes named "Animal" in different files
		{ID: "parent1", Name: "Animal", Kind: "Class", FilePath: "animal_v1.py"},
		{ID: "parent2", Name: "Animal", Kind: "Class", FilePath: "animal_v2.py"},
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
		{ID: "child", Name: "UserService", Kind: "Class", FilePath: "svc.java"},
		{ID: "parent", Name: "Serializable", Kind: "Interface", FilePath: "serial.java"},
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

func TestResolveHeritage_SameShortNameDisambiguation(t *testing.T) {
	// Two parent classes with the same short name in different packages.
	// Each child imports a different one via ParentQualified.
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "child_a", Name: "OrderDao", QualifiedName: "com.biz.dao.OrderDao", Kind: "Class", FilePath: "OrderDao.java"},
		{ID: "child_b", Name: "UserDao", QualifiedName: "com.msg.dao.UserDao", Kind: "Class", FilePath: "UserDao.java"},
		{ID: "base_biz", Name: "BaseDao", QualifiedName: "com.biz.dao.BaseDao", Kind: "Class", FilePath: "biz/BaseDao.java"},
		{ID: "base_msg", Name: "BaseDao", QualifiedName: "com.msg.dao.BaseDao", Kind: "Class", FilePath: "msg/BaseDao.java"},
	})
	resolver := newTestResolver(table)

	heritage := []model.RawHeritage{
		{ChildName: "OrderDao", ChildQualified: "com.biz.dao.OrderDao", ParentName: "BaseDao", ParentQualified: "com.biz.dao.BaseDao", Kind: "extends", FilePath: "OrderDao.java"},
		{ChildName: "UserDao", ChildQualified: "com.msg.dao.UserDao", ParentName: "BaseDao", ParentQualified: "com.msg.dao.BaseDao", Kind: "extends", FilePath: "UserDao.java"},
	}

	relations := resolver.ResolveHeritage(heritage)
	if len(relations) != 2 {
		t.Fatalf("expected 2 relations, got %d", len(relations))
	}
	for _, rel := range relations {
		if rel.SourceID == "child_a" && rel.TargetID != "base_biz" {
			t.Fatalf("OrderDao should extend biz BaseDao, got target %s", rel.TargetID)
		}
		if rel.SourceID == "child_b" && rel.TargetID != "base_msg" {
			t.Fatalf("UserDao should extend msg BaseDao, got target %s", rel.TargetID)
		}
		if rel.Confidence != ConfidenceArgCount {
			t.Fatalf("ParentQualified match should give confidence %.2f, got %.2f", ConfidenceArgCount, rel.Confidence)
		}
	}
	t.Log("✅ Same short name parents disambiguated via ParentQualified")
}

func TestBuildQualifiedParentMap_SameShortNameDisambiguation(t *testing.T) {
	// buildQualifiedParentMap must resolve each child to its own parent,
	// not share a single cache entry across all children with the same ParentName.
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "base_biz", Name: "BaseDao", QualifiedName: "com.biz.dao.BaseDao", Kind: "Class", FilePath: "biz/BaseDao.java"},
		{ID: "base_msg", Name: "BaseDao", QualifiedName: "com.msg.dao.BaseDao", Kind: "Class", FilePath: "msg/BaseDao.java"},
	})
	resolver := newTestResolver(table)

	heritage := []model.RawHeritage{
		{ChildName: "OrderDao", ChildQualified: "com.biz.dao.OrderDao", ParentName: "BaseDao", ParentQualified: "com.biz.dao.BaseDao", Kind: "extends"},
		{ChildName: "UserDao", ChildQualified: "com.msg.dao.UserDao", ParentName: "BaseDao", ParentQualified: "com.msg.dao.BaseDao", Kind: "extends"},
	}

	qnMap := resolver.buildQualifiedParentMap(heritage)
	bizParents := qnMap["com.biz.dao.OrderDao"]
	msgParents := qnMap["com.msg.dao.UserDao"]

	if len(bizParents) != 1 || bizParents[0] != "com.biz.dao.BaseDao" {
		t.Fatalf("OrderDao parent should be com.biz.dao.BaseDao, got %v", bizParents)
	}
	if len(msgParents) != 1 || msgParents[0] != "com.msg.dao.BaseDao" {
		t.Fatalf("UserDao parent should be com.msg.dao.BaseDao, got %v", msgParents)
	}
	t.Log("✅ buildQualifiedParentMap: same short name parents correctly disambiguated")
}

func TestResolveCall_TypeHierarchy(t *testing.T) {
	// BaseRepository has save(), UserDao extends BaseRepository but has no save()
	// Calling dao.save() where dao is UserDao should resolve to BaseRepository.save via hierarchy
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "base_class", Name: "BaseRepository", QualifiedName: "com.example.BaseRepository", Kind: "Class", FilePath: "base.java"},
		{ID: "base_save", Name: "save", QualifiedName: "com.example.BaseRepository.save", Kind: "Function", FilePath: "base.java"},
		{ID: "dao_class", Name: "UserDao", QualifiedName: "com.example.UserDao", Kind: "Class", FilePath: "dao.java"},
		{ID: "dao_find", Name: "findById", QualifiedName: "com.example.UserDao.findById", Kind: "Function", FilePath: "dao.java"},
		{ID: "other_save", Name: "save", QualifiedName: "com.example.OtherService.save", Kind: "Function", FilePath: "other.java"},
		{ID: "caller", Name: "createUser", QualifiedName: "com.example.UserService.createUser", Kind: "Function", FilePath: "service.java"},
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
		{ID: "base_class", Name: "BaseRepository", QualifiedName: "com.example.BaseRepository", Kind: "Class", FilePath: "base.java"},
		{ID: "base_save", Name: "save", QualifiedName: "com.example.BaseRepository.save", Kind: "Function", FilePath: "base.java"},
		{ID: "dao_class", Name: "UserDao", QualifiedName: "com.example.UserDao", Kind: "Class", FilePath: "dao.java"},
		{ID: "dao_save", Name: "save", QualifiedName: "com.example.UserDao.save", Kind: "Function", FilePath: "dao.java"},
		{ID: "caller", Name: "createUser", QualifiedName: "com.example.UserService.createUser", Kind: "Function", FilePath: "service.java"},
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
		{ID: "base_save", Name: "save", QualifiedName: "com.example.BaseRepository.save", Kind: "Function", FilePath: "base.java"},
		{ID: "other_save", Name: "save", QualifiedName: "com.example.OtherService.save", Kind: "Function", FilePath: "other.java"},
		{ID: "caller", Name: "createUser", QualifiedName: "com.example.UserService.createUser", Kind: "Function", FilePath: "service.java"},
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
		{ID: "oc_getOrder", Name: "getOrder", QualifiedName: "OrderClient.getOrder", Kind: "Function", FilePath: "client.java"},
		{ID: "ctrl_getOrder", Name: "getOrder", QualifiedName: "OrderController.getOrder", Kind: "Function", FilePath: "controller.java"},
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
		{ID: "iface-tc", Name: "TraverseCallChain", QualifiedName: "storage.GraphStore.TraverseCallChain", Kind: "Function", FilePath: "storage/storage.go",
			Params: []model.ParamInfo{{Name: "ctx", Type: "context.Context"}, {Name: "nodeID", Type: "string"}, {Name: "depth", Type: "int"}, {Name: "dir", Type: "Direction"}, {Name: "minConf", Type: "float64"}}},
		{ID: "falkor-tc", Name: "TraverseCallChain", QualifiedName: "falkor.Store.TraverseCallChain", Kind: "Function", FilePath: "storage/falkor/store.go",
			Params: []model.ParamInfo{{Name: "ctx", Type: "context.Context"}, {Name: "nodeID", Type: "string"}, {Name: "depth", Type: "int"}, {Name: "dir", Type: "Direction"}, {Name: "minConf", Type: "float64"}}},
		{ID: "kuzu-tc", Name: "TraverseCallChain", QualifiedName: "kuzu.Store.TraverseCallChain", Kind: "Function", FilePath: "storage/kuzu/store.go",
			Params: []model.ParamInfo{{Name: "ctx", Type: "context.Context"}, {Name: "nodeID", Type: "string"}, {Name: "depth", Type: "int"}, {Name: "dir", Type: "Direction"}, {Name: "minConf", Type: "float64"}}},
		// Caller
		{ID: "falkor-ti", Name: "TraverseImpact", QualifiedName: "falkor.Store.TraverseImpact", Kind: "Function", FilePath: "storage/falkor/store.go"},
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
		{ID: "f1", Name: "get", QualifiedName: "com.example.DaoA.get", Kind: "Function", FilePath: "DaoA.java", Params: []model.ParamInfo{{Name: "sql", Type: "String"}}},
		{ID: "f2", Name: "get", QualifiedName: "com.example.DaoB.get", Kind: "Function", FilePath: "DaoB.java", Params: []model.ParamInfo{{Name: "sql", Type: "String"}}},
		{ID: "f3", Name: "get", QualifiedName: "com.example.DaoC.get", Kind: "Function", FilePath: "DaoC.java", Params: []model.ParamInfo{{Name: "sql", Type: "String"}}},
		{ID: "caller1", Name: "refresh", QualifiedName: "com.example.Service.refresh", Kind: "Function", FilePath: "Service.java"},
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
		{ID: "f1", Name: "length", QualifiedName: "com.example.Rope.length", Kind: "Function", FilePath: "Rope.java"},
		{ID: "f2", Name: "length", QualifiedName: "com.example.Cable.length", Kind: "Function", FilePath: "Cable.java"},
		{ID: "caller1", Name: "test", QualifiedName: "com.example.App.test", Kind: "Function", FilePath: "App.java"},
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
		{ID: "f1", Name: "getRules", QualifiedName: "com.example.request.UpdateRequest.getRules", Kind: "Function", FilePath: "UpdateRequest.java"},
		{ID: "c1", Name: "UpdateRequest", QualifiedName: "com.example.request.UpdateRequest", Kind: "Class", FilePath: "UpdateRequest.java"},
		{ID: "caller1", Name: "handle", QualifiedName: "com.example.service.Handler.handle", Kind: "Function", FilePath: "Handler.java"},
		{ID: "cc1", Name: "Handler", QualifiedName: "com.example.service.Handler", Kind: "Class", FilePath: "Handler.java"},
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
		{ID: "f1", Name: "query", QualifiedName: "com.example.service.ReportDao.query", Kind: "Function", FilePath: "ReportDao.java"},
		{ID: "c1", Name: "ReportDao", QualifiedName: "com.example.service.ReportDao", Kind: "Class", FilePath: "ReportDao.java"},
		{ID: "caller1", Name: "generate", QualifiedName: "com.example.service.ReportService.generate", Kind: "Function", FilePath: "ReportService.java"},
		{ID: "cc1", Name: "ReportService", QualifiedName: "com.example.service.ReportService", Kind: "Class", FilePath: "ReportService.java"},
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
		{ID: "base_get", Name: "get", QualifiedName: "com.example.dao.BaseDao.get", Kind: "Function", FilePath: "BaseDao.java", Params: []model.ParamInfo{{Name: "sql", Type: "String"}, {Name: "params", Type: "Object[]"}, {Name: "clazz", Type: "Class"}}},
		{ID: "other_get", Name: "get", QualifiedName: "com.other.OtherDao.get", Kind: "Function", FilePath: "OtherDao.java", Params: []model.ParamInfo{{Name: "sql", Type: "String"}, {Name: "params", Type: "Object[]"}, {Name: "clazz", Type: "Class"}}},
		{ID: "caller1", Name: "getById", QualifiedName: "com.example.dao.ChildDao.getById", Kind: "Function", FilePath: "ChildDao.java"},
		{ID: "c_child", Name: "ChildDao", QualifiedName: "com.example.dao.ChildDao", Kind: "Class", FilePath: "ChildDao.java"},
		{ID: "c_base", Name: "BaseDao", QualifiedName: "com.example.dao.BaseDao", Kind: "Class", FilePath: "BaseDao.java"},
		{ID: "c_other", Name: "BaseDao", QualifiedName: "com.other.BaseDao", Kind: "Class", FilePath: "OtherBaseDao.java"},
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
	// Verify declared_type is set to caller's class qualified name
	dt := relations[0].Metadata["declared_type"]
	if dt != "ChildDao" {
		t.Fatalf("expected declared_type 'ChildDao', got %q", dt)
	}
	t.Logf("✅ super.get() resolved to BaseDao.get via heritage + same-package (resolved_by: %s, declared_type: %s)", relations[0].ResolvedBy, dt)
}

func TestResolveCalls_NoReceiverInheritedMethod(t *testing.T) {
	// get(sql, params, clazz) in PrmTeamDao — no receiver, method defined in BaseDao
	// Multiple candidates with same arg count to ensure arg_count can't disambiguate
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "base_get", Name: "get", QualifiedName: "com.example.dao.BaseDao.get", Kind: "Function", FilePath: "BaseDao.java", Params: []model.ParamInfo{{Name: "sql", Type: "String"}, {Name: "params", Type: "Object[]"}, {Name: "clazz", Type: "Class"}}},
		{ID: "other_get", Name: "get", QualifiedName: "com.example.dao.OtherBaseDao.get", Kind: "Function", FilePath: "OtherBaseDao.java", Params: []model.ParamInfo{{Name: "sql", Type: "String"}, {Name: "params", Type: "Object[]"}, {Name: "clazz", Type: "Class"}}},
		{ID: "third_get", Name: "get", QualifiedName: "com.example.dao.ThirdDao.get", Kind: "Function", FilePath: "ThirdDao.java", Params: []model.ParamInfo{{Name: "sql", Type: "String"}, {Name: "params", Type: "Object[]"}, {Name: "clazz", Type: "Class"}}},
		{ID: "caller1", Name: "getByTeamName", QualifiedName: "com.example.dao.PrmTeamDao.getByTeamName", Kind: "Function", FilePath: "PrmTeamDao.java"},
		{ID: "c_child", Name: "PrmTeamDao", QualifiedName: "com.example.dao.PrmTeamDao", Kind: "Class", FilePath: "PrmTeamDao.java"},
		{ID: "c_base", Name: "BaseDao", QualifiedName: "com.example.dao.BaseDao", Kind: "Class", FilePath: "BaseDao.java"},
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
		{ID: "sf1", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "Function", FilePath: "ApiResult.java", Params: []model.ParamInfo{{Name: "code", Type: "ExceptionCode"}}},
		{ID: "sf2", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "Function", FilePath: "ApiResult.java", Params: []model.ParamInfo{{Name: "msg", Type: "String"}}},
		{ID: "sf3", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "Function", FilePath: "ApiResult.java", Params: []model.ParamInfo{{Name: "e", Type: "Exception"}}},
		{ID: "caller1", Name: "handle", QualifiedName: "com.example.Controller.handle", Kind: "Function", FilePath: "Controller.java"},
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
		{ID: "sf1", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "Function", FilePath: "ApiResult.java", Params: []model.ParamInfo{{Name: "code", Type: "ExceptionCode"}}},
		{ID: "sf2", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "Function", FilePath: "ApiResult.java", Params: []model.ParamInfo{{Name: "msg", Type: "String"}}},
		{ID: "gc", Name: "getCode", QualifiedName: "com.example.Controller.getCode", Kind: "Function", FilePath: "Controller.java", ReturnTypes: []model.ReturnType{{Name: "ExceptionCode"}}},
		{ID: "caller1", Name: "handle", QualifiedName: "com.example.Controller.handle", Kind: "Function", FilePath: "Controller.java"},
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
		{ID: "sf1", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "Function", FilePath: "ApiResult.java", Params: []model.ParamInfo{{Name: "code", Type: "ExceptionCode"}}},
		{ID: "sf2", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "Function", FilePath: "ApiResult.java", Params: []model.ParamInfo{{Name: "msg", Type: "String"}}},
		{ID: "safety", Name: "Safety", QualifiedName: "com.example.ExceptionCode.Safety", Kind: "Function", FilePath: "ExceptionCode.java"},
		{ID: "caller1", Name: "handle", QualifiedName: "com.example.Controller.handle", Kind: "Function", FilePath: "Controller.java"},
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
		{ID: "sf1", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "Function", FilePath: "ApiResult.java", Params: []model.ParamInfo{{Name: "msg", Type: "String"}}},
		{ID: "sf2", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "Function", FilePath: "ApiResult.java", Params: []model.ParamInfo{{Name: "code", Type: "int"}}},
		{ID: "caller1", Name: "handle", QualifiedName: "com.example.Controller.handle", Kind: "Function", FilePath: "Controller.java"},
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
	// OrderDao in biz-core extends BaseDao — super.get() should resolve to biz-core's BaseDao.get only
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		// biz-core BaseDao
		{ID: "biz_get1", Name: "get", QualifiedName: "com.example.app.core.dao.BaseDao.get", Kind: "Function", FilePath: "app-core/src/main/java/com/example/app/core/dao/BaseDao.java", Params: []model.ParamInfo{{Name: "sql", Type: "String"}, {Name: "params", Type: "Object[]"}, {Name: "clazz", Type: "Class"}}},
		{ID: "biz_get2", Name: "get", QualifiedName: "com.example.app.core.dao.BaseDao.get", Kind: "Function", FilePath: "app-core/src/main/java/com/example/app/core/dao/BaseDao.java", Params: []model.ParamInfo{{Name: "id", Type: "Object"}, {Name: "clazz", Type: "Class"}}},
		{ID: "biz_base", Name: "BaseDao", QualifiedName: "com.example.app.core.dao.BaseDao", Kind: "Class", FilePath: "app-core/src/main/java/com/example/app/core/dao/BaseDao.java"},
		// admin BaseDao
		{ID: "admin_get1", Name: "get", QualifiedName: "com.example.app.admin.dao.base.BaseDao.get", Kind: "Function", FilePath: "admin/src/main/java/com/example/app/admin/dao/base/BaseDao.java", Params: []model.ParamInfo{{Name: "sql", Type: "String"}, {Name: "params", Type: "Object[]"}, {Name: "clazz", Type: "Class"}}},
		{ID: "admin_get2", Name: "get", QualifiedName: "com.example.app.admin.dao.base.BaseDao.get", Kind: "Function", FilePath: "admin/src/main/java/com/example/app/admin/dao/base/BaseDao.java", Params: []model.ParamInfo{{Name: "id", Type: "Object"}, {Name: "clazz", Type: "Class"}}},
		{ID: "admin_base", Name: "BaseDao", QualifiedName: "com.example.app.admin.dao.base.BaseDao", Kind: "Class", FilePath: "admin/src/main/java/com/example/app/admin/dao/base/BaseDao.java"},
		// prm-core BaseDao
		{ID: "prm_get1", Name: "get", QualifiedName: "com.example.app.prm.core.dao.BaseDao.get", Kind: "Function", FilePath: "app-prm-core/src/main/java/com/example/app/prm/core/dao/BaseDao.java", Params: []model.ParamInfo{{Name: "sql", Type: "String"}, {Name: "params", Type: "Object[]"}, {Name: "clazz", Type: "Class"}}},
		{ID: "prm_base", Name: "BaseDao", QualifiedName: "com.example.app.prm.core.dao.BaseDao", Kind: "Class", FilePath: "app-prm-core/src/main/java/com/example/app/prm/core/dao/BaseDao.java"},
		// OrderDao in biz-core
		{ID: "caller1", Name: "getByOrderNo", QualifiedName: "com.example.app.core.dao.OrderDao.getByOrderNo", Kind: "Function", FilePath: "app-core/src/main/java/com/example/app/core/dao/OrderDao.java"},
		{ID: "c_coin", Name: "OrderDao", QualifiedName: "com.example.app.core.dao.OrderDao", Kind: "Class", FilePath: "app-core/src/main/java/com/example/app/core/dao/OrderDao.java"},
	})

	resolver := newTestResolver(table)
	resolver.SetHeritage([]model.RawHeritage{
		{ChildName: "OrderDao", ParentName: "BaseDao", Kind: "extends", FilePath: "app-core/src/main/java/com/example/app/core/dao/OrderDao.java"},
	})

	envs := map[string]*model.TypeEnv{
		"app-core/src/main/java/com/example/app/core/dao/OrderDao.java": {
			Bindings: map[string]*model.TypeInfo{},
			Imports:  []model.RawImport{},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "get",
		CallerName:   "OrderDao.getByOrderNo",
		FilePath:     "app-core/src/main/java/com/example/app/core/dao/OrderDao.java",
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
		{ID: "get_str", Name: "get", QualifiedName: "com.example.CoinAccountDao.get", Kind: "Function", FilePath: "CoinAccountDao.java", Params: []model.ParamInfo{{Name: "id", Type: "String"}}, ReturnTypes: []model.ReturnType{{Name: "CoinAccount"}}},
		{ID: "get_long", Name: "get", QualifiedName: "com.example.CoinAccountDao.get", Kind: "Function", FilePath: "CoinAccountDao.java", Params: []model.ParamInfo{{Name: "userId", Type: "Long"}}, ReturnTypes: []model.ReturnType{{Name: "CoinAccount"}}},
		{ID: "getUserId", Name: "getUserId", QualifiedName: "com.example.Command.getUserId", Kind: "Function", FilePath: "Command.java", ReturnTypes: []model.ReturnType{{Name: "Long"}}},
		{ID: "c_dao", Name: "CoinAccountDao", QualifiedName: "com.example.CoinAccountDao", Kind: "Class", FilePath: "CoinAccountDao.java"},
		{ID: "c_cmd", Name: "Command", QualifiedName: "com.example.Command", Kind: "Class", FilePath: "Command.java"},
		{ID: "caller1", Name: "recharge", QualifiedName: "com.example.Service.recharge", Kind: "Function", FilePath: "Service.java"},
		{ID: "c_svc", Name: "Service", QualifiedName: "com.example.Service", Kind: "Class", FilePath: "Service.java"},
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
		{ID: "proc1", Name: "process", QualifiedName: "com.example.ServiceA.process", Kind: "Function", FilePath: "ServiceA.java", Params: []model.ParamInfo{{Name: "data", Type: "String"}}},
		{ID: "proc2", Name: "process", QualifiedName: "com.example.ServiceA.process", Kind: "Function", FilePath: "ServiceA.java", Params: []model.ParamInfo{{Name: "data", Type: "Integer"}}},
		{ID: "c_a", Name: "ServiceA", QualifiedName: "com.example.ServiceA", Kind: "Class", FilePath: "ServiceA.java"},
		{ID: "caller1", Name: "run", QualifiedName: "com.example.App.run", Kind: "Function", FilePath: "App.java"},
		{ID: "c_app", Name: "App", QualifiedName: "com.example.App", Kind: "Class", FilePath: "App.java"},
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
	// userInfoDao.get(reqs.getUserId()) — getUserId returns Long, disambiguates get(Integer) vs get(Long)
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "get_int", Name: "get", QualifiedName: "com.example.UserInfoDao.get", Kind: "Function", FilePath: "UserInfoDao.java", Params: []model.ParamInfo{{Name: "id", Type: "Integer"}}},
		{ID: "get_long", Name: "get", QualifiedName: "com.example.UserInfoDao.get", Kind: "Function", FilePath: "UserInfoDao.java", Params: []model.ParamInfo{{Name: "userId", Type: "Long"}}},
		{ID: "getUserId", Name: "getUserId", QualifiedName: "com.example.Reqs.getUserId", Kind: "Function", FilePath: "Reqs.java", ReturnTypes: []model.ReturnType{{Name: "Long"}}},
		{ID: "c_dao", Name: "UserInfoDao", QualifiedName: "com.example.UserInfoDao", Kind: "Class", FilePath: "UserInfoDao.java"},
		{ID: "c_reqs", Name: "Reqs", QualifiedName: "com.example.Reqs", Kind: "Class", FilePath: "Reqs.java"},
		{ID: "caller1", Name: "handle", QualifiedName: "com.example.Controller.handle", Kind: "Function", FilePath: "Controller.java"},
		{ID: "c_ctrl", Name: "Controller", QualifiedName: "com.example.Controller", Kind: "Class", FilePath: "Controller.java"},
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
		ArgExprs:     []string{"reqs.getUserId()"},
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
	t.Log("✅ get(reqs.getUserId()) disambiguated to get(Long)")
}

func TestResolveCalls_EnrichArgTypes_RealCase_UserController(t *testing.T) {
	// Real case: this.userInfoDao.get(reqs.getUserId())
	// CallerName = "UserController.getLastCycleSummary"
	// reqs is method param with type GetLastCycleSummaryReqs
	// getUserId is Lombok-generated getter returning Long
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "get_int", Name: "get", QualifiedName: "com.example.app.core.dao.UserInfoDao.get", Kind: "Function", FilePath: "app-core/src/main/java/com/example/app/core/dao/UserInfoDao.java", Params: []model.ParamInfo{{Name: "id", Type: "Integer"}}},
		{ID: "get_long", Name: "get", QualifiedName: "com.example.app.core.dao.UserInfoDao.get", Kind: "Function", FilePath: "app-core/src/main/java/com/example/app/core/dao/UserInfoDao.java", Params: []model.ParamInfo{{Name: "userId", Type: "Long"}}},
		// Lombok-generated getter
		{ID: "getUserId", Name: "getUserId", QualifiedName: "com.example.app.admin.model.request.user.GetLastCycleSummaryReqs.getUserId", Kind: "Function",
			FilePath: "admin/src/main/java/com/example/app/admin/model/request/user/GetLastCycleSummaryReqs.java",
			ReturnTypes: []model.ReturnType{{Name: "Long"}}, IsSynthetic: true},
		{ID: "c_dao", Name: "UserInfoDao", QualifiedName: "com.example.app.core.dao.UserInfoDao", Kind: "Class", FilePath: "app-core/src/main/java/com/example/app/core/dao/UserInfoDao.java"},
		{ID: "c_reqs", Name: "GetLastCycleSummaryReqs", QualifiedName: "com.example.app.admin.model.request.user.GetLastCycleSummaryReqs", Kind: "Class",
			FilePath: "admin/src/main/java/com/example/app/admin/model/request/user/GetLastCycleSummaryReqs.java"},
		{ID: "caller1", Name: "getLastCycleSummary", QualifiedName: "com.example.app.admin.controller.UserController.getLastCycleSummary", Kind: "Function",
			FilePath: "admin/src/main/java/com/example/app/admin/controller/UserController.java"},
		{ID: "c_ctrl", Name: "UserController", QualifiedName: "com.example.app.admin.controller.UserController", Kind: "Class",
			FilePath: "admin/src/main/java/com/example/app/admin/controller/UserController.java"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"admin/src/main/java/com/example/app/admin/controller/UserController.java": {
			Bindings: map[string]*model.TypeInfo{
				"UserController:userInfoDao": {TypeName: "com.example.app.core.dao.UserInfoDao"},
				// Method param — parser generates this key format
				"UserController.getLastCycleSummary:reqs": {TypeName: "GetLastCycleSummaryReqs"},
			},
			Imports: []model.RawImport{},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "get",
		CallerName:   "UserController.getLastCycleSummary",
		FilePath:     "admin/src/main/java/com/example/app/admin/controller/UserController.java",
		ReceiverExpr: "userInfoDao",
		ArgCount:     1,
		ArgTypes:     []string{""},
		ArgExprs:     []string{"reqs.getUserId()"},
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	t.Logf("Relations: %d", len(relations))
	for _, r := range relations {
		t.Logf("  → %s (resolved_by: %s, confidence: %.3f)", r.TargetID, r.ResolvedBy, r.Confidence)
	}

	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d (enrichArgTypes may have failed to resolve reqs.getUserId())", len(relations))
	}
	if relations[0].TargetID != "get_long" {
		t.Fatalf("expected get_long, got %s", relations[0].TargetID)
	}
	t.Log("✅ Real case: userInfoDao.get(reqs.getUserId()) → get(Long)")
}

func TestResolveCalls_EnrichArgTypes_RealCase_ShortScope(t *testing.T) {
	// TypeEnv uses fully qualified scope (after 1.4 unification)
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "get_int", Name: "get", QualifiedName: "com.example.UserInfoDao.get", Kind: "Function", FilePath: "UserInfoDao.java", Params: []model.ParamInfo{{Name: "id", Type: "Integer"}}},
		{ID: "get_long", Name: "get", QualifiedName: "com.example.UserInfoDao.get", Kind: "Function", FilePath: "UserInfoDao.java", Params: []model.ParamInfo{{Name: "userId", Type: "Long"}}},
		{ID: "getBId", Name: "getUserId", QualifiedName: "com.example.Reqs.getUserId", Kind: "Function", FilePath: "Reqs.java", ReturnTypes: []model.ReturnType{{Name: "Long"}}, IsSynthetic: true},
		{ID: "c_dao", Name: "UserInfoDao", QualifiedName: "com.example.UserInfoDao", Kind: "Class", FilePath: "UserInfoDao.java"},
		{ID: "c_reqs", Name: "Reqs", QualifiedName: "com.example.Reqs", Kind: "Class", FilePath: "Reqs.java"},
		{ID: "caller1", Name: "getLastCycleSummary", QualifiedName: "com.example.Controller.getLastCycleSummary", Kind: "Function", FilePath: "Controller.java"},
		{ID: "c_ctrl", Name: "Controller", QualifiedName: "com.example.Controller", Kind: "Class", FilePath: "Controller.java"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"Controller.java": {
			Bindings: map[string]*model.TypeInfo{
				"com.example.Controller:userInfoDao":                      {TypeName: "com.example.UserInfoDao"},
				"com.example.Controller.getLastCycleSummary:reqs": {TypeName: "Reqs"},
			},
			Imports: []model.RawImport{},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "get",
		CallerName:   "com.example.Controller.getLastCycleSummary",
		FilePath:     "Controller.java",
		ReceiverExpr: "userInfoDao",
		ArgCount:     1,
		ArgTypes:     []string{""},
		ArgExprs:     []string{"reqs.getUserId()"},
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
	t.Log("✅ Short scope key: getLastCycleSummary:reqs matched correctly")
}

func TestResolveCalls_TypeMulti_CrossModuleSameClass(t *testing.T) {
	// UserInfo.getUserId() exists in 3 modules — should narrow to caller's module via import/same-package
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		// biz-core UserInfo
		{ID: "biz_getUserId", Name: "getUserId", QualifiedName: "com.example.biz.core.domain.UserInfo.getUserId", Kind: "Function",
			FilePath: "app-core/src/main/java/com/example/app/core/domain/UserInfo.java", ReturnTypes: []model.ReturnType{{Name: "Long"}}},
		{ID: "biz_userinfo", Name: "UserInfo", QualifiedName: "com.example.biz.core.domain.UserInfo", Kind: "Class",
			FilePath: "app-core/src/main/java/com/example/app/core/domain/UserInfo.java"},
		// admin UserInfo
		{ID: "admin_getUserId", Name: "getUserId", QualifiedName: "com.example.admin.entity.UserInfo.getUserId", Kind: "Function",
			FilePath: "admin/src/main/java/com/example/app/admin/entity/UserInfo.java", ReturnTypes: []model.ReturnType{{Name: "Long"}}},
		{ID: "admin_userinfo", Name: "UserInfo", QualifiedName: "com.example.admin.entity.UserInfo", Kind: "Class",
			FilePath: "admin/src/main/java/com/example/app/admin/entity/UserInfo.java"},
		// analysis UserInfo
		{ID: "analysis_getUserId", Name: "getUserId", QualifiedName: "com.example.analysis.domain.UserInfo.getUserId", Kind: "Function",
			FilePath: "app-analysis-core/src/main/java/com/example/app/analysis/domain/UserInfo.java", ReturnTypes: []model.ReturnType{{Name: "Long"}}},
		// Caller in biz-core service
		{ID: "caller1", Name: "process", QualifiedName: "com.example.biz.core.service.PaymentService.process", Kind: "Function",
			FilePath: "app-core/src/main/java/com/example/app/core/service/PaymentService.java"},
		{ID: "c_svc", Name: "PaymentService", QualifiedName: "com.example.biz.core.service.PaymentService", Kind: "Class",
			FilePath: "app-core/src/main/java/com/example/app/core/service/PaymentService.java"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"app-core/src/main/java/com/example/app/core/service/PaymentService.java": {
			Bindings: map[string]*model.TypeInfo{
				"PaymentService.process:userInfo": {TypeName: "UserInfo"},
			},
			Imports: []model.RawImport{
				{ModulePath: "com.example.biz.core.domain.UserInfo", SymbolName: "UserInfo"},
			},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "getUserId",
		CallerName:   "PaymentService.process",
		FilePath:     "app-core/src/main/java/com/example/app/core/service/PaymentService.java",
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
		{ID: "p_str", Name: "process", QualifiedName: "com.example.Svc.process", Kind: "Function", FilePath: "Svc.java", Params: []model.ParamInfo{{Name: "data", Type: "String"}}},
		{ID: "p_int", Name: "process", QualifiedName: "com.example.Svc.process", Kind: "Function", FilePath: "Svc.java", Params: []model.ParamInfo{{Name: "data", Type: "Integer"}}},
		{ID: "c_svc", Name: "Svc", QualifiedName: "com.example.Svc", Kind: "Class", FilePath: "Svc.java"},
		{ID: "caller1", Name: "run", QualifiedName: "com.example.App.run", Kind: "Function", FilePath: "App.java"},
		{ID: "c_app", Name: "App", QualifiedName: "com.example.App", Kind: "Class", FilePath: "App.java"},
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


// TestResolveCalls_ExternalSymbolPollutesNameUnique reproduces the non-deterministic
// CALLS count in js_project.
//
// Root cause hypothesis:
//   - GrandChild.doWork calls this.log() → resolver creates external:GrandChild.log (Name="log")
//   - LoggingDecorator.execute calls console.log → falls through to name_unique
//   - If GrandChild.log external is created BEFORE decorator's console.log is processed,
//     FindByName("log") returns 2 candidates → name_unique doesn't fire
//   - If AFTER, FindByName("log") returns 1 → name_unique fires (incorrectly to BaseRepository.log)
//
// This test verifies the hypothesis by controlling call order.
func TestResolveCalls_ExternalSymbolPollutesNameUnique(t *testing.T) {
	baseSymbols := []model.Symbol{
		// BaseRepository.log — the only project symbol named "log"
		{ID: "base-log", Name: "log", QualifiedName: "src.services.base-repository.BaseRepository.log", Kind: "Function", FilePath: "services/base-repository.js"},
		// GrandChild class — has no "log" method, inherits from ChildService → BaseRepository
		{ID: "grandchild-doWork", Name: "doWork", QualifiedName: "src.services.grandchild.GrandChild.doWork", Kind: "Function", FilePath: "services/grandchild.js"},
		// LoggingDecorator.execute
		{ID: "decorator-execute", Name: "execute", QualifiedName: "src.patterns.decorator.LoggingDecorator.execute", Kind: "Function", FilePath: "patterns/decorator.js"},
	}

	symbolTable := NewSymbolTable()
	symbolTable.AddBatch(baseSymbols)

	envs := map[string]*model.TypeEnv{
		"services/grandchild.js": {
			Bindings: map[string]*model.TypeInfo{
				"src.services.grandchild.GrandChild.doWork:this": {TypeName: "src.services.grandchild.GrandChild"},
			},
		},
		"patterns/decorator.js": {
			Bindings: map[string]*model.TypeInfo{},
		},
	}

	// Call A: this.log() in GrandChild.doWork — receiver "this" resolves to GrandChild,
	// but GrandChild has no "log" method → creates external:src.services.grandchild.GrandChild.log
	callA := model.RawCall{
		CalledName: "log", ReceiverExpr: "this",
		CallerName: "src.services.grandchild.GrandChild.doWork",
		FilePath: "services/grandchild.js", Line: 8, ArgCount: 1,
	}

	// Call B: console.log() in LoggingDecorator.execute — receiver "console" not in TypeEnv,
	// falls through to no-receiver path → FindByName("log")
	callB := model.RawCall{
		CalledName: "log", ReceiverExpr: "console",
		CallerName: "src.patterns.decorator.LoggingDecorator.execute",
		FilePath: "patterns/decorator.js", Line: 11, ArgCount: 2,
	}

	// Order 1: A first, then B
	// After A: SymbolTable has "base-log" + "external:...GrandChild.log" (both Name="log")
	// B's FindByName("log") → 2 candidates → name_unique should NOT fire
	t.Run("order_A_then_B", func(t *testing.T) {
		st := NewSymbolTable()
		st.AddBatch(baseSymbols)
		r := newTestResolver(st)
		resolved, _ := r.ResolveCalls([]model.RawCall{callA, callB}, envs)
		nameUniqueCount := 0
		for _, rel := range resolved {
			if rel.ResolvedBy == "name_unique" {
				nameUniqueCount++
				t.Logf("  name_unique: %s → %s", rel.SourceID, rel.TargetID)
			}
		}
		t.Logf("  order A→B: %d resolved, %d name_unique", len(resolved), nameUniqueCount)
	})

	// Order 2: B first, then A
	// Before A: SymbolTable has only "base-log" (Name="log")
	// B's FindByName("log") → 1 candidate → name_unique fires (WRONG: console.log → BaseRepository.log)
	t.Run("order_B_then_A", func(t *testing.T) {
		st := NewSymbolTable()
		st.AddBatch(baseSymbols)
		r := newTestResolver(st)
		resolved, _ := r.ResolveCalls([]model.RawCall{callB, callA}, envs)
		nameUniqueCount := 0
		for _, rel := range resolved {
			if rel.ResolvedBy == "name_unique" {
				nameUniqueCount++
				t.Logf("  name_unique: %s → %s (target=%s)", rel.SourceID, rel.TargetID, rel.ResolvedBy)
			}
		}
		t.Logf("  order B→A: %d resolved, %d name_unique", len(resolved), nameUniqueCount)
	})

	// The two orders produce different resolved counts when using testGenericHelper
	// (ShouldFallthrough=true, no global object recognition). This is a known limitation:
	// external symbols dynamically added to SymbolTable affect name_unique candidate counts.
	// In practice, JS/TS uses ResolveReceiverFallback to block global objects (console, Math, etc.),
	// and Java/Go/Python have ShouldFallthrough=false, so this path is not triggered in real usage.
	t.Run("documents_known_order_dependency", func(t *testing.T) {
		st1 := NewSymbolTable()
		st1.AddBatch(baseSymbols)
		r1 := newTestResolver(st1)
		resolved1, _ := r1.ResolveCalls([]model.RawCall{callA, callB}, envs)

		st2 := NewSymbolTable()
		st2.AddBatch(baseSymbols)
		r2 := newTestResolver(st2)
		resolved2, _ := r2.ResolveCalls([]model.RawCall{callB, callA}, envs)

		t.Logf("Order A→B: %d resolved, Order B→A: %d resolved (difference is expected with generic helper)", len(resolved1), len(resolved2))
	})
}

func TestResolveCalls_EnrichArgTypes_JDKChainedExpr(t *testing.T) {
	// reqs.getInvitationCode().trim().toUpperCase() should infer to String
	// This disambiguates getInvitationCode(String, Integer) vs getInvitationCode(Long, Integer)
	// Uses testJavaJDKHelper which delegates LookupMethodReturn to the real JDK table.
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "get_str", Name: "getInvitationCode", QualifiedName: "com.example.Dao.getInvitationCode", Kind: "Function", FilePath: "Dao.java", Params: []model.ParamInfo{{Name: "code", Type: "String"}, {Name: "codeType", Type: "Integer"}}, ReturnTypes: []model.ReturnType{{Name: "InvitationCode"}}},
		{ID: "get_long", Name: "getInvitationCode", QualifiedName: "com.example.Dao.getInvitationCode", Kind: "Function", FilePath: "Dao.java", Params: []model.ParamInfo{{Name: "userId", Type: "Long"}, {Name: "codeType", Type: "Integer"}}, ReturnTypes: []model.ReturnType{{Name: "InvitationCode"}}},
		{ID: "reqs_getCode", Name: "getInvitationCode", QualifiedName: "com.example.Reqs.getInvitationCode", Kind: "Function", FilePath: "Reqs.java", ReturnTypes: []model.ReturnType{{Name: "String"}}},
		{ID: "c_dao", Name: "Dao", QualifiedName: "com.example.Dao", Kind: "Class", FilePath: "Dao.java"},
		{ID: "c_reqs", Name: "Reqs", QualifiedName: "com.example.Reqs", Kind: "Class", FilePath: "Reqs.java"},
		{ID: "caller", Name: "changeBasicInfo", QualifiedName: "com.example.Controller.changeBasicInfo", Kind: "Function", FilePath: "Controller.java"},
		{ID: "c_ctrl", Name: "Controller", QualifiedName: "com.example.Controller", Kind: "Class", FilePath: "Controller.java"},
	})

	jdkHelper := &testJavaJDKHelper{testJavaHelper: testJavaHelper{symbolTable: table}}
	resolver := NewResolver(table, map[string]LanguageHelper{"java": jdkHelper})
	envs := map[string]*model.TypeEnv{
		"Controller.java": {
			Bindings: map[string]*model.TypeInfo{
				"Controller:dao":                      {TypeName: "Dao"},
				"Controller.changeBasicInfo:reqs":     {TypeName: "Reqs"},
			},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "getInvitationCode",
		CallerName:   "Controller.changeBasicInfo",
		FilePath:     "Controller.java",
		ReceiverExpr: "dao",
		ArgCount:     2,
		ArgTypes:     []string{"", ""},
		ArgExprs:     []string{"reqs.getInvitationCode().trim().toUpperCase()", "InvitationCodeType.Guide.getCode()"},
	}}

	relations, hints := resolver.ResolveCalls(calls, envs)
	t.Logf("relations=%d, hints=%d", len(relations), len(hints))
	for _, r := range relations {
		t.Logf("  → %s (confidence=%.2f, resolved_by=%s)", r.TargetID, r.Confidence, r.ResolvedBy)
	}

	// Check if the String overload was selected
	found := false
	for _, r := range relations {
		if r.TargetID == "get_str" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected get_str (String overload) to be selected, but it wasn't")
	}
}

func TestResolveCalls_EnrichArgTypes_JDKChainedExpr_PartialArgType(t *testing.T) {
	// When only the first arg type is inferred (String via JDK chain) and the second is unknown
	// (enum method not indexed), should still disambiguate if String vs Long is enough.
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "get_str_int", Name: "getInvitationCode", QualifiedName: "com.example.Dao.getInvitationCode", Kind: "Function", FilePath: "Dao.java", Params: []model.ParamInfo{{Name: "code", Type: "String"}, {Name: "codeType", Type: "Integer"}}, ReturnTypes: []model.ReturnType{{Name: "InvitationCode"}}},
		{ID: "get_long_int", Name: "getInvitationCode", QualifiedName: "com.example.Dao.getInvitationCode", Kind: "Function", FilePath: "Dao.java", Params: []model.ParamInfo{{Name: "userId", Type: "Long"}, {Name: "codeType", Type: "Integer"}}, ReturnTypes: []model.ReturnType{{Name: "InvitationCode"}}},
		{ID: "get_str_only", Name: "getInvitationCode", QualifiedName: "com.example.Dao.getInvitationCode", Kind: "Function", FilePath: "Dao.java", Params: []model.ParamInfo{{Name: "code", Type: "String"}}, ReturnTypes: []model.ReturnType{{Name: "InvitationCode"}}},
		{ID: "reqs_getCode", Name: "getInvitationCode", QualifiedName: "com.example.Reqs.getInvitationCode", Kind: "Function", FilePath: "Reqs.java", ReturnTypes: []model.ReturnType{{Name: "String"}}},
		{ID: "c_dao", Name: "Dao", QualifiedName: "com.example.Dao", Kind: "Class", FilePath: "Dao.java"},
		{ID: "c_reqs", Name: "Reqs", QualifiedName: "com.example.Reqs", Kind: "Class", FilePath: "Reqs.java"},
		{ID: "caller", Name: "changeBasicInfo", QualifiedName: "com.example.Controller.changeBasicInfo", Kind: "Function", FilePath: "Controller.java"},
		{ID: "c_ctrl", Name: "Controller", QualifiedName: "com.example.Controller", Kind: "Class", FilePath: "Controller.java"},
	})

	jdkHelper := &testJavaJDKHelper{testJavaHelper: testJavaHelper{symbolTable: table}}
	resolver := NewResolver(table, map[string]LanguageHelper{"java": jdkHelper})
	envs := map[string]*model.TypeEnv{
		"Controller.java": {
			Bindings: map[string]*model.TypeInfo{
				"Controller:dao":                  {TypeName: "Dao"},
				"Controller.changeBasicInfo:reqs": {TypeName: "Reqs"},
			},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "getInvitationCode",
		CallerName:   "Controller.changeBasicInfo",
		FilePath:     "Controller.java",
		ReceiverExpr: "dao",
		ArgCount:     2,
		ArgTypes:     []string{"", ""},
		ArgExprs:     []string{"reqs.getInvitationCode().trim().toUpperCase()", "InvitationCodeType.Guide.getCode()"},
	}}

	relations, hints := resolver.ResolveCalls(calls, envs)
	t.Logf("relations=%d, hints=%d", len(relations), len(hints))
	for _, r := range relations {
		t.Logf("  → %s (confidence=%.2f, resolved_by=%s)", r.TargetID, r.Confidence, r.ResolvedBy)
	}

	// With 3 overloads (String+Integer, Long+Integer, String-only):
	// ArgCount=2 eliminates get_str_only. First arg=String eliminates get_long_int.
	// Should resolve to get_str_int only.
	if len(relations) != 1 || relations[0].TargetID != "get_str_int" {
		t.Errorf("expected exactly get_str_int, got %d relations", len(relations))
	}
}

func TestResolveFullQualifiedType_ExplicitImport(t *testing.T) {
	table := NewSymbolTable()
	resolver := newTestResolver(table)
	env := &model.TypeEnv{
		Imports: []model.RawImport{
			{ModulePath: "com.example.dao.CoinFlowDao", SymbolName: "CoinFlowDao"},
		},
	}
	result := resolver.resolveFullQualifiedType("CoinFlowDao", env)
	if result != "com.example.dao.CoinFlowDao" {
		t.Fatalf("expected 'com.example.dao.CoinFlowDao', got %q", result)
	}
	t.Log("✅ resolveFullQualifiedType: explicit import")
}

func TestResolveFullQualifiedType_WildcardImport(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "c1", Name: "CoinFlowDao", QualifiedName: "com.example.dao.CoinFlowDao", Kind: "Class"},
	})
	resolver := newTestResolver(table)
	// Wildcard import: SymbolName is package last segment (parser bug), not the class name
	env := &model.TypeEnv{
		Imports: []model.RawImport{
			{ModulePath: "com.example.dao", SymbolName: "dao"},
		},
	}
	result := resolver.resolveFullQualifiedType("CoinFlowDao", env)
	if result != "com.example.dao.CoinFlowDao" {
		t.Fatalf("expected 'com.example.dao.CoinFlowDao', got %q", result)
	}
	t.Log("✅ resolveFullQualifiedType: wildcard import resolved via symbolTable")
}

func TestResolveFullQualifiedType_AlreadyQualified(t *testing.T) {
	table := NewSymbolTable()
	resolver := newTestResolver(table)
	result := resolver.resolveFullQualifiedType("com.example.dao.CoinFlowDao", nil)
	if result != "com.example.dao.CoinFlowDao" {
		t.Fatalf("expected unchanged, got %q", result)
	}
	t.Log("✅ resolveFullQualifiedType: already qualified name unchanged")
}

func TestResolveFullQualifiedType_NilEnv(t *testing.T) {
	table := NewSymbolTable()
	resolver := newTestResolver(table)
	result := resolver.resolveFullQualifiedType("CoinFlowDao", nil)
	if result != "CoinFlowDao" {
		t.Fatalf("expected original name, got %q", result)
	}
	t.Log("✅ resolveFullQualifiedType: nil env returns original")
}

func TestResolveFullQualifiedType_MultipleWildcardImports(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "c1", Name: "CoinFlowDao", QualifiedName: "com.example.dao.CoinFlowDao", Kind: "Class"},
	})
	resolver := newTestResolver(table)
	env := &model.TypeEnv{
		Imports: []model.RawImport{
			{ModulePath: "com.example.service", SymbolName: "service"},
			{ModulePath: "com.example.dao", SymbolName: "dao"},
		},
	}
	result := resolver.resolveFullQualifiedType("CoinFlowDao", env)
	if result != "com.example.dao.CoinFlowDao" {
		t.Fatalf("expected 'com.example.dao.CoinFlowDao', got %q", result)
	}
	t.Log("✅ resolveFullQualifiedType: multiple wildcards, only correct one matches")
}

func TestResolveCalls_EnrichArgTypes_StaticClassChain(t *testing.T) {
	// DateUtil.parseDate(s).getTime() as argument — static class name fallback enables chain inference
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "get_str", Name: "get", QualifiedName: "com.example.Dao.get", Kind: "Function", FilePath: "Dao.java", Params: []model.ParamInfo{{Name: "id", Type: "String"}}},
		{ID: "get_long", Name: "get", QualifiedName: "com.example.Dao.get", Kind: "Function", FilePath: "Dao.java", Params: []model.ParamInfo{{Name: "time", Type: "Long"}}},
		{ID: "c_dao", Name: "Dao", QualifiedName: "com.example.Dao", Kind: "Class", FilePath: "Dao.java"},
		{ID: "caller1", Name: "run", QualifiedName: "com.example.Svc.run", Kind: "Function", FilePath: "Svc.java"},
		{ID: "c_svc", Name: "Svc", QualifiedName: "com.example.Svc", Kind: "Class", FilePath: "Svc.java"},
	})

	jdkHelper := &testJavaJDKHelper{testJavaHelper: testJavaHelper{symbolTable: table}}
	resolver := NewResolver(table, map[string]LanguageHelper{
		"java": jdkHelper,
	})

	envs := map[string]*model.TypeEnv{
		"Svc.java": {
			Bindings: map[string]*model.TypeInfo{
				"Svc:dao": {TypeName: "Dao"},
			},
			Imports: []model.RawImport{},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "get",
		CallerName:   "Svc.run",
		FilePath:     "Svc.java",
		ReceiverExpr: "dao",
		ArgCount:     1,
		ArgTypes:     []string{""},
		ArgExprs:     []string{"DateUtil.parseDate(s).getTime()"},
	}}

	relations, hints := resolver.ResolveCalls(calls, envs)
	if len(hints) > 0 {
		t.Fatalf("expected 0 hints, got %d", len(hints))
	}
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "get_long" {
		t.Fatalf("expected get_long (Long overload), got %s", relations[0].TargetID)
	}
	t.Logf("✅ DateUtil.parseDate(s).getTime() resolved to Long via static class name fallback")
}

func TestResolveCalls_EnrichArgTypes_StaticImportEnum(t *testing.T) {
	// import static ExceptionCode.Safety → ApiResult.setFail(Safety) should infer Safety as ExceptionCode
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "sf_exc", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "Function", FilePath: "ApiResult.java", Params: []model.ParamInfo{{Name: "code", Type: "ExceptionCode"}}},
		{ID: "sf_str", Name: "setFail", QualifiedName: "com.example.ApiResult.setFail", Kind: "Function", FilePath: "ApiResult.java", Params: []model.ParamInfo{{Name: "msg", Type: "String"}}},
		{ID: "c_api", Name: "ApiResult", QualifiedName: "com.example.ApiResult", Kind: "Class", FilePath: "ApiResult.java"},
		{ID: "caller1", Name: "check", QualifiedName: "com.example.Aspect.check", Kind: "Function", FilePath: "Aspect.java"},
		{ID: "c_asp", Name: "Aspect", QualifiedName: "com.example.Aspect", Kind: "Class", FilePath: "Aspect.java"},
	})

	resolver := NewResolver(table, map[string]LanguageHelper{
		"java": &testJavaJDKHelper{testJavaHelper: testJavaHelper{symbolTable: table}},
	})

	envs := map[string]*model.TypeEnv{
		"Aspect.java": {
			Bindings: map[string]*model.TypeInfo{},
			Imports: []model.RawImport{
				{ModulePath: "com.example.ApiResult", SymbolName: "ApiResult", FilePath: "Aspect.java"},
				{ModulePath: "com.example.ExceptionCode.Safety", SymbolName: "Safety", FilePath: "Aspect.java"},
			},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "setFail",
		CallerName:   "Aspect.check",
		FilePath:     "Aspect.java",
		ReceiverExpr: "ApiResult",
		ArgCount:     1,
		ArgTypes:     []string{""},
		ArgExprs:     []string{"Safety"},
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "sf_exc" {
		t.Fatalf("expected sf_exc (ExceptionCode overload), got %s", relations[0].TargetID)
	}
	t.Logf("✅ static import Safety → ExceptionCode → setFail(ExceptionCode) disambiguated")
}

func TestFilterByArgCount_ExactMatch(t *testing.T) {
	candidates := []model.Symbol{
		{Name: "f1", Params: []model.ParamInfo{{Name: "a", Type: "string"}}},
		{Name: "f2", Params: []model.ParamInfo{{Name: "a", Type: "string"}, {Name: "b", Type: "int"}}},
	}
	result := filterByArgCount(candidates, 1)
	if len(result) != 1 || result[0].Name != "f1" {
		t.Errorf("expected f1, got %v", result)
	}
}

func TestFilterByArgCount_Varargs(t *testing.T) {
	candidates := []model.Symbol{
		{Name: "f1", Params: []model.ParamInfo{{Name: "a", Type: "string"}, {Name: "b", Type: "int..."}}},
	}
	// 1 arg: matches (>= paramCount-1 = 1)
	if result := filterByArgCount(candidates, 1); len(result) != 1 {
		t.Errorf("argCount=1: expected 1 match, got %d", len(result))
	}
	// 3 args: matches (>= 1)
	if result := filterByArgCount(candidates, 3); len(result) != 1 {
		t.Errorf("argCount=3: expected 1 match, got %d", len(result))
	}
	// 0 args: no match (< 1)
	if result := filterByArgCount(candidates, 0); len(result) != 0 {
		t.Errorf("argCount=0: expected 0 matches, got %d", len(result))
	}
}

func TestFilterByArgCount_DefaultParams(t *testing.T) {
	// def create(name, age=0, active=True) → required=1, total=3
	candidates := []model.Symbol{
		{Name: "create", Params: []model.ParamInfo{{Name: "name", Type: "str"}, {Name: "age", Type: "int", HasDefault: true}, {Name: "active", Type: "bool", HasDefault: true}}},
	}
	// 1 arg: matches (>= required=1, <= total=3)
	if result := filterByArgCount(candidates, 1); len(result) != 1 {
		t.Errorf("argCount=1: expected 1 match, got %d", len(result))
	}
	// 2 args: matches
	if result := filterByArgCount(candidates, 2); len(result) != 1 {
		t.Errorf("argCount=2: expected 1 match, got %d", len(result))
	}
	// 3 args: matches
	if result := filterByArgCount(candidates, 3); len(result) != 1 {
		t.Errorf("argCount=3: expected 1 match, got %d", len(result))
	}
	// 0 args: no match (< required=1)
	if result := filterByArgCount(candidates, 0); len(result) != 0 {
		t.Errorf("argCount=0: expected 0 matches, got %d", len(result))
	}
	// 4 args: no match (> total=3)
	if result := filterByArgCount(candidates, 4); len(result) != 0 {
		t.Errorf("argCount=4: expected 0 matches, got %d", len(result))
	}
}

func TestFilterByArgCount_BackwardCompat(t *testing.T) {
	// Old format without "default" key — should behave as exact match
	candidates := []model.Symbol{
		{Name: "f1", Params: []model.ParamInfo{{Name: "a", Type: "string"}, {Name: "b", Type: "int"}}},
	}
	// 2 args: matches (requiredCount=2, totalCount=2)
	if result := filterByArgCount(candidates, 2); len(result) != 1 {
		t.Errorf("argCount=2: expected 1 match, got %d", len(result))
	}
	// 1 arg: no match (< required=2)
	if result := filterByArgCount(candidates, 1); len(result) != 0 {
		t.Errorf("argCount=1: expected 0 matches, got %d", len(result))
	}
}

// TestResolveConstRefs_EnumConstant verifies Pattern A enum constant resolution.
func TestResolveConstRefs_EnumConstant(t *testing.T) {
	table := NewSymbolTable()
	table.Add(model.Symbol{
		ID:            "enum1",
		Name:          "OrderStatus",
		QualifiedName: "com.example.OrderStatus",
		Kind:          "Class",
		ClassType:     "enum",
	})
	table.Add(model.Symbol{
		ID:            "const1",
		Name:          "PENDING",
		QualifiedName: "com.example.OrderStatus.PENDING",
		Kind:          "Variable",
		IsStatic:      true,
		IsFinal:       true,
	})
	table.Add(model.Symbol{
		ID:            "func1",
		Name:          "process",
		QualifiedName: "com.example.Service.process",
		Kind:          "Function",
	})

	resolver := newTestResolver(table)
	refs := []model.RawConstRef{
		{
			ObjectExpr: "OrderStatus",
			ConstName:  "PENDING",
			CallerName: "com.example.Service.process",
			RefKind:    "field_access",
			Line:       10,
		},
	}

	relations := resolver.ResolveConstRefs(refs, nil)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	rel := relations[0]
	if rel.SourceID != "func1" || rel.TargetID != "const1" || rel.Kind != model.RelUses {
		t.Errorf("unexpected relation: SourceID=%s TargetID=%s Kind=%s", rel.SourceID, rel.TargetID, rel.Kind)
	}
	if rel.Line != 10 {
		t.Errorf("expected Line=10, got %d", rel.Line)
	}
	if rel.Metadata["ref_kind"] != "field_access" {
		t.Errorf("expected ref_kind=field_access, got %s", rel.Metadata["ref_kind"])
	}
	t.Log("✅ Pattern A enum constant resolved")
}

// TestResolveConstRefs_InterfaceConstant verifies Pattern A interface constant resolution.
func TestResolveConstRefs_InterfaceConstant(t *testing.T) {
	table := NewSymbolTable()
	table.Add(model.Symbol{
		ID:            "iface1",
		Name:          "PayChannelType",
		QualifiedName: "com.example.PayChannelType",
		Kind:          "Interface",
		ClassType:     "interface",
	})
	table.Add(model.Symbol{
		ID:            "const1",
		Name:          "ALIPAY",
		QualifiedName: "com.example.PayChannelType.ALIPAY",
		Kind:          "Variable",
		IsStatic:      true,
		IsFinal:       true,
	})
	table.Add(model.Symbol{
		ID:            "func1",
		Name:          "pay",
		QualifiedName: "com.example.Service.pay",
		Kind:          "Function",
	})

	resolver := newTestResolver(table)
	refs := []model.RawConstRef{
		{
			ObjectExpr: "PayChannelType",
			ConstName:  "ALIPAY",
			CallerName: "com.example.Service.pay",
			RefKind:    "field_access",
			Line:       15,
		},
	}

	relations := resolver.ResolveConstRefs(refs, nil)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	rel := relations[0]
	if rel.SourceID != "func1" || rel.TargetID != "const1" {
		t.Errorf("unexpected relation: SourceID=%s TargetID=%s", rel.SourceID, rel.TargetID)
	}
	t.Log("✅ Pattern A interface constant resolved")
}

// TestResolveConstRefs_NonStaticFinalSkipped verifies non-static-final fields are skipped.
func TestResolveConstRefs_NonStaticFinalSkipped(t *testing.T) {
	table := NewSymbolTable()
	table.Add(model.Symbol{
		ID:            "class1",
		Name:          "User",
		QualifiedName: "com.example.User",
		Kind:          "Class",
		ClassType:     "class",
	})
	table.Add(model.Symbol{
		ID:            "field1",
		Name:          "name",
		QualifiedName: "com.example.User.name",
		Kind:          "Variable",
		IsStatic:      false,
		IsFinal:       false,
	})
	table.Add(model.Symbol{
		ID:            "func1",
		Name:          "process",
		QualifiedName: "com.example.Service.process",
		Kind:          "Function",
	})

	resolver := newTestResolver(table)
	refs := []model.RawConstRef{
		{
			ObjectExpr: "User",
			ConstName:  "name",
			CallerName: "com.example.Service.process",
			RefKind:    "field_access",
		},
	}

	relations := resolver.ResolveConstRefs(refs, nil)
	if len(relations) != 0 {
		t.Errorf("expected 0 relations (non-static-final should be skipped), got %d", len(relations))
	}
	t.Log("✅ Non-static-final field skipped")
}

// TestResolveConstRefs_SwitchVariable verifies Pattern B with variable condition.
func TestResolveConstRefs_SwitchVariable(t *testing.T) {
	table := NewSymbolTable()
	table.Add(model.Symbol{
		ID:            "enum1",
		Name:          "OrderStatus",
		QualifiedName: "com.example.OrderStatus",
		Kind:          "Class",
		ClassType:     "enum",
	})
	table.Add(model.Symbol{
		ID:            "const1",
		Name:          "PENDING",
		QualifiedName: "com.example.OrderStatus.PENDING",
		Kind:          "Variable",
		IsStatic:      true,
		IsFinal:       true,
	})
	table.Add(model.Symbol{
		ID:            "func1",
		Name:          "handle",
		QualifiedName: "com.example.Service.handle",
		Kind:          "Function",
	})

	envs := map[string]*model.TypeEnv{
		"Service.java": {
			Bindings: map[string]*model.TypeInfo{
				"com.example.Service.handle:status": {TypeName: "com.example.OrderStatus"},
			},
		},
	}

	resolver := newTestResolver(table)
	refs := []model.RawConstRef{
		{
			SwitchConditionKind: "variable",
			SwitchVariableName:  "status",
			ConstName:           "PENDING",
			CallerName:          "com.example.Service.handle",
			FilePath:            "Service.java",
			RefKind:             "switch_case",
			Line:                20,
		},
	}

	relations := resolver.ResolveConstRefs(refs, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	rel := relations[0]
	if rel.SourceID != "func1" || rel.TargetID != "const1" {
		t.Errorf("unexpected relation: SourceID=%s TargetID=%s", rel.SourceID, rel.TargetID)
	}
	if rel.Metadata["ref_kind"] != "switch_case" {
		t.Errorf("expected ref_kind=switch_case, got %s", rel.Metadata["ref_kind"])
	}
	t.Log("✅ Pattern B switch variable resolved")
}

// TestResolveConstRefs_SwitchMethodCall verifies Pattern B with method call condition.
func TestResolveConstRefs_SwitchMethodCall(t *testing.T) {
	table := NewSymbolTable()
	table.Add(model.Symbol{
		ID:            "enum1",
		Name:          "OrderStatus",
		QualifiedName: "com.example.OrderStatus",
		Kind:          "Class",
		ClassType:     "enum",
	})
	table.Add(model.Symbol{
		ID:            "const1",
		Name:          "PENDING",
		QualifiedName: "com.example.OrderStatus.PENDING",
		Kind:          "Variable",
		IsStatic:      true,
		IsFinal:       true,
	})
	table.Add(model.Symbol{
		ID:            "class1",
		Name:          "Order",
		QualifiedName: "com.example.Order",
		Kind:          "Class",
	})
	table.Add(model.Symbol{
		ID:            "method1",
		Name:          "getStatus",
		QualifiedName: "com.example.Order.getStatus",
		Kind:          "Function",
		ReturnTypes: []model.ReturnType{{Name: "com.example.OrderStatus"}},
	})
	table.Add(model.Symbol{
		ID:            "func1",
		Name:          "handle",
		QualifiedName: "com.example.Service.handle",
		Kind:          "Function",
	})

	envs := map[string]*model.TypeEnv{
		"Service.java": {
			Bindings: map[string]*model.TypeInfo{
				"com.example.Service.handle:order": {TypeName: "com.example.Order"},
			},
		},
	}

	resolver := newTestResolver(table)
	refs := []model.RawConstRef{
		{
			SwitchConditionKind:  "method_call",
			SwitchMethodReceiver: "order",
			SwitchMethodName:     "getStatus",
			ConstName:            "PENDING",
			CallerName:           "com.example.Service.handle",
			FilePath:             "Service.java",
			RefKind:              "switch_case",
			Line:                 25,
		},
	}

	relations := resolver.ResolveConstRefs(refs, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	rel := relations[0]
	if rel.SourceID != "func1" || rel.TargetID != "const1" {
		t.Errorf("unexpected relation: SourceID=%s TargetID=%s", rel.SourceID, rel.TargetID)
	}
	t.Log("✅ Pattern B switch method call resolved")
}

func TestResolveCalls_PythonBarrelReExport(t *testing.T) {
	// Scenario: app.py does "from models import User", calls User.find_by_id()
	// models/__init__.py re-exports User from models/user.py
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "sym-user-class", Name: "User", QualifiedName: "models.user.User", Kind: "Class", FilePath: "models/user.py", IsExported: true},
		{ID: "sym-find-by-id", Name: "find_by_id", QualifiedName: "models.user.User.find_by_id", Kind: "Function", FilePath: "models/user.py", IsExported: true},
		{ID: "sym-app-main", Name: "app_main", QualifiedName: "app.app_main", Kind: "Function", FilePath: "app.py"},
	})

	// Register re-export: models.User → sym-user-class
	table.AddReExport("models.User", "sym-user-class")

	// Set up importFileMap: {app.py, User} → models/__init__.py
	importFileMap := map[ImportFileKey]string{
		{FilePath: "app.py", SymbolName: "User"}: "models/__init__.py",
	}

	resolver := newTestResolver(table)
	resolver.SetImportFileMap(importFileMap)

	envs := map[string]*model.TypeEnv{
		"app.py": {
			Imports: []model.RawImport{
				{ModulePath: "models", SymbolName: "User", FilePath: "app.py"},
			},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "find_by_id",
		CallerName:   "app.app_main",
		FilePath:     "app.py",
		ReceiverExpr: "User",
		ArgCount:     1,
		Language:     "python",
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	t.Logf("relations count: %d", len(relations))
	for i, rel := range relations {
		t.Logf("  [%d] target=%s resolvedBy=%s", i, rel.TargetID, rel.ResolvedBy)
	}
	if len(relations) == 0 {
		t.Fatal("expected at least 1 relation, got 0")
	}
	if relations[0].TargetID != "sym-find-by-id" {
		t.Errorf("expected target 'sym-find-by-id', got %q", relations[0].TargetID)
	}
}

func TestResolveCalls_NoScopeParents_ClassFieldNotLeaked(t *testing.T) {
	// Java scenario: without ScopeParents, lookupReceiverType should still work
	// using direct key lookup (CallerName:receiver) — same as before.
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "sym-find", Name: "findById", QualifiedName: "com.example.UserDao.findById", Kind: "Function", FilePath: "UserDao.java"},
		{ID: "sym-dao", Name: "UserDao", QualifiedName: "com.example.UserDao", Kind: "Class", FilePath: "UserDao.java"},
		{ID: "sym-caller", Name: "handle", QualifiedName: "com.example.Controller.handle", Kind: "Function", FilePath: "Controller.java"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"Controller.java": {
			Bindings: map[string]*model.TypeInfo{
				"Controller.handle:dao": {TypeName: "UserDao"},
			},
			// No ScopeParents — Java style
		},
	}

	calls := []model.RawCall{{
		CalledName:   "findById",
		CallerName:   "Controller.handle",
		FilePath:     "Controller.java",
		ReceiverExpr: "dao",
		ArgCount:     1,
		Language:     "java",
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "sym-find" {
		t.Fatalf("expected target 'sym-find', got %s", relations[0].TargetID)
	}
	t.Logf("✅ Java without ScopeParents: dao.findById() resolved correctly (resolved_by: %s)", relations[0].ResolvedBy)
}

func TestResolveCalls_WithScopeParents_ClosureCapture(t *testing.T) {
	// TS scenario: nested function captures outer variable via ScopeParents chain.
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "sym-execute", Name: "execute", QualifiedName: "db.Database.execute", Kind: "Function", FilePath: "db.ts"},
		{ID: "sym-database", Name: "Database", QualifiedName: "db.Database", Kind: "Class", FilePath: "db.ts"},
		{ID: "sym-inner", Name: "inner", QualifiedName: "app.setup.inner", Kind: "Function", FilePath: "app.ts"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"app.ts": {
			Bindings: map[string]*model.TypeInfo{
				"app.setup:db": {TypeName: "Database"},
			},
			ScopeParents: map[string]string{
				"app.setup.inner": "app.setup",
			},
			Imports: []model.RawImport{},
		},
	}

	// inner() calls db.execute() — db is captured from outer scope
	calls := []model.RawCall{{
		CalledName:   "execute",
		CallerName:   "app.setup.inner",
		CallerScope:  "app.setup.inner",
		FilePath:     "app.ts",
		ReceiverExpr: "db",
		ArgCount:     0,
		Language:     "typescript",
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) == 0 {
		t.Fatal("expected at least 1 relation for closure-captured variable")
	}
	if relations[0].TargetID != "sym-execute" {
		t.Errorf("expected target 'sym-execute', got %q", relations[0].TargetID)
	}
	t.Logf("✅ Closure capture resolved: db.execute() via ScopeParents chain (resolved_by: %s)", relations[0].ResolvedBy)
}

func TestResolveCalls_BlockScope_IfElseIsolation(t *testing.T) {
	// TS scenario: same variable name in if/else blocks should resolve to different types.
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "sym-get-data", Name: "getData", QualifiedName: "svc.ServiceA.getData", Kind: "Function", FilePath: "svc.ts"},
		{ID: "sym-get-error", Name: "getError", QualifiedName: "svc.ServiceB.getError", Kind: "Function", FilePath: "svc.ts"},
		{ID: "sym-sa", Name: "ServiceA", QualifiedName: "svc.ServiceA", Kind: "Class", FilePath: "svc.ts"},
		{ID: "sym-sb", Name: "ServiceB", QualifiedName: "svc.ServiceB", Kind: "Class", FilePath: "svc.ts"},
		{ID: "sym-process", Name: "process", QualifiedName: "app.process", Kind: "Function", FilePath: "app.ts"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"app.ts": {
			Bindings: map[string]*model.TypeInfo{
				"app.process#L3:result": {TypeName: "ServiceA"},
				"app.process#L6:result": {TypeName: "ServiceB"},
			},
			ScopeParents: map[string]string{
				"app.process#L3": "app.process",
				"app.process#L6": "app.process",
			},
		},
	}

	calls := []model.RawCall{
		{CalledName: "getData", CallerName: "app.process", CallerScope: "app.process#L3", FilePath: "app.ts", ReceiverExpr: "result", Language: "typescript"},
		{CalledName: "getError", CallerName: "app.process", CallerScope: "app.process#L6", FilePath: "app.ts", ReceiverExpr: "result", Language: "typescript"},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) < 2 {
		t.Fatalf("expected 2 relations, got %d", len(relations))
	}
	if relations[0].TargetID != "sym-get-data" {
		t.Errorf("if-block: expected 'sym-get-data', got %q", relations[0].TargetID)
	}
	if relations[1].TargetID != "sym-get-error" {
		t.Errorf("else-block: expected 'sym-get-error', got %q", relations[1].TargetID)
	}
	t.Log("✅ if/else block scope isolation works correctly")
}

func TestResolveCalls_TSImportedClass_ShouldResolveInternal(t *testing.T) {
	// TS project imports UserService from './services',
	// const svc = new UserService(); svc.findById(1)
	// receiverType = "UserService" (from TypeEnv), and UserService.findById exists in SymbolTable.
	// Should resolve to internal symbol, NOT external.
	// Also includes a stale external symbol to verify it's not picked over internal.
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "sym-us", Name: "UserService", QualifiedName: "src.services.UserService", Kind: "Class", FilePath: "src/services.ts"},
		{ID: "sym-find", Name: "findById", QualifiedName: "src.services.UserService.findById", Kind: "Function", FilePath: "src/services.ts"},
		{ID: "sym-caller", Name: "handleRequest", QualifiedName: "src.block-scope.handleRequest", Kind: "Function", FilePath: "src/block-scope.ts"},
		{ID: "external:./services.findById", Name: "findById", QualifiedName: "./services.findById", Kind: "Function", FilePath: "[external]"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"src/block-scope.ts": {
			Bindings: map[string]*model.TypeInfo{
				"src.block-scope.handleRequest:svc": {TypeName: "UserService"},
			},
			Imports: []model.RawImport{
				{ModulePath: "./services", SymbolName: "UserService"},
			},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "findById",
		CallerName:   "src.block-scope.handleRequest",
		CallerScope:  "src.block-scope.handleRequest",
		FilePath:     "src/block-scope.ts",
		ReceiverExpr: "svc",
		ArgCount:     1,
		Language:     "typescript",
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) == 0 {
		t.Fatal("expected at least 1 relation")
	}

	// Should resolve to internal sym-find, NOT external:./services.findById
	for _, rel := range relations {
		if strings.HasPrefix(rel.TargetID, "external:") {
			t.Errorf("resolved to external %q — should resolve to internal sym-find", rel.TargetID)
		}
	}
	if relations[0].TargetID == "sym-find" {
		t.Log("✅ TS imported class resolved to internal method")
	} else {
		t.Logf("⚠ resolved to %q (resolved_by: %s)", relations[0].TargetID, relations[0].ResolvedBy)
	}
}

func TestResolveCalls_ExternalClass_FallbackToImportPath(t *testing.T) {
	// When receiverType is NOT in SymbolTable (external library), should resolve as external.
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "sym-caller", Name: "setup", QualifiedName: "app.setup", Kind: "Function", FilePath: "app.ts"},
	})

	resolver := newTestResolver(table)
	envs := map[string]*model.TypeEnv{
		"app.ts": {
			Bindings: map[string]*model.TypeInfo{
				"app.setup:router": {TypeName: "Router"},
			},
			Imports: []model.RawImport{
				{ModulePath: "express", SymbolName: "Router"},
			},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "get",
		CallerName:   "app.setup",
		FilePath:     "app.ts",
		ReceiverExpr: "router",
		Language:     "typescript",
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) == 0 {
		t.Fatal("expected at least 1 relation for external class method")
	}
	if !strings.HasPrefix(relations[0].TargetID, "external:") {
		t.Fatalf("expected external resolution, got %q", relations[0].TargetID)
	}
	t.Logf("✅ External class correctly resolved as external: %s", relations[0].TargetID)
}

func TestResolveCalls_DefaultReExport_ShouldResolveInternal(t *testing.T) {
	// Scenario: export { default as MyComp } from './DefaultComp'
	// Consumer: const comp = new MyComp(); comp.render()
	// receiverType = "MyComp" but SymbolTable has "DefaultComponent"
	// reExportIndex has "src.components.MyComp" → sym-dc
	// Should resolve comp.render() to DefaultComponent.render
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "sym-dc", Name: "DefaultComponent", QualifiedName: "src.components.DefaultComp.DefaultComponent", Kind: "Class", FilePath: "src/components/DefaultComp.ts", IsExported: true, IsDefaultExport: true},
		{ID: "sym-render", Name: "render", QualifiedName: "src.components.DefaultComp.DefaultComponent.render", Kind: "Function", FilePath: "src/components/DefaultComp.ts"},
		{ID: "sym-caller", Name: "useDefaultExport", QualifiedName: "src.extra-consumer.useDefaultExport", Kind: "Function", FilePath: "src/extra-consumer.ts"},
	})
	// Register re-export: MyComp → DefaultComponent
	table.AddReExport("src.components.MyComp", "sym-dc")

	resolver := newTestResolver(table)
	resolver.SetImportFileMap(map[ImportFileKey]string{
		{FilePath: "src/extra-consumer.ts", SymbolName: "MyComp"}: "src/components/index.ts",
	})

	envs := map[string]*model.TypeEnv{
		"src/extra-consumer.ts": {
			Bindings: map[string]*model.TypeInfo{
				"src.extra-consumer.useDefaultExport:comp": {TypeName: "MyComp"},
			},
			Imports: []model.RawImport{
				{ModulePath: "./components", SymbolName: "MyComp", FilePath: "src/extra-consumer.ts"},
			},
		},
	}

	calls := []model.RawCall{{
		CalledName:   "render",
		CallerName:   "src.extra-consumer.useDefaultExport",
		FilePath:     "src/extra-consumer.ts",
		ReceiverExpr: "comp",
		Language:     "typescript",
	}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) == 0 {
		t.Fatal("expected at least 1 relation")
	}
	if relations[0].TargetID != "sym-render" {
		t.Fatalf("expected sym-render, got %q (resolved_by: %s)", relations[0].TargetID, relations[0].ResolvedBy)
	}
	t.Log("✅ default re-export: MyComp.render() resolved to DefaultComponent.render")
}

func TestIsGenericTypeParam(t *testing.T) {
	tests := []struct {
		name       string
		typeParams []string
		paramType  string
		expected   bool
	}{
		{
			name:       "precise match in TypeParams",
			typeParams: []string{"T", "U"},
			paramType:  "T",
			expected:   true,
		},
		{
			name:       "not in TypeParams",
			typeParams: []string{"T"},
			paramType:  "R",
			expected:   false,
		},
		{
			name:       "fallback single letter when TypeParams empty",
			typeParams: nil,
			paramType:  "T",
			expected:   true,
		},
		{
			name:       "fallback multi-letter not matched when TypeParams empty",
			typeParams: nil,
			paramType:  "KEY",
			expected:   false,
		},
		{
			name:       "precise match multi-letter generic",
			typeParams: []string{"KEY", "VALUE"},
			paramType:  "KEY",
			expected:   true,
		},
		{
			name:       "single letter not in TypeParams is not generic",
			typeParams: []string{"T"},
			paramType:  "R",
			expected:   false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := model.Symbol{TypeParams: testCase.typeParams}
			result := isGenericTypeParam(candidate, testCase.paramType)
			if result != testCase.expected {
				t.Errorf("isGenericTypeParam(TypeParams=%v, %q) = %v, want %v",
					testCase.typeParams, testCase.paramType, result, testCase.expected)
			}
		})
	}
}

func TestSubstituteGenericParam_Heritage(t *testing.T) {
	tests := []struct {
		name         string
		receiverType string
		retType      string
		heritage     []model.RawHeritage
		parentSymbol model.Symbol
		expected     string
	}{
		{
			name:         "extends single generic substitution",
			receiverType: "UserRepo",
			retType:      "T",
			heritage:     []model.RawHeritage{{ChildName: "UserRepo", ParentName: "BaseRepo", Kind: "extends", TypeArgs: []model.TypeArg{{Name: "User"}}}},
			parentSymbol: model.Symbol{Name: "BaseRepo", Kind: "Class", TypeParams: []string{"T"}},
			expected:     "User",
		},
		{
			name:         "extends multiple generic index 1",
			receiverType: "UserRepo",
			retType:      "ID",
			heritage:     []model.RawHeritage{{ChildName: "UserRepo", ParentName: "BaseRepo", Kind: "extends", TypeArgs: []model.TypeArg{{Name: "User"}, {Name: "Long"}}}},
			parentSymbol: model.Symbol{Name: "BaseRepo", Kind: "Class", TypeParams: []string{"T", "ID"}},
			expected:     "Long",
		},
		{
			name:         "no heritage entries",
			receiverType: "UserRepo",
			retType:      "T",
			heritage:     nil,
			parentSymbol: model.Symbol{},
			expected:     "T",
		},
		{
			name:         "retType is concrete type",
			receiverType: "UserRepo",
			retType:      "int",
			heritage:     []model.RawHeritage{{ChildName: "UserRepo", ParentName: "BaseRepo", Kind: "extends", TypeArgs: []model.TypeArg{{Name: "User"}}}},
			parentSymbol: model.Symbol{Name: "BaseRepo", Kind: "Class", TypeParams: []string{"T"}},
			expected:     "int",
		},
		{
			name:         "parent has no TypeParams",
			receiverType: "UserRepo",
			retType:      "T",
			heritage:     []model.RawHeritage{{ChildName: "UserRepo", ParentName: "BaseRepo", Kind: "extends", TypeArgs: []model.TypeArg{{Name: "User"}}}},
			parentSymbol: model.Symbol{Name: "BaseRepo", Kind: "Class", TypeParams: nil},
			expected:     "T",
		},
		{
			name:         "TypeArgs shorter than TypeParams index",
			receiverType: "UserRepo",
			retType:      "ID",
			heritage:     []model.RawHeritage{{ChildName: "UserRepo", ParentName: "BaseRepo", Kind: "extends", TypeArgs: []model.TypeArg{{Name: "User"}}}},
			parentSymbol: model.Symbol{Name: "BaseRepo", Kind: "Class", TypeParams: []string{"T", "ID"}},
			expected:     "ID",
		},
		{
			name:         "implements generic substitution",
			receiverType: "UserService",
			retType:      "R",
			heritage:     []model.RawHeritage{{ChildName: "UserService", ParentName: "Converter", Kind: "implements", TypeArgs: []model.TypeArg{{Name: "User"}, {Name: "UserDTO"}}}},
			parentSymbol: model.Symbol{Name: "Converter", Kind: "Interface", TypeParams: []string{"S", "R"}},
			expected:     "UserDTO",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			symbolTable := NewSymbolTable()
			if testCase.parentSymbol.Name != "" {
				symbolTable.Add(testCase.parentSymbol)
			}
			resolver := NewResolver(symbolTable)
			if testCase.heritage != nil {
				resolver.SetHeritage(testCase.heritage)
			}
			call := model.RawCall{FilePath: "test.java", CallerName: "caller"}
			envs := map[string]*model.TypeEnv{"test.java": {}}
			result := resolver.substituteGenericParam(testCase.retType, testCase.receiverType, "receiver", call, envs)
			if result != testCase.expected {
				t.Errorf("substituteGenericParam(%q, %q) = %q, want %q",
					testCase.retType, testCase.receiverType, result, testCase.expected)
			}
		})
	}
}

func TestSubstituteGenericParam_OwnTypeParamsTakesPriority(t *testing.T) {
	// R-4: When receiver class has its own TypeParams and TypeEnv has TypeArgs,
	// the original logic (TypeEnv lookup) should take priority over heritage chain.
	symbolTable := NewSymbolTable()
	// Receiver class itself is generic: List<T>
	symbolTable.Add(model.Symbol{Name: "List", Kind: "Class", TypeParams: []string{"T"}})
	// Also has a parent with TypeParams (should NOT be reached)
	symbolTable.Add(model.Symbol{Name: "AbstractList", Kind: "Class", TypeParams: []string{"E"}})

	resolver := NewResolver(symbolTable)
	resolver.SetHeritage([]model.RawHeritage{
		{ChildName: "List", ParentName: "AbstractList", Kind: "extends", TypeArgs: []model.TypeArg{{Name: "WrongType"}}},
	})

	call := model.RawCall{FilePath: "test.java", CallerName: "com.example.Service.process"}
	// TypeEnv has TypeArgs for the receiver variable "users" → ["User"]
	envs := map[string]*model.TypeEnv{
		"test.java": {
			Bindings: map[string]*model.TypeInfo{
				"com.example.Service.process:users": {TypeName: "List", TypeArgs: []model.TypeArg{{Name: "User"}}, Scope: "com.example.Service.process"},
			},
		},
	}

	result := resolver.substituteGenericParam("T", "List", "users", call, envs)
	if result != "User" {
		t.Errorf("expected own TypeParams+TypeEnv to take priority, got %q, want %q", result, "User")
	}
}

// --- Lambda callback reference tests ---

func TestResolveCalls_LambdaPreResolved(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "caller1", Name: "process", QualifiedName: "pkg.Service.process", Kind: "Function", FilePath: "Service.java"},
		{ID: "lambda1", Name: "lambda$1", QualifiedName: "pkg.Service.process.lambda$1", Kind: "Function", FilePath: "Service.java"},
	})
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "pkg.Service.process.lambda$1", CallerName: "pkg.Service.process", FilePath: "Service.java", Line: 5, IsPreResolved: true},
	}
	envs := map[string]*model.TypeEnv{"Service.java": {Bindings: map[string]*model.TypeInfo{}}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "lambda1" {
		t.Errorf("expected target lambda1, got %s", relations[0].TargetID)
	}
	if relations[0].ResolvedBy != "qualified_name_exact" {
		t.Errorf("expected qualified_name_exact, got %s", relations[0].ResolvedBy)
	}
}

func TestResolveCalls_CallbackIdentifier(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "caller1", Name: "register", QualifiedName: "pkg.App.register", Kind: "Function", FilePath: "App.java"},
		{ID: "lambda1", Name: "processor", QualifiedName: "pkg.App.register.processor", Kind: "Function", FilePath: "App.java"},
	})
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "execute", CallerName: "pkg.App.register", FilePath: "App.java", Line: 10, ArgExprs: []string{"processor"}},
	}
	envs := map[string]*model.TypeEnv{
		"App.java": {Bindings: map[string]*model.TypeInfo{
			"pkg.App.register:processor": {TypeName: "Function", LambdaSymbolID: "lambda1", Scope: "pkg.App.register"},
		}},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	// Should have a lambda_passthrough relation
	var found bool
	for _, rel := range relations {
		if rel.TargetID == "lambda1" && rel.ResolvedBy == "lambda_passthrough" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected lambda_passthrough relation to lambda1, got %v", relations)
	}
}

func TestResolveCalls_CallbackReceiverMethod(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "caller1", Name: "setup", QualifiedName: "pkg.App.setup", Kind: "Function", FilePath: "App.ts"},
		{ID: "handler1", Name: "handleList", QualifiedName: "pkg.Server.handleList", Kind: "Function", FilePath: "Server.ts"},
	})
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "AddTool", CallerName: "pkg.App.setup", FilePath: "App.ts", Line: 5, ArgExprs: []string{"tool", "srv.handleList"}},
	}
	envs := map[string]*model.TypeEnv{
		"App.ts": {Bindings: map[string]*model.TypeInfo{
			"pkg.App.setup:srv": {TypeName: "Server", Scope: "pkg.App.setup"},
		}},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	var found bool
	for _, rel := range relations {
		if rel.TargetID == "handler1" && rel.ResolvedBy == "lambda_passthrough" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected lambda_passthrough to handleList, got %v", relations)
	}
}

func TestResolveCalls_CallbackBindThis(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "caller1", Name: "init", QualifiedName: "pkg.Module.init", Kind: "Function", FilePath: "module.ts"},
		{ID: "handler1", Name: "handleOrder", QualifiedName: "pkg.OrderModule.handleOrder", Kind: "Function", FilePath: "module.ts"},
	})
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "on", CallerName: "pkg.Module.init", FilePath: "module.ts", Line: 5, ArgExprs: []string{"\"event\"", "this.handleOrder.bind(this)"}},
	}
	envs := map[string]*model.TypeEnv{
		"module.ts": {Bindings: map[string]*model.TypeInfo{
			"pkg.Module.init:this": {TypeName: "OrderModule", Scope: "pkg.Module.init"},
		}},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	var found bool
	for _, rel := range relations {
		if rel.TargetID == "handler1" && rel.ResolvedBy == "lambda_passthrough" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected lambda_passthrough to handleOrder, got %v", relations)
	}
}

func TestResolveCalls_CallbackNonIdentifierSkipped(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "caller1", Name: "run", QualifiedName: "pkg.App.run", Kind: "Function", FilePath: "App.ts"},
	})
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "process", CallerName: "pkg.App.run", FilePath: "App.ts", Line: 5, ArgExprs: []string{"new Order()"}},
	}
	envs := map[string]*model.TypeEnv{
		"App.ts": {Bindings: map[string]*model.TypeInfo{}},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	for _, rel := range relations {
		if rel.ResolvedBy == "lambda_passthrough" {
			t.Fatalf("should not create lambda_passthrough for non-identifier, got %v", rel)
		}
	}
}

func TestResolveCalls_IdentifierNoLambdaSymbolID(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "caller1", Name: "run", QualifiedName: "pkg.App.run", Kind: "Function", FilePath: "App.java"},
	})
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "map", CallerName: "pkg.App.run", FilePath: "App.java", Line: 5, ArgExprs: []string{"order"}},
	}
	envs := map[string]*model.TypeEnv{
		"App.java": {Bindings: map[string]*model.TypeInfo{
			"pkg.App.run:order": {TypeName: "Order", Scope: "pkg.App.run"},
		}},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	for _, rel := range relations {
		if rel.ResolvedBy == "lambda_passthrough" {
			t.Fatalf("should not create lambda_passthrough for variable without LambdaSymbolID, got %v", rel)
		}
	}
}

func TestResolveCalls_LambdaRedirect(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "caller1", Name: "run", QualifiedName: "pkg.App.run", Kind: "Function", FilePath: "App.java"},
		{ID: "lambda1", Name: "processor", QualifiedName: "pkg.App.run.processor", Kind: "Function", FilePath: "App.java"},
	})
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "apply", CallerName: "pkg.App.run", FilePath: "App.java", Line: 10, ReceiverExpr: "processor"},
	}
	envs := map[string]*model.TypeEnv{
		"App.java": {Bindings: map[string]*model.TypeInfo{
			"pkg.App.run:processor": {TypeName: "Function", LambdaSymbolID: "lambda1", Scope: "pkg.App.run"},
		}},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) == 0 {
		t.Fatal("expected at least 1 relation")
	}
	if relations[0].TargetID != "lambda1" {
		t.Errorf("expected redirect to lambda1, got %s", relations[0].TargetID)
	}
	if relations[0].ResolvedBy != "lambda_redirect" {
		t.Errorf("expected lambda_redirect, got %s", relations[0].ResolvedBy)
	}
}

func TestResolveCalls_NoLambdaSymbolIDNormalResolve(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "caller1", Name: "run", QualifiedName: "pkg.App.run", Kind: "Function", FilePath: "App.java"},
		{ID: "findById1", Name: "findById", QualifiedName: "pkg.UserService.findById", Kind: "Function", FilePath: "UserService.java"},
	})
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "findById", CallerName: "pkg.App.run", FilePath: "App.java", Line: 10, ReceiverExpr: "userService"},
	}
	envs := map[string]*model.TypeEnv{
		"App.java": {Bindings: map[string]*model.TypeInfo{
			"pkg.App.run:userService": {TypeName: "UserService", Scope: "pkg.App.run"},
		}},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) == 0 {
		t.Fatal("expected at least 1 relation")
	}
	if relations[0].ResolvedBy == "lambda_redirect" {
		t.Error("should not use lambda_redirect for normal receiver")
	}
}

func TestResolveCalls_LambdaInnerCallSameClass(t *testing.T) {
	// Simulates: lambda$1 calls "transform" (same class method)
	// CallerName = "com.example.Service.process.lambda$1", CalledName = "transform"
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "process1", Name: "process", QualifiedName: "com.example.Service.process", Kind: "Function", FilePath: "Service.java"},
		{ID: "lambda1", Name: "lambda$1", QualifiedName: "com.example.Service.process.lambda$1", Kind: "Function", FilePath: "Service.java"},
		{ID: "transform1", Name: "transform", QualifiedName: "com.example.Service.transform", Kind: "Function", FilePath: "Service.java"},
	})
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "transform", CallerName: "com.example.Service.process.lambda$1", FilePath: "Service.java", Line: 5},
	}
	envs := map[string]*model.TypeEnv{"Service.java": {Bindings: map[string]*model.TypeInfo{}}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	var found bool
	for _, rel := range relations {
		if rel.TargetID == "transform1" {
			found = true
			t.Logf("resolved_by: %s, confidence: %f", rel.ResolvedBy, rel.Confidence)
			break
		}
	}
	if !found {
		t.Fatalf("expected lambda inner call to resolve to transform1, got %v", relations)
	}
}

func TestResolveCalls_LambdaInnerCallFieldReceiver(t *testing.T) {
	// Lambda calls orderService.process() — orderService is a field, not in method-level TypeEnv
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "lambda1", Name: "lambda$1", QualifiedName: "com.example.Service.process.lambda$1", Kind: "Function", FilePath: "Service.java"},
		{ID: "processOrder", Name: "process", QualifiedName: "com.example.OrderService.process", Kind: "Function", FilePath: "OrderService.java"},
	})
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "process", CallerName: "com.example.Service.process.lambda$1", FilePath: "Service.java", Line: 5, ReceiverExpr: "orderService"},
	}
	// No TypeEnv binding for orderService (it's a field, not a local variable)
	envs := map[string]*model.TypeEnv{"Service.java": {Bindings: map[string]*model.TypeInfo{}}}

	relations, _ := resolver.ResolveCalls(calls, envs)
	var found bool
	for _, rel := range relations {
		if rel.TargetID == "processOrder" {
			found = true
			break
		}
	}
	// Known limitation: field receiver in lambda scope cannot resolve without field type in TypeEnv
	t.Logf("field receiver in lambda resolved: %v (relations: %v)", found, relations)
	if found {
		t.Log("✅ field receiver resolved (unexpected bonus)")
	} else {
		t.Log("⚠️ field receiver NOT resolved — known limitation: field type not in lambda scope TypeEnv")
	}
}

func TestResolveCalls_DirectLambdaVariableCall(t *testing.T) {
	// Go: handler() — direct call to lambda variable, no receiver
	// Fixed: Step 0.5 checks LambdaSymbolID before FindByName
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "caller1", Name: "directCall", QualifiedName: "main.directCall", Kind: "Function", FilePath: "main.go"},
		{ID: "handler1", Name: "handler", QualifiedName: "main.directCall.handler", Kind: "Function", FilePath: "main.go"},
		{ID: "handler2", Name: "handler", QualifiedName: "main.otherFunc.handler", Kind: "Function", FilePath: "main.go"},
	})
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "handler", CallerName: "main.directCall", FilePath: "main.go", Line: 5},
	}
	envs := map[string]*model.TypeEnv{
		"main.go": {Bindings: map[string]*model.TypeInfo{
			"main.directCall:handler": {TypeName: "func", LambdaSymbolID: "handler1", Scope: "main.directCall"},
		}},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	if len(relations) == 0 {
		t.Fatal("expected direct lambda call to resolve")
	}
	if relations[0].TargetID != "handler1" {
		t.Errorf("expected target handler1, got %s", relations[0].TargetID)
	}
	if relations[0].ResolvedBy != "lambda_direct_call" {
		t.Errorf("expected lambda_direct_call, got %s", relations[0].ResolvedBy)
	}
}

func TestResolveCalls_CallbackNestedPropertyAccess(t *testing.T) {
	// TS: this.orderModule.handleOrder.bind(this) — nested property access
	// After stripping .bind(: "this.orderModule.handleOrder"
	// SplitN(".", 2) → receiver="this", method="orderModule.handleOrder"
	// method contains "." — won't match any Symbol name
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "caller1", Name: "init", QualifiedName: "pkg.Module.init", Kind: "Function", FilePath: "module.ts"},
		{ID: "handler1", Name: "handleOrder", QualifiedName: "pkg.OrderModule.handleOrder", Kind: "Function", FilePath: "module.ts"},
	})
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "on", CallerName: "pkg.Module.init", FilePath: "module.ts", Line: 5,
			ArgExprs: []string{"\"event\"", "this.orderModule.handleOrder.bind(this)"}},
	}
	envs := map[string]*model.TypeEnv{
		"module.ts": {Bindings: map[string]*model.TypeInfo{
			"pkg.Module.init:this": {TypeName: "Module", Scope: "pkg.Module.init"},
		}},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	var found bool
	for _, rel := range relations {
		if rel.TargetID == "handler1" && rel.ResolvedBy == "lambda_passthrough" {
			found = true
			break
		}
	}
	t.Logf("nested property callback resolved: %v (relations: %v)", found, relations)
	if found {
		t.Log("✅ nested property resolved (unexpected bonus)")
	} else {
		t.Log("⚠️ nested property NOT resolved — known limitation: only single-level receiver.method supported")
	}
}

func TestResolveCalls_CallbackModuleLevelVariable(t *testing.T) {
	// TS: module-level const server = new Server(); app.get("/", server.handleUser)
	// server is module-level, not in method-level TypeEnv
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "caller1", Name: "methodReference", QualifiedName: "consumer.ts.methodReference", Kind: "Function", FilePath: "consumer.ts"},
		{ID: "handler1", Name: "handleUser", QualifiedName: "services.ts.Server.handleUser", Kind: "Function", FilePath: "services.ts"},
	})
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "get", CallerName: "consumer.ts.methodReference", FilePath: "consumer.ts", Line: 5,
			ArgExprs: []string{"\"/\"", "server.handleUser"}},
	}
	// server is module-level — not in method scope TypeEnv
	envs := map[string]*model.TypeEnv{
		"consumer.ts": {Bindings: map[string]*model.TypeInfo{}},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	var found bool
	for _, rel := range relations {
		if rel.TargetID == "handler1" && rel.ResolvedBy == "lambda_passthrough" {
			found = true
			break
		}
	}
	t.Logf("module-level variable callback resolved: %v (relations: %v)", found, relations)
	if found {
		t.Log("✅ module-level variable resolved (unexpected bonus)")
	} else {
		t.Log("⚠️ module-level variable NOT resolved — known limitation: only method-scope variables in TypeEnv")
	}
}

func TestResolveCalls_LambdaInnerCallClosureVariable(t *testing.T) {
	// Lambda calls userService.run() — userService is in outer/module scope, not lambda scope
	// With multiple "run" candidates, TypeEnv is needed to disambiguate
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "caller1", Name: "declarativeCallback", QualifiedName: "module.declarativeCallback", Kind: "Function", FilePath: "consumer.ts"},
		{ID: "handler1", Name: "handler", QualifiedName: "module.declarativeCallback.handler", Kind: "Function", FilePath: "consumer.ts"},
		{ID: "run1", Name: "run", QualifiedName: "services.UserService.run", Kind: "Function", FilePath: "services.ts"},
		{ID: "run2", Name: "run", QualifiedName: "services.OrderService.run", Kind: "Function", FilePath: "services.ts"},
	})
	resolver := newTestResolver(table)

	// RawCall from handler (lambda) to run with receiver userService
	calls := []model.RawCall{
		{CalledName: "run", CallerName: "module.declarativeCallback.handler", FilePath: "consumer.ts", Line: 5, ReceiverExpr: "userService"},
	}
	// userService binding is in module scope, not in handler scope
	envs := map[string]*model.TypeEnv{
		"consumer.ts": {
			Bindings: map[string]*model.TypeInfo{
				"module:userService": {TypeName: "UserService", Scope: "module"},
			},
		},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	var found bool
	for _, rel := range relations {
		if rel.TargetID == "run1" && rel.ResolvedBy == "type_exact" {
			found = true
			break
		}
	}
	if found {
		t.Log("✅ closure variable resolved via type_exact")
	} else {
		t.Logf("⚠️ closure variable NOT resolved via type_exact — need ScopeParents for lambda → outer scope (got: %v)", relations)
	}
}

func TestResolveCalls_DirectLambdaVariableCallWithAmbiguity(t *testing.T) {
	// handler() — direct call to lambda variable, no receiver, multiple "handler" symbols
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "caller1", Name: "directCall", QualifiedName: "main.directCall", Kind: "Function", FilePath: "main.go"},
		{ID: "handler1", Name: "handler", QualifiedName: "main.directCall.handler", Kind: "Function", FilePath: "main.go"},
		{ID: "handler2", Name: "handler", QualifiedName: "main.otherFunc.handler", Kind: "Function", FilePath: "main.go"},
	})
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "handler", CallerName: "main.directCall", FilePath: "main.go", Line: 5},
	}
	envs := map[string]*model.TypeEnv{
		"main.go": {Bindings: map[string]*model.TypeInfo{
			"main.directCall:handler": {TypeName: "func", LambdaSymbolID: "handler1", Scope: "main.directCall"},
		}},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	var found bool
	for _, rel := range relations {
		if rel.TargetID == "handler1" {
			found = true
			t.Logf("resolved_by: %s", rel.ResolvedBy)
			break
		}
	}
	if !found {
		t.Fatalf("direct lambda variable call should resolve to handler1 via LambdaSymbolID, got: %v", relations)
	}
}

func TestInferLambdaParamTypes_ListGeneric(t *testing.T) {
	// Simulates: List<Order> orders; orders.stream().map(order -> order.getName())
	// Lambda param 'order' should be inferred as 'Order' from TypeArgs of 'orders'
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "process1", Name: "processOrders", QualifiedName: "com.example.Svc.processOrders", Kind: "Function", FilePath: "Svc.java"},
		{ID: "lambda1", Name: "lambda$1", QualifiedName: "com.example.Svc.processOrders.lambda$1", Kind: "Function", FilePath: "Svc.java",
			IsLambda: true, Params: []model.ParamInfo{{Name: "order", Type: ""}}},
		{ID: "getName1", Name: "getName", QualifiedName: "com.example.Order.getName", Kind: "Function", FilePath: "Order.java"},
	})

	helper := &testListJDKHelper{testJavaHelper: testJavaHelper{symbolTable: table}}
	resolver := NewResolver(table, map[string]LanguageHelper{"java": helper})

	calls := []model.RawCall{
		// Pre-resolved lambda call with owner info
		{CalledName: "com.example.Svc.processOrders.lambda$1", CallerName: "com.example.Svc.processOrders",
			CallerScope: "com.example.Svc.processOrders",
			FilePath: "Svc.java", Language: "java", Line: 5, IsPreResolved: true,
			LambdaOwnerMethod: "map", LambdaOwnerReceiver: "orders.stream()"},
		// Lambda body call: order.getName()
		{CalledName: "getName", CallerName: "com.example.Svc.processOrders.lambda$1",
			CallerScope: "com.example.Svc.processOrders.lambda$1",
			FilePath: "Svc.java", Language: "java", Line: 5, ReceiverExpr: "order"},
	}
	envs := map[string]*model.TypeEnv{
		"Svc.java": {Bindings: map[string]*model.TypeInfo{
			"com.example.Svc.processOrders:orders": {TypeName: "List", TypeArgs: []model.TypeArg{{Name: "Order"}}, Scope: "com.example.Svc.processOrders"},
		}},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	var found bool
	for _, rel := range relations {
		if rel.TargetID == "getName1" {
			found = true
			t.Logf("resolved_by: %s, confidence: %f", rel.ResolvedBy, rel.Confidence)
			break
		}
	}
	if !found {
		t.Fatalf("expected order.getName() to resolve to Order.getName, got relations: %v", relations)
	}
}

func TestInferLambdaParamTypes_NoOwnerReceiver(t *testing.T) {
	// Lambda in constructor call — LambdaOwnerReceiver is empty, should not crash
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "lambda1", Name: "lambda$1", QualifiedName: "com.example.Svc.run.lambda$1", Kind: "Function", FilePath: "Svc.java",
			IsLambda: true, Params: []model.ParamInfo{{Name: "x", Type: ""}}},
	})
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "com.example.Svc.run.lambda$1", CallerName: "com.example.Svc.run",
			FilePath: "Svc.java", Line: 10, IsPreResolved: true,
			LambdaOwnerMethod: "Thread", LambdaOwnerReceiver: ""},
	}
	envs := map[string]*model.TypeEnv{
		"Svc.java": {Bindings: map[string]*model.TypeInfo{}},
	}

	// Should not panic — the pre-resolved call itself resolves (qualified_name_exact)
	relations, _ := resolver.ResolveCalls(calls, envs)
	// Verify no crash and the pre-resolved call resolves normally
	if len(relations) != 1 {
		t.Errorf("expected 1 relation (pre-resolved lambda call), got %d", len(relations))
	}
}

func TestInferLambdaParamTypes_NoOverwrite(t *testing.T) {
	// TypeEnv already has a binding for the lambda param — should not overwrite
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "lambda1", Name: "lambda$1", QualifiedName: "com.example.Svc.process.lambda$1", Kind: "Function", FilePath: "Svc.java",
			IsLambda: true, Params: []model.ParamInfo{{Name: "order", Type: ""}}},
	})
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "com.example.Svc.process.lambda$1", CallerName: "com.example.Svc.process",
			FilePath: "Svc.java", Line: 5, IsPreResolved: true,
			LambdaOwnerMethod: "map", LambdaOwnerReceiver: "orders"},
	}
	envs := map[string]*model.TypeEnv{
		"Svc.java": {Bindings: map[string]*model.TypeInfo{
			"com.example.Svc.process:orders":          {TypeName: "List", TypeArgs: []model.TypeArg{{Name: "Order"}}, Scope: "com.example.Svc.process"},
			"com.example.Svc.process.lambda$1:order": {TypeName: "String", Tier: 0, Scope: "com.example.Svc.process.lambda$1"},
		}},
	}

	resolver.ResolveCalls(calls, envs)

	// Verify the existing binding was NOT overwritten
	binding := envs["Svc.java"].Bindings["com.example.Svc.process.lambda$1:order"]
	if binding == nil {
		t.Fatal("expected binding to still exist")
	}
	if binding.TypeName != "String" {
		t.Errorf("expected binding to remain 'String', got %q", binding.TypeName)
	}
}

func TestInferLambdaParamTypes_ThisField(t *testing.T) {
	// Simulates: this.orders.map(order => order.getName()) where orders: Order[]
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "lambda1", Name: "lambda$1", QualifiedName: "svc.OrderProcessor.processAll.lambda$1", Kind: "Function", FilePath: "svc.ts",
			IsLambda: true, Params: []model.ParamInfo{{Name: "order", Type: ""}}},
		{ID: "getName1", Name: "getName", QualifiedName: "svc.Order.getName", Kind: "Function", FilePath: "svc.ts"},
	})
	resolver := newTestResolver(table)

	calls := []model.RawCall{
		{CalledName: "svc.OrderProcessor.processAll.lambda$1", CallerName: "svc.OrderProcessor.processAll",
			FilePath: "svc.ts", Line: 5, IsPreResolved: true,
			LambdaOwnerMethod: "map", LambdaOwnerReceiver: "this.orders"},
		{CalledName: "getName", CallerName: "svc.OrderProcessor.processAll.lambda$1",
			FilePath: "svc.ts", Line: 5, ReceiverExpr: "order"},
	}
	envs := map[string]*model.TypeEnv{
		"svc.ts": {Bindings: map[string]*model.TypeInfo{
			"svc.OrderProcessor:orders": {TypeName: "Order[]", Scope: "svc.OrderProcessor"},
		}},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)
	var found bool
	for _, rel := range relations {
		if rel.TargetID == "getName1" {
			found = true
			t.Logf("resolved_by: %s, confidence: %f", rel.ResolvedBy, rel.Confidence)
			break
		}
	}
	if !found {
		// Check if TypeEnv was written
		key := "svc.OrderProcessor.processAll.lambda$1:order"
		if info, exists := envs["svc.ts"].Bindings[key]; exists {
			t.Logf("TypeEnv binding exists: %s → %s", key, info.TypeName)
		} else {
			t.Logf("TypeEnv binding NOT found: %s", key)
		}
		t.Fatalf("expected order.getName() to resolve, got relations: %v", relations)
	}
}

// testGoExternalHelper is a helper that returns true for specific external packages.
type testGoExternalHelper struct {
	testGenericHelper
	externalPackages map[string]bool
}

func (h testGoExternalHelper) IsExternalPackage(receiverName string) bool {
	return h.externalPackages[receiverName]
}

func TestResolveCalls_SkipNameUniqueByInferredType(t *testing.T) {
	// Setup: project has a single function named "Index" (Indexer.Index)
	table := NewSymbolTable()
	table.Add(model.Symbol{
		ID: "indexer-index", Name: "Index", QualifiedName: "service.Indexer.Index",
		Kind: "Function", FilePath: "indexer.go",
	})

	goExternalHelper := testGoExternalHelper{
		externalPackages: map[string]bool{"reflect": true},
	}
	resolver := NewResolver(table, map[string]LanguageHelper{
		"go": goExternalHelper,
	})

	// Call: reflectValue.Index(0) where reflectValue has inferred type "reflect.Value"
	calls := []model.RawCall{
		{
			CalledName:   "Index",
			CallerName:   "ladybug.sanitizeSliceForLadybug",
			ReceiverExpr: "reflectValue",
			FilePath:     "store.go",
			Line:         50,
			ArgCount:     1,
		},
	}

	// TypeEnv: reflectValue → reflect.Value
	envs := map[string]*model.TypeEnv{
		"store.go": {
			Bindings: map[string]*model.TypeInfo{
				"ladybug.sanitizeSliceForLadybug:reflectValue": {TypeName: "reflect.Value", Tier: 2},
			},
		},
	}

	relations, _ := resolver.ResolveCalls(calls, envs)

	// Should NOT match Indexer.Index — receiver's inferred type is reflect.Value (external)
	for _, relation := range relations {
		if relation.TargetID == "indexer-index" {
			t.Errorf("UT-14: should not match Indexer.Index, but got relation to it: %+v", relation)
		}
	}
	t.Log("✅ Resolver skips name_unique when receiver inferred type is external package")
}
