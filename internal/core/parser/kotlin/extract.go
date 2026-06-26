package kotlin

import (
	"fmt"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

// Extract extracts symbols, calls, type hints, and heritage from a Kotlin AST.
func Extract(rootNode *tree_sitter.Node, content []byte, file scanner.ScannedFile, result *model.ParseResult) {
	packageName := ""
	var classStack []string
	lambdaCounter := 0

	astutil.WalkNamedChildren(rootNode, func(node *tree_sitter.Node) bool {
		switch node.Kind() {
		case "package_header":
			packageName = extractPackageName(node, content)
			return false

		case "import_list":
			extractImports(node, content, result)
			return false

		case "import":
			extractSingleImport(node, content, result)
			return false

		case "class_declaration":
			extractClassDeclaration(node, content, file.RelPath, packageName, classStack, &lambdaCounter, result)
			return false

		case "object_declaration":
			extractObjectDeclaration(node, content, file.RelPath, packageName, classStack, &lambdaCounter, result)
			return false

		case "function_declaration":
			extractFunctionDeclaration(node, content, file.RelPath, packageName, classStack, &lambdaCounter, result)
			return false

		case "property_declaration":
			extractPropertyDeclaration(node, content, file.RelPath, packageName, classStack, &lambdaCounter, result)
			return false
		}
		return true
	})

}

// extractPackageName extracts the package name from a package_header node.
func extractPackageName(node *tree_sitter.Node, content []byte) string {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "qualified_identifier" {
			return child.Utf8Text(content)
		}
	}
	return ""
}

// extractImports extracts all import declarations from an import_list node.
func extractImports(node *tree_sitter.Node, content []byte, result *model.ParseResult) {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "import_header" || child.Kind() == "import" {
			extractSingleImport(child, content, result)
		}
	}
}

// extractSingleImport extracts one import declaration.
func extractSingleImport(node *tree_sitter.Node, content []byte, result *model.ParseResult) {
	importPath := ""
	isWildcard := false
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Kind() {
		case "identifier", "qualified_identifier":
			importPath = child.Utf8Text(content)
		case "*":
			isWildcard = true
		}
	}
	if importPath == "" {
		return
	}
	symbolName := importPath
	if dotIndex := strings.LastIndex(importPath, "."); dotIndex >= 0 {
		symbolName = importPath[dotIndex+1:]
	}
	if isWildcard {
		symbolName = "*"
	}
	result.Imports = append(result.Imports, model.RawImport{
		ModulePath: importPath,
		SymbolName: symbolName,
	})
}

// buildQualifiedName constructs a qualified name from package, class stack, and name.
func buildQualifiedName(packageName string, classStack []string, name string) string {
	parts := make([]string, 0, 3)
	if packageName != "" {
		parts = append(parts, packageName)
	}
	parts = append(parts, classStack...)
	if name != "" {
		parts = append(parts, name)
	}
	return strings.Join(parts, ".")
}

// modifierInfo holds all extracted modifier information from a modifiers AST node.
type modifierInfo struct {
	Annotations   []model.StructuredAnnotation
	ClassModifier string
	IsSuspend     bool
	IsOverride    bool
	IsInner       bool
}

// extractModifierInfo extracts annotations and modifier flags from a modifiers node.
func extractModifierInfo(node *tree_sitter.Node, content []byte) modifierInfo {
	var info modifierInfo
	if node == nil {
		return info
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Kind() {
		case "annotation":
			annotation := extractSingleAnnotation(child, content)
			if annotation.Name != "" {
				info.Annotations = append(info.Annotations, annotation)
			}
		case "class_modifier":
			if child.ChildCount() > 0 {
				info.ClassModifier = child.Child(0).Kind()
			}
		case "inheritance_modifier":
			if child.ChildCount() > 0 {
				modifier := child.Child(0).Kind()
				if modifier == "sealed" || modifier == "abstract" || modifier == "open" {
					info.ClassModifier = modifier
				}
			}
		case "platform_modifier", "function_modifier":
			if child.ChildCount() > 0 && child.Child(0).Kind() == "suspend" {
				info.IsSuspend = true
			}
		case "member_modifier":
			if child.ChildCount() > 0 {
				switch child.Child(0).Kind() {
				case "override":
					info.IsOverride = true
				case "inner":
					info.IsInner = true
				}
			}
		}
	}
	return info
}

// extractSingleAnnotation extracts one annotation's name and parameters.
func extractSingleAnnotation(node *tree_sitter.Node, content []byte) model.StructuredAnnotation {
	annotation := model.StructuredAnnotation{
		Params: make(map[string]string),
		Line:   int(node.StartPosition().Row) + 1,
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Kind() {
		case "user_type":
			annotation.Name = extractUserTypeName(child, content)
		case "constructor_invocation":
			for j := uint(0); j < child.ChildCount(); j++ {
				grandchild := child.Child(j)
				switch grandchild.Kind() {
				case "user_type":
					annotation.Name = extractUserTypeName(grandchild, content)
				case "value_arguments":
					annotation.Params = extractAnnotationParams(grandchild, content)
				}
			}
		case "value_arguments":
			annotation.Params = extractAnnotationParams(child, content)
		}
	}
	return annotation
}

