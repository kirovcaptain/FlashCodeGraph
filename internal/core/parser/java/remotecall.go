package java

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/urlutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
)

// RestTemplate method name → HTTP method
var restTemplateMethods = map[string]string{
	"getForObject":    "GET",
	"getForEntity":    "GET",
	"postForObject":   "POST",
	"postForEntity":   "POST",
	"postForLocation": "POST",
	"put":             "PUT",
	"delete":          "DELETE",
	"patchForObject":  "PATCH",
	"exchange":        "UNKNOWN",
}

// Fully qualified type names recognized as HTTP clients
var httpClientTypes = map[string]bool{
	"org.springframework.web.client.RestTemplate": true,
}

// ExtractFeignClient extracts remote calls from a @FeignClient interface.
// Detects @FeignClient(name, url, path) + method-level @GetMapping/@PostMapping.
func ExtractFeignClient(classAnnotations []string, node *tree_sitter.Node, content []byte, className, filePath string, result *model.ParseResult) {
	serviceName, feignPath := parseFeignClientAnnotation(classAnnotations)
	if serviceName == "" {
		return
	}

	// Also check @RequestMapping for class-level path prefix
	if feignPath == "" {
		for _, ann := range classAnnotations {
			if strings.Contains(ann, "RequestMapping") {
				feignPath = extractAnnotationValue(ann)
				break
			}
		}
	}

	// Determine confidence based on how service name was resolved
	resolvedBy := "literal"
	confidence := constants.RemoteCallConfidenceLiteral
	if strings.HasPrefix(serviceName, "${") {
		// Placeholder — unresolved
		resolvedBy = "unresolved"
		confidence = constants.RemoteCallConfidenceUnresolved
	}

	// Walk interface methods for @GetMapping/@PostMapping etc.
	astutil.WalkNamedChildren(node, func(child *tree_sitter.Node) bool {
		if child.Kind() != "method_declaration" {
			return true
		}

		methodName := ""
		nameNode := child.ChildByFieldName("name")
		if nameNode != nil {
			methodName = nameNode.Utf8Text(content)
		}

		// Extract method annotations
		annotations := collectAnnotations(child, content)
		for _, annotation := range annotations {
			baseName := strings.TrimPrefix(annotation, "@")
			if idx := strings.Index(baseName, "("); idx >= 0 {
				baseName = baseName[:idx]
			}

			httpMethod, isRoute := javaRouteAnnotations[baseName]
			if !isRoute {
				continue
			}

			methodPath := extractAnnotationValue(annotation)
			fullPath := feignPath + methodPath

			result.Routes = append(result.Routes, model.RawRoute{
				Method:      httpMethod,
				PathPattern: fullPath,
				HandlerName: className + "." + methodName,
				Framework:   "feign",
				FilePath:    filePath,
				Line:        int(child.StartPosition().Row) + 1,
			})

			result.RemoteCalls = append(result.RemoteCalls, model.RawRemoteCall{
				Method:            httpMethod,
				TargetURL:         fullPath,
				TargetService:     serviceName,
				ServiceResolvedBy: resolvedBy,
				ServiceConfidence: confidence,
				Protocol:          "http",
				CallerName:        className + "." + methodName,
				FilePath:          filePath,
				Line:              int(child.StartPosition().Row) + 1,
			})
		}
		return true
	})
}

