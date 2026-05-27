package java

import (
	"testing"
)

func TestSplitParams(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		// U-24: generic internal comma not split
		{"Function<T,R>,int", []string{"Function<T,R>", "int"}},
		// U-25: nested generics
		{"Map<K,V>,BiFunction<K,V,R>", []string{"Map<K,V>", "BiFunction<K,V,R>"}},
		// Simple single param
		{"int", []string{"int"}},
		// Multiple simple params
		{"int,String,boolean", []string{"int", "String", "boolean"}},
		// Empty string
		{"", []string{""}},
		// Deeply nested
		{"Map<String,List<Set<T>>>", []string{"Map<String,List<Set<T>>>"}},
	}

	for _, testCase := range tests {
		result := splitParams(testCase.input)
		if len(result) != len(testCase.expected) {
			t.Errorf("splitParams(%q): got %v (len=%d), want %v (len=%d)",
				testCase.input, result, len(result), testCase.expected, len(testCase.expected))
			continue
		}
		for i, param := range result {
			if param != testCase.expected[i] {
				t.Errorf("splitParams(%q)[%d]: got %q, want %q",
					testCase.input, i, param, testCase.expected[i])
			}
		}
	}
}

func TestParseMethodKey(t *testing.T) {
	tests := []struct {
		key            string
		value          string
		expectedClass  string
		expectedMethod string
		expectedParams []string
	}{
		// Standard method
		{"java.util.List.get(int)", "T", "java.util.List", "get", []string{"int"}},
		// No params
		{"java.util.List.size()", "int", "java.util.List", "size", nil},
		// Generic params
		{"java.util.stream.Stream.map(Function<T,R>)", "Stream<R>", "java.util.stream.Stream", "map", []string{"Function<T,R>"}},
		// Multiple params
		{"java.util.stream.Stream.reduce(T,BinaryOperator<T>)", "T", "java.util.stream.Stream", "reduce", []string{"T", "BinaryOperator<T>"}},
		// Short class name (TS-style)
		{"Array.map(Function<T,R>)", "Array<R>", "Array", "map", []string{"Function<T,R>"}},
		// Standalone function (no dot)
		{"useContext()", "T", "", "useContext", nil},
	}

	for _, testCase := range tests {
		entry := parseMethodKey(testCase.key, testCase.value)
		if entry.ClassName != testCase.expectedClass {
			t.Errorf("parseMethodKey(%q): ClassName=%q, want %q", testCase.key, entry.ClassName, testCase.expectedClass)
		}
		if entry.MethodName != testCase.expectedMethod {
			t.Errorf("parseMethodKey(%q): MethodName=%q, want %q", testCase.key, entry.MethodName, testCase.expectedMethod)
		}
		if len(entry.ParamTypes) != len(testCase.expectedParams) {
			t.Errorf("parseMethodKey(%q): ParamTypes=%v, want %v", testCase.key, entry.ParamTypes, testCase.expectedParams)
			continue
		}
		for i, param := range entry.ParamTypes {
			if param != testCase.expectedParams[i] {
				t.Errorf("parseMethodKey(%q): ParamTypes[%d]=%q, want %q", testCase.key, i, param, testCase.expectedParams[i])
			}
		}
		if entry.ReturnType != testCase.value {
			t.Errorf("parseMethodKey(%q): ReturnType=%q, want %q", testCase.key, entry.ReturnType, testCase.value)
		}
	}
}

func TestShortClassName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"java.util.List", "List"},
		{"java.util.stream.Stream", "Stream"},
		{"List", "List"},
		{"", ""},
	}
	for _, testCase := range tests {
		result := shortClassName(testCase.input)
		if result != testCase.expected {
			t.Errorf("shortClassName(%q): got %q, want %q", testCase.input, result, testCase.expected)
		}
	}
}

func TestLoadJSON_EmptyTypeParams(t *testing.T) {
	manager := &ExternalMethodManager{
		methods:         make(map[string]methodEntry),
		shortNameIndex:  make(map[string][]string),
		classTypeParams: make(map[string][]string),
		shortClassIndex: make(map[string][]string),
	}
	// Empty value should produce nil, not [""]
	manager.loadJSON([]byte(`{"java.util.stream.IntStream": ""}`), "test")
	params := manager.classTypeParams["java.util.stream.IntStream"]
	if params != nil {
		t.Errorf("empty TypeParams: expected nil, got %v", params)
	}
}

func TestLoadJSON_ClassAndMethodMixed(t *testing.T) {
	manager := &ExternalMethodManager{
		methods:         make(map[string]methodEntry),
		shortNameIndex:  make(map[string][]string),
		classTypeParams: make(map[string][]string),
		shortClassIndex: make(map[string][]string),
	}
	data := []byte(`{
		"java.util.List": "T",
		"java.util.List.get(int)": "T",
		"java.util.List.size()": "int"
	}`)
	manager.loadJSON(data, "test")

	// Class TypeParams
	params := manager.classTypeParams["java.util.List"]
	if len(params) != 1 || params[0] != "T" {
		t.Errorf("classTypeParams: expected [T], got %v", params)
	}

	// Methods
	if len(manager.methods) != 2 {
		t.Errorf("methods count: expected 2, got %d", len(manager.methods))
	}

	// Short name index
	if keys, ok := manager.shortNameIndex["List.get"]; !ok || len(keys) == 0 {
		t.Error("shortNameIndex[List.get] not populated")
	}
	if keys, ok := manager.shortClassIndex["List"]; !ok || len(keys) == 0 {
		t.Error("shortClassIndex[List] not populated")
	}
}
