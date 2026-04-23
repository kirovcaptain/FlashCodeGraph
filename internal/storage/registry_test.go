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

func TestRegistry_MultiBranch(t *testing.T) {
	dir := t.TempDir()
	reg, _ := NewRegistry(dir)
	reg.Register("myproj", "/path/to/proj", "falkordb", "main")
	reg.Register("myproj", "/path/to/proj", "falkordb", "develop")
	reg.Register("myproj", "/path/to/proj", "falkordb", "feature/login")

	if len(reg.List()) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(reg.List()))
	}
}

func TestRegistry_UpdateSameBranch(t *testing.T) {
	dir := t.TempDir()
	reg, _ := NewRegistry(dir)
	reg.Register("old-name", "/path/a", "kuzu", "main")
	reg.Register("new-name", "/path/a", "falkordb", "main")

	if len(reg.List()) != 1 {
		t.Fatalf("expected 1 entry after update, got %d", len(reg.List()))
	}
	entries := reg.FindByPath("/path/a")
	if len(entries) != 1 || entries[0].Name != "new-name" || entries[0].Database != "falkordb" {
		t.Errorf("entry not updated: %+v", entries)
	}
}

func TestRegistry_FindByPath_MultiBranch(t *testing.T) {
	dir := t.TempDir()
	reg, _ := NewRegistry(dir)
	reg.Register("proj", "/path/to/proj", "falkordb", "main")
	reg.Register("proj", "/path/to/proj", "falkordb", "develop")
	reg.Register("other", "/path/to/other", "falkordb", "main")

	entries := reg.FindByPath("/path/to/proj")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries for proj, got %d", len(entries))
	}

	entries = reg.FindByPath("/path/to/other")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for other, got %d", len(entries))
	}

	entries = reg.FindByPath("/no/such/path")
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries for unknown path, got %d", len(entries))
	}
}

func TestRegistry_FindByNameAndPath(t *testing.T) {
	dir := t.TempDir()
	reg, _ := NewRegistry(dir)
	reg.Register("myproj", "/home/user/myproj", "kuzu", "main")

	if e := reg.FindByName("myproj"); e == nil || e.Path != "/home/user/myproj" {
		t.Error("FindByName failed")
	}
	if entries := reg.FindByPath("/home/user/myproj"); len(entries) != 1 || entries[0].Name != "myproj" {
		t.Error("FindByPath failed")
	}
	if e := reg.FindByName("nonexist"); e != nil {
		t.Error("FindByName should return nil for unknown")
	}
	if entries := reg.FindByPath("/no/such/path"); len(entries) != 0 {
		t.Error("FindByPath should return empty for unknown")
	}
}

func TestRegistry_Unregister_SingleBranch(t *testing.T) {
	dir := t.TempDir()
	reg, _ := NewRegistry(dir)
	reg.Register("proj", "/path/a", "falkordb", "main")
	reg.Register("proj", "/path/a", "falkordb", "develop")

	reg.Unregister("/path/a", "main")

	entries := reg.FindByPath("/path/a")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after unregister, got %d", len(entries))
	}
	if entries[0].Branch != "develop" {
		t.Errorf("wrong branch remaining: %s", entries[0].Branch)
	}
}

func TestRegistry_UnregisterAll(t *testing.T) {
	dir := t.TempDir()
	reg, _ := NewRegistry(dir)
	reg.Register("proj", "/path/a", "falkordb", "main")
	reg.Register("proj", "/path/a", "falkordb", "develop")
	reg.Register("other", "/path/b", "falkordb", "main")

	reg.UnregisterAll("/path/a")

	if len(reg.List()) != 1 {
		t.Fatalf("expected 1 entry after unregister all, got %d", len(reg.List()))
	}
	if reg.List()[0].Path != "/path/b" {
		t.Errorf("wrong entry remaining: %+v", reg.List()[0])
	}
}

func TestRegistry_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "registry.json"), []byte("[]"), 0o644)
	reg, err := NewRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.List()) != 0 {
		t.Fatal("empty JSON array should yield 0 entries")
	}
}