// extractAnnotationParams extracts annotation parameters as structured key-value pairs from AST.
func extractAnnotationParams(node *tree_sitter.Node, content []byte) map[string]string {
	params := make(map[string]string)

	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() != "value_argument" {
			continue
		}

		paramName := ""
		paramValue := ""

		for j := uint(0); j < child.ChildCount(); j++ {
			argumentChild := child.Child(j)
			switch argumentChild.Kind() {
			case "identifier":
				if paramName == "" {
					paramName = argumentChild.Utf8Text(content)
				}
			case "string_literal":
				paramValue = extractStringContent(argumentChild, content)
			case "multiline_string_literal":
				paramValue = extractMultilineStringContent(argumentChild, content)
			case "collection_literal", "navigation_expression", "call_expression":
				paramValue = argumentChild.Utf8Text(content)
			}
		}

		if paramName == "" {
			paramName = "value"
		}
		if paramValue != "" {
			params[paramName] = paramValue
		}
	}

	return params
}

// extractStringContent extracts the text content from a string_literal node.
func extractStringContent(node *tree_sitter.Node, content []byte) string {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "string_content" {
			return child.Utf8Text(content)
		}
	}
	// Fallback: strip quotes from full text
	text := node.Utf8Text(content)
	text = strings.Trim(text, "\"")
	return text
}

// extractMultilineStringContent extracts and normalizes text from a multiline_string_literal node.
func extractMultilineStringContent(node *tree_sitter.Node, content []byte) string {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "string_content" {
			rawText := child.Utf8Text(content)
			lines := strings.Split(rawText, "\n")
			var trimmedLines []string
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				if trimmed != "" {
					trimmedLines = append(trimmedLines, trimmed)
				}
			}
			return strings.Join(trimmedLines, " ")
		}
	}
	return ""
}

// extractUserTypeName extracts the simple name from a user_type node.
func extractUserTypeName(node *tree_sitter.Node, content []byte) string {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "identifier" {
			return child.Utf8Text(content)
		}
	}
	return node.Utf8Text(content)
}

// extractClassDeclaration handles class_declaration (class, interface, enum, sealed, data, etc.)
func extractClassDeclaration(node *tree_sitter.Node, content []byte, filePath string, packageName string, classStack []string, lambdaCounter *int, result *model.ParseResult) {
	var modifiersNode *tree_sitter.Node
	var className string
	isInterface := false
	var delegationSpecifiers *tree_sitter.Node
	var classBody *tree_sitter.Node
	var primaryConstructor *tree_sitter.Node
	var typeParams string

	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Kind() {
		case "modifiers":
			modifiersNode = child
		case "class":
			// it's a class (not interface)
		case "interface":
			isInterface = true
		case "identifier":
			if className == "" {
				className = child.Utf8Text(content)
			}
		case "type_parameters":
			typeParams = child.Utf8Text(content)
		case "primary_constructor":
			primaryConstructor = child
		case "delegation_specifiers":
			delegationSpecifiers = child
		case "class_body", "enum_class_body":
			classBody = child
		}
	}

	if className == "" {
		return
	}

	modifiers := extractModifierInfo(modifiersNode, content)

	// Determine class_type
	classType := "class"
	if isInterface {
		classType = "interface"
	}
	if modifiers.ClassModifier != "" {
		classType = modifiers.ClassModifier
	}

	kind := constants.KindClass
	if isInterface {
		kind = constants.KindInterface
	}

	qualifiedName := buildQualifiedName(packageName, classStack, className)
	symbolID := astutil.GenerateSymbolID(filePath, qualifiedName, int(node.StartPosition().Row)+1)

	symbol := model.Symbol{
		ID:            symbolID,
		Name:          className,
		QualifiedName: qualifiedName,
		Kind:          kind,
		FilePath:      filePath,
		StartLine:     int(node.StartPosition().Row) + 1,
		EndLine:       int(node.EndPosition().Row) + 1,
		ClassType:     classType,
		TypeParams:    parseTypeParams(typeParams),
		Annotations:   modifiers.Annotations,
	}
	result.Symbols = append(result.Symbols, symbol)

	// Extract heritage from delegation_specifiers
	if delegationSpecifiers != nil {
		extractHeritage(delegationSpecifiers, content, className, qualifiedName, kind, filePath, result)
	}

	// Extract primary constructor fields and generate <init> symbol
	if primaryConstructor != nil {
		extractPrimaryConstructor(primaryConstructor, content, filePath, packageName, classStack, className, result)
	}

	// Recurse into class body
	newClassStack := append(append([]string{}, classStack...), className)
	if classBody != nil {
		extractClassBody(classBody, content, filePath, packageName, newClassStack, lambdaCounter, result)
	}
}

