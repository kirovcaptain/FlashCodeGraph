package java

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

// extractJava extracts symbols from Java/Kotlin AST.
func Extract(rootNode *tree_sitter.Node, content []byte, file scanner.ScannedFile, result *model.ParseResult) {
	packageName := ""
	var currentClass string

	astutil.WalkNamedChildren(rootNode, func(node *tree_sitter.Node) bool {
		switch node.Kind() {
		case "package_declaration":
			packageName = extractPackageName(node, content)
			return false

		case "import_declaration":
			extractImport(node, content, file.RelPath, result)
			return false

		case "class_declaration", "interface_declaration", "enum_declaration":
			extractClass(node, content, file.RelPath, packageName, result)
			className := astutil.NodeFieldText(node, "name", content)
			currentClass = className
			var classType string
			switch node.Kind() {
			case "interface_declaration":
				classType = constants.ClassTypeInterface
			case "enum_declaration":
				classType = constants.ClassTypeEnum
			default:
				classType = constants.ClassTypeClass
			}
			// Extract class annotations for route prefix
			var classAnnotations []model.StructuredAnnotation
			for i := uint(0); i < node.ChildCount(); i++ {
				child := node.Child(i)
				if child.Kind() == "modifiers" {
					classAnnotations = ExtractAnnotations(child, content)
					break
				}
			}
			extractClassBody(node, content, file.RelPath, packageName, className, classType, classAnnotations, result)

			// Feign client detection
			if HasFeignClient(classAnnotations) {
				ExtractFeignClient(classAnnotations, node, content, className, file.RelPath, result)
			}

			currentClass = ""
			return false

		case "method_declaration", "constructor_declaration":
			// Top-level methods (shouldn't happen in Java, but handle gracefully)
			extractMethod(node, content, file.RelPath, packageName, "", nil, result)
			return false
		}
		return true
	})

	_ = currentClass
}

// extractPackageName extracts the package name from a package_declaration.
func extractPackageName(node *tree_sitter.Node, content []byte) string {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "scoped_identifier" || child.Kind() == "identifier" {
			return child.Utf8Text(content)
		}
	}
	return ""
}

// extractJavaImport extracts an import declaration.
func extractImport(node *tree_sitter.Node, content []byte, filePath string, result *model.ParseResult) {
	importPath := ""
	isWildcard := false
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "scoped_identifier" || child.Kind() == "identifier" {
			importPath = child.Utf8Text(content)
		}
		if child.Kind() == "asterisk" {
			isWildcard = true
		}
	}
	if importPath == "" {
		return
	}

	// Extract symbol name (last segment)
	parts := strings.Split(importPath, ".")
	symbolName := parts[len(parts)-1]

	result.Imports = append(result.Imports, model.RawImport{
		ModulePath: importPath,
		SymbolName: symbolName,
		FilePath:   filePath,
		Line:       int(node.StartPosition().Row) + 1,
		IsWildcard: isWildcard,
	})
}

// extractJavaClass extracts a class/interface/enum declaration.
func extractClass(node *tree_sitter.Node, content []byte, filePath, packageName string, result *model.ParseResult) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	className := nameNode.Utf8Text(content)
	qualifiedName := className
	if packageName != "" {
		qualifiedName = packageName + "." + className
	}

	classType := constants.ClassTypeClass
	isAbstract := false
	isExported := false
	var annotations []model.StructuredAnnotation

	switch node.Kind() {
	case "interface_declaration":
		classType = constants.ClassTypeInterface
	case "enum_declaration":
		classType = constants.ClassTypeEnum
	}

	// Extract modifiers and annotations
	modifiers := node.ChildByFieldName("modifiers")
	if modifiers == nil {
		// Try first child
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child.Kind() == "modifiers" {
				modifiers = child
				break
			}
		}
	}
	if modifiers != nil {
		modText := modifiers.Utf8Text(content)
		if strings.Contains(modText, "abstract") {
			isAbstract = true
			classType = constants.ClassTypeAbstract
		}
		if strings.Contains(modText, "public") {
			isExported = true
		}
		annotations = ExtractAnnotations(modifiers, content)
	}

	// Resolve short type name → qualified name using imports and package context
	resolveParentQualified := func(shortName string) string {
		if strings.Contains(shortName, ".") {
			return shortName
		}
		for _, imp := range result.Imports {
			if imp.SymbolName == shortName {
				return imp.ModulePath
			}
		}
		if packageName != "" {
			return packageName + "." + shortName
		}
		return ""
	}

	// Extract heritage (extends/implements)
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Kind() {
		case "superclass":
			parentType := extractTypeFromHeritage(child, content)
			if parentType != "" {
				heritageTypeArgs := extractHeritageTypeArgs(child, content)
				result.Heritage = append(result.Heritage, model.RawHeritage{
					ChildName:       className,
					ChildQualified:  qualifiedName,
					ParentName:      parentType,
					ParentQualified: resolveParentQualified(parentType),
					Kind:            "extends",
					Language:        "java",
					FilePath:        filePath,
					TypeArgs:        heritageTypeArgs,
				})
			}
		case "super_interfaces":
			typeList := child.ChildByFieldName("type_list")
			if typeList == nil {
				for j := uint(0); j < child.ChildCount(); j++ {
					c := child.Child(j)
					if c.Kind() == "type_list" {
						typeList = c
						break
					}
				}
			}
			if typeList != nil {
				for j := uint(0); j < typeList.ChildCount(); j++ {
					iface := typeList.Child(j)
					if iface.IsNamed() {
						ifaceName := ExtractTypeName(iface, content)
						if ifaceName != "" {
							var interfaceTypeArgs []model.TypeArg
							if iface.Kind() == "generic_type" {
								interfaceTypeArgs = ExtractTypeArgs(iface, content)
							}
							result.Heritage = append(result.Heritage, model.RawHeritage{
								ChildName:       className,
								ChildQualified:  qualifiedName,
								ParentName:      ifaceName,
								ParentQualified: resolveParentQualified(ifaceName),
								Kind:            "implements",
								Language:        "java",
								FilePath:        filePath,
								TypeArgs:        interfaceTypeArgs,
							})
						}
					}
				}
			}
		}
	}

	annotationsJSON, _ := json.Marshal(annotations)

	// Extract generic type parameters: class Foo<T, U extends Bar> → ["T", "U"]
	typeParams := extractTypeParams(node, content)

	// Determine node Kind from classType
	nodeKind := constants.KindClass
	if classType == constants.ClassTypeInterface {
		nodeKind = constants.KindInterface
	}

	result.Symbols = append(result.Symbols, model.Symbol{
		ID:            astutil.GenerateSymbolID(filePath, qualifiedName, int(node.StartPosition().Row)+1),
		Name:          className,
		QualifiedName: qualifiedName,
		Kind:          nodeKind,
		FilePath:      filePath,
		StartLine:     int(node.StartPosition().Row) + 1,
		EndLine:       int(node.EndPosition().Row) + 1,
		ClassType:     classType,
		TypeParams:    typeParams,
		IsAbstract:    isAbstract,
		IsExported:    isExported,
		Annotations:   string(annotationsJSON),
	})
}

