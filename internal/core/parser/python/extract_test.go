package python

import (
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func parse(code []byte) (*tree_sitter.Node, func()) {
	parser := tree_sitter.NewParser()
	lang := tree_sitter.NewLanguage(tree_sitter_python.Language())
	parser.SetLanguage(lang)
	tree := parser.Parse(code, nil)
	return tree.RootNode(), func() { tree.Close(); parser.Close() }
}

func TestExtract_ClassAndFunctions(t *testing.T) {
	code := []byte(`class UserService:
    def find_by_id(self, user_id):
        pass

    def save(self, user):
        pass

def helper():
    pass
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "service.py", Language: "python"}
	file := scanner.ScannedFile{Path: "/test/service.py", RelPath: "service.py", Language: "python"}
	Extract(root, code, file, result)

	if len(result.Symbols) < 4 {
		t.Fatalf("expected at least 4 symbols (class + 2 methods + helper), got %d", len(result.Symbols))
	}

	names := map[string]bool{}
	for _, sym := range result.Symbols {
		names[sym.Name] = true
	}
	for _, expected := range []string{"UserService", "find_by_id", "save", "helper"} {
		if !names[expected] {
			t.Fatalf("missing symbol: %s", expected)
		}
	}
	t.Log("✅ Python Extract: class + methods + top-level function")
}

func TestExtract_Inheritance(t *testing.T) {
	code := []byte(`class Animal:
    pass

class Dog(Animal):
    def speak(self):
        pass
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "models.py", Language: "python"}
	file := scanner.ScannedFile{Path: "/test/models.py", RelPath: "models.py", Language: "python"}
	Extract(root, code, file, result)

	if len(result.Heritage) < 1 {
		t.Fatalf("expected at least 1 heritage, got %d", len(result.Heritage))
	}
	if result.Heritage[0].ChildName != "Dog" || result.Heritage[0].ParentName != "Animal" {
		t.Fatalf("expected Dog extends Animal, got %s extends %s", result.Heritage[0].ChildName, result.Heritage[0].ParentName)
	}
	t.Log("✅ Python Extract: Dog(Animal) inheritance")
}

func TestExtract_ImportFrom(t *testing.T) {
	code := []byte(`from models import User, Order
import os
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "app.py", Language: "python"}
	file := scanner.ScannedFile{Path: "/test/app.py", RelPath: "app.py", Language: "python"}
	Extract(root, code, file, result)

	if len(result.Imports) < 2 {
		t.Fatalf("expected at least 2 imports (User, Order), got %d", len(result.Imports))
	}
	t.Logf("✅ Python Extract: %d imports", len(result.Imports))
}

func TestPythonFlowContext(t *testing.T) {
	code := []byte(`
def process():
    validate()
    if True:
        save()
    else:
        log_error()
    for x in items:
        update(x)
`)
	root, cleanup := parse(code)
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "service.py", Language: "python"}
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
	if flowMap["log_error"] != "else" {
		t.Errorf("log_error: expected else, got %q", flowMap["log_error"])
	}
	if flowMap["update"] != "loop" {
		t.Errorf("update: expected loop, got %q", flowMap["update"])
	}
	t.Log("✅ Python FlowContext: if/else/loop")
}

func TestPythonPendingAssignment(t *testing.T) {
	code := []byte(`
def process():
    user = get_user()
    addr = user.get_address()
    name = addr.name
    alias = user
`)
	root, cleanup := parse(code)
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "service.py", Language: "python"}
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
	t.Log("✅ Python PendingAssignment: 4 kinds extracted")
}

func TestExtract_PythonPropertyAccessor(t *testing.T) {
	code := []byte(`class User:
    def __init__(self, name):
        self._name = name

    @property
    def name(self):
        return self._name

    @name.setter
    def name(self, value):
        self._name = value

    def greet(self):
        return "hello " + self._name
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "user.py", Language: "python"}
	Extract(root, code, file, result)

	found := map[string]struct{ getter, setter bool }{}
	for _, sym := range result.Symbols {
		if sym.Kind != "Function" {
			continue
		}
		found[sym.Name] = struct{ getter, setter bool }{sym.IsGetter, sym.IsSetter}
	}

	if !found["name"].getter && !found["name"].setter {
		t.Fatal("Python @property 'name' not detected")
	}
	if found["greet"].getter || found["greet"].setter {
		t.Error("greet is a normal method, should not be accessor")
	}
	if found["__init__"].getter || found["__init__"].setter {
		t.Error("__init__ should not be accessor")
	}
	t.Log("✅ Python @property/@setter accessor detection")
}

func TestExtract_NestedDefScopeParent(t *testing.T) {
	code := []byte(`def outer():
    x = get_connection()

    def inner():
        x.query()

    inner()
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "app.py", Language: "python"}
	file := scanner.ScannedFile{Path: "/test/app.py", RelPath: "app.py", Language: "python"}
	Extract(root, code, file, result)

	if result.ScopeParents == nil {
		t.Fatal("ScopeParents should be populated for nested def")
	}

	// Find inner's qualified name
	var innerQualifiedName string
	for child, parent := range result.ScopeParents {
		if parent != "" && child != "" {
			innerQualifiedName = child
			_ = parent
			break
		}
	}
	if innerQualifiedName == "" {
		t.Fatal("no ScopeParents entry found for nested function")
	}

	parent := result.ScopeParents[innerQualifiedName]
	if parent == "" {
		t.Fatalf("parent of %q should not be empty", innerQualifiedName)
	}

	// Verify inner function is extracted as a Symbol
	var innerSymbolFound bool
	for _, sym := range result.Symbols {
		if sym.Name == "inner" {
			innerSymbolFound = true
			break
		}
	}
	if !innerSymbolFound {
		t.Fatal("nested def 'inner' should be extracted as a Symbol")
	}

	t.Logf("✅ Python nested def parent: %s → %s", innerQualifiedName, parent)
}

func TestExtract_PythonBlockScopeInAssignment(t *testing.T) {
	code := []byte(`def process(flag):
    if flag:
        svc = ServiceA()
        svc.get_data()
    else:
        svc = ServiceB()
        svc.get_error()
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "app.py", Language: "python"}
	file := scanner.ScannedFile{Path: "/test/app.py", RelPath: "app.py", Language: "python"}
	Extract(root, code, file, result)

	scopeMap := map[string]string{}
	for _, call := range result.Calls {
		if call.CalledName == "get_data" || call.CalledName == "get_error" {
			scopeMap[call.CalledName] = call.CallerScope
		}
	}

	if scopeMap["get_data"] == scopeMap["get_error"] {
		t.Fatalf("if/else calls should have different CallerScope, both got %q", scopeMap["get_data"])
	}
	if scopeMap["get_data"] == "" || scopeMap["get_error"] == "" {
		t.Fatal("CallerScope should not be empty for calls inside if/else blocks")
	}
	t.Logf("✅ Python block scope: get_data=%q, get_error=%q", scopeMap["get_data"], scopeMap["get_error"])
}
