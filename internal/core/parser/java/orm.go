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

// sqlVariable tracks a SQL string variable's accumulated fragments and metadata.
type sqlVariable struct {
	fragments  []string
	line       int
	conditions []model.ConditionalFragment
	baseSQL    string
}

// scopeEnvironment tracks SQL variables within a scope, supporting parent-child nesting.
type scopeEnvironment struct {
	variables map[string]*sqlVariable
	parent    *scopeEnvironment
}

func newScopeEnvironment(parent *scopeEnvironment) *scopeEnvironment {
	return &scopeEnvironment{
		variables: make(map[string]*sqlVariable),
		parent:    parent,
	}
}

func (scope *scopeEnvironment) lookup(name string) *sqlVariable {
	if variable, exists := scope.variables[name]; exists {
		return variable
	}
	if scope.parent != nil {
		return scope.parent.lookup(name)
	}
	return nil
}

func (scope *scopeEnvironment) set(name string, variable *sqlVariable) {
	current := scope
	for current != nil {
		if _, exists := current.variables[name]; exists {
			current.variables[name] = variable
			return
		}
		current = current.parent
	}
	scope.variables[name] = variable
}

func (scope *scopeEnvironment) snapshot() map[string][]string {
	result := make(map[string][]string)
	current := scope
	for current != nil {
		for name, variable := range current.variables {
			if _, exists := result[name]; !exists {
				fragmentsCopy := make([]string, len(variable.fragments))
				copy(fragmentsCopy, variable.fragments)
				result[name] = fragmentsCopy
			}
		}
		current = current.parent
	}
	return result
}

// ExtractSQLStringsJava extracts SQL strings from a Java method body using variable tracking.
func ExtractSQLStringsJava(bodyNode *tree_sitter.Node, content []byte, callerName, filePath string, result *model.ParseResult) {
	scope := newScopeEnvironment(nil)
	processBlock(bodyNode, content, scope, "", result, callerName, filePath)

	// Emit queries from all tracked variables
	emitQueriesFromScope(scope, callerName, filePath, result)
}

func processBlock(blockNode *tree_sitter.Node, content []byte, scope *scopeEnvironment, parentCondition string, result *model.ParseResult, callerName, filePath string) {
	for index := uint(0); index < blockNode.NamedChildCount(); index++ {
		statement := blockNode.NamedChild(index)
		processStatement(statement, content, scope, parentCondition, result, callerName, filePath)
	}
}

func processStatement(statement *tree_sitter.Node, content []byte, scope *scopeEnvironment, parentCondition string, result *model.ParseResult, callerName, filePath string) {
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

func processVariableDeclaration(declarationNode *tree_sitter.Node, content []byte, scope *scopeEnvironment) {
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
		if resolvedValue != "" && isSQLStub(resolvedValue) {
			scope.set(variableName, &sqlVariable{
				fragments: []string{resolvedValue},
				line:      int(declarationNode.StartPosition().Row) + 1,
			})
		}
		return
	}

	resolvedValue := resolveJavaExpression(valueNode, content)
	if resolvedValue != "" && isSQLStub(resolvedValue) {
		scope.set(variableName, &sqlVariable{
			fragments: []string{resolvedValue},
			line:      int(declarationNode.StartPosition().Row) + 1,
		})
	}
}

func processExpression(expressionNode *tree_sitter.Node, content []byte, scope *scopeEnvironment, result *model.ParseResult, callerName, filePath string) {
	switch expressionNode.Kind() {
	case "assignment_expression":
		processAssignment(expressionNode, content, scope)
	case "method_invocation":
		processMethodInvocation(expressionNode, content, scope, result, callerName, filePath)
	}
}

func processAssignment(assignmentNode *tree_sitter.Node, content []byte, scope *scopeEnvironment) {
	leftNode := assignmentNode.ChildByFieldName("left")
	rightNode := assignmentNode.ChildByFieldName("right")
	if leftNode == nil || rightNode == nil {
		return
	}

	variableName := leftNode.Utf8Text(content)

	// Determine operator by checking the raw text between left and right
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
		existingVariable := scope.lookup(variableName)
		if existingVariable != nil {
			existingVariable.fragments = append(existingVariable.fragments, resolvedValue)
		} else if isSQLStub(resolvedValue) {
			scope.set(variableName, &sqlVariable{
				fragments: []string{resolvedValue},
				line:      int(assignmentNode.StartPosition().Row) + 1,
			})
		}
	} else if operatorText == "=" {
		existingVariable := scope.lookup(variableName)
		if existingVariable != nil {
			existingVariable.fragments = []string{resolvedValue}
		} else if isSQLStub(resolvedValue) {
			scope.set(variableName, &sqlVariable{
				fragments: []string{resolvedValue},
				line:      int(assignmentNode.StartPosition().Row) + 1,
			})
		}
	}
}

