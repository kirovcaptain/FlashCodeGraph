package golang

import (
	"fmt"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
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

	// Extract struct-level generic type parameters: type Pair[K, V any] → ["K", "V"]
	typeParams := extractTypeParams(specNode, content)

	result.Symbols = append(result.Symbols, model.Symbol{
		ID:            astutil.GenerateSymbolID(file.RelPath, qualifiedName, int(specNode.StartPosition().Row)+1),
		Name:          structName,
		QualifiedName: qualifiedName,
		Kind:          constants.KindClass,
		ClassType:     constants.ClassTypeStruct,
		FilePath:      file.RelPath,
		StartLine:     int(specNode.StartPosition().Row) + 1,
		EndLine:       int(specNode.EndPosition().Row) + 1,
		TypeParams:    typeParams,
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
				// FieldDeclaration output
				fieldVisibility := "private"
				if isExported(fieldName) {
					fieldVisibility = "public"
				}
				result.Fields = append(result.Fields, model.FieldDeclaration{
					FieldInfo: model.FieldInfo{
						Name:       fieldName,
						Type:       fieldType,
						Visibility: fieldVisibility,
					},
					OwnerQualifiedName: structQualifiedName,
					FilePath:           file.RelPath,
					Line:               int(field.StartPosition().Row) + 1,
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
			Params:        paramTypes,
		})
	}
}

func extractInterfaceMethodParams(methodElem *tree_sitter.Node, content []byte) []model.ParamInfo {
	var params []model.ParamInfo
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
				params = append(params, model.ParamInfo{Name: name, Type: typ})
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

	// Add type hints for parameters
	for _, param := range paramTypes {
		if param.Name != "" && param.Type != "" {
			result.TypeHints = append(result.TypeHints, model.TypeBinding{
				VarName: param.Name, TypeName: param.Type, Tier: 0, Scope: qualifiedName, FilePath: filePath,
			})
		}
	}

	complexity := 1
	body := node.ChildByFieldName("body")
	if body != nil {
		complexity = countComplexity(body, content)
		extractCalls(body, content, filePath, funcName, className, packageName, result)
	}

	// Extract function-level generic type parameters: [T any, U comparable] → ["T", "U"]
	typeParams := extractTypeParams(node, content)

	result.Symbols = append(result.Symbols, model.Symbol{
		ID:            astutil.GenerateSymbolID(filePath, qualifiedName, int(node.StartPosition().Row)+1),
		Name:          funcName,
		QualifiedName: qualifiedName,
		Kind:          constants.KindFunction,
		FilePath:      filePath,
		StartLine:     int(node.StartPosition().Row) + 1,
		EndLine:       int(node.EndPosition().Row) + 1,
		Params:        paramTypes,
		ReturnTypes:   model.StringsToReturnTypes(returnTypes),
		TypeParams:    typeParams,
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
	var paramTypes []model.ParamInfo
	if len(paramLists) >= 2 {
		paramTypes = extractParamList(paramLists[1], content)
	}

	// Add type hints for parameters
	for _, param := range paramTypes {
		if param.Name != "" && param.Type != "" {
			result.TypeHints = append(result.TypeHints, model.TypeBinding{
				VarName: param.Name, TypeName: param.Type, Tier: 0, Scope: qualifiedName, FilePath: filePath,
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

	// Extract method-level generic type parameters: [T any, U comparable] → ["T", "U"]
	typeParams := extractTypeParams(node, content)

	result.Symbols = append(result.Symbols, model.Symbol{
		ID:            astutil.GenerateSymbolID(filePath, qualifiedName, int(node.StartPosition().Row)+1),
		Name:          funcName,
		QualifiedName: qualifiedName,
		Kind:          constants.KindFunction,
		FilePath:      filePath,
		StartLine:     int(node.StartPosition().Row) + 1,
		EndLine:       int(node.EndPosition().Row) + 1,
		Params:        paramTypes,
		ReturnTypes:   model.StringsToReturnTypes(returnTypes),
		TypeParams:    typeParams,
		IsExported:    isExported(funcName),
		Complexity:    complexity,
	})
}

// extractGoCalls walks a function body for calls.
// extractMultiReturnHints generates TypeHints for multi-return value assignments.
// Handles: a, b, c := funcName() and a, b = receiver.Method()
// extractLocalFuncLiteral extracts anonymous functions assigned to local variables.
// Handles: handler := func(w http.ResponseWriter) { ... }
func extractLocalFuncLiteral(node *tree_sitter.Node, content []byte, filePath, parentQualifiedName string, result *model.ParseResult) {
	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child.Kind() != "var_spec" && child.Kind() != "short_var_declaration" {
			// For short_var_declaration, the node itself is the declaration
			if node.Kind() == "short_var_declaration" {
				child = node
			} else {
				continue
			}
		}
		nameNode := child.ChildByFieldName("left")
		if nameNode == nil {
			nameNode = child.ChildByFieldName("name")
		}
		valueNode := child.ChildByFieldName("right")
		if valueNode == nil {
			valueNode = child.ChildByFieldName("value")
		}
		if nameNode == nil || valueNode == nil {
			continue
		}
		// Handle expression_list wrapper
		if valueNode.Kind() == "expression_list" && valueNode.NamedChildCount() == 1 {
			valueNode = valueNode.NamedChild(0)
		}
		if nameNode.Kind() == "expression_list" && nameNode.NamedChildCount() == 1 {
			nameNode = nameNode.NamedChild(0)
		}
		if valueNode.Kind() != "func_literal" {
			continue
		}
		funcName := nameNode.Utf8Text(content)
		if funcName == "" || funcName == "_" {
			continue
		}
		qualifiedName := parentQualifiedName + "." + funcName
		params := extractParams(valueNode, content)
		returnTypes := extractReturnTypes(valueNode, content, "")

		lambdaID := astutil.GenerateSymbolID(filePath, qualifiedName, int(valueNode.StartPosition().Row)+1)
		result.Symbols = append(result.Symbols, model.Symbol{
			ID:            lambdaID,
			Name:          funcName,
			QualifiedName: qualifiedName,
			Kind:          constants.KindFunction,
			FilePath:      filePath,
			StartLine:     int(valueNode.StartPosition().Row) + 1,
			EndLine:       int(valueNode.EndPosition().Row) + 1,
			Params:        params,
			ReturnTypes:   model.StringsToReturnTypes(returnTypes),
			IsLambda:      true,
			LambdaContext: parentQualifiedName,
		})
		// Write TypeHint with LambdaSymbolID for declarative lambda
		result.TypeHints = append(result.TypeHints, model.TypeBinding{
			VarName:        funcName,
			TypeName:       "func",
			Scope:          parentQualifiedName,
			FilePath:       filePath,
			LambdaSymbolID: lambdaID,
		})
		// Produce TypeHints for func literal parameters (Go always has explicit types)
		for _, param := range params {
			if param.Type != "" && param.Name != "" {
				result.TypeHints = append(result.TypeHints, model.TypeBinding{
					VarName:  param.Name,
					TypeName: param.Type,
					Tier:     0,
					Scope:    qualifiedName,
					FilePath: filePath,
				})
			}
		}
		// Recursively extract calls from lambda body
		bodyNode := valueNode.ChildByFieldName("body")
		if bodyNode != nil {
			extractCalls(bodyNode, content, filePath, qualifiedName, "", "", result)
		}
		break // short_var_declaration is the node itself, only process once
	}
}

// mergeGoScopeParents merges discovered block scope parent relationships into the ParseResult.
func mergeGoScopeParents(result *model.ParseResult, parents map[string]string) {
	if len(parents) == 0 {
		return
	}
	if result.ScopeParents == nil {
		result.ScopeParents = make(map[string]string)
	}
	for child, parent := range parents {
		result.ScopeParents[child] = parent
	}
}

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

		// Detect block scope for this declaration
		blockScope := astutil.DetectBlockScope(node, callerName)
		mergeGoScopeParents(result, blockScope.ScopeParents)

		// Generate TypeHint for each variable (skip _)
		for i, name := range varNames {
			if name == "_" {
				continue
			}
			result.TypeHints = append(result.TypeHints, model.TypeBinding{
				VarName:       name,
				Tier:          0,
				Scope:         blockScope.ScopeKey,
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

	lambdaCounter := 0 // method-body-level counter shared across all call sites
	astutil.WalkNamedChildren(body, func(node *tree_sitter.Node) bool {
		// Skip func_literal nodes that are already handled elsewhere
		if node.Kind() == "func_literal" {
			parent := node.Parent()
			if parent == nil {
				return true
			}
			// Argument position → handled by lambda detection in call_expression below
			if parent.Kind() == "argument_list" {
				return false
			}
			// Declarative assignment → handled by extractLocalFuncLiteral
			if parent.Kind() == "expression_list" {
				grandparent := parent.Parent()
				if grandparent != nil && (grandparent.Kind() == "short_var_declaration" || grandparent.Kind() == "var_spec") {
					return false
				}
			}
			// Other positions (return/go/defer) → walker recurses normally (caller=outer method)
			return true
		}
		// Extract local anonymous functions assigned to variables
		// (e.g. handler := func(w http.ResponseWriter) { ... })
		if node.Kind() == "short_var_declaration" || node.Kind() == "var_declaration" {
			blockScope := astutil.DetectBlockScope(node, fullCaller)
			mergeGoScopeParents(result, blockScope.ScopeParents)
			extractLocalFuncLiteral(node, content, filePath, fullCaller, result)
		}
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
			var argExprs []string
			argsNode := node.ChildByFieldName("arguments")
			if argsNode != nil {
				for j := uint(0); j < argsNode.ChildCount(); j++ {
					child := argsNode.Child(j)
					if !child.IsNamed() {
						continue
					}
					if child.Kind() == "func_literal" {
						argCount++
						argExprs = append(argExprs, "")
						lambdaCounter++
						lambdaName := fmt.Sprintf("lambda$%d", lambdaCounter)
						lambdaQualifiedName := fullCaller + "." + lambdaName
						lambdaID := astutil.GenerateSymbolID(filePath, lambdaQualifiedName, int(child.StartPosition().Row)+1)
						lambdaParams := extractParams(child, content)
						result.Symbols = append(result.Symbols, model.Symbol{
							ID:            lambdaID,
							Name:          lambdaName,
							QualifiedName: lambdaQualifiedName,
							Kind:          constants.KindFunction,
							FilePath:      filePath,
							StartLine:     int(child.StartPosition().Row) + 1,
							EndLine:       int(child.EndPosition().Row) + 1,
							IsLambda:      true,
							LambdaContext: fullCaller,
							Params:        lambdaParams,
						})
						// Go func literal parameters always have explicit types — produce TypeHints
						for _, param := range lambdaParams {
							if param.Type != "" && param.Name != "" {
								result.TypeHints = append(result.TypeHints, model.TypeBinding{
									VarName:  param.Name,
									TypeName: param.Type,
									Tier:     0,
									Scope:    lambdaQualifiedName,
									FilePath: filePath,
								})
							}
						}
						result.Calls = append(result.Calls, model.RawCall{
							CalledName:          lambdaQualifiedName,
							CallerName:          fullCaller,
							CallerKind:          constants.KindFunction,
							FilePath:            filePath,
							Line:                int(child.StartPosition().Row) + 1,
							IsPreResolved:       true,
							LambdaOwnerMethod:   calledName,
							LambdaOwnerReceiver: receiverExpr,
						})
						lambdaBody := child.ChildByFieldName("body")
						if lambdaBody != nil {
							extractCalls(lambdaBody, content, filePath, lambdaQualifiedName, "", "", result)
						}
					} else {
						argCount++
						argExprs = append(argExprs, child.Utf8Text(content))
					}
				}
			}

			if calledName != "" {
				flowContext := astutil.DetectFlowContext(node, content)
				blockScope := astutil.DetectBlockScope(node, fullCaller)
				mergeGoScopeParents(result, blockScope.ScopeParents)
				chainID, chainDepth := computeGoChainInfo(node)
				result.Calls = append(result.Calls, model.RawCall{
					CalledName:   calledName,
					CallerName:   fullCaller,
					CallerScope:  blockScope.ScopeKey,
					CallerKind:   constants.KindFunction,
					FilePath:     filePath,
					Line:         int(node.StartPosition().Row) + 1,
					ArgCount:     argCount,
					ArgExprs:     argExprs,
					ReceiverExpr: receiverExpr,
					FlowContext:  flowContext.Kind,
					FlowLine:     flowContext.Line,
					ChainID:      chainID,
					ChainDepth:   chainDepth,
				})
			}
		}
		return true
	})
}

// Helper functions

// extractTypeParams extracts generic type parameter names from a node's type_parameters child.
// For Go: [T any, U comparable] → ["T", "U"] (only names, not constraints).
func extractTypeParams(node *tree_sitter.Node, content []byte) []string {
	typeParametersNode := node.ChildByFieldName("type_parameters")
	if typeParametersNode == nil {
		return nil
	}
	var typeParams []string
	for i := uint(0); i < typeParametersNode.ChildCount(); i++ {
		typeParamDecl := typeParametersNode.Child(i)
		if typeParamDecl.Kind() == "type_parameter_declaration" {
			nameChild := typeParamDecl.ChildByFieldName("name")
			if nameChild != nil {
				typeParams = append(typeParams, nameChild.Utf8Text(content))
			}
		}
	}
	return typeParams
}

func extractTypeName(node *tree_sitter.Node, content []byte) string {
	switch node.Kind() {
	case "type_identifier":
		return node.Utf8Text(content)
	case "pointer_type":
		child := astutil.FindChildByKind(node, "type_identifier")
		if child != nil {
			return "*" + child.Utf8Text(content)
		}
		child = astutil.FindChildByKind(node, "qualified_type")
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

func extractParams(node *tree_sitter.Node, content []byte) []model.ParamInfo {
	paramsNode := node.ChildByFieldName("parameters")
	if paramsNode == nil {
		return nil
	}
	return extractParamList(paramsNode, content)
}

func extractParamList(paramsNode *tree_sitter.Node, content []byte) []model.ParamInfo {
	var params []model.ParamInfo
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
			params = append(params, model.ParamInfo{Name: paramName, Type: paramType})
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

// computeGoChainInfo determines the chain position of a call_expression node in Go.
// Go chain structure: call_expression → function(selector_expression) → operand(call_expression)
// Returns (chainID, chainDepth). chainID is the outermost call's line number (1-based).
// Returns (0, 0) if the node is not part of a chain.
func computeGoChainInfo(node *tree_sitter.Node) (int, int) {
	// Check if this call has an inner chained call
	funcNode := node.ChildByFieldName("function")
	hasInnerChainedCall := false
	if funcNode != nil && funcNode.Kind() == "selector_expression" {
		operand := funcNode.ChildByFieldName("operand")
		if operand != nil && operand.Kind() == "call_expression" {
			hasInnerChainedCall = true
		}
	}

	// Check if this call is the inner part of an outer chain
	hasOuterChainedCall := false
	parent := node.Parent()
	if parent != nil && parent.Kind() == "selector_expression" {
		grandparent := parent.Parent()
		if grandparent != nil && grandparent.Kind() == "call_expression" {
			grandparentFunc := grandparent.ChildByFieldName("function")
			if grandparentFunc != nil && sameGoASTNode(grandparentFunc, parent) {
				hasOuterChainedCall = true
			}
		}
	}

	if !hasInnerChainedCall && !hasOuterChainedCall {
		return 0, 0
	}

	// Walk up to find the outermost call_expression in the chain
	outermost := node
	for {
		outermostParent := outermost.Parent()
		if outermostParent == nil || outermostParent.Kind() != "selector_expression" {
			break
		}
		grandparent := outermostParent.Parent()
		if grandparent == nil || grandparent.Kind() != "call_expression" {
			break
		}
		grandparentFunc := grandparent.ChildByFieldName("function")
		if grandparentFunc == nil || !sameGoASTNode(grandparentFunc, outermostParent) {
			break
		}
		outermost = grandparent
	}
	chainID := int(outermost.StartPosition().Row) + 1

	// Count total depth from outermost inward
	totalDepth := 0
	current := outermost
	for {
		currentFunc := current.ChildByFieldName("function")
		if currentFunc == nil || currentFunc.Kind() != "selector_expression" {
			break
		}
		inner := currentFunc.ChildByFieldName("operand")
		if inner == nil || inner.Kind() != "call_expression" {
			break
		}
		totalDepth++
		current = inner
	}

	// Count distance from outermost to current node
	distanceFromOutermost := 0
	current = outermost
	for !sameGoASTNode(current, node) {
		currentFunc := current.ChildByFieldName("function")
		if currentFunc == nil || currentFunc.Kind() != "selector_expression" {
			break
		}
		inner := currentFunc.ChildByFieldName("operand")
		if inner == nil || inner.Kind() != "call_expression" {
			break
		}
		current = inner
		distanceFromOutermost++
	}

	chainDepth := totalDepth - distanceFromOutermost
	return chainID, chainDepth
}

// sameGoASTNode compares two tree-sitter nodes by byte range and kind (pointer equality is unreliable).
func sameGoASTNode(a, b *tree_sitter.Node) bool {
	if a == nil || b == nil {
		return false
	}
	return a.StartByte() == b.StartByte() && a.EndByte() == b.EndByte() && a.Kind() == b.Kind()
}
