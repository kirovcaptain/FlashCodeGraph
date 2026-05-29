package defparser

import "testing"

func TestParseProtoServices_Basic(t *testing.T) {
	content := `syntax = "proto3";
package com.example.payment;

service PaymentService {
  rpc Pay(PayRequest) returns (PayResponse);
  rpc Refund(RefundRequest) returns (RefundResponse);
}`
	services := ParseProtoServices(content)
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].Name != "PaymentService" {
		t.Errorf("expected PaymentService, got %s", services[0].Name)
	}
	if len(services[0].Methods) != 2 {
		t.Fatalf("expected 2 methods, got %d", len(services[0].Methods))
	}
	if services[0].Methods[0].Name != "Pay" {
		t.Errorf("expected Pay, got %s", services[0].Methods[0].Name)
	}
	if services[0].Methods[1].Name != "Refund" {
		t.Errorf("expected Refund, got %s", services[0].Methods[1].Name)
	}
}

func TestParseProtoServices_MultiService(t *testing.T) {
	content := `syntax = "proto3";

service ServiceA {
  rpc MethodA(Req) returns (Res);
}

service ServiceB {
  rpc MethodB1(Req) returns (Res);
  rpc MethodB2(Req) returns (Res);
}`
	services := ParseProtoServices(content)
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
	if services[0].Name != "ServiceA" || len(services[0].Methods) != 1 {
		t.Errorf("ServiceA: name=%s methods=%d", services[0].Name, len(services[0].Methods))
	}
	if services[1].Name != "ServiceB" || len(services[1].Methods) != 2 {
		t.Errorf("ServiceB: name=%s methods=%d", services[1].Name, len(services[1].Methods))
	}
}

func TestParseProtoServices_Comments(t *testing.T) {
	content := `syntax = "proto3";

// service FakeService { rpc Fake(Req) returns (Res); }

/* service BlockComment {
   rpc Also(Req) returns (Res);
} */

service RealService {
  // rpc CommentedOut(Req) returns (Res);
  rpc Active(Req) returns (Res);
}`
	services := ParseProtoServices(content)
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].Name != "RealService" {
		t.Errorf("expected RealService, got %s", services[0].Name)
	}
	if len(services[0].Methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(services[0].Methods))
	}
	if services[0].Methods[0].Name != "Active" {
		t.Errorf("expected Active, got %s", services[0].Methods[0].Name)
	}
}

func TestParseProtoServices_NestedBraces(t *testing.T) {
	content := `syntax = "proto3";
package gateway.v1;

service GatewayService {
  rpc GetUser(GetUserRequest) returns (UserResponse) {
    option (google.api.http) = {
      get: "/v1/users/{user_id}"
    };
  }
  rpc CreateUser(CreateUserRequest) returns (UserResponse) {
    option (google.api.http) = {
      post: "/v1/users"
      body: "*"
    };
  }
}`
	services := ParseProtoServices(content)
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if len(services[0].Methods) != 2 {
		t.Fatalf("expected 2 methods, got %d", len(services[0].Methods))
	}
	if services[0].Methods[0].Name != "GetUser" {
		t.Errorf("expected GetUser, got %s", services[0].Methods[0].Name)
	}
	if services[0].Methods[1].Name != "CreateUser" {
		t.Errorf("expected CreateUser, got %s", services[0].Methods[1].Name)
	}
}

func TestParseProtoServices_NoService(t *testing.T) {
	content := `syntax = "proto3";
message UserRequest {
  string name = 1;
}`
	services := ParseProtoServices(content)
	if len(services) != 0 {
		t.Fatalf("expected 0 services, got %d", len(services))
	}
}

func TestProtoParser_Detect(t *testing.T) {
	p := &ProtoParser{}
	if !p.Detect([]byte("service Foo { rpc Bar(Req) returns (Res); }")) {
		t.Error("expected Detect to return true for proto with service+rpc")
	}
	if p.Detect([]byte("message Foo { string name = 1; }")) {
		t.Error("expected Detect to return false for proto without service")
	}
}

func TestProtoParser_Parse(t *testing.T) {
	p := &ProtoParser{}
	content := []byte(`syntax = "proto3";
service PaymentService {
  rpc Pay(PayRequest) returns (PayResponse);
  rpc Query(QueryRequest) returns (QueryResponse);
}`)
	result := p.Parse(content, "proto/payment.proto")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(result.Routes))
	}
	r := result.Routes[0]
	if r.PathPattern != "PaymentService" {
		t.Errorf("expected PathPattern=PaymentService, got %s", r.PathPattern)
	}
	if r.Method != "Pay" {
		t.Errorf("expected Method=Pay, got %s", r.Method)
	}
	if r.Framework != "grpc" {
		t.Errorf("expected Framework=grpc, got %s", r.Framework)
	}
	if r.Handlers[len(r.Handlers)-1] != "PaymentService.Pay" {
		t.Errorf("expected HandlerName=PaymentService.Pay, got %s", r.Handlers[len(r.Handlers)-1])
	}
}

func TestParseProtoServices_MalformedUnclosed(t *testing.T) {
	content := `syntax = "proto3";
service IncompleteService {
  rpc SomeMethod(Req) returns (Res);
`
	services := ParseProtoServices(content)
	if len(services) != 0 {
		t.Fatalf("expected 0 services for unclosed brace, got %d", len(services))
	}
}
