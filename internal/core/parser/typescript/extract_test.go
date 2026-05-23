package typescript

import (
	"strings"
	"testing"
	"unsafe"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func parse(code []byte) (*tree_sitter.Node, func()) {
	parser := tree_sitter.NewParser()
	lang := tree_sitter.NewLanguage(unsafe.Pointer(tree_sitter_typescript.LanguageTypescript()))
	parser.SetLanguage(lang)
	tree := parser.Parse(code, nil)
	return tree.RootNode(), func() { tree.Close(); parser.Close() }
}

func TestExtract_ClassAndMethods(t *testing.T) {
	code := []byte(`export class UserService {
    findById(id: number): User | null { return null; }
    save(user: User): void {}
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "service.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/service.ts", RelPath: "service.ts", Language: "typescript"}
	Extract(root, code, file, result)

	names := map[string]string{}
	for _, sym := range result.Symbols {
		names[sym.Name] = sym.Kind
	}

	if names["UserService"] != "Class" {
		t.Fatalf("UserService expected class, got %s", names["UserService"])
	}
	if names["findById"] != "Function" {
		t.Fatalf("findById expected function, got %s", names["findById"])
	}
	t.Logf("✅ TS Extract: class + methods, %d symbols", len(result.Symbols))
}

func TestExtract_InterfaceAndInheritance(t *testing.T) {
	code := []byte(`interface Animal {
    speak(): string;
}

class Dog implements Animal {
    speak(): string { return "woof"; }
}

class Puppy extends Dog {
    speak(): string { return "yip"; }
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "animals.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/animals.ts", RelPath: "animals.ts", Language: "typescript"}
	Extract(root, code, file, result)

	hasExtends, hasImplements := false, false
	for _, h := range result.Heritage {
		if h.Kind == "extends" && h.ChildName == "Puppy" && h.ParentName == "Dog" {
			hasExtends = true
		}
		if h.Kind == "implements" && h.ChildName == "Dog" && h.ParentName == "Animal" {
			hasImplements = true
		}
	}
	if !hasExtends {
		t.Fatal("missing Puppy extends Dog")
	}
	if !hasImplements {
		t.Fatal("missing Dog implements Animal")
	}
	t.Log("✅ TS Extract: interface + extends + implements")
}

func TestExtract_ArrowFunction(t *testing.T) {
	code := []byte(`const handler = (req: Request, res: Response) => {
    res.json({ok: true});
};

export const helper = () => {};
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "routes.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/routes.ts", RelPath: "routes.ts", Language: "typescript"}
	Extract(root, code, file, result)

	names := map[string]bool{}
	for _, sym := range result.Symbols {
		names[sym.Name] = true
	}
	if !names["handler"] {
		t.Fatal("missing arrow function 'handler'")
	}
	if !names["helper"] {
		t.Fatal("missing arrow function 'helper'")
	}
	t.Log("✅ TS Extract: arrow functions as symbols")
}

func TestTSFlowContext(t *testing.T) {
	code := []byte(`
function process() {
    validate();
    if (true) {
        save();
    } else {
        logError();
    }
    for (const x of items) {
        update(x);
    }
}
`)
	root, cleanup := parse(code)
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "service.ts", Language: "typescript"}
	Extract(root, code, file, result)

	flowMap := make(map[string]string)
	for _, call := range result.Calls {
		flowMap[call.CalledName] = call.FlowContext
	}

	if flowMap["validate"] != "" {
		t.Errorf("validate: expected empty, got %q", flowMap["validate"])
	}
	if flowMap["save"] != "if" {
		t.Errorf("save: expected if, got %q", flowMap["save"])
	}
	if flowMap["logError"] != "else" {
		t.Errorf("logError: expected else, got %q", flowMap["logError"])
	}
	if flowMap["update"] != "loop" {
		t.Errorf("update: expected loop, got %q", flowMap["update"])
	}
	t.Log("✅ TypeScript FlowContext: if/else/loop")
}

func TestTSPendingAssignment(t *testing.T) {
	code := []byte(`
function process() {
    const user = getUser();
    const addr = user.getAddress();
    const name = addr.name;
    const alias = user;
}
`)
	root, cleanup := parse(code)
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "service.ts", Language: "typescript"}
	Extract(root, code, file, result)

	kinds := make(map[string]string)
	for _, p := range result.PendingAssignments {
		kinds[p.LHS] = p.Kind
	}
	if kinds["user"] != "call_result" {
		t.Errorf("user: expected call_result, got %q", kinds["user"])
	}
	if kinds["addr"] != "method_call_result" {
		t.Errorf("addr: expected method_call_result, got %q", kinds["addr"])
	}
	if kinds["name"] != "field_access" {
		t.Errorf("name: expected field_access, got %q", kinds["name"])
	}
	if kinds["alias"] != "copy" {
		t.Errorf("alias: expected copy, got %q", kinds["alias"])
	}
	t.Log("✅ TypeScript PendingAssignment: 4 kinds extracted")
}

func TestExtract_JavaScriptClassHeritage(t *testing.T) {
	// JS AST has no extends_clause wrapper — class_heritage children are directly
	// "extends" keyword + identifier, unlike TS which wraps them in extends_clause.
	code := []byte(`class BaseRepository {
    save(data) { console.log('Saving:', data); }
    log(message) { console.log('[LOG]', message); }
}

class ChildService extends BaseRepository {
    validate(data) { if (!data) throw new Error('empty'); }
}

class GrandChild extends ChildService {
    doWork(data) {
        this.validate(data);
        this.save(data);
        this.log('done');
    }
}
`)
	// Parse as JavaScript
	jsParser := tree_sitter.NewParser()
	defer jsParser.Close()
	jsLang := tree_sitter.NewLanguage(unsafe.Pointer(tree_sitter_javascript.Language()))
	jsParser.SetLanguage(jsLang)
	jsTree := jsParser.Parse(code, nil)
	defer jsTree.Close()

	result := &model.ParseResult{FilePath: "services.js", Language: "javascript"}
	file := scanner.ScannedFile{Path: "/test/services.js", RelPath: "services.js", Language: "javascript"}
	Extract(jsTree.RootNode(), code, file, result)

	// Verify heritage extraction
	heritageMap := map[string]string{}
	for _, h := range result.Heritage {
		heritageMap[h.ChildName] = h.ParentName
	}

	if heritageMap["ChildService"] != "BaseRepository" {
		t.Errorf("expected ChildService extends BaseRepository, got heritage: %v", heritageMap)
	}
	if heritageMap["GrandChild"] != "ChildService" {
		t.Errorf("expected GrandChild extends ChildService, got heritage: %v", heritageMap)
	}
	t.Logf("✅ JS heritage: %v", heritageMap)
}

func TestExtract_JavaScriptGlobalObjectNotResolved(t *testing.T) {
	// This test belongs in resolver/typescript package — see TestJSGlobalObject there
	t.Skip("moved to resolver/typescript")
}

func TestExtract_TSAccessor(t *testing.T) {
	code := []byte(`export class User {
    private _name: string;

    get name(): string {
        return this._name;
    }

    set name(value: string) {
        this._name = value;
    }

    greet(): string {
        return "hello " + this._name;
    }
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "user.ts", Language: "typescript"}
	Extract(root, code, file, result)

	found := map[string]struct{ getter, setter bool }{}
	for _, sym := range result.Symbols {
		if sym.Kind != "Function" {
			continue
		}
		found[sym.Name] = struct{ getter, setter bool }{sym.IsGetter, sym.IsSetter}
	}

	if !found["name"].getter && !found["name"].setter {
		t.Fatal("TS 'name' accessor not detected at all")
	}
	if !found["greet"].getter == false && found["greet"].setter == false {
		// greet is a normal method, should not be accessor
	}
	if found["greet"].getter || found["greet"].setter {
		t.Error("greet is a normal method, should not be accessor")
	}
	t.Log("✅ TypeScript get/set accessor detection")
}

func TestExtract_NamedReexport(t *testing.T) {
	code := []byte(`export { User } from './models/User';`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "index.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/index.ts", RelPath: "index.ts", Language: "typescript"}
	Extract(root, code, file, result)

	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}
	imp := result.Imports[0]
	if !imp.IsReexport {
		t.Error("expected IsReexport=true")
	}
	if imp.SymbolName != "User" {
		t.Errorf("expected SymbolName='User', got %q", imp.SymbolName)
	}
	if imp.LocalName != "User" {
		t.Errorf("expected LocalName='User', got %q", imp.LocalName)
	}
	if imp.ModulePath != "./models/User" {
		t.Errorf("expected ModulePath='./models/User', got %q", imp.ModulePath)
	}
}

func TestExtract_RenameReexport(t *testing.T) {
	code := []byte(`export { InternalLogger as Logger } from './internal/logger';`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "api.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/api.ts", RelPath: "api.ts", Language: "typescript"}
	Extract(root, code, file, result)

	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}
	imp := result.Imports[0]
	if imp.SymbolName != "InternalLogger" {
		t.Errorf("expected SymbolName='InternalLogger', got %q", imp.SymbolName)
	}
	if imp.LocalName != "Logger" {
		t.Errorf("expected LocalName='Logger', got %q", imp.LocalName)
	}
	if !imp.IsReexport {
		t.Error("expected IsReexport=true")
	}
}

func TestExtract_DefaultReexport(t *testing.T) {
	code := []byte(`export { default as MyComponent } from './Component';`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "index.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/index.ts", RelPath: "index.ts", Language: "typescript"}
	Extract(root, code, file, result)

	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}
	imp := result.Imports[0]
	if imp.SymbolName != "default" {
		t.Errorf("expected SymbolName='default', got %q", imp.SymbolName)
	}
	if imp.LocalName != "MyComponent" {
		t.Errorf("expected LocalName='MyComponent', got %q", imp.LocalName)
	}
}

