package java

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/sqlutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// ExtractORM extracts ORM queries from Java method bodies.
// Detects: @Query annotations, string SQL literals, MyBatis-style queries.
func ExtractORM(annotations []model.StructuredAnnotation, bodyNode *tree_sitter.Node, content []byte, callerName, filePath string, startLine int, result *model.ParseResult) {
	// 1. @Query annotation with JPQL/SQL
	for _, annotation := range annotations {
		if annotation.Name != "Query" {
			continue
		}
		sqlText := annotation.Params["value"]
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
		ExtractSQLStringsJava(bodyNode, content, callerName, filePath, result)
	}
}

// ExtractSQLStringsJava extracts SQL strings from a Java method body using variable tracking.
func ExtractSQLStringsJava(bodyNode *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	scope := sqlutil.NewScopeEnvironment(nil)
	processBlock(bodyNode, content, scope, "", result, callerName, filePath)
	sqlutil.EmitQueriesFromScope(scope, callerName, filePath, result)
}

func processBlock(blockNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment, parentCondition string, result *model.ParseResult, callerName, filePath string) {
	for index := uint(0); index < blockNode.NamedChildCount(); index++ {
		statement := blockNode.NamedChild(index)
		processStatement(statement, content, scope, parentCondition, result, callerName, filePath)
	}
}

func processStatement(statement *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment, parentCondition string, result *model.ParseResult, callerName, filePath string) {
	switch statement.Kind() {
	case "local_variable_declaration":
		processVariableDeclaration(statement, content, scope)
	case "expression_statement":
		expressionNode := statement.NamedChild(0)
		if expressionNode != nil {
			processExpression(expressionNode, content, scope, result, callerName, filePath)
		}
	case "if_statement":
		processIfStatement(statement, content, scope, parentCondition, result, callerName, filePath)
	case "block":
		processBlock(statement, content, scope, parentCondition, result, callerName, filePath)
	}
}

func processVariableDeclaration(declarationNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment) {
	declaratorNode := declarationNode.ChildByFieldName("declarator")
	if declaratorNode == nil {
		for index := uint(0); index < declarationNode.NamedChildCount(); index++ {
			child := declarationNode.NamedChild(index)
			if child.Kind() == "variable_declarator" {
				declaratorNode = child
				break
			}
		}
	}
	if declaratorNode == nil {
		return
	}

	nameNode := declaratorNode.ChildByFieldName("name")
	valueNode := declaratorNode.ChildByFieldName("value")
	if nameNode == nil || valueNode == nil {
		return
	}

	variableName := nameNode.Utf8Text(content)

	// StringBuilder: new StringBuilder("...")
	if valueNode.Kind() == "object_creation_expression" {
		resolvedValue := resolveStringBuilderConstructor(valueNode, content)
		if resolvedValue != "" && sqlutil.IsSQLStub(resolvedValue) {
			scope.Set(variableName, &sqlutil.SQLVariable{
				Fragments: []string{resolvedValue},
				Line:      int(declarationNode.StartPosition().Row) + 1,
			})
		}
		return
	}

	resolvedValue := resolveJavaExpression(valueNode, content)
	if resolvedValue != "" && sqlutil.IsSQLStub(resolvedValue) {
		scope.Set(variableName, &sqlutil.SQLVariable{
			Fragments: []string{resolvedValue},
			Line:      int(declarationNode.StartPosition().Row) + 1,
		})
	}
}

func processExpression(expressionNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment, result *model.ParseResult, callerName, filePath string) {
	switch expressionNode.Kind() {
	case "assignment_expression":
		processAssignment(expressionNode, content, scope)
	case "method_invocation":
		processMethodInvocation(expressionNode, content, scope, result, callerName, filePath)
	}
}

