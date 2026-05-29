package typescript

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

var nestjsDecorators = map[string]string{
	"Get":    "GET",
	"Post":   "POST",
	"Put":    "PUT",
	"Delete": "DELETE",
	"Patch":  "PATCH",
}

func ExtractDecoratorRoutes(node *tree_sitter.Node, content []byte, funcName, className, filePath string, result *model.ParseResult) {
	// Look for decorated method — check parent or sibling decorators
	parent := node.Parent()
	if parent == nil {
		return
	}

	// In TS, decorators are siblings before the method in class_body
	// Check previous siblings for decorator nodes
	prevSibling := node.PrevNamedSibling()
	for prevSibling != nil && prevSibling.Kind() == "decorator" {
		decoratorText := prevSibling.Utf8Text(content)
		for decoratorName, httpMethod := range nestjsDecorators {
			if strings.Contains(decoratorText, "@"+decoratorName+"(") || decoratorText == "@"+decoratorName {
				pathPattern := extractDecoratorArg(decoratorText)
				handlerName := funcName
				if className != "" {
					handlerName = className + "." + funcName
				}
				result.Routes = append(result.Routes, model.RawRoute{
					Method:      httpMethod,
					PathPattern: pathPattern,
					Handlers:    []string{handlerName},
					Framework:   "nestjs",
					FilePath:    filePath,
					Line:        int(prevSibling.StartPosition().Row) + 1,
				})
				return
			}
		}
		prevSibling = prevSibling.PrevNamedSibling()
	}
}

var routeMethodNames = map[string]string{
	"get":    "GET",
	"post":   "POST",
	"put":    "PUT",
	"delete": "DELETE",
	"patch":  "PATCH",
	"all":    "GET",
}

func ExtractChainedRoutes(node *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	if node.Kind() != "call_expression" {
		return
	}
	// Only process the outermost call in a chain.
	// If this node is the object of a parent member_expression → call_expression, skip it.
	parent := node.Parent()
	if parent != nil && parent.Kind() == "member_expression" {
		grandparent := parent.Parent()
		if grandparent != nil && grandparent.Kind() == "call_expression" {
			return
		}
	}

	// Unwrap the chain: collect HTTP methods and find route() path
	var methods []string
	pathPattern := ""
	current := node

	for current != nil && current.Kind() == "call_expression" {
		funcNode := current.ChildByFieldName("function")
		if funcNode == nil {
			break
		}

		if funcNode.Kind() == "member_expression" {
			propNode := funcNode.ChildByFieldName("property")
			if propNode != nil {
				methodName := propNode.Utf8Text(content)
				if _, isHTTP := routeMethodNames[methodName]; isHTTP {
					methods = append(methods, strings.ToUpper(methodName))
				} else if methodName == "route" {
					// Extract path from route() arguments
					argsNode := current.ChildByFieldName("arguments")
					if argsNode != nil {
						for i := uint(0); i < argsNode.ChildCount(); i++ {
							arg := argsNode.Child(i)
							if arg.Kind() == "string" || arg.Kind() == "template_string" {
								pathPattern = strings.Trim(arg.Utf8Text(content), "\x22\x27\x60")
								break
							}
						}
					}
				}
			}
			// Walk inward
			objNode := funcNode.ChildByFieldName("object")
			if objNode != nil && objNode.Kind() == "call_expression" {
				current = objNode
			} else {
				break
			}
		} else {
			break
		}
	}

	if pathPattern != "" && len(methods) > 0 {
		for _, method := range methods {
			result.Routes = append(result.Routes, model.RawRoute{
				Method:      method,
				PathPattern: pathPattern,
				Handlers:    []string{callerName},
				Framework:   "express",
				FilePath:    filePath,
				Line:        int(node.StartPosition().Row) + 1,
			})
		}
	}
}

// TSRouteDetection holds the result of detecting a TS route registration call.
type TSRouteDetection struct {
	Method      string
	PathPattern string
	ArgsNode    *tree_sitter.Node
}