func TestExtract_WildcardReexport(t *testing.T) {
	code := []byte(`export * from './utils';`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "index.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/index.ts", RelPath: "index.ts", Language: "typescript"}
	Extract(root, code, file, result)

	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}
	imp := result.Imports[0]
	if !imp.IsReexport {
		t.Error("expected IsReexport=true")
	}
	if !imp.IsWildcard {
		t.Error("expected IsWildcard=true")
	}
	if imp.ModulePath != "./utils" {
		t.Errorf("expected ModulePath='./utils', got %q", imp.ModulePath)
	}
}

func TestExtract_MultipleSpecifiers(t *testing.T) {
	code := []byte(`export { A, B as C } from './module';`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "index.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/index.ts", RelPath: "index.ts", Language: "typescript"}
	Extract(root, code, file, result)

	if len(result.Imports) != 2 {
		t.Fatalf("expected 2 imports, got %d", len(result.Imports))
	}
	if result.Imports[0].SymbolName != "A" || result.Imports[0].LocalName != "A" {
		t.Errorf("first specifier: expected A/A, got %s/%s", result.Imports[0].SymbolName, result.Imports[0].LocalName)
	}
	if result.Imports[1].SymbolName != "B" || result.Imports[1].LocalName != "C" {
		t.Errorf("second specifier: expected B/C, got %s/%s", result.Imports[1].SymbolName, result.Imports[1].LocalName)
	}
}

