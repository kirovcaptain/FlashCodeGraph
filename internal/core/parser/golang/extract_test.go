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
			retMap[sym.Name] = model.FormatReturnTypes(sym.ReturnTypes)
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

func TestGoPendingAssignment(t *testing.T) {
	code := []byte(`package main

import (
	"reflect"
	"strings"
)

func doSomething() interface{} { return nil }

func example(input string) {
	// call_result: bare function call
	result := doSomething()

	// call_result: import package function call
	index := strings.Index(input, ".")
	reflectValue := reflect.ValueOf(input)

	// method_call_result: variable method call
	elem := reflectValue.Index(0)
	trimmed := result.String()

	// field_access: field access
	length := result.Len

	// copy: variable assignment
	alias := trimmed

	// skip: multi-return (handled by extractMultiReturnHints)
	a, b := doSomething(), doSomething()

	// skip: underscore
	_ = doSomething()

	// skip: string literal
	s := "hello"

	// skip: func literal (handled by extractLocalFuncLiteral)
	handler := func() {}

	_, _, _, _, _, _, _ = index, reflectValue, elem, length, alias, a, b
	_, _ = s, handler
}
`)
	root, cleanup := parse(code)
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "pending.go", Language: "go"}
	Extract(root, code, file, result)

	// Build map of PendingAssignments by LHS
	type pendingInfo struct {
		Kind     string
		Callee   string
		Receiver string
		Method   string
		Field    string
		RHS      string
	}
	pendingMap := make(map[string]pendingInfo)
	for _, pending := range result.PendingAssignments {
		pendingMap[pending.LHS] = pendingInfo{
			Kind:     pending.Kind,
			Callee:   pending.Callee,
			Receiver: pending.Receiver,
			Method:   pending.Method,
			Field:    pending.Field,
			RHS:      pending.RHS,
		}
	}

	// UT-01: call_result bare function
	if p, ok := pendingMap["result"]; !ok || p.Kind != "call_result" || p.Callee != "doSomething" {
		t.Errorf("UT-01 result: expected call_result/doSomething, got %+v", pendingMap["result"])
	}

	// UT-02: call_result import package
	if p, ok := pendingMap["index"]; !ok || p.Kind != "call_result" || p.Callee != "strings.Index" {
		t.Errorf("UT-02 index: expected call_result/strings.Index, got %+v", pendingMap["index"])
	}
	if p, ok := pendingMap["reflectValue"]; !ok || p.Kind != "call_result" || p.Callee != "reflect.ValueOf" {
		t.Errorf("UT-02 reflectValue: expected call_result/reflect.ValueOf, got %+v", pendingMap["reflectValue"])
	}

	// UT-03: method_call_result
	if p, ok := pendingMap["elem"]; !ok || p.Kind != "method_call_result" || p.Receiver != "reflectValue" || p.Method != "Index" {
		t.Errorf("UT-03 elem: expected method_call_result/reflectValue.Index, got %+v", pendingMap["elem"])
	}
	if p, ok := pendingMap["trimmed"]; !ok || p.Kind != "method_call_result" || p.Receiver != "result" || p.Method != "String" {
		t.Errorf("UT-03 trimmed: expected method_call_result/result.String, got %+v", pendingMap["trimmed"])
	}

	// UT-04: field_access
	if p, ok := pendingMap["length"]; !ok || p.Kind != "field_access" || p.Receiver != "result" || p.Field != "Len" {
		t.Errorf("UT-04 length: expected field_access/result.Len, got %+v", pendingMap["length"])
	}

	// UT-05: copy
	if p, ok := pendingMap["alias"]; !ok || p.Kind != "copy" || p.RHS != "trimmed" {
		t.Errorf("UT-05 alias: expected copy/trimmed, got %+v", pendingMap["alias"])
	}

	// UT-06: skip multi-return (should NOT appear as PendingAssignment)
	if _, ok := pendingMap["a"]; ok {
		t.Error("UT-06: multi-return 'a' should not be in PendingAssignments")
	}
	if _, ok := pendingMap["b"]; ok {
		t.Error("UT-06: multi-return 'b' should not be in PendingAssignments")
	}

	// UT-08: skip underscore
	if _, ok := pendingMap["_"]; ok {
		t.Error("UT-08: underscore should not be in PendingAssignments")
	}

	// UT-09: skip string literal
	if _, ok := pendingMap["s"]; ok {
		t.Error("UT-09: string literal 's' should not be in PendingAssignments")
	}

	// UT-07: skip func literal
	if p, ok := pendingMap["handler"]; ok && p.Kind != "" {
		t.Errorf("UT-07: func literal 'handler' should not be in PendingAssignments, got %+v", p)
	}

	t.Logf("✅ Go PendingAssignment: %d assignments extracted", len(result.PendingAssignments))
}

