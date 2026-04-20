package golang

import (
	"encoding/json"
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func parse(code []byte) (*tree_sitter.Node, func()) {
	parser := tree_sitter.NewParser()
	lang := tree_sitter.NewLanguage(tree_sitter_go.Language())
	parser.SetLanguage(lang)
	tree := parser.Parse(code, nil)
	return tree.RootNode(), func() { tree.Close(); parser.Close() }
}

func TestExtract_StructAndMethods(t *testing.T) {
	code := []byte(`package main

type UserService struct {
	db *DB
}

func (svc *UserService) FindByID(id int) {}
func (svc *UserService) Save(user User) {}
func NewUserService() *UserService { return &UserService{} }
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "service.go", Language: "go"}
	file := scanner.ScannedFile{Path: "/test/service.go", RelPath: "service.go", Language: "go"}
	Extract(root, code, file, result)

	names := map[string]string{}
	for _, sym := range result.Symbols {
		names[sym.Name] = sym.Kind
	}

	if names["UserService"] != "class" {
		t.Fatalf("UserService expected class, got %s", names["UserService"])
	}
	if names["FindByID"] != "function" {
		t.Fatalf("FindByID expected function, got %s", names["FindByID"])
	}
	if names["NewUserService"] != "function" {
		t.Fatalf("NewUserService expected function, got %s", names["NewUserService"])
	}
	t.Logf("✅ Go Extract: struct + methods + constructor, %d symbols", len(result.Symbols))
}

func TestExtract_Interface(t *testing.T) {
	code := []byte(`package main

type Repository interface {
	Find(id int) interface{}
	Save(item interface{})
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "repo.go", Language: "go"}
	file := scanner.ScannedFile{Path: "/test/repo.go", RelPath: "repo.go", Language: "go"}
	Extract(root, code, file, result)

	hasInterface := false
	for _, sym := range result.Symbols {
		if sym.Name == "Repository" && sym.Kind == "interface" {
			hasInterface = true
		}
	}
	if !hasInterface {
		t.Fatal("missing Repository interface")
	}
	t.Log("✅ Go Extract: interface")
}

func TestExtract_Imports(t *testing.T) {
	code := []byte(`package main

import (
	"fmt"
	"myapp/pkg/service"
)

func main() { fmt.Println("hi") }
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "main.go", Language: "go"}
	file := scanner.ScannedFile{Path: "/test/main.go", RelPath: "main.go", Language: "go"}
	Extract(root, code, file, result)

	if len(result.Imports) < 2 {
		t.Fatalf("expected at least 2 imports, got %d", len(result.Imports))
	}
	t.Logf("✅ Go Extract: %d imports", len(result.Imports))
}

func TestExtractReturnTypes_PackagePrefix(t *testing.T) {
	code := []byte(`package kuzu

func New(dbPath string) (*Store, error) { return nil, nil }
func GetAll() ([]Item, error) { return nil, nil }
func Simple() string { return "" }
func Multi() (Config, *Store, error) { return Config{}, nil, nil }
func WithImport() (context.Context, error) { return nil, nil }
func NoReturn() {}
`)
	root, cleanup := parse(code)
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "store.go", Language: "go"}
	Extract(root, code, file, result)

	// Build map: funcName → ReturnTypes
	retMap := make(map[string][]string)
	for _, sym := range result.Symbols {
		if sym.Kind == "function" {
			retMap[sym.Name] = sym.ReturnTypes
		}
	}

	tests := []struct {
		name     string
		expected []string
	}{
		{"New", []string{"*kuzu.Store", "error"}},
		{"GetAll", []string{"[]kuzu.Item", "error"}},
		{"Simple", []string{"string"}},
		{"Multi", []string{"kuzu.Config", "*kuzu.Store", "error"}},
		{"WithImport", []string{"context.Context", "error"}},
		{"NoReturn", nil},
	}

	for _, tt := range tests {
		got := retMap[tt.name]
		if len(got) != len(tt.expected) {
			t.Errorf("%s: expected %v, got %v", tt.name, tt.expected, got)
			continue
		}
		for i := range got {
			if got[i] != tt.expected[i] {
				t.Errorf("%s[%d]: expected %q, got %q", tt.name, i, tt.expected[i], got[i])
			}
		}
	}
	t.Log("✅ extractReturnTypes: package prefix applied correctly")
}

func TestExtractMultiReturnHints(t *testing.T) {
	code := []byte(`package cli

func createQuerier() (*service.Querier, storage.GraphStore, error) { return nil, nil, nil }

func run() {
	querier, store, err := createQuerier()
	_, s2, _ := createQuerier()
	_ = querier; _ = store; _ = err; _ = s2
}
`)
	root, cleanup := parse(code)
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "query.go", Language: "go"}
	Extract(root, code, file, result)

	// Check TypeHints for multi-return
	type mrHint struct {
		funcExpr string
		index    int
	}
	hintMap := make(map[string]mrHint)
	for _, h := range result.TypeHints {
		if h.MultiReturnOf != "" {
			hintMap[h.VarName] = mrHint{h.MultiReturnOf, h.ReturnIndex}
		}
	}

	if h := hintMap["querier"]; h.funcExpr != "createQuerier" || h.index != 0 {
		t.Errorf("querier: expected createQuerier[0], got %s[%d]", h.funcExpr, h.index)
	}
	if h := hintMap["store"]; h.funcExpr != "createQuerier" || h.index != 1 {
		t.Errorf("store: expected createQuerier[1], got %s[%d]", h.funcExpr, h.index)
	}
	if h := hintMap["err"]; h.funcExpr != "createQuerier" || h.index != 2 {
		t.Errorf("err: expected createQuerier[2], got %s[%d]", h.funcExpr, h.index)
	}
	if _, exists := hintMap["_"]; exists {
		t.Error("_ should not have TypeHint")
	}
	if h := hintMap["s2"]; h.funcExpr != "createQuerier" || h.index != 1 {
		t.Errorf("s2: expected createQuerier[1], got %s[%d]", h.funcExpr, h.index)
	}
	t.Log("✅ multi-return TypeHints: correct variables, _ skipped")
}

func TestExtract_InterfaceMethodParams(t *testing.T) {
	code := []byte(`package storage

type GraphStore interface {
	QueryEdges(ctx context.Context, nodeID string, relKind RelationKind, dir Direction) ([]Edge, error)
	Close() error
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "storage.go", Language: "go"}
	file := scanner.ScannedFile{Path: "/test/storage.go", RelPath: "storage.go", Language: "go"}
	Extract(root, code, file, result)

	for _, sym := range result.Symbols {
		if sym.Name == "QueryEdges" {
			var params []map[string]string
			json.Unmarshal([]byte(sym.Params), &params)
			if len(params) != 4 {
				t.Fatalf("QueryEdges: expected 4 params, got %d: %s", len(params), sym.Params)
			}
			t.Log("✅ interface method params: return types not included in params")
			return
		}
	}
	t.Fatal("QueryEdges method not found")
}
