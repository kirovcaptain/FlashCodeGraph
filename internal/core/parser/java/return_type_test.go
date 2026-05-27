package java

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestExtractReturnType_GenericPreserved(t *testing.T) {
	code := `
package com.example;
import java.util.*;
import java.util.concurrent.CompletableFuture;

public class Repository {
    public List<User> findAll() { return null; }
    public Map<String, List<User>> getMap() { return null; }
    public List<? extends Animal> getAnimals() { return null; }
    public Map<String, Map<Integer, List<Order>>> nested() { return null; }
    public String getName() { return null; }
    public User[] getUsers() { return null; }
    public int getCount() { return 0; }
    public void save() {}
    public <T> T getBean(Class<T> c) { return null; }
    public Optional<User> findById(Long id) { return null; }
    public CompletableFuture<Response> call() { return null; }
}
`
	result := parseJavaFile(t, code, "Repository.java")

	symbolMap := make(map[string]model.Symbol)
	for _, sym := range result.Symbols {
		if sym.Kind == "Function" {
			symbolMap[sym.Name] = sym
		}
	}

	tests := []struct {
		methodName       string
		expectedName     string
		expectedArgCount int
	}{
		{"findAll", "List", 1},
		{"getMap", "Map", 2},
		{"getAnimals", "List", 1},
		{"nested", "Map", 2},
		{"getName", "String", 0},
		{"getUsers", "User[]", 0},
		{"getCount", "int", 0},
		{"getBean", "T", 0},
		{"findById", "Optional", 1},
		{"call", "CompletableFuture", 1},
	}

	for _, tc := range tests {
		sym, ok := symbolMap[tc.methodName]
		if !ok {
			t.Errorf("method %q not found", tc.methodName)
			continue
		}
		if len(sym.ReturnTypes) == 0 {
			t.Errorf("%s: no return types", tc.methodName)
			continue
		}
		returnType := sym.ReturnTypes[0]
		if returnType.Name != tc.expectedName {
			t.Errorf("%s: Name=%q, want %q", tc.methodName, returnType.Name, tc.expectedName)
		}
		if len(returnType.Args) != tc.expectedArgCount {
			t.Errorf("%s: Args count=%d, want %d", tc.methodName, len(returnType.Args), tc.expectedArgCount)
		}
	}

	// void is extracted as ReturnType{Name:"void"}
	if sym, ok := symbolMap["save"]; ok {
		if len(sym.ReturnTypes) != 1 || sym.ReturnTypes[0].Name != "void" {
			t.Errorf("save: expected [{void []}], got %v", sym.ReturnTypes)
		}
	}

	// Verify nested: Map<String, List<User>> → Args[1] = {Name:"List", Args:[{Name:"User"}]}
	if sym, ok := symbolMap["getMap"]; ok && len(sym.ReturnTypes) > 0 && len(sym.ReturnTypes[0].Args) == 2 {
		secondArg := sym.ReturnTypes[0].Args[1]
		if secondArg.Name != "List" || len(secondArg.Args) != 1 || secondArg.Args[0].Name != "User" {
			t.Errorf("getMap: second arg should be List<User>, got %+v", secondArg)
		}
	}

	// Verify Optional<User> → Args[0] = {Name:"User"}
	if sym, ok := symbolMap["findById"]; ok && len(sym.ReturnTypes) > 0 && len(sym.ReturnTypes[0].Args) == 1 {
		if sym.ReturnTypes[0].Args[0].Name != "User" {
			t.Errorf("findById: arg should be User, got %q", sym.ReturnTypes[0].Args[0].Name)
		}
	}

	t.Log("✅ Java ReturnTypes preserve generic type arguments")
}
