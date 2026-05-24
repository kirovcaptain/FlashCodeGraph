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

	if names["UserService"] != "Class" {
		t.Fatalf("UserService expected class, got %s", names["UserService"])
	}
	if names["FindByID"] != "Function" {
		t.Fatalf("FindByID expected function, got %s", names["FindByID"])
	}
	if names["NewUserService"] != "Function" {
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
		if sym.Name == "Repository" && sym.Kind == "Interface" {
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
		if sym.Kind == "Function" {
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
			if len(sym.Params) != 4 {
				b, _ := json.Marshal(sym.Params)
				t.Fatalf("QueryEdges: expected 4 params, got %d: %s", len(sym.Params), b)
			}
			t.Log("✅ interface method params: return types not included in params")
			return
		}
	}
	t.Fatal("QueryEdges method not found")
}

func TestExtractFunction_TypeParams(t *testing.T) {
	tests := []struct {
		name           string
		code           string
		funcName       string
		expectedParams []string
	}{
		{
			name: "single generic function",
			code: `package main
func Identity[T any](v T) T { return v }`,
			funcName:       "Identity",
			expectedParams: []string{"T"},
		},
		{
			name: "multiple generic parameters",
			code: `package main
func Map[T any, U any](s []T, f func(T) U) []U { return nil }`,
			funcName:       "Map",
			expectedParams: []string{"T", "U"},
		},
		{
			name: "generic with constraint",
			code: `package main
func Max[T interface{ ~int | ~float64 }](a, b T) T { return a }`,
			funcName:       "Max",
			expectedParams: []string{"T"},
		},
		{
			name: "no generic parameters",
			code: `package main
func Save(user User) error { return nil }`,
			funcName:       "Save",
			expectedParams: nil,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			root, cleanup := parse([]byte(testCase.code))
			defer cleanup()
			result := &model.ParseResult{}
			file := scanner.ScannedFile{RelPath: "main.go", Language: "go"}
			Extract(root, []byte(testCase.code), file, result)

			var found *model.Symbol
			for i := range result.Symbols {
				if result.Symbols[i].Name == testCase.funcName {
					found = &result.Symbols[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("function %q not found in symbols", testCase.funcName)
			}
			if len(found.TypeParams) != len(testCase.expectedParams) {
				t.Fatalf("TypeParams length: got %d, want %d (got %v)", len(found.TypeParams), len(testCase.expectedParams), found.TypeParams)
			}
			for i, expected := range testCase.expectedParams {
				if found.TypeParams[i] != expected {
					t.Errorf("TypeParams[%d]: got %q, want %q", i, found.TypeParams[i], expected)
				}
			}
		})
	}
}

func TestExtractMethod_TypeParams_Go(t *testing.T) {
	// Note: Go methods on generic types don't have their own type_parameter_list;
	// the type params are on the struct definition. This test verifies no false positives.
	code := []byte(`package main
type Store[T any] struct{}
func (s *Store[T]) Get(id string) T { var zero T; return zero }
func (s *Store[T]) Save(item T) error { return nil }
`)
	root, cleanup := parse(code)
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "store.go", Language: "go"}
	Extract(root, code, file, result)

	for _, symbol := range result.Symbols {
		if symbol.Name == "Get" || symbol.Name == "Save" {
			if len(symbol.TypeParams) != 0 {
				t.Errorf("method %q should have no TypeParams (they belong to struct), got %v", symbol.Name, symbol.TypeParams)
			}
		}
	}
}

// --- Lambda extraction tests ---

func parseGoFile(t *testing.T, code string, filename string) *model.ParseResult {
	t.Helper()
	root, cleanup := parse([]byte(code))
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: filename, Language: "go"}
	Extract(root, []byte(code), file, result)
	return result
}

func TestExtractLambda_GoArgumentPosition(t *testing.T) {
	code := `package main

func process() {
    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        svc.Serve(w, r)
    })
}
`
	result := parseGoFile(t, code, "main.go")

	var lambdaSymbol *model.Symbol
	for i := range result.Symbols {
		if result.Symbols[i].Name == "lambda$1" {
			lambdaSymbol = &result.Symbols[i]
			break
		}
	}
	if lambdaSymbol == nil {
		t.Fatal("expected lambda$1 symbol")
	}
	if !lambdaSymbol.IsLambda {
		t.Error("expected IsLambda=true")
	}

	// Should have inner call from lambda to Serve
	var innerCall *model.RawCall
	for i := range result.Calls {
		if result.Calls[i].CallerName == lambdaSymbol.QualifiedName && result.Calls[i].CalledName == "Serve" {
			innerCall = &result.Calls[i]
			break
		}
	}
	if innerCall == nil {
		t.Fatal("expected RawCall from lambda$1 to Serve")
	}
}