func TestExtract_ExportDefaultClass(t *testing.T) {
	code := []byte(`export default class Foo { bar() {} }`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "foo.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/foo.ts", RelPath: "foo.ts", Language: "typescript"}
	Extract(root, code, file, result)

	var fooSymbol *model.Symbol
	for i := range result.Symbols {
		if result.Symbols[i].Name == "Foo" {
			fooSymbol = &result.Symbols[i]
			break
		}
	}
	if fooSymbol == nil {
		t.Fatal("expected symbol 'Foo'")
	}
	if !fooSymbol.IsDefaultExport {
		t.Error("expected IsDefaultExport=true for Foo")
	}
	if !fooSymbol.IsExported {
		t.Error("expected IsExported=true for Foo")
	}
}

func TestExtract_NormalExportNotReexport(t *testing.T) {
	code := []byte(`export class User { getName(): string { return ""; } }`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "user.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/user.ts", RelPath: "user.ts", Language: "typescript"}
	Extract(root, code, file, result)

	for _, imp := range result.Imports {
		if imp.IsReexport {
			t.Error("normal export should not produce IsReexport imports")
		}
	}
}

func TestExtract_BlockScopeInIfElse(t *testing.T) {
	code := []byte(`function process(flag: boolean) {
    if (flag) {
        const svc = new ServiceA();
        svc.getData();
    } else {
        const svc = new ServiceB();
        svc.getError();
    }
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "app.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/app.ts", RelPath: "app.ts", Language: "typescript"}
	Extract(root, code, file, result)

	// Verify calls have different CallerScope
	if len(result.Calls) < 2 {
		t.Fatalf("expected at least 2 calls, got %d", len(result.Calls))
	}

	scopeMap := map[string]string{}
	for _, call := range result.Calls {
		if call.CalledName == "getData" || call.CalledName == "getError" {
			scopeMap[call.CalledName] = call.CallerScope
		}
	}

	if scopeMap["getData"] == scopeMap["getError"] {
		t.Fatalf("getData and getError should have different CallerScope, both got %q", scopeMap["getData"])
	}
	if scopeMap["getData"] == "" || scopeMap["getError"] == "" {
		t.Fatal("CallerScope should not be empty for calls inside if/else blocks")
	}

	// Verify ScopeParents exist
	if len(result.ScopeParents) == 0 {
		t.Fatal("ScopeParents should be populated for block-scoped calls")
	}

	t.Logf("✅ Block scope: getData scope=%q, getError scope=%q, parents=%v", scopeMap["getData"], scopeMap["getError"], result.ScopeParents)
}

func TestExtract_DestructureAssignment(t *testing.T) {
	code := []byte(`function setup() {
    const { data, error } = useQuery();
    const [count, setCount] = useState(0);
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "app.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/app.ts", RelPath: "app.ts", Language: "typescript"}
	Extract(root, code, file, result)

	destructures := map[string]string{}
	for _, pa := range result.PendingAssignments {
		if pa.Kind == "destructure" {
			destructures[pa.LHS] = pa.DestructuredKey
		}
	}

	// Object destructure
	if destructures["data"] != "data" {
		t.Errorf("expected data→data, got %q", destructures["data"])
	}
	if destructures["error"] != "error" {
		t.Errorf("expected error→error, got %q", destructures["error"])
	}
	// Array destructure
	if destructures["count"] != "0" {
		t.Errorf("expected count→0, got %q", destructures["count"])
	}
	if destructures["setCount"] != "1" {
		t.Errorf("expected setCount→1, got %q", destructures["setCount"])
	}

	t.Logf("✅ Destructure: %d pending assignments extracted", len(result.PendingAssignments))
}

func TestExtract_NestedFunctionScopeParent(t *testing.T) {
	code := []byte(`function outer() {
    const inner = (x: number) => {
        console.log(x);
    };
    inner(1);
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "app.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/app.ts", RelPath: "app.ts", Language: "typescript"}
	Extract(root, code, file, result)

	// Find the nested function symbol
	var nestedQualifiedName string
	for _, sym := range result.Symbols {
		if sym.Name == "inner" {
			nestedQualifiedName = sym.QualifiedName
			break
		}
	}
	if nestedQualifiedName == "" {
		t.Fatal("nested function 'inner' not found in symbols")
	}

	// Verify ScopeParents maps inner → outer
	parent, exists := result.ScopeParents[nestedQualifiedName]
	if !exists {
		t.Fatalf("ScopeParents should contain %q", nestedQualifiedName)
	}
	if !strings.Contains(parent, "outer") {
		t.Fatalf("expected parent to contain 'outer', got %q", parent)
	}

	t.Logf("✅ Nested function parent: %s → %s", nestedQualifiedName, parent)
}

func TestExtract_BlockScopeInTryCatch(t *testing.T) {
	code := []byte(`function handle() {
    try {
        const conn = getConnection();
        conn.query();
    } catch (e) {
        const logger = getLogger();
        logger.error(e);
    }
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "app.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/app.ts", RelPath: "app.ts", Language: "typescript"}
	Extract(root, code, file, result)

	scopeMap := map[string]string{}
	for _, call := range result.Calls {
		if call.CalledName == "query" || call.CalledName == "error" {
			scopeMap[call.CalledName] = call.CallerScope
		}
	}

	if scopeMap["query"] == scopeMap["error"] {
		t.Fatalf("try/catch calls should have different CallerScope, both got %q", scopeMap["query"])
	}
	if scopeMap["query"] == "" || scopeMap["error"] == "" {
		t.Fatal("CallerScope should not be empty for calls inside try/catch blocks")
	}
	t.Logf("✅ Try/catch block scope: query=%q, error=%q", scopeMap["query"], scopeMap["error"])
}

func TestExtract_BlockScopeInForLoop(t *testing.T) {
	code := []byte(`function process(items: string[]) {
    for (const item of items) {
        const svc = getService();
        svc.handle(item);
    }
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "app.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/app.ts", RelPath: "app.ts", Language: "typescript"}
	Extract(root, code, file, result)

	var handleScope string
	for _, call := range result.Calls {
		if call.CalledName == "handle" {
			handleScope = call.CallerScope
			break
		}
	}

	if handleScope == "" {
		t.Fatal("handle() call not found")
	}
	if !strings.Contains(handleScope, "#L") {
		t.Fatalf("expected block scope with #L suffix, got %q", handleScope)
	}
	t.Logf("✅ For loop block scope: handle scope=%q", handleScope)
}

func TestExtract_NestedBlockScope(t *testing.T) {
	code := []byte(`function process(flag: boolean) {
    if (flag) {
        for (const item of items) {
            const svc = getService();
            svc.run(item);
        }
    }
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "app.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/app.ts", RelPath: "app.ts", Language: "typescript"}
	Extract(root, code, file, result)

	var runScope string
	for _, call := range result.Calls {
		if call.CalledName == "run" {
			runScope = call.CallerScope
			break
		}
	}

	if runScope == "" {
		t.Fatal("run() call not found")
	}
	// Should have nested block: e.g. "app.ts.process#L2#L3"
	if strings.Count(runScope, "#L") < 2 {
		t.Fatalf("expected nested block scope with at least 2 #L segments, got %q", runScope)
	}

	// Verify parent chain exists
	if len(result.ScopeParents) < 2 {
		t.Fatalf("expected at least 2 ScopeParents entries for nested blocks, got %d", len(result.ScopeParents))
	}
	t.Logf("✅ Nested block scope: run scope=%q, parents=%v", runScope, result.ScopeParents)
}

func TestExtract_DestructureWithRename(t *testing.T) {
	code := []byte(`function setup() {
    const { data: result, error: err } = fetchData();
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "app.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/app.ts", RelPath: "app.ts", Language: "typescript"}
	Extract(root, code, file, result)

	destructures := map[string]string{}
	for _, pa := range result.PendingAssignments {
		if pa.Kind == "destructure" {
			destructures[pa.LHS] = pa.DestructuredKey
		}
	}

	if destructures["result"] != "data" {
		t.Errorf("expected LHS=result → key=data, got key=%q", destructures["result"])
	}
	if destructures["err"] != "error" {
		t.Errorf("expected LHS=err → key=error, got key=%q", destructures["err"])
	}
	t.Logf("✅ Destructure with rename: %v", destructures)
}

func TestExtract_DestructureAwait(t *testing.T) {
	code := []byte(`async function load() {
    const { data, loading } = await useQuery();
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "app.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/app.ts", RelPath: "app.ts", Language: "typescript"}
	Extract(root, code, file, result)

	destructures := map[string]string{}
	callees := map[string]string{}
	for _, pa := range result.PendingAssignments {
		if pa.Kind == "destructure" {
			destructures[pa.LHS] = pa.DestructuredKey
			callees[pa.LHS] = pa.Callee
		}
	}

	if destructures["data"] != "data" {
		t.Errorf("expected data→data, got %q", destructures["data"])
	}
	if callees["data"] != "useQuery" {
		t.Errorf("expected callee=useQuery, got %q", callees["data"])
	}
	t.Logf("✅ Destructure await: callee=%q, keys=%v", callees["data"], destructures)
}

func TestExtract_NewExpressionTypeHint(t *testing.T) {
	code := []byte(`function setup() {
    const svc = new UserService();
    svc.findById(1);
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "app.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/app.ts", RelPath: "app.ts", Language: "typescript"}
	Extract(root, code, file, result)

	// Verify TypeHint for svc = new UserService()
	var found bool
	for _, hint := range result.TypeHints {
		if hint.VarName == "svc" && hint.TypeName == "UserService" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected TypeHint for svc=UserService from new_expression")
	}
	t.Log("✅ new_expression produces TypeHint: svc → UserService")
}

func TestExtract_NewExpressionInBlockScope(t *testing.T) {
	code := []byte(`function process(flag: boolean) {
    if (flag) {
        const svc = new ServiceA();
        svc.run();
    } else {
        const svc = new ServiceB();
        svc.stop();
    }
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "app.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/app.ts", RelPath: "app.ts", Language: "typescript"}
	Extract(root, code, file, result)

	// Verify TypeHints have block-level scope (different for if vs else)
	scopesByType := map[string]string{}
	for _, hint := range result.TypeHints {
		if hint.VarName == "svc" {
			scopesByType[hint.TypeName] = hint.Scope
		}
	}

	if scopesByType["ServiceA"] == "" {
		t.Fatal("missing TypeHint for svc=ServiceA")
	}
	if scopesByType["ServiceB"] == "" {
		t.Fatal("missing TypeHint for svc=ServiceB")
	}
	if scopesByType["ServiceA"] == scopesByType["ServiceB"] {
		t.Fatalf("ServiceA and ServiceB should have different scopes, both got %q", scopesByType["ServiceA"])
	}
	t.Logf("✅ new_expression block scope: ServiceA scope=%q, ServiceB scope=%q", scopesByType["ServiceA"], scopesByType["ServiceB"])
}

func TestExtractFunction_TypeParams(t *testing.T) {
	tests := []struct {
		name           string
		code           string
		funcName       string
		expectedParams []string
	}{
		{
			name:           "single generic function",
			code:           `function identity<T>(arg: T): T { return arg; }`,
			funcName:       "identity",
			expectedParams: []string{"T"},
		},
		{
			name:           "multiple generic parameters",
			code:           `function merge<T, U>(a: T, b: U): T & U { return {...a, ...b}; }`,
			funcName:       "merge",
			expectedParams: []string{"T", "U"},
		},
		{
			name:           "generic with extends constraint",
			code:           `function find<T extends Entity>(id: string): T { return null as any; }`,
			funcName:       "find",
			expectedParams: []string{"T"},
		},
		{
			name:           "no generic parameters",
			code:           `function save(user: User): void {}`,
			funcName:       "save",
			expectedParams: nil,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			root, cleanup := parse([]byte(testCase.code))
			defer cleanup()
			result := &model.ParseResult{}
			file := scanner.ScannedFile{RelPath: "test.ts", Language: "typescript"}
			Extract(root, []byte(testCase.code), file, result)

			var found *model.Symbol
			for i := range result.Symbols {
				if result.Symbols[i].Name == testCase.funcName {
					found = &result.Symbols[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("function %q not found in symbols", testCase.funcName)
			}
			if len(found.TypeParams) != len(testCase.expectedParams) {
				t.Fatalf("TypeParams length: got %d, want %d (got %v)", len(found.TypeParams), len(testCase.expectedParams), found.TypeParams)
			}
			for i, expected := range testCase.expectedParams {
				if found.TypeParams[i] != expected {
					t.Errorf("TypeParams[%d]: got %q, want %q", i, found.TypeParams[i], expected)
				}
			}
		})
	}
}

func TestExtractMethod_TypeParams(t *testing.T) {
	code := []byte(`class Service {
    transform<T>(input: T): T { return input; }
    save(user: User): void {}
}`)
	root, cleanup := parse(code)
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "service.ts", Language: "typescript"}
	Extract(root, code, file, result)

	var transformSymbol *model.Symbol
	var saveSymbol *model.Symbol
	for i := range result.Symbols {
		if result.Symbols[i].Name == "transform" {
			transformSymbol = &result.Symbols[i]
		}
		if result.Symbols[i].Name == "save" {
			saveSymbol = &result.Symbols[i]
		}
	}
	if transformSymbol == nil {
		t.Fatal("transform method not found")
	}
	if len(transformSymbol.TypeParams) != 1 || transformSymbol.TypeParams[0] != "T" {
		t.Errorf("transform TypeParams: got %v, want [T]", transformSymbol.TypeParams)
	}
	if saveSymbol == nil {
		t.Fatal("save method not found")
	}
	if len(saveSymbol.TypeParams) != 0 {
		t.Errorf("save TypeParams: got %v, want nil", saveSymbol.TypeParams)
	}
}

func TestExtractAmbientDeclaration_TypeParams(t *testing.T) {
	code := []byte(`declare function useQuery<T>(): QueryResult<T>;
declare function useState<S>(initial: S): [S, (s: S) => void];`)
	root, cleanup := parse(code)
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "types.d.ts", Language: "typescript"}
	Extract(root, code, file, result)

	var useQuerySymbol *model.Symbol
	var useStateSymbol *model.Symbol
	for i := range result.Symbols {
		if result.Symbols[i].Name == "useQuery" {
			useQuerySymbol = &result.Symbols[i]
		}
		if result.Symbols[i].Name == "useState" {
			useStateSymbol = &result.Symbols[i]
		}
	}
	if useQuerySymbol == nil {
		t.Fatal("useQuery not found")
	}
	if len(useQuerySymbol.TypeParams) != 1 || useQuerySymbol.TypeParams[0] != "T" {
		t.Errorf("useQuery TypeParams: got %v, want [T]", useQuerySymbol.TypeParams)
	}
	if useStateSymbol == nil {
		t.Fatal("useState not found")
	}
	if len(useStateSymbol.TypeParams) != 1 || useStateSymbol.TypeParams[0] != "S" {
		t.Errorf("useState TypeParams: got %v, want [S]", useStateSymbol.TypeParams)
	}
}

func TestExtractClass_HeritageTypeArgs(t *testing.T) {
	tests := []struct {
		name             string
		code             string
		parentName       string
		expectedTypeArgs []string
		heritageKind     string
	}{
		{
			name:             "single generic extends",
			code:             `class UserService extends BaseService<User> {}`,
			parentName:       "BaseService",
			expectedTypeArgs: []string{"User"},
			heritageKind:     "extends",
		},
		{
			name:             "multiple generic extends",
			code:             `class Repo extends Base<User, string> {}`,
			parentName:       "Base",
			expectedTypeArgs: []string{"User", "string"},
			heritageKind:     "extends",
		},
		{
			name:             "no generic extends",
			code:             `class Dog extends Animal {}`,
			parentName:       "Animal",
			expectedTypeArgs: nil,
			heritageKind:     "extends",
		},
		{
			name:             "implements generic",
			code:             `class UserRepo implements Repository<User> {}`,
			parentName:       "Repository",
			expectedTypeArgs: []string{"User"},
			heritageKind:     "implements",
		},
		{
			name:             "abstract class extends",
			code:             `class UserService extends AbstractService<User> {}`,
			parentName:       "AbstractService",
			expectedTypeArgs: []string{"User"},
			heritageKind:     "extends",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			root, cleanup := parse([]byte(testCase.code))
			defer cleanup()
			result := &model.ParseResult{}
			file := scanner.ScannedFile{RelPath: "test.ts", Language: "typescript"}
			Extract(root, []byte(testCase.code), file, result)

			var found *model.RawHeritage
			for i := range result.Heritage {
				if result.Heritage[i].ParentName == testCase.parentName && result.Heritage[i].Kind == testCase.heritageKind {
					found = &result.Heritage[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("heritage entry for parent %q kind %q not found", testCase.parentName, testCase.heritageKind)
			}
			if len(found.TypeArgs) != len(testCase.expectedTypeArgs) {
				t.Fatalf("TypeArgs length: got %d, want %d (got %v)", len(found.TypeArgs), len(testCase.expectedTypeArgs), found.TypeArgs)
			}
			for i, expected := range testCase.expectedTypeArgs {
				if found.TypeArgs[i] != expected {
					t.Errorf("TypeArgs[%d]: got %q, want %q", i, found.TypeArgs[i], expected)
				}
			}
		})
	}
}