// extractObjectDeclaration handles object_declaration (singleton).
func extractObjectDeclaration(node *tree_sitter.Node, content []byte, filePath string, packageName string, classStack []string, lambdaCounter *int, result *model.ParseResult) {
	var objectName string
	var classBody *tree_sitter.Node
	var delegationSpecifiers *tree_sitter.Node

	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Kind() {
		case "identifier":
			objectName = child.Utf8Text(content)
		case "class_body":
			classBody = child
		case "delegation_specifiers":
			delegationSpecifiers = child
		}
	}

	if objectName == "" {
		return
	}

	qualifiedName := buildQualifiedName(packageName, classStack, objectName)
	symbolID := astutil.GenerateSymbolID(filePath, qualifiedName, int(node.StartPosition().Row)+1)

	symbol := model.Symbol{
		ID:            symbolID,
		Name:          objectName,
		QualifiedName: qualifiedName,
		Kind:          constants.KindClass,
		FilePath:      filePath,
		StartLine:     int(node.StartPosition().Row) + 1,
		EndLine:       int(node.EndPosition().Row) + 1,
		ClassType:     "object",
	}
	result.Symbols = append(result.Symbols, symbol)

	if delegationSpecifiers != nil {
		extractHeritage(delegationSpecifiers, content, objectName, qualifiedName, constants.KindClass, filePath, result)
	}

	newClassStack := append(append([]string{}, classStack...), objectName)
	if classBody != nil {
		extractClassBody(classBody, content, filePath, packageName, newClassStack, lambdaCounter, result)
	}
}

// extractClassBody recurses into class body members.
func extractClassBody(node *tree_sitter.Node, content []byte, filePath string, packageName string, classStack []string, lambdaCounter *int, result *model.ParseResult) {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Kind() {
		case "class_declaration":
			extractClassDeclaration(child, content, filePath, packageName, classStack, lambdaCounter, result)
		case "object_declaration":
			extractObjectDeclaration(child, content, filePath, packageName, classStack, lambdaCounter, result)
		case "companion_object":
			extractCompanionObject(child, content, filePath, packageName, classStack, lambdaCounter, result)
		case "function_declaration":
			extractFunctionDeclaration(child, content, filePath, packageName, classStack, lambdaCounter, result)
		case "secondary_constructor":
			extractSecondaryConstructor(child, content, filePath, packageName, classStack, lambdaCounter, result)
		case "property_declaration":
			extractPropertyDeclaration(child, content, filePath, packageName, classStack, lambdaCounter, result)
		case "enum_entry":
			extractEnumEntry(child, content, filePath, packageName, classStack, result)
		}
	}
}

// extractCompanionObject handles companion object declarations.
func extractCompanionObject(node *tree_sitter.Node, content []byte, filePath string, packageName string, classStack []string, lambdaCounter *int, result *model.ParseResult) {
	companionName := "Companion"
	var classBody *tree_sitter.Node

	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Kind() {
		case "identifier":
			companionName = child.Utf8Text(content)
		case "class_body":
			classBody = child
		}
	}

	qualifiedName := buildQualifiedName(packageName, classStack, companionName)
	symbolID := astutil.GenerateSymbolID(filePath, qualifiedName, int(node.StartPosition().Row)+1)

	symbol := model.Symbol{
		ID:            symbolID,
		Name:          companionName,
		QualifiedName: qualifiedName,
		Kind:          constants.KindClass,
		FilePath:      filePath,
		StartLine:     int(node.StartPosition().Row) + 1,
		EndLine:       int(node.EndPosition().Row) + 1,
		ClassType:     "object",
	}
	result.Symbols = append(result.Symbols, symbol)

	newClassStack := append(append([]string{}, classStack...), companionName)
	if classBody != nil {
		extractClassBody(classBody, content, filePath, packageName, newClassStack, lambdaCounter, result)
	}
}

// extractEnumEntry extracts enum constants as Field symbols.
func extractEnumEntry(node *tree_sitter.Node, content []byte, filePath string, packageName string, classStack []string, result *model.ParseResult) {
	var entryName string
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "identifier" {
			entryName = child.Utf8Text(content)
			break
		}
	}
	if entryName == "" {
		return
	}

	qualifiedName := buildQualifiedName(packageName, classStack, entryName)
	symbolID := astutil.GenerateSymbolID(filePath, qualifiedName, int(node.StartPosition().Row)+1)

	result.Symbols = append(result.Symbols, model.Symbol{
		ID:            symbolID,
		Name:          entryName,
		QualifiedName: qualifiedName,
		Kind:          constants.KindVariable,
		FilePath:      filePath,
		StartLine:     int(node.StartPosition().Row) + 1,
		EndLine:       int(node.EndPosition().Row) + 1,
		IsStatic:      true,
	})
}

