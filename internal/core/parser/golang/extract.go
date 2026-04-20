package golang

import (
	"encoding/json"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
)

// extractGo extracts symbols from Go AST.
func Extract(rootNode *tree_sitter.Node, content []byte, file scanner.ScannedFile, result *model.ParseResult) {
	packageName := ""

	astutil.WalkNamedChildren(rootNode, func(node *tree_sitter.Node) bool {
		switch node.Kind() {
		case "package_clause":
			pkgNode := astutil.FindChildByKind(node, "package_identifier")
			if pkgNode != nil {
				packageName = pkgNode.Utf8Text(content)
			}
			return false

		case "import_declaration":
			extractImports(node, content, file.RelPath, result)
			return false

		case "type_declaration":
			extractTypeDeclaration(node, content, file, packageName, result)
			return false

		case "function_declaration":
			extractFunction(node, content, file.RelPath, packageName, "", result)
			return false

		case "method_declaration":
			extractMethod(node, content, file.RelPath, packageName, result)
			return false
		}
		return true
	})
}

// extractGoImports extracts import declarations.
func extractImports(node *tree_sitter.Node, content []byte, filePath string, result *model.ParseResult) {
	astutil.WalkNamedChildren(node, func(child *tree_sitter.Node) bool {
		if child.Kind() == "import_spec" {
			modulePath := ""
			alias := ""

			nameNode := child.ChildByFieldName("name")
			if nameNode != nil {
				alias = nameNode.Utf8Text(content)
			}

			pathNode := child.ChildByFieldName("path")
			if pathNode != nil {
				modulePath = strings.Trim(pathNode.Utf8Text(content), "\"")
			}

			if modulePath != "" {
				result.Imports = append(result.Imports, model.RawImport{
					ModulePath: modulePath,
					Alias:      alias,
					FilePath:   filePath,
					Line:       int(child.StartPosition().Row) + 1,
				})
			}
		}
		return true
	})
}

// extractGoTypeDeclaration handles type declarations (struct, interface).
func extractTypeDeclaration(node *tree_sitter.Node, content []byte, file scanner.ScannedFile, packageName string, result *model.ParseResult) {
	astutil.WalkNamedChildren(node, func(child *tree_sitter.Node) bool {
		if child.Kind() == "type_spec" {
			nameNode := child.ChildByFieldName("name")
			if nameNode == nil {
				return false
			}
			typeName := nameNode.Utf8Text(content)
			typeNode := child.ChildByFieldName("type")
			if typeNode == nil {
				return false
			}

			switch typeNode.Kind() {
			case "struct_type":
				extractStruct(child, typeNode, content, file, packageName, typeName, result)
			case "interface_type":
				extractInterface(child, typeNode, content, file, packageName, typeName, result)
			}
		}
		return false
	})
}

// extractGoStruct extracts a struct type.
func extractStruct(specNode, structNode *tree_sitter.Node, content []byte, file scanner.ScannedFile, packageName, structName string, result *model.ParseResult) {
	qualifiedName := packageName + "." + structName

	result.Symbols = append(result.Symbols, model.Symbol{
		ID:            astutil.GenerateSymbolID(file.RelPath, qualifiedName, int(specNode.StartPosition().Row)+1),
		Name:          structName,
		QualifiedName: qualifiedName,
		Kind:          constants.KindClass,
		ClassType:     constants.ClassTypeStruct,
		FilePath:      file.RelPath,
		StartLine:     int(specNode.StartPosition().Row) + 1,
		EndLine:       int(specNode.EndPosition().Row) + 1,
		IsExported:    isExported(structName),
	})

	// Extract fields and embedded types
	fieldList := astutil.FindChildByKind(structNode, "field_declaration_list")
	if fieldList == nil {
		return
	}

	for i := uint(0); i < fieldList.ChildCount(); i++ {
		field := fieldList.Child(i)
		if field.Kind() != "field_declaration" {
			continue
		}

		nameNode := field.ChildByFieldName("name")
		typeNode := field.ChildByFieldName("type")

		if nameNode != nil && typeNode != nil {
			// Named field → type hint
			fieldName := nameNode.Utf8Text(content)
			fieldType := extractTypeName(typeNode, content)
			if fieldType != "" {
				structQualifiedName := packageName + "." + structName
				result.TypeHints = append(result.TypeHints, model.TypeBinding{
					VarName:  fieldName,
					TypeName: fieldType,
					Tier:     0,
					Scope:    structQualifiedName,
					FilePath: file.RelPath,
				})
			}
		} else if typeNode != nil {
			// Embedded type → heritage (struct embedding)
			embeddedType := extractTypeName(typeNode, content)
			if embeddedType != "" {
				parentName := embeddedType
				parentQualified := ""
				// Cross-package embedding: "pkga.Service" → resolve via imports
				if dotIdx := strings.Index(embeddedType, "."); dotIdx > 0 {
					alias := embeddedType[:dotIdx]
					typeName := embeddedType[dotIdx+1:]
					parentName = typeName
					for _, imp := range result.Imports {
						impAlias := imp.Alias
						if impAlias == "" {
							if slashIdx := strings.LastIndex(imp.ModulePath, "/"); slashIdx >= 0 {
								impAlias = imp.ModulePath[slashIdx+1:]
							} else {
								impAlias = imp.ModulePath
							}
						}
						if impAlias == alias {
							parentQualified = impAlias + "." + typeName
							break
						}
					}
				}
				result.Heritage = append(result.Heritage, model.RawHeritage{
					ChildName:       structName,
					ChildQualified:  qualifiedName,
					ParentName:      parentName,
					ParentQualified: parentQualified,
					Kind:            "embedding",
					FilePath:        file.RelPath,
				})
			}
		}
	}
}

