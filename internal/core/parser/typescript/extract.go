package typescript

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

// extractTypeScript extracts symbols from TypeScript/JavaScript AST.
func Extract(rootNode *tree_sitter.Node, content []byte, file scanner.ScannedFile, result *model.ParseResult) {
	astutil.WalkNamedChildren(rootNode, func(node *tree_sitter.Node) bool {
		switch node.Kind() {
		case "import_statement":
			extractImport(node, content, file.RelPath, result)
			return false
		case "export_statement":
			// Unwrap: export may contain class_declaration, function_declaration, etc.
			extractExport(node, content, file, result)
			return false
		case "class_declaration", "abstract_class_declaration":
			extractClass(node, content, file, true, result)
			return false
		case "interface_declaration":
			extractInterface(node, content, file, true, result)
			return false
		case "function_declaration":
			extractFunction(node, content, file.RelPath, "", true, result)
			return false
		case "ambient_declaration":
			extractAmbientDeclaration(node, content, file, result)
			return false
		case "lexical_declaration", "variable_declaration":
			extractArrowFunctions(node, content, file.RelPath, "", result)
			return false
		}
		return true
	})
}

// extractTSImport handles import statements.
func extractImport(node *tree_sitter.Node, content []byte, filePath string, result *model.ParseResult) {
	modulePath := ""
	var symbolNames []string

	sourceNode := node.ChildByFieldName("source")
	if sourceNode != nil {
		modulePath = strings.Trim(sourceNode.Utf8Text(content), "'\"")
	}

	// Named imports: import { A, B } from '...' or default import: import X from '...'
	astutil.WalkNamedChildren(node, func(child *tree_sitter.Node) bool {
		if child.Kind() == "import_clause" {
			hasNamedImports := false
			astutil.WalkNamedChildren(child, func(inner *tree_sitter.Node) bool {
				if inner.Kind() == "named_imports" {
					hasNamedImports = true
					// Walk import_specifiers inside named_imports
					astutil.WalkNamedChildren(inner, func(spec *tree_sitter.Node) bool {
						if spec.Kind() == "import_specifier" {
							nameNode := spec.ChildByFieldName("name")
							if nameNode != nil {
								symbolNames = append(symbolNames, nameNode.Utf8Text(content))
							} else {
								symbolNames = append(symbolNames, spec.Utf8Text(content))
							}
						}
						return false
					})
				}
				return true
			})
			if !hasNamedImports {
				// Default import: import X from '...'
				for i := uint(0); i < child.ChildCount(); i++ {
					c := child.Child(i)
					if c.Kind() == "identifier" {
						symbolNames = append(symbolNames, c.Utf8Text(content))
					}
				}
			}
		}
		return false
	})

	for _, symbolName := range symbolNames {
		result.Imports = append(result.Imports, model.RawImport{
			ModulePath: modulePath,
			SymbolName: symbolName,
			FilePath:   filePath,
			Line:       int(node.StartPosition().Row) + 1,
		})
	}
	if len(symbolNames) == 0 && modulePath != "" {
		result.Imports = append(result.Imports, model.RawImport{
			ModulePath: modulePath,
			FilePath:   filePath,
			Line:       int(node.StartPosition().Row) + 1,
		})
	}
}

// extractAmbientReturnTypes extracts return types from a type_annotation node.
// Handles tuple types [A, B] as multiple return types, otherwise single type.
func extractAmbientReturnTypes(returnTypeNode *tree_sitter.Node, content []byte) []string {
	// type_annotation children: ":" + actual_type_node
	// If actual type is tuple_type, extract each element as a separate return type
	for i := uint(0); i < returnTypeNode.ChildCount(); i++ {
		child := returnTypeNode.Child(i)
		if child.Kind() == "tuple_type" {
			// [UserService, OrderService] → ["UserService", "OrderService"]
			var types []string
			for j := uint(0); j < child.ChildCount(); j++ {
				elem := child.Child(j)
				if elem.IsNamed() {
					types = append(types, elem.Utf8Text(content))
				}
			}
			return types
		}
	}
	// Non-tuple: single return type (e.g. ": QueryResult")
	typeName := extractTypeName(returnTypeNode, content)
	if typeName != "" {
		return []string{typeName}
	}
	return nil
}

// extractAmbientDeclaration handles `declare function/class/interface` statements.
func extractAmbientDeclaration(node *tree_sitter.Node, content []byte, file scanner.ScannedFile, result *model.ParseResult) {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Kind() {
		case "function_signature":
			nameNode := child.ChildByFieldName("name")
			if nameNode == nil {
				continue
			}
			funcName := nameNode.Utf8Text(content)
			qualifiedName := buildQualifiedName(file.RelPath, funcName)
			var returnTypes []string
			returnTypeNode := child.ChildByFieldName("return_type")
			if returnTypeNode != nil {
				returnTypes = extractAmbientReturnTypes(returnTypeNode, content)
			}
			typeParams := extractTypeParams(child, content)
			result.Symbols = append(result.Symbols, model.Symbol{
				ID:            astutil.GenerateSymbolID(file.RelPath, qualifiedName, int(child.StartPosition().Row)+1),
				Name:          funcName,
				QualifiedName: qualifiedName,
				Kind:          constants.KindFunction,
				FilePath:      file.RelPath,
				StartLine:     int(child.StartPosition().Row) + 1,
				EndLine:       int(child.EndPosition().Row) + 1,
				ReturnTypes:   returnTypes,
				TypeParams:    typeParams,
			})
		}
	}
}

