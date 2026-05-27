package java

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestLookupMethodReturn_Structured(t *testing.T) {
	manager := NewExternalMethodManager(nil, "")
	helper := NewHelper(nil, manager)

	tests := []struct {
		typeName     string
		methodName   string
		expectedName string
		expectedArgs int
		expectFound  bool
	}{
		// U-1: List.stream → Stream<T>
		{"List", "stream", "Stream", 1, true},
		// U-2: List.get → T
		{"List", "get", "T", 0, true},
		// U-3: Stream.forEach → ""
		{"Stream", "forEach", "", 0, true},
		// U-4: StringBuilder.append → SELF
		{"StringBuilder", "append", "SELF", 0, true},
		// U-5: Map.get → V
		{"Map", "get", "V", 0, true},
		// U-6: Map.entrySet → Set<Entry<K, V>>
		{"Map", "entrySet", "Set", 1, true},
		// U-7: Optional.filter → Optional<T>
		{"Optional", "filter", "Optional", 1, true},
		// U-8: CompletableFuture.thenApply → CompletableFuture<T>
		{"CompletableFuture", "thenApply", "CompletableFuture", 1, true},
		// U-9: unknown method
		{"List", "unknown", "", 0, false},
		// U-10: Stream.filter → Stream<T>
		{"Stream", "filter", "Stream", 1, true},
	}

	for _, testCase := range tests {
		returnType, found := helper.LookupMethodReturn(testCase.typeName, testCase.methodName)
		if found != testCase.expectFound {
			t.Errorf("LookupMethodReturn(%s, %s): found=%v, want %v", testCase.typeName, testCase.methodName, found, testCase.expectFound)
			continue
		}
		if !found {
			continue
		}
		if returnType.Name != testCase.expectedName {
			t.Errorf("LookupMethodReturn(%s, %s): Name=%q, want %q", testCase.typeName, testCase.methodName, returnType.Name, testCase.expectedName)
		}
		if len(returnType.Args) != testCase.expectedArgs {
			t.Errorf("LookupMethodReturn(%s, %s): Args count=%d, want %d", testCase.typeName, testCase.methodName, len(returnType.Args), testCase.expectedArgs)
		}
	}

	// Verify U-6 nested args: Map.entrySet → Set<Entry<K, V>>
	returnType, _ := helper.LookupMethodReturn("Map", "entrySet")
	if len(returnType.Args) == 1 {
		entryArg := returnType.Args[0]
		if entryArg.Name != "Entry" || len(entryArg.Args) != 2 {
			t.Errorf("Map.entrySet: expected Entry<K,V>, got %+v", entryArg)
		}
	}

	// Verify U-1 args: List.stream → Stream<T>, T is the arg
	returnType, _ = helper.LookupMethodReturn("List", "stream")
	if len(returnType.Args) == 1 && returnType.Args[0].Name != "T" {
		t.Errorf("List.stream: expected arg T, got %q", returnType.Args[0].Name)
	}

	t.Log("✅ LookupMethodReturn returns structured ReturnType with Args")
}

func TestLookupClassTypeParams(t *testing.T) {
	helper := NewHelper(nil, NewExternalMethodManager(nil, ""))

	tests := []struct {
		typeName       string
		expectedParams []string
	}{
		{"List", []string{"T"}},
		{"Map", []string{"K", "V"}},
		{"Optional", []string{"T"}},
		{"Stream", []string{"T"}},
		{"CompletableFuture", []string{"T"}},
		{"String", nil},
	}

	for _, testCase := range tests {
		params := helper.LookupClassTypeParams(testCase.typeName)
		if len(params) != len(testCase.expectedParams) {
			t.Errorf("LookupClassTypeParams(%s): got %v, want %v", testCase.typeName, params, testCase.expectedParams)
			continue
		}
		for i, param := range params {
			if param != testCase.expectedParams[i] {
				t.Errorf("LookupClassTypeParams(%s)[%d]: got %q, want %q", testCase.typeName, i, param, testCase.expectedParams[i])
			}
		}
	}
	t.Log("✅ LookupClassTypeParams returns correct TypeParams for JDK classes")
}

func TestSubstituteTypeArgs_ViaParseReturnType(t *testing.T) {
	// Verify that ParseReturnType correctly handles the JDK table format
	tests := []struct {
		input        string
		expectedName string
		expectedArgs []string
	}{
		// U-11: Stream<T> → base=Stream, args=[T]
		{"Stream<T>", "Stream", []string{"T"}},
		// U-12: Set<Entry<K, V>> → base=Set, args=[Entry<K, V>]
		{"Set<Entry<K, V>>", "Set", []string{"Entry<K, V>"}},
		// U-13: non-generic
		{"String", "String", nil},
	}

	for _, testCase := range tests {
		result := model.ParseReturnType(testCase.input)
		if result.Name != testCase.expectedName {
			t.Errorf("ParseReturnType(%q): Name=%q, want %q", testCase.input, result.Name, testCase.expectedName)
		}
		if testCase.expectedArgs == nil {
			if len(result.Args) != 0 {
				t.Errorf("ParseReturnType(%q): expected no args, got %v", testCase.input, result.Args)
			}
		} else if len(result.Args) != len(testCase.expectedArgs) {
			t.Errorf("ParseReturnType(%q): args count=%d, want %d", testCase.input, len(result.Args), len(testCase.expectedArgs))
		}
	}
	t.Log("✅ ParseReturnType correctly parses JDK table format strings")
}