// extractJavaClassBody walks the class body for methods and fields.
func extractClassBody(classNode *tree_sitter.Node, content []byte, filePath, packageName, className, classType string, classAnnotations []model.StructuredAnnotation, result *model.ParseResult) {
	body := classNode.ChildByFieldName("body")
	if body == nil {
		return
	}

	// Track fields for Lombok accessor generation
	var fields []fieldInfo

	for i := uint(0); i < body.ChildCount(); i++ {
		child := body.Child(i)
		if !child.IsNamed() {
			continue
		}
		switch child.Kind() {
		case "method_declaration", "constructor_declaration":
			extractMethod(child, content, filePath, packageName, className, classAnnotations, result)
		case "field_declaration", "constant_declaration":
			extractField(child, content, filePath, packageName, className, classType, result)
			ExtractDubboReference(child, content, packageName, className, filePath, result)
			ExtractGrpcClientField(child, content, packageName, className, filePath, result)
			// Collect field info for Lombok
			if typeNode := child.ChildByFieldName("type"); typeNode != nil {
				fieldTypeName := ExtractTypeName(typeNode, content)
				for j := uint(0); j < child.ChildCount(); j++ {
					if variableDeclarator := child.Child(j); variableDeclarator.Kind() == "variable_declarator" {
						if nameNode := variableDeclarator.ChildByFieldName("name"); nameNode != nil {
							fields = append(fields, fieldInfo{nameNode.Utf8Text(content), fieldTypeName, int(child.StartPosition().Row) + 1})
						}
					}
				}
			}
		case "enum_constant":
			nameNode := child.ChildByFieldName("name")
			if nameNode == nil {
				continue
			}
			constantName := nameNode.Utf8Text(content)
			qualifiedConstantName := packageName + "." + className + "." + constantName
			result.Symbols = append(result.Symbols, model.Symbol{
				ID:            astutil.GenerateSymbolID(filePath, qualifiedConstantName, int(child.StartPosition().Row)+1),
				Name:          constantName,
				QualifiedName: qualifiedConstantName,
				Kind:          constants.KindVariable,
				FilePath:      filePath,
				StartLine:     int(child.StartPosition().Row) + 1,
				EndLine:       int(child.EndPosition().Row) + 1,
				IsStatic:      true,
				IsFinal:       true,
				IsExported:    true,
			})
		case "class_declaration", "interface_declaration", "enum_declaration":
			// Inner class — recurse
			innerName := astutil.NodeFieldText(child, "name", content)
			if innerName != "" {
				extractClass(child, content, filePath, packageName+"."+className, result)
				var innerAnnotations []model.StructuredAnnotation
				for j := uint(0); j < child.ChildCount(); j++ {
					if child.Child(j).Kind() == "modifiers" {
						innerAnnotations = ExtractAnnotations(child.Child(j), content)
						break
					}
				}
				var innerClassType string
				switch child.Kind() {
				case "interface_declaration":
					innerClassType = constants.ClassTypeInterface
				case "enum_declaration":
					innerClassType = constants.ClassTypeEnum
				default:
					innerClassType = constants.ClassTypeClass
				}
				extractClassBody(child, content, filePath, packageName+"."+className, innerName, innerClassType, innerAnnotations, result)
			}
		case "enum_body_declarations":
			// Enum methods/fields are inside enum_body_declarations (after the semicolon)
			for j := uint(0); j < child.ChildCount(); j++ {
				inner := child.Child(j)
				if !inner.IsNamed() {
					continue
				}
				switch inner.Kind() {
				case "method_declaration", "constructor_declaration":
					extractMethod(inner, content, filePath, packageName, className, classAnnotations, result)
				case "field_declaration":
					extractField(inner, content, filePath, packageName, className, classType, result)
				case "class_declaration", "interface_declaration", "enum_declaration":
					innerName := astutil.NodeFieldText(inner, "name", content)
					if innerName != "" {
						extractClass(inner, content, filePath, packageName+"."+className, result)
						var innerClassType string
						switch inner.Kind() {
						case "interface_declaration":
							innerClassType = constants.ClassTypeInterface
						case "enum_declaration":
							innerClassType = constants.ClassTypeEnum
						default:
							innerClassType = constants.ClassTypeClass
						}
						extractClassBody(inner, content, filePath, packageName+"."+className, innerName, innerClassType, nil, result)
					}
				}
			}
		}
	}

	// Generate Lombok accessor symbols
	generateLombokAccessors(classAnnotations, fields, filePath, packageName, className, result)
}

// extractJavaMethod extracts a method/constructor declaration.
// Also infers accessor status (IsGetter/IsSetter) based on naming pattern, complexity, and body line count.
func extractMethod(node *tree_sitter.Node, content []byte, filePath, packageName, className string, classAnnotations []model.StructuredAnnotation, result *model.ParseResult) {
	nameNode := node.ChildByFieldName("name")
	methodName := ""
	if nameNode != nil {
		methodName = nameNode.Utf8Text(content)
	} else if node.Kind() == "constructor_declaration" {
		methodName = className
	}
	if methodName == "" {
		return
	}

	qualifiedName := methodName
	if className != "" {
		qualifiedName = className + "." + methodName
	}
	if packageName != "" {
		qualifiedName = packageName + "." + qualifiedName
	}

	// Return type
	var returnTypes []model.ReturnType
	typeNode := node.ChildByFieldName("type")
	if typeNode != nil {
		returnTypes = []model.ReturnType{extractReturnType(typeNode, content)}
	}

	// Parameters
	var paramTypes []model.ParamInfo
	paramsNode := node.ChildByFieldName("parameters")
	if paramsNode != nil {
		for j := uint(0); j < paramsNode.ChildCount(); j++ {
			param := paramsNode.Child(j)
			if param.Kind() == "formal_parameter" || param.Kind() == "spread_parameter" {
				paramName := ""
				paramType := ""
				pNameNode := param.ChildByFieldName("name")
				if pNameNode != nil {
					paramName = pNameNode.Utf8Text(content)
				}
				pTypeNode := param.ChildByFieldName("type")
				if pTypeNode != nil {
					paramType = ExtractTypeName(pTypeNode, content)
				}
				// spread_parameter: Object... args → extract from children if field names fail
				if param.Kind() == "spread_parameter" && (paramName == "" || paramType == "") {
					for k := uint(0); k < param.ChildCount(); k++ {
						child := param.Child(k)
						if child.Kind() == "identifier" && paramName == "" {
							paramName = child.Utf8Text(content)
						}
						if (child.Kind() == "type_identifier" || child.Kind() == "generic_type" || child.Kind() == "scoped_type_identifier") && paramType == "" {
							paramType = ExtractTypeName(child, content)
						}
					}
					paramType += "..."
				}
				paramTypes = append(paramTypes, model.ParamInfo{Name: paramName, Type: paramType, TypeArgs: ExtractTypeArgs(pTypeNode, content)})
				// Add type hint for parameter (enables TypeInfer to resolve receiver types)
				if paramType != "" && paramName != "" {
					result.TypeHints = append(result.TypeHints, model.TypeBinding{
						VarName:  paramName,
						TypeName: paramType,
						TypeArgs: ExtractTypeArgs(pTypeNode, content),
						Tier:     0,
						Scope:    qualifiedName,
						FilePath: filePath,
					})
				}
			}
		}
	}
	// Modifiers and annotations
	isStatic := false
	isExported := false
	isAbstract := false
	isConstructor := node.Kind() == "constructor_declaration"
	var annotations []model.StructuredAnnotation

	// Extract method-level generic type parameters: <T, U extends Foo> → ["T", "U"]
	typeParams := extractTypeParams(node, content)

	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "modifiers" {
			modText := child.Utf8Text(content)
			isStatic = strings.Contains(modText, "static")
			isExported = strings.Contains(modText, "public")
			isAbstract = strings.Contains(modText, "abstract")
			annotations = ExtractAnnotations(child, content)
		}
	}
	annotationsJSON, _ := json.Marshal(annotations)

	// Complexity (count branches)
	complexity := 1
	bodyNode := node.ChildByFieldName("body")
	if bodyNode != nil {
		complexity = countComplexity(bodyNode, content)
		// Extract calls from body
		extractCalls(bodyNode, content, filePath, methodName, className, packageName, result)
		// Extract gRPC stub calls
		ExtractGRPCStubCalls(bodyNode, content, qualifiedName, filePath, result)
		// Extract RestTemplate remote calls (needs type env from class fields)
		classQN := className
		if packageName != "" {
			classQN = packageName + "." + className
		}
		typeEnv := buildTypeEnv(result.TypeHints, classQN, result.Imports)
		ExtractRestTemplateCalls(bodyNode, content, qualifiedName, filePath, typeEnv, result)
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
		IsStatic:      isStatic,
		IsExported:    isExported,
		IsConstructor: isConstructor,
		IsAbstract:    isAbstract,
		IsGetter:      isAccessorGetter(methodName, isStatic, len(paramTypes), returnTypes, complexity, node),
		IsSetter:      isAccessorSetter(methodName, isStatic, len(paramTypes), returnTypes, complexity, node),
		Annotations:   string(annotationsJSON),
		Complexity:    complexity,
	})

	// Extract routes from Spring annotations
	ExtractRoutes(annotations, classAnnotations, methodName, className, filePath, int(node.StartPosition().Row)+1, result)

	// Extract GraphQL routes from annotations
	ExtractGraphQLRoutes(annotations, methodName, className, filePath, int(node.StartPosition().Row)+1, result)

	// Extract ORM queries
	ExtractORM(annotations, bodyNode, content, qualifiedName, filePath, int(node.StartPosition().Row)+1, result)
}