// extractTSExport unwraps export statements.
func extractExport(node *tree_sitter.Node, content []byte, file scanner.ScannedFile, result *model.ParseResult) {
	// Check if this is a re-export: export { X } from '...' or export * from '...'
	sourceNode := node.ChildByFieldName("source")
	if sourceNode != nil {
		extractReexport(node, sourceNode, content, file, result)
		return
	}

	// Check if this is an export default
	isDefaultExport := false
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if !child.IsNamed() && child.Utf8Text(content) == "default" {
			isDefaultExport = true
			break
		}
	}

	// Process exported declarations (existing logic)
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Kind() {
		case "class_declaration", "abstract_class_declaration":
			classSymbolIndex := len(result.Symbols)
			extractClass(child, content, file, true, result)
			if isDefaultExport && classSymbolIndex < len(result.Symbols) {
				result.Symbols[classSymbolIndex].IsDefaultExport = true
			}
		case "interface_declaration":
			extractInterface(child, content, file, true, result)
		case "function_declaration":
			funcSymbolIndex := len(result.Symbols)
			extractFunction(child, content, file.RelPath, "", true, result)
			if isDefaultExport && funcSymbolIndex < len(result.Symbols) {
				result.Symbols[funcSymbolIndex].IsDefaultExport = true
			}
		case "lexical_declaration", "variable_declaration":
			extractArrowFunctions(child, content, file.RelPath, "", result)
		}
	}
}

// extractReexport handles "export { X } from '...'" and "export * from '...'" statements.
func extractReexport(node, sourceNode *tree_sitter.Node, content []byte, file scanner.ScannedFile, result *model.ParseResult) {
	modulePath := extractStringContent(sourceNode, content)
	if modulePath == "" {
		return
	}

	// Look for export_clause (named re-export: export { X } from / export { X as Y } from)
	exportClause := astutil.FindChildByKind(node, "export_clause")
	if exportClause != nil {
		for i := uint(0); i < exportClause.ChildCount(); i++ {
			specifier := exportClause.Child(i)
			if specifier.Kind() != "export_specifier" {
				continue
			}
			nameNode := specifier.ChildByFieldName("name")
			if nameNode == nil {
				continue
			}
			importedName := nameNode.Utf8Text(content)
			localName := importedName
			aliasNode := specifier.ChildByFieldName("alias")
			if aliasNode != nil {
				localName = aliasNode.Utf8Text(content)
			}
			result.Imports = append(result.Imports, model.RawImport{
				ModulePath: modulePath,
				SymbolName: importedName,
				LocalName:  localName,
				FilePath:   file.RelPath,
				Line:       int(node.StartPosition().Row) + 1,
				IsReexport: true,
			})
		}
		return
	}

	// No export_clause → wildcard: export * from '...'
	result.Imports = append(result.Imports, model.RawImport{
		ModulePath: modulePath,
		FilePath:   file.RelPath,
		Line:       int(node.StartPosition().Row) + 1,
		IsReexport: true,
		IsWildcard: true,
	})
}

// extractStringContent extracts the text content from a string node (strips quotes).
func extractStringContent(stringNode *tree_sitter.Node, content []byte) string {
	for i := uint(0); i < stringNode.ChildCount(); i++ {
		child := stringNode.Child(i)
		if child.Kind() == "string_fragment" {
			return child.Utf8Text(content)
		}
	}
	// Fallback: strip surrounding quotes
	text := stringNode.Utf8Text(content)
	text = strings.TrimPrefix(text, "'")
	text = strings.TrimPrefix(text, "\"")
	text = strings.TrimSuffix(text, "'")
	text = strings.TrimSuffix(text, "\"")
	return text
}

// markLastSymbolAsDefaultExport marks the most recently added symbol as a default export.
func markLastSymbolAsDefaultExport(result *model.ParseResult) {
	if len(result.Symbols) > 0 {
		result.Symbols[len(result.Symbols)-1].IsDefaultExport = true
	}
}

