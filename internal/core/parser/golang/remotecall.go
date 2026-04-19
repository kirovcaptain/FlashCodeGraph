package golang

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/liuymcn/flash-code-graph/internal/core/parser/astutil"
	"github.com/liuymcn/flash-code-graph/internal/core/parser/urlutil"
	"github.com/liuymcn/flash-code-graph/internal/core/resolver"
	"github.com/liuymcn/flash-code-graph/internal/model"
	"github.com/liuymcn/flash-code-graph/internal/constants"
)

// net/http methods: package-level functions
var netHTTPFunctions = map[string]string{
	"Get":  "GET",
	"Post": "POST",
	"Head": "HEAD",
}

// gRPC dial functions
var grpcDialFunctions = map[string]bool{
	"Dial": true, "NewClient": true, "DialContext": true,
	"DialInsecure": true, "MustDial": true,
}

// ExtractGoRemoteCalls extracts HTTP and gRPC remote calls from a Go function body.
func ExtractGoRemoteCalls(body *tree_sitter.Node, content []byte, callerName, filePath string, imports map[string]string, result *model.ParseResult) {
	if body == nil {
		return
	}
	connVars := make(map[string]string)   // var → dial target
	clientVars := make(map[string]string) // var → proto service name

	astutil.WalkNamedChildren(body, func(node *tree_sitter.Node) bool {
		switch node.Kind() {
		case "short_var_declaration", "assignment_statement":
			extractGoConnDecl(node, content, imports, connVars, clientVars)
		case "call_expression":
			extractGoHTTPCall(node, content, callerName, filePath, imports, result)
			extractGoNewRequest(node, content, callerName, filePath, imports, result)
			extractGoGRPCCall(node, content, callerName, filePath, imports, clientVars, result)
		}
		return true
	})
}

func extractGoHTTPCall(node *tree_sitter.Node, content []byte, callerName, filePath string, imports map[string]string, result *model.ParseResult) {
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil || funcNode.Kind() != "selector_expression" {
		return
	}
	operand := funcNode.ChildByFieldName("operand")
	field := funcNode.ChildByFieldName("field")
	if operand == nil || field == nil {
		return
	}
	pkg := operand.Utf8Text(content)
	methodName := field.Utf8Text(content)

	// http.Get / http.Post / http.Head
	httpMethod, ok := netHTTPFunctions[methodName]
	if !ok {
		return
	}
	// Verify it's net/http
	if imports[pkg] != "net/http" && pkg != "http" {
		return
	}

	argsNode := node.ChildByFieldName("arguments")
	rawURL := extractFirstGoStringArg(argsNode, content)
	normalizedPath, serviceName := urlutil.NormalizeURL(rawURL)
	resolvedBy := "url_host"
	confidence := constants.RemoteCallConfidenceLiteral
	if serviceName == "" {
		resolvedBy = "unresolved"
		confidence = constants.RemoteCallConfidenceUnresolved
	}

	result.RemoteCalls = append(result.RemoteCalls, model.RawRemoteCall{
		Method:            httpMethod,
		TargetURL:         normalizedPath,
		TargetService:     serviceName,
		ServiceResolvedBy: resolvedBy,
		ServiceConfidence: confidence,
		Protocol:          "http",
		CallerName:        callerName,
		FilePath:          filePath,
		Line:              int(node.StartPosition().Row) + 1,
	})
}

// http.NewRequest("DELETE", url, body)
func extractGoNewRequest(node *tree_sitter.Node, content []byte, callerName, filePath string, imports map[string]string, result *model.ParseResult) {
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil || funcNode.Kind() != "selector_expression" {
		return
	}
	operand := funcNode.ChildByFieldName("operand")
	field := funcNode.ChildByFieldName("field")
	if operand == nil || field == nil {
		return
	}
	if field.Utf8Text(content) != "NewRequest" {
		return
	}
	pkg := operand.Utf8Text(content)
	if imports[pkg] != "net/http" && pkg != "http" {
		return
	}

	argsNode := node.ChildByFieldName("arguments")
	if argsNode == nil {
		return
	}
	args := collectNamedChildren(argsNode, content)
	if len(args) < 2 {
		return
	}
	httpMethod := strings.Trim(args[0], "\"")
	rawURL := strings.Trim(args[1], "\"")
	normalizedPath, serviceName := urlutil.NormalizeURL(rawURL)
	resolvedBy := "url_host"
	confidence := constants.RemoteCallConfidenceLiteral
	if serviceName == "" {
		resolvedBy = "unresolved"
		confidence = constants.RemoteCallConfidenceUnresolved
	}

	result.RemoteCalls = append(result.RemoteCalls, model.RawRemoteCall{
		Method:            httpMethod,
		TargetURL:         normalizedPath,
		TargetService:     serviceName,
		ServiceResolvedBy: resolvedBy,
		ServiceConfidence: confidence,
		Protocol:          "http",
		CallerName:        callerName,
		FilePath:          filePath,
		Line:              int(node.StartPosition().Row) + 1,
	})
}

