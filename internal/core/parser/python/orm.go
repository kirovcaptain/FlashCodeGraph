package python

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/liuymcn/flash-code-graph/internal/core/parser/astutil"
	"github.com/liuymcn/flash-code-graph/internal/core/parser/sqlutil"
	"github.com/liuymcn/flash-code-graph/internal/model"
)

// ORM call patterns: receiver.method → query type
var ormPatterns = map[string]string{
	"query":    "SELECT",
	"filter":   "SELECT",
	"get":      "SELECT",
	"all":      "SELECT",
	"first":    "SELECT",
	"add":      "INSERT",
	"save":     "INSERT",
	"commit":   "INSERT",
	"delete":   "DELETE",
	"update":   "UPDATE",
	"execute":  "UNKNOWN",
}

// ORM receiver patterns that indicate ORM usage
var ormReceivers = map[string]bool{
	"session":  true, // SQLAlchemy
	"db":       true, // SQLAlchemy
	"objects":  true, // Django ORM
	"cursor":   true, // raw DB
}

// ExtractORM extracts ORM queries from Python function bodies.
// Detects: SQLAlchemy session.query(), Django Model.objects.filter(), raw SQL strings.
func ExtractORM(bodyNode *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	if bodyNode == nil {
		return
	}

	astutil.WalkNamedChildren(bodyNode, func(node *tree_sitter.Node) bool {
		if node.Kind() != "call" {
			return true
		}

		funcNode := node.ChildByFieldName("function")
		if funcNode == nil {
			return true
		}

		if funcNode.Kind() == "attribute" {
			objNode := funcNode.ChildByFieldName("object")
			attrNode := funcNode.ChildByFieldName("attribute")
			if objNode == nil || attrNode == nil {
				return true
			}

			receiver := objNode.Utf8Text(content)
			method := attrNode.Utf8Text(content)

			// Check if this is an ORM call
			queryType, isORM := ormPatterns[method]
			if !isORM {
				return true
			}

			// Verify receiver looks like ORM
			receiverParts := strings.Split(receiver, ".")
			lastPart := receiverParts[len(receiverParts)-1]
			if !ormReceivers[lastPart] && !strings.HasSuffix(receiver, ".objects") {
				return true
			}

			// Extract model/table name from receiver (e.g., User.objects → User)
			tableName := ""
			if strings.HasSuffix(receiver, ".objects") {
				tableName = strings.TrimSuffix(receiver, ".objects")
			}

			var tables []string
			if tableName != "" {
				tables = []string{tableName}
			}

			result.Queries = append(result.Queries, model.RawQuery{
				SQLText:    receiver + "." + method + "(...)",
				QueryType:  queryType,
				Tables:     tables,
				CallerName: callerName,
				FilePath:   filePath,
				Line:       int(node.StartPosition().Row) + 1,
			})
		}

		return true
	})

	// Also check for raw SQL strings
	extractPythonSQLStrings(bodyNode, content, callerName, filePath, result)
}

func extractPythonSQLStrings(node *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	astutil.WalkNamedChildren(node, func(child *tree_sitter.Node) bool {
		if child.Kind() == "string" || child.Kind() == "concatenated_string" {
			text := strings.Trim(child.Utf8Text(content), "\"'")
			upper := strings.ToUpper(strings.TrimSpace(text))
			if strings.HasPrefix(upper, "SELECT ") || strings.HasPrefix(upper, "INSERT ") ||
				strings.HasPrefix(upper, "UPDATE ") || strings.HasPrefix(upper, "DELETE ") {
				result.Queries = append(result.Queries, model.RawQuery{
					SQLText:    text,
					QueryType:  sqlutil.DetectQueryType(text),
					Tables:     sqlutil.ExtractTablesFromSQL(text),
					CallerName: callerName,
					FilePath:   filePath,
					Line:       int(child.StartPosition().Row) + 1,
				})
			}
		}
		return true
	})
}


