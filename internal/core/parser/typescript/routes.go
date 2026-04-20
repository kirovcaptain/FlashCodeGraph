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
					HandlerName: handlerName,
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
	"use":    "MIDDLEWARE",
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
				HandlerName: callerName,
				Framework:   "express",
				FilePath:    filePath,
				Line:        int(node.StartPosition().Row) + 1,
			})
		}
	}
}

func ExtractRoutes(node *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	if node.Kind() != "call_expression" {
		return
	}

	funcNode := node.ChildByFieldName("function")
	if funcNode == nil || funcNode.Kind() != "member_expression" {
		return
	}

	objNode := funcNode.ChildByFieldName("object")
	propNode := funcNode.ChildByFieldName("property")
	if objNode == nil || propNode == nil {
		return
	}

	receiverName := objNode.Utf8Text(content)
	methodName := propNode.Utf8Text(content)

	// Match common framework receiver names
	validReceivers := map[string]bool{
		"app": true, "router": true, "server": true,
		"route": true, "api": true, "express": true,
	}
	if !validReceivers[receiverName] {
		return
	}

	httpMethod, isRoute := routeMethodNames[methodName]
	if !isRoute {
		return
	}

	// Extract first string argument as path
	argsNode := node.ChildByFieldName("arguments")
	if argsNode == nil {
		return
	}

	pathPattern := ""
	for i := uint(0); i < argsNode.ChildCount(); i++ {
		arg := argsNode.Child(i)
		if arg.Kind() == "string" || arg.Kind() == "template_string" {
			pathPattern = strings.Trim(arg.Utf8Text(content), "\"'`")
			break
		}
	}

	if pathPattern != "" {
		result.Routes = append(result.Routes, model.RawRoute{
			Method:      httpMethod,
			PathPattern: pathPattern,
			HandlerName: callerName,
			Framework:   "express",
			FilePath:    filePath,
			Line:        int(node.StartPosition().Row) + 1,
		})
	}
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
