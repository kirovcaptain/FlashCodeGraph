package typescript

import (
	"fmt"
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/typeinfer"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestExtract_ArrayTypeAnnotation_TypeHint(t *testing.T) {
	code := []byte(`
function process() {
    const files: string[] = [];
    files.push('hello');

    const nums: number[] = [1, 2, 3];
    nums.push(4);

    const items: any[] = [];
    items.push({});
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "test.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/test.ts", RelPath: "test.ts", Language: "typescript"}
	Extract(root, code, file, result)

	fmt.Println("=== TypeHints ===")
	for _, h := range result.TypeHints {
		fmt.Printf("  scope=%q var=%q type=%q\n", h.Scope, h.VarName, h.TypeName)
	}

	infer := &typeinfer.TypeInfer{}
	env := infer.InferLocal(result)

	fmt.Println("\n=== TypeEnv Bindings ===")
	for key, info := range env.Bindings {
		fmt.Printf("  %s → %s\n", key, info.TypeName)
	}

	// Check files type
	info := typeinfer.LookupBindingInEnv(env, "test.process", "files")
	if info == nil {
		t.Fatal("files not found in TypeEnv")
	}
	t.Logf("files type = %q", info.TypeName)

	info = typeinfer.LookupBindingInEnv(env, "test.process", "nums")
	if info == nil {
		t.Fatal("nums not found in TypeEnv")
	}
	t.Logf("nums type = %q", info.TypeName)
}
