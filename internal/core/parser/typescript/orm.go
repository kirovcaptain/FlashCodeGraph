package typescript

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/sqlutil"
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
// Detects: TypeORM repository methods, Prisma client calls, Sequelize model methods, raw SQL strings.
func ExtractORM(bodyNode *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	if bodyNode == nil {
		return
	}

	extractTSORMCalls(bodyNode, content, callerName, filePath, result)
	ExtractSQLStringsTypeScript(bodyNode, content, callerName, filePath, result)
}

// extractTSORMCalls detects ORM method calls (TypeORM/Prisma/Sequelize).
func extractTSORMCalls(bodyNode *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
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

// ExtractSQLStringsTypeScript extracts SQL strings from a TS/JS function body using variable tracking.
func ExtractSQLStringsTypeScript(bodyNode *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	scope := sqlutil.NewScopeEnvironment(nil)
	processTSBlock(bodyNode, content, scope, "", result, callerName, filePath)
	sqlutil.EmitQueriesFromScope(scope, callerName, filePath, result)
}

func processTSBlock(blockNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment, parentCondition string, result *model.ParseResult, callerName, filePath string) {
	for index := uint(0); index < blockNode.NamedChildCount(); index++ {
		child := blockNode.NamedChild(index)
		processTSStatement(child, content, scope, parentCondition, result, callerName, filePath)
	}
}

func processTSStatement(statement *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment, parentCondition string, result *model.ParseResult, callerName, filePath string) {
	switch statement.Kind() {
	case "lexical_declaration", "variable_declaration":
		processTSVariableDeclaration(statement, content, scope)
	case "expression_statement":
		expressionNode := statement.NamedChild(0)
		if expressionNode != nil {
			processTSExpression(expressionNode, content, scope, result, callerName, filePath)
		}
	case "if_statement":
		processTSIfStatement(statement, content, scope, parentCondition, result, callerName, filePath)
	case "statement_block":
		processTSBlock(statement, content, scope, parentCondition, result, callerName, filePath)
	}
}

func processTSVariableDeclaration(declarationNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment) {
	for index := uint(0); index < declarationNode.NamedChildCount(); index++ {
		child := declarationNode.NamedChild(index)
		if child.Kind() != "variable_declarator" {
			continue
		}
		nameNode := child.ChildByFieldName("name")
		valueNode := child.ChildByFieldName("value")
		if nameNode == nil || valueNode == nil {
			continue
		}

		variableName := nameNode.Utf8Text(content)
		resolvedValue := resolveTSStringExpression(valueNode, content)
		if resolvedValue != "" && sqlutil.IsSQLStub(resolvedValue) {
			scope.Set(variableName, &sqlutil.SQLVariable{
				Fragments: []string{resolvedValue},
				Line:      int(child.StartPosition().Row) + 1,
			})
		}
	}
}

func processTSExpression(expressionNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment, result *model.ParseResult, callerName, filePath string) {
	switch expressionNode.Kind() {
	case "assignment_expression":
		processTSAssignment(expressionNode, content, scope)
	case "augmented_assignment_expression":
		processTSAugmentedAssignment(expressionNode, content, scope)
	}
}

func processTSAssignment(assignmentNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment) {
	leftNode := assignmentNode.ChildByFieldName("left")
	rightNode := assignmentNode.ChildByFieldName("right")
	if leftNode == nil || rightNode == nil {
		return
	}

	variableName := leftNode.Utf8Text(content)
	resolvedValue := resolveTSStringExpression(rightNode, content)
	if resolvedValue == "" {
		return
	}

	existingVariable := scope.Lookup(variableName)
	if existingVariable != nil {
		existingVariable.Fragments = []string{resolvedValue}
	} else if sqlutil.IsSQLStub(resolvedValue) {
		scope.Set(variableName, &sqlutil.SQLVariable{
			Fragments: []string{resolvedValue},
			Line:      int(assignmentNode.StartPosition().Row) + 1,
		})
	}
}

func processTSAugmentedAssignment(assignmentNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment) {
	leftNode := assignmentNode.ChildByFieldName("left")
	rightNode := assignmentNode.ChildByFieldName("right")
	if leftNode == nil || rightNode == nil {
		return
	}

	variableName := leftNode.Utf8Text(content)
	existingVariable := scope.Lookup(variableName)
	if existingVariable == nil {
		return
	}

	resolvedValue := resolveTSStringExpression(rightNode, content)
	if resolvedValue == "" {
		existingVariable.Fragments = append(existingVariable.Fragments, "?")
	} else {
		existingVariable.Fragments = append(existingVariable.Fragments, resolvedValue)
	}
}

func processTSIfStatement(ifNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment, parentCondition string, result *model.ParseResult, callerName, filePath string) {
	conditionNode := ifNode.ChildByFieldName("condition")
	conditionText := ""
	if conditionNode != nil {
		conditionText = conditionNode.Utf8Text(content)
		conditionText = strings.TrimPrefix(conditionText, "(")
		conditionText = strings.TrimSuffix(conditionText, ")")
	}

	fullCondition := conditionText
	if parentCondition != "" {
		fullCondition = parentCondition + " && " + conditionText
	}

	snapshotBefore := scope.Snapshot()

	consequenceNode := ifNode.ChildByFieldName("consequence")
	if consequenceNode != nil {
		processTSBlock(consequenceNode, content, scope, fullCondition, result, callerName, filePath)
	}

	sqlutil.RecordConditionalDiff(scope, snapshotBefore, fullCondition, false)
	sqlutil.RestoreSnapshot(scope, snapshotBefore)

	alternativeNode := ifNode.ChildByFieldName("alternative")
	if alternativeNode != nil {
		snapshotBeforeElse := scope.Snapshot()
		// else_clause contains a statement_block or another if_statement
		for childIndex := uint(0); childIndex < alternativeNode.NamedChildCount(); childIndex++ {
			elseChild := alternativeNode.NamedChild(childIndex)
			if elseChild.Kind() == "if_statement" {
				processTSIfStatement(elseChild, content, scope, parentCondition, result, callerName, filePath)
			} else if elseChild.Kind() == "statement_block" {
				processTSBlock(elseChild, content, scope, fullCondition, result, callerName, filePath)
			}
		}
		sqlutil.RecordConditionalDiff(scope, snapshotBeforeElse, fullCondition, true)
		sqlutil.RestoreSnapshot(scope, snapshotBeforeElse)
	}
}

// resolveTSStringExpression extracts a string literal value from a TS/JS AST node.
func resolveTSStringExpression(node *tree_sitter.Node, content []byte) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case "string":
		text := node.Utf8Text(content)
		if len(text) >= 2 {
			return text[1 : len(text)-1]
		}
		return ""
	case "string_fragment":
		return node.Utf8Text(content)
	case "binary_expression":
		leftNode := node.ChildByFieldName("left")
		rightNode := node.ChildByFieldName("right")
		operatorNode := node.ChildByFieldName("operator")
		if operatorNode != nil && operatorNode.Utf8Text(content) == "+" {
			leftValue := resolveTSStringExpression(leftNode, content)
			rightValue := resolveTSStringExpression(rightNode, content)
			if leftValue != "" || rightValue != "" {
				return leftValue + rightValue
			}
		}
		return ""
	case "parenthesized_expression":
		if node.NamedChildCount() > 0 {
			return resolveTSStringExpression(node.NamedChild(0), content)
		}
		return ""
	default:
		return ""
	}
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
	if strings.HasPrefix(receiver, "this.") && strings.HasSuffix(strings.ToLower(receiver), "repository") {
		return true
	}
	return false
}
