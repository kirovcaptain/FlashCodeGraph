package python

import (
	"encoding/json"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
)

// extractPython extracts symbols from Python AST.
func Extract(rootNode *tree_sitter.Node, content []byte, file scanner.ScannedFile, result *model.ParseResult) {
	astutil.WalkNamedChildren(rootNode, func(node *tree_sitter.Node) bool {
		switch node.Kind() {
		case "import_from_statement":
			extractImportFrom(node, content, file.RelPath, result)
			return false
		case "import_statement":
			extractImport(node, content, file.RelPath, result)
			return false
		case "class_definition":
			extractClass(node, content, file, result)
			return false
		case "function_definition":
			extractFunction(node, content, file.RelPath, "", result)
			return false
		case "decorated_definition":
			extractDecorated(node, content, file, "", result)
			return false
		}
		return true
	})
}

// extractPythonImportFrom handles "from x import y".
func extractImportFrom(node *tree_sitter.Node, content []byte, filePath string, result *model.ParseResult) {
	modulePath := ""
	var symbolNames []string

	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Kind() {
		case "dotted_name", "relative_import":
			if modulePath == "" {
				modulePath = child.Utf8Text(content)
			} else {
				symbolNames = append(symbolNames, child.Utf8Text(content))
			}
		case "identifier":
			// "from x import a" — imported name may be identifier, not dotted_name
			if modulePath != "" {
				symbolNames = append(symbolNames, child.Utf8Text(content))
			}
		case "aliased_import":
			// "from x import User as U" — extract original name, not alias
			nameNode := child.ChildByFieldName("name")
			if nameNode != nil && modulePath != "" {
				symbolNames = append(symbolNames, nameNode.Utf8Text(content))
			}
		}
	}

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

// extractPythonImport handles "import x".
func extractImport(node *tree_sitter.Node, content []byte, filePath string, result *model.ParseResult) {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "dotted_name" {
			result.Imports = append(result.Imports, model.RawImport{
				ModulePath: child.Utf8Text(content),
				FilePath:   filePath,
				Line:       int(node.StartPosition().Row) + 1,
			})
		} else if child.Kind() == "aliased_import" {
			nameNode := child.ChildByFieldName("name")
			aliasNode := child.ChildByFieldName("alias")
			modulePath := ""
			alias := ""
			if nameNode != nil {
				modulePath = nameNode.Utf8Text(content)
			}
			if aliasNode != nil {
				alias = aliasNode.Utf8Text(content)
			}
			result.Imports = append(result.Imports, model.RawImport{
				ModulePath: modulePath,
				Alias:      alias,
				FilePath:   filePath,
				Line:       int(node.StartPosition().Row) + 1,
			})
		}
	}
}

// extractPythonClass extracts a class definition.
func extractClass(node *tree_sitter.Node, content []byte, file scanner.ScannedFile, result *model.ParseResult) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	className := nameNode.Utf8Text(content)

	// Build qualified name from file path
	qualifiedName := buildQualifiedName(file.RelPath, className)

	classType := constants.ClassTypeClass
	isAbstract := false

	// Extract bases (heritage)
	argList := astutil.FindChildByKind(node, "argument_list")
	if argList != nil {
		for i := uint(0); i < argList.ChildCount(); i++ {
			base := argList.Child(i)
			if !base.IsNamed() {
				continue
			}
			baseName := base.Utf8Text(content)
			if baseName == "ABC" || baseName == "ABCMeta" {
				isAbstract = true
				classType = constants.ClassTypeAbstract
			}
			result.Heritage = append(result.Heritage, model.RawHeritage{
				ChildName:      className,
				ChildQualified: qualifiedName,
				ParentName:     baseName,
				Kind:           "extends",
				FilePath:       file.RelPath,
			})
		}
	}

	result.Symbols = append(result.Symbols, model.Symbol{
		ID:            astutil.GenerateSymbolID(file.RelPath, qualifiedName, int(node.StartPosition().Row)+1),
		Name:          className,
		QualifiedName: qualifiedName,
		Kind:          classType,
		FilePath:      file.RelPath,
		StartLine:     int(node.StartPosition().Row) + 1,
		EndLine:       int(node.EndPosition().Row) + 1,
		ClassType:     classType,
		IsAbstract:    isAbstract,
		IsExported:    !strings.HasPrefix(className, "_"),
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
		switch child.Kind() {
		case "function_definition":
			extractFunction(child, content, file.RelPath, className, result)
		case "decorated_definition":
			extractDecorated(child, content, file, className, result)
		}
	}
}

