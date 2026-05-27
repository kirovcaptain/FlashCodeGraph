package typescript

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestExtractReturnType_TSGenericPreserved(t *testing.T) {
	code := `
// U-12: simple generic
export function getUsers(): Array<User> { return []; }
// U-13: Promise
export function fetchData(): Promise<Response> { return null as any; }
// U-14: nested generic
export function getMap(): Map<string, Array<User>> { return null as any; }
// U-15: union type (non-generic)
export function getValue(): string | null { return null; }
// U-16: non-generic
export function getName(): string { return ""; }
`
	result := parseTSFile(t, code, "src/service.ts")

	symbolMap := make(map[string]model.Symbol)
	for _, sym := range result.Symbols {
		if sym.Kind == "Function" {
			symbolMap[sym.Name] = sym
		}
	}

	tests := []struct {
		methodName       string
		expectedName     string
		expectedArgCount int
	}{
		{"getUsers", "Array", 1},
		{"fetchData", "Promise", 1},
		{"getMap", "Map", 2},
		{"getName", "string", 0},
	}

	for _, tc := range tests {
		sym, ok := symbolMap[tc.methodName]
		if !ok {
			t.Errorf("function %q not found", tc.methodName)
			continue
		}
		if len(sym.ReturnTypes) == 0 {
			t.Errorf("%s: no return types", tc.methodName)
			continue
		}
		returnType := sym.ReturnTypes[0]
		if returnType.Name != tc.expectedName {
			t.Errorf("%s: Name=%q, want %q", tc.methodName, returnType.Name, tc.expectedName)
		}
		if len(returnType.Args) != tc.expectedArgCount {
			t.Errorf("%s: Args count=%d, want %d", tc.methodName, len(returnType.Args), tc.expectedArgCount)
		}
	}

	// Verify Array<User> → Args[0] = {Name:"User"}
	if sym, ok := symbolMap["getUsers"]; ok && len(sym.ReturnTypes) > 0 && len(sym.ReturnTypes[0].Args) == 1 {
		if sym.ReturnTypes[0].Args[0].Name != "User" {
			t.Errorf("getUsers: arg should be User, got %q", sym.ReturnTypes[0].Args[0].Name)
		}
	}

	// Verify Map<string, Array<User>> → Args[1] = {Name:"Array", Args:[{Name:"User"}]}
	if sym, ok := symbolMap["getMap"]; ok && len(sym.ReturnTypes) > 0 && len(sym.ReturnTypes[0].Args) == 2 {
		secondArg := sym.ReturnTypes[0].Args[1]
		if secondArg.Name != "Array" || len(secondArg.Args) != 1 || secondArg.Args[0].Name != "User" {
			t.Errorf("getMap: second arg should be Array<User>, got %+v", secondArg)
		}
	}

	t.Log("✅ TS ReturnTypes preserve generic type arguments")
}

func TestExtractReturnType_ArrayType(t *testing.T) {
	code := `
// U-26: simple array type User[]
export function getUsers(): User[] { return []; }
// U-27: nested array User[][]
export function getMatrix(): User[][] { return []; }
// U-28: primitive array string[]
export function getNames(): string[] { return []; }
// U-29: generic Array<User> (existing behavior)
export function getList(): Array<User> { return []; }
// U-30: non-array string
export function getName(): string { return ""; }
// U-31: Promise<Response> (not affected)
export function fetchData(): Promise<Response> { return null as any; }
`
	result := parseTSFile(t, code, "src/arrays.ts")

	symbolMap := make(map[string]model.Symbol)
	for _, sym := range result.Symbols {
		if sym.Kind == "Function" {
			symbolMap[sym.Name] = sym
		}
	}

	tests := []struct {
		methodName       string
		expectedName     string
		expectedArgCount int
		firstArgName     string
	}{
		// U-26: User[] → Array<User>
		{"getUsers", "Array", 1, "User"},
		// U-27: User[][] → Array<Array<User>>
		{"getMatrix", "Array", 1, "Array"},
		// U-28: string[] → Array<string>
		{"getNames", "Array", 1, "string"},
		// U-29: Array<User> unchanged
		{"getList", "Array", 1, "User"},
		// U-30: string not affected
		{"getName", "string", 0, ""},
		// U-31: Promise<Response> not affected
		{"fetchData", "Promise", 1, "Response"},
	}

	for _, tc := range tests {
		sym, ok := symbolMap[tc.methodName]
		if !ok {
			t.Errorf("function %q not found", tc.methodName)
			continue
		}
		if len(sym.ReturnTypes) == 0 {
			t.Errorf("%s: no return types", tc.methodName)
			continue
		}
		returnType := sym.ReturnTypes[0]
		if returnType.Name != tc.expectedName {
			t.Errorf("%s: Name=%q, want %q", tc.methodName, returnType.Name, tc.expectedName)
		}
		if len(returnType.Args) != tc.expectedArgCount {
			t.Errorf("%s: Args count=%d, want %d", tc.methodName, len(returnType.Args), tc.expectedArgCount)
			continue
		}
		if tc.firstArgName != "" && len(returnType.Args) > 0 {
			if returnType.Args[0].Name != tc.firstArgName {
				t.Errorf("%s: Args[0].Name=%q, want %q", tc.methodName, returnType.Args[0].Name, tc.firstArgName)
			}
		}
	}

	// U-27 deep check: User[][] → Array<Array<User>>
	if sym, ok := symbolMap["getMatrix"]; ok && len(sym.ReturnTypes) > 0 {
		rt := sym.ReturnTypes[0]
		if rt.Name == "Array" && len(rt.Args) == 1 {
			inner := rt.Args[0]
			if inner.Name != "Array" || len(inner.Args) != 1 || inner.Args[0].Name != "User" {
				t.Errorf("getMatrix: expected Array<Array<User>>, got %+v", rt)
			}
		}
	}

	t.Log("✅ TS array_type (User[]) correctly converts to Array<T> structure")
}