// extractFunctionDeclaration handles function_declaration (member, top-level, extension).
func extractFunctionDeclaration(node *tree_sitter.Node, content []byte, filePath string, packageName string, classStack []string, lambdaCounter *int, result *model.ParseResult) {
	var modifiersNode *tree_sitter.Node
	var functionName string
	var params []model.ParamInfo
	var returnTypes []model.ReturnType
	var functionBody *tree_sitter.Node
	var typeParams string
	foundFun := false
	foundDot := false

	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Kind() {
		case "modifiers":
			modifiersNode = child
		case "fun":
			foundFun = true
		case "type_parameters":
			typeParams = child.Utf8Text(content)
		case "user_type":
			// user_type before the dot = receiver type (extension function) — skip for now
			if foundFun && !foundDot && functionName == "" {
				// Extension function receiver, will be handled in resolver iteration
			}
			// user_type after ) and : = return type
			if functionName != "" && params != nil {
				returnTypes = append(returnTypes, model.ReturnType{Name: child.Utf8Text(content)})
			}
		case "nullable_type":
			if functionName != "" && params != nil {
				returnTypes = append(returnTypes, model.ReturnType{Name: child.Utf8Text(content)})
			}
		case ".":
			foundDot = true
		case "identifier":
			if foundFun && functionName == "" {
				// If we already found a receiver type, this is the function name after .
				// If not, this is the function name directly
				functionName = child.Utf8Text(content)
			}
		case "function_value_parameters":
			params = extractParameters(child, content)
		case "function_body":
			functionBody = child
		}
	}

	if functionName == "" {
		return
	}

	modifiers := extractModifierInfo(modifiersNode, content)

	qualifiedName := buildQualifiedName(packageName, classStack, functionName)
	symbolID := astutil.GenerateSymbolID(filePath, qualifiedName, int(node.StartPosition().Row)+1)

	symbol := model.Symbol{
		ID:            symbolID,
		Name:          functionName,
		QualifiedName: qualifiedName,
		Kind:          constants.KindFunction,
		FilePath:      filePath,
		StartLine:     int(node.StartPosition().Row) + 1,
		EndLine:       int(node.EndPosition().Row) + 1,
		Params:        params,
		ReturnTypes:   returnTypes,
		TypeParams:    parseTypeParams(typeParams),
		Annotations:   modifiers.Annotations,
		IsAsync:       modifiers.IsSuspend,
	}
	result.Symbols = append(result.Symbols, symbol)

	// Extract Retrofit routes from annotations
	currentClassName := ""
	if len(classStack) > 0 {
		currentClassName = strings.Join(classStack, ".")
	}
	if len(modifiers.Annotations) > 0 {
		ExtractRetrofitRoutes(modifiers.Annotations, functionName, currentClassName, filePath, int(node.StartPosition().Row)+1, result)
		ExtractRoomQueries(modifiers.Annotations, functionName, currentClassName, filePath, int(node.StartPosition().Row)+1, result)
	}

	// Generate type hints for parameters
	scope := qualifiedName
	for _, param := range params {
		if param.Type != "" {
			result.TypeHints = append(result.TypeHints, model.TypeBinding{
				VarName:  param.Name,
				TypeName: param.Type,
				Tier:     0,
				Scope:    scope,
				FilePath: filePath,
			})
		}
	}

	// Extract calls from function body
	if functionBody != nil {
		callerName := qualifiedName
		extractCalls(functionBody, content, filePath, callerName, constants.KindFunction, classStack, packageName, lambdaCounter, result)
	}
}

// extractSecondaryConstructor handles secondary_constructor nodes.
func extractSecondaryConstructor(node *tree_sitter.Node, content []byte, filePath string, packageName string, classStack []string, lambdaCounter *int, result *model.ParseResult) {
	var params []model.ParamInfo
	var body *tree_sitter.Node

	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Kind() {
		case "function_value_parameters":
			params = extractParameters(child, content)
		case "block":
			body = child
		}
	}

	className := ""
	if len(classStack) > 0 {
		className = classStack[len(classStack)-1]
	}
	_ = className

	qualifiedName := buildQualifiedName(packageName, classStack, "<init>")
	symbolID := astutil.GenerateSymbolID(filePath, qualifiedName, int(node.StartPosition().Row)+1)

	result.Symbols = append(result.Symbols, model.Symbol{
		ID:            symbolID,
		Name:          "<init>",
		QualifiedName: qualifiedName,
		Kind:          constants.KindFunction,
		FilePath:      filePath,
		StartLine:     int(node.StartPosition().Row) + 1,
		EndLine:       int(node.EndPosition().Row) + 1,
		Params:        params,
		IsConstructor: true,
	})

	if body != nil {
		extractCalls(body, content, filePath, qualifiedName, constants.KindFunction, classStack, packageName, lambdaCounter, result)
	}
}