// extractTSClass extracts a class declaration.
func extractClass(node *tree_sitter.Node, content []byte, file scanner.ScannedFile, isExported bool, result *model.ParseResult) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	className := nameNode.Utf8Text(content)
	qualifiedName := buildQualifiedName(file.RelPath, className)

	classType := constants.ClassTypeClass
	isAbstract := false

	// Check for abstract keyword in modifiers
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if !child.IsNamed() && child.Utf8Text(content) == "abstract" {
			isAbstract = true
			classType = constants.ClassTypeAbstract
		}
	}

	// Heritage: extends / implements
	// TS AST: class_heritage → extends_clause → identifier
	// JS AST: class_heritage → identifier (no extends_clause wrapper)
	heritageNode := astutil.FindChildByKind(node, "class_heritage")
	if heritageNode != nil {
		sawExtends := false
		sawImplements := false
		for i := uint(0); i < heritageNode.ChildCount(); i++ {
			clause := heritageNode.Child(i)
			switch clause.Kind() {
			case "extends_clause":
				var parentName string
				var heritageTypeArgs []model.TypeArg
				for j := uint(0); j < clause.ChildCount(); j++ {
					typeNode := clause.Child(j)
					if typeNode.Kind() == "type_arguments" {
						heritageTypeArgs = extractTypeArgsFromNode(typeNode, content)
					} else if typeNode.IsNamed() {
						parentName = extractTypeName(typeNode, content)
					}
				}
				if parentName != "" {
					result.Heritage = append(result.Heritage, model.RawHeritage{
						ChildName: className, ChildQualified: qualifiedName,
						ParentName: parentName,
						Kind:       "extends", FilePath: file.RelPath,
						TypeArgs:   heritageTypeArgs,
					})
				}
			case "implements_clause":
				for j := uint(0); j < clause.ChildCount(); j++ {
					typeNode := clause.Child(j)
					if !typeNode.IsNamed() {
						continue
					}
					if typeNode.Kind() == "type_arguments" {
						continue
					}
					ifaceName := extractTypeName(typeNode, content)
					if ifaceName == "" {
						continue
					}
					var interfaceTypeArgs []model.TypeArg
					if typeNode.Kind() == "generic_type" {
						interfaceTypeArgs = extractTypeArgsFromNode(astutil.FindChildByKind(typeNode, "type_arguments"), content)
					}
					result.Heritage = append(result.Heritage, model.RawHeritage{
						ChildName: className, ChildQualified: qualifiedName,
						ParentName: ifaceName,
						Kind:       "implements", FilePath: file.RelPath,
						TypeArgs:   interfaceTypeArgs,
					})
				}
			case "extends":
				sawExtends = true
			case "implements":
				sawImplements = true
			case "identifier":
				// JS AST: identifier directly under class_heritage (no clause wrapper)
				parentName := clause.Utf8Text(content)
				if parentName != "" {
					kind := "extends"
					if sawImplements {
						kind = "implements"
					}
					result.Heritage = append(result.Heritage, model.RawHeritage{
						ChildName: className, ChildQualified: qualifiedName,
						ParentName: parentName,
						Kind:       kind, FilePath: file.RelPath,
					})
				}
			}
		}
		_ = sawExtends
	}

	// Extract class-level generic type parameters: class Foo<T, U> → ["T", "U"]
	typeParams := extractTypeParams(node, content)

	result.Symbols = append(result.Symbols, model.Symbol{
		ID:            astutil.GenerateSymbolID(file.RelPath, qualifiedName, int(node.StartPosition().Row)+1),
		Name:          className,
		QualifiedName: qualifiedName,
		Kind:          constants.KindClass,
		ClassType:     classType,
		FilePath:      file.RelPath,
		StartLine:     int(node.StartPosition().Row) + 1,
		EndLine:       int(node.EndPosition().Row) + 1,
		TypeParams:    typeParams,
		IsAbstract:    isAbstract,
		IsExported:    isExported,
	})

	// Extract class body
	body := node.ChildByFieldName("body")
	if body == nil {
		return
	}
	for i := uint(0); i < body.ChildCount(); i++ {
		child := body.Child(i)
		if !child.IsNamed() {
			continue
		}
		if child.Kind() == "method_definition" {
			extractMethod(child, content, file.RelPath, className, result)
		} else if child.Kind() == "public_field_definition" {
			nameNode := astutil.FindChildByKind(child, "property_identifier")
			typeAnn := astutil.FindChildByKind(child, "type_annotation")
			if nameNode != nil && typeAnn != nil {
				var typeNode *tree_sitter.Node
				for j := uint(0); j < typeAnn.ChildCount(); j++ {
					if candidate := typeAnn.Child(j); candidate.IsNamed() {
						typeNode = candidate
						break
					}
				}
				if typeNode != nil {
					fieldName := nameNode.Utf8Text(content)
					typeName := extractTypeName(typeNode, content)
					if typeName != "" {
						result.TypeHints = append(result.TypeHints, model.TypeBinding{
							VarName:  fieldName,
							TypeName: typeName,
							Tier:     0,
							Scope:    qualifiedName,
							FilePath: file.RelPath,
						})
						// FieldDeclaration output
						result.Fields = append(result.Fields, model.FieldDeclaration{
							FieldInfo: model.FieldInfo{
								Name: fieldName,
								Type: typeName,
							},
							OwnerQualifiedName: qualifiedName,
							FilePath:           file.RelPath,
							Line:               int(child.StartPosition().Row) + 1,
						})
					}
				}
			}
		}
	}
}

