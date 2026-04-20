// Package typeinfer implements type inference for variable-to-type mapping.
package typeinfer

import (
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// TypeInfer performs type inference on parsed results.
type TypeInfer struct{}

// New creates a TypeInfer instance.
func New() *TypeInfer {
	return &TypeInfer{}
}

// InferLocal performs single-file type inference (Tier 0-2).
// Returns a TypeEnv mapping (scope, varName) → TypeInfo.
func (infer *TypeInfer) InferLocal(result *model.ParseResult) *model.TypeEnv {
	env := &model.TypeEnv{
		Bindings: make(map[string]*model.TypeInfo),
	}

	// Tier 0: explicit type annotations (from parser TypeHints)
	for _, hint := range result.TypeHints {
		key := scopedKey(hint.Scope, hint.VarName)
		env.Bindings[key] = &model.TypeInfo{
			TypeName:      hint.TypeName,
			TypeArgs:      hint.TypeArgs,
			Tier:          hint.Tier,
			Scope:         hint.Scope,
			MultiReturnOf: hint.MultiReturnOf,
			ReturnIndex:   hint.ReturnIndex,
		}
	}

	// Tier 0.5: resolve simple type names to fully qualified names via imports
	resolveTypeNames(env, result.Imports)

	// Store imports for receiver resolution in resolver
	env.Imports = result.Imports

	// Tier 1: constructor inference from calls (new Xxx(), Xxx(), NewXxx())
	for _, call := range result.Calls {
		if call.ReceiverExpr != "" {
			continue // method call, not constructor
		}
		typeName := inferConstructorType(call.CalledName)
		if typeName == "" {
			continue
		}
		// Find the variable this is assigned to by checking surrounding context
		// For now, register the constructor call's type for the caller scope
		// This will be refined when we have assignment context from the parser
	}

	// Tier 1: from variable assignments with constructor patterns in calls
	// Look for patterns: var = new Type(), var = Type(), var = NewType()
	inferTier1FromCalls(result, env)

	// Tier 2a: copy propagation (b = a, a already typed → b gets a's type)
	for round := 0; round < 3; round++ {
		changed := false
		for _, call := range result.Calls {
			if call.ReceiverExpr == "" {
				continue
			}
			receiverKey := scopedKey(call.CallerName, call.ReceiverExpr)
			if _, exists := env.Bindings[receiverKey]; exists {
				continue
			}
			// Check if receiver matches a known binding in broader scope
			globalKey := scopedKey("", call.ReceiverExpr)
			if info, exists := env.Bindings[globalKey]; exists {
				env.Bindings[receiverKey] = &model.TypeInfo{
					TypeName: info.TypeName,
					Tier:     2,
					Scope:    call.CallerName,
				}
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	// self/this receiver binding
	inferSelfReceiver(result, env)

	// Tier 2c: local return type inference
	// If a call targets a function in the same file with a known return_type,
	// infer the type of the call result variable.
	symbolReturnTypes := make(map[string][]string)
	for _, sym := range result.Symbols {
		if sym.Kind == constants.KindFunction && len(sym.ReturnTypes) > 0 {
			symbolReturnTypes[sym.Name] = sym.ReturnTypes
		}
	}
	for _, call := range result.Calls {
		if call.ReceiverExpr != "" {
			continue // method call, not a function call
		}
		retTypes, ok := symbolReturnTypes[call.CalledName]
		if !ok || len(retTypes) == 0 {
			continue
		}
		key := scopedKey(call.CallerName, call.CalledName+"_result")
		if _, exists := env.Bindings[key]; !exists {
			env.Bindings[key] = &model.TypeInfo{
				TypeName: retTypes[0],
				Tier:     2,
				Scope:    call.CallerName,
			}
		}
	}

	// Final pass: resolve all TypeNames to fully qualified names via imports
	resolveTypeNames(env, result.Imports)

	return env
}

// ResolveFixpoint iterates over pending assignments until no new bindings are produced.
// Handles: copy (b=a), call_result (x=f()), field_access (x=a.field), method_call_result (x=a.method()).
func (infer *TypeInfer) ResolveFixpoint(env *model.TypeEnv, pendings []model.PendingAssignment, findByName func(string) []model.Symbol) {
	if env == nil || len(pendings) == 0 {
		return
	}
	resolved := make([]bool, len(pendings))
	for iter := 0; iter < 10; iter++ {
		changed := false
		for i, p := range pendings {
			if resolved[i] {
				continue
			}
			key := scopedKey(p.Scope, p.LHS)
			if _, exists := env.Bindings[key]; exists {
				resolved[i] = true
				continue
			}
			var typeName string
			switch p.Kind {
			case "copy":
				typeName = lookupInEnv(env, p.Scope, p.RHS)
			case "call_result":
				typeName = lookupReturnType(p.Callee, findByName)
			case "field_access":
				receiverType := lookupInEnv(env, p.Scope, p.Receiver)
				if receiverType != "" {
					typeName = lookupFieldType(receiverType, p.Field, findByName)
				}
			case "method_call_result":
				receiverType := lookupInEnv(env, p.Scope, p.Receiver)
				if receiverType != "" {
					typeName = lookupMethodReturnTypeWithArgs(env, p.Scope, p.Receiver, receiverType, p.Method, findByName)
				}
			}
			if typeName != "" {
				env.Bindings[key] = &model.TypeInfo{TypeName: typeName, Tier: 2, Scope: p.Scope}
				resolved[i] = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

func lookupInEnv(env *model.TypeEnv, scope, varName string) string {
	key := scopedKey(scope, varName)
	if info, exists := env.Bindings[key]; exists {
		return info.TypeName
	}
	// Try class scope (field TypeHint)
	if dotIdx := strings.LastIndex(scope, "."); dotIdx >= 0 {
		classKey := scopedKey(scope[:dotIdx], varName)
		if info, exists := env.Bindings[classKey]; exists {
			return info.TypeName
		}
	}
	return ""
}

func lookupReturnType(callee string, findByName func(string) []model.Symbol) string {
	symbols := findByName(callee)
	for _, s := range symbols {
		if s.Kind == constants.KindFunction && len(s.ReturnTypes) > 0 {
			return s.ReturnTypes[0]
		}
	}
	return ""
}

func lookupFieldType(receiverType, fieldName string, findByName func(string) []model.Symbol) string {
	// Look for Lombok getter: getFieldName
	capName := strings.ToUpper(fieldName[:1]) + fieldName[1:]
	getterName := "get" + capName
	symbols := findByName(getterName)
	for _, s := range symbols {
		if s.Kind == constants.KindFunction && strings.Contains(s.QualifiedName, lastSegmentFromType(receiverType)+".") && len(s.ReturnTypes) > 0 {
			return s.ReturnTypes[0]
		}
	}
	return ""
}

func lookupMethodReturnType(receiverType, methodName string, findByName func(string) []model.Symbol) string {
	symbols := findByName(methodName)
	typeSeg := lastSegmentFromType(receiverType)
	for _, s := range symbols {
		if s.Kind == constants.KindFunction && strings.Contains(s.QualifiedName, typeSeg+".") && len(s.ReturnTypes) > 0 {
			return s.ReturnTypes[0]
		}
	}
	return ""
}

// containerElementIndex maps known container types to which TypeArg index their element methods return.
// arity 1: List<User>.get → index 0; arity 2: Map<K,V>.get → index 1 (value)
var containerElementIndex = map[string]int{
	"List": 0, "ArrayList": 0, "LinkedList": 0, "Set": 0, "HashSet": 0, "TreeSet": 0,
	"Queue": 0, "Deque": 0, "Stack": 0, "Collection": 0, "Iterable": 0, "Iterator": 0,
	"Optional": 0, "Stream": 0, "Vec": 0,
	"Map": 1, "HashMap": 1, "TreeMap": 1, "LinkedHashMap": 1, "ConcurrentHashMap": 1,
}

func lookupMethodReturnTypeWithArgs(env *model.TypeEnv, scope, receiver, receiverType, methodName string, findByName func(string) []model.Symbol) string {
	// First try normal method lookup
	result := lookupMethodReturnType(receiverType, methodName, findByName)
	if result != "" {
		// Check if result is a generic type parameter that needs substitution
		result = substituteTypeParam(result, receiverType, receiver, env, scope, findByName)
		return result
	}
	// Container generic resolution: List<User>.get → User
	typeSeg := lastSegmentFromType(receiverType)
	if elemIdx, ok := containerElementIndex[typeSeg]; ok {
		typeArgs := lookupTypeArgs(env, scope, receiver)
		if elemIdx < len(typeArgs) {
			return typeArgs[elemIdx]
		}
	}
	return ""
}

// substituteTypeParam replaces a generic type parameter (e.g. "T") with the actual type argument.
func substituteTypeParam(returnType, receiverType, receiver string, env *model.TypeEnv, scope string, findByName func(string) []model.Symbol) string {
	// Only substitute single-letter or simple type params
	if len(returnType) > 20 || strings.Contains(returnType, ".") {
		return returnType
	}
	// Find the class definition to get TypeParams
	typeSeg := lastSegmentFromType(receiverType)
	classSymbols := findByName(typeSeg)
	for _, cls := range classSymbols {
		if len(cls.TypeParams) == 0 {
			continue
		}
		// Find which index this return type corresponds to
		for idx, tp := range cls.TypeParams {
			if tp == returnType {
				typeArgs := lookupTypeArgs(env, scope, receiver)
				if idx < len(typeArgs) {
					return typeArgs[idx]
				}
			}
		}
	}
	return returnType
}

// LookupTypeArgs returns the TypeArgs for a variable in the given scope.
func LookupTypeArgs(env *model.TypeEnv, scope, varName string) []string {
	return lookupTypeArgs(env, scope, varName)
}

func lookupTypeArgs(env *model.TypeEnv, scope, varName string) []string {
	key := scopedKey(scope, varName)
	if info, exists := env.Bindings[key]; exists && len(info.TypeArgs) > 0 {
		return info.TypeArgs
	}
	if dotIdx := strings.LastIndex(scope, "."); dotIdx >= 0 {
		classKey := scopedKey(scope[:dotIdx], varName)
		if info, exists := env.Bindings[classKey]; exists && len(info.TypeArgs) > 0 {
			return info.TypeArgs
		}
	}
	return nil
}

func lastSegmentFromType(typeName string) string {
	if dotIdx := strings.LastIndex(typeName, "."); dotIdx >= 0 {
		return typeName[dotIdx+1:]
	}
	return typeName
}

// Propagate performs cross-file type propagation.
// Returns updated TypeEnvs and list of files that need CALLS re-resolution.
// NOTE: This function modifies envs in-place for performance (avoids deep-copying all TypeEnvs).
// The caller should use the returned map and not rely on envs remaining unchanged.
func (infer *TypeInfer) Propagate(
	results []model.ParseResult,
	importGraph map[string][]string,
	envs map[string]*model.TypeEnv,
) (map[string]*model.TypeEnv, []string) {
	// Topological sort (Kahn's algorithm)
	order := topologicalSort(importGraph)

	var affectedFiles []string

	// Propagate along topological order
	for _, filePath := range order {
		env, exists := envs[filePath]
		if !exists {
			continue
		}

		// For each file this file imports, propagate exported return types
		importedFiles := importGraph[filePath]
		for _, importedFile := range importedFiles {
			importedResult := findResult(results, importedFile)
			if importedResult == nil {
				continue
			}

			changed := false
			// Propagate return types of exported functions
			for _, symbol := range importedResult.Symbols {
				if symbol.Kind != constants.KindFunction || len(symbol.ReturnTypes) == 0 {
					continue
				}
				if !symbol.IsExported {
					continue
				}
				// Tier 2b: callResult — if this file calls the imported function,
				// the return value gets the function's return type
				for _, call := range findResult(results, filePath).Calls {
					if call.CalledName == symbol.Name {
						key := scopedKey(call.CallerName, call.CalledName+"_result")
						if _, exists := env.Bindings[key]; !exists {
							env.Bindings[key] = &model.TypeInfo{
								TypeName: symbol.ReturnTypes[0],
								Tier:     2,
								Scope:    call.CallerName,
							}
							changed = true
						}
					}
				}
			}

			if changed {
				affectedFiles = append(affectedFiles, filePath)
			}
		}
	}

	return envs, affectedFiles
}

// Helper functions

// InferMultiReturn resolves "funcExpr()[N]" TypeHints using SymbolTable.
func (infer *TypeInfer) InferMultiReturn(env *model.TypeEnv, findByName func(string) []model.Symbol) {
	for round := 0; round < 3; round++ {
		changed := false
		for _, info := range env.Bindings {
			if info.MultiReturnOf == "" {
				continue
			}
			funcExpr := info.MultiReturnOf
			index := info.ReturnIndex

			var returnTypes []string

			if strings.Contains(funcExpr, ".") {
				// Try as package.Function first (e.g. "kuzu.New")
				parts := strings.SplitN(funcExpr, ".", 2)
				candidates := findByName(parts[1])
				for _, sym := range candidates {
					if sym.QualifiedName == funcExpr && len(sym.ReturnTypes) > 0 {
						returnTypes = sym.ReturnTypes
						break
					}
				}

				// Fallback: receiver.Method()
				if len(returnTypes) == 0 {
					receiverName := parts[0]
					for key, rInfo := range env.Bindings {
						if strings.HasSuffix(key, ":"+receiverName) || key == receiverName {
							if rInfo.MultiReturnOf == "" && rInfo.TypeName != "" {
								for _, sym := range candidates {
									if strings.Contains(sym.QualifiedName, rInfo.TypeName+".") && len(sym.ReturnTypes) > 0 {
										returnTypes = sym.ReturnTypes
										break
									}
								}
							}
							break
						}
					}
				}
			} else {
				candidates := findByName(funcExpr)
				for _, sym := range candidates {
					if len(sym.ReturnTypes) > 0 {
						returnTypes = sym.ReturnTypes
						break
					}
				}
			}

			if index < len(returnTypes) {
				info.TypeName = returnTypes[index]
				info.MultiReturnOf = "" // resolved
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

func resolveTypeNames(env *model.TypeEnv, imports []model.RawImport) {
	for _, info := range env.Bindings {
		for _, imp := range imports {
			if imp.SymbolName == info.TypeName {
				info.TypeName = imp.ModulePath
				break
			}
		}
	}
}

func scopedKey(scope, varName string) string {
	if scope == "" {
		return varName
	}
	return scope + ":" + varName
}

func inferConstructorType(calledName string) string {
	if calledName == "" {
		return ""
	}
	// NewXxx() → Xxx
	if strings.HasPrefix(calledName, "New") && len(calledName) > 3 {
		rest := calledName[3:]
		if len(rest) > 0 && rest[0] >= 'A' && rest[0] <= 'Z' {
			return rest
		}
	}
	// Xxx() where Xxx starts with uppercase → likely constructor
	// Exclude short names that are unlikely to be constructors
	if calledName[0] >= 'A' && calledName[0] <= 'Z' && len(calledName) > 3 {
		return calledName
	}
	return ""
}

func inferTier1FromCalls(result *model.ParseResult, env *model.TypeEnv) {
	// Look for constructor calls and try to bind them to variables
	for _, call := range result.Calls {
		typeName := inferConstructorType(call.CalledName)
		if typeName == "" {
			continue
		}
		// If this is a bare constructor call (no receiver), it might be assigned to a variable
		// We can't know the variable name from RawCall alone, but we can register
		// the type for the scope so that subsequent receiver lookups can find it
		if call.ReceiverExpr == "" {
			// Register as a potential type in the caller's scope
			key := scopedKey(call.CallerName, strings.ToLower(typeName[:1])+typeName[1:])
			if _, exists := env.Bindings[key]; !exists {
				env.Bindings[key] = &model.TypeInfo{
					TypeName: typeName,
					Tier:     1,
					Scope:    call.CallerName,
				}
			}
		}
	}
}

func inferSelfReceiver(result *model.ParseResult, env *model.TypeEnv) {
	// For methods inside a class, self/this refers to the class
	for _, symbol := range result.Symbols {
		if symbol.Kind != constants.KindFunction {
			continue
		}
		// If qualified name contains a class (e.g., "services.user_service.UserService.findById")
		lastDot := strings.LastIndex(symbol.QualifiedName, ".")
		if lastDot <= 0 {
			continue
		}
		classQualifiedName := symbol.QualifiedName[:lastDot]
		scope := symbol.QualifiedName

		// self → classQualifiedName
		for _, receiver := range []string{"self", "this"} {
			key := scopedKey(scope, receiver)
			if _, exists := env.Bindings[key]; !exists {
				env.Bindings[key] = &model.TypeInfo{
					TypeName: classQualifiedName,
					Tier:     0,
					Scope:    scope,
				}
			}
		}
	}
}

func topologicalSort(graph map[string][]string) []string {
	// Kahn's algorithm on reversed graph (dependencies before dependents)
	// graph: file → [files it imports]. We want imported files processed first.
	inDegree := make(map[string]int)
	reverseGraph := make(map[string][]string)
	for node := range graph {
		if _, exists := inDegree[node]; !exists {
			inDegree[node] = 0
		}
		for _, dep := range graph[node] {
			if _, exists := inDegree[dep]; !exists {
				inDegree[dep] = 0
			}
			reverseGraph[dep] = append(reverseGraph[dep], node)
			inDegree[node]++
		}
	}

	var queue []string
	for node, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, node)
		}
	}

	var order []string
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		order = append(order, node)

		for _, dep := range reverseGraph[node] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	// Add remaining nodes (cycles)
	orderSet := make(map[string]bool, len(order))
	for _, n := range order {
		orderSet[n] = true
	}
	for node := range inDegree {
		if !orderSet[node] {
			order = append(order, node)
		}
	}

	return order
}

func findResult(results []model.ParseResult, filePath string) *model.ParseResult {
	for i := range results {
		if results[i].FilePath == filePath {
			return &results[i]
		}
	}
	return nil
}
