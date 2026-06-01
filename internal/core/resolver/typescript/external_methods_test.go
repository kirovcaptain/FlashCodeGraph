package typescript

import "testing"

func TestExternalMethodManager_Lookup(t *testing.T) {
	manager := NewExternalMethodManager([]string{"react", "vue", "express", "axios"}, "/nonexistent")

	tests := []struct {
		className  string
		methodName string
		expected   string
	}{
		{"Router", "get", "Router"},
		{"Router", "post", "Router"},
		{"Response", "json", "Response"},
		{"Response", "status", "Response"},
		{"axios", "get", "AxiosResponse"},
		{"AxiosInstance", "post", "AxiosResponse"},
	}

	for _, tc := range tests {
		result, found := manager.Lookup(tc.className, tc.methodName, nil)
		if !found {
			t.Errorf("Lookup(%s, %s): not found", tc.className, tc.methodName)
			continue
		}
		if result.Name != tc.expected {
			t.Errorf("Lookup(%s, %s): expected %q, got %q", tc.className, tc.methodName, tc.expected, result)
		}
	}
	t.Log("✅ ExternalMethodManager Lookup works for express/axios")
}

func TestExternalMethodManager_NotLoaded(t *testing.T) {
	// Only load react, should not find express methods
	manager := NewExternalMethodManager([]string{"react"}, "/nonexistent")

	_, found := manager.Lookup("Router", "get", nil)
	if found {
		t.Error("should not find express Router.get when only react is loaded")
	}
	t.Log("✅ ExternalMethodManager correctly filters by framework")
}

func TestExternalMethodManager_Nil(t *testing.T) {
	var manager *ExternalMethodManager

	_, found := manager.Lookup("Router", "get", nil)
	if found {
		t.Error("nil manager should return not found")
	}
	t.Log("✅ ExternalMethodManager nil-safe")
}

func TestExternalMethodManager_BuiltinArrayMethods(t *testing.T) {
	// Builtin globals.json should always load
	manager := NewExternalMethodManager(nil, "/nonexistent")

	tests := []struct {
		className  string
		methodName string
		expected   string
	}{
		{"Array", "map", "Array"},
		{"Array", "filter", "Array"},
		{"Array", "find", "T"},
		{"Array", "forEach", ""},
		{"Promise", "then", "Promise"},
		{"Promise", "catch", "Promise"},
		{"Map", "get", "V"},
		{"Map", "set", "SELF"},
		{"Map", "has", "boolean"},
		{"Set", "add", "SELF"},
		{"Set", "has", "boolean"},
	}

	for _, tc := range tests {
		result, found := manager.Lookup(tc.className, tc.methodName, nil)
		if !found {
			t.Errorf("Builtin Lookup(%s, %s): not found", tc.className, tc.methodName)
			continue
		}
		if result.Name != tc.expected {
			t.Errorf("Builtin Lookup(%s, %s): expected %q, got %q", tc.className, tc.methodName, tc.expected, result.Name)
		}
	}
}

func TestExternalMethodManager_BuiltinClassTypeParams(t *testing.T) {
	manager := NewExternalMethodManager(nil, "/nonexistent")

	tests := []struct {
		className string
		expected  []string
	}{
		{"Array", []string{"T"}},
		{"Promise", []string{"T"}},
		{"Map", []string{"K", "V"}},
		{"Set", []string{"T"}},
		{"UnknownClass", nil},
	}

	for _, tc := range tests {
		params := manager.LookupClassTypeParams(tc.className)
		if len(params) != len(tc.expected) {
			t.Errorf("LookupClassTypeParams(%s): got %v, want %v", tc.className, params, tc.expected)
			continue
		}
		for i, param := range params {
			if param != tc.expected[i] {
				t.Errorf("LookupClassTypeParams(%s)[%d]: got %q, want %q", tc.className, i, param, tc.expected[i])
			}
		}
	}
}

func TestExternalMethodManager_ArrayMapReturnTypeArgs(t *testing.T) {
	// Array.map should return Array<R> with Args
	manager := NewExternalMethodManager(nil, "/nonexistent")
	result, found := manager.Lookup("Array", "map", nil)
	if !found {
		t.Fatal("Array.map: not found")
	}
	if result.Name != "Array" {
		t.Fatalf("Array.map: expected Array, got %q", result.Name)
	}
	if len(result.Args) != 1 || result.Args[0].Name != "R" {
		t.Errorf("Array.map: expected Args=[{R}], got %v", result.Args)
	}
}

func TestExternalMethodManager_PromiseThenReturnTypeArgs(t *testing.T) {
	// Promise.then should return Promise<R> with Args
	manager := NewExternalMethodManager(nil, "/nonexistent")
	result, found := manager.Lookup("Promise", "then", nil)
	if !found {
		t.Fatal("Promise.then: not found")
	}
	if result.Name != "Promise" {
		t.Fatalf("Promise.then: expected Promise, got %q", result.Name)
	}
	if len(result.Args) != 1 || result.Args[0].Name != "R" {
		t.Errorf("Promise.then: expected Args=[{R}], got %v", result.Args)
	}
}

func TestExternalMethodManager_PrimitiveTypeLookup(t *testing.T) {
	manager := NewExternalMethodManager(nil, "/nonexistent")

	tests := []struct {
		className  string
		methodName string
		expectName string
	}{
		// Lowercase primitive → should match String/Number/Boolean methods
		{"string", "trim", "string"},
		{"string", "split", "Array"},
		{"string", "replace", "string"},
		{"string", "startsWith", "boolean"},
		{"number", "toFixed", "string"},
		// Uppercase should still work
		{"String", "trim", "string"},
		{"String", "split", "Array"},
	}

	for _, tc := range tests {
		result, found := manager.Lookup(tc.className, tc.methodName, nil)
		if !found {
			t.Errorf("Lookup(%s, %s): not found", tc.className, tc.methodName)
			continue
		}
		if result.Name != tc.expectName {
			t.Errorf("Lookup(%s, %s): expected %q, got %q", tc.className, tc.methodName, tc.expectName, result.Name)
		}
	}
	t.Log("✅ Primitive type lookup: string/number/boolean map to String/Number/Boolean")
}
