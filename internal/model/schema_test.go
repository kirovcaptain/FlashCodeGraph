package model

import (
	"strings"
	"testing"
)

func TestNodeColumns_AllKindsHaveColumns(t *testing.T) {
	expectedKinds := []string{
		"Function", "Class", "Interface", "Variable",
		"File", "Directory", "Repository",
		"Route", "QueryNode", "Community", "Process",
		"Annotation", "ExternalService",
	}
	for _, kind := range expectedKinds {
		cols, ok := NodeColumns[kind]
		if !ok {
			t.Fatalf("missing schema for kind %s", kind)
		}
		if len(cols) == 0 {
			t.Fatalf("kind %s has 0 columns", kind)
		}
	}
	t.Logf("✅ All %d node kinds have schema definitions", len(expectedKinds))
}

func TestColumnNames(t *testing.T) {
	names := ColumnNames("Function")
	if len(names) < 10 {
		t.Fatalf("Function expected 10+ columns, got %d", len(names))
	}
	// Must include key fields
	has := map[string]bool{}
	for _, n := range names {
		has[n] = true
	}
	for _, required := range []string{"name", "qualified_name", "file_path", "start_line", "params", "return_types"} {
		if !has[required] {
			t.Fatalf("Function missing column: %s", required)
		}
	}
	t.Logf("✅ Function has %d columns including all required fields", len(names))
}

func TestQueryReturnClause(t *testing.T) {
	clause := QueryReturnClause("Class")
	if !strings.HasPrefix(clause, "n.id") {
		t.Fatalf("clause should start with n.id, got: %s", clause)
	}
	if !strings.Contains(clause, "n.qualified_name") {
		t.Fatal("Class clause missing n.qualified_name")
	}
	if !strings.Contains(clause, "n.class_type") {
		t.Fatal("Class clause missing n.class_type")
	}
	if !strings.Contains(clause, "n.is_abstract") {
		t.Fatal("Class clause missing n.is_abstract")
	}
	t.Logf("✅ Class return clause: %s", clause[:60]+"...")
}

func TestQueryReturnClause_UnknownKind(t *testing.T) {
	clause := QueryReturnClause("UnknownKind")
	if clause != "n.id" {
		t.Fatalf("unknown kind should return 'n.id', got: %s", clause)
	}
	t.Log("✅ Unknown kind returns minimal clause")
}

func TestColumnNames_UnknownKind(t *testing.T) {
	names := ColumnNames("UnknownKind")
	if len(names) != 2 || names[0] != "name" || names[1] != "file_path" {
		t.Fatalf("unknown kind expected [name, file_path], got %v", names)
	}
	t.Log("✅ Unknown kind returns fallback columns")
}
