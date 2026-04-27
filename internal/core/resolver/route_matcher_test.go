package resolver

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestMatchRoute_ExactMatch(t *testing.T) {
	if !MatchRoute("/api/users", "GET", "/api/users", "GET", "") {
		t.Fatal("exact match should succeed")
	}
}

func TestMatchRoute_ParamMatch(t *testing.T) {
	if !MatchRoute("/api/users/{param}", "GET", "/api/users/{id}", "GET", "") {
		t.Fatal("param match should succeed")
	}
	if !MatchRoute("/api/users/{param}/orders/{param}", "GET", "/api/users/{id}/orders/{orderId}", "GET", "") {
		t.Fatal("multi-param match should succeed")
	}
}

func TestMatchRoute_MethodMismatch(t *testing.T) {
	if MatchRoute("/api/users", "GET", "/api/users", "POST", "") {
		t.Fatal("different methods should not match")
	}
}

func TestMatchRoute_SegmentCountMismatch(t *testing.T) {
	if MatchRoute("/api/users", "GET", "/api/users/{id}", "GET", "") {
		t.Fatal("different segment count should not match")
	}
}

func TestMatchRoute_CatchAll(t *testing.T) {
	if !MatchRoute("/api/files/a/b/c", "GET", "/api/files/**", "GET", "") {
		t.Fatal("catch-all ** should match")
	}
	if !MatchRoute("/api/docs/x/y", "GET", "/api/docs/[...slug]", "GET", "") {
		t.Fatal("catch-all [...slug] should match")
	}
	if MatchRoute("/api", "GET", "/api/files/**", "GET", "") {
		t.Fatal("catch-all should require prefix segments to match")
	}
}

func TestMatchRoute_EmptyMethod(t *testing.T) {
	if !MatchRoute("/api/users", "", "/api/users", "GET", "") {
		t.Fatal("empty method should match any")
	}
}

func TestFindMatchingRoutes(t *testing.T) {
	routes := []model.Node{
		{ID: "r1", Properties: map[string]any{"method": "GET", "path_pattern": "/api/users/{id}", "framework": ""}},
		{ID: "r2", Properties: map[string]any{"method": "POST", "path_pattern": "/api/users", "framework": ""}},
		{ID: "r3", Properties: map[string]any{"method": "GET", "path_pattern": "/api/orders/{id}", "framework": ""}},
	}

	matched := FindMatchingRoutes("/api/users/{param}", "GET", routes)
	if len(matched) != 1 || matched[0] != "r1" {
		t.Fatalf("expected [r1], got %v", matched)
	}

	matched = FindMatchingRoutes("/api/users", "POST", routes)
	if len(matched) != 1 || matched[0] != "r2" {
		t.Fatalf("expected [r2], got %v", matched)
	}

	matched = FindMatchingRoutes("/api/products", "GET", routes)
	if len(matched) != 0 {
		t.Fatalf("expected no match, got %v", matched)
	}
}

func TestMatchRoute_gRPC(t *testing.T) {
	// Exact match on service + method
	if !MatchRoute("UserService", "GetUser", "UserService", "GetUser", "grpc") {
		t.Fatal("gRPC exact match should succeed")
	}
	// Case-insensitive
	if !MatchRoute("UserService", "getUser", "UserService", "GetUser", "grpc") {
		t.Fatal("gRPC case-insensitive match should succeed")
	}
	// Different service
	if MatchRoute("OrderService", "GetUser", "UserService", "GetUser", "grpc") {
		t.Fatal("gRPC different service should not match")
	}
	// ServiceName/MethodName format (from RawRemoteCall.TargetURL)
	if !MatchRoute("UserService/GetUser", "GetUser", "UserService", "GetUser", "grpc") {
		t.Fatal("gRPC ServiceName/MethodName format should match")
	}
	if !MatchRoute("UserService/getUser", "getUser", "UserService", "GetUser", "grpc") {
		t.Fatal("gRPC ServiceName/MethodName case-insensitive should match")
	}
	if MatchRoute("OrderService/GetUser", "GetUser", "UserService", "GetUser", "grpc") {
		t.Fatal("gRPC ServiceName/MethodName different service should not match")
	}
}

func TestMatchRoute_GraphQL(t *testing.T) {
	if !MatchRoute("Query", "getUser", "Query", "getUser", "graphql") {
		t.Fatal("GraphQL exact match should succeed")
	}
	if !MatchRoute("Mutation", "CreateUser", "Mutation", "createUser", "graphql") {
		t.Fatal("GraphQL case-insensitive match should succeed")
	}
	if MatchRoute("Query", "getUser", "Mutation", "getUser", "graphql") {
		t.Fatal("GraphQL different type should not match")
	}
}

func TestMatchRoute_Dubbo(t *testing.T) {
	if !MatchRoute("com.example.api.UserService", "getUser", "com.example.api.UserService", "getUser", "dubbo") {
		t.Fatal("Dubbo exact match should succeed")
	}
	// Dubbo is case-sensitive
	if MatchRoute("com.example.api.UserService", "GetUser", "com.example.api.UserService", "getUser", "dubbo") {
		t.Fatal("Dubbo should be case-sensitive")
	}
}