// extractPrimaryConstructor extracts the primary constructor and its val/var parameters as fields.
func extractPrimaryConstructor(node *tree_sitter.Node, content []byte, filePath string, packageName string, classStack []string, className string, result *model.ParseResult) {
	var params []model.ParamInfo
	var constructorAnnotations []model.StructuredAnnotation

	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Kind() {
		case "modifiers":
			constructorModifiers := extractModifierInfo(child, content)
			constructorAnnotations = constructorModifiers.Annotations
		case "class_parameters":
			params = extractClassParameters(child, content, filePath, packageName, classStack, className, result)
		}
	}

	newClassStack := append(append([]string{}, classStack...), className)
	qualifiedName := buildQualifiedName(packageName, newClassStack, "<init>")
	symbolID := astutil.GenerateSymbolID(filePath, qualifiedName, int(node.StartPosition().Row)+1)

	result.Symbols = append(result.Symbols, model.Symbol{
		ID:            symbolID,
		Name:          "<init>",
		QualifiedName: qualifiedName,
		Kind:          constants.KindFunction,
		FilePath:      filePath,
		StartLine:     int(node.StartPosition().Row) + 1,
		EndLine:       int(node.EndPosition().Row) + 1,
		Params:        params,
		IsConstructor: true,
		Annotations:   constructorAnnotations,
	})
}

// extractClassParameters extracts parameters from primary constructor, generating Field symbols for val/var.
func extractClassParameters(node *tree_sitter.Node, content []byte, filePath string, packageName string, classStack []string, className string, result *model.ParseResult) []model.ParamInfo {
	var params []model.ParamInfo
	newClassStack := append(append([]string{}, classStack...), className)

	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() != "class_parameter" {
			continue
		}

		var paramName string
		var paramType string
		isField := false
		var fieldAnnotations []model.StructuredAnnotation

		for j := uint(0); j < child.ChildCount(); j++ {
			grandchild := child.Child(j)
			switch grandchild.Kind() {
			case "val", "var":
				isField = true
			case "identifier":
				if paramName == "" {
					paramName = grandchild.Utf8Text(content)
				}
			case "user_type", "nullable_type":
				paramType = grandchild.Utf8Text(content)
			case "modifiers":
				fieldModifiers := extractModifierInfo(grandchild, content)
				fieldAnnotations = fieldModifiers.Annotations
			}
		}

		params = append(params, model.ParamInfo{Name: paramName, Type: paramType})

		// Generate Field symbol for val/var parameters
		if isField && paramName != "" {
			fieldQualifiedName := buildQualifiedName(packageName, newClassStack, paramName)
			fieldSymbolID := astutil.GenerateSymbolID(filePath, fieldQualifiedName, int(child.StartPosition().Row)+1)
			result.Symbols = append(result.Symbols, model.Symbol{
				ID:            fieldSymbolID,
				Name:          paramName,
				QualifiedName: fieldQualifiedName,
				Kind:          constants.KindVariable,
				FilePath:      filePath,
				StartLine:     int(child.StartPosition().Row) + 1,
				EndLine:       int(child.EndPosition().Row) + 1,
				Annotations:   fieldAnnotations,
			})
			if paramType != "" {
				result.TypeHints = append(result.TypeHints, model.TypeBinding{
					VarName:  paramName,
					TypeName: paramType,
					Tier:     0,
					Scope:    buildQualifiedName(packageName, newClassStack, ""),
					FilePath: filePath,
				})
			}
		}
	}
	return params
}