// isAccessorGetter checks if a method is a simple getter (getXxx/isXxx with no params, no branches, single-line body).
func isAccessorGetter(name string, isStatic bool, paramCount int, returnTypes []model.ReturnType, complexity int, node *tree_sitter.Node) bool {
	if isStatic || paramCount != 0 || len(returnTypes) == 0 {
		return false
	}
	if !(strings.HasPrefix(name, "get") || strings.HasPrefix(name, "is")) || len(name) <= 3 {
		return false
	}
	if complexity > 1 {
		return false
	}
	bodyLines := int(node.EndPosition().Row) - int(node.StartPosition().Row) - 1
	return bodyLines <= 1
}

// isAccessorSetter checks if a method is a simple setter (setXxx with one param, void return, no branches, single-line body).
func isAccessorSetter(name string, isStatic bool, paramCount int, returnTypes []model.ReturnType, complexity int, node *tree_sitter.Node) bool {
	if isStatic || paramCount != 1 {
		return false
	}
	if !strings.HasPrefix(name, "set") || len(name) <= 3 {
		return false
	}
	if len(returnTypes) > 0 && !(len(returnTypes) == 1 && returnTypes[0].Name == "void") {
		return false
	}
	if complexity > 1 {
		return false
	}
	bodyLines := int(node.EndPosition().Row) - int(node.StartPosition().Row) - 1
	return bodyLines <= 1
}

// extractJavaField extracts a field declaration.
type fieldInfo struct {
	name     string
	typeName string
	line     int
}

// generateLombokAccessors creates synthetic getter/setter symbols for @Data/@Getter/@Setter classes.
func generateLombokAccessors(classAnnotations []model.StructuredAnnotation, fields []fieldInfo, filePath, packageName, className string, result *model.ParseResult) {
	hasGetter := false
	hasSetter := false
	for _, annotation := range classAnnotations {
		if annotation.Name == "Data" {
			hasGetter = true
			hasSetter = true
		}
		if annotation.Name == "Getter" {
			hasGetter = true
		}
		if annotation.Name == "Setter" {
			hasSetter = true
		}
	}
	if !hasGetter && !hasSetter {
		return
	}

	for _, field := range fields {
		prefix := "get"
		if field.typeName == "boolean" {
			prefix = "is"
		}
		capName := strings.ToUpper(field.name[:1]) + field.name[1:]
		qualifiedNameBase := className + "."
		if packageName != "" {
			qualifiedNameBase = packageName + "." + qualifiedNameBase
		}

		if hasGetter {
			getterName := prefix + capName
			result.Symbols = append(result.Symbols, model.Symbol{
				ID:            astutil.GenerateSymbolID(filePath, qualifiedNameBase+getterName, field.line),
				Name:          getterName,
				QualifiedName: qualifiedNameBase + getterName,
				Kind:          constants.KindFunction,
				FilePath:      filePath,
				StartLine:     field.line,
				ReturnTypes:   []model.ReturnType{{Name: field.typeName}},
				Params:        nil,
				IsSynthetic:   true,
				IsGetter:      true,
				IsExported:    true,
			})
		}
		if hasSetter {
			setterName := "set" + capName
			result.Symbols = append(result.Symbols, model.Symbol{
				ID:            astutil.GenerateSymbolID(filePath, qualifiedNameBase+setterName, field.line),
				Name:          setterName,
				QualifiedName: qualifiedNameBase + setterName,
				Kind:          constants.KindFunction,
				FilePath:      filePath,
				StartLine:     field.line,
				Params:        []model.ParamInfo{{Name: field.name, Type: field.typeName}},
				IsSynthetic:   true,
				IsSetter:      true,
				IsExported:    true,
			})
		}
	}
}

func extractField(node *tree_sitter.Node, content []byte, filePath, packageName, className, classType string, result *model.ParseResult) {
	typeNode := node.ChildByFieldName("type")
	fieldType := ""
	if typeNode != nil {
		fieldType = ExtractTypeName(typeNode, content)
	}

	// Interface fields are implicitly public static final
	isStatic := classType == constants.ClassTypeInterface
	isFinal := classType == constants.ClassTypeInterface
	visibility := "package"
	if classType == constants.ClassTypeInterface {
		visibility = "public"
	}
	var fieldAnnotations []model.StructuredAnnotation
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "modifiers" {
			modText := child.Utf8Text(content)
			if strings.Contains(modText, "static") {
				isStatic = true
			}
			if strings.Contains(modText, "final") {
				isFinal = true
			}
			if strings.Contains(modText, "private") {
				visibility = "private"
			} else if strings.Contains(modText, "protected") {
				visibility = "protected"
			} else if strings.Contains(modText, "public") {
				visibility = "public"
			}
			fieldAnnotations = ExtractAnnotations(child, content)
		}
	}

	// Find variable declarators
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "variable_declarator" {
			nameNode := child.ChildByFieldName("name")
			if nameNode == nil {
				continue
			}
			fieldName := nameNode.Utf8Text(content)
			qualifiedName := fieldName
			if className != "" {
				qualifiedName = className + "." + fieldName
			}
			if packageName != "" {
				qualifiedName = packageName + "." + qualifiedName
			}

			// Type hint for TypeEnv (always emit, even for static final)
			if fieldType != "" {
				classQualifiedName := className
				if packageName != "" {
					classQualifiedName = packageName + "." + className
				}
				result.TypeHints = append(result.TypeHints, model.TypeBinding{
					VarName:  fieldName,
					TypeName: fieldType,
					TypeArgs: ExtractTypeArgs(typeNode, content),
					Tier:     0,
					Scope:    classQualifiedName,
					FilePath: filePath,
				})
			}

			// Static final fields emit Variable Symbol for constant tracking
			if isStatic && isFinal {
				result.Symbols = append(result.Symbols, model.Symbol{
					ID:            astutil.GenerateSymbolID(filePath, qualifiedName, int(node.StartPosition().Row)+1),
					Name:          fieldName,
					QualifiedName: qualifiedName,
					Kind:          constants.KindVariable,
					FilePath:      filePath,
					StartLine:     int(node.StartPosition().Row) + 1,
					EndLine:       int(node.EndPosition().Row) + 1,
					IsStatic:      true,
					IsFinal:       true,
					IsExported:    visibility == "public",
					Visibility:    visibility,
				})
			}

			// FieldDeclaration output (skip static final constants)
			if !(isStatic && isFinal) && fieldType != "" {
				ownerQualifiedName := className
				if packageName != "" {
					ownerQualifiedName = packageName + "." + className
				}
				result.Fields = append(result.Fields, model.FieldDeclaration{
					FieldInfo: model.FieldInfo{
						Name:        fieldName,
						Type:        fieldType,
						Visibility:  visibility,
						Annotations: fieldAnnotations,
						IsStatic:    isStatic,
					},
					OwnerQualifiedName: ownerQualifiedName,
					FilePath:           filePath,
					Line:               int(node.StartPosition().Row) + 1,
				})
			}
		}
	}
}

