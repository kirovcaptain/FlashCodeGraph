// Package resolver provides gRPC proto service name extraction and RPC method normalization.
package resolver

import "strings"

// ExtractProtoServiceName extracts the proto service name from stub constructor/registration functions.
//
//	Go:     NewUserServiceClient    → UserService
//	Go:     RegisterUserServiceServer → UserService
//	Java:   UserServiceGrpc         → UserService
//	Python: UserServiceStub         → UserService
//	TS:     UserServiceClient       → UserService
func ExtractProtoServiceName(funcName string) string {
	// Go / TS client
	if after, ok := strings.CutPrefix(funcName, "New"); ok {
		if svc, ok := strings.CutSuffix(after, "Client"); ok && svc != "" {
			return svc
		}
	}
	// Go server registration
	if after, ok := strings.CutPrefix(funcName, "Register"); ok {
		if svc, ok := strings.CutSuffix(after, "Server"); ok && svc != "" {
			return svc
		}
	}
	// Python stub
	if svc, ok := strings.CutSuffix(funcName, "Stub"); ok && svc != "" {
		return svc
	}
	// Python server registration: add_UserServiceServicer_to_server
	if after, ok := strings.CutPrefix(funcName, "add_"); ok {
		if svc, ok := strings.CutSuffix(after, "Servicer_to_server"); ok && svc != "" {
			return svc
		}
	}
	// Java: UserServiceGrpc
	if svc, ok := strings.CutSuffix(funcName, "Grpc"); ok && svc != "" {
		return svc
	}
	return ""
}

// NormalizeRPCMethod normalizes RPC method names for cross-language matching.
// Java uses lowerCamelCase (getUser), Go uses UpperCamelCase (GetUser).
// Normalizes to lowerCamelCase.
func NormalizeRPCMethod(method string) string {
	if method == "" {
		return ""
	}
	return strings.ToLower(method[:1]) + method[1:]
}

// StripGRPCScheme removes service discovery scheme prefixes from gRPC dial targets.
// "dns:///user-service:50051" → "user-service:50051"
// "consul:///user-service"    → "user-service"
// "user-service:50051"        → "user-service:50051" (unchanged)
func StripGRPCScheme(target string) string {
	if idx := strings.Index(target, ":///"); idx >= 0 {
		return target[idx+4:]
	}
	return target
}

// StripPort removes port from a host:port string.
// "user-service:50051" → "user-service"
func StripPort(hostPort string) string {
	if host, _, ok := strings.Cut(hostPort, ":"); ok {
		return host
	}
	return hostPort
}

// ParseGRPCInvokePath parses a gRPC Invoke path like "/UserService/GetUser".
// Returns (service, method).
func ParseGRPCInvokePath(path string) (string, string) {
	path = strings.TrimPrefix(path, "/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}
