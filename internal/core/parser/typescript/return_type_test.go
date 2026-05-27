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
