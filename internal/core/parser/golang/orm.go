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

// ExtractORM extracts GORM queries and raw SQL strings from Go function bodies.
func ExtractORM(bodyNode *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	if bodyNode == nil {
		return
	}

	extractGORMCalls(bodyNode, content, callerName, filePath, result)
	ExtractSQLStringsGo(bodyNode, content, callerName, filePath, result)
}

// extractGORMCalls detects GORM chain method calls (Find/Where/Raw/Exec etc).
func extractGORMCalls(bodyNode *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
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

		if !looksLikeGORMCall(funcNode, content) {
			return true
		}

		sqlText := ""
		if methodName == "Raw" || methodName == "Exec" {
			argumentsNode := node.ChildByFieldName("arguments")
			if argumentsNode != nil {
				for argumentIndex := uint(0); argumentIndex < argumentsNode.ChildCount(); argumentIndex++ {
					argument := argumentsNode.Child(argumentIndex)
					if argument.Kind() == "interpreted_string_literal" {
						sqlText = strings.Trim(argument.Utf8Text(content), "\"")
						queryType = sqlutil.DetectQueryType(sqlText)
						break
					}
				}
			}
		}

		if sqlText == "" {
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

// ExtractSQLStringsGo extracts SQL strings from a Go function body using variable tracking.
func ExtractSQLStringsGo(bodyNode *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	scope := sqlutil.NewScopeEnvironment(nil)
	processGoBlock(bodyNode, content, scope, "", result, callerName, filePath)
	sqlutil.EmitQueriesFromScope(scope, callerName, filePath, result)
}

func processGoBlock(blockNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment, parentCondition string, result *model.ParseResult, callerName, filePath string) {
	for index := uint(0); index < blockNode.NamedChildCount(); index++ {
		child := blockNode.NamedChild(index)
		if child.Kind() == "statement_list" {
			processGoBlock(child, content, scope, parentCondition, result, callerName, filePath)
			continue
		}
		processGoStatement(child, content, scope, parentCondition, result, callerName, filePath)
	}
}

func processGoStatement(statement *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment, parentCondition string, result *model.ParseResult, callerName, filePath string) {
	switch statement.Kind() {
	case "short_var_declaration":
		processGoShortVarDeclaration(statement, content, scope)
	case "var_declaration":
		processGoVarDeclaration(statement, content, scope)
	case "assignment_statement":
		processGoAssignment(statement, content, scope)
	case "if_statement":
		processGoIfStatement(statement, content, scope, parentCondition, result, callerName, filePath)
	case "expression_statement":
		// Go doesn't typically use expression_statement for SQL, skip
	case "block":
		processGoBlock(statement, content, scope, parentCondition, result, callerName, filePath)
	}
}

// processGoShortVarDeclaration handles sql := "..."
// Supports multi-variable declarations like: err, sql := nil, "SELECT ..."
func processGoShortVarDeclaration(declarationNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment) {
	leftNode := declarationNode.ChildByFieldName("left")
	rightNode := declarationNode.ChildByFieldName("right")
	if leftNode == nil || rightNode == nil {
		return
	}

	var names []string
	var valueNodes []*tree_sitter.Node

	if leftNode.Kind() == "expression_list" {
		for index := uint(0); index < leftNode.NamedChildCount(); index++ {
			names = append(names, leftNode.NamedChild(index).Utf8Text(content))
		}
	} else {
		names = append(names, leftNode.Utf8Text(content))
	}

	if rightNode.Kind() == "expression_list" {
		for index := uint(0); index < rightNode.NamedChildCount(); index++ {
			valueNodes = append(valueNodes, rightNode.NamedChild(index))
		}
	} else {
		valueNodes = append(valueNodes, rightNode)
	}

	for pairIndex := 0; pairIndex < len(names) && pairIndex < len(valueNodes); pairIndex++ {
		resolvedValue := resolveGoStringExpression(valueNodes[pairIndex], content)
		if resolvedValue != "" && sqlutil.IsSQLStub(resolvedValue) {
			scope.Set(names[pairIndex], &sqlutil.SQLVariable{
				Fragments: []string{resolvedValue},
				Line:      int(declarationNode.StartPosition().Row) + 1,
			})
		}
	}
}

// processGoVarDeclaration handles var sql string = "..."
// Supports multi-variable declarations like: var a, b = 1, "SELECT ..."
func processGoVarDeclaration(declarationNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment) {
	for index := uint(0); index < declarationNode.NamedChildCount(); index++ {
		child := declarationNode.NamedChild(index)
		if child.Kind() != "var_spec" {
			continue
		}

		var names []*tree_sitter.Node
		var values []*tree_sitter.Node

		for fieldIndex := uint(0); fieldIndex < child.NamedChildCount(); fieldIndex++ {
			fieldChild := child.NamedChild(fieldIndex)
			switch fieldChild.Kind() {
			case "identifier":
				names = append(names, fieldChild)
			case "expression_list":
				for expressionIndex := uint(0); expressionIndex < fieldChild.NamedChildCount(); expressionIndex++ {
					values = append(values, fieldChild.NamedChild(expressionIndex))
				}
			case "interpreted_string_literal", "raw_string_literal":
				values = append(values, fieldChild)
			}
		}

		for pairIndex := 0; pairIndex < len(names) && pairIndex < len(values); pairIndex++ {
			variableName := names[pairIndex].Utf8Text(content)
			resolvedValue := resolveGoStringExpression(values[pairIndex], content)
			if resolvedValue != "" && sqlutil.IsSQLStub(resolvedValue) {
				scope.Set(variableName, &sqlutil.SQLVariable{
					Fragments: []string{resolvedValue},
					Line:      int(child.StartPosition().Row) + 1,
				})
			}
		}
	}
}

// processGoAssignment handles sql = "..." and sql += "..."
// Supports multi-variable assignments like: a, sql = 1, "SELECT ..."
func processGoAssignment(assignmentNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment) {
	leftNode := assignmentNode.ChildByFieldName("left")
	rightNode := assignmentNode.ChildByFieldName("right")
	if leftNode == nil || rightNode == nil {
		return
	}

	operatorText := ""
	operatorNode := assignmentNode.ChildByFieldName("operator")
	if operatorNode != nil {
		operatorText = operatorNode.Utf8Text(content)
	}

	var names []string
	var valueNodes []*tree_sitter.Node

	if leftNode.Kind() == "expression_list" {
		for index := uint(0); index < leftNode.NamedChildCount(); index++ {
			names = append(names, leftNode.NamedChild(index).Utf8Text(content))
		}
	} else {
		names = append(names, leftNode.Utf8Text(content))
	}

	if rightNode.Kind() == "expression_list" {
		for index := uint(0); index < rightNode.NamedChildCount(); index++ {
			valueNodes = append(valueNodes, rightNode.NamedChild(index))
		}
	} else {
		valueNodes = append(valueNodes, rightNode)
	}

	for pairIndex := 0; pairIndex < len(names) && pairIndex < len(valueNodes); pairIndex++ {
		variableName := names[pairIndex]
		resolvedValue := resolveGoStringExpression(valueNodes[pairIndex], content)

		if operatorText == "+=" {
			existingVariable := scope.Lookup(variableName)
			if existingVariable == nil {
				continue
			}
			if resolvedValue == "" {
				existingVariable.Fragments = append(existingVariable.Fragments, "?")
			} else {
				existingVariable.Fragments = append(existingVariable.Fragments, resolvedValue)
			}
		} else if operatorText == "=" {
			if resolvedValue == "" {
				continue
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
	}
}

func processGoIfStatement(ifNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment, parentCondition string, result *model.ParseResult, callerName, filePath string) {
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
		processGoBlock(consequenceNode, content, scope, fullCondition, result, callerName, filePath)
	}

	sqlutil.RecordConditionalDiff(scope, snapshotBefore, fullCondition, false)
	sqlutil.RestoreSnapshot(scope, snapshotBefore)

	alternativeNode := ifNode.ChildByFieldName("alternative")
	if alternativeNode != nil {
		snapshotBeforeElse := scope.Snapshot()
		if alternativeNode.Kind() == "if_statement" {
			processGoIfStatement(alternativeNode, content, scope, parentCondition, result, callerName, filePath)
		} else {
			processGoBlock(alternativeNode, content, scope, fullCondition, result, callerName, filePath)
		}
		sqlutil.RecordConditionalDiff(scope, snapshotBeforeElse, fullCondition, true)
		sqlutil.RestoreSnapshot(scope, snapshotBeforeElse)
	}
}

// resolveGoStringExpression extracts a string literal value from a Go AST node.
func resolveGoStringExpression(node *tree_sitter.Node, content []byte) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case "interpreted_string_literal":
		text := node.Utf8Text(content)
		return strings.Trim(text, "\"")
	case "interpreted_string_literal_content":
		return node.Utf8Text(content)
	case "raw_string_literal":
		text := node.Utf8Text(content)
		return strings.Trim(text, "`")
	case "binary_expression":
		leftNode := node.ChildByFieldName("left")
		rightNode := node.ChildByFieldName("right")
		operatorNode := node.ChildByFieldName("operator")
		if operatorNode != nil && operatorNode.Utf8Text(content) == "+" {
			leftValue := resolveGoStringExpression(leftNode, content)
			rightValue := resolveGoStringExpression(rightNode, content)
			if leftValue != "" || rightValue != "" {
				return leftValue + rightValue
			}
		}
		return ""
	case "parenthesized_expression":
		if node.NamedChildCount() > 0 {
			return resolveGoStringExpression(node.NamedChild(0), content)
		}
		return ""
	default:
		return ""
	}
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
