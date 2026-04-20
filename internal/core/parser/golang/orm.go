package golang

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/sqlutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// GORM method patterns
var gormMethods = map[string]string{
	"Find":    "SELECT",
	"First":   "SELECT",
	"Last":    "SELECT",
	"Take":    "SELECT",
	"Where":   "SELECT",
	"Create":  "INSERT",
	"Save":    "INSERT",
	"Update":  "UPDATE",
	"Updates": "UPDATE",
	"Delete":  "DELETE",
	"Raw":     "UNKNOWN",
	"Exec":    "UNKNOWN",
}

// ExtractORM extracts GORM queries from Go function bodies.
func ExtractORM(bodyNode *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	if bodyNode == nil {
		return
	}

	astutil.WalkNamedChildren(bodyNode, func(node *tree_sitter.Node) bool {
		if node.Kind() != "call_expression" {
			return true
		}

		funcNode := node.ChildByFieldName("function")
		if funcNode == nil || funcNode.Kind() != "selector_expression" {
			return true
		}

		field := funcNode.ChildByFieldName("field")
		if field == nil {
			return true
		}

		methodName := field.Utf8Text(content)
		queryType, isGORM := gormMethods[methodName]
		if !isGORM {
			return true
		}

		// Verify receiver chain contains a known DB variable
		if !looksLikeGORMCall(funcNode, content) {
			return true
		}


		// Check for Raw/Exec with SQL string argument
		sqlText := ""
		if methodName == "Raw" || methodName == "Exec" {
			argsNode := node.ChildByFieldName("arguments")
			if argsNode != nil {
				for i := uint(0); i < argsNode.ChildCount(); i++ {
					arg := argsNode.Child(i)
					if arg.Kind() == "interpreted_string_literal" {
						sqlText = strings.Trim(arg.Utf8Text(content), "\"")
						queryType = sqlutil.DetectQueryType(sqlText)
						break
					}
				}
			}
		}

		if sqlText == "" {
			// Build description from method chain
			receiver := funcNode.ChildByFieldName("operand")
			receiverText := ""
			if receiver != nil {
				receiverText = receiver.Utf8Text(content)
			}
			sqlText = receiverText + "." + methodName + "(...)"
		}

		result.Queries = append(result.Queries, model.RawQuery{
			SQLText:    sqlText,
			QueryType:  queryType,
			CallerName: callerName,
			FilePath:   filePath,
			Line:       int(node.StartPosition().Row) + 1,
		})

		return true
	})
}


// looksLikeGORMCall checks if a selector expression chain contains "db" as the root receiver.
func looksLikeGORMCall(node *tree_sitter.Node, content []byte) bool {
	current := node
	for current != nil {
		if current.Kind() == "identifier" {
			name := current.Utf8Text(content)
			return name == "db" || name == "DB" || name == "gormDB" || name == "conn"
		}
		if current.Kind() == "selector_expression" {
			current = current.ChildByFieldName("operand")
			continue
		}
		if current.Kind() == "call_expression" {
			funcChild := current.ChildByFieldName("function")
			if funcChild != nil {
				current = funcChild
				continue
			}
		}
		break
	}
	return false
}