func extractGoConnDecl(node *tree_sitter.Node, content []byte, imports map[string]string, connVars, clientVars map[string]string) {
	text := node.Utf8Text(content)

	// grpc.Dial("target") / grpc.NewClient("target")
	for fn := range grpcDialFunctions {
		if strings.Contains(text, "."+fn+"(") {
			if addr := extractQuotedFromText(text); addr != "" {
				addr = resolver.StripGRPCScheme(addr)
				addr = resolver.StripPort(addr)
				varName := extractLHSVar(node, content)
				if varName != "" {
					connVars[varName] = addr
				}
			}
		}
	}

	// pb.NewUserServiceClient(conn)
	if strings.Contains(text, "New") && strings.Contains(text, "Client(") {
		varName := extractLHSVar(node, content)
		// Extract function name to get service name
		if idx := strings.Index(text, "New"); idx >= 0 {
			rest := text[idx:]
			if paren := strings.Index(rest, "("); paren > 0 {
				funcName := rest[:paren]
				// Remove package prefix: pb.NewUserServiceClient → NewUserServiceClient
				if dot := strings.LastIndex(funcName, "."); dot >= 0 {
					funcName = funcName[dot+1:]
				}
				svcName := resolver.ExtractProtoServiceName(funcName)
				if svcName != "" && varName != "" {
					clientVars[varName] = svcName
				}
			}
		}
	}
}

func extractGoGRPCCall(node *tree_sitter.Node, content []byte, callerName, filePath string, imports map[string]string, clientVars map[string]string, result *model.ParseResult) {
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil || funcNode.Kind() != "selector_expression" {
		return
	}
	operand := funcNode.ChildByFieldName("operand")
	field := funcNode.ChildByFieldName("field")
	if operand == nil || field == nil {
		return
	}
	receiver := operand.Utf8Text(content)
	methodName := field.Utf8Text(content)

	// Check if receiver is a known gRPC client stub
	svcName, ok := clientVars[receiver]
	if !ok {
		return
	}

	result.RemoteCalls = append(result.RemoteCalls, model.RawRemoteCall{
		Method:            methodName,
		TargetURL:         svcName + "/" + methodName,
		TargetService:     svcName,
		ServiceResolvedBy: "proto_service",
		ServiceConfidence: constants.RemoteCallConfidenceInferred,
		Protocol:          "grpc",
		CallerName:        callerName,
		FilePath:          filePath,
		Line:              int(node.StartPosition().Row) + 1,
	})
}

// ExtractGoGRPCRegister detects pb.RegisterXxxServer(grpcServer, &impl{}).
func ExtractGoGRPCRegister(body *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	if body == nil {
		return
	}
	astutil.WalkNamedChildren(body, func(node *tree_sitter.Node) bool {
		if node.Kind() != "call_expression" {
			return true
		}
		funcNode := node.ChildByFieldName("function")
		if funcNode == nil {
			return true
		}
		text := funcNode.Utf8Text(content)
		// pb.RegisterUserServiceServer or RegisterUserServiceServer
		funcName := text
		if dot := strings.LastIndex(text, "."); dot >= 0 {
			funcName = text[dot+1:]
		}
		svcName := resolver.ExtractProtoServiceName(funcName)
		if svcName == "" || !strings.HasPrefix(funcName, "Register") {
			return true
		}

		result.Routes = append(result.Routes, model.RawRoute{
			Method:      "*",
			PathPattern: svcName,
			Framework:   "grpc",
			HandlerName: callerName,
			FilePath:    filePath,
			Line:        int(node.StartPosition().Row) + 1,
		})
		return true
	})
}

// --- helpers ---

func extractFirstGoStringArg(argsNode *tree_sitter.Node, content []byte) string {
	if argsNode == nil {
		return ""
	}
	for i := uint(0); i < argsNode.ChildCount(); i++ {
		arg := argsNode.Child(i)
		if !arg.IsNamed() {
			continue
		}
		if arg.Kind() == "interpreted_string_literal" || arg.Kind() == "raw_string_literal" {
			return strings.Trim(arg.Utf8Text(content), "\"`")
		}
		return "" // first named arg is not a string
	}
	return ""
}

func extractQuotedFromText(text string) string {
	start := strings.Index(text, "\"")
	if start < 0 {
		return ""
	}
	end := strings.Index(text[start+1:], "\"")
	if end < 0 {
		return ""
	}
	return text[start+1 : start+1+end]
}

func extractLHSVar(node *tree_sitter.Node, content []byte) string {
	// short_var_declaration: left := right
	left := node.ChildByFieldName("left")
	if left != nil {
		// expression_list → first child
		for i := uint(0); i < left.ChildCount(); i++ {
			child := left.Child(i)
			if child.IsNamed() {
				return child.Utf8Text(content)
			}
		}
		return left.Utf8Text(content)
	}
	return ""
}

func collectNamedChildren(node *tree_sitter.Node, content []byte) []string {
	var result []string
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.IsNamed() {
			result = append(result, child.Utf8Text(content))
		}
	}
	return result
}