func processAssignment(assignmentNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment) {
	leftNode := assignmentNode.ChildByFieldName("left")
	rightNode := assignmentNode.ChildByFieldName("right")
	if leftNode == nil || rightNode == nil {
		return
	}

	variableName := leftNode.Utf8Text(content)

	operatorText := ""
	for index := uint(0); index < assignmentNode.ChildCount(); index++ {
		child := assignmentNode.Child(index)
		if child != nil && !child.IsNamed() {
			text := child.Utf8Text(content)
			if text == "+=" || text == "=" {
				operatorText = text
				break
			}
		}
	}

	resolvedValue := resolveJavaExpression(rightNode, content)
	if resolvedValue == "" {
		return
	}

	if operatorText == "+=" {
		existingVariable := scope.Lookup(variableName)
		if existingVariable != nil {
			existingVariable.Fragments = append(existingVariable.Fragments, resolvedValue)
		} else if sqlutil.IsSQLStub(resolvedValue) {
			scope.Set(variableName, &sqlutil.SQLVariable{
				Fragments: []string{resolvedValue},
				Line:      int(assignmentNode.StartPosition().Row) + 1,
			})
		}
	} else if operatorText == "=" {
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

func processMethodInvocation(invocationNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment, result *model.ParseResult, callerName, filePath string) {
	methodName := ""
	nameNode := invocationNode.ChildByFieldName("name")
	if nameNode != nil {
		methodName = nameNode.Utf8Text(content)
	}

	// StringBuilder.append()
	if methodName == "append" {
		baseVariableName := getBaseVariableName(invocationNode, content)
		if baseVariableName != "" {
			existingVariable := scope.Lookup(baseVariableName)
			if existingVariable != nil {
				argumentsNode := invocationNode.ChildByFieldName("arguments")
				if argumentsNode != nil && argumentsNode.NamedChildCount() > 0 {
					argumentValue := resolveJavaExpression(argumentsNode.NamedChild(0), content)
					if argumentValue != "" {
						existingVariable.Fragments = append(existingVariable.Fragments, argumentValue)
					}
				}
			}
		}
		return
	}

	// Check method arguments for direct SQL literals
	argumentsNode := invocationNode.ChildByFieldName("arguments")
	if argumentsNode == nil {
		return
	}
	for argumentIndex := uint(0); argumentIndex < argumentsNode.NamedChildCount(); argumentIndex++ {
		argumentNode := argumentsNode.NamedChild(argumentIndex)
		argumentValue := resolveJavaExpression(argumentNode, content)
		if argumentValue != "" && sqlutil.IsSQLStatement(argumentValue) {
			result.Queries = append(result.Queries, model.RawQuery{
				SQLText:    argumentValue,
				QueryType:  sqlutil.DetectQueryType(argumentValue),
				Tables:     sqlutil.ExtractTablesFromSQL(argumentValue),
				CallerName: callerName,
				FilePath:   filePath,
				Line:       int(argumentNode.StartPosition().Row) + 1,
			})
		}
	}
}

func processIfStatement(ifNode *tree_sitter.Node, content []byte, scope *sqlutil.ScopeEnvironment, parentCondition string, result *model.ParseResult, callerName, filePath string) {
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
		processBlock(consequenceNode, content, scope, fullCondition, result, callerName, filePath)
	}

	sqlutil.RecordConditionalDiff(scope, snapshotBefore, fullCondition, false)
	sqlutil.RestoreSnapshot(scope, snapshotBefore)

	alternativeNode := ifNode.ChildByFieldName("alternative")
	if alternativeNode != nil {
		snapshotBeforeElse := scope.Snapshot()
		if alternativeNode.Kind() == "if_statement" {
			processIfStatement(alternativeNode, content, scope, parentCondition, result, callerName, filePath)
		} else {
			processBlock(alternativeNode, content, scope, fullCondition, result, callerName, filePath)
		}
		sqlutil.RecordConditionalDiff(scope, snapshotBeforeElse, fullCondition, true)
		sqlutil.RestoreSnapshot(scope, snapshotBeforeElse)
	}
}

// resolveJavaExpression extracts a string literal value from an AST node.
// Returns empty string for non-literal expressions (method calls, identifiers) to prevent data pollution.
func resolveJavaExpression(node *tree_sitter.Node, content []byte) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case "string_literal":
		text := node.Utf8Text(content)
		return strings.Trim(text, "\"")
	case "string_fragment":
		return node.Utf8Text(content)
	case "text_block":
		text := node.Utf8Text(content)
		text = strings.TrimPrefix(text, `"""`)
		text = strings.TrimSuffix(text, `"""`)
		return strings.TrimSpace(text)
	case "binary_expression":
		leftNode := node.ChildByFieldName("left")
		rightNode := node.ChildByFieldName("right")
		operatorNode := node.ChildByFieldName("operator")
		if operatorNode != nil && operatorNode.Utf8Text(content) == "+" {
			leftValue := resolveJavaExpression(leftNode, content)
			rightValue := resolveJavaExpression(rightNode, content)
			if leftValue != "" || rightValue != "" {
				return leftValue + rightValue
			}
		}
		return ""
	case "parenthesized_expression":
		if node.NamedChildCount() > 0 {
			return resolveJavaExpression(node.NamedChild(0), content)
		}
		return ""
	default:
		return ""
	}
}

// resolveStringBuilderConstructor extracts the initial string from new StringBuilder("...").
func resolveStringBuilderConstructor(creationNode *tree_sitter.Node, content []byte) string {
	argumentsNode := creationNode.ChildByFieldName("arguments")
	if argumentsNode == nil || argumentsNode.NamedChildCount() == 0 {
		return ""
	}
	return resolveJavaExpression(argumentsNode.NamedChild(0), content)
}

// getBaseVariableName traverses a chained method invocation to find the root variable name.
// For sb.append("A").append("B"), returns "sb".
func getBaseVariableName(invocationNode *tree_sitter.Node, content []byte) string {
	objectNode := invocationNode.ChildByFieldName("object")
	if objectNode == nil {
		return ""
	}
	if objectNode.Kind() == "identifier" {
		return objectNode.Utf8Text(content)
	}
	if objectNode.Kind() == "method_invocation" {
		return getBaseVariableName(objectNode, content)
	}
	return ""
}