// mergeScopeParents merges discovered block scope parent relationships into the ParseResult.
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

// extractJavaCalls walks a method body and extracts function calls.
func extractCalls(bodyNode *tree_sitter.Node, content []byte, filePath, callerName, callerClass, packageName string, result *model.ParseResult) {
	scope := callerName
	if callerClass != "" {
		scope = callerClass + "." + callerName
	}
	if packageName != "" {
		scope = packageName + "." + scope
	}
	lambdaCounter := 0 // method-body-level counter shared across all call sites
	astutil.WalkNamedChildren(bodyNode, func(node *tree_sitter.Node) bool {
		// Skip lambda nodes already handled elsewhere:
		// - argument position: handled by extractLambdaFromArgs
		// - declarative (variable_declarator): handled by extractDeclarativeLambda
		if node.Kind() == "lambda_expression" {
			parent := node.Parent()
			if parent != nil && (parent.Kind() == "argument_list" || parent.Kind() == "variable_declarator") {
				return false
			}
		}
		if node.Kind() == "method_invocation" {
			blockScope := astutil.DetectBlockScope(node, scope)
			mergeScopeParents(result, blockScope.ScopeParents)
			extractMethodInvocationWithScope(node, content, filePath, scope, blockScope.ScopeKey, &lambdaCounter, result)
			return true
		}
		if node.Kind() == "object_creation_expression" {
			blockScope := astutil.DetectBlockScope(node, scope)
			mergeScopeParents(result, blockScope.ScopeParents)
			extractConstructorCallWithScope(node, content, filePath, scope, blockScope.ScopeKey, &lambdaCounter, result)
			return true
		}
		if node.Kind() == "local_variable_declaration" {
			blockScope := astutil.DetectBlockScope(node, scope)
			mergeScopeParents(result, blockScope.ScopeParents)
			extractLocalVarTypeHint(node, content, blockScope.ScopeKey, filePath, result)
			extractPendingAssignment(node, content, blockScope.ScopeKey, result)
			extractDeclarativeLambda(node, content, filePath, blockScope.ScopeKey, &lambdaCounter, result)
		}
		if node.Kind() == "enhanced_for_statement" {
			blockScope := astutil.DetectBlockScope(node, scope)
			mergeScopeParents(result, blockScope.ScopeParents)
			typeNode := node.ChildByFieldName("type")
			nameNode := node.ChildByFieldName("name")
			if typeNode != nil && nameNode != nil {
				typeName := ExtractTypeName(typeNode, content)
				varName := nameNode.Utf8Text(content)
				if typeName != "" && varName != "" {
					result.TypeHints = append(result.TypeHints, model.TypeBinding{
						VarName: varName, TypeName: typeName,
						Tier: 0, Scope: blockScope.ScopeKey, FilePath: filePath,
					})
				}
			}
		}
		if node.Kind() == "catch_clause" {
			blockScope := astutil.DetectBlockScope(node, scope)
			mergeScopeParents(result, blockScope.ScopeParents)
			// catch (Exception e) → TypeHint for e
			for i := uint(0); i < node.ChildCount(); i++ {
				child := node.Child(i)
				if child.Kind() == "catch_formal_parameter" {
					var pTypeNode *tree_sitter.Node
					pNameNode := child.ChildByFieldName("name")
					for j := uint(0); j < child.ChildCount(); j++ {
						if child.Child(j).Kind() == "catch_type" {
							pTypeNode = child.Child(j)
							break
						}
					}
					if pTypeNode != nil && pNameNode != nil {
						typeName := ExtractTypeName(pTypeNode, content)
						varName := pNameNode.Utf8Text(content)
						if typeName != "" && varName != "" {
							result.TypeHints = append(result.TypeHints, model.TypeBinding{
								VarName:  varName,
								TypeName: typeName,
								Tier:     0,
								Scope:    blockScope.ScopeKey,
								FilePath: filePath,
							})
						}
					}
				}
			}
		}
		// Pattern A: field_access → static constant reference (e.g. OrderStatus.PENDING, CoinGoodsSubType.Type.EQUITY)
		if node.Kind() == "field_access" {
			objectNode := node.ChildByFieldName("object")
			fieldNode := node.ChildByFieldName("field")
			if objectNode != nil && fieldNode != nil {
				constName := fieldNode.Utf8Text(content)
				var objectExpr string
				if objectNode.Kind() == "identifier" {
					objectExpr = objectNode.Utf8Text(content)
				} else if objectNode.Kind() == "field_access" {
					innerObject := objectNode.ChildByFieldName("object")
					innerField := objectNode.ChildByFieldName("field")
					if innerObject != nil && innerField != nil && innerObject.Kind() == "identifier" {
						objectExpr = innerObject.Utf8Text(content) + "." + innerField.Utf8Text(content)
					}
				}
				if objectExpr != "" {
					result.ConstRefs = append(result.ConstRefs, model.RawConstRef{
						ObjectExpr: objectExpr,
						ConstName:  constName,
						CallerName: scope,
						FilePath:   filePath,
						Line:       int(node.StartPosition().Row) + 1,
						RefKind:    "field_access",
					})
				}
			}
			return true
		}
		// Pattern B: switch statement/expression → extract bare identifiers from case labels
		if node.Kind() == "switch_statement" || node.Kind() == "switch_expression" {
			extractSwitchCaseConstRefs(node, content, filePath, scope, result)
			return true
		}
		return true
	})
}

