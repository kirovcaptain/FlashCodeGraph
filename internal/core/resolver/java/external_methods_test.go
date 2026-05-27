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
	ret, found := manager.Lookup("List", "stream", nil)
	if !found || ret.Name != "Stream" {
		t.Fatalf("expected Stream, got %q found=%v", ret, found)
	}
}

func TestExternalMethodManager_EmbedLookup(t *testing.T) {
	manager := java.NewExternalMethodManager([]string{"spring"}, "")
	// Spring method should be found
	ret, found := manager.Lookup("BeanUtils", "copyProperties", nil)
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
	_, found := manager.Lookup("BeanUtils", "copyProperties", nil)
	if found {
		t.Fatal("expected BeanUtils.copyProperties not found without spring framework")
	}
}

func TestExternalMethodManager_UserDefinedOverrides(t *testing.T) {
	// Create temp dir with custom JSON
	tmpDir := t.TempDir()
	customDir := filepath.Join(tmpDir, ".fcg", "external")
	os.MkdirAll(customDir, 0755)
	os.WriteFile(filepath.Join(customDir, "custom.json"), []byte(`{"List.stream()":"CustomStream"}`), 0644)

	manager := java.NewExternalMethodManager(nil, tmpDir)
	// User-defined should override JDK
	ret, found := manager.Lookup("List", "stream", nil)
	if !found || ret.Name != "CustomStream" {
		t.Fatalf("expected CustomStream, got %q found=%v", ret, found)
	}
}

func TestExternalMethodManager_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	customDir := filepath.Join(tmpDir, ".fcg", "external")
	os.MkdirAll(customDir, 0755)
	os.WriteFile(filepath.Join(customDir, "bad.json"), []byte(`{invalid`), 0644)
	os.WriteFile(filepath.Join(customDir, "good.json"), []byte(`{"Foo.bar()":"String"}`), 0644)

	manager := java.NewExternalMethodManager(nil, tmpDir)
	// Good file should still load
	ret, found := manager.Lookup("Foo", "bar", nil)
	if !found || ret.Name != "String" {
		t.Fatalf("expected String, got %q found=%v", ret, found)
	}
}

func TestExternalMethodManager_NilSafe(t *testing.T) {
	var manager *java.ExternalMethodManager
	_, found := manager.Lookup("List", "stream", nil)
	if found {
		t.Fatal("expected not found on nil manager")
	}
}

func TestExternalMethodManager_OverloadDisambiguation(t *testing.T) {
	// U-19: Stream.reduce has two overloads in stream.json:
	//   reduce(BinaryOperator) → Optional<T>
	//   reduce(T,BinaryOperator) → T
	manager := java.NewExternalMethodManager(nil, "")

	// With 1 arg → should pick reduce(BinaryOperator) → Optional<T>
	ret, found := manager.Lookup("Stream", "reduce", []string{"BinaryOperator"})
	if !found {
		t.Fatal("Stream.reduce with 1 arg: not found")
	}
	if ret.Name != "Optional" {
		t.Errorf("Stream.reduce with 1 arg: expected Optional, got %q", ret.Name)
	}

	// With 2 args → should pick reduce(T,BinaryOperator) → T
	ret, found = manager.Lookup("Stream", "reduce", []string{"String", "BinaryOperator"})
	if !found {
		t.Fatal("Stream.reduce with 2 args: not found")
	}
	if ret.Name != "T" {
		t.Errorf("Stream.reduce with 2 args: expected T, got %q", ret.Name)
	}
}

func TestExternalMethodManager_OverloadNilArgTypes(t *testing.T) {
	// U-20: nil argTypes should return first candidate without panic
	manager := java.NewExternalMethodManager(nil, "")
	ret, found := manager.Lookup("Stream", "reduce", nil)
	if !found {
		t.Fatal("Stream.reduce with nil argTypes: not found")
	}
	// Should return one of the overloads (not panic)
	if ret.Name != "Optional" && ret.Name != "T" {
		t.Errorf("Stream.reduce with nil argTypes: unexpected %q", ret.Name)
	}
}

