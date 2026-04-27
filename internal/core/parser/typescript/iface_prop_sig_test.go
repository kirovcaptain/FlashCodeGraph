package typescript

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestExtract_InterfacePropertySignatureMethods(t *testing.T) {
	code := []byte(`
interface MyService {
    process(input: string): void;
    execute: (task: string) => boolean;
    count: number;
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "test.ts", Language: "typescript"}
	Extract(root, code, scanner.ScannedFile{RelPath: "test.ts", Language: "typescript"}, result)

	methods := make(map[string]bool)
	for _, sym := range result.Symbols {
		if sym.Kind == "Function" {
			methods[sym.Name] = true
		}
	}

	// method_signature: process → should be extracted
	if !methods["process"] {
		t.Error("process (method_signature) should be extracted")
	}
	// property_signature with arrow function type: execute → should be extracted
	if !methods["execute"] {
		t.Error("execute (property_signature with => type) should be extracted")
	}
	// property_signature with non-function type: count → should NOT be extracted
	if methods["count"] {
		t.Error("count (property_signature with number type) should NOT be extracted as Function")
	}
}
