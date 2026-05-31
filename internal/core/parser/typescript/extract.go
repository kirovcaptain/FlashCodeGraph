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
	commanderParentMap := map[string]string{} // top-level commander parent tracking

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
			extractTSPendingAssignment(node, content, "", result)
			extractArrowFunctions(node, content, file.RelPath, "", result)
			// Commander: collect top-level parent assignments
			if detectCliFramework(result) == "commander" {
				collectCommanderParent(node, content, commanderParentMap)
			}
			return false
		case "expression_statement":
			// Handle top-level MCP tool registration: server.tool("name", ...)
			extractTopLevelMCPTool(node, content, file.RelPath, result)
			// Handle top-level commander: program.command("x").action(handler)
			if detectCliFramework(result) == "commander" {
				extractTopLevelCommanderRoute(node, content, file.RelPath, commanderParentMap, result)
			}
			// Handle top-level yargs: yargs.command("name", ...)
			if detectCliFramework(result) == "yargs" {
				extractTopLevelYargsRoute(node, content, file.RelPath, result)
			}
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
func extractAmbientReturnTypes(returnTypeNode *tree_sitter.Node, content []byte) []model.ReturnType {
	// type_annotation children: ":" + actual_type_node
	// If actual type is tuple_type, extract each element as a separate return type
	for i := uint(0); i < returnTypeNode.ChildCount(); i++ {
		child := returnTypeNode.Child(i)
		if child.Kind() == "tuple_type" {
			// [UserService, OrderService] → [{Name:"UserService"}, {Name:"OrderService"}]
			var types []model.ReturnType
			for j := uint(0); j < child.ChildCount(); j++ {
				elem := child.Child(j)
				if elem.IsNamed() {
					types = append(types, extractReturnTypeStructured(elem, content))
				}
			}
			return types
		}
	}
	// Non-tuple: single return type (e.g. ": QueryResult")
	return []model.ReturnType{extractReturnTypeStructured(returnTypeNode, content)}
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
			var returnTypes []model.ReturnType
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
						TypeArgs: heritageTypeArgs,
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
						TypeArgs: interfaceTypeArgs,
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

				// Produce TypeHints for explicitly typed arrow function parameters
				for _, param := range paramTypes {
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

	// Detect CLI framework for route extraction
	cliFramework := detectCliFramework(result)
	commanderParentMap := map[string]string{} // variableName → commandName

	lambdaCounter := 0                    // method-body-level counter shared across all call sites
	lambdaMap := make(map[uintptr]string) // node.Id() → lambdaQualifiedName for route handler resolution
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
			// Commander: collect parent command assignments (const group = program.command("group"))
			if cliFramework == "commander" {
				collectCommanderParent(node, content, commanderParentMap)
			}
		}
		if node.Kind() == "call_expression" {
			// 1. Detect if this is a route registration
			routeDetection := DetectTSRoute(node, content)

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

			// 2. Parse arguments (creates lambdas, builds lambdaMap)
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
						if _, exists := lambdaMap[child.Id()]; exists {
							argCount++
							argExprs = append(argExprs, "")
							continue
						}
						argCount++
						argExprs = append(argExprs, "")
						lambdaCounter++
						lambdaName := fmt.Sprintf("lambda$%d", lambdaCounter)
						lambdaQualifiedName := qualifiedCallerName + "." + lambdaName
						lambdaID := astutil.GenerateSymbolID(filePath, lambdaQualifiedName, int(child.StartPosition().Row)+1)
						lambdaParams := extractArrowFunctionParams(child, content)
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
							Params:        lambdaParams,
						})
						// Produce TypeHints for explicitly typed arrow function parameters
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
						// Record lambda → enclosing block scope parent relationship
						blockScope := astutil.DetectBlockScope(child, qualifiedCallerName)
						if blockScope.ScopeKey != qualifiedCallerName {
							if result.ScopeParents == nil {
								result.ScopeParents = make(map[string]string)
							}
							result.ScopeParents[lambdaQualifiedName] = blockScope.ScopeKey
							mergeScopeParents(result, blockScope.ScopeParents)
						}
						result.Calls = append(result.Calls, model.RawCall{
							CalledName:          lambdaQualifiedName,
							CallerName:          qualifiedCallerName,
							CallerKind:          constants.KindFunction,
							FilePath:            filePath,
							Line:                int(child.StartPosition().Row) + 1,
							IsPreResolved:       true,
							LambdaOwnerMethod:   calledName,
							LambdaOwnerReceiver: receiverExpr,
						})
						// Record in lambdaMap for route handler resolution
						lambdaMap[child.Id()] = lambdaQualifiedName
						lambdaBody := child.ChildByFieldName("body")
						if lambdaBody != nil {
							if lambdaBody.Kind() == "statement_block" {
								extractCalls(lambdaBody, content, filePath, lambdaQualifiedName, result)
							} else {
								extractCalls(child, content, filePath, lambdaQualifiedName, result)
							}
						}
					} else {
						argCount++
						argExprs = append(argExprs, child.Utf8Text(content))
						// Scan nested arrow_functions inside call_expression args (e.g. auth((req,res)=>{...}))
						// so they are registered in lambdaMap before route extraction
						astutil.WalkNamedChildren(child, func(nested *tree_sitter.Node) bool {
							if nested.Kind() == "arrow_function" {
								lambdaCounter++
								nestedLambdaName := fmt.Sprintf("lambda$%d", lambdaCounter)
								nestedLambdaQualifiedName := qualifiedCallerName + "." + nestedLambdaName
								nestedLambdaID := astutil.GenerateSymbolID(filePath, nestedLambdaQualifiedName, int(nested.StartPosition().Row)+1)
								nestedLambdaParams := extractArrowFunctionParams(nested, content)
								result.Symbols = append(result.Symbols, model.Symbol{
									ID:            nestedLambdaID,
									Name:          nestedLambdaName,
									QualifiedName: nestedLambdaQualifiedName,
									Kind:          constants.KindFunction,
									FilePath:      filePath,
									StartLine:     int(nested.StartPosition().Row) + 1,
									EndLine:       int(nested.EndPosition().Row) + 1,
									IsLambda:      true,
									LambdaContext: qualifiedCallerName,
									Params:        nestedLambdaParams,
								})
								for _, param := range nestedLambdaParams {
									if param.Type != "" && param.Name != "" {
										result.TypeHints = append(result.TypeHints, model.TypeBinding{
											VarName:  param.Name,
											TypeName: param.Type,
											Tier:     0,
											Scope:    nestedLambdaQualifiedName,
											FilePath: filePath,
										})
									}
								}
								// Record nested lambda → enclosing block scope parent relationship
								nestedBlockScope := astutil.DetectBlockScope(nested, qualifiedCallerName)
								if nestedBlockScope.ScopeKey != qualifiedCallerName {
									if result.ScopeParents == nil {
										result.ScopeParents = make(map[string]string)
									}
									result.ScopeParents[nestedLambdaQualifiedName] = nestedBlockScope.ScopeKey
									mergeScopeParents(result, nestedBlockScope.ScopeParents)
								}
								result.Calls = append(result.Calls, model.RawCall{
									CalledName:          nestedLambdaQualifiedName,
									CallerName:          qualifiedCallerName,
									CallerKind:          constants.KindFunction,
									FilePath:            filePath,
									Line:                int(nested.StartPosition().Row) + 1,
									IsPreResolved:       true,
									LambdaOwnerMethod:   calledName,
									LambdaOwnerReceiver: receiverExpr,
								})
								lambdaMap[nested.Id()] = nestedLambdaQualifiedName
								nestedBody := nested.ChildByFieldName("body")
								if nestedBody != nil {
									if nestedBody.Kind() == "statement_block" {
										extractCalls(nestedBody, content, filePath, nestedLambdaQualifiedName, result)
									} else {
										extractCalls(nested, content, filePath, nestedLambdaQualifiedName, result)
									}
								}
								return false
							}
							return true
						})
					}
				}
			}

			// 3. Route extraction (AFTER lambda creation)
			if routeDetection != nil {
				handlers := ResolveTSHandlerArgs(routeDetection.ArgsNode, content, lambdaMap)
				if len(handlers) > 0 {
					result.Routes = append(result.Routes, model.RawRoute{
						Method:      routeDetection.Method,
						PathPattern: routeDetection.PathPattern,
						Handlers:    handlers,
						Framework:   "express",
						FilePath:    filePath,
						Line:        int(node.StartPosition().Row) + 1,
					})
				}
			}

			// 4. MCP tool registration: server.tool("name", ..., handler)
			if calledName == "tool" && receiverExpr != "" && detectMcpFramework(result) != "" {
				extractTSMCPToolRoute(node, content, filePath, argExprs, lambdaMap, result)
			}

			// 5. CLI framework route extraction
			if cliFramework == "commander" && calledName == "action" {
				extractCommanderRoute(node, content, filePath, commanderParentMap, lambdaMap, result)
			}
			if cliFramework == "yargs" && calledName == "command" && receiverExpr != "" {
				extractYargsRoute(node, content, filePath, lambdaMap, result)
			}

			if calledName != "" {
				flowContext := astutil.DetectFlowContext(node, content)
				blockScope := astutil.DetectBlockScope(node, qualifiedCallerName)
				mergeScopeParents(result, blockScope.ScopeParents)
				chainID, chainDepth := computeTSChainInfo(node)
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
					ChainID:      chainID,
					ChainDepth:   chainDepth,
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
			// Infer type from default value new_expression when no type annotation exists
			if paramType == "" {
				valueNode := param.ChildByFieldName("value")
				if valueNode != nil && valueNode.Kind() == "new_expression" {
					constructorNode := valueNode.ChildByFieldName("constructor")
					if constructorNode != nil && constructorNode.Kind() == "identifier" {
						paramType = constructorNode.Utf8Text(content)
					}
				}
			}
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

// extractArrowFunctionParams extracts parameter information from an arrow_function node.
// Handles both parenthesized and non-parenthesized forms:
// - (order: Order) => ... → [{Name:"order", Type:"Order"}]
// - order => ... → [{Name:"order", Type:""}]
// - (order) => ... → [{Name:"order", Type:""}]
func extractArrowFunctionParams(arrowNode *tree_sitter.Node, content []byte) []model.ParamInfo {
	// Try standard extractParams first (handles formal_parameters case)
	params := extractParams(arrowNode, content)
	if len(params) > 0 {
		return params
	}
	// Fallback: single identifier without parentheses (order => ...)
	firstNamed := arrowNode.NamedChild(0)
	if firstNamed != nil && firstNamed.Kind() == "identifier" {
		return []model.ParamInfo{{Name: firstNamed.Utf8Text(content), Type: ""}}
	}
	return nil
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

func extractReturnTypes(node *tree_sitter.Node, content []byte) []model.ReturnType {
	returnTypeNode := node.ChildByFieldName("return_type")
	if returnTypeNode == nil {
		return nil
	}
	return []model.ReturnType{extractReturnTypeStructured(returnTypeNode, content)}
}

// extractReturnTypeStructured extracts a return type as a structured ReturnType with generic args.
func extractReturnTypeStructured(node *tree_sitter.Node, content []byte) model.ReturnType {
	// type_annotation wraps the actual type: ": Type"
	if node.Kind() == "type_annotation" {
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child.IsNamed() {
				return extractReturnTypeStructured(child, content)
			}
		}
	}
	if node.Kind() == "generic_type" {
		nameNode := node.ChildByFieldName("name")
		baseName := ""
		if nameNode != nil {
			baseName = nameNode.Utf8Text(content)
		}
		args := extractTypeArgsFromNode(astutil.FindChildByKind(node, "type_arguments"), content)
		return model.ReturnType{Name: baseName, Args: args}
	}
	if node.Kind() == "array_type" {
		// tree-sitter AST: array_type → [type_identifier, "[]"]
		// Use NamedChild(0) to skip punctuation nodes
		elementNode := node.NamedChild(0)
		if elementNode != nil {
			elementType := extractReturnTypeStructured(elementNode, content)
			return model.ReturnType{Name: "Array", Args: []model.TypeArg{{Name: elementType.Name, Args: elementType.Args}}}
		}
	}
	return model.ReturnType{Name: extractTypeName(node, content)}
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

// computeTSChainInfo determines the chain position of a call_expression node in TS/JS.
// TS chain structure: call_expression → function(member_expression) → object(call_expression)
// Returns (chainID, chainDepth). chainID is the outermost call's line number (1-based).
// Returns (0, 0) if the node is not part of a chain.
func computeTSChainInfo(node *tree_sitter.Node) (int, int) {
	// Check if this call has an inner chained call
	funcNode := node.ChildByFieldName("function")
	hasInnerChainedCall := false
	if funcNode != nil && funcNode.Kind() == "member_expression" {
		objectNode := funcNode.ChildByFieldName("object")
		if objectNode != nil && objectNode.Kind() == "call_expression" {
			hasInnerChainedCall = true
		}
	}

	// Check if this call is the inner part of an outer chain
	hasOuterChainedCall := false
	parent := node.Parent()
	if parent != nil && parent.Kind() == "member_expression" {
		grandparent := parent.Parent()
		if grandparent != nil && grandparent.Kind() == "call_expression" {
			grandparentFunc := grandparent.ChildByFieldName("function")
			if grandparentFunc != nil && sameTSASTNode(grandparentFunc, parent) {
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
		if outermostParent == nil || outermostParent.Kind() != "member_expression" {
			break
		}
		grandparent := outermostParent.Parent()
		if grandparent == nil || grandparent.Kind() != "call_expression" {
			break
		}
		grandparentFunc := grandparent.ChildByFieldName("function")
		if grandparentFunc == nil || !sameTSASTNode(grandparentFunc, outermostParent) {
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
		if currentFunc == nil || currentFunc.Kind() != "member_expression" {
			break
		}
		inner := currentFunc.ChildByFieldName("object")
		if inner == nil || inner.Kind() != "call_expression" {
			break
		}
		totalDepth++
		current = inner
	}

	// Count distance from outermost to current node
	distanceFromOutermost := 0
	current = outermost
	for !sameTSASTNode(current, node) {
		currentFunc := current.ChildByFieldName("function")
		if currentFunc == nil || currentFunc.Kind() != "member_expression" {
			break
		}
		inner := currentFunc.ChildByFieldName("object")
		if inner == nil || inner.Kind() != "call_expression" {
			break
		}
		current = inner
		distanceFromOutermost++
	}

	chainDepth := totalDepth - distanceFromOutermost
	return chainID, chainDepth
}

// sameTSASTNode compares two tree-sitter nodes by byte range and kind (pointer equality is unreliable).
func sameTSASTNode(a, b *tree_sitter.Node) bool {
	if a == nil || b == nil {
		return false
	}
	return a.StartByte() == b.StartByte() && a.EndByte() == b.EndByte() && a.Kind() == b.Kind()
}

// detectCliFramework returns the CLI framework name based on imports.
func detectCliFramework(result *model.ParseResult) string {
	for _, imp := range result.Imports {
		if imp.ModulePath == "commander" {
			return "commander"
		}
		if strings.Contains(imp.ModulePath, "yargs") {
			return "yargs"
		}
	}
	return ""
}

// detectMcpFramework returns the MCP framework name based on imports.
func detectMcpFramework(result *model.ParseResult) string {
	for _, imp := range result.Imports {
		if strings.Contains(imp.ModulePath, "modelcontextprotocol") || strings.Contains(imp.ModulePath, "/mcp") {
			return "mcp-sdk"
		}
	}
	return ""
}

// extractTSMCPToolRoute extracts MCP tool route from server.tool("name", ..., handler) calls.
func extractTSMCPToolRoute(node *tree_sitter.Node, content []byte, filePath string, argExprs []string, lambdaMap map[uintptr]string, result *model.ParseResult) {
	argsNode := node.ChildByFieldName("arguments")
	if argsNode == nil {
		return
	}

	// Collect named argument nodes
	var namedArgNodes []*tree_sitter.Node
	for i := uint(0); i < argsNode.ChildCount(); i++ {
		child := argsNode.Child(i)
		if child.IsNamed() {
			namedArgNodes = append(namedArgNodes, child)
		}
	}
	if len(namedArgNodes) < 2 {
		return
	}

	// First arg must be a string literal (tool name)
	firstArg := namedArgNodes[0]
	if firstArg.Kind() != "string" && firstArg.Kind() != "template_string" {
		return
	}
	toolName := strings.Trim(firstArg.Utf8Text(content), "\"'`")
	if toolName == "" {
		return
	}

	// Handler is the last argument — could be arrow_function (in lambdaMap) or identifier
	lastArg := namedArgNodes[len(namedArgNodes)-1]
	handlerName := ""
	switch lastArg.Kind() {
	case "arrow_function":
		if lambdaMap != nil {
			if qualifiedName, exists := lambdaMap[lastArg.Id()]; exists {
				handlerName = qualifiedName
			}
		}
		// For top-level or unresolved arrow functions, use tool name as handler
		if handlerName == "" {
			handlerName = toolName
		}
	case "identifier":
		handlerName = lastArg.Utf8Text(content)
	case "member_expression":
		propNode := lastArg.ChildByFieldName("property")
		if propNode != nil {
			handlerName = propNode.Utf8Text(content)
		}
	}
	if handlerName == "" {
		return
	}

	result.Routes = append(result.Routes, model.RawRoute{
		Method:      "TOOL",
		PathPattern: toolName,
		Handlers:    []string{handlerName},
		Framework:   "mcp",
		FilePath:    filePath,
		Line:        int(node.StartPosition().Row) + 1,
	})
}

// extractTopLevelMCPTool handles top-level expression_statement containing server.tool(...) calls.
func extractTopLevelMCPTool(node *tree_sitter.Node, content []byte, filePath string, result *model.ParseResult) {
	if detectMcpFramework(result) == "" {
		return
	}
	// expression_statement → call_expression
	astutil.WalkNamedChildren(node, func(child *tree_sitter.Node) bool {
		if child.Kind() != "call_expression" {
			return true
		}
		funcNode := child.ChildByFieldName("function")
		if funcNode == nil || funcNode.Kind() != "member_expression" {
			return true
		}
		propNode := funcNode.ChildByFieldName("property")
		if propNode == nil || propNode.Utf8Text(content) != "tool" {
			return true
		}
		extractTSMCPToolRoute(child, content, filePath, nil, nil, result)
		return false
	})
}

// extractCommanderRoute extracts commander CLI route from .command("name").action(handler) chain.
// Only processes the outermost call in a chain (same guard as ExtractChainedRoutes).
func extractCommanderRoute(node *tree_sitter.Node, content []byte, filePath string, commanderParentMap map[string]string, lambdaMap map[uintptr]string, result *model.ParseResult) {
	// Only process outermost call
	parent := node.Parent()
	if parent != nil && parent.Kind() == "member_expression" {
		grandparent := parent.Parent()
		if grandparent != nil && grandparent.Kind() == "call_expression" {
			return
		}
	}

	// Find .action() in the chain — it must be the outermost method
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil || funcNode.Kind() != "member_expression" {
		return
	}
	propNode := funcNode.ChildByFieldName("property")
	if propNode == nil || propNode.Utf8Text(content) != "action" {
		return
	}

	// Extract handler from .action() arguments
	handlerName := ""
	argsNode := node.ChildByFieldName("arguments")
	if argsNode != nil {
		for i := uint(0); i < argsNode.ChildCount(); i++ {
			argChild := argsNode.Child(i)
			if !argChild.IsNamed() {
				continue
			}
			switch argChild.Kind() {
			case "identifier":
				handlerName = argChild.Utf8Text(content)
			case "arrow_function":
				if qualifiedName, exists := lambdaMap[argChild.Id()]; exists {
					handlerName = qualifiedName
				} else {
					handlerName = "" // will use command name as fallback
				}
			case "call_expression":
				innerArgs := argChild.ChildByFieldName("arguments")
				if innerArgs != nil {
					for j := uint(0); j < innerArgs.ChildCount(); j++ {
						innerArg := innerArgs.Child(j)
						if innerArg.IsNamed() && innerArg.Kind() == "string" {
							handlerName = strings.Trim(innerArg.Utf8Text(content), "\"'`")
							break
						}
					}
				}
			}
			break // only first argument
		}
	}

	// Walk chain inward to find .command("name")
	commandName := ""
	receiverName := ""
	current := funcNode.ChildByFieldName("object") // object of .action()
	for current != nil && current.Kind() == "call_expression" {
		innerFunc := current.ChildByFieldName("function")
		if innerFunc == nil {
			break
		}
		if innerFunc.Kind() == "member_expression" {
			innerProp := innerFunc.ChildByFieldName("property")
			if innerProp != nil && innerProp.Utf8Text(content) == "command" {
				// Found .command("name") — extract name
				innerArgs := current.ChildByFieldName("arguments")
				if innerArgs != nil {
					for j := uint(0); j < innerArgs.ChildCount(); j++ {
						argChild := innerArgs.Child(j)
						if argChild.IsNamed() && argChild.Kind() == "string" {
							rawName := strings.Trim(argChild.Utf8Text(content), "\"'`")
							if spaceIndex := strings.IndexByte(rawName, ' '); spaceIndex > 0 {
								commandName = rawName[:spaceIndex]
							} else {
								commandName = rawName
							}
							break
						}
					}
				}
				// Get the receiver of .command() for parent lookup
				innerObj := innerFunc.ChildByFieldName("object")
				if innerObj != nil && innerObj.Kind() == "identifier" {
					receiverName = innerObj.Utf8Text(content)
				}
				break
			}
			// Continue walking inward (skip .description(), .option(), etc.)
			current = innerFunc.ChildByFieldName("object")
		} else {
			break
		}
	}

	if commandName == "" {
		return
	}
	if handlerName == "" {
		handlerName = commandName // fallback
	}

	// Resolve parent prefix
	fullPath := commandName
	if parentCommandName, exists := commanderParentMap[receiverName]; exists {
		fullPath = parentCommandName + " " + commandName
	}

	result.Routes = append(result.Routes, model.RawRoute{
		Method:      "CLI",
		PathPattern: fullPath,
		Handlers:    []string{handlerName},
		Framework:   "commander",
		FilePath:    filePath,
		Line:        int(node.StartPosition().Row) + 1,
	})
}

// collectCommanderParent detects `const group = xxx.command("group")` pattern for parent tracking.
func collectCommanderParent(node *tree_sitter.Node, content []byte, commanderParentMap map[string]string) {
	// lexical_declaration → variable_declarator → name + value
	for i := uint(0); i < node.NamedChildCount(); i++ {
		declarator := node.NamedChild(i)
		if declarator.Kind() != "variable_declarator" {
			continue
		}
		nameNode := declarator.ChildByFieldName("name")
		valueNode := declarator.ChildByFieldName("value")
		if nameNode == nil || valueNode == nil || nameNode.Kind() != "identifier" {
			continue
		}
		variableName := nameNode.Utf8Text(content)

		// Walk the chain to find .command("name") without .action()
		hasAction := false
		commandName := ""
		current := valueNode
		for current != nil && current.Kind() == "call_expression" {
			funcNode := current.ChildByFieldName("function")
			if funcNode == nil || funcNode.Kind() != "member_expression" {
				break
			}
			propNode := funcNode.ChildByFieldName("property")
			if propNode == nil {
				break
			}
			methodName := propNode.Utf8Text(content)
			if methodName == "action" {
				hasAction = true
				break
			}
			if methodName == "command" && commandName == "" {
				argsNode := current.ChildByFieldName("arguments")
				if argsNode != nil {
					for j := uint(0); j < argsNode.ChildCount(); j++ {
						argChild := argsNode.Child(j)
						if argChild.IsNamed() && argChild.Kind() == "string" {
							rawName := strings.Trim(argChild.Utf8Text(content), "\"'`")
							if spaceIndex := strings.IndexByte(rawName, ' '); spaceIndex > 0 {
								commandName = rawName[:spaceIndex]
							} else {
								commandName = rawName
							}
							break
						}
					}
				}
			}
			current = funcNode.ChildByFieldName("object")
		}

		// Only record if has .command() but no .action() (parent command, not leaf)
		if commandName != "" && !hasAction {
			commanderParentMap[variableName] = commandName
		}
	}
}

// extractYargsRoute extracts yargs CLI route from .command("name", ..., handler) or .command({command, handler}).
func extractYargsRoute(node *tree_sitter.Node, content []byte, filePath string, lambdaMap map[uintptr]string, result *model.ParseResult) {
	argsNode := node.ChildByFieldName("arguments")
	if argsNode == nil {
		return
	}

	var namedArgNodes []*tree_sitter.Node
	for i := uint(0); i < argsNode.ChildCount(); i++ {
		child := argsNode.Child(i)
		if child.IsNamed() {
			namedArgNodes = append(namedArgNodes, child)
		}
	}
	if len(namedArgNodes) == 0 {
		return
	}

	commandName := ""
	handlerName := ""

	// Object mode: yargs.command({command: "name", handler: fn})
	if len(namedArgNodes) == 1 && namedArgNodes[0].Kind() == "object" {
		objectNode := namedArgNodes[0]
		for i := uint(0); i < objectNode.NamedChildCount(); i++ {
			prop := objectNode.NamedChild(i)
			if prop.Kind() != "pair" {
				continue
			}
			keyNode := prop.ChildByFieldName("key")
			valueNode := prop.ChildByFieldName("value")
			if keyNode == nil || valueNode == nil {
				continue
			}
			keyText := keyNode.Utf8Text(content)
			switch keyText {
			case "command":
				commandName = strings.Trim(valueNode.Utf8Text(content), "\"'`")
			case "handler":
				if valueNode.Kind() == "identifier" {
					handlerName = valueNode.Utf8Text(content)
				} else if valueNode.Kind() == "arrow_function" {
					if qualifiedName, exists := lambdaMap[valueNode.Id()]; exists {
						handlerName = qualifiedName
					}
				}
			}
		}
	} else if len(namedArgNodes) >= 2 {
		// Positional mode: first string = name, last function/identifier = handler
		firstArg := namedArgNodes[0]
		if firstArg.Kind() == "string" {
			rawName := strings.Trim(firstArg.Utf8Text(content), "\"'`")
			if spaceIndex := strings.IndexByte(rawName, ' '); spaceIndex > 0 {
				commandName = rawName[:spaceIndex]
			} else {
				commandName = rawName
			}
		}
		// Last function-like argument is handler
		for j := len(namedArgNodes) - 1; j >= 1; j-- {
			lastArg := namedArgNodes[j]
			switch lastArg.Kind() {
			case "arrow_function":
				if qualifiedName, exists := lambdaMap[lastArg.Id()]; exists {
					handlerName = qualifiedName
				} else {
					handlerName = commandName // fallback
				}
			case "identifier":
				handlerName = lastArg.Utf8Text(content)
			}
			if handlerName != "" {
				break
			}
		}
	}

	if commandName == "" {
		return
	}
	if handlerName == "" {
		handlerName = commandName
	}

	result.Routes = append(result.Routes, model.RawRoute{
		Method:      "CLI",
		PathPattern: commandName,
		Handlers:    []string{handlerName},
		Framework:   "yargs",
		FilePath:    filePath,
		Line:        int(node.StartPosition().Row) + 1,
	})
}

// extractTopLevelCommanderRoute handles top-level expression_statement containing .command().action() chains.
func extractTopLevelCommanderRoute(node *tree_sitter.Node, content []byte, filePath string, commanderParentMap map[string]string, result *model.ParseResult) {
	astutil.WalkNamedChildren(node, func(child *tree_sitter.Node) bool {
		if child.Kind() != "call_expression" {
			return true
		}
		funcNode := child.ChildByFieldName("function")
		if funcNode == nil || funcNode.Kind() != "member_expression" {
			return true
		}
		propNode := funcNode.ChildByFieldName("property")
		if propNode == nil || propNode.Utf8Text(content) != "action" {
			return true
		}
		extractCommanderRoute(child, content, filePath, commanderParentMap, nil, result)
		return false
	})
}

// extractTopLevelYargsRoute handles top-level yargs.command(...) calls.
func extractTopLevelYargsRoute(node *tree_sitter.Node, content []byte, filePath string, result *model.ParseResult) {
	astutil.WalkNamedChildren(node, func(child *tree_sitter.Node) bool {
		if child.Kind() != "call_expression" {
			return true
		}
		funcNode := child.ChildByFieldName("function")
		if funcNode == nil || funcNode.Kind() != "member_expression" {
			return true
		}
		propNode := funcNode.ChildByFieldName("property")
		if propNode == nil || propNode.Utf8Text(content) != "command" {
			return true
		}
		extractYargsRoute(child, content, filePath, nil, result)
		return false
	})
}