// ExtractRestTemplateCalls extracts remote calls from RestTemplate method invocations.
// Requires TypeEnv to verify receiver type via fully qualified name.
func ExtractRestTemplateCalls(bodyNode *tree_sitter.Node, content []byte, callerName, filePath string, typeEnv map[string]string, result *model.ParseResult) {
	if bodyNode == nil {
		return
	}

	// Collect local string variable assignments for URL tracking
	stringVars := collectStringVars(bodyNode, content)

	astutil.WalkNamedChildren(bodyNode, func(node *tree_sitter.Node) bool {
		if node.Kind() != "method_invocation" {
			return true
		}

		// Extract method name
		nameNode := node.ChildByFieldName("name")
		if nameNode == nil {
			return true
		}
		methodName := nameNode.Utf8Text(content)

		httpMethod, isRT := restTemplateMethods[methodName]
		if !isRT {
			return true
		}

		// Verify receiver type via fully qualified name
		objNode := node.ChildByFieldName("object")
		if objNode == nil {
			return true
		}
		receiverName := objNode.Utf8Text(content)

		// Check TypeEnv for fully qualified name
		fqn := typeEnv[receiverName]
		if fqn == "" {
			// Try scoped key: callerName:receiverName
			fqn = typeEnv[callerName+":"+receiverName]
		}
		if !httpClientTypes[fqn] {
			return true // Not a recognized HTTP client
		}

		// Extract first string argument as URL
		argsNode := node.ChildByFieldName("arguments")
		rawURL := extractFirstStringArg(argsNode, content, stringVars)

		// Normalize URL and extract service name
		normalizedPath, serviceName := urlutil.NormalizeURL(rawURL)
		resolvedBy := "literal"
		confidence := constants.RemoteCallConfidenceLiteral
		if serviceName == "" {
			resolvedBy = "unresolved"
			confidence = constants.RemoteCallConfidenceUnresolved
		}
		if normalizedPath == "" && rawURL != "" {
			normalizedPath = rawURL // keep raw if normalization failed
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

		return true
	})
}

// parseFeignClientAnnotation extracts name, url, path from @FeignClient annotation.
func parseFeignClientAnnotation(annotations []string) (serviceName, path string) {
	for _, annotation := range annotations {
		if !strings.Contains(annotation, "FeignClient") {
			continue
		}

		// Extract name attribute
		serviceName = extractNamedAttribute(annotation, "name")
		if serviceName == "" {
			serviceName = extractNamedAttribute(annotation, "value")
		}

		// Extract path attribute
		path = extractNamedAttribute(annotation, "path")

		return serviceName, path
	}
	return "", ""
}

// extractNamedAttribute extracts a named attribute value from an annotation string.
// e.g., @FeignClient(name = "user-service", path = "/api") → "user-service" for key="name"
func extractNamedAttribute(annotation, key string) string {
	search := key + " = \""
	idx := strings.Index(annotation, search)
	if idx < 0 {
		search = key + "=\""
		idx = strings.Index(annotation, search)
	}
	if idx < 0 {
		return ""
	}
	start := idx + len(search)
	end := strings.Index(annotation[start:], "\"")
	if end < 0 {
		return ""
	}
	return annotation[start : start+end]
}

// collectStringVars collects local variable assignments: String x = "literal"
func collectStringVars(node *tree_sitter.Node, content []byte) map[string]string {
	vars := make(map[string]string)
	astutil.WalkNamedChildren(node, func(child *tree_sitter.Node) bool {
		if child.Kind() == "local_variable_declaration" {
			for i := uint(0); i < child.ChildCount(); i++ {
				decl := child.Child(i)
				if decl.Kind() == "variable_declarator" {
					nameNode := decl.ChildByFieldName("name")
					valueNode := decl.ChildByFieldName("value")
					if nameNode != nil && valueNode != nil && valueNode.Kind() == "string_literal" {
						vars[nameNode.Utf8Text(content)] = strings.Trim(valueNode.Utf8Text(content), "\"")
					}
				}
			}
		}
		return true
	})
	return vars
}

// extractFirstStringArg extracts the first string argument from an argument list.
// Resolves variable references via stringVars map.
func extractFirstStringArg(argsNode *tree_sitter.Node, content []byte, stringVars map[string]string) string {
	if argsNode == nil {
		return ""
	}
	for i := uint(0); i < argsNode.ChildCount(); i++ {
		arg := argsNode.Child(i)
		kind := arg.Kind()
		// Skip non-named nodes (punctuation: ( ) ,)
		if !arg.IsNamed() {
			continue
		}
		switch kind {
		case "string_literal":
			return strings.Trim(arg.Utf8Text(content), "\"")
		case "identifier":
			varName := arg.Utf8Text(content)
			if val, ok := stringVars[varName]; ok {
				return val
			}
		}
		// First real argument found but not a string/variable — stop
		return ""
	}
	return ""
}

// collectAnnotations extracts annotation strings from a node's modifiers.
func collectAnnotations(node *tree_sitter.Node, content []byte) []string {
	var annotations []string
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "modifiers" {
			for j := uint(0); j < child.ChildCount(); j++ {
				mod := child.Child(j)
				if mod.Kind() == "marker_annotation" || mod.Kind() == "annotation" {
					annotations = append(annotations, mod.Utf8Text(content))
				}
			}
		}
	}
	return annotations
}

// hasFeignClient checks if class annotations contain @FeignClient.
func HasFeignClient(annotations []string) bool {
	for _, annotation := range annotations {
		if strings.Contains(annotation, "FeignClient") {
			return true
		}
	}
	return false
}

// --- Java gRPC ---

// ExtractGRPCStubCalls extracts gRPC stub creation and method calls from a method body.
// Detects: XxxGrpc.newBlockingStub(channel), XxxGrpc.newStub(channel)
// and ManagedChannelBuilder.forTarget("addr").
func ExtractGRPCStubCalls(bodyNode *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	if bodyNode == nil {
		return
	}
	// Track channel variable → target address
	channelVars := make(map[string]string)
	// Track stub variable → service name
	stubVars := make(map[string]string)

	astutil.WalkNamedChildren(bodyNode, func(node *tree_sitter.Node) bool {
		switch node.Kind() {
		case "local_variable_declaration":
			extractGRPCVarDecl(node, content, channelVars, stubVars)
		case "method_invocation":
			extractGRPCMethodCall(node, content, callerName, filePath, stubVars, channelVars, result)
		}
		return true
	})
}

