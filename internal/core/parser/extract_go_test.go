package parser

import (
	"context"
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
)

func TestExtractGo_StructAndMethod(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`package service

import "fmt"

type UserService struct {
	dao UserDao
}

func (service *UserService) FindById(id int64) *User {
	fmt.Println("finding")
	return service.dao.FindById(id)
}

func NewUserService(dao UserDao) *UserService {
	return &UserService{dao: dao}
}
`)
	file := scanner.ScannedFile{Path: "/test/service.go", RelPath: "service.go", Language: "go"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	// Symbols: 1 struct + 2 functions
	structCount := 0
	funcCount := 0
	for _, symbol := range result.Symbols {
		if symbol.ClassType == "struct" {
			structCount++
			if symbol.Name != "UserService" {
				t.Fatalf("expected UserService, got %s", symbol.Name)
			}
			if symbol.QualifiedName != "service.UserService" {
				t.Fatalf("expected service.UserService, got %s", symbol.QualifiedName)
			}
		}
		if symbol.Kind == "Function" {
			funcCount++
		}
	}
	if structCount != 1 {
		t.Fatalf("expected 1 struct, got %d", structCount)
	}
	if funcCount != 2 {
		t.Fatalf("expected 2 functions, got %d", funcCount)
	}

	// Imports
	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}
	if result.Imports[0].ModulePath != "fmt" {
		t.Fatalf("expected fmt, got %s", result.Imports[0].ModulePath)
	}

	// Calls: fmt.Println + service.dao.FindById + UserService{}
	if len(result.Calls) < 2 {
		t.Fatalf("expected at least 2 calls, got %d", len(result.Calls))
	}

	// Type hints: dao field
	if len(result.TypeHints) < 1 {
		t.Fatalf("expected at least 1 type hint, got %d", len(result.TypeHints))
	}

	t.Logf("✅ Go extraction: %d symbols, %d calls, %d imports, %d type hints",
		len(result.Symbols), len(result.Calls), len(result.Imports), len(result.TypeHints))
}

func TestExtractGo_InterfaceAndEmbedding(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`package handler

type Handler interface {
	Handle(req Request) Response
}

type BaseHandler struct {
	logger Logger
}

type AuthHandler struct {
	BaseHandler
	authService AuthService
}
`)
	file := scanner.ScannedFile{Path: "/test/handler.go", RelPath: "handler.go", Language: "go"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	// 1 interface + 2 structs
	interfaceCount := 0
	structCount := 0
	for _, symbol := range result.Symbols {
		if symbol.Kind == "Interface" {
			interfaceCount++
		}
		if symbol.ClassType == "struct" {
			structCount++
		}
	}
	if interfaceCount != 1 {
		t.Fatalf("expected 1 interface, got %d", interfaceCount)
	}
	if structCount != 2 {
		t.Fatalf("expected 2 structs, got %d", structCount)
	}

	// Heritage: AuthHandler embeds BaseHandler
	if len(result.Heritage) != 1 {
		t.Fatalf("expected 1 heritage (embedding), got %d", len(result.Heritage))
	}
	if result.Heritage[0].Kind != "embedding" {
		t.Fatalf("expected embedding, got %s", result.Heritage[0].Kind)
	}
	if result.Heritage[0].ParentName != "BaseHandler" {
		t.Fatalf("expected BaseHandler, got %s", result.Heritage[0].ParentName)
	}

	t.Log("✅ Go interface + struct embedding extraction works")
}

func TestExtractGo_ExportedCheck(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`package pkg

func PublicFunc() {}
func privateFunc() {}
`)
	file := scanner.ScannedFile{Path: "/test/pkg.go", RelPath: "pkg.go", Language: "go"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	for _, symbol := range result.Symbols {
		if symbol.Name == "PublicFunc" && !symbol.IsExported {
			t.Fatal("PublicFunc should be exported")
		}
		if symbol.Name == "privateFunc" && symbol.IsExported {
			t.Fatal("privateFunc should not be exported")
		}
	}
	t.Log("✅ Go exported detection works")
}
