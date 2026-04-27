package golang

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestExtractField_GoStruct(t *testing.T) {
	code := []byte(`package service

type Indexer struct {
	graphStore  GraphStore
	config      *Config
	name        string
}

func (indexer *Indexer) Index() {}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "indexer.go", Language: "go"}
	file := scanner.ScannedFile{Path: "/test/indexer.go", RelPath: "indexer.go", Language: "go"}
	Extract(root, code, file, result)

	t.Logf("Fields: %d", len(result.Fields))
	for _, field := range result.Fields {
		t.Logf("  %s: %s (owner=%s, vis=%s)", field.Name, field.Type, field.OwnerQualifiedName, field.Visibility)
	}

	if len(result.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(result.Fields))
	}

	for _, field := range result.Fields {
		if field.Name == "graphStore" && field.Visibility != "private" {
			t.Errorf("expected graphStore to be private, got %s", field.Visibility)
		}
		if field.OwnerQualifiedName != "service.Indexer" {
			t.Errorf("expected owner service.Indexer, got %s", field.OwnerQualifiedName)
		}
	}
}