// extractPropertyDeclaration handles property_declaration (val/var fields).
func extractPropertyDeclaration(node *tree_sitter.Node, content []byte, filePath string, packageName string, classStack []string, lambdaCounter *int, result *model.ParseResult) {
	var propertyName string
	var propertyType string
	var modifiersNode *tree_sitter.Node
	var hasDelegate bool
	var initExpression *tree_sitter.Node
	var getterNode *tree_sitter.Node
	var setterNode *tree_sitter.Node

	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Kind() {
		case "modifiers":
			modifiersNode = child
		case "variable_declaration":
			for j := uint(0); j < child.ChildCount(); j++ {
				varChild := child.Child(j)
				switch varChild.Kind() {
				case "identifier":
					propertyName = varChild.Utf8Text(content)
				case "user_type", "nullable_type":
					propertyType = varChild.Utf8Text(content)
				}
			}
		case "property_delegate":
			hasDelegate = true
			// Extract calls inside delegate (e.g. lazy { createDB() })
			callerName := buildQualifiedName(packageName, classStack, "")
			propertyCallerKind := constants.KindClass
			if propertyName != "" {
				callerName = buildQualifiedName(packageName, classStack, propertyName)
				propertyCallerKind = constants.KindVariable
			}
			if len(classStack) == 0 {
				propertyCallerKind = constants.KindVariable
			}
			extractCalls(child, content, filePath, callerName, propertyCallerKind, classStack, packageName, lambdaCounter, result)
		case "call_expression", "navigation_expression", "identifier":
			// Initialization expression — could be a call
			if propertyName != "" {
				initExpression = child
			}
		case "getter":
			getterNode = child
		case "setter":
			setterNode = child
		}
	}

	if propertyName == "" {
		return
	}

	modifiers := extractModifierInfo(modifiersNode, content)

	qualifiedName := buildQualifiedName(packageName, classStack, propertyName)
	symbolID := astutil.GenerateSymbolID(filePath, qualifiedName, int(node.StartPosition().Row)+1)

	result.Symbols = append(result.Symbols, model.Symbol{
		ID:            symbolID,
		Name:          propertyName,
		QualifiedName: qualifiedName,
		Kind:          constants.KindVariable,
		FilePath:      filePath,
		StartLine:     int(node.StartPosition().Row) + 1,
		EndLine:       int(node.EndPosition().Row) + 1,
		Annotations:   modifiers.Annotations,
	})

	// Type hint
	if propertyType != "" {
		scope := buildQualifiedName(packageName, classStack, "")
		result.TypeHints = append(result.TypeHints, model.TypeBinding{
			VarName:  propertyName,
			TypeName: propertyType,
			Tier:     0,
			Scope:    scope,
			FilePath: filePath,
		})
	}

	// Extract calls from init expression
	if initExpression != nil && !hasDelegate {
		callerName := buildQualifiedName(packageName, classStack, "")
		if len(classStack) == 0 {
			callerName = buildQualifiedName(packageName, classStack, propertyName)
		}
		propertyCallerKind := constants.KindClass
		if len(classStack) == 0 {
			propertyCallerKind = constants.KindVariable
		}
		extractCalls(initExpression, content, filePath, callerName, propertyCallerKind, classStack, packageName, lambdaCounter, result)
	}

	// Extract calls from custom getter/setter
	if getterNode != nil {
		callerName := qualifiedName
		extractCalls(getterNode, content, filePath, callerName, constants.KindVariable, classStack, packageName, lambdaCounter, result)
	}
	if setterNode != nil {
		callerName := qualifiedName
		extractCalls(setterNode, content, filePath, callerName, constants.KindVariable, classStack, packageName, lambdaCounter, result)
	}
}

// extractParameters extracts function parameters from function_value_parameters.
func extractParameters(node *tree_sitter.Node, content []byte) []model.ParamInfo {
	var params []model.ParamInfo
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() != "parameter" {
			continue
		}
		var paramName, paramType string
		for j := uint(0); j < child.ChildCount(); j++ {
			grandchild := child.Child(j)
			switch grandchild.Kind() {
			case "identifier":
				if paramName == "" {
					paramName = grandchild.Utf8Text(content)
				}
			case "user_type", "nullable_type", "function_type":
				paramType = grandchild.Utf8Text(content)
			}
		}
		if paramName != "" {
			params = append(params, model.ParamInfo{Name: paramName, Type: paramType})
		}
	}
	return params
}

// extractHeritage extracts EXTENDS/IMPLEMENTS from delegation_specifiers.
func extractHeritage(node *tree_sitter.Node, content []byte, className string, childQualifiedName string, childKind string, filePath string, result *model.ParseResult) {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() != "delegation_specifier" {
			continue
		}
		for j := uint(0); j < child.ChildCount(); j++ {
			specifier := child.Child(j)
			switch specifier.Kind() {
			case "constructor_invocation":
				parentName := ""
				for k := uint(0); k < specifier.ChildCount(); k++ {
					if specifier.Child(k).Kind() == "user_type" {
						parentName = extractUserTypeName(specifier.Child(k), content)
						break
					}
				}
				if parentName != "" {
					result.Heritage = append(result.Heritage, model.RawHeritage{
						ChildName:      className,
						ChildQualified: childQualifiedName,
						ParentName:     parentName,
						Kind:           "extends",
						FilePath:       filePath,
						Language:       "kotlin",
					})
				}
			case "user_type":
				parentName := extractUserTypeName(specifier, content)
				if parentName != "" {
					heritageKind := "implements"
					if childKind == constants.KindInterface {
						heritageKind = "extends"
					}
					result.Heritage = append(result.Heritage, model.RawHeritage{
						ChildName:      className,
						ChildQualified: childQualifiedName,
						ParentName:     parentName,
						Kind:           heritageKind,
						FilePath:       filePath,
						Language:       "kotlin",
					})
				}
			}
		}
	}
}