// extractTSMethod extracts a method from a class body.
func extractMethod(node *tree_sitter.Node, content []byte, filePath, className string, result *model.ParseResult) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	methodName := nameNode.Utf8Text(content)
	qualifiedName := buildQualifiedName(filePath, className) + "." + methodName

	returnTypes := extractReturnTypes(node, content)
	paramTypes := extractParams(node, content)

	// Add type hints for parameters (TS has type annotations)
	for _, param := range paramTypes {
		if param.Name != "" && param.Type != "" {
			result.TypeHints = append(result.TypeHints, model.TypeBinding{
				VarName: param.Name, TypeName: param.Type, Tier: 0, Scope: qualifiedName, FilePath: filePath,
			})
		}
	}

	isAsync := false
	isStatic := false
	isGetter := false // TypeScript `get xxx()` accessor
	isSetter := false // TypeScript `set xxx(v)` accessor
	isConstructor := methodName == "constructor"
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		text := child.Utf8Text(content)
		if text == "async" {
			isAsync = true
		}
		if text == "static" {
			isStatic = true
		}
		if text == "get" && child.Kind() == "get" {
			isGetter = true
		}
		if text == "set" && child.Kind() == "set" {
			isSetter = true
		}
	}

	// Extract method-level generic type parameters: <T, U> → ["T", "U"]
	typeParams := extractTypeParams(node, content)

	complexity := 1
	body := node.ChildByFieldName("body")
	if body != nil {
		complexity = countComplexity(body, content)
		extractCalls(body, content, filePath, qualifiedName, result)
	}

	result.Symbols = append(result.Symbols, model.Symbol{
		ID:            astutil.GenerateSymbolID(filePath, qualifiedName, int(node.StartPosition().Row)+1),
		Name:          methodName,
		QualifiedName: qualifiedName,
		Kind:          constants.KindFunction,
		FilePath:      filePath,
		StartLine:     int(node.StartPosition().Row) + 1,
		EndLine:       int(node.EndPosition().Row) + 1,
		Params:        paramTypes,
		ReturnTypes:   returnTypes,
		TypeParams:    typeParams,
		IsAsync:       isAsync,
		IsStatic:      isStatic,
		IsConstructor: isConstructor,
		IsGetter:      isGetter,
		IsSetter:      isSetter,
		Complexity:    complexity,
	})

	// Check for NestJS-style route decorators
	ExtractDecoratorRoutes(node, content, methodName, className, filePath, result)
}

// extractTSFunction extracts a top-level function declaration.
func extractFunction(node *tree_sitter.Node, content []byte, filePath, className string, isExported bool, result *model.ParseResult) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	funcName := nameNode.Utf8Text(content)
	qualifiedName := buildQualifiedName(filePath, funcName)

	returnTypes := extractReturnTypes(node, content)
	paramTypes := extractParams(node, content)

	for _, param := range paramTypes {
		if param.Name != "" && param.Type != "" {
			result.TypeHints = append(result.TypeHints, model.TypeBinding{
				VarName: param.Name, TypeName: param.Type, Tier: 0, Scope: qualifiedName, FilePath: filePath,
			})
		}
	}

	isAsync := false
	for i := uint(0); i < node.ChildCount(); i++ {
		if node.Child(i).Utf8Text(content) == "async" {
			isAsync = true
		}
	}

	// Extract function-level generic type parameters: <T, U> → ["T", "U"]
	typeParams := extractTypeParams(node, content)

	complexity := 1
	body := node.ChildByFieldName("body")
	if body != nil {
		complexity = countComplexity(body, content)
		extractCalls(body, content, filePath, qualifiedName, result)
	}

	result.Symbols = append(result.Symbols, model.Symbol{
		ID:            astutil.GenerateSymbolID(filePath, qualifiedName, int(node.StartPosition().Row)+1),
		Name:          funcName,
		QualifiedName: qualifiedName,
		Kind:          constants.KindFunction,
		FilePath:      filePath,
		StartLine:     int(node.StartPosition().Row) + 1,
		EndLine:       int(node.EndPosition().Row) + 1,
		Params:        paramTypes,
		ReturnTypes:   returnTypes,
		TypeParams:    typeParams,
		IsAsync:       isAsync,
		IsExported:    isExported,
		Complexity:    complexity,
	})
}

// extractTSVariableDeclaration handles const/let/var with arrow functions.
func extractArrowFunctions(node *tree_sitter.Node, content []byte, filePath, callerQualifiedName string, result *model.ParseResult) {
	astutil.WalkNamedChildren(node, func(child *tree_sitter.Node) bool {
		if child.Kind() == "variable_declarator" {
			nameNode := child.ChildByFieldName("name")
			valueNode := child.ChildByFieldName("value")
			if nameNode != nil && valueNode != nil && valueNode.Kind() == "arrow_function" {
				funcName := nameNode.Utf8Text(content)
				returnTypes := extractReturnTypes(valueNode, content)
				paramTypes := extractParams(valueNode, content)

				var qualifiedName string
				if callerQualifiedName != "" {
					qualifiedName = callerQualifiedName + "." + funcName
				} else {
					qualifiedName = buildQualifiedName(filePath, funcName)
				}

				lambdaID := astutil.GenerateSymbolID(filePath, qualifiedName, int(child.StartPosition().Row)+1)
				result.Symbols = append(result.Symbols, model.Symbol{
					ID:            lambdaID,
					Name:          funcName,
					QualifiedName: qualifiedName,
					Kind:          constants.KindFunction,
					FilePath:      filePath,
					StartLine:     int(child.StartPosition().Row) + 1,
					EndLine:       int(child.EndPosition().Row) + 1,
					Params:        paramTypes,
					ReturnTypes:   returnTypes,
					IsLambda:      true,
					LambdaContext: callerQualifiedName,
				})

				// Write TypeHint with LambdaSymbolID for declarative lambda
				if callerQualifiedName != "" {
					result.TypeHints = append(result.TypeHints, model.TypeBinding{
						VarName:        funcName,
						Scope:          callerQualifiedName,
						FilePath:       filePath,
						LambdaSymbolID: lambdaID,
					})
				}

				body := valueNode.ChildByFieldName("body")
				if body != nil {
					if body.Kind() == "statement_block" {
						extractCalls(body, content, filePath, qualifiedName, result)
					} else {
						// Expression body — pass arrow_function node so walker visits body as child
						extractCalls(valueNode, content, filePath, qualifiedName, result)
					}
				}
			}
		}
		return false
	})
}

