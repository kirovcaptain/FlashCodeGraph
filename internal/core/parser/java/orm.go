package java

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/sqlutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// ExtractORM extracts ORM queries from Java method bodies.
// Detects: @Query annotations, string SQL literals, MyBatis-style queries.
func ExtractORM(annotations []string, bodyNode *tree_sitter.Node, content []byte, callerName, filePath string, startLine int, result *model.ParseResult) {
	// 1. @Query annotation with JPQL/SQL
	for _, annotation := range annotations {
		if !strings.Contains(annotation, "Query") {
			continue
		}
		sqlText := extractAnnotationValue(annotation)
		if sqlText == "" {
			continue
		}
		queryType := sqlutil.DetectQueryType(sqlText)
		tables := sqlutil.ExtractTablesFromSQL(sqlText)
		result.Queries = append(result.Queries, model.RawQuery{
			SQLText:    sqlText,
			QueryType:  queryType,
			Tables:     tables,
			CallerName: callerName,
			FilePath:   filePath,
			Line:       startLine,
		})
	}

	// 2. String SQL literals in method body
	if bodyNode != nil {
		extractSQLStrings(bodyNode, content, callerName, filePath, result)
	}
}

// extractSQLStrings finds string literals containing SQL keywords.
func extractSQLStrings(node *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	astutil.WalkNamedChildren(node, func(child *tree_sitter.Node) bool {
		if child.Kind() == "string_literal" || child.Kind() == "string_fragment" {
			text := child.Utf8Text(content)
			text = strings.Trim(text, "\"")
			if sqlutil.IsSQLStatement(text) {
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



