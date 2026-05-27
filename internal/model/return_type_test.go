package model

import "testing"

func TestParseReturnType(t *testing.T) {
	tests := []struct {
		input        string
		expectedName string
		expectedArgs []string // flattened arg names for simple verification
	}{
		// R-1: simple generic
		{"List<User>", "List", []string{"User"}},
		// R-2: nested generic
		{"Map<String, List<User>>", "Map", []string{"String", "List<User>"}},
		// R-3: non-generic
		{"String", "String", nil},
		// R-4: Optional
		{"Optional<User>", "Optional", []string{"User"}},
		// R-5: deeply nested
		{"Map<String, Map<Integer, List<Order>>>", "Map", []string{"String", "Map<Integer, List<Order>>"}},
		// R-6: type parameter
		{"T", "T", nil},
	}

	for _, tc := range tests {
		result := ParseReturnType(tc.input)
		if result.Name != tc.expectedName {
			t.Errorf("ParseReturnType(%q): name=%q, want %q", tc.input, result.Name, tc.expectedName)
		}
		if tc.expectedArgs == nil {
			if len(result.Args) != 0 {
				t.Errorf("ParseReturnType(%q): args=%v, want nil", tc.input, result.Args)
			}
		} else {
			if len(result.Args) != len(tc.expectedArgs) {
				t.Errorf("ParseReturnType(%q): got %d args, want %d", tc.input, len(result.Args), len(tc.expectedArgs))
				continue
			}
			for i, arg := range result.Args {
				formatted := FormatReturnType(ReturnType{Name: arg.Name, Args: arg.Args})
				if formatted != tc.expectedArgs[i] {
					t.Errorf("ParseReturnType(%q): arg[%d]=%q, want %q", tc.input, i, formatted, tc.expectedArgs[i])
				}
			}
		}
	}
	t.Log("✅ ParseReturnType handles simple, nested, and non-generic types")
}

func TestFormatReturnType(t *testing.T) {
	tests := []struct {
		input    ReturnType
		expected string
	}{
		{ReturnType{Name: "String"}, "String"},
		{ReturnType{Name: "List", Args: []TypeArg{{Name: "User"}}}, "List<User>"},
		{ReturnType{Name: "Map", Args: []TypeArg{{Name: "String"}, {Name: "List", Args: []TypeArg{{Name: "User"}}}}}, "Map<String, List<User>>"},
		{ReturnType{Name: "Optional", Args: []TypeArg{{Name: "User"}}}, "Optional<User>"},
		{ReturnType{Name: "T"}, "T"},
		{ReturnType{Name: "int"}, "int"},
	}

	for _, tc := range tests {
		result := FormatReturnType(tc.input)
		if result != tc.expected {
			t.Errorf("FormatReturnType(%v): got %q, want %q", tc.input, result, tc.expected)
		}
	}
	t.Log("✅ FormatReturnType correctly formats simple and nested generic types")
}

func TestStringsToReturnTypes(t *testing.T) {
	input := []string{"List<User>", "Map<String, List<Order>>", "String", "T"}
	result := StringsToReturnTypes(input)

	if len(result) != 4 {
		t.Fatalf("expected 4 results, got %d", len(result))
	}
	if result[0].Name != "List" || len(result[0].Args) != 1 || result[0].Args[0].Name != "User" {
		t.Errorf("result[0]: got %+v, want List<User>", result[0])
	}
	if result[1].Name != "Map" || len(result[1].Args) != 2 {
		t.Errorf("result[1]: got %+v, want Map<String, List<Order>>", result[1])
	}
	if result[2].Name != "String" || len(result[2].Args) != 0 {
		t.Errorf("result[2]: got %+v, want String", result[2])
	}
	if result[3].Name != "T" || len(result[3].Args) != 0 {
		t.Errorf("result[3]: got %+v, want T", result[3])
	}
	t.Log("✅ StringsToReturnTypes parses mixed generic and non-generic strings")
}