// extractTSCalls walks a function body for calls.
func extractCalls(body *tree_sitter.Node, content []byte, filePath, qualifiedCallerName string, result *model.ParseResult) {
	// Extract ORM queries
	ExtractORM(body, content, qualifiedCallerName, filePath, result)

	// Extract HTTP and gRPC remote calls
	ExtractTSRemoteCalls(body, content, qualifiedCallerName, filePath, result)
	ExtractGQLTemplateCalls(body, content, qualifiedCallerName, filePath, result)

	lambdaCounter := 0 // method-body-level counter shared across all call sites
	astutil.WalkNamedChildren(body, func(node *tree_sitter.Node) bool {
		// Skip arrow_function already handled elsewhere:
		// - argument position: handled by lambda detection in call_expression below
		// - declarative (variable_declarator): handled by extractArrowFunctions
		if node.Kind() == "arrow_function" {
			parent := node.Parent()
			if parent != nil && (parent.Kind() == "arguments" || parent.Kind() == "variable_declarator") {
				return false
			}
		}
		if node.Kind() == "lexical_declaration" || node.Kind() == "variable_declaration" {
			extractTSPendingAssignment(node, content, qualifiedCallerName, result)
			// Also extract const arrow functions defined inside function bodies
			// Record nested function → outer function parent relationship
			symbolCountBefore := len(result.Symbols)
			extractArrowFunctions(node, content, filePath, qualifiedCallerName, result)
			for j := symbolCountBefore; j < len(result.Symbols); j++ {
				nestedQN := result.Symbols[j].QualifiedName
				if result.ScopeParents == nil {
					result.ScopeParents = make(map[string]string)
				}
				result.ScopeParents[nestedQN] = qualifiedCallerName
			}
		}
		if node.Kind() == "call_expression" {
			ExtractRoutes(node, content, qualifiedCallerName, filePath, result)
			ExtractChainedRoutes(node, content, qualifiedCallerName, filePath, result)
			calledName := ""
			receiverExpr := ""

			funcNode := node.ChildByFieldName("function")
			if funcNode != nil {
				switch funcNode.Kind() {
				case "identifier":
					calledName = funcNode.Utf8Text(content)
				case "member_expression":
					objNode := funcNode.ChildByFieldName("object")
					propNode := funcNode.ChildByFieldName("property")
					if objNode != nil {
						receiverExpr = objNode.Utf8Text(content)
					}
					if propNode != nil {
						calledName = propNode.Utf8Text(content)
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
					if child.Kind() == "arrow_function" {
						argCount++
						argExprs = append(argExprs, "")
						lambdaCounter++
						lambdaName := fmt.Sprintf("lambda$%d", lambdaCounter)
						lambdaQualifiedName := qualifiedCallerName + "." + lambdaName
						lambdaID := astutil.GenerateSymbolID(filePath, lambdaQualifiedName, int(child.StartPosition().Row)+1)
						result.Symbols = append(result.Symbols, model.Symbol{
							ID:            lambdaID,
							Name:          lambdaName,
							QualifiedName: lambdaQualifiedName,
							Kind:          constants.KindFunction,
							FilePath:      filePath,
							StartLine:     int(child.StartPosition().Row) + 1,
							EndLine:       int(child.EndPosition().Row) + 1,
							IsLambda:      true,
							LambdaContext: qualifiedCallerName,
						})
						result.Calls = append(result.Calls, model.RawCall{
							CalledName:    lambdaQualifiedName,
							CallerName:    qualifiedCallerName,
							CallerKind:    constants.KindFunction,
							FilePath:      filePath,
							Line:          int(child.StartPosition().Row) + 1,
							IsPreResolved: true,
						})
						lambdaBody := child.ChildByFieldName("body")
						if lambdaBody != nil {
							if lambdaBody.Kind() == "statement_block" {
								extractCalls(lambdaBody, content, filePath, lambdaQualifiedName, result)
							} else {
								// Expression body (e.g. call_expression) — wrap in extractCalls on parent
								// since extractCalls walks children, pass the arrow_function body's parent context
								extractCalls(child, content, filePath, lambdaQualifiedName, result)
							}
						}
					} else {
						argCount++
						argExprs = append(argExprs, child.Utf8Text(content))
					}
				}
			}

			if calledName != "" {
				flowContext := astutil.DetectFlowContext(node, content)
				blockScope := astutil.DetectBlockScope(node, qualifiedCallerName)
				mergeScopeParents(result, blockScope.ScopeParents)
				result.Calls = append(result.Calls, model.RawCall{
					CalledName:   calledName,
					CallerName:   qualifiedCallerName,
					CallerScope:  blockScope.ScopeKey,
					CallerKind:   constants.KindFunction,
					FilePath:     filePath,
					Line:         int(node.StartPosition().Row) + 1,
					ArgCount:     argCount,
					ArgExprs:     argExprs,
					ReceiverExpr: receiverExpr,
					FlowContext:  flowContext.Kind,
					FlowLine:     flowContext.Line,
				})
			}
		}
		return true
	})
}

// Helper functions

func extractParams(node *tree_sitter.Node, content []byte) []model.ParamInfo {
	paramsNode := node.ChildByFieldName("parameters")
	if paramsNode == nil {
		return nil
	}
	var params []model.ParamInfo
	for i := uint(0); i < paramsNode.ChildCount(); i++ {
		param := paramsNode.Child(i)
		if !param.IsNamed() {
			continue
		}
		paramName := ""
		paramType := ""

		nameNode := param.ChildByFieldName("pattern")
		if nameNode == nil {
			nameNode = param.ChildByFieldName("name")
		}
		if nameNode == nil && param.Kind() == "identifier" {
			nameNode = param
		}
		if nameNode != nil {
			paramName = nameNode.Utf8Text(content)
		}

		typeNode := param.ChildByFieldName("type")
		if typeNode != nil {
			paramType = extractTypeName(typeNode, content)
		}

		if paramName != "" && paramName != "this" {
			entry := model.ParamInfo{Name: paramName, Type: paramType}
			if param.Kind() == "optional_parameter" || param.ChildByFieldName("value") != nil {
				entry.HasDefault = true
			}
			// Extract TypeArgs for generic_type params (e.g. Constructor<T>)
			if typeNode != nil {
				for j := uint(0); j < typeNode.ChildCount(); j++ {
					typeChild := typeNode.Child(j)
					if typeChild.Kind() == "generic_type" {
						entry.TypeArgs = extractTypeArgsFromNode(astutil.FindChildByKind(typeChild, "type_arguments"), content)
						break
					}
				}
			}
			params = append(params, entry)
		}
	}
	return params
}

// extractTypeParams extracts generic type parameter names from a node's type_parameters child.
// For TS: <T, U extends object> → ["T", "U"] (only names, not constraints).
func extractTypeParams(node *tree_sitter.Node, content []byte) []string {
	typeParametersNode := node.ChildByFieldName("type_parameters")
	if typeParametersNode == nil {
		return nil
	}
	var typeParams []string
	for i := uint(0); i < typeParametersNode.ChildCount(); i++ {
		typeParamNode := typeParametersNode.Child(i)
		if typeParamNode.Kind() == "type_parameter" {
			nameChild := typeParamNode.ChildByFieldName("name")
			if nameChild != nil {
				typeParams = append(typeParams, nameChild.Utf8Text(content))
			}
		}
	}
	return typeParams
}

// extractTypeArgsFromNode extracts type arguments from a type_arguments node.
// For TS: <User, Promise<Order>> → [{Name:"User"}, {Name:"Promise", Args:[{Name:"Order"}]}]
func extractTypeArgsFromNode(typeArgsNode *tree_sitter.Node, content []byte) []model.TypeArg {
	if typeArgsNode == nil {
		return nil
	}
	var typeArgs []model.TypeArg
	for i := uint(0); i < typeArgsNode.ChildCount(); i++ {
		child := typeArgsNode.Child(i)
		if !child.IsNamed() {
			continue
		}
		if child.Kind() == "generic_type" {
			name := extractTypeName(child, content)
			nestedTypeArgs := extractTypeArgsFromNode(astutil.FindChildByKind(child, "type_arguments"), content)
			typeArgs = append(typeArgs, model.TypeArg{Name: name, Args: nestedTypeArgs})
		} else {
			typeArgs = append(typeArgs, model.TypeArg{Name: extractTypeName(child, content)})
		}
	}
	return typeArgs
}

func extractReturnTypes(node *tree_sitter.Node, content []byte) []string {
	returnTypeNode := node.ChildByFieldName("return_type")
	if returnTypeNode == nil {
		return nil
	}
	return []string{extractTypeName(returnTypeNode, content)}
}

func extractTypeName(node *tree_sitter.Node, content []byte) string {
	switch node.Kind() {
	case "type_identifier", "predefined_type", "identifier":
		return node.Utf8Text(content)
	case "type_annotation":
		// : Type → extract the type part
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child.IsNamed() {
				return extractTypeName(child, content)
			}
		}
	case "generic_type":
		nameNode := node.ChildByFieldName("name")
		if nameNode != nil {
			return nameNode.Utf8Text(content)
		}
	}
	text := node.Utf8Text(content)
	text = strings.TrimPrefix(text, ": ")
	if len(text) < 30 {
		return text
	}
	return ""
}

