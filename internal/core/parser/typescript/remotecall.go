package typescript

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/urlutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
)

// HTTP client receivers
var tsHTTPReceivers = map[string]map[string]string{
	"axios":      {"get": "GET", "post": "POST", "put": "PUT", "delete": "DELETE", "patch": "PATCH", "head": "HEAD"},
	"got":        {"get": "GET", "post": "POST", "put": "PUT", "delete": "DELETE", "patch": "PATCH"},
	"ky":         {"get": "GET", "post": "POST", "put": "PUT", "delete": "DELETE", "patch": "PATCH"},
	"superagent": {"get": "GET", "post": "POST", "put": "PUT", "delete": "DELETE", "patch": "PATCH"},
}

// NestJS GraphQL decorators
var tsGraphQLDecorators = map[string]string{
	"Query":        "Query",
	"Mutation":     "Mutation",
	"Subscription": "Subscription",
}

// ExtractTSRemoteCalls extracts HTTP and gRPC remote calls from a TS/JS function body.
func ExtractTSRemoteCalls(body *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	if body == nil {
		return
	}
	clientVars := make(map[string]string) // var → service name

	astutil.WalkNamedChildren(body, func(node *tree_sitter.Node) bool {
		if node.Kind() == "call_expression" || node.Kind() == "await_expression" {
			actual := node
			if node.Kind() == "await_expression" {
				// await fetch(...) → get the call_expression inside
				for i := uint(0); i < node.ChildCount(); i++ {
					if node.Child(i).Kind() == "call_expression" {
						actual = node.Child(i)
						break
					}
				}
			}
			if actual.Kind() == "call_expression" {
				extractTSFetchCall(actual, content, callerName, filePath, result)
				extractTSAxiosCall(actual, content, callerName, filePath, result)
				extractTSGRPCCall(actual, content, callerName, filePath, clientVars, result)
			}
		}
		if node.Kind() == "variable_declarator" || node.Kind() == "lexical_declaration" {
			extractTSGRPCClientDecl(node, content, clientVars)
		}
		return true
	})
}

