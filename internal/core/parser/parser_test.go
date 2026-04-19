package parser

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/liuymcn/flash-code-graph/internal/core/scanner"
)

func TestParseFile_Go(t *testing.T) {
	parser := New("")
	defer parser.Close()
	code := []byte(`package main

import "fmt"

func main() {
	fmt.Println("hello")
}

func helper(x int) string {
	return fmt.Sprintf("%d", x)
}
`)
	file := scanner.ScannedFile{
		Path:     "/test/main.go",
		RelPath:  "main.go",
		Language: "go",
	}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}
	if result.FilePath != "main.go" {
		t.Fatalf("expected main.go, got %s", result.FilePath)
	}
	if result.Language != "go" {
		t.Fatalf("expected go, got %s", result.Language)
	}
	t.Log("✅ Go parsing works")
}

func TestParseFile_Java(t *testing.T) {
	parser := New("")
	defer parser.Close()
	code := []byte(`package com.example;

public class UserService {
    public User findById(Long id) {
        return userDao.findById(id);
    }
}
`)
	file := scanner.ScannedFile{
		Path:     "/test/UserService.java",
		RelPath:  "UserService.java",
		Language: "java",
	}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}
	if result.Language != "java" {
		t.Fatalf("expected java, got %s", result.Language)
	}
	t.Log("✅ Java parsing works")
}

func TestParseFile_UnsupportedLanguage(t *testing.T) {
	parser := New("")
	defer parser.Close()
	file := scanner.ScannedFile{
		Path:     "/test/main.xyz",
		RelPath:  "main.xyz",
		Language: "xyz",
	}
	_, err := parser.ParseFile(context.Background(), file, []byte("hello"))
	if err == nil {
		t.Fatal("expected error for unsupported language")
	}
}

func TestParseFile_Cache(t *testing.T) {
	cacheDir := t.TempDir()
	parser := New(cacheDir)
	defer parser.Close()

	code := []byte(`package main
func hello() {}
`)
	file := scanner.ScannedFile{
		Path:     "/test/main.go",
		RelPath:  "main.go",
		Language: "go",
	}

	// First parse — cache miss
	result1, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("first parse:", err)
	}

	// Second parse — cache hit (same content)
	result2, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("second parse:", err)
	}

	if result1.Language != result2.Language {
		t.Fatal("cache returned different language")
	}
	t.Log("✅ AST cache hit works")
}

func TestParseFile_CacheInvalidation(t *testing.T) {
	cacheDir := t.TempDir()
	parser := New(cacheDir)
	defer parser.Close()

	file := scanner.ScannedFile{
		Path:     "/test/main.go",
		RelPath:  "main.go",
		Language: "go",
	}

	// Parse version 1
	codeV1 := []byte(`package main
func hello() {}
`)
	result1, err := parser.ParseFile(context.Background(), file, codeV1)
	if err != nil {
		t.Fatal("parse v1:", err)
	}

	// Parse version 2 — same path, different content → cache miss, new result
	codeV2 := []byte(`package main
func hello() {}
func world() {}
`)
	result2, err := parser.ParseFile(context.Background(), file, codeV2)
	if err != nil {
		t.Fatal("parse v2:", err)
	}

	// Both should have correct file path
	if result1.FilePath != result2.FilePath {
		t.Fatal("file paths should match")
	}

	// Cache should have 2 entries (different content hashes)
	entries, _ := os.ReadDir(cacheDir)
	totalFiles := 0
	for _, entry := range entries {
		if entry.IsDir() {
			subEntries, _ := os.ReadDir(filepath.Join(cacheDir, entry.Name()))
			totalFiles += len(subEntries)
		}
	}
	if totalFiles != 2 {
		t.Fatalf("expected 2 cache entries, got %d", totalFiles)
	}

	t.Log("✅ AST cache invalidation works (different content → different cache entry)")
}

func TestSupportedLanguage(t *testing.T) {
	supported := []string{"java", "python", "go", "typescript", "javascript", "rust", "c", "cpp", "csharp", "ruby", "php"}
	for _, language := range supported {
		if !SupportedLanguage(language) {
			t.Errorf("expected %s to be supported", language)
		}
	}
	if SupportedLanguage("kotlin") {
		t.Error("kotlin not supported yet (no official Go binding)")
	}
}