// parseTypeParams extracts type parameter names from a type_parameters text like "<T, R>".
func parseTypeParams(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.TrimPrefix(text, "<")
	text = strings.TrimSuffix(text, ">")
	parts := strings.Split(text, ",")
	var result []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		// Remove variance modifiers and bounds: "out T : Comparable<T>" → "T"
		part = strings.TrimPrefix(part, "out ")
		part = strings.TrimPrefix(part, "in ")
		if colonIndex := strings.Index(part, " :"); colonIndex >= 0 {
			part = part[:colonIndex]
		}
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

// extractCalls recursively extracts call expressions from an AST subtree.
func extractCalls(node *tree_sitter.Node, content []byte, filePath string, callerName string, callerKind string, classStack []string, packageName string, lambdaCounter *int, result *model.ParseResult) {
	astutil.WalkNamedChildren(node, func(child *tree_sitter.Node) bool {
		switch child.Kind() {
		case "call_expression":
			extractSingleCall(child, content, filePath, callerName, callerKind, classStack, packageName, lambdaCounter, result)
			return false // don't recurse into call_expression children (handled inside)
		case "navigation_expression":
			// A navigation_expression without call (e.g. user?.name) — skip
			return true
		}
		return true
	})
}

// extractSingleCall extracts one call_expression into a RawCall + handles trailing lambda.
func extractSingleCall(node *tree_sitter.Node, content []byte, filePath string, callerName string, callerKind string, classStack []string, packageName string, lambdaCounter *int, result *model.ParseResult) {
	var receiverExpression string
	var calledName string
	var argumentCount int
	var lambdaNode *tree_sitter.Node
	var navigationNode *tree_sitter.Node
	var innerCallNode *tree_sitter.Node

	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Kind() {
		case "navigation_expression":
			navigationNode = child
			receiverExpression, calledName = extractNavigationExpression(child, content)
		case "call_expression":
			// Nested call_expression: withContext(args) { } → outer has call_expression + annotated_lambda
			innerCallNode = child
		case "identifier":
			if calledName == "" {
				calledName = child.Utf8Text(content)
			}
		case "value_arguments":
			argumentCount = countArguments(child)
			extractLambdasInArguments(child, content, filePath, callerName, classStack, packageName, lambdaCounter, result)
		case "annotated_lambda":
			lambdaNode = child
		}
	}

	// If calledName is empty but we have an inner call_expression, extract from it
	if calledName == "" && innerCallNode != nil {
		for i := uint(0); i < innerCallNode.ChildCount(); i++ {
			child := innerCallNode.Child(i)
			switch child.Kind() {
			case "navigation_expression":
				navigationNode = child
				receiverExpression, calledName = extractNavigationExpression(child, content)
			case "identifier":
				if calledName == "" {
					calledName = child.Utf8Text(content)
				}
			case "value_arguments":
				argumentCount = countArguments(child)
				extractLambdasInArguments(child, content, filePath, callerName, classStack, packageName, lambdaCounter, result)
			}
		}
	}

	if calledName == "" {
		if navigationNode != nil {
			extractCalls(navigationNode, content, filePath, callerName, callerKind, classStack, packageName, lambdaCounter, result)
		}
		return
	}

	line := int(node.StartPosition().Row) + 1

	result.Calls = append(result.Calls, model.RawCall{
		CalledName:   calledName,
		CallerName:   callerName,
		CallerKind:   callerKind,
		ReceiverExpr: receiverExpression,
		FilePath:     filePath,
		Line:         line,
		Language:     "kotlin",
		ArgCount:     argumentCount,
	})

	// Handle trailing lambda
	if lambdaNode != nil {
		*lambdaCounter++
		lambdaName := fmt.Sprintf("lambda$%d", *lambdaCounter)
		lambdaQualifiedName := callerName + "." + lambdaName
		lambdaSymbolID := astutil.GenerateSymbolID(filePath, lambdaQualifiedName, int(lambdaNode.StartPosition().Row)+1)

		result.Symbols = append(result.Symbols, model.Symbol{
			ID:            lambdaSymbolID,
			Name:          lambdaName,
			QualifiedName: lambdaQualifiedName,
			Kind:          constants.KindFunction,
			FilePath:      filePath,
			StartLine:     int(lambdaNode.StartPosition().Row) + 1,
			EndLine:       int(lambdaNode.EndPosition().Row) + 1,
			IsLambda:      true,
			LambdaContext: callerName,
		})

		result.Calls = append(result.Calls, model.RawCall{
			CalledName:          lambdaQualifiedName,
			CallerName:          callerName,
			CallerKind:          callerKind,
			FilePath:            filePath,
			Line:                int(lambdaNode.StartPosition().Row) + 1,
			IsPreResolved:       true,
			LambdaOwnerMethod:   calledName,
			LambdaOwnerReceiver: receiverExpression,
			Language:            "kotlin",
		})

		// Recurse into lambda body
		var lambdaLiteral *tree_sitter.Node
		for i := uint(0); i < lambdaNode.ChildCount(); i++ {
			if lambdaNode.Child(i).Kind() == "lambda_literal" {
				lambdaLiteral = lambdaNode.Child(i)
				break
			}
		}
		if lambdaLiteral != nil {
			extractCalls(lambdaLiteral, content, filePath, lambdaQualifiedName, constants.KindFunction, classStack, packageName, lambdaCounter, result)
		}

		// Also recurse into navigation expression for chained calls (e.g. repo.getUser(id).also { })
		if navigationNode != nil {
			extractCalls(navigationNode, content, filePath, callerName, callerKind, classStack, packageName, lambdaCounter, result)
		}
	} else {
		// No trailing lambda — recurse into the navigation expression for nested calls (chain)
		if navigationNode != nil {
			extractCalls(navigationNode, content, filePath, callerName, callerKind, classStack, packageName, lambdaCounter, result)
		}
	}
}

