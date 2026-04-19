package typescript

import (
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_ts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
	"github.com/liuymcn/flash-code-graph/internal/core/scanner"
	"github.com/liuymcn/flash-code-graph/internal/model"
)

func parseTSCode(code []byte) (*tree_sitter.Node, func()) {
	parser := tree_sitter.NewParser()
	lang := tree_sitter.NewLanguage(tree_sitter_ts.LanguageTypescript())
	parser.SetLanguage(lang)
	tree := parser.Parse(code, nil)
	return tree.RootNode(), func() { tree.Close(); parser.Close() }
}

func extractTSResult(code []byte) *model.ParseResult {
	root, cleanup := parseTSCode(code)
	defer cleanup()
	result := &model.ParseResult{FilePath: "app.ts", Language: "typescript"}
	file := scanner.ScannedFile{Path: "/test/app.ts", RelPath: "app.ts", Language: "typescript"}
	Extract(root, code, file, result)
	return result
}

func TestTSFetchCall(t *testing.T) {
	code := []byte(`
async function fetchUser() {
    const resp = await fetch("http://user-service/api/users");
}
`)
	result := extractTSResult(code)
	found := false
	for _, rc := range result.RemoteCalls {
		if rc.Protocol == "http" && rc.Method == "GET" && rc.TargetService == "user-service" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected fetch to user-service, got %+v", result.RemoteCalls)
	}
}

func TestTSAxiosGet(t *testing.T) {
	code := []byte(`
async function getOrders() {
    const resp = await axios.get("http://order-service/api/orders");
}
`)
	result := extractTSResult(code)
	found := false
	for _, rc := range result.RemoteCalls {
		if rc.Protocol == "http" && rc.Method == "GET" && rc.TargetService == "order-service" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected axios GET to order-service, got %+v", result.RemoteCalls)
	}
}

func TestTSGRPCClient(t *testing.T) {
	code := []byte(`
function getUser() {
    const client = new UserServiceClient("user-service:50051");
    client.getUser(request, (err, response) => {});
}
`)
	result := extractTSResult(code)
	found := false
	for _, rc := range result.RemoteCalls {
		if rc.Protocol == "grpc" && rc.TargetService == "UserService" && rc.Method == "getUser" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected gRPC call to UserService.getUser, got %+v", result.RemoteCalls)
	}
}

func TestTSGQLTemplate(t *testing.T) {
	code := []byte(`
function fetchUser() {
    const result = gql("query { getUser(id: 1) { name } }");
}
`)
	result := extractTSResult(code)
	found := false
	for _, rc := range result.RemoteCalls {
		if rc.Protocol == "graphql" && rc.Method == "getUser" && rc.TargetService == "Query" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected GraphQL Query.getUser from gql template, got %+v", result.RemoteCalls)
	}
}
