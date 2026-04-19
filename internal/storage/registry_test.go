package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistry_RegisterAndList(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.List()) != 0 {
		t.Fatal("new registry should be empty")
	}

	reg.Register("proj-a", "/path/to/a", "falkordb", "main")
	reg.Register("proj-b", "/path/to/b", "kuzu", "main")

	if len(reg.List()) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(reg.List()))
	}

	// Reload from disk
	reg2, err := NewRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg2.List()) != 2 {
		t.Fatalf("expected 2 entries after reload, got %d", len(reg2.List()))
	}
}

func TestRegistry_FindByNameAndPath(t *testing.T) {
	dir := t.TempDir()
	reg, _ := NewRegistry(dir)
	reg.Register("myproj", "/home/user/myproj", "kuzu", "main")

	if e := reg.FindByName("myproj"); e == nil || e.Path != "/home/user/myproj" {
		t.Error("FindByName failed")
	}
	if e := reg.FindByPath("/home/user/myproj"); e == nil || e.Name != "myproj" {
		t.Error("FindByPath failed")
	}
	if e := reg.FindByName("nonexist"); e != nil {
		t.Error("FindByName should return nil for unknown")
	}
	if e := reg.FindByPath("/no/such/path"); e != nil {
		t.Error("FindByPath should return nil for unknown")
	}
}

func TestRegistry_UpdateExisting(t *testing.T) {
	dir := t.TempDir()
	reg, _ := NewRegistry(dir)
	reg.Register("old-name", "/path/a", "kuzu", "main")
	reg.Register("new-name", "/path/a", "falkordb", "main")

	if len(reg.List()) != 1 {
		t.Fatalf("expected 1 entry after update, got %d", len(reg.List()))
	}
	e := reg.FindByPath("/path/a")
	if e.Name != "new-name" || e.Database != "falkordb" {
		t.Errorf("entry not updated: %+v", e)
	}
}

func TestRegistry_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	// Create empty registry file
	os.WriteFile(filepath.Join(dir, "registry.json"), []byte("[]"), 0o644)
	reg, err := NewRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.List()) != 0 {
		t.Fatal("empty JSON array should yield 0 entries")
	}
}