func extractGRPCVarDecl(node *tree_sitter.Node, content []byte, channelVars, stubVars map[string]string) {
	for i := uint(0); i < node.ChildCount(); i++ {
		decl := node.Child(i)
		if decl.Kind() != "variable_declarator" {
			continue
		}
		nameNode := decl.ChildByFieldName("name")
		valueNode := decl.ChildByFieldName("value")
		if nameNode == nil || valueNode == nil {
			continue
		}
		varName := nameNode.Utf8Text(content)
		valueText := valueNode.Utf8Text(content)

		// ManagedChannelBuilder.forTarget("user-service:50051").build()
		if strings.Contains(valueText, "forTarget") || strings.Contains(valueText, "forAddress") {
			if addr := extractQuotedString(valueText); addr != "" {
				channelVars[varName] = addr
			}
		}
		// UserServiceGrpc.newBlockingStub(channel)
		if strings.Contains(valueText, "newBlockingStub") || strings.Contains(valueText, "newStub") || strings.Contains(valueText, "newFutureStub") {
			if idx := strings.Index(valueText, "Grpc."); idx > 0 {
				svcName := valueText[:idx]
				// Remove package prefix if present
				if dot := strings.LastIndex(svcName, "."); dot >= 0 {
					svcName = svcName[dot+1:]
				}
				stubVars[varName] = svcName
			}
		}
	}
}

func extractGRPCMethodCall(node *tree_sitter.Node, content []byte, callerName, filePath string, stubVars, channelVars map[string]string, result *model.ParseResult) {
	objNode := node.ChildByFieldName("object")
	nameNode := node.ChildByFieldName("name")
	if objNode == nil || nameNode == nil {
		return
	}
	receiver := objNode.Utf8Text(content)
	methodName := nameNode.Utf8Text(content)

	svcName, isStub := stubVars[receiver]
	if !isStub {
		return
	}

	// Find target address from channel tracking
	targetService := svcName
	resolvedBy := "proto_service"
	confidence := constants.RemoteCallConfidenceInferred

	result.RemoteCalls = append(result.RemoteCalls, model.RawRemoteCall{
		Method:            methodName,
		TargetURL:         svcName + "/" + methodName,
		TargetService:     targetService,
		ServiceResolvedBy: resolvedBy,
		ServiceConfidence: confidence,
		Protocol:          "grpc",
		CallerName:        callerName,
		FilePath:          filePath,
		Line:              int(node.StartPosition().Row) + 1,
	})
}

// --- Java Dubbo ---

// ExtractDubboReference extracts @DubboReference field annotations.
// Returns interface type names that are Dubbo remote references.
func ExtractDubboReference(fieldNode *tree_sitter.Node, content []byte, className, filePath string, result *model.ParseResult) {
	annotations := collectAnnotations(fieldNode, content)
	isDubbo := false
	for _, a := range annotations {
		if strings.Contains(a, "DubboReference") || strings.Contains(a, "Reference") {
			isDubbo = true
			break
		}
	}
	if !isDubbo {
		return
	}

	// Extract field type as the interface name
	typeNode := fieldNode.ChildByFieldName("type")
	if typeNode == nil {
		return
	}
	interfaceType := typeNode.Utf8Text(content)

	// Extract field name
	fieldName := ""
	for i := uint(0); i < fieldNode.ChildCount(); i++ {
		child := fieldNode.Child(i)
		if child.Kind() == "variable_declarator" {
			n := child.ChildByFieldName("name")
			if n != nil {
				fieldName = n.Utf8Text(content)
			}
		}
	}

	result.RemoteCalls = append(result.RemoteCalls, model.RawRemoteCall{
		Method:            "*",
		TargetURL:         interfaceType,
		TargetService:     interfaceType,
		ServiceResolvedBy: "interface_type",
		ServiceConfidence: 1.0,
		Protocol:          "dubbo",
		CallerName:        className + "." + fieldName,
		FilePath:          filePath,
		Line:              int(fieldNode.StartPosition().Row) + 1,
	})
}

// HasDubboService checks if class annotations contain @DubboService.
func HasDubboService(annotations []string) bool {
	for _, a := range annotations {
		if strings.Contains(a, "DubboService") {
			return true
		}
	}
	return false
}

// --- Java GraphQL ---

// graphqlAnnotations maps Spring GraphQL annotations to operation types.
var graphqlAnnotations = map[string]string{
	"QueryMapping":        "Query",
	"MutationMapping":     "Mutation",
	"SubscriptionMapping": "Subscription",
	"SchemaMapping":       "Query",
}

// ExtractGraphQLRoutes extracts GraphQL resolver routes from method annotations.
func ExtractGraphQLRoutes(annotations []string, methodName, className, filePath string, line int, result *model.ParseResult) {
	for _, annotation := range annotations {
		baseName := strings.TrimPrefix(annotation, "@")
		if idx := strings.Index(baseName, "("); idx >= 0 {
			baseName = baseName[:idx]
		}
		opType, ok := graphqlAnnotations[baseName]
		if !ok {
			continue
		}
			result.Routes = append(result.Routes, model.RawRoute{
			Method:      methodName,
			PathPattern: opType,
			Framework:   "graphql",
			HandlerName: className + "." + methodName,
			FilePath:    filePath,
			Line:        line,
		})
		return
	}
}

// --- helpers ---

// extractQuotedString extracts the first quoted string from text.
func extractQuotedString(text string) string {
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
