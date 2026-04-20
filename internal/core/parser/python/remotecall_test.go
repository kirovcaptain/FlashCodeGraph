package python

import (
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func parsePyCode(code []byte) (*tree_sitter.Node, func()) {
	parser := tree_sitter.NewParser()
	lang := tree_sitter.NewLanguage(tree_sitter_python.Language())
	parser.SetLanguage(lang)
	tree := parser.Parse(code, nil)
	return tree.RootNode(), func() { tree.Close(); parser.Close() }
}

func extractPyResult(code []byte) *model.ParseResult {
	root, cleanup := parsePyCode(code)
	defer cleanup()
	result := &model.ParseResult{FilePath: "app.py", Language: "python"}
	file := scanner.ScannedFile{Path: "/test/app.py", RelPath: "app.py", Language: "python"}
	Extract(root, code, file, result)
	return result
}

func TestPythonRequestsGet(t *testing.T) {
	code := []byte(`
import requests

def fetch_user():
    resp = requests.get("http://user-service/api/users")
`)
	result := extractPyResult(code)
	found := false
	for _, rc := range result.RemoteCalls {
		if rc.Protocol == "http" && rc.Method == "GET" && rc.TargetService == "user-service" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected HTTP GET to user-service, got %+v", result.RemoteCalls)
	}
}

func TestPythonHttpxPost(t *testing.T) {
	code := []byte(`
import httpx

def create_order():
    resp = httpx.post("http://order-service/api/orders")
`)
	result := extractPyResult(code)
	found := false
	for _, rc := range result.RemoteCalls {
		if rc.Protocol == "http" && rc.Method == "POST" && rc.TargetService == "order-service" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected HTTP POST to order-service, got %+v", result.RemoteCalls)
	}
}

func TestPythonGRPCStub(t *testing.T) {
	code := []byte(`
import grpc
import user_pb2_grpc

def get_user():
    channel = grpc.insecure_channel("user-service:50051")
    stub = user_pb2_grpc.UserServiceStub(channel)
    response = stub.GetUser(request)
`)
	result := extractPyResult(code)
	found := false
	for _, rc := range result.RemoteCalls {
		if rc.Protocol == "grpc" && rc.TargetService == "UserService" && rc.Method == "GetUser" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected gRPC call to UserService.GetUser, got %+v", result.RemoteCalls)
	}
}

func TestPythonStrawberryGraphQL(t *testing.T) {
	code := []byte(`
import strawberry

@strawberry.type
class Query:
    @strawberry.field
    def get_user(self, id: int) -> str:
        return "user"
`)
	result := extractPyResult(code)
	found := false
	for _, r := range result.Routes {
		if r.Framework == "graphql" && r.PathPattern == "Query" && r.Method == "get_user" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected GraphQL Query.get_user route, got %+v", result.Routes)
	}
}
