package python

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/urlutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
)

// Python HTTP client modules and their methods
var pythonHTTPMethods = map[string]map[string]string{
	"requests": {"get": "GET", "post": "POST", "put": "PUT", "delete": "DELETE", "patch": "PATCH", "head": "HEAD"},
	"httpx":    {"get": "GET", "post": "POST", "put": "PUT", "delete": "DELETE", "patch": "PATCH", "head": "HEAD"},
}

// ExtractPythonRemoteCalls extracts HTTP and gRPC remote calls from a Python function body.
func ExtractPythonRemoteCalls(body *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	if body == nil {
		return
	}
	// Track gRPC channel and stub variables
	stubVars := make(map[string]string) // var → service name

	astutil.WalkNamedChildren(body, func(node *tree_sitter.Node) bool {
		switch node.Kind() {
		case "assignment":
			extractPythonGRPCAssignment(node, content, stubVars)
		case "call":
			extractPythonHTTPCall(node, content, callerName, filePath, result)
			extractPythonGRPCCall(node, content, callerName, filePath, stubVars, result)
		}
		return true
	})
}

func extractPythonHTTPCall(node *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil || funcNode.Kind() != "attribute" {
		return
	}
	objNode := funcNode.ChildByFieldName("object")
	attrNode := funcNode.ChildByFieldName("attribute")
	if objNode == nil || attrNode == nil {
		return
	}
	receiver := objNode.Utf8Text(content)
	methodName := attrNode.Utf8Text(content)

	methods, ok := pythonHTTPMethods[receiver]
	if !ok {
		return
	}
	httpMethod, ok := methods[methodName]
	if !ok {
		return
	}

	argsNode := node.ChildByFieldName("arguments")
	rawURL := extractFirstPyStringArg(argsNode, content)
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

func extractPythonGRPCAssignment(node *tree_sitter.Node, content []byte, stubVars map[string]string) {
	text := node.Utf8Text(content)
	// stub = user_pb2_grpc.UserServiceStub(channel)
	if strings.Contains(text, "Stub(") {
		left := node.ChildByFieldName("left")
		right := node.ChildByFieldName("right")
		if left == nil || right == nil {
			return
		}
		varName := left.Utf8Text(content)
		rightText := right.Utf8Text(content)
		// Extract service name from XxxStub
		if idx := strings.Index(rightText, "Stub("); idx > 0 {
			funcPart := rightText[:idx+4] // include "Stub"
			if dot := strings.LastIndex(funcPart, "."); dot >= 0 {
				funcPart = funcPart[dot+1:]
			}
			svcName := resolver.ExtractProtoServiceName(funcPart)
			if svcName != "" {
				stubVars[varName] = svcName
			}
		}
	}
}

func extractPythonGRPCCall(node *tree_sitter.Node, content []byte, callerName, filePath string, stubVars map[string]string, result *model.ParseResult) {
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil || funcNode.Kind() != "attribute" {
		return
	}
	objNode := funcNode.ChildByFieldName("object")
	attrNode := funcNode.ChildByFieldName("attribute")
	if objNode == nil || attrNode == nil {
		return
	}
	receiver := objNode.Utf8Text(content)
	methodName := attrNode.Utf8Text(content)

	svcName, ok := stubVars[receiver]
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

// ExtractStrawberryRoutes extracts GraphQL routes from @strawberry.field decorated methods.
func ExtractStrawberryRoutes(node *tree_sitter.Node, content []byte, funcName, className, filePath string, result *model.ParseResult) {
	// Check decorators for @strawberry.field
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "decorator" {
			text := child.Utf8Text(content)
			if strings.Contains(text, "strawberry.field") || strings.Contains(text, "strawberry.mutation") {
				opType := "Query"
				if strings.Contains(text, "mutation") {
					opType = "Mutation"
				}
				// Use parent class name to determine type
				if className == "Mutation" {
					opType = "Mutation"
				} else if className == "Subscription" {
					opType = "Subscription"
				}
				result.Routes = append(result.Routes, model.RawRoute{
					Method:      funcName,
					PathPattern: opType,
					Framework:   "graphql",
					HandlerName: className + "." + funcName,
					FilePath:    filePath,
					Line:        int(node.StartPosition().Row) + 1,
				})
				return
			}
		}
	}
}

// --- helpers ---

func extractFirstPyStringArg(argsNode *tree_sitter.Node, content []byte) string {
	if argsNode == nil {
		return ""
	}
	for i := uint(0); i < argsNode.ChildCount(); i++ {
		arg := argsNode.Child(i)
		if !arg.IsNamed() {
			continue
		}
		if arg.Kind() == "string" {
			return strings.Trim(arg.Utf8Text(content), "\"'")
		}
		return ""
	}
	return ""
}
