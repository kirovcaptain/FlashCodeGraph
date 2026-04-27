// Package crossindex manages cross-project symbol and route registration
// for cross-service call chain analysis.
package crossindex

import "strings"

// RouteRole constants.
const (
	RoleProvider = "provider"
	RoleConsumer = "consumer"
)

// ConsumerFrameworks lists frameworks that represent outbound RPC calls.
// All other frameworks default to provider.
//
// Complete framework value enumeration (source: parser */routes.go, */remotecall.go):
//   provider: spring(Java), gin(Go), express(TS), nestjs(TS),
//             flask(Python), fastapi(Python), django(Python), python(Python),
//             graphql(Java/TS/Python), grpc(Go server-side registration)
//   consumer: feign(Java)
//
// Note: RestTemplate/axios/gRPC stub/dubbo consumer calls do not produce Route nodes,
// only RawRemoteCall entries, so they never enter DetermineRouteRole.
var ConsumerFrameworks = map[string]bool{
	"feign": true,
}

// DetermineRouteRole returns "consumer" or "provider" based on framework.
func DetermineRouteRole(framework string) string {
	if ConsumerFrameworks[framework] {
		return RoleConsumer
	}
	return RoleProvider
}

// GlobalSymbol represents an exported class/interface registered in the cross-project index.
type GlobalSymbol struct {
	QualifiedName string         `json:"qualified_name"` // e.g. com.dayu.pay.payermax.web.SeaPayApi
	Name          string         `json:"name"`           // e.g. SeaPayApi
	Kind          string         `json:"kind"`           // Interface / Class
	ClassType     string         `json:"class_type"`     // interface / class / enum / abstract_class
	NodeID        string         `json:"node_id"`        // graph node ID
	Annotations   []string       `json:"annotations"`    // e.g. ["FeignClient", "RequestMapping"]
	Methods       []GlobalMethod `json:"methods"`
	FilePath      string         `json:"file_path"`
}

// GlobalMethod represents a method of a registered class/interface.
type GlobalMethod struct {
	Name        string   `json:"name"`
	NodeID      string   `json:"node_id"`
	Params      []string `json:"params"`
	ReturnType  string   `json:"return_type"`
	Annotations []string `json:"annotations,omitempty"`
	RouteMethod string   `json:"route_method,omitempty"` // e.g. POST
	RoutePath   string   `json:"route_path,omitempty"`   // e.g. /seaPay/queryPayOutOrder
}

// GlobalRoute represents a route (HTTP endpoint, gRPC service, etc.) registered in the cross-project index.
type GlobalRoute struct {
	Method      string `json:"method"`       // e.g. POST
	Path        string `json:"path"`         // e.g. /seaPay/queryPayOutOrder
	HandlerName string `json:"handler_name"` // e.g. SeaPayController.queryPayOutOrder
	HandlerID   string `json:"handler_id"`   // handler Function node ID
	Framework   string `json:"framework"`    // e.g. spring, gin, express
	Role        string `json:"role"`         // provider / consumer
}

// ProjectEntry groups all symbols and routes for a single project+branch.
type ProjectEntry struct {
	ProjectPath string         `json:"project_path"`
	Branch      string         `json:"branch"`
	Symbols     []GlobalSymbol `json:"symbols"`
	Routes      []GlobalRoute  `json:"routes"`
	UpdatedAt   int64          `json:"updated_at"` // unix timestamp
}

// Dependency represents a project dependency declared in config.
type Dependency struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
}

// SymbolMatch wraps a symbol with its source project info.
type SymbolMatch struct {
	Symbol      GlobalSymbol `json:"symbol"`
	ProjectPath string       `json:"project_path"`
	Branch      string       `json:"branch"`
}

// RouteMatch wraps a route with its source project info.
type RouteMatch struct {
	Route       GlobalRoute `json:"route"`
	ProjectPath string      `json:"project_path"`
	Branch      string      `json:"branch"`
}

// projectKey returns the map key for a project+branch combination.
func projectKey(projectPath, branch string) string {
	return projectPath + "::" + branch
}

// normalizeRoutePath ensures path starts with / and is lowercase for matching.
func normalizeRoutePath(path string) string {
	path = strings.TrimSpace(path)
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return strings.ToLower(path)
}
