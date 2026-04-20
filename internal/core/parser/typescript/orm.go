package typescript

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// TypeORM/Prisma/Sequelize method patterns
var ormMethods = map[string]string{
	"find":               "SELECT",
	"findOne":            "SELECT",
	"findMany":           "SELECT",
	"findFirst":          "SELECT",
	"findUnique":         "SELECT",
	"findAll":            "SELECT",
	"create":             "INSERT",
	"save":               "INSERT",
	"insert":             "INSERT",
	"update":             "UPDATE",
	"updateMany":         "UPDATE",
	"delete":             "DELETE",
	"deleteMany":         "DELETE",
	"remove":             "DELETE",
	"destroy":            "DELETE",
	"query":              "UNKNOWN",
	"createQueryBuilder": "SELECT",
}

// ExtractORM extracts ORM queries from TS/JS function bodies.
// Detects: TypeORM repository methods, Prisma client calls, Sequelize model methods.
func ExtractORM(bodyNode *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	if bodyNode == nil {
		return
	}

	astutil.WalkNamedChildren(bodyNode, func(node *tree_sitter.Node) bool {
		if node.Kind() != "call_expression" {
			return true
		}

		funcNode := node.ChildByFieldName("function")
		if funcNode == nil || funcNode.Kind() != "member_expression" {
			return true
		}

		propNode := funcNode.ChildByFieldName("property")
		if propNode == nil {
			return true
		}

		methodName := propNode.Utf8Text(content)
		queryType, isORM := ormMethods[methodName]
		if !isORM {
			return true
		}

		objNode := funcNode.ChildByFieldName("object")
		receiver := ""
		if objNode != nil {
			receiver = objNode.Utf8Text(content)
		}

		// Filter: only match known ORM receivers
		if !looksLikeORMReceiver(receiver) {
			return true
		}

		result.Queries = append(result.Queries, model.RawQuery{
			SQLText:    receiver + "." + methodName + "(...)",
			QueryType:  queryType,
			CallerName: callerName,
			FilePath:   filePath,
			Line:       int(node.StartPosition().Row) + 1,
		})

		return true
	})
}

func looksLikeORMReceiver(receiver string) bool {
	lower := strings.ToLower(receiver)
	ormKeywords := []string{
		"repository", "repo", "prisma", "model",
		"db", "sequelize", "connection", "manager",
	}
	for _, keyword := range ormKeywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	// Also match this.xxxRepository pattern
	if strings.HasPrefix(receiver, "this.") && strings.HasSuffix(strings.ToLower(receiver), "repository") {
		return true
	}
	return false
}
