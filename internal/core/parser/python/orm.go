package python

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/sqlutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// ORM call patterns: receiver.method → query type
var ormPatterns = map[string]string{
	"query":   "SELECT",
	"filter":  "SELECT",
	"get":     "SELECT",
	"all":     "SELECT",
	"first":   "SELECT",
	"add":     "INSERT",
	"save":    "INSERT",
	"commit":  "INSERT",
	"delete":  "DELETE",
	"update":  "UPDATE",
	"execute": "UNKNOWN",
}

// ORM receiver patterns that indicate ORM usage
var ormReceivers = map[string]bool{
	"session": true, // SQLAlchemy
	"db":      true, // SQLAlchemy
	"objects": true, // Django ORM
	"cursor":  true, // raw DB
}

// ExtractORM extracts ORM queries from Python function bodies.
// Detects: SQLAlchemy session.query(), Django Model.objects.filter(), raw SQL strings.
func ExtractORM(bodyNode *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	if bodyNode == nil {
		return
	}

	extractPythonORMCalls(bodyNode, content, callerName, filePath, result)
	ExtractSQLStringsPython(bodyNode, content, callerName, filePath, result)
}

// extractPythonORMCalls detects ORM method calls (SQLAlchemy/Django).
func extractPythonORMCalls(bodyNode *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
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

			queryType, isORM := ormPatterns[method]
			if !isORM {
				return true
			}

			receiverParts := strings.Split(receiver, ".")
			lastPart := receiverParts[len(receiverParts)-1]
			if !ormReceivers[lastPart] && !strings.HasSuffix(receiver, ".objects") {
				return true
			}

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
}

// ExtractSQLStringsPython extracts SQL strings from a Python function body using variable tracking.
func ExtractSQLStringsPython(bodyNode *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	scope := sqlutil.NewScopeEnvironment(nil)
	processPythonBlock(bodyNode, content, scope, "", result, callerName, filePath)
	sqlutil.EmitQueriesFromScope(scope, callerName, filePath, result)
}

func processPythonBlock(blockNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment, parentCondition string, result *model.ParseResult, callerName, filePath string) {
	for index := uint(0); index < blockNode.NamedChildCount(); index++ {
		child := blockNode.NamedChild(index)
		processPythonStatement(child, content, scope, parentCondition, result, callerName, filePath)
	}
}

func processPythonStatement(statement *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment, parentCondition string, result *model.ParseResult, callerName, filePath string) {
	switch statement.Kind() {
	case "expression_statement":
		expressionNode := statement.NamedChild(0)
		if expressionNode != nil {
			processPythonExpression(expressionNode, content, scope, result, callerName, filePath)
		}
	case "if_statement":
		processPythonIfStatement(statement, content, scope, parentCondition, result, callerName, filePath)
	case "block":
		processPythonBlock(statement, content, scope, parentCondition, result, callerName, filePath)
	}
}

func processPythonExpression(expressionNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment, result *model.ParseResult, callerName, filePath string) {
	switch expressionNode.Kind() {
	case "assignment":
		processPythonAssignment(expressionNode, content, scope)
	case "augmented_assignment":
		processPythonAugmentedAssignment(expressionNode, content, scope)
	}
}

