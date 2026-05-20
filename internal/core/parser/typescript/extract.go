package typescript

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
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
		case "lexical_declaration", "variable_declaration":
			extractArrowFunctions(node, content, file.RelPath, result)
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
			extractArrowFunctions(child, content, file.RelPath, result)
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
				for j := uint(0); j < clause.ChildCount(); j++ {
					typeNode := clause.Child(j)
					if typeNode.IsNamed() && typeNode.Kind() != "type_arguments" {
						parentName := extractTypeName(typeNode, content)
						if parentName != "" {
							result.Heritage = append(result.Heritage, model.RawHeritage{
								ChildName: className, ChildQualified: qualifiedName,
								ParentName: parentName,
								Kind: "extends", FilePath: file.RelPath,
							})
						}
					}
				}
			case "implements_clause":
				for j := uint(0); j < clause.ChildCount(); j++ {
					typeNode := clause.Child(j)
					if typeNode.IsNamed() && typeNode.Kind() != "type_arguments" {
						ifaceName := extractTypeName(typeNode, content)
						if ifaceName != "" {
							result.Heritage = append(result.Heritage, model.RawHeritage{
								ChildName: className, ChildQualified: qualifiedName,
								ParentName: ifaceName,
								Kind: "implements", FilePath: file.RelPath,
							})
						}
					}
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
						Kind: kind, FilePath: file.RelPath,
					})
				}
			}
		}
		_ = sawExtends
	}

	result.Symbols = append(result.Symbols, model.Symbol{
		ID:            astutil.GenerateSymbolID(file.RelPath, qualifiedName, int(node.StartPosition().Row)+1),
		Name:          className,
		QualifiedName: qualifiedName,
		Kind:          constants.KindClass,
		ClassType:     classType,
		FilePath:      file.RelPath,
		StartLine:     int(node.StartPosition().Row) + 1,
		EndLine:       int(node.EndPosition().Row) + 1,
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
	isGetter := false  // TypeScript `get xxx()` accessor
	isSetter := false  // TypeScript `set xxx(v)` accessor
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
		IsAsync:       isAsync,
		IsExported:    isExported,
		Complexity:    complexity,
	})
}

// extractTSVariableDeclaration handles const/let/var with arrow functions.
func extractArrowFunctions(node *tree_sitter.Node, content []byte, filePath string, result *model.ParseResult) {
	astutil.WalkNamedChildren(node, func(child *tree_sitter.Node) bool {
		if child.Kind() == "variable_declarator" {
			nameNode := child.ChildByFieldName("name")
			valueNode := child.ChildByFieldName("value")
			if nameNode != nil && valueNode != nil && valueNode.Kind() == "arrow_function" {
				funcName := nameNode.Utf8Text(content)
				returnTypes := extractReturnTypes(valueNode, content)
				paramTypes := extractParams(valueNode, content)

				qualifiedName := buildQualifiedName(filePath, funcName)
				result.Symbols = append(result.Symbols, model.Symbol{
					ID:            astutil.GenerateSymbolID(filePath, funcName, int(child.StartPosition().Row)+1),
					Name:          funcName,
					QualifiedName: qualifiedName,
					Kind:          constants.KindFunction,
					FilePath:      filePath,
					StartLine:     int(child.StartPosition().Row) + 1,
					EndLine:       int(child.EndPosition().Row) + 1,
					Params:        paramTypes,
					ReturnTypes:   returnTypes,
					IsLambda:      true,
					LambdaContext: funcName,
				})

				body := valueNode.ChildByFieldName("body")
				if body != nil {
					extractCalls(body, content, filePath, qualifiedName, result)
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

	astutil.WalkNamedChildren(body, func(node *tree_sitter.Node) bool {
		if node.Kind() == "lexical_declaration" || node.Kind() == "variable_declaration" {
			extractTSPendingAssignment(node, content, qualifiedCallerName, result)
			// Also extract const arrow functions defined inside function bodies
			// (e.g. const ensureSlash = (path: string) => ...)
			extractArrowFunctions(node, content, filePath, result)
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
					CallerName:   qualifiedCallerName,
					CallerKind:   constants.KindFunction,
					FilePath:     filePath,
					Line:         int(node.StartPosition().Row) + 1,
					ArgCount:     argCount,
					ReceiverExpr: receiverExpr,
					FlowContext:  fc.Kind,
					FlowLine:     fc.Line,
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
			params = append(params, entry)
		}
	}
	return params
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

// extractTSPendingAssignment extracts assignment patterns for fixpoint type propagation.
func extractTSPendingAssignment(node *tree_sitter.Node, content []byte, scope string, result *model.ParseResult) {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() != "variable_declarator" {
			continue
		}
		nameNode := child.ChildByFieldName("name")
		valueNode := child.ChildByFieldName("value")
		if nameNode == nil || valueNode == nil || nameNode.Kind() != "identifier" {
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
					Scope:    scope,
					FilePath: result.FilePath,
				})
			}
		}

		switch valueNode.Kind() {
		case "identifier":
			result.PendingAssignments = append(result.PendingAssignments, model.PendingAssignment{
				Kind: "copy", LHS: lhs, Scope: scope, RHS: valueNode.Utf8Text(content),
			})
		case "member_expression":
			obj := valueNode.ChildByFieldName("object")
			prop := valueNode.ChildByFieldName("property")
			if obj != nil && prop != nil && obj.Kind() == "identifier" {
				result.PendingAssignments = append(result.PendingAssignments, model.PendingAssignment{
					Kind: "field_access", LHS: lhs, Scope: scope, Receiver: obj.Utf8Text(content), Field: prop.Utf8Text(content),
				})
			}
		case "call_expression":
			fn := valueNode.ChildByFieldName("function")
			if fn == nil {
				continue
			}
			if fn.Kind() == "identifier" {
				result.PendingAssignments = append(result.PendingAssignments, model.PendingAssignment{
					Kind: "call_result", LHS: lhs, Scope: scope, Callee: fn.Utf8Text(content),
				})
			} else if fn.Kind() == "member_expression" {
				obj := fn.ChildByFieldName("object")
				prop := fn.ChildByFieldName("property")
				if obj != nil && prop != nil && obj.Kind() == "identifier" {
					result.PendingAssignments = append(result.PendingAssignments, model.PendingAssignment{
						Kind: "method_call_result", LHS: lhs, Scope: scope, Receiver: obj.Utf8Text(content), Method: prop.Utf8Text(content),
					})
				}
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
			// addNode: (node: string) => void — function-typed property
			typeNode := child.ChildByFieldName("type")
			if typeNode == nil || !strings.Contains(typeNode.Utf8Text(content), "=>") {
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