func TestCobraRouteExtraction(t *testing.T) {
	code := []byte(`package cli

import "github.com/spf13/cobra"

func RegisterCommands() {
	mcpCmd := &cobra.Command{
		Use:   "mcp",
		Short: "MCP commands",
	}
	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start MCP server",
		RunE:  runMCPServe,
	}
	mcpCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(mcpCmd)
}

func runMCPServe(cmd *cobra.Command, args []string) error { return nil }
`)
	root, cleanup := parse(code)
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "cli/mcp.go", Language: "go"}
	Extract(root, code, file, result)

	// Should extract "mcp serve" CLI route
	var cliRoutes []model.RawRoute
	for _, route := range result.Routes {
		if route.Method == "CLI" {
			cliRoutes = append(cliRoutes, route)
		}
	}
	if len(cliRoutes) != 1 {
		t.Fatalf("expected 1 CLI route, got %d", len(cliRoutes))
	}
	if cliRoutes[0].PathPattern != "mcp serve" {
		t.Errorf("expected path 'mcp serve', got '%s'", cliRoutes[0].PathPattern)
	}
	if cliRoutes[0].Handlers[0] != "runMCPServe" {
		t.Errorf("expected handler 'runMCPServe', got '%s'", cliRoutes[0].Handlers[0])
	}
	if cliRoutes[0].Framework != "cobra" {
		t.Errorf("expected framework 'cobra', got '%s'", cliRoutes[0].Framework)
	}
	t.Log("✅ Cobra route extraction: mcp serve → runMCPServe [cobra]")
}

func TestMCPToolRouteExtraction(t *testing.T) {
	code := []byte(`package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type Server struct {
	mcpServer *server.MCPServer
}

func (srv *Server) RegisterTools() {
	srv.mcpServer.AddTool(mcp.NewTool("index_repository", mcp.WithDescription("Index a repo")), srv.handleIndexRepository)
	srv.mcpServer.AddTool(mcp.NewTool("query_symbol", mcp.WithDescription("Find symbol")), srv.handleQuerySymbol)
}

func (srv *Server) handleIndexRepository() {}
func (srv *Server) handleQuerySymbol() {}
`)
	root, cleanup := parse(code)
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "mcp/server.go", Language: "go"}
	Extract(root, code, file, result)

	var mcpRoutes []model.RawRoute
	for _, route := range result.Routes {
		if route.Method == "TOOL" {
			mcpRoutes = append(mcpRoutes, route)
		}
	}
	if len(mcpRoutes) != 2 {
		t.Fatalf("expected 2 MCP tool routes, got %d", len(mcpRoutes))
	}
	// Verify first tool
	found := false
	for _, route := range mcpRoutes {
		if route.PathPattern == "index_repository" && route.Handlers[0] == "handleIndexRepository" && route.Framework == "mcp" {
			found = true
		}
	}
	if !found {
		t.Error("expected MCP route: TOOL index_repository → handleIndexRepository [mcp]")
	}
	t.Log("✅ MCP tool route extraction: 2 tools extracted correctly")
}

func TestPackageLevelVarTypeExtraction(t *testing.T) {
	code := []byte(`package google

import "myapp/service"

var (
	s      = &service.GoogleService{}
	logger = log.Logger
)

func VerifyOrder() {
	s.VerifyOrder()
}
`)
	root, cleanup := parse(code)
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "handler.go", Language: "go"}
	Extract(root, code, file, result)

	// Should extract TypeBinding for s = &service.GoogleService{}
	var found bool
	for _, hint := range result.TypeHints {
		if hint.VarName == "s" && hint.TypeName == "service.GoogleService" && hint.Scope == "google" && hint.Tier == 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected TypeBinding for s → service.GoogleService (scope=google), got: %+v", result.TypeHints)
	}

	// Should NOT extract TypeBinding for logger (not &Type{} pattern)
	for _, hint := range result.TypeHints {
		if hint.VarName == "logger" {
			t.Errorf("unexpected TypeBinding for logger: %+v", hint)
		}
	}
	t.Log("✅ Package-level var type extraction: s → service.GoogleService [scope=google]")
}

func TestUrfaveCliRouteExtraction(t *testing.T) {
	code := []byte(`package main

import "github.com/urfave/cli/v2"

func main() {
	app := &cli.App{
		Commands: []*cli.Command{
			{
				Name:   "index",
				Action: runIndex,
			},
			{
				Name: "config",
				Subcommands: []*cli.Command{
					{
						Name:   "get",
						Action: runConfigGet,
					},
					{
						Name:   "set",
						Action: runConfigSet,
					},
				},
			},
			{
				Name: "version",
			},
		},
	}
	_ = app
}

func runIndex(c *cli.Context) error    { return nil }
func runConfigGet(c *cli.Context) error { return nil }
func runConfigSet(c *cli.Context) error { return nil }
`)
	root, cleanup := parse(code)
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "main.go", Language: "go"}
	Extract(root, code, file, result)

	var cliRoutes []model.RawRoute
	for _, route := range result.Routes {
		if route.Method == "CLI" {
			cliRoutes = append(cliRoutes, route)
		}
	}
	// Should extract: index, config get, config set (version has no Action → skip)
	if len(cliRoutes) != 3 {
		t.Fatalf("expected 3 urfave CLI routes, got %d: %+v", len(cliRoutes), cliRoutes)
	}
	paths := map[string]string{}
	for _, route := range cliRoutes {
		paths[route.PathPattern] = route.Handlers[0]
	}
	if paths["index"] != "runIndex" {
		t.Errorf("expected index→runIndex, got %s", paths["index"])
	}
	if paths["config get"] != "runConfigGet" {
		t.Errorf("expected 'config get'→runConfigGet, got %s", paths["config get"])
	}
	if paths["config set"] != "runConfigSet" {
		t.Errorf("expected 'config set'→runConfigSet, got %s", paths["config set"])
	}
	// version has no Action, should not generate Route
	if _, exists := paths["version"]; exists {
		t.Error("version should not generate Route (no Action)")
	}
	for _, route := range cliRoutes {
		if route.Framework != "urfave" {
			t.Errorf("expected framework 'urfave', got '%s'", route.Framework)
		}
	}
	t.Log("✅ Urfave CLI route extraction: index, config get, config set [urfave]")
}
