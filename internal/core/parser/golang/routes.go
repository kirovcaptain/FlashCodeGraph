package golang

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/liuymcn/flash-code-graph/internal/core/parser/astutil"
	"github.com/liuymcn/flash-code-graph/internal/model"
)

var routeMethodNames = map[string]string{
	"GET":     "GET",
	"POST":    "POST",
	"PUT":     "PUT",
	"DELETE":  "DELETE",
	"PATCH":   "PATCH",
	"Handle":  "GET",
	"Any":     "GET",
}

type GroupPrefixes map[string]string

// collectGoGroupPrefixes scans a function body for router.Group() assignments.
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


func ExtractRoutes(node *tree_sitter.Node, content []byte, callerName, filePath string, groupPrefixes GroupPrefixes, result *model.ParseResult) {
	if node.Kind() != "call_expression" {
		return
	}
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil || funcNode.Kind() != "selector_expression" {
		return
	}
	field := funcNode.ChildByFieldName("field")
	if field == nil {
		return
	}
	methodName := field.Utf8Text(content)
	httpMethod, isRoute := routeMethodNames[methodName]
	if !isRoute {
		return
	}
	receiverNode := funcNode.ChildByFieldName("operand")
	receiverName := ""
	if receiverNode != nil {
		receiverName = receiverNode.Utf8Text(content)
	}
	argsNode := node.ChildByFieldName("arguments")
	if argsNode == nil {
		return
	}
	pathPattern := ""
	for i := uint(0); i < argsNode.ChildCount(); i++ {
		arg := argsNode.Child(i)
		if arg.Kind() == "interpreted_string_literal" {
			pathPattern = strings.Trim(arg.Utf8Text(content), "\"")
			break
		}
	}
	if pathPattern != "" {
		if prefix, exists := groupPrefixes[receiverName]; exists {
			pathPattern = prefix + pathPattern
		}
		result.Routes = append(result.Routes, model.RawRoute{
			Method:      httpMethod,
			PathPattern: pathPattern,
			HandlerName: callerName,
			Framework:   "gin",
			FilePath:    filePath,
			Line:        int(node.StartPosition().Row) + 1,
		})
	}
}