// extractGoInterface extracts an interface type.
func extractInterface(specNode, interfaceNode *tree_sitter.Node, content []byte, file scanner.ScannedFile, packageName, interfaceName string, result *model.ParseResult) {
	qualifiedName := packageName + "." + interfaceName

	result.Symbols = append(result.Symbols, model.Symbol{
		ID:            astutil.GenerateSymbolID(file.RelPath, qualifiedName, int(specNode.StartPosition().Row)+1),
		Name:          interfaceName,
		QualifiedName: qualifiedName,
		Kind:          constants.KindInterface,
		FilePath:      file.RelPath,
		StartLine:     int(specNode.StartPosition().Row) + 1,
		EndLine:       int(specNode.EndPosition().Row) + 1,
		IsExported:    isExported(interfaceName),
	})

	// Extract interface method signatures as Function symbols
	for i := uint(0); i < interfaceNode.ChildCount(); i++ {
		child := interfaceNode.Child(i)
		if child.Kind() == "type_elem" {
			// Interface embedding — generate heritage
			typeChild := child.Child(0)
			if typeChild == nil {
				continue
			}
			embeddedType := extractTypeName(typeChild, content)
			if embeddedType == "" {
				continue
			}
			parentName := embeddedType
			parentQualified := ""
			if dotIdx := strings.Index(embeddedType, "."); dotIdx > 0 {
				alias := embeddedType[:dotIdx]
				typeName := embeddedType[dotIdx+1:]
				parentName = typeName
				for _, imp := range result.Imports {
					impAlias := imp.Alias
					if impAlias == "" {
						if slashIdx := strings.LastIndex(imp.ModulePath, "/"); slashIdx >= 0 {
							impAlias = imp.ModulePath[slashIdx+1:]
						} else {
							impAlias = imp.ModulePath
						}
					}
					if impAlias == alias {
						parentQualified = impAlias + "." + typeName
						break
					}
				}
			}
			result.Heritage = append(result.Heritage, model.RawHeritage{
				ChildName:       interfaceName,
				ChildQualified:  qualifiedName,
				ParentName:      parentName,
				ParentQualified: parentQualified,
				Kind:            "embedding",
				FilePath:        file.RelPath,
			})
			continue
		}
		if child.Kind() != "method_elem" {
			continue
		}
		methodName := ""
		for j := uint(0); j < child.ChildCount(); j++ {
			c := child.Child(j)
			if c.Kind() == "field_identifier" {
				methodName = c.Utf8Text(content)
				break
			}
		}
		if methodName == "" {
			continue
		}
		methodQN := qualifiedName + "." + methodName
		paramTypes := extractInterfaceMethodParams(child, content)
		paramsJSON, _ := json.Marshal(paramTypes)

		result.Symbols = append(result.Symbols, model.Symbol{
			ID:            astutil.GenerateSymbolID(file.RelPath, methodQN, int(child.StartPosition().Row)+1),
			Name:          methodName,
			QualifiedName: methodQN,
			Kind:          constants.KindFunction,
			FilePath:      file.RelPath,
			StartLine:     int(child.StartPosition().Row) + 1,
			EndLine:       int(child.EndPosition().Row) + 1,
			IsExported:    isExported(methodName),
			IsAbstract:    true,
			Params:        string(paramsJSON),
		})
	}
}

