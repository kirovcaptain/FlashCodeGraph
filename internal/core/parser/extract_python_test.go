package parser

import (
	"context"
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
)

func TestExtractPython_FullClass(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`from abc import ABC
from services.user_dao import UserDao

class UserService(BaseService, ABC):
    def __init__(self, dao: UserDao):
        self.dao = dao

    def find_by_id(self, user_id: int) -> User:
        return self.dao.find_by_id(user_id)

    @staticmethod
    def helper():
        pass

def top_level_func(x):
    UserService().find_by_id(x)
`)
	file := scanner.ScannedFile{Path: "/test/services/user_service.py", RelPath: "services/user_service.py", Language: "python"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	// Imports: 2 (ABC, UserDao)
	if len(result.Imports) != 2 {
		t.Fatalf("expected 2 imports, got %d", len(result.Imports))
	}

	// Symbols: 1 class + 3 methods + 1 top-level func = 5
	classCount := 0
	funcCount := 0
	for _, symbol := range result.Symbols {
		switch {
		case symbol.Kind == "abstract_class" || symbol.Kind == "class":
			classCount++
			if symbol.Name != "UserService" {
				t.Fatalf("expected UserService, got %s", symbol.Name)
			}
			if !symbol.IsAbstract {
				t.Fatal("UserService should be abstract (inherits ABC)")
			}
		case symbol.Kind == "function":
			funcCount++
		}
	}
	if classCount != 1 {
		t.Fatalf("expected 1 class, got %d", classCount)
	}
	if funcCount != 4 {
		t.Fatalf("expected 4 functions (__init__ + find_by_id + helper + top_level_func), got %d", funcCount)
	}

	// Heritage: BaseService + ABC
	if len(result.Heritage) != 2 {
		t.Fatalf("expected 2 heritage, got %d", len(result.Heritage))
	}

	// Calls: self.dao.find_by_id + UserService() + find_by_id
	if len(result.Calls) < 2 {
		t.Fatalf("expected at least 2 calls, got %d", len(result.Calls))
	}

	// Type hints: dao: UserDao, user_id: int
	if len(result.TypeHints) < 2 {
		t.Fatalf("expected at least 2 type hints, got %d", len(result.TypeHints))
	}

	// Check qualified name
	for _, symbol := range result.Symbols {
		if symbol.Name == "UserService" {
			expected := "services.user_service.UserService"
			if symbol.QualifiedName != expected {
				t.Fatalf("expected qualified name %s, got %s", expected, symbol.QualifiedName)
			}
		}
	}

	t.Logf("✅ Python extraction: %d symbols, %d calls, %d heritage, %d imports, %d type hints",
		len(result.Symbols), len(result.Calls), len(result.Heritage), len(result.Imports), len(result.TypeHints))
}

func TestExtractPython_TopLevelFunctions(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`import os

def read_file(path: str) -> str:
    with open(path) as f:
        return f.read()

def process(data):
    result = read_file("input.txt")
    print(result)
`)
	file := scanner.ScannedFile{Path: "/test/utils.py", RelPath: "utils.py", Language: "python"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	if len(result.Symbols) != 2 {
		t.Fatalf("expected 2 functions, got %d", len(result.Symbols))
	}

	// Check return type annotation
	for _, symbol := range result.Symbols {
		if symbol.Name == "read_file" && symbol.ReturnTypes[0] != "str" {
			t.Fatalf("expected return type str, got %s", symbol.ReturnTypes[0])
		}
	}

	t.Log("✅ Python top-level functions extraction works")
}

func TestExtractPython_MultiImportFrom(t *testing.T) {
	p := New("")
	defer p.Close()

	code := []byte(`from models import User, Order, Product

def process():
    pass
`)
	file := scanner.ScannedFile{Path: "/test/app.py", RelPath: "app.py", Language: "python"}
	result, err := p.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal(err)
	}

	// Should extract 3 imports: User, Order, Product
	if len(result.Imports) != 3 {
		names := []string{}
		for _, imp := range result.Imports {
			names = append(names, imp.SymbolName)
		}
		t.Fatalf("expected 3 imports (User, Order, Product), got %d: %v", len(result.Imports), names)
	}

	expected := map[string]bool{"User": false, "Order": false, "Product": false}
	for _, imp := range result.Imports {
		if _, ok := expected[imp.SymbolName]; !ok {
			t.Fatalf("unexpected import symbol: %s", imp.SymbolName)
		}
		expected[imp.SymbolName] = true
		if imp.ModulePath != "models" {
			t.Fatalf("expected module 'models', got %s", imp.ModulePath)
		}
	}
	for name, found := range expected {
		if !found {
			t.Fatalf("missing import: %s", name)
		}
	}

	t.Log("✅ Python multi-import: from models import User, Order, Product")
}

func TestExtractPython_AliasedImport(t *testing.T) {
	p := New("")
	defer p.Close()

	code := []byte(`from models import User as U, Order as O
from pkg.utils import helper as h
`)
	file := scanner.ScannedFile{Path: "/test/app.py", RelPath: "app.py", Language: "python"}
	result, err := p.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Imports) != 3 {
		names := []string{}
		for _, imp := range result.Imports {
			names = append(names, imp.SymbolName)
		}
		t.Fatalf("expected 3 imports, got %d: %v", len(result.Imports), names)
	}

	// Should extract original names, not aliases
	expected := map[string]string{
		"User":   "models",
		"Order":  "models",
		"helper": "pkg.utils",
	}
	for _, imp := range result.Imports {
		expectedModule, ok := expected[imp.SymbolName]
		if !ok {
			t.Fatalf("unexpected symbol: %s (should be original name, not alias)", imp.SymbolName)
		}
		if imp.ModulePath != expectedModule {
			t.Fatalf("symbol %s: expected module %s, got %s", imp.SymbolName, expectedModule, imp.ModulePath)
		}
	}

	t.Log("✅ Aliased imports: original names extracted, aliases ignored")
}
