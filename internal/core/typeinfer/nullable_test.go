package typeinfer

import "testing"

func TestStripNullableType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Fast path — no nullable
		{"User", "User"},
		{"string", "string"},
		{"", ""},

		// Pure nullable
		{"null", ""},
		{"undefined", ""},
		{"None", ""},

		// TS union with null
		{"User | null", "User"},
		{"null | User", "User"},
		{"string | undefined", "string"},
		{"undefined | string", "string"},
		{"User | null | undefined", "User"},

		// Non-nullable union — preserved
		{"string | number", "string | number"},

		// Python Optional
		{"Optional[User]", "User"},
		{"Optional[List[User]]", "List[User]"},
		{"Optional[Dict[str, Any]]", "Dict[str, Any]"},
	}

	for _, testCase := range tests {
		result := StripNullableType(testCase.input)
		if result != testCase.expected {
			t.Errorf("StripNullableType(%q) = %q, want %q", testCase.input, result, testCase.expected)
		}
	}
}