// processPythonAssignment handles sql = "..."
func processPythonAssignment(assignmentNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment) {
	leftNode := assignmentNode.ChildByFieldName("left")
	rightNode := assignmentNode.ChildByFieldName("right")
	if leftNode == nil || rightNode == nil {
		return
	}

	variableName := leftNode.Utf8Text(content)
	resolvedValue := resolvePythonStringExpression(rightNode, content)
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

// processPythonAugmentedAssignment handles sql += "..."
func processPythonAugmentedAssignment(assignmentNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment) {
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

	resolvedValue := resolvePythonStringExpression(rightNode, content)
	if resolvedValue == "" {
		existingVariable.Fragments = append(existingVariable.Fragments, "?")
	} else {
		existingVariable.Fragments = append(existingVariable.Fragments, resolvedValue)
	}
}

func processPythonIfStatement(ifNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment, parentCondition string, result *model.ParseResult, callerName, filePath string) {
	conditionNode := ifNode.ChildByFieldName("condition")
	conditionText := ""
	if conditionNode != nil {
		conditionText = conditionNode.Utf8Text(content)
	}

	fullCondition := conditionText
	if parentCondition != "" {
		fullCondition = parentCondition + " && " + conditionText
	}

	snapshotBefore := scope.Snapshot()

	consequenceNode := ifNode.ChildByFieldName("consequence")
	if consequenceNode != nil {
		processPythonBlock(consequenceNode, content, scope, fullCondition, result, callerName, filePath)
	}

	sqlutil.RecordConditionalDiff(scope, snapshotBefore, fullCondition, false)
	sqlutil.RestoreSnapshot(scope, snapshotBefore)

	alternativeNode := ifNode.ChildByFieldName("alternative")
	if alternativeNode != nil {
		snapshotBeforeElse := scope.Snapshot()
		if alternativeNode.Kind() == "elif_clause" {
			// elif is like a nested if
			processPythonElifClause(alternativeNode, content, scope, parentCondition, result, callerName, filePath)
		} else if alternativeNode.Kind() == "else_clause" {
			elseBody := alternativeNode.ChildByFieldName("body")
			if elseBody != nil {
				processPythonBlock(elseBody, content, scope, fullCondition, result, callerName, filePath)
			}
		}
		sqlutil.RecordConditionalDiff(scope, snapshotBeforeElse, fullCondition, true)
		sqlutil.RestoreSnapshot(scope, snapshotBeforeElse)
	}
}

func processPythonElifClause(elifNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment, parentCondition string, result *model.ParseResult, callerName, filePath string) {
	conditionNode := elifNode.ChildByFieldName("condition")
	conditionText := ""
	if conditionNode != nil {
		conditionText = conditionNode.Utf8Text(content)
	}

	fullCondition := conditionText
	if parentCondition != "" {
		fullCondition = parentCondition + " && " + conditionText
	}

	consequenceNode := elifNode.ChildByFieldName("consequence")
	if consequenceNode != nil {
		processPythonBlock(consequenceNode, content, scope, fullCondition, result, callerName, filePath)
	}
}

// resolvePythonStringExpression extracts a string literal value from a Python AST node.
func resolvePythonStringExpression(node *tree_sitter.Node, content []byte) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case "string":
		text := node.Utf8Text(content)
		// Strip quotes: "...", '...', """...""", '''...'''
		if strings.HasPrefix(text, `"""`) && strings.HasSuffix(text, `"""`) {
			return strings.TrimSpace(text[3 : len(text)-3])
		}
		if strings.HasPrefix(text, `'''`) && strings.HasSuffix(text, `'''`) {
			return strings.TrimSpace(text[3 : len(text)-3])
		}
		if len(text) >= 2 {
			return text[1 : len(text)-1]
		}
		return ""
	case "string_content":
		return node.Utf8Text(content)
	case "concatenated_string":
		var parts []string
		for index := uint(0); index < node.NamedChildCount(); index++ {
			child := node.NamedChild(index)
			part := resolvePythonStringExpression(child, content)
			if part != "" {
				parts = append(parts, part)
			}
		}
		return strings.Join(parts, "")
	case "binary_operator":
		leftNode := node.ChildByFieldName("left")
		rightNode := node.ChildByFieldName("right")
		operatorNode := node.ChildByFieldName("operator")
		if operatorNode != nil && operatorNode.Utf8Text(content) == "+" {
			leftValue := resolvePythonStringExpression(leftNode, content)
			rightValue := resolvePythonStringExpression(rightNode, content)
			if leftValue != "" || rightValue != "" {
				return leftValue + rightValue
			}
		}
		return ""
	case "parenthesized_expression":
		if node.NamedChildCount() > 0 {
			return resolvePythonStringExpression(node.NamedChild(0), content)
		}
		return ""
	default:
		return ""
	}
}