// extractSwitchCaseConstRefs extracts enum constant references from switch-case labels.
func extractSwitchCaseConstRefs(switchNode *tree_sitter.Node, content []byte, filePath, scope string, result *model.ParseResult) {
	condNode := switchNode.ChildByFieldName("condition")
	if condNode == nil {
		return
	}
	condInner := condNode
	if condNode.Kind() == "parenthesized_expression" && condNode.ChildCount() >= 2 {
		condInner = condNode.Child(1)
	}
	if condInner == nil || !condInner.IsNamed() {
		return
	}

	var switchConditionKind, switchVariableName, switchMethodReceiver, switchMethodName string
	switch condInner.Kind() {
	case "identifier":
		switchConditionKind = "variable"
		switchVariableName = condInner.Utf8Text(content)
	case "method_invocation":
		nameNode := condInner.ChildByFieldName("name")
		objectNode := condInner.ChildByFieldName("object")
		if nameNode == nil {
			return
		}
		switchConditionKind = "method_call"
		switchMethodName = nameNode.Utf8Text(content)
		if objectNode != nil && objectNode.Kind() == "identifier" {
			switchMethodReceiver = objectNode.Utf8Text(content)
		}
	default:
		return
	}

	bodyNode := switchNode.ChildByFieldName("body")
	if bodyNode == nil {
		return
	}
	for i := uint(0); i < bodyNode.ChildCount(); i++ {
		group := bodyNode.Child(i)
		if !group.IsNamed() {
			continue
		}
		for j := uint(0); j < group.ChildCount(); j++ {
			label := group.Child(j)
			if !label.IsNamed() || label.Kind() != "switch_label" {
				continue
			}
			for k := uint(0); k < label.ChildCount(); k++ {
				child := label.Child(k)
				if child.IsNamed() && child.Kind() == "identifier" {
					result.ConstRefs = append(result.ConstRefs, model.RawConstRef{
						SwitchConditionKind:  switchConditionKind,
						SwitchVariableName:   switchVariableName,
						SwitchMethodReceiver: switchMethodReceiver,
						SwitchMethodName:     switchMethodName,
						ConstName:            child.Utf8Text(content),
						CallerName:           scope,
						FilePath:             filePath,
						Line:                 int(child.StartPosition().Row) + 1,
						RefKind:              "switch_case",
					})
				}
			}
		}
	}
}

// extractLocalVarTypeHint generates Tier 0 TypeHints from local variable declarations with explicit types.
// e.g. "UserService svc = new UserService();" → TypeHint{VarName:"svc", TypeName:"UserService", Scope:"Foo.bar"}
func extractLocalVarTypeHint(node *tree_sitter.Node, content []byte, scope, filePath string, result *model.ParseResult) {
	typeNode := node.ChildByFieldName("type")
	if typeNode == nil {
		return
	}
	typeName := ExtractTypeName(typeNode, content)
	if typeName == "" || typeName == "var" {
		return
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() != "variable_declarator" {
			continue
		}
		nameNode := child.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		result.TypeHints = append(result.TypeHints, model.TypeBinding{
			VarName:  nameNode.Utf8Text(content),
			TypeName: typeName,
			TypeArgs: ExtractTypeArgs(typeNode, content),
			Tier:     0,
			Scope:    scope,
			FilePath: filePath,
		})
	}
}

// extractPendingAssignment extracts assignment patterns for fixpoint type propagation.
func extractPendingAssignment(node *tree_sitter.Node, content []byte, scope string, result *model.ParseResult) {
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
		lhs := nameNode.Utf8Text(content)

		switch valueNode.Kind() {
		case "identifier":
			// copy: User alias = user
			result.PendingAssignments = append(result.PendingAssignments, model.PendingAssignment{
				Kind: "copy", LHS: lhs, Scope: scope, RHS: valueNode.Utf8Text(content),
			})
		case "field_access":
			// fieldAccess: String name = addr.name
			obj := valueNode.ChildByFieldName("object")
			field := valueNode.ChildByFieldName("field")
			if obj != nil && field != nil && obj.Kind() == "identifier" {
				result.PendingAssignments = append(result.PendingAssignments, model.PendingAssignment{
					Kind: "field_access", LHS: lhs, Scope: scope, Receiver: obj.Utf8Text(content), Field: field.Utf8Text(content),
				})
			}
		case "method_invocation":
			obj := valueNode.ChildByFieldName("object")
			name := valueNode.ChildByFieldName("name")
			if name == nil {
				break
			}
			var argTypes []string
			argsNode := valueNode.ChildByFieldName("arguments")
			if argsNode != nil {
				for j := uint(0); j < argsNode.ChildCount(); j++ {
					arg := argsNode.Child(j)
					if arg.IsNamed() {
						argTypes = append(argTypes, inferArgType(arg, content))
					}
				}
			}
			if obj == nil {
				// callResult: User user = getUser()
				result.PendingAssignments = append(result.PendingAssignments, model.PendingAssignment{
					Kind: "call_result", LHS: lhs, Scope: scope, Callee: name.Utf8Text(content), ArgTypes: argTypes,
				})
			} else if obj.Kind() == "identifier" {
				// methodCallResult: Address addr = user.getAddress()
				result.PendingAssignments = append(result.PendingAssignments, model.PendingAssignment{
					Kind: "method_call_result", LHS: lhs, Scope: scope, Receiver: obj.Utf8Text(content), Method: name.Utf8Text(content), ArgTypes: argTypes,
				})
			}
		}
	}
}

// extractMethodInvocation extracts a method_invocation node.
func extractMethodInvocation(node *tree_sitter.Node, content []byte, filePath, qualifiedCallerName string, lambdaCounter *int, result *model.ParseResult) {
	// method_invocation has: object.method(args) or method(args)
	methodName := ""
	receiverExpr := ""

	nameNode := node.ChildByFieldName("name")
	if nameNode != nil {
		methodName = nameNode.Utf8Text(content)
	}

	objectNode := node.ChildByFieldName("object")
	if objectNode != nil {
		receiverExpr = objectNode.Utf8Text(content)
	}

	// Count arguments, extract lambda from args
	argsNode := node.ChildByFieldName("arguments")
	argCount, argTypes, argExprs := extractLambdaFromArgs(argsNode, content, filePath, qualifiedCallerName, lambdaCounter, result, methodName, receiverExpr)

	if methodName == "" {
		return
	}

	callerContext := qualifiedCallerName

	flowContext := astutil.DetectFlowContext(node, content)
	result.Calls = append(result.Calls, model.RawCall{
		CalledName:   methodName,
		CallerName:   callerContext,
		CallerKind:   constants.KindFunction,
		FilePath:     filePath,
		Line:         int(node.StartPosition().Row) + 1,
		ArgCount:     argCount,
		ArgTypes:     argTypes,
		ArgExprs:     argExprs,
		ReceiverExpr: receiverExpr,
		FlowContext:  flowContext.Kind,
		FlowLine:     flowContext.Line,
	})
}

// extractMethodInvocationWithScope calls extractMethodInvocation and sets CallerScope on the direct RawCall produced.
func extractMethodInvocationWithScope(node *tree_sitter.Node, content []byte, filePath, qualifiedCallerName, callerScope string, lambdaCounter *int, result *model.ParseResult) {
	callsBefore := len(result.Calls)
	extractMethodInvocation(node, content, filePath, qualifiedCallerName, lambdaCounter, result)
	// Set CallerScope and ChainID/ChainDepth on calls that belong to this caller (not recursively produced lambda body calls)
	chainID, chainDepth := computeJavaChainInfo(node)
	for i := callsBefore; i < len(result.Calls); i++ {
		if result.Calls[i].CallerName == qualifiedCallerName {
			result.Calls[i].CallerScope = callerScope
			if chainID > 0 {
				result.Calls[i].ChainID = chainID
				result.Calls[i].ChainDepth = chainDepth
			}
		}
	}
}