func extractInterfaceMethodParams(methodElem *tree_sitter.Node, content []byte) []map[string]string {
	var params []map[string]string
	for i := uint(0); i < methodElem.ChildCount(); i++ {
		child := methodElem.Child(i)
		if child.Kind() != "parameter_list" {
			continue
		}
		// First parameter_list is input params; stop after processing it
		// (second parameter_list, if any, is return types)
		for j := uint(0); j < child.ChildCount(); j++ {
			param := child.Child(j)
			if param.Kind() == "parameter_declaration" {
				name := ""
				typ := ""
				for k := uint(0); k < param.ChildCount(); k++ {
					c := param.Child(k)
					if c.Kind() == "identifier" {
						name = c.Utf8Text(content)
					} else if c.IsNamed() && name != "" {
						typ = c.Utf8Text(content)
					}
				}
				params = append(params, map[string]string{"name": name, "type": typ})
			}
		}
		return params // only process the first parameter_list
	}
	return params
}

// extractGoFunction extracts a top-level function declaration.
func extractFunction(node *tree_sitter.Node, content []byte, filePath, packageName, className string, result *model.ParseResult) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	funcName := nameNode.Utf8Text(content)

	qualifiedName := packageName + "." + funcName
	if className != "" {
		qualifiedName = packageName + "." + className + "." + funcName
	}

	returnTypes := extractReturnTypes(node, content, packageName)
	paramTypes := extractParams(node, content)
	paramsJSON, _ := json.Marshal(paramTypes)

	// Add type hints for parameters
	for _, param := range paramTypes {
		if param["name"] != "" && param["type"] != "" {
			result.TypeHints = append(result.TypeHints, model.TypeBinding{
				VarName: param["name"], TypeName: param["type"], Tier: 0, Scope: qualifiedName, FilePath: filePath,
			})
		}
	}

	complexity := 1
	body := node.ChildByFieldName("body")
	if body != nil {
		complexity = countComplexity(body, content)
		extractCalls(body, content, filePath, funcName, className, packageName, result)
	}

	result.Symbols = append(result.Symbols, model.Symbol{
		ID:            astutil.GenerateSymbolID(filePath, qualifiedName, int(node.StartPosition().Row)+1),
		Name:          funcName,
		QualifiedName: qualifiedName,
		Kind:          constants.KindFunction,
		FilePath:      filePath,
		StartLine:     int(node.StartPosition().Row) + 1,
		EndLine:       int(node.EndPosition().Row) + 1,
		Params:        string(paramsJSON),
		ReturnTypes:   returnTypes,
		IsExported:    isExported(funcName),
		Complexity:    complexity,
	})
}

// extractGoMethod extracts a method declaration (has receiver).
func extractMethod(node *tree_sitter.Node, content []byte, filePath, packageName string, result *model.ParseResult) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	funcName := nameNode.Utf8Text(content)

	// Extract receiver type
	receiverType := ""
	receiverVarName := ""
	receiverList := astutil.FindChildByKind(node, "parameter_list")
	if receiverList != nil {
		for i := uint(0); i < receiverList.ChildCount(); i++ {
			param := receiverList.Child(i)
			if param.Kind() == "parameter_declaration" {
				nameNode := param.ChildByFieldName("name")
				if nameNode != nil {
					receiverVarName = nameNode.Utf8Text(content)
				}
				typeNode := param.ChildByFieldName("type")
				if typeNode != nil {
					receiverType = extractTypeName(typeNode, content)
				}
			}
		}
	}

	qualifiedName := packageName + "." + funcName
	receiverName := strings.TrimPrefix(receiverType, "*")
	if receiverName != "" {
		qualifiedName = packageName + "." + receiverName + "." + funcName
	}

	returnTypes := extractReturnTypes(node, content, packageName)

	// Get params from the second parameter_list (first is receiver)
	paramLists := astutil.CollectChildrenByKind(node, "parameter_list")
	var paramTypes []map[string]string
	if len(paramLists) >= 2 {
		paramTypes = extractParamList(paramLists[1], content)
	}
	paramsJSON, _ := json.Marshal(paramTypes)

	// Add type hints for parameters
	for _, param := range paramTypes {
		if param["name"] != "" && param["type"] != "" {
			result.TypeHints = append(result.TypeHints, model.TypeBinding{
				VarName: param["name"], TypeName: param["type"], Tier: 0, Scope: qualifiedName, FilePath: filePath,
			})
		}
	}

	// Add type hint for method receiver: func (s *Store) Method() → s:Store
	if receiverVarName != "" && receiverName != "" {
		result.TypeHints = append(result.TypeHints, model.TypeBinding{
			VarName: receiverVarName, TypeName: receiverName, Tier: 0, Scope: qualifiedName, FilePath: filePath,
		})
	}
	complexity := 1
	body := node.ChildByFieldName("body")
	if body != nil {
		complexity = countComplexity(body, content)
		extractCalls(body, content, filePath, funcName, receiverName, packageName, result)
	}

	result.Symbols = append(result.Symbols, model.Symbol{
		ID:            astutil.GenerateSymbolID(filePath, qualifiedName, int(node.StartPosition().Row)+1),
		Name:          funcName,
		QualifiedName: qualifiedName,
		Kind:          constants.KindFunction,
		FilePath:      filePath,
		StartLine:     int(node.StartPosition().Row) + 1,
		EndLine:       int(node.EndPosition().Row) + 1,
		Params:        string(paramsJSON),
		ReturnTypes:   returnTypes,
		IsExported:    isExported(funcName),
		Complexity:    complexity,
	})
}

