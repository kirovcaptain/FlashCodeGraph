package golang

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

var routeMethodNames = map[string]string{
	"GET":    "GET",
	"POST":   "POST",
	"PUT":    "PUT",
	"DELETE": "DELETE",
	"PATCH":  "PATCH",
	"Handle": "GET",
	"Any":    "GET",
}

type GroupPrefixes map[string]string

// RouteDetection holds the result of detecting a route registration call.
type RouteDetection struct {
	Method      string
	PathPattern string
	ArgsNode    *tree_sitter.Node
}

// CollectGroupPrefixes scans a function body for router.Group() assignments.
func CollectGroupPrefixes(body *tree_sitter.Node, content []byte) GroupPrefixes {
	prefixes := make(GroupPrefixes)
	astutil.WalkNamedChildren(body, func(node *tree_sitter.Node) bool {
		if node.Kind() != "short_var_declaration" && node.Kind() != "assignment_statement" {
			return true
		}
		leftNode := node.ChildByFieldName("left")
		if leftNode == nil {
			return true
		}
		varName := ""
		if leftNode.Kind() == "expression_list" {
			for i := uint(0); i < leftNode.ChildCount(); i++ {
				if leftNode.Child(i).Kind() == "identifier" {
					varName = leftNode.Child(i).Utf8Text(content)
					break
				}
			}
		} else if leftNode.Kind() == "identifier" {
			varName = leftNode.Utf8Text(content)
		}
		if varName == "" {
			return true
		}
		rightNode := node.ChildByFieldName("right")
		if rightNode == nil {
			return true
		}
		callNode := rightNode
		if rightNode.Kind() == "expression_list" {
			for i := uint(0); i < rightNode.ChildCount(); i++ {
				if rightNode.Child(i).Kind() == "call_expression" {
					callNode = rightNode.Child(i)
					break
				}
			}
		}
		if callNode.Kind() != "call_expression" {
			return true
		}
		funcNode := callNode.ChildByFieldName("function")
		if funcNode == nil || funcNode.Kind() != "selector_expression" {
			return true
		}
		field := funcNode.ChildByFieldName("field")
		if field == nil || field.Utf8Text(content) != "Group" {
			return true
		}
		receiverNode := funcNode.ChildByFieldName("operand")
		receiverName := ""
		if receiverNode != nil {
			receiverName = receiverNode.Utf8Text(content)
		}
		argsNode := callNode.ChildByFieldName("arguments")
		if argsNode == nil {
			return true
		}
		groupPrefix := ""
		for i := uint(0); i < argsNode.ChildCount(); i++ {
			arg := argsNode.Child(i)
			if arg.Kind() == "interpreted_string_literal" {
				groupPrefix = strings.Trim(arg.Utf8Text(content), "\"")
				break
			}
		}
		prefixes[varName] = prefixes[receiverName] + groupPrefix
		return true
	})
	return prefixes
}

// DetectRoute checks if a call_expression is a route registration (e.g. r.GET("/path", ...)).
// Returns route info without writing to result, or nil if not a route call.
func DetectRoute(node *tree_sitter.Node, content []byte, groupPrefixes GroupPrefixes) *RouteDetection {
	if node.Kind() != "call_expression" {
		return nil
	}
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil || funcNode.Kind() != "selector_expression" {
		return nil
	}
	field := funcNode.ChildByFieldName("field")
	if field == nil {
		return nil
	}
	methodName := field.Utf8Text(content)
	httpMethod, isRoute := routeMethodNames[methodName]
	if !isRoute {
		return nil
	}
	receiverNode := funcNode.ChildByFieldName("operand")
	receiverName := ""
	if receiverNode != nil {
		receiverName = receiverNode.Utf8Text(content)
	}
	argsNode := node.ChildByFieldName("arguments")
	if argsNode == nil {
		return nil
	}
	pathPattern := ""
	for i := uint(0); i < argsNode.ChildCount(); i++ {
		arg := argsNode.Child(i)
		if arg.Kind() == "interpreted_string_literal" {
			pathPattern = strings.Trim(arg.Utf8Text(content), "\"")
			break
		}
	}
	if pathPattern == "" {
		return nil
	}
	if prefix, exists := groupPrefixes[receiverName]; exists {
		pathPattern = prefix + pathPattern
	}
	return &RouteDetection{
		Method:      httpMethod,
		PathPattern: pathPattern,
		ArgsNode:    argsNode,
	}
}

// ResolveHandlerArgs extracts the ordered handler chain from route arguments.
// Skips string literal arguments (path), processes all other named arguments.
func ResolveHandlerArgs(argsNode *tree_sitter.Node, content []byte, lambdaMap map[uintptr]string) []string {
	if argsNode == nil {
		return nil
	}
	var handlers []string
	for i := uint(0); i < argsNode.ChildCount(); i++ {
		child := argsNode.Child(i)
		if !child.IsNamed() {
			continue
		}
		if child.Kind() == "interpreted_string_literal" {
			continue
		}
		resolved := resolveOneArg(child, content, lambdaMap)
		handlers = append(handlers, resolved...)
	}
	return handlers
}

// resolveOneArg recursively resolves a single argument node into handler names.
// For middleware wrappers like auth(log(handler)), returns ["auth", "log", "handler"].
func resolveOneArg(node *tree_sitter.Node, content []byte, lambdaMap map[uintptr]string) []string {
	switch node.Kind() {
	case "identifier":
		return []string{node.Utf8Text(content)}
	case "selector_expression":
		return []string{node.Utf8Text(content)}
	case "func_literal":
		if lambdaMap != nil {
			if qualifiedName, ok := lambdaMap[node.Id()]; ok {
				return []string{qualifiedName}
			}
		}
	case "unary_expression":
		// &userHandler{} → recurse into operand
		operand := node.ChildByFieldName("operand")
		if operand != nil {
			return resolveOneArg(operand, content, lambdaMap)
		}
	case "composite_literal":
		// userHandler{} → extract type name
		typeNode := node.ChildByFieldName("type")
		if typeNode != nil {
			return []string{typeNode.Utf8Text(content)}
		}
	case "call_expression":
		// Middleware wrapper: auth(handler) → ["auth", ...inner...]
		funcNode := node.ChildByFieldName("function")
		middlewareName := ""
		if funcNode != nil {
			switch funcNode.Kind() {
			case "identifier":
				middlewareName = funcNode.Utf8Text(content)
			case "selector_expression":
				fieldNode := funcNode.ChildByFieldName("field")
				if fieldNode != nil {
					middlewareName = fieldNode.Utf8Text(content)
				}
			}
		}
		// Recurse into the last non-string argument
		argsNode := node.ChildByFieldName("arguments")
		if argsNode != nil {
			var lastNamedArg *tree_sitter.Node
			for i := uint(0); i < argsNode.ChildCount(); i++ {
				child := argsNode.Child(i)
				if child.IsNamed() && child.Kind() != "interpreted_string_literal" {
					lastNamedArg = child
				}
			}
			if lastNamedArg != nil {
				inner := resolveOneArg(lastNamedArg, content, lambdaMap)
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

// ExtractRoutes is kept for backward compatibility but now delegates to DetectRoute.
// It is called from extractCalls BEFORE lambda creation, so it uses callerName as fallback.
// The proper handler extraction happens in the main loop AFTER lambda creation.
func ExtractRoutes(node *tree_sitter.Node, content []byte, callerName, filePath string, groupPrefixes GroupPrefixes, result *model.ParseResult) {
	// No-op: route extraction is now handled in the main extractCalls loop after lambda creation.
}