// extractNavigationExpression extracts receiver and method name from a navigation_expression.
func extractNavigationExpression(node *tree_sitter.Node, content []byte) (receiverExpression string, calledName string) {
	// navigation_expression children: [receiver_expr] [. or ?.] [identifier]
	// The last identifier is the called method name.
	// Everything before the last separator is the receiver.
	childCount := node.ChildCount()
	if childCount < 3 {
		return "", node.Utf8Text(content)
	}

	lastChild := node.Child(childCount - 1)
	if lastChild.Kind() == "identifier" {
		calledName = lastChild.Utf8Text(content)
	}

	// Receiver is everything up to the last . or ?.
	// For simple cases: identifier.identifier → receiver = first identifier
	// For complex cases: call_expression.identifier → receiver = call_expression text
	firstChild := node.Child(0)
	switch firstChild.Kind() {
	case "identifier":
		receiverExpression = firstChild.Utf8Text(content)
	case "super_expression":
		receiverExpression = "super"
		// Check for super<Type> form
		for i := uint(0); i < firstChild.ChildCount(); i++ {
			if firstChild.Child(i).Kind() == "user_type" {
				receiverExpression = "super<" + firstChild.Child(i).Utf8Text(content) + ">"
			}
		}
	default:
		// Complex receiver (e.g. another call_expression) — use full text
		// But trim the last .identifier or ?.identifier
		fullText := node.Utf8Text(content)
		if calledName != "" {
			// Remove the last .calledName or ?.calledName
			suffixes := []string{"?." + calledName, "." + calledName}
			for _, suffix := range suffixes {
				if strings.HasSuffix(fullText, suffix) {
					receiverExpression = fullText[:len(fullText)-len(suffix)]
					break
				}
			}
		}
		if receiverExpression == "" {
			receiverExpression = firstChild.Utf8Text(content)
		}
	}

	return receiverExpression, calledName
}

// countArguments counts the number of value_argument nodes in a value_arguments node.
func countArguments(node *tree_sitter.Node) int {
	count := 0
	for i := uint(0); i < node.ChildCount(); i++ {
		if node.Child(i).Kind() == "value_argument" {
			count++
		}
	}
	return count
}

// extractLambdasInArguments extracts lambda expressions inside value_arguments (non-trailing).
func extractLambdasInArguments(node *tree_sitter.Node, content []byte, filePath string, callerName string, classStack []string, packageName string, lambdaCounter *int, result *model.ParseResult) {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "value_argument" {
			for j := uint(0); j < child.ChildCount(); j++ {
				argChild := child.Child(j)
				if argChild.Kind() == "lambda_literal" {
					*lambdaCounter++
					lambdaName := fmt.Sprintf("lambda$%d", *lambdaCounter)
					lambdaQualifiedName := callerName + "." + lambdaName
					lambdaSymbolID := astutil.GenerateSymbolID(filePath, lambdaQualifiedName, int(argChild.StartPosition().Row)+1)

					result.Symbols = append(result.Symbols, model.Symbol{
						ID:            lambdaSymbolID,
						Name:          lambdaName,
						QualifiedName: lambdaQualifiedName,
						Kind:          constants.KindFunction,
						FilePath:      filePath,
						StartLine:     int(argChild.StartPosition().Row) + 1,
						EndLine:       int(argChild.EndPosition().Row) + 1,
						IsLambda:      true,
						LambdaContext: callerName,
					})

					result.Calls = append(result.Calls, model.RawCall{
						CalledName:    lambdaQualifiedName,
						CallerName:    callerName,
						CallerKind:    constants.KindFunction,
						FilePath:      filePath,
						Line:          int(argChild.StartPosition().Row) + 1,
						IsPreResolved: true,
						Language:      "kotlin",
					})

					extractCalls(argChild, content, filePath, lambdaQualifiedName, constants.KindFunction, classStack, packageName, lambdaCounter, result)
				}
			}
		}
	}
}
