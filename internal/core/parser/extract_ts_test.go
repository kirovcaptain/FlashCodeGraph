package parser

import (
	"context"
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
)

func TestExtractTS_FullClass(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`import { UserDao } from './user-dao';

export class UserService extends BaseService implements Repository {
  private dao: UserDao;

  constructor(dao: UserDao) {
    super();
    this.dao = dao;
  }

  async findById(id: number): Promise<User> {
    return this.dao.findById(id);
  }
}

export function helper(x: string): void {
  console.log(x);
}

const arrow = (x: number) => x * 2;
`)
	file := scanner.ScannedFile{Path: "/test/user-service.ts", RelPath: "src/user-service.ts", Language: "typescript"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	// Imports
	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}
	if result.Imports[0].SymbolName != "UserDao" {
		t.Fatalf("expected UserDao import, got %s", result.Imports[0].SymbolName)
	}

	// Symbols: 1 class + constructor + findById + helper + arrow = 5
	classCount := 0
	funcCount := 0
	for _, symbol := range result.Symbols {
		if symbol.Kind == "class" {
			classCount++
			if symbol.Name != "UserService" {
				t.Fatalf("expected UserService, got %s", symbol.Name)
			}
		}
		if symbol.Kind == "function" {
			funcCount++
		}
	}
	if classCount != 1 {
		t.Fatalf("expected 1 class, got %d", classCount)
	}
	if funcCount < 3 {
		t.Fatalf("expected at least 3 functions, got %d", funcCount)
	}

	// Heritage: extends BaseService + implements Repository
	if len(result.Heritage) != 2 {
		t.Fatalf("expected 2 heritage, got %d", len(result.Heritage))
	}
	extendsFound := false
	implementsFound := false
	for _, heritage := range result.Heritage {
		if heritage.Kind == "extends" && heritage.ParentName == "BaseService" {
			extendsFound = true
		}
		if heritage.Kind == "implements" && heritage.ParentName == "Repository" {
			implementsFound = true
		}
	}
	if !extendsFound {
		t.Fatal("missing extends BaseService")
	}
	if !implementsFound {
		t.Fatal("missing implements Repository")
	}

	// Calls
	if len(result.Calls) < 2 {
		t.Fatalf("expected at least 2 calls, got %d", len(result.Calls))
	}

	// Check async method
	for _, symbol := range result.Symbols {
		if symbol.Name == "findById" && !symbol.IsAsync {
			t.Fatal("findById should be async")
		}
	}

	// Check arrow function
	arrowFound := false
	for _, symbol := range result.Symbols {
		if symbol.Name == "arrow" && symbol.IsLambda {
			arrowFound = true
		}
	}
	if !arrowFound {
		t.Fatal("missing arrow function")
	}

	t.Logf("✅ TS extraction: %d symbols, %d calls, %d heritage, %d imports",
		len(result.Symbols), len(result.Calls), len(result.Heritage), len(result.Imports))
}

func TestExtractJS_Basic(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`const express = require('express');

class UserController {
  constructor(service) {
    this.service = service;
  }

  getUser(req, res) {
    const user = this.service.findById(req.params.id);
    res.json(user);
  }
}

module.exports = UserController;
`)
	file := scanner.ScannedFile{Path: "/test/controller.js", RelPath: "controller.js", Language: "javascript"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	classCount := 0
	for _, symbol := range result.Symbols {
		if symbol.Kind == "class" {
			classCount++
		}
	}
	if classCount != 1 {
		t.Fatalf("expected 1 class, got %d", classCount)
	}

	if len(result.Calls) < 2 {
		t.Fatalf("expected at least 2 calls, got %d", len(result.Calls))
	}

	t.Logf("✅ JS extraction: %d symbols, %d calls", len(result.Symbols), len(result.Calls))
}