func extractTSFetchCall(node *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil {
		return
	}
	funcName := funcNode.Utf8Text(content)
	if funcName != "fetch" {
		return
	}

	argsNode := node.ChildByFieldName("arguments")
	rawURL := extractFirstTSStringArg(argsNode, content)
	httpMethod := "GET" // default for fetch

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

func extractTSAxiosCall(node *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil || funcNode.Kind() != "member_expression" {
		return
	}
	objNode := funcNode.ChildByFieldName("object")
	propNode := funcNode.ChildByFieldName("property")
	if objNode == nil || propNode == nil {
		return
	}
	receiver := objNode.Utf8Text(content)
	methodName := propNode.Utf8Text(content)

	methods, ok := tsHTTPReceivers[receiver]
	if !ok {
		return
	}
	httpMethod, ok := methods[methodName]
	if !ok {
		return
	}

	argsNode := node.ChildByFieldName("arguments")
	rawURL := extractFirstTSStringArg(argsNode, content)
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

func extractTSGRPCClientDecl(node *tree_sitter.Node, content []byte, clientVars map[string]string) {
	text := node.Utf8Text(content)
	// const client = new UserServiceClient("addr")
	if strings.Contains(text, "new ") && strings.Contains(text, "Client(") {
		if idx := strings.Index(text, "new "); idx >= 0 {
			rest := text[idx+4:]
			if paren := strings.Index(rest, "("); paren > 0 {
				className := rest[:paren]
				svcName := resolver.ExtractProtoServiceName("New" + className)
				if svcName == "" {
					// Try direct: UserServiceClient → UserService
					if s, ok := strings.CutSuffix(className, "Client"); ok && s != "" {
						svcName = s
					}
				}
				if svcName != "" {
					// Extract variable name
					varName := extractTSVarName(node, content)
					if varName != "" {
						clientVars[varName] = svcName
					}
				}
			}
		}
	}
}

func extractTSGRPCCall(node *tree_sitter.Node, content []byte, callerName, filePath string, clientVars map[string]string, result *model.ParseResult) {
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil || funcNode.Kind() != "member_expression" {
		return
	}
	objNode := funcNode.ChildByFieldName("object")
	propNode := funcNode.ChildByFieldName("property")
	if objNode == nil || propNode == nil {
		return
	}
	receiver := objNode.Utf8Text(content)
	methodName := propNode.Utf8Text(content)

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

// ExtractTSGraphQLRoutes extracts NestJS @Query/@Mutation decorator routes.
func ExtractTSGraphQLRoutes(node *tree_sitter.Node, content []byte, methodName, className, filePath string, line int, result *model.ParseResult) {
	// Check decorators on the method
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() != "decorator" {
			continue
		}
		text := child.Utf8Text(content)
		for dec, opType := range tsGraphQLDecorators {
			if strings.Contains(text, "@"+dec) {
				result.Routes = append(result.Routes, model.RawRoute{
					Method:      methodName,
					PathPattern: opType,
					Framework:   "graphql",
					Handlers: []string{className + "." + methodName},
					FilePath:    filePath,
					Line:        line,
				})
				return
			}
		}
	}
}

// ExtractGQLTemplateCalls extracts GraphQL operations from gql tagged template literals.
// Detects: gql`query { getUser(...) { ... } }` and gql`mutation { createUser(...) { ... } }`
func ExtractGQLTemplateCalls(body *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	if body == nil {
		return
	}
	astutil.WalkNamedChildren(body, func(node *tree_sitter.Node) bool {
		// gql`...` is a call_expression with function=identifier("gql") and arguments containing template_string
		// OR it's a tagged template: gql`...`
		if node.Kind() != "call_expression" {
			return true
		}
		funcNode := node.ChildByFieldName("function")
		if funcNode == nil || funcNode.Utf8Text(content) != "gql" {
			return true
		}
		argsNode := node.ChildByFieldName("arguments")
		if argsNode == nil {
			return true
		}
		// Find template_string in arguments
		for i := uint(0); i < argsNode.ChildCount(); i++ {
			arg := argsNode.Child(i)
			if arg.Kind() == "template_string" || arg.Kind() == "string" {
				text := arg.Utf8Text(content)
				extractGQLOps(text, callerName, filePath, int(node.StartPosition().Row)+1, result)
			}
		}
		return true
	})
}

// --- helpers ---

// extractGQLOps parses a GraphQL query string to extract operation type and field names.
// e.g. "query { getUser(id: \"1\") { name } }" → RemoteCall{Method: "getUser", TargetService: "Query"}
func extractGQLOps(text, callerName, filePath string, line int, result *model.ParseResult) {
	text = strings.Trim(text, "`\"'")
	// Match: (query|mutation) optional_name { fieldName
	// Also match shorthand: { fieldName (defaults to query)
	opType := "Query"
	remaining := text

	for _, prefix := range []string{"query", "mutation", "subscription"} {
		idx := strings.Index(strings.ToLower(remaining), prefix)
		if idx >= 0 {
			switch prefix {
			case "mutation":
				opType = "Mutation"
			case "subscription":
				opType = "Subscription"
			}
			remaining = remaining[idx+len(prefix):]
			break
		}
	}

	// Find first { then first identifier after it
	braceIdx := strings.Index(remaining, "{")
	if braceIdx < 0 {
		return
	}
	remaining = strings.TrimSpace(remaining[braceIdx+1:])

	// Extract field name (first word)
	var fieldName strings.Builder
	for _, ch := range remaining {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' {
			fieldName.WriteRune(ch)
		} else {
			break
		}
	}
	if fieldName.Len() == 0 {
		return
	}

	result.RemoteCalls = append(result.RemoteCalls, model.RawRemoteCall{
		Method:            fieldName.String(),
		TargetURL:         opType + "." + fieldName.String(),
		TargetService:     opType,
		ServiceResolvedBy: "gql_template",
		ServiceConfidence: 1.0,
		Protocol:          "graphql",
		CallerName:        callerName,
		FilePath:          filePath,
		Line:              line,
	})
}

func extractFirstTSStringArg(argsNode *tree_sitter.Node, content []byte) string {
	if argsNode == nil {
		return ""
	}
	for i := uint(0); i < argsNode.ChildCount(); i++ {
		arg := argsNode.Child(i)
		if !arg.IsNamed() {
			continue
		}
		if arg.Kind() == "string" {
			return strings.Trim(arg.Utf8Text(content), "\"'`")
		}
		if arg.Kind() == "template_string" {
			// Simple template without expressions
			text := arg.Utf8Text(content)
			return strings.Trim(text, "`")
		}
		return ""
	}
	return ""
}

func extractTSVarName(node *tree_sitter.Node, content []byte) string {
	// variable_declarator: name = value
	nameNode := node.ChildByFieldName("name")
	if nameNode != nil {
		return nameNode.Utf8Text(content)
	}
	return ""
}