// extractPythonFunction extracts a function/method definition.
func extractFunction(node *tree_sitter.Node, content []byte, filePath, className string, result *model.ParseResult) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	funcName := nameNode.Utf8Text(content)

	qualifiedName := buildQualifiedName(filePath, funcName)
	if className != "" {
		qualifiedName = buildQualifiedName(filePath, className) + "." + funcName
	}

	// Return type
	var returnTypes []string
	returnTypeNode := node.ChildByFieldName("return_type")
	if returnTypeNode != nil {
		returnTypes = []string{returnTypeNode.Utf8Text(content)}
	}

	// Parameters
	var paramTypes []map[string]string
	paramsNode := node.ChildByFieldName("parameters")
	if paramsNode != nil {
		for j := uint(0); j < paramsNode.ChildCount(); j++ {
			param := paramsNode.Child(j)
			if !param.IsNamed() {
				continue
			}
			paramName := ""
			paramType := ""

			switch param.Kind() {
			case "identifier":
				paramName = param.Utf8Text(content)
				if paramName == "self" || paramName == "cls" {
					continue
				}
			case "typed_parameter":
				pName := param.ChildByFieldName("name")
				if pName == nil {
					pName = astutil.FindChildByKind(param, "identifier")
				}
				if pName != nil {
					paramName = pName.Utf8Text(content)
				}
				pType := param.ChildByFieldName("type")
				if pType != nil {
					paramType = pType.Utf8Text(content)
				}
				if paramName == "self" || paramName == "cls" {
					continue
				}
			case "typed_default_parameter", "default_parameter":
				pName := param.ChildByFieldName("name")
				if pName != nil {
					paramName = pName.Utf8Text(content)
				}
				pType := param.ChildByFieldName("type")
				if pType != nil {
					paramType = pType.Utf8Text(content)
				}
			default:
				continue
			}

			if paramName != "" {
				entry := map[string]string{"name": paramName}
				if paramType != "" {
					entry["type"] = paramType
				}
				paramTypes = append(paramTypes, entry)
				// Type hint
				if paramType != "" {
					result.TypeHints = append(result.TypeHints, model.TypeBinding{
						VarName:  paramName,
						TypeName: paramType,
						Tier:     0,
						Scope:    qualifiedName,
						FilePath: filePath,
					})
				}
			}
		}
	}
	paramsJSON, _ := json.Marshal(paramTypes)

	isStatic := false
	isExported := !strings.HasPrefix(funcName, "_") || funcName == "__init__"

	// Complexity
	complexity := 1
	body := node.ChildByFieldName("body")
	if body != nil {
		complexity = countComplexity(body, content)
		extractCalls(body, content, filePath, qualifiedName, result)
	}

	// Detect @abstractmethod decorator
	isAbstract := false
	if parent := node.Parent(); parent != nil && parent.Kind() == "decorated_definition" {
		for i := uint(0); i < parent.ChildCount(); i++ {
			dec := parent.Child(i)
			if dec.Kind() == "decorator" && strings.Contains(dec.Utf8Text(content), "abstractmethod") {
				isAbstract = true
				break
			}
		}
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
		IsStatic:      isStatic,
		IsExported:    isExported,
		IsConstructor: funcName == "__init__",
		IsAbstract:    isAbstract,
		Complexity:    complexity,
	})

	// Extract routes from decorators
	ExtractRoutes(node, content, funcName, filePath, result)

	// Extract GraphQL Strawberry routes
	ExtractStrawberryRoutes(node, content, funcName, className, filePath, result)

	// Extract ORM queries
	ExtractORM(body, content, qualifiedName, filePath, result)

	// Extract HTTP and gRPC remote calls
	ExtractPythonRemoteCalls(body, content, qualifiedName, filePath, result)
}

// extractPythonDecorated handles decorated definitions.
func extractDecorated(node *tree_sitter.Node, content []byte, file scanner.ScannedFile, className string, result *model.ParseResult) {
	// Find the actual definition inside
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Kind() {
		case "function_definition":
			extractFunction(child, content, file.RelPath, className, result)
			// Check parent decorated_definition for Strawberry decorators
			nameNode := child.ChildByFieldName("name")
			if nameNode != nil {
				ExtractStrawberryRoutes(node, content, nameNode.Utf8Text(content), className, file.RelPath, result)
			}
		case "class_definition":
			extractClass(child, content, file, result)
		}
	}
}