func TestExtractLambda_GoDeclarative(t *testing.T) {
	code := `package main

func process() {
    handler := func() { svc.Run() }
    handler()
}
`
	result := parseGoFile(t, code, "main.go")

	var lambdaSymbol *model.Symbol
	for i := range result.Symbols {
		if result.Symbols[i].Name == "handler" && result.Symbols[i].IsLambda {
			lambdaSymbol = &result.Symbols[i]
			break
		}
	}
	if lambdaSymbol == nil {
		t.Fatal("expected declarative lambda symbol 'handler'")
	}

	// Should have TypeHint with LambdaSymbolID
	var hint *model.TypeBinding
	for i := range result.TypeHints {
		if result.TypeHints[i].VarName == "handler" && result.TypeHints[i].LambdaSymbolID != "" {
			hint = &result.TypeHints[i]
			break
		}
	}
	if hint == nil {
		t.Fatal("expected TypeHint with LambdaSymbolID for 'handler'")
	}

	// Should have inner call from handler to Run
	var innerCall *model.RawCall
	for i := range result.Calls {
		if result.Calls[i].CallerName == lambdaSymbol.QualifiedName && result.Calls[i].CalledName == "Run" {
			innerCall = &result.Calls[i]
			break
		}
	}
	if innerCall == nil {
		t.Fatal("expected RawCall from handler to Run")
	}
}

func TestExtractLambda_GoReturnFuncLiteralNotLost(t *testing.T) {
	code := `package main

func NewHandler() http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        svc.Handle(w, r)
    }
}
`
	result := parseGoFile(t, code, "main.go")

	// The inner call svc.Handle should still be extracted (not lost)
	var found bool
	for i := range result.Calls {
		if result.Calls[i].CalledName == "Handle" && result.Calls[i].ReceiverExpr == "svc" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected svc.Handle() call not to be lost in return func_literal")
	}
}

func TestExtractLambda_GoFuncLiteralNotLost(t *testing.T) {
	code := `package main

func process() {
    go func() { svc.Background() }()
}
`
	result := parseGoFile(t, code, "main.go")

	// go func(){...}() — inner call should not be lost
	var found bool
	for i := range result.Calls {
		if result.Calls[i].CalledName == "Background" && result.Calls[i].ReceiverExpr == "svc" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected svc.Background() call not to be lost in go func_literal")
	}
}

func TestExtractFuncLiteralParams_Argument(t *testing.T) {
	code := []byte(`package main

func process() {
    stream.Map(func(order *Order) string {
        return order.GetName()
    })
}
`)
	root, cleanup := parse(code)
	defer cleanup()
	result := &model.ParseResult{FilePath: "main.go", Language: "go"}
	file := scanner.ScannedFile{Path: "/test/main.go", RelPath: "main.go", Language: "go"}
	Extract(root, code, file, result)

	var lambdaSymbol *model.Symbol
	for i := range result.Symbols {
		if result.Symbols[i].IsLambda {
			lambdaSymbol = &result.Symbols[i]
			break
		}
	}
	if lambdaSymbol == nil {
		t.Fatal("expected lambda symbol")
	}
	if len(lambdaSymbol.Params) == 0 {
		t.Fatal("expected params to be extracted")
	}
	if lambdaSymbol.Params[0].Name != "order" {
		t.Errorf("expected param name 'order', got %q", lambdaSymbol.Params[0].Name)
	}
	if lambdaSymbol.Params[0].Type != "*Order" {
		t.Errorf("expected param type '*Order', got %q", lambdaSymbol.Params[0].Type)
	}
}

func TestExtractFuncLiteralParams_TypeHint(t *testing.T) {
	code := []byte(`package main

func process() {
    stream.Map(func(order *Order) string {
        return order.GetName()
    })
}
`)
	root, cleanup := parse(code)
	defer cleanup()
	result := &model.ParseResult{FilePath: "main.go", Language: "go"}
	file := scanner.ScannedFile{Path: "/test/main.go", RelPath: "main.go", Language: "go"}
	Extract(root, code, file, result)

	found := false
	for _, hint := range result.TypeHints {
		if hint.VarName == "order" && hint.TypeName == "*Order" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected TypeHint for func literal param 'order' with type '*Order'")
	}
}

func TestExtractLocalFuncLiteral_ParamTypeHint(t *testing.T) {
	code := []byte(`package main

import "net/http"

func setup() {
    handler := func(w http.ResponseWriter, r *http.Request) {
        w.Write(nil)
    }
    _ = handler
}
`)
	root, cleanup := parse(code)
	defer cleanup()
	result := &model.ParseResult{FilePath: "main.go", Language: "go"}
	file := scanner.ScannedFile{Path: "/test/main.go", RelPath: "main.go", Language: "go"}
	Extract(root, code, file, result)

	foundW := false
	foundR := false
	for _, hint := range result.TypeHints {
		if hint.VarName == "w" && hint.TypeName == "http.ResponseWriter" {
			foundW = true
		}
		if hint.VarName == "r" && hint.TypeName == "*http.Request" {
			foundR = true
		}
	}
	if !foundW {
		t.Error("expected TypeHint for 'w' with type 'http.ResponseWriter'")
	}
	if !foundR {
		t.Error("expected TypeHint for 'r' with type '*http.Request'")
	}
}