// extractConstructorCallWithScope calls extractConstructorCall and sets CallerScope on the direct RawCall produced.
func extractConstructorCallWithScope(node *tree_sitter.Node, content []byte, filePath, qualifiedCallerName, callerScope string, lambdaCounter *int, result *model.ParseResult) {
	callsBefore := len(result.Calls)
	extractConstructorCall(node, content, filePath, qualifiedCallerName, lambdaCounter, result)
	// Only set CallerScope on calls that belong to this caller (not recursively produced lambda body calls)
	for i := callsBefore; i < len(result.Calls); i++ {
		if result.Calls[i].CallerName == qualifiedCallerName {
			result.Calls[i].CallerScope = callerScope
		}
	}
}


// extractLambdaFromArgs iterates argument children, extracts lambda_expression as Symbol with recursive calls,
// and collects argCount/argTypes/argExprs for non-lambda arguments.
func extractLambdaFromArgs(argsNode *tree_sitter.Node, content []byte, filePath, qualifiedCallerName string, lambdaCounter *int, result *model.ParseResult, ownerMethodName, ownerReceiverExpr string) (int, []string, []string) {
	if argsNode == nil {
		return 0, nil, nil
	}
	var argCount int
	var argTypes, argExprs []string
	for i := uint(0); i < argsNode.ChildCount(); i++ {
		child := argsNode.Child(i)
		if !child.IsNamed() {
			continue
		}
		if child.Kind() == "lambda_expression" {
			argCount++
			argTypes = append(argTypes, "")
			argExprs = append(argExprs, "")
			*lambdaCounter++
			lambdaName := fmt.Sprintf("lambda$%d", *lambdaCounter)
			lambdaQualifiedName := qualifiedCallerName + "." + lambdaName
			lambdaID := astutil.GenerateSymbolID(filePath, lambdaQualifiedName, int(child.StartPosition().Row)+1)
			lambdaParams := extractLambdaParams(child, content)
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
			// Produce TypeHints for explicitly typed lambda parameters
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
				CallerName:          qualifiedCallerName,
				CallerKind:          constants.KindFunction,
				FilePath:            filePath,
				Line:                int(child.StartPosition().Row) + 1,
				IsPreResolved:       true,
				LambdaOwnerMethod:   ownerMethodName,
				LambdaOwnerReceiver: ownerReceiverExpr,
			})
			body := child.ChildByFieldName("body")
			if body != nil {
				// Expression lambda: body is the expression itself (e.g. method_invocation)
				// Block lambda: body is a block containing statements
				// Use extractCalls on the lambda node itself so walker visits body as child
				if body.Kind() != "block" {
					extractCalls(child, content, filePath, lambdaQualifiedName, "", "", result)
				} else {
					extractCalls(body, content, filePath, lambdaQualifiedName, "", "", result)
				}
			}
		} else {
			argCount++
			argTypes = append(argTypes, inferArgType(child, content))
			argExprs = append(argExprs, child.Utf8Text(content))
		}
	}
	return argCount, argTypes, argExprs
}

// extractLambdaParams extracts parameter information from a lambda_expression node.
// Explicit type: (Order order) -> ... → [{Name:"order", Type:"Order"}]
// Implicit single: order -> ... → [{Name:"order", Type:""}]
// Implicit multi: (k, v) -> ... → [{Name:"k", Type:""}, {Name:"v", Type:""}]
func extractLambdaParams(lambdaNode *tree_sitter.Node, content []byte) []model.ParamInfo {
	// Check first named child to determine parameter style
	firstNamed := lambdaNode.NamedChild(0)
	if firstNamed == nil {
		return nil
	}

	// Case 1: formal_parameters → formal_parameter (explicitly typed)
	if firstNamed.Kind() == "formal_parameters" {
		var params []model.ParamInfo
		for i := uint(0); i < firstNamed.NamedChildCount(); i++ {
			param := firstNamed.NamedChild(i)
			if param.Kind() == "formal_parameter" {
				typeNode := param.ChildByFieldName("type")
				nameNode := param.ChildByFieldName("name")
				typeName := ""
				if typeNode != nil {
					typeName = ExtractTypeName(typeNode, content)
				}
				paramName := ""
				if nameNode != nil {
					paramName = nameNode.Utf8Text(content)
				}
				if paramName != "" {
					params = append(params, model.ParamInfo{Name: paramName, Type: typeName})
				}
			}
		}
		return params
	}

	// Case 2: inferred_parameters → (k, v) without types
	if firstNamed.Kind() == "inferred_parameters" {
		var params []model.ParamInfo
		for i := uint(0); i < firstNamed.NamedChildCount(); i++ {
			child := firstNamed.NamedChild(i)
			if child.Kind() == "identifier" {
				params = append(params, model.ParamInfo{Name: child.Utf8Text(content), Type: ""})
			}
		}
		return params
	}

	// Case 3: single identifier without parentheses (order -> ...)
	if firstNamed.Kind() == "identifier" {
		return []model.ParamInfo{{Name: firstNamed.Utf8Text(content), Type: ""}}
	}

	return nil
}

// extractDeclarativeLambda detects variable declarations with lambda_expression on the right side,
// extracts the lambda as a Symbol and recursively extracts calls from its body.
func extractDeclarativeLambda(node *tree_sitter.Node, content []byte, filePath, callerQualifiedName string, lambdaCounter *int, result *model.ParseResult) {
	for i := uint(0); i < node.NamedChildCount(); i++ {
		declarator := node.NamedChild(i)
		if declarator.Kind() != "variable_declarator" {
			continue
		}
		nameNode := declarator.ChildByFieldName("name")
		valueNode := declarator.ChildByFieldName("value")
		if nameNode == nil || valueNode == nil || valueNode.Kind() != "lambda_expression" {
			continue
		}
		lambdaName := nameNode.Utf8Text(content)
		if lambdaName == "" {
			continue
		}
		lambdaQualifiedName := callerQualifiedName + "." + lambdaName
		lambdaID := astutil.GenerateSymbolID(filePath, lambdaQualifiedName, int(valueNode.StartPosition().Row)+1)
		lambdaParams := extractLambdaParams(valueNode, content)
		result.Symbols = append(result.Symbols, model.Symbol{
			ID:            lambdaID,
			Name:          lambdaName,
			QualifiedName: lambdaQualifiedName,
			Kind:          constants.KindFunction,
			FilePath:      filePath,
			StartLine:     int(valueNode.StartPosition().Row) + 1,
			EndLine:       int(valueNode.EndPosition().Row) + 1,
			IsLambda:      true,
			LambdaContext: callerQualifiedName,
			Params:        lambdaParams,
		})
		// Append TypeHint with LambdaSymbolID — this will override the one from extractLocalVarTypeHint
		// because TypeInfer processes hints in order and same key overwrites.
		typeNode := node.ChildByFieldName("type")
		typeName := ""
		if typeNode != nil {
			typeName = ExtractTypeName(typeNode, content)
		}
		result.TypeHints = append(result.TypeHints, model.TypeBinding{
			VarName:        lambdaName,
			TypeName:       typeName,
			Scope:          callerQualifiedName,
			FilePath:       filePath,
			LambdaSymbolID: lambdaID,
		})
		// Produce TypeHints for explicitly typed lambda parameters
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
		body := valueNode.ChildByFieldName("body")
		if body != nil {
			if body.Kind() != "block" {
				extractCalls(valueNode, content, filePath, lambdaQualifiedName, "", "", result)
			} else {
				extractCalls(body, content, filePath, lambdaQualifiedName, "", "", result)
			}
		}
	}
}