// inferTSArgType infers a type hint from a call argument AST node.
func inferTSArgType(node *tree_sitter.Node, content []byte) string {
	switch node.Kind() {
	case "identifier":
		name := node.Utf8Text(content)
		if len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z' {
			return name
		}
	case "new_expression":
		constructor := node.ChildByFieldName("constructor")
		if constructor != nil && constructor.Kind() == "identifier" {
			return constructor.Utf8Text(content)
		}
	case "string":
		return "string"
	case "number":
		return "number"
	case "true", "false":
		return "boolean"
	}
	return ""
}

// extractTSPendingAssignment extracts assignment patterns for fixpoint type propagation.
func extractTSPendingAssignment(node *tree_sitter.Node, content []byte, scope string, result *model.ParseResult) {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() != "variable_declarator" {
			continue
		}
		nameNode := child.ChildByFieldName("name")
		valueNode := child.ChildByFieldName("value")
		if nameNode == nil || valueNode == nil {
			continue
		}

		// Determine block-level scope for this declaration
		blockScope := astutil.DetectBlockScope(child, scope)
		blockScopeKey := blockScope.ScopeKey
		mergeScopeParents(result, blockScope.ScopeParents)

		// Handle destructuring patterns
		if nameNode.Kind() == "object_pattern" || nameNode.Kind() == "array_pattern" {
			extractDestructureAssignment(nameNode, valueNode, content, blockScopeKey, result)
			continue
		}

		if nameNode.Kind() != "identifier" {
			continue
		}
		lhs := nameNode.Utf8Text(content)

		// Extract local variable type annotation as TypeHint (e.g. const x: SomeType = ...)
		typeNode := child.ChildByFieldName("type")
		if typeNode != nil {
			typeName := extractTypeName(typeNode, content)
			if typeName != "" {
				result.TypeHints = append(result.TypeHints, model.TypeBinding{
					VarName:  lhs,
					TypeName: typeName,
					Tier:     0,
					Scope:    blockScopeKey,
					FilePath: result.FilePath,
				})
			}
		}

		switch valueNode.Kind() {
		case "identifier":
			result.PendingAssignments = append(result.PendingAssignments, model.PendingAssignment{
				Kind: "copy", LHS: lhs, Scope: blockScopeKey, RHS: valueNode.Utf8Text(content),
			})
		case "member_expression":
			obj := valueNode.ChildByFieldName("object")
			prop := valueNode.ChildByFieldName("property")
			if obj != nil && prop != nil && obj.Kind() == "identifier" {
				result.PendingAssignments = append(result.PendingAssignments, model.PendingAssignment{
					Kind: "field_access", LHS: lhs, Scope: blockScopeKey, Receiver: obj.Utf8Text(content), Field: prop.Utf8Text(content),
				})
			}
		case "call_expression":
			fn := valueNode.ChildByFieldName("function")
			if fn == nil {
				continue
			}
			var argTypes []string
			argsNode := valueNode.ChildByFieldName("arguments")
			if argsNode != nil {
				for j := uint(0); j < argsNode.ChildCount(); j++ {
					arg := argsNode.Child(j)
					if arg.IsNamed() {
						argTypes = append(argTypes, inferTSArgType(arg, content))
					}
				}
			}
			if fn.Kind() == "identifier" {
				result.PendingAssignments = append(result.PendingAssignments, model.PendingAssignment{
					Kind: "call_result", LHS: lhs, Scope: blockScopeKey, Callee: fn.Utf8Text(content), ArgTypes: argTypes,
				})
			} else if fn.Kind() == "member_expression" {
				obj := fn.ChildByFieldName("object")
				prop := fn.ChildByFieldName("property")
				if obj != nil && prop != nil && obj.Kind() == "identifier" {
					result.PendingAssignments = append(result.PendingAssignments, model.PendingAssignment{
						Kind: "method_call_result", LHS: lhs, Scope: blockScopeKey, Receiver: obj.Utf8Text(content), Method: prop.Utf8Text(content), ArgTypes: argTypes,
					})
				}
			}
		case "new_expression":
			constructor := valueNode.ChildByFieldName("constructor")
			if constructor != nil && constructor.Kind() == "identifier" {
				typeName := constructor.Utf8Text(content)
				result.TypeHints = append(result.TypeHints, model.TypeBinding{
					VarName:  lhs,
					TypeName: typeName,
					Tier:     1,
					Scope:    blockScopeKey,
					FilePath: result.FilePath,
				})
			}
		}
	}
}

