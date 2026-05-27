package java_test

import (
	"os"
	"path/filepath"
	"testing"

	java "github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver/java"
)

func TestExternalMethodManager_JDKLookup(t *testing.T) {
	manager := java.NewExternalMethodManager(nil, "")
	// JDK method should be found
	ret, found := manager.Lookup("List", "stream")
	if !found || ret.Name != "Stream" {
		t.Fatalf("expected Stream, got %q found=%v", ret, found)
	}
}

func TestExternalMethodManager_EmbedLookup(t *testing.T) {
	manager := java.NewExternalMethodManager([]string{"spring"}, "")
	// Spring method should be found
	ret, found := manager.Lookup("BeanUtils", "copyProperties")
	if !found {
		t.Fatal("expected BeanUtils.copyProperties to be found")
	}
	if ret.Name != "" {
		t.Fatalf("expected empty return (void), got %q", ret)
	}
}

func TestExternalMethodManager_NoFrameworkNoEmbed(t *testing.T) {
	manager := java.NewExternalMethodManager(nil, "")
	// Spring method should NOT be found without framework
	_, found := manager.Lookup("BeanUtils", "copyProperties")
	if found {
		t.Fatal("expected BeanUtils.copyProperties not found without spring framework")
	}
}

func TestExternalMethodManager_UserDefinedOverrides(t *testing.T) {
	// Create temp dir with custom JSON
	tmpDir := t.TempDir()
	customDir := filepath.Join(tmpDir, ".fcg", "external")
	os.MkdirAll(customDir, 0755)
	os.WriteFile(filepath.Join(customDir, "custom.json"), []byte(`{"List.stream":"CustomStream"}`), 0644)

	manager := java.NewExternalMethodManager(nil, tmpDir)
	// User-defined should override JDK
	ret, found := manager.Lookup("List", "stream")
	if !found || ret.Name != "CustomStream" {
		t.Fatalf("expected CustomStream, got %q found=%v", ret, found)
	}
}

func TestExternalMethodManager_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	customDir := filepath.Join(tmpDir, ".fcg", "external")
	os.MkdirAll(customDir, 0755)
	os.WriteFile(filepath.Join(customDir, "bad.json"), []byte(`{invalid`), 0644)
	os.WriteFile(filepath.Join(customDir, "good.json"), []byte(`{"Foo.bar":"String"}`), 0644)

	manager := java.NewExternalMethodManager(nil, tmpDir)
	// Good file should still load
	ret, found := manager.Lookup("Foo", "bar")
	if !found || ret.Name != "String" {
		t.Fatalf("expected String, got %q found=%v", ret, found)
	}
}

func TestExternalMethodManager_NilSafe(t *testing.T) {
	var manager *java.ExternalMethodManager
	_, found := manager.Lookup("List", "stream")
	if found {
		t.Fatal("expected not found on nil manager")
	}
}
