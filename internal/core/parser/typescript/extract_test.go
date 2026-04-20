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

	if names["UserService"] != "class" {
		t.Fatalf("UserService expected class, got %s", names["UserService"])
	}
	if names["findById"] != "function" {
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