// DetectTSRoute checks if a call_expression is an Express route registration.
// Returns route info without writing to result, or nil if not a route call.
func DetectTSRoute(node *tree_sitter.Node, content []byte) *TSRouteDetection {
	if node.Kind() != "call_expression" {
		return nil
	}
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil || funcNode.Kind() != "member_expression" {
		return nil
	}
	objNode := funcNode.ChildByFieldName("object")
	propNode := funcNode.ChildByFieldName("property")
	if objNode == nil || propNode == nil {
		return nil
	}
	receiverName := objNode.Utf8Text(content)
	methodName := propNode.Utf8Text(content)

	validReceivers := map[string]bool{
		"app": true, "router": true, "server": true,
		"route": true, "api": true, "express": true,
	}
	if !validReceivers[receiverName] {
		return nil
	}
	httpMethod, isRoute := routeMethodNames[methodName]
	if !isRoute {
		return nil
	}
	argsNode := node.ChildByFieldName("arguments")
	if argsNode == nil {
		return nil
	}
	pathPattern := ""
	for i := uint(0); i < argsNode.ChildCount(); i++ {
		arg := argsNode.Child(i)
		if arg.Kind() == "string" || arg.Kind() == "template_string" {
			pathPattern = strings.Trim(arg.Utf8Text(content), "\"'`")
			break
		}
	}
	if pathPattern == "" {
		return nil
	}
	return &TSRouteDetection{
		Method:      httpMethod,
		PathPattern: pathPattern,
		ArgsNode:    argsNode,
	}
}

// ResolveTSHandlerArgs extracts the ordered handler chain from TS route arguments.
func ResolveTSHandlerArgs(argsNode *tree_sitter.Node, content []byte, lambdaMap map[uintptr]string) []string {
	if argsNode == nil {
		return nil
	}
	var handlers []string
	for i := uint(0); i < argsNode.ChildCount(); i++ {
		child := argsNode.Child(i)
		if !child.IsNamed() {
			continue
		}
		if child.Kind() == "string" || child.Kind() == "template_string" {
			continue
		}
		resolved := resolveTSOneArg(child, content, lambdaMap)
		handlers = append(handlers, resolved...)
	}
	return handlers
}

// resolveTSOneArg recursively resolves a single TS argument node into handler names.
func resolveTSOneArg(node *tree_sitter.Node, content []byte, lambdaMap map[uintptr]string) []string {
	switch node.Kind() {
	case "identifier":
		return []string{node.Utf8Text(content)}
	case "member_expression":
		return []string{node.Utf8Text(content)}
	case "arrow_function", "function":
		if lambdaMap != nil {
			if qualifiedName, ok := lambdaMap[node.Id()]; ok {
				return []string{qualifiedName}
			}
		}
	case "call_expression":
		// Middleware wrapper: auth(handler) → ["auth", ...inner...]
		funcNode := node.ChildByFieldName("function")
		middlewareName := ""
		if funcNode != nil {
			switch funcNode.Kind() {
			case "identifier":
				middlewareName = funcNode.Utf8Text(content)
			case "member_expression":
				propNode := funcNode.ChildByFieldName("property")
				if propNode != nil {
					middlewareName = propNode.Utf8Text(content)
				}
			}
		}
		argsNode := node.ChildByFieldName("arguments")
		if argsNode != nil {
			var lastNamedArg *tree_sitter.Node
			for i := uint(0); i < argsNode.ChildCount(); i++ {
				child := argsNode.Child(i)
				if child.IsNamed() && child.Kind() != "string" && child.Kind() != "template_string" {
					lastNamedArg = child
				}
			}
			if lastNamedArg != nil {
				inner := resolveTSOneArg(lastNamedArg, content, lambdaMap)
				if middlewareName != "" && len(inner) > 0 {
					return append([]string{middlewareName}, inner...)
				}
				return inner
			}
		}
		if middlewareName != "" {
			return []string{middlewareName}
		}
	}
	return nil
}

func ExtractRoutes(node *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	// No-op: route extraction is now handled in the main extractCalls loop after lambda creation.
}

func extractDecoratorArg(decoratorText string) string {
	start := strings.Index(decoratorText, "(")
	if start < 0 {
		return ""
	}
	end := strings.Index(decoratorText, ")")
	if end <= start {
		return ""
	}
	arg := strings.TrimSpace(decoratorText[start+1 : end])
	if idx := strings.Index(arg, ","); idx >= 0 {
		arg = arg[:idx]
	}
	arg = strings.NewReplacer("\"", "", "'", "", " ", "").Replace(arg)
	return arg
}