// extractGoCalls walks a function body for calls.
// extractMultiReturnHints generates TypeHints for multi-return value assignments.
// Handles: a, b, c := funcName() and a, b = receiver.Method()
func extractMultiReturnHints(body *tree_sitter.Node, content []byte, filePath, callerName string, result *model.ParseResult) {
	astutil.WalkNamedChildren(body, func(node *tree_sitter.Node) bool {
		if node.Kind() != "short_var_declaration" && node.Kind() != "assignment_statement" {
			return true
		}
		// Left side: expression_list
		left := node.ChildByFieldName("left")
		if left == nil || left.Kind() != "expression_list" {
			return true
		}
		// Right side: must be single call_expression
		right := node.ChildByFieldName("right")
		if right == nil {
			return true
		}
		var callExpr *tree_sitter.Node
		if right.Kind() == "expression_list" && right.NamedChildCount() == 1 {
			callExpr = right.NamedChild(0)
		} else if right.Kind() == "call_expression" {
			callExpr = right
		}
		if callExpr == nil || callExpr.Kind() != "call_expression" {
			return true
		}

		// Collect left-side variable names
		var varNames []string
		for i := uint(0); i < left.ChildCount(); i++ {
			child := left.Child(i)
			if child.Kind() == "identifier" {
				varNames = append(varNames, child.Utf8Text(content))
			}
		}
		if len(varNames) < 2 {
			return true
		}

		// Extract function expression (funcName or receiver.Method)
		funcNode := callExpr.ChildByFieldName("function")
		if funcNode == nil {
			return true
		}
		funcExpr := funcNode.Utf8Text(content)

		// Generate TypeHint for each variable (skip _)
		for i, name := range varNames {
			if name == "_" {
				continue
			}
			result.TypeHints = append(result.TypeHints, model.TypeBinding{
				VarName:       name,
				Tier:          0,
				Scope:         callerName,
				FilePath:      filePath,
				MultiReturnOf: funcExpr,
				ReturnIndex:   i,
			})
		}
		return true
	})
}

func extractCalls(body *tree_sitter.Node, content []byte, filePath, callerName, callerClass, packageName string, result *model.ParseResult) {
	fullCaller := callerName
	if callerClass != "" {
		fullCaller = callerClass + "." + callerName
	}
	if packageName != "" {
		fullCaller = packageName + "." + fullCaller
	}

	// Extract multi-return value TypeHints
	extractMultiReturnHints(body, content, filePath, fullCaller, result)

	groupPrefixes := CollectGroupPrefixes(body, content)

	// Extract ORM queries
	ExtractORM(body, content, fullCaller, filePath, result)

	// Build imports map: alias → full import path (for receiver verification)
	importsMap := make(map[string]string)
	for _, imp := range result.Imports {
		path := imp.ModulePath
		// Go import alias is last segment of path
		alias := path
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			alias = path[idx+1:]
		}
		if imp.Alias != "" {
			alias = imp.Alias
		}
		importsMap[alias] = path
	}
	ExtractGoRemoteCalls(body, content, fullCaller, filePath, importsMap, result)
	ExtractGoGRPCRegister(body, content, fullCaller, filePath, result)

	astutil.WalkNamedChildren(body, func(node *tree_sitter.Node) bool {
		if node.Kind() == "call_expression" {
			ExtractRoutes(node, content, callerName, filePath, groupPrefixes, result)
			calledName := ""
			receiverExpr := ""

			funcNode := node.ChildByFieldName("function")
			if funcNode != nil {
				switch funcNode.Kind() {
				case "identifier":
					calledName = funcNode.Utf8Text(content)
				case "selector_expression":
					operand := funcNode.ChildByFieldName("operand")
					field := funcNode.ChildByFieldName("field")
					if operand != nil {
						receiverExpr = operand.Utf8Text(content)
					}
					if field != nil {
						calledName = field.Utf8Text(content)
					}
				}
			}

			argCount := 0
			argsNode := node.ChildByFieldName("arguments")
			if argsNode != nil {
				for j := uint(0); j < argsNode.ChildCount(); j++ {
					if argsNode.Child(j).IsNamed() {
						argCount++
					}
				}
			}

			if calledName != "" {
				fc := astutil.DetectFlowContext(node, content)
				result.Calls = append(result.Calls, model.RawCall{
					CalledName:   calledName,
					CallerName:   fullCaller,
					CallerKind:   constants.KindFunction,
					FilePath:     filePath,
					Line:         int(node.StartPosition().Row) + 1,
					ArgCount:     argCount,
					ReceiverExpr: receiverExpr,
					FlowContext:  fc.Kind,
					FlowLine:    fc.Line,
				})
			}
		}
		return true
	})
}

