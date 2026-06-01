package typescript

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/typeinfer"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestExtract_ArrayTypeAnnotation_Normalized(t *testing.T) {
	code := []byte(`
function process() {
    const files: string[] = [];
    files.push('hello');

    const nums: number[] = [1, 2, 3];
    nums.push(4);

    const users: User[] = [];
    users.push(new User());

    const maps: Map<string, number> = new Map();
    maps.get('key');
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "test.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/test.ts", RelPath: "test.ts", Language: "typescript"}
	Extract(root, code, file, result)

	// Verify TypeHints are normalized: T[] → Array with TypeArgs
	hintsByVar := map[string]*model.TypeBinding{}
	for i := range result.TypeHints {
		hintsByVar[result.TypeHints[i].VarName] = &result.TypeHints[i]
	}

	// files: string[] → TypeName="Array", TypeArgs=[{Name:"string"}]
	if h := hintsByVar["files"]; h == nil {
		t.Fatal("files TypeHint not found")
	} else {
		if h.TypeName != "Array" {
			t.Errorf("files: expected TypeName='Array', got %q", h.TypeName)
		}
		if len(h.TypeArgs) != 1 || h.TypeArgs[0].Name != "string" {
			t.Errorf("files: expected TypeArgs=[{Name:'string'}], got %v", h.TypeArgs)
		}
	}

	// nums: number[] → TypeName="Array", TypeArgs=[{Name:"number"}]
	if h := hintsByVar["nums"]; h == nil {
		t.Fatal("nums TypeHint not found")
	} else {
		if h.TypeName != "Array" {
			t.Errorf("nums: expected TypeName='Array', got %q", h.TypeName)
		}
		if len(h.TypeArgs) != 1 || h.TypeArgs[0].Name != "number" {
			t.Errorf("nums: expected TypeArgs=[{Name:'number'}], got %v", h.TypeArgs)
		}
	}

	// users: User[] → TypeName="Array", TypeArgs=[{Name:"User"}]
	if h := hintsByVar["users"]; h == nil {
		t.Fatal("users TypeHint not found")
	} else {
		if h.TypeName != "Array" {
			t.Errorf("users: expected TypeName='Array', got %q", h.TypeName)
		}
		if len(h.TypeArgs) != 1 || h.TypeArgs[0].Name != "User" {
			t.Errorf("users: expected TypeArgs=[{Name:'User'}], got %v", h.TypeArgs)
		}
	}

	// maps: Map<string, number> → TypeName="Map" (should NOT change, regression check)
	if h := hintsByVar["maps"]; h == nil {
		t.Fatal("maps TypeHint not found")
	} else {
		if h.TypeName != "Map" {
			t.Errorf("maps: expected TypeName='Map', got %q", h.TypeName)
		}
	}

	// Verify TypeEnv also has normalized values
	infer := &typeinfer.TypeInfer{}
	env := infer.InferLocal(result)

	filesInfo := typeinfer.LookupBindingInEnv(env, "test.process", "files")
	if filesInfo == nil {
		t.Fatal("files not in TypeEnv")
	}
	if filesInfo.TypeName != "Array" {
		t.Errorf("TypeEnv files: expected 'Array', got %q", filesInfo.TypeName)
	}
	if len(filesInfo.TypeArgs) != 1 || filesInfo.TypeArgs[0].Name != "string" {
		t.Errorf("TypeEnv files TypeArgs: expected [{Name:'string'}], got %v", filesInfo.TypeArgs)
	}

	t.Log("✅ Array type annotations normalized: T[] → Array<T> with TypeArgs preserved")
}

func TestExtractParams_ArrayTypeAnnotation_Normalized(t *testing.T) {
	code := []byte(`
function process(items: number[], callback: Function) {
    items.push(1);
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "test.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/test.ts", RelPath: "test.ts", Language: "typescript"}
	Extract(root, code, file, result)

	var processFunc *model.Symbol
	for i := range result.Symbols {
		if result.Symbols[i].Name == "process" {
			processFunc = &result.Symbols[i]
			break
		}
	}
	if processFunc == nil {
		t.Fatal("function 'process' not found")
	}

	// items param: number[] → Type="Array", TypeArgs=[{Name:"number"}]
	itemsParam := processFunc.Params[0]
	if itemsParam.Name != "items" {
		t.Fatalf("expected first param 'items', got %q", itemsParam.Name)
	}
	if itemsParam.Type != "Array" {
		t.Errorf("items param: expected Type='Array', got %q", itemsParam.Type)
	}
	if len(itemsParam.TypeArgs) != 1 || itemsParam.TypeArgs[0].Name != "number" {
		t.Errorf("items param: expected TypeArgs=[{Name:'number'}], got %v", itemsParam.TypeArgs)
	}

	// callback param: Function → should NOT change
	callbackParam := processFunc.Params[1]
	if callbackParam.Type != "Function" {
		t.Errorf("callback param: expected Type='Function', got %q", callbackParam.Type)
	}

	t.Log("✅ Param array type normalized: number[] → Array<number>")
}