func countComplexity(node *tree_sitter.Node, content []byte) int {
	complexity := 1
	astutil.WalkNamedChildren(node, func(child *tree_sitter.Node) bool {
		switch child.Kind() {
		case "if_statement", "for_statement", "for_in_statement",
			"while_statement", "do_statement", "catch_clause",
			"switch_case", "ternary_expression":
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

func buildQualifiedName(relPath, name string) string {
	modulePath := strings.TrimSuffix(relPath, ".ts")
	modulePath = strings.TrimSuffix(modulePath, ".tsx")
	modulePath = strings.TrimSuffix(modulePath, ".js")
	modulePath = strings.TrimSuffix(modulePath, ".jsx")
	modulePath = strings.ReplaceAll(modulePath, "/", ".")
	modulePath = strings.ReplaceAll(modulePath, "\\", ".")
	if strings.HasSuffix(modulePath, ".index") {
		modulePath = strings.TrimSuffix(modulePath, ".index")
	}
	return modulePath + "." + name
}

// extractTSInterface extracts an interface declaration.
func extractInterface(node *tree_sitter.Node, content []byte, file scanner.ScannedFile, isExported bool, result *model.ParseResult) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	interfaceName := nameNode.Utf8Text(content)
	ifaceQualifiedName := buildQualifiedName(file.RelPath, interfaceName)

	result.Symbols = append(result.Symbols, model.Symbol{
		ID:            astutil.GenerateSymbolID(file.RelPath, ifaceQualifiedName, int(node.StartPosition().Row)+1),
		Name:          interfaceName,
		QualifiedName: ifaceQualifiedName,
		Kind:          constants.KindInterface,
		FilePath:      file.RelPath,
		StartLine:     int(node.StartPosition().Row) + 1,
		EndLine:       int(node.EndPosition().Row) + 1,
		IsExported:    isExported,
	})

	// Extract interface method signatures as Function symbols
	body := node.ChildByFieldName("body")
	if body == nil {
		return
	}
	for i := uint(0); i < body.ChildCount(); i++ {
		child := body.Child(i)
		switch child.Kind() {
		case "method_signature":
			// addNode(node: string): void
		case "property_signature":
			// Extract as field declaration for FindFieldByOwner
			propNameNode := child.ChildByFieldName("name")
			propTypeNode := child.ChildByFieldName("type")
			if propNameNode != nil && propTypeNode != nil {
				propName := propNameNode.Utf8Text(content)
				propType := extractTypeName(propTypeNode, content)
				if propType != "" {
					// Function-typed property also gets a Symbol (existing behavior)
					if strings.Contains(propTypeNode.Utf8Text(content), "=>") {
						// fall through to method extraction below
					} else {
						// Plain field — emit FieldDeclaration only
						result.Fields = append(result.Fields, model.FieldDeclaration{
							FieldInfo: model.FieldInfo{
								Name: propName,
								Type: propType,
							},
							OwnerQualifiedName: ifaceQualifiedName,
							FilePath:           file.RelPath,
							Line:               int(child.StartPosition().Row) + 1,
						})
						continue
					}
				} else {
					continue
				}
			} else if propTypeNode == nil || !strings.Contains(propTypeNode.Utf8Text(content), "=>") {
				continue
			}
		default:
			continue
		}
		methodNameNode := child.ChildByFieldName("name")
		if methodNameNode == nil {
			continue
		}
		methodName := methodNameNode.Utf8Text(content)
		methodQualifiedName := ifaceQualifiedName + "." + methodName
		returnTypes := extractReturnTypes(child, content)
		paramTypes := extractParams(child, content)

		result.Symbols = append(result.Symbols, model.Symbol{
			ID:            astutil.GenerateSymbolID(file.RelPath, methodQualifiedName, int(child.StartPosition().Row)+1),
			Name:          methodName,
			QualifiedName: methodQualifiedName,
			Kind:          constants.KindFunction,
			FilePath:      file.RelPath,
			StartLine:     int(child.StartPosition().Row) + 1,
			EndLine:       int(child.EndPosition().Row) + 1,
			IsExported:    isExported,
			IsAbstract:    true,
			Params:        paramTypes,
			ReturnTypes:   returnTypes,
		})
	}
}

// mergeScopeParents merges discovered scope parent relationships into the ParseResult.
func mergeScopeParents(result *model.ParseResult, parents map[string]string) {
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

// extractDestructureAssignment handles object/array destructuring patterns.
func extractDestructureAssignment(nameNode, valueNode *tree_sitter.Node, content []byte, scope string, result *model.ParseResult) {
	callee := extractCalleeName(valueNode, content)
	if callee == "" {
		return
	}

	if nameNode.Kind() == "object_pattern" {
		for i := uint(0); i < nameNode.ChildCount(); i++ {
			fieldNode := nameNode.Child(i)
			switch fieldNode.Kind() {
			case "shorthand_property_identifier_pattern":
				fieldName := fieldNode.Utf8Text(content)
				result.PendingAssignments = append(result.PendingAssignments, model.PendingAssignment{
					Kind: "destructure", LHS: fieldName, Scope: scope, Callee: callee, DestructuredKey: fieldName,
				})
			case "pair_pattern":
				keyNode := fieldNode.ChildByFieldName("key")
				valNode := fieldNode.ChildByFieldName("value")
				if keyNode != nil && valNode != nil {
					result.PendingAssignments = append(result.PendingAssignments, model.PendingAssignment{
						Kind: "destructure", LHS: valNode.Utf8Text(content), Scope: scope, Callee: callee, DestructuredKey: keyNode.Utf8Text(content),
					})
				}
			}
		}
	} else if nameNode.Kind() == "array_pattern" {
		idx := 0
		for i := uint(0); i < nameNode.ChildCount(); i++ {
			elemNode := nameNode.Child(i)
			if elemNode.Kind() == "identifier" {
				result.PendingAssignments = append(result.PendingAssignments, model.PendingAssignment{
					Kind: "destructure", LHS: elemNode.Utf8Text(content), Scope: scope, Callee: callee, DestructuredKey: strconv.Itoa(idx),
				})
				idx++
			} else if elemNode.IsNamed() {
				idx++ // skip rest_pattern etc but count position
			}
		}
	}
}

// extractCalleeName extracts the function name from a call expression or identifier value node.
func extractCalleeName(valueNode *tree_sitter.Node, content []byte) string {
	switch valueNode.Kind() {
	case "call_expression":
		fn := valueNode.ChildByFieldName("function")
		if fn == nil {
			return ""
		}
		if fn.Kind() == "identifier" {
			return fn.Utf8Text(content)
		}
		if fn.Kind() == "member_expression" {
			prop := fn.ChildByFieldName("property")
			if prop != nil {
				return prop.Utf8Text(content)
			}
		}
	case "await_expression":
		// const { data } = await useQuery()
		for i := uint(0); i < valueNode.ChildCount(); i++ {
			child := valueNode.Child(i)
			if child.IsNamed() {
				return extractCalleeName(child, content)
			}
		}
	case "identifier":
		return valueNode.Utf8Text(content)
	}
	return ""
}