// inferArgType infers a type hint from a call argument AST node.
// Returns empty string for unknown types (conservative).
func inferArgType(node *tree_sitter.Node, content []byte) string {
	switch node.Kind() {
	case "string_literal":
		return "String"
	case "decimal_integer_literal":
		return "int"
	case "decimal_floating_point_literal":
		return "double"
	case "true", "false":
		return "boolean"
	case "null_literal":
		return "null"
	case "object_creation_expression":
		typeNode := node.ChildByFieldName("type")
		if typeNode != nil {
			return ExtractTypeName(typeNode, content)
		}
	case "field_access":
		obj := node.ChildByFieldName("object")
		if obj == nil {
			return ""
		}
		// Single-level: ResponseCode.FAIL → type is "ResponseCode"
		if obj.Kind() == "identifier" {
			name := obj.Utf8Text(content)
			if len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z' {
				return name
			}
		}
		// Multi-level: ResponseCode.FAIL.code → definitely NOT the object's type
		// Prefix with "!" to signal exclusion
		if obj.Kind() == "field_access" {
			outerObj := obj.ChildByFieldName("object")
			if outerObj != nil && outerObj.Kind() == "identifier" {
				name := outerObj.Utf8Text(content)
				if len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z' {
					return "!" + name // "!ResponseCode" means NOT ResponseCode
				}
			}
		}
	case "class_literal":
		// User.class → "User"
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child.Kind() == "type_identifier" {
				return child.Utf8Text(content)
			}
		}
	}
	return ""
}

// extractConstructorCall extracts a new Xxx() expression.
func extractConstructorCall(node *tree_sitter.Node, content []byte, filePath, qualifiedCallerName string, lambdaCounter *int, result *model.ParseResult) {
	typeNode := node.ChildByFieldName("type")
	if typeNode == nil {
		return
	}
	typeName := ExtractTypeName(typeNode, content)
	if typeName == "" {
		return
	}

	// Count arguments, extract lambda from args
	argsNode := node.ChildByFieldName("arguments")
	argCount, argTypes, argExprs := extractLambdaFromArgs(argsNode, content, filePath, qualifiedCallerName, lambdaCounter, result, typeName, "")

	callerContext := qualifiedCallerName

	flowContext := astutil.DetectFlowContext(node, content)
	result.Calls = append(result.Calls, model.RawCall{
		CalledName:  typeName,
		CallerName:  callerContext,
		CallerKind:  constants.KindFunction,
		FilePath:    filePath,
		Line:        int(node.StartPosition().Row) + 1,
		ArgCount:    argCount,
		ArgTypes:    argTypes,
		ArgExprs:    argExprs,
		FlowContext: flowContext.Kind,
		FlowLine:    flowContext.Line,
	})
}

// Helper functions

// extractAnnotations extracts annotation names from a modifiers node.
func ExtractAnnotations(modifiers *tree_sitter.Node, content []byte) []model.StructuredAnnotation {
	var annotations []model.StructuredAnnotation
	for i := uint(0); i < modifiers.ChildCount(); i++ {
		child := modifiers.Child(i)
		if child.Kind() == "marker_annotation" || child.Kind() == "annotation" {
			nameNode := child.ChildByFieldName("name")
			if nameNode == nil {
				for j := uint(0); j < child.ChildCount(); j++ {
					candidate := child.Child(j)
					if candidate.Kind() == "identifier" {
						nameNode = candidate
						break
					}
				}
			}
			if nameNode != nil {
				name := nameNode.Utf8Text(content)
				var params map[string]string
				for j := uint(0); j < child.ChildCount(); j++ {
					part := child.Child(j)
					if part.Kind() == "annotation_argument_list" {
						params = parseAnnotationArguments(part, content)
					}
				}
				annotations = append(annotations, model.StructuredAnnotation{
					Name:   name,
					Params: params,
				})
			}
		}
	}
	return annotations
}

// parseAnnotationArguments parses annotation_argument_list into key-value pairs.
// Handles: ("value"), (key = "value"), (key1 = "v1", key2 = "v2"), (key = EnumType.VALUE)
func parseAnnotationArguments(argList *tree_sitter.Node, content []byte) map[string]string {
	params := make(map[string]string)
	for i := uint(0); i < argList.ChildCount(); i++ {
		child := argList.Child(i)
		switch child.Kind() {
		case "element_value_pair":
			// key = value
			keyNode := child.ChildByFieldName("key")
			valueNode := child.ChildByFieldName("value")
			if keyNode != nil && valueNode != nil {
				key := keyNode.Utf8Text(content)
				value := extractAnnotationValueText(valueNode, content)
				params[key] = value
			}
		case "string_literal":
			// Single value: ("literal")
			value := strings.Trim(child.Utf8Text(content), "\"")
			params["value"] = value
		case "identifier", "field_access", "scoped_identifier":
			// Single enum/constant value: (RequestMethod.POST)
			params["value"] = child.Utf8Text(content)
		case "element_value_array_initializer", "array_initializer":
			// Array value without key: ({"/a", "/b"})
			params["value"] = child.Utf8Text(content)
		}
	}
	return params
}

// extractAnnotationValueText extracts the text value from an annotation value node.
// Strips quotes from string literals, preserves other values as-is.
func extractAnnotationValueText(valueNode *tree_sitter.Node, content []byte) string {
	text := valueNode.Utf8Text(content)
	if valueNode.Kind() == "string_literal" {
		return strings.Trim(text, "\"")
	}
	return text
}

// AnnotationsToLegacyStrings converts structured annotations to legacy string format.
// Used for backward compatibility where string format is still needed.
func AnnotationsToLegacyStrings(annotations []model.StructuredAnnotation) []string {
	result := make([]string, 0, len(annotations))
	for _, annotation := range annotations {
		str := "@" + annotation.Name
		if len(annotation.Params) > 0 {
			var parts []string
			for key, value := range annotation.Params {
				if key == "value" && len(annotation.Params) == 1 {
					parts = append(parts, "\""+value+"\"")
				} else {
					parts = append(parts, key+" = \""+value+"\"")
				}
			}
			str += "(" + strings.Join(parts, ", ") + ")"
		}
		result = append(result, str)
	}
	return result
}

// buildTypeEnv builds a variable→type map from TypeHints for a given class scope.
// Uses imports to resolve short type names to fully qualified names.
func buildTypeEnv(hints []model.TypeBinding, className string, imports []model.RawImport) map[string]string {
	// Build short name → FQN map from imports
	importMap := make(map[string]string)
	for _, imp := range imports {
		importMap[imp.SymbolName] = imp.ModulePath
	}

	env := make(map[string]string)
	for _, h := range hints {
		if h.Scope == className || h.Scope == "" {
			typeName := h.TypeName
			if fqn, ok := importMap[typeName]; ok {
				typeName = fqn
			}
			env[h.VarName] = typeName
		}
	}
	return env
}

// extractTypeParams extracts generic type parameter names from a node's type_parameters child.
// For Java: <T, U extends Foo> → ["T", "U"] (only names, not bounds).
func extractTypeParams(node *tree_sitter.Node, content []byte) []string {
	typeParametersNode := node.ChildByFieldName("type_parameters")
	if typeParametersNode == nil {
		return nil
	}
	var typeParams []string
	for i := uint(0); i < typeParametersNode.ChildCount(); i++ {
		typeParamNode := typeParametersNode.Child(i)
		if typeParamNode.Kind() == "type_parameter" {
			for k := uint(0); k < typeParamNode.ChildCount(); k++ {
				child := typeParamNode.Child(k)
				if child.Kind() == "type_identifier" {
					typeParams = append(typeParams, child.Utf8Text(content))
					break
				}
			}
		}
	}
	return typeParams
}