func TestExternalMethodManager_EmptyTypeParams(t *testing.T) {
	// U-23: IntStream has empty TypeParams ("") → should be nil, not [""]
	manager := java.NewExternalMethodManager(nil, "")
	params := manager.LookupClassTypeParams("IntStream")
	if params != nil {
		t.Errorf("IntStream TypeParams: expected nil, got %v", params)
	}
	// LongStream same
	params = manager.LookupClassTypeParams("LongStream")
	if params != nil {
		t.Errorf("LongStream TypeParams: expected nil, got %v", params)
	}
}

func TestExternalMethodManager_FullQualifiedLookup(t *testing.T) {
	// U-12: Lookup with full qualified class name
	manager := java.NewExternalMethodManager(nil, "")
	ret, found := manager.Lookup("java.util.List", "get", []string{"int"})
	if !found {
		t.Fatal("java.util.List.get(int): not found")
	}
	if ret.Name != "T" {
		t.Errorf("java.util.List.get(int): expected T, got %q", ret.Name)
	}
}

func TestExternalMethodManager_ShortNameFallback(t *testing.T) {
	// U-13: Lookup with short class name falls back to shortNameIndex
	manager := java.NewExternalMethodManager(nil, "")
	ret, found := manager.Lookup("List", "get", []string{"int"})
	if !found {
		t.Fatal("List.get(int) short name fallback: not found")
	}
	if ret.Name != "T" {
		t.Errorf("List.get(int) short name fallback: expected T, got %q", ret.Name)
	}
}

func TestExternalMethodManager_UnknownMethod(t *testing.T) {
	// U-14: Unknown method returns false
	manager := java.NewExternalMethodManager(nil, "")
	_, found := manager.Lookup("List", "nonexistent", nil)
	if found {
		t.Error("List.nonexistent: expected not found")
	}
}

func TestExternalMethodManager_UnknownClass(t *testing.T) {
	// U-15: Unknown class returns false
	manager := java.NewExternalMethodManager(nil, "")
	_, found := manager.Lookup("UnknownClass", "get", nil)
	if found {
		t.Error("UnknownClass.get: expected not found")
	}
}

func TestExternalMethodManager_LookupClassTypeParams_FullQualified(t *testing.T) {
	// U-16: Full qualified class name
	manager := java.NewExternalMethodManager(nil, "")
	params := manager.LookupClassTypeParams("java.util.List")
	if len(params) != 1 || params[0] != "T" {
		t.Errorf("java.util.List TypeParams: expected [T], got %v", params)
	}
}

func TestExternalMethodManager_LookupClassTypeParams_ShortName(t *testing.T) {
	// U-17: Short class name fallback
	manager := java.NewExternalMethodManager(nil, "")
	params := manager.LookupClassTypeParams("List")
	if len(params) != 1 || params[0] != "T" {
		t.Errorf("List TypeParams short name: expected [T], got %v", params)
	}
}

func TestExternalMethodManager_LookupClassTypeParams_Unknown(t *testing.T) {
	// U-18: Unknown class returns nil
	manager := java.NewExternalMethodManager(nil, "")
	params := manager.LookupClassTypeParams("UnknownClass")
	if params != nil {
		t.Errorf("UnknownClass TypeParams: expected nil, got %v", params)
	}
}

func TestExternalMethodManager_LoadOrder(t *testing.T) {
	// U-22: builtin → framework → user, later overrides earlier
	tmpDir := t.TempDir()
	customDir := filepath.Join(tmpDir, ".fcg", "external")
	os.MkdirAll(customDir, 0755)
	// Override List.get to return "Object" instead of "T"
	os.WriteFile(filepath.Join(customDir, "override.json"), []byte(`{"java.util.List.get(int)":"Object"}`), 0644)

	manager := java.NewExternalMethodManager(nil, tmpDir)
	ret, found := manager.Lookup("List", "get", []string{"int"})
	if !found {
		t.Fatal("List.get after override: not found")
	}
	if ret.Name != "Object" {
		t.Errorf("List.get after override: expected Object, got %q", ret.Name)
	}
}
