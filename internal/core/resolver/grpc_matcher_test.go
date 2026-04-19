package resolver

import "testing"

func TestExtractProtoServiceName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"NewUserServiceClient", "UserService"},
		{"NewOrderServiceClient", "OrderService"},
		{"RegisterUserServiceServer", "UserService"},
		{"UserServiceStub", "UserService"},
		{"add_UserServiceServicer_to_server", "UserService"},
		{"UserServiceGrpc", "UserService"},
		{"NewClient", ""},       // no service name
		{"RandomFunction", ""},  // no match
		{"", ""},
	}
	for _, tt := range tests {
		got := ExtractProtoServiceName(tt.input)
		if got != tt.want {
			t.Errorf("ExtractProtoServiceName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeRPCMethod(t *testing.T) {
	tests := []struct{ input, want string }{
		{"GetUser", "getUser"},
		{"getUser", "getUser"},
		{"CreateOrder", "createOrder"},
		{"", ""},
	}
	for _, tt := range tests {
		got := NormalizeRPCMethod(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeRPCMethod(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestStripGRPCScheme(t *testing.T) {
	tests := []struct{ input, want string }{
		{"dns:///user-service:50051", "user-service:50051"},
		{"consul:///user-service", "user-service"},
		{"etcd:///user-service", "user-service"},
		{"user-service:50051", "user-service:50051"},
		{"user-service", "user-service"},
	}
	for _, tt := range tests {
		got := StripGRPCScheme(tt.input)
		if got != tt.want {
			t.Errorf("StripGRPCScheme(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestStripPort(t *testing.T) {
	tests := []struct{ input, want string }{
		{"user-service:50051", "user-service"},
		{"user-service", "user-service"},
		{"localhost:8080", "localhost"},
	}
	for _, tt := range tests {
		got := StripPort(tt.input)
		if got != tt.want {
			t.Errorf("StripPort(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseGRPCInvokePath(t *testing.T) {
	tests := []struct {
		input       string
		wantSvc     string
		wantMethod  string
	}{
		{"/UserService/GetUser", "UserService", "GetUser"},
		{"/OrderService/CreateOrder", "OrderService", "CreateOrder"},
		{"UserService/GetUser", "UserService", "GetUser"},
		{"invalid", "", ""},
		{"", "", ""},
	}
	for _, tt := range tests {
		svc, method := ParseGRPCInvokePath(tt.input)
		if svc != tt.wantSvc || method != tt.wantMethod {
			t.Errorf("ParseGRPCInvokePath(%q) = (%q, %q), want (%q, %q)",
				tt.input, svc, method, tt.wantSvc, tt.wantMethod)
		}
	}
}