// extractReturnType extracts a return type as a structured ReturnType with generic args.
func extractReturnType(node *tree_sitter.Node, content []byte) model.ReturnType {
	switch node.Kind() {
	case "generic_type":
		baseName := ""
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child.Kind() == "type_identifier" {
				baseName = child.Utf8Text(content)
				break
			}
		}
		args := ExtractTypeArgs(node, content)
		return model.ReturnType{Name: baseName, Args: args}
	case "array_type":
		elemType := node.ChildByFieldName("element")
		if elemType != nil {
			return model.ReturnType{Name: ExtractTypeName(elemType, content) + "[]"}
		}
	}
	return model.ReturnType{Name: ExtractTypeName(node, content)}
}

// extractTypeName extracts a type name, handling generics.
func ExtractTypeName(node *tree_sitter.Node, content []byte) string {
	switch node.Kind() {
	case "type_identifier", "identifier":
		return node.Utf8Text(content)
	case "generic_type":
		// Get base type (first child)
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child.Kind() == "type_identifier" {
				return child.Utf8Text(content)
			}
		}
	case "scoped_type_identifier":
		return node.Utf8Text(content)
	case "array_type":
		elemType := node.ChildByFieldName("element")
		if elemType != nil {
			return ExtractTypeName(elemType, content) + "[]"
		}
	}
	// Fallback: primitive types
	text := node.Utf8Text(content)
	if len(text) < 30 {
		return text
	}
	return ""
}

// ExtractTypeArgs extracts generic type arguments from a type node.
// e.g. List<User> → ["User"], Map<String, User> → ["String", "User"]
func ExtractTypeArgs(node *tree_sitter.Node, content []byte) []model.TypeArg {
	if node == nil {
		return nil
	}
	if node.Kind() != "generic_type" && node.Kind() != "parameterized_type" {
		return nil
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "type_arguments" {
			var args []model.TypeArg
			for j := uint(0); j < child.ChildCount(); j++ {
				arg := child.Child(j)
				if arg.IsNamed() {
					args = append(args, extractTypeArg(arg, content))
				}
			}
			return args
		}
	}
	return nil
}

// extractTypeArg extracts a single type argument, handling wildcards and nested generics.
func extractTypeArg(node *tree_sitter.Node, content []byte) model.TypeArg {
	switch node.Kind() {
	case "wildcard":
		// ? extends Animal → wildcard children: [type_identifier("Animal")]
		// ? super Dog      → wildcard children: [super, type_identifier("Dog")]
		// ?                → wildcard children: (none)
		hasSuperKeyword := false
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child.Kind() == "super" {
				hasSuperKeyword = true
			}
			if child.Kind() == "type_identifier" || child.Kind() == "generic_type" {
				if hasSuperKeyword {
					return model.TypeArg{Name: "Object"}
				}
				return extractTypeArg(child, content)
			}
		}
		return model.TypeArg{Name: "Object"}
	case "generic_type":
		name := ExtractTypeName(node, content)
		nestedArgs := ExtractTypeArgs(node, content)
		return model.TypeArg{Name: name, Args: nestedArgs}
	default:
		return model.TypeArg{Name: ExtractTypeName(node, content)}
	}
}

// extractTypeFromHeritage extracts the parent type from a superclass node.
func extractTypeFromHeritage(node *tree_sitter.Node, content []byte) string {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.IsNamed() {
			return ExtractTypeName(child, content)
		}
	}
	return ""
}

// extractHeritageTypeArgs extracts generic type arguments from a superclass node.
// For: extends BaseRepo<User, Long> → [{Name:"User"}, {Name:"Long"}]
func extractHeritageTypeArgs(superclassNode *tree_sitter.Node, content []byte) []model.TypeArg {
	for i := uint(0); i < superclassNode.ChildCount(); i++ {
		child := superclassNode.Child(i)
		if child.Kind() == "generic_type" {
			return ExtractTypeArgs(child, content)
		}
	}
	return nil
}

// countComplexity counts cyclomatic complexity (branches + 1).
func countComplexity(node *tree_sitter.Node, content []byte) int {
	complexity := 1
	astutil.WalkNamedChildren(node, func(child *tree_sitter.Node) bool {
		switch child.Kind() {
		case "if_statement", "for_statement", "enhanced_for_statement",
			"while_statement", "do_statement", "catch_clause",
			"switch_expression", "ternary_expression",
			"binary_expression":
			if child.Kind() == "binary_expression" {
				op := ""
				for j := uint(0); j < child.ChildCount(); j++ {
					c := child.Child(j)
					if !c.IsNamed() {
						op = c.Utf8Text(content)
					}
				}
				if op == "&&" || op == "||" {
					complexity++
				}
			} else {
				complexity++
			}
		}
		return true
	})
	return complexity
}

// walkNamedChildren iterates named children recursively.

// computeJavaChainInfo determines the chain position of a method_invocation node.
// Returns (chainID, chainDepth). chainID is the outermost call's line number (1-based).
// chainDepth is 0 for the innermost call, increasing outward.
// Returns (0, 0) if the node is not part of a chain (single call or no nesting).
func computeJavaChainInfo(node *tree_sitter.Node) (int, int) {
	objectNode := node.ChildByFieldName("object")
	hasInnerMethodInvocation := objectNode != nil && objectNode.Kind() == "method_invocation"

	hasOuterMethodInvocation := false
	parent := node.Parent()
	if parent != nil && parent.Kind() == "method_invocation" {
		parentObject := parent.ChildByFieldName("object")
		if parentObject != nil && sameASTNode(parentObject, node) {
			hasOuterMethodInvocation = true
		}
	}

	if !hasInnerMethodInvocation && !hasOuterMethodInvocation {
		return 0, 0
	}

	// Walk up to find the outermost method_invocation in the chain
	outermost := node
	for {
		parentNode := outermost.Parent()
		if parentNode == nil || parentNode.Kind() != "method_invocation" {
			break
		}
		parentObject := parentNode.ChildByFieldName("object")
		if parentObject == nil || !sameASTNode(parentObject, outermost) {
			break
		}
		outermost = parentNode
	}
	chainID := int(outermost.StartPosition().Row) + 1

	// Count total depth from outermost inward
	totalDepth := 0
	current := outermost
	for {
		inner := current.ChildByFieldName("object")
		if inner == nil || inner.Kind() != "method_invocation" {
			break
		}
		totalDepth++
		current = inner
	}

	// Count distance from outermost to current node
	distanceFromOutermost := 0
	current = outermost
	for !sameASTNode(current, node) {
		inner := current.ChildByFieldName("object")
		if inner == nil {
			break
		}
		current = inner
		distanceFromOutermost++
	}

	chainDepth := totalDepth - distanceFromOutermost
	return chainID, chainDepth
}

// sameASTNode compares two tree-sitter nodes by byte range and kind (pointer equality is unreliable).
func sameASTNode(a, b *tree_sitter.Node) bool {
	if a == nil || b == nil {
		return false
	}
	return a.StartByte() == b.StartByte() && a.EndByte() == b.EndByte() && a.Kind() == b.Kind()
}
