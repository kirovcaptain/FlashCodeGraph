package typescript

import (
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
