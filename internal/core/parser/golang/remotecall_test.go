package golang

import (
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func parseGoCode(code []byte) (*tree_sitter.Node, func()) {
	parser := tree_sitter.NewParser()
	lang := tree_sitter.NewLanguage(tree_sitter_go.Language())
	parser.SetLanguage(lang)
	tree := parser.Parse(code, nil)
	return tree.RootNode(), func() { tree.Close(); parser.Close() }
}

func extractGoResult(code []byte) *model.ParseResult {
	root, cleanup := parseGoCode(code)
	defer cleanup()
	result := &model.ParseResult{FilePath: "main.go", Language: "go"}
	file := scanner.ScannedFile{Path: "/test/main.go", RelPath: "main.go", Language: "go"}
	Extract(root, code, file, result)
	return result
}

func TestGoHTTPGet(t *testing.T) {
	code := []byte(`package main
import "net/http"
func fetchUser() {
	http.Get("http://user-service/api/users")
}
`)
	result := extractGoResult(code)
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

func TestGoGRPCClientStub(t *testing.T) {
	code := []byte(`package main
func placeOrder() {
	conn, _ := grpc.NewClient("user-service:50051")
	client := pb.NewUserServiceClient(conn)
	client.GetUser(ctx, req)
}
`)
	result := extractGoResult(code)
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

func TestGoGRPCRegister(t *testing.T) {
	code := []byte(`package main
func main() {
	pb.RegisterUserServiceServer(grpcServer, &userImpl{})
}
`)
	result := extractGoResult(code)
	found := false
	for _, r := range result.Routes {
		if r.Framework == "grpc" && r.PathPattern == "UserService" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected gRPC route for UserService, got %+v", result.Routes)
	}
}

func TestGoHTTPNewRequest(t *testing.T) {
	code := []byte(`package main
import "net/http"
func deleteUser() {
	req, _ := http.NewRequest("DELETE", "http://user-service/api/users/1", nil)
}
`)
	result := extractGoResult(code)
	found := false
	for _, rc := range result.RemoteCalls {
		if rc.Protocol == "http" && rc.Method == "DELETE" && rc.TargetService == "user-service" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected HTTP DELETE to user-service via NewRequest, got %+v", result.RemoteCalls)
	}
}
