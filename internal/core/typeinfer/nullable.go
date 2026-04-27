package typeinfer

import "strings"

// nullableKeywords are type names that represent null/undefined/none values.
var nullableKeywords = map[string]bool{
	"null":      true,
	"undefined": true,
	"None":      true,
	"void":      true,
}

// StripNullableType removes nullable markers from a type name.
// Handles:
//   - TS union: "User | null" → "User", "string | undefined" → "string", "User | null | undefined" → "User"
//   - Python Optional: "Optional[User]" → "User", "Optional[List[User]]" → "List[User]"
//   - Pure nullable: "null", "undefined", "None" → ""
//   - Non-nullable: "User" → "User" (fast path, returned as-is)
func StripNullableType(typeName string) string {
	if typeName == "" {
		return ""
	}

	// Fast path: no nullable markers
	if !strings.Contains(typeName, "|") && !strings.HasPrefix(typeName, "Optional[") {
		if nullableKeywords[typeName] {
			return ""
		}
		return typeName
	}

	// Python Optional[X] → X
	if strings.HasPrefix(typeName, "Optional[") && strings.HasSuffix(typeName, "]") {
		inner := typeName[len("Optional[") : len(typeName)-1]
		if inner != "" {
			return inner
		}
		return ""
	}

	// TS union: split by "|", filter out nullable parts
	parts := strings.Split(typeName, "|")
	var nonNullableParts []string
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" && !nullableKeywords[trimmed] {
			nonNullableParts = append(nonNullableParts, trimmed)
		}
	}
	if len(nonNullableParts) == 0 {
		return ""
	}
	if len(nonNullableParts) == 1 {
		return nonNullableParts[0]
	}
	// Multiple non-nullable types remain (e.g. "string | number") — return as-is
	return typeName
}