func processMethodInvocation(invocationNode *tree_sitter.Node, content []byte, scope *scopeEnvironment, result *model.ParseResult, callerName, filePath string) {
	methodName := ""
	nameNode := invocationNode.ChildByFieldName("name")
	if nameNode != nil {
		methodName = nameNode.Utf8Text(content)
	}

	// StringBuilder.append()
	if methodName == "append" {
		baseVariableName := getBaseVariableName(invocationNode, content)
		if baseVariableName != "" {
			existingVariable := scope.lookup(baseVariableName)
			if existingVariable != nil {
				argumentsNode := invocationNode.ChildByFieldName("arguments")
				if argumentsNode != nil && argumentsNode.NamedChildCount() > 0 {
					argumentValue := resolveJavaExpression(argumentsNode.NamedChild(0), content)
					if argumentValue != "" {
						existingVariable.fragments = append(existingVariable.fragments, argumentValue)
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

func processIfStatement(ifNode *tree_sitter.Node, content []byte, scope *scopeEnvironment, parentCondition string, result *model.ParseResult, callerName, filePath string) {
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

	// Snapshot before then-branch
	snapshotBefore := scope.snapshot()

	// Process then-branch (consequence)
	consequenceNode := ifNode.ChildByFieldName("consequence")
	if consequenceNode != nil {
		processBlock(consequenceNode, content, scope, fullCondition, result, callerName, filePath)
	}

	// Compare snapshot to find fragments added in then-branch
	recordConditionalDiff(scope, snapshotBefore, fullCondition, false)

	// Restore to pre-then state for else-branch
	restoreSnapshot(scope, snapshotBefore)

	// Process else-branch (alternative)
	alternativeNode := ifNode.ChildByFieldName("alternative")
	if alternativeNode != nil {
		snapshotBeforeElse := scope.snapshot()
		if alternativeNode.Kind() == "if_statement" {
			processIfStatement(alternativeNode, content, scope, parentCondition, result, callerName, filePath)
		} else {
			processBlock(alternativeNode, content, scope, fullCondition, result, callerName, filePath)
		}
		recordConditionalDiff(scope, snapshotBeforeElse, fullCondition, true)
		restoreSnapshot(scope, snapshotBeforeElse)
	}
}

func recordConditionalDiff(scope *scopeEnvironment, snapshotBefore map[string][]string, condition string, isElse bool) {
	current := scope
	for current != nil {
		for variableName, variable := range current.variables {
			beforeFragments, existed := snapshotBefore[variableName]
			if !existed {
				continue
			}
			if len(variable.fragments) > len(beforeFragments) {
				addedFragments := variable.fragments[len(beforeFragments):]
				for _, fragment := range addedFragments {
					variable.conditions = append(variable.conditions, model.ConditionalFragment{
						Condition: condition,
						Fragment:  fragment,
						IsElse:   isElse,
					})
				}
				if variable.baseSQL == "" {
					variable.baseSQL = strings.Join(beforeFragments, "")
				}
			}
		}
		current = current.parent
	}
}

func restoreSnapshot(scope *scopeEnvironment, snapshotBefore map[string][]string) {
	current := scope
	for current != nil {
		for variableName, variable := range current.variables {
			if beforeFragments, existed := snapshotBefore[variableName]; existed {
				restoredFragments := make([]string, len(beforeFragments))
				copy(restoredFragments, beforeFragments)
				variable.fragments = restoredFragments
			}
		}
		current = current.parent
	}
}

func emitQueriesFromScope(scope *scopeEnvironment, callerName, filePath string, result *model.ParseResult) {
	current := scope
	for current != nil {
		for _, variable := range current.variables {
			fullSQL := strings.Join(variable.fragments, "")
			if len(variable.conditions) > 0 {
				for _, condition := range variable.conditions {
					fullSQL += condition.Fragment
				}
			}
			if !sqlutil.IsSQLStatement(fullSQL) {
				continue
			}
			query := model.RawQuery{
				SQLText:    fullSQL,
				QueryType:  sqlutil.DetectQueryType(fullSQL),
				Tables:     sqlutil.ExtractTablesFromSQL(fullSQL),
				CallerName: callerName,
				FilePath:   filePath,
				Line:       variable.line,
			}
			if len(variable.conditions) > 0 {
				query.BaseSQL = variable.baseSQL
				query.Conditions = variable.conditions
			}
			result.Queries = append(result.Queries, query)
		}
		current = current.parent
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

// isSQLStub checks if a string fragment contains SQL keywords, used as a loose filter
// to decide whether to start tracking a variable.
func isSQLStub(text string) bool {
	upper := strings.ToUpper(text)
	keywords := []string{"SELECT", "INSERT", "UPDATE", "DELETE", "CREATE", "ALTER", "DROP", "WITH", "FROM", "WHERE", "JOIN", "SET", "INTO", "VALUES"}
	for _, keyword := range keywords {
		if strings.Contains(upper, keyword) {
			return true
		}
	}
	return false
}