// Helper functions

func extractTypeName(node *tree_sitter.Node, content []byte) string {
	switch node.Kind() {
	case "type_identifier":
		return node.Utf8Text(content)
	case "pointer_type":
		child := astutil.FindChildByKind(node, "type_identifier")
		if child != nil {
			return "*" + child.Utf8Text(content)
		}
	case "qualified_type":
		return node.Utf8Text(content)
	}
	text := node.Utf8Text(content)
	if len(text) < 30 {
		return strings.TrimPrefix(text, "*")
	}
	return ""
}

var goBuiltinTypes = map[string]bool{
	"error": true, "string": true, "int": true, "int8": true,
	"int16": true, "int32": true, "int64": true, "uint": true,
	"uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true, "bool": true, "byte": true,
	"rune": true, "any": true, "interface{}": true, "uintptr": true,
}

func extractReturnTypes(node *tree_sitter.Node, content []byte, packageName string) []string {
	resultNode := node.ChildByFieldName("result")
	if resultNode == nil {
		return nil
	}
	var types []string
	if resultNode.Kind() == "parameter_list" {
		for i := uint(0); i < resultNode.ChildCount(); i++ {
			child := resultNode.Child(i)
			if child.Kind() == "parameter_declaration" {
				typeNode := child.ChildByFieldName("type")
				if typeNode != nil {
					types = append(types, extractTypeName(typeNode, content))
				} else {
					types = append(types, child.Utf8Text(content))
				}
			}
		}
	} else {
		types = []string{extractTypeName(resultNode, content)}
	}
	// Qualify same-package types with package prefix
	for i, t := range types {
		base := strings.TrimPrefix(t, "*")
		base = strings.TrimPrefix(base, "[]")
		if goBuiltinTypes[base] || strings.Contains(base, ".") || base == "" {
			continue
		}
		types[i] = strings.Replace(t, base, packageName+"."+base, 1)
	}
	return types
}

func extractParams(node *tree_sitter.Node, content []byte) []map[string]string {
	paramsNode := node.ChildByFieldName("parameters")
	if paramsNode == nil {
		return nil
	}
	return extractParamList(paramsNode, content)
}

func extractParamList(paramsNode *tree_sitter.Node, content []byte) []map[string]string {
	var params []map[string]string
	for i := uint(0); i < paramsNode.ChildCount(); i++ {
		param := paramsNode.Child(i)
		if param.Kind() != "parameter_declaration" {
			continue
		}
		nameNode := param.ChildByFieldName("name")
		typeNode := param.ChildByFieldName("type")
		paramName := ""
		paramType := ""
		if nameNode != nil {
			paramName = nameNode.Utf8Text(content)
		}
		if typeNode != nil {
			paramType = extractTypeName(typeNode, content)
		}
		if paramName != "" {
			params = append(params, map[string]string{"name": paramName, "type": paramType})
		}
	}
	return params
}


func countComplexity(node *tree_sitter.Node, content []byte) int {
	complexity := 1
	astutil.WalkNamedChildren(node, func(child *tree_sitter.Node) bool {
		switch child.Kind() {
		case "if_statement", "for_statement", "expression_case",
			"default_case", "select_statement", "communication_case":
			complexity++
		case "binary_expression":
			for j := uint(0); j < child.ChildCount(); j++ {
				c := child.Child(j)
				if !c.IsNamed() {
					op := c.Utf8Text(content)
					if op == "&&" || op == "||" {
						complexity++
					}
				}
			}
		}
		return true
	})
	return complexity
}

func isExported(name string) bool {
	if len(name) == 0 {
		return false
	}
	return name[0] >= 'A' && name[0] <= 'Z'
}