// extractPythonCalls walks a function body for calls.
func extractCalls(body *tree_sitter.Node, content []byte, filePath, qualifiedCallerName string, result *model.ParseResult) {
	scope := qualifiedCallerName
	astutil.WalkNamedChildren(body, func(node *tree_sitter.Node) bool {
		if node.Kind() == "assignment" {
			extractPythonPendingAssignment(node, content, scope, filePath, result)
		}
		if node.Kind() == "call" {
			ExtractDjangoRoutes(node, content, filePath, result)
			calledName := ""
			receiverExpr := ""
			funcNode := node.ChildByFieldName("function")
			if funcNode != nil {
				switch funcNode.Kind() {
				case "identifier":
					calledName = funcNode.Utf8Text(content)
				case "attribute":
					objNode := funcNode.ChildByFieldName("object")
					attrNode := funcNode.ChildByFieldName("attribute")
					if objNode != nil {
						receiverExpr = objNode.Utf8Text(content)
					}
					if attrNode != nil {
						calledName = attrNode.Utf8Text(content)
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

// extractPythonPendingAssignment extracts assignment patterns for fixpoint type propagation.
func extractPythonPendingAssignment(node *tree_sitter.Node, content []byte, scope, filePath string, result *model.ParseResult) {
	left := node.ChildByFieldName("left")
	right := node.ChildByFieldName("right")
	if left == nil {
		return
	}

	// Handle self.xxx: Type = value / self.xxx = Constructor() → TypeHint for field
	if left.Kind() == "attribute" {
		obj := left.ChildByFieldName("object")
		attr := left.ChildByFieldName("attribute")
		if obj != nil && attr != nil && obj.Utf8Text(content) == "self" {
			fieldName := attr.Utf8Text(content)
			classQN := scope
			if dotIdx := strings.LastIndex(scope, "."); dotIdx >= 0 {
				classQN = scope[:dotIdx]
			}
			typeName := ""
			// Priority 1: type annotation (self.state: OrderState = ...)
			if typeNode := node.ChildByFieldName("type"); typeNode != nil && typeNode.Kind() == "identifier" {
				typeName = typeNode.Utf8Text(content)
			}
			// Priority 2: infer from constructor call (self.dao = UserDao())
			if typeName == "" && right != nil && right.Kind() == "call" {
				fn := right.ChildByFieldName("function")
				if fn != nil && fn.Kind() == "identifier" {
					typeName = fn.Utf8Text(content)
				}
			}
			if typeName != "" {
				result.TypeHints = append(result.TypeHints, model.TypeBinding{
					VarName:  fieldName,
					TypeName: typeName,
					Tier:     0,
					Scope:    classQN,
					FilePath: filePath,
				})
			}
		}
		return
	}

	if left.Kind() != "identifier" {
		return
	}
	if right == nil {
		return
	}
	lhs := left.Utf8Text(content)

	switch right.Kind() {
	case "identifier":
		result.PendingAssignments = append(result.PendingAssignments, model.PendingAssignment{
			Kind: "copy", LHS: lhs, Scope: scope, RHS: right.Utf8Text(content),
		})
	case "attribute":
		obj := right.ChildByFieldName("object")
		attr := right.ChildByFieldName("attribute")
		if obj != nil && attr != nil && obj.Kind() == "identifier" {
			result.PendingAssignments = append(result.PendingAssignments, model.PendingAssignment{
				Kind: "field_access", LHS: lhs, Scope: scope, Receiver: obj.Utf8Text(content), Field: attr.Utf8Text(content),
			})
		}
	case "call":
		fn := right.ChildByFieldName("function")
		if fn == nil {
			return
		}
		if fn.Kind() == "identifier" {
			result.PendingAssignments = append(result.PendingAssignments, model.PendingAssignment{
				Kind: "call_result", LHS: lhs, Scope: scope, Callee: fn.Utf8Text(content),
			})
		} else if fn.Kind() == "attribute" {
			obj := fn.ChildByFieldName("object")
			attr := fn.ChildByFieldName("attribute")
			if obj != nil && attr != nil && obj.Kind() == "identifier" {
				result.PendingAssignments = append(result.PendingAssignments, model.PendingAssignment{
					Kind: "method_call_result", LHS: lhs, Scope: scope, Receiver: obj.Utf8Text(content), Method: attr.Utf8Text(content),
				})
			}
		}
	}
}

func countComplexity(node *tree_sitter.Node, content []byte) int {
	complexity := 1
	astutil.WalkNamedChildren(node, func(child *tree_sitter.Node) bool {
		switch child.Kind() {
		case "if_statement", "elif_clause", "for_statement",
			"while_statement", "except_clause", "with_statement",
			"conditional_expression", "boolean_operator":
			complexity++
		}
		return true
	})
	return complexity
}

func buildQualifiedName(relPath, name string) string {
	// Convert file path to module path: services/user_service.py → services.user_service.ClassName
	modulePath := strings.TrimSuffix(relPath, ".py")
	modulePath = strings.TrimSuffix(modulePath, ".pyi")
	modulePath = strings.ReplaceAll(modulePath, "/", ".")
	modulePath = strings.ReplaceAll(modulePath, "\\", ".")
	if strings.HasSuffix(modulePath, ".__init__") {
		modulePath = strings.TrimSuffix(modulePath, ".__init__")
	}
	return modulePath + "." + name
}

