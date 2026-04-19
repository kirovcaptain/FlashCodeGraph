package resolver


import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/liuymcn/flash-code-graph/internal/core/typeinfer"
	"github.com/liuymcn/flash-code-graph/internal/model"
)

// Base confidence scores by match type.
const (
	ConfidenceTypeExact  = 0.95
	ConfidenceArgCount   = 0.85
	ConfidenceSameFile   = 0.85
	ConfidenceNameUnique = 0.70
	ConfidenceTypeParent = 0.65
	ConfidenceBestGuess  = 0.25
	ConfidenceExternal   = 0.70
	ConfidenceImportPath = 1.0
)

// Resolver resolves raw calls and heritage into typed relationships.
type Resolver struct {
	symbolTable          *SymbolTable
	heritage             []model.RawHeritage
	langHelpers          map[string]LanguageHelper
	qualifiedParentMap   map[string][]string           // cached buildQualifiedParentMap result
	hierarchyCache       map[string]*model.Symbol      // cached FindMethodInHierarchy results
	heritageByChild      map[string][]model.RawHeritage // childName → heritage entries
	globalBindings       map[string]string             // classQN:fieldName → typeName
	chainedReceiverCache map[string]string             // expr:callerName:filePath → resolved type
}

// NewResolver creates a Resolver with the given SymbolTable and language helpers.
func NewResolver(symbolTable *SymbolTable, helpers ...map[string]LanguageHelper) *Resolver {
	r := &Resolver{
		symbolTable: symbolTable,
		langHelpers: make(map[string]LanguageHelper),
	}
	if len(helpers) > 0 && helpers[0] != nil {
		r.langHelpers = helpers[0]
	}
	return r
}

func (resolver *Resolver) helperFor(call model.RawCall) LanguageHelper {
	lang := call.Language
	// Backward compat: detect from file extension when Language not stamped
	if lang == "" {
		switch {
		case strings.HasSuffix(call.FilePath, ".java"):
			lang = "java"
		case strings.HasSuffix(call.FilePath, ".go"):
			lang = "go"
		case strings.HasSuffix(call.FilePath, ".py"):
			lang = "python"
		case strings.HasSuffix(call.FilePath, ".ts"), strings.HasSuffix(call.FilePath, ".tsx"):
			lang = "typescript"
		case strings.HasSuffix(call.FilePath, ".js"), strings.HasSuffix(call.FilePath, ".jsx"):
			lang = "javascript"
		}
	}
	if helper, ok := resolver.langHelpers[lang]; ok {
		return helper
	}
	panic("resolver: no helper registered for language " + lang)
}

// SetHeritage sets the heritage data for inheritance chain lookups.
func (resolver *Resolver) SetHeritage(heritage []model.RawHeritage) {
	resolver.heritage = heritage
	// Build childName → heritage entries index for O(1) lookup in BFS
	resolver.heritageByChild = make(map[string][]model.RawHeritage, len(heritage))
	for _, entry := range heritage {
		resolver.heritageByChild[entry.ChildName] = append(resolver.heritageByChild[entry.ChildName], entry)
	}
	for _, helper := range resolver.langHelpers {
		if ha, ok := helper.(HeritageAware); ok {
			ha.SetHeritage(heritage)
		}
	}
}

// ResolveCalls resolves raw function calls into CALLS relationships with confidence.
func (resolver *Resolver) ResolveCalls(calls []model.RawCall, envs map[string]*model.TypeEnv) ([]model.ResolvedRelation, []model.UnresolvedHint) {
	var relations []model.ResolvedRelation
	var hints []model.UnresolvedHint

	// Build global bindings index for O(1) field type lookup
	resolver.globalBindings = make(map[string]string)
	for _, env := range envs {
		for key, info := range env.Bindings {
			resolver.globalBindings[key] = info.TypeName
		}
	}

	for _, call := range calls {
		resolved, hint := resolver.resolveCall(call, envs)
		relations = append(relations, resolved...)
		if hint != nil {
			hints = append(hints, *hint)
		}
	}

	return relations, hints
}

func (resolver *Resolver) resolveCall(call model.RawCall, envs map[string]*model.TypeEnv) ([]model.ResolvedRelation, *model.UnresolvedHint) {
	candidates := resolver.symbolTable.FindByName(call.CalledName)

	// Filter to functions only
	var funcCandidates []model.Symbol
	for _, candidate := range candidates {
		if candidate.Kind == "function" || candidate.Kind == "class" {
			funcCandidates = append(funcCandidates, candidate)
		}
	}

	if len(funcCandidates) == 0 {
		
		langHelper := resolver.helperFor(call)
		// No project symbol matches — try import-based external resolution
		if call.ReceiverExpr != "" {
			callerID := resolver.findCallerID(call)
			// Try ReceiverExpr as class name (static call: DateUtil.format)
			if relations, handled := resolver.resolveImportedCall(call, envs, callerID, langHelper); handled && len(relations) > 0 {
				return relations, nil
			}
			// Try receiverType from TypeEnv (instance call: jdbcTemplate.query)
			receiverType := resolver.lookupReceiverType(call, envs, langHelper)
			if receiverType != "" && receiverType != call.ReceiverExpr {
				if env := envs[call.FilePath]; env != nil {
					for _, imp := range env.Imports {
						if imp.SymbolName == receiverType || (strings.Contains(receiverType, ".") && strings.HasPrefix(receiverType, imp.ModulePath)) {
							fqn := imp.ModulePath + "." + call.CalledName
							if strings.Contains(receiverType, ".") {
								fqn = receiverType + "." + call.CalledName
							}
							externalID := "external:" + fqn
							resolver.symbolTable.AddBatch([]model.Symbol{{
								ID: externalID, Name: call.CalledName,
								QualifiedName: fqn, Kind: "function", FilePath: "[external]",
							}})
							return []model.ResolvedRelation{makeRelation(callerID, externalID, call, ConfidenceExternal, "external", 1)}, nil
						}
					}
				}
			}
		}
		return nil, nil
	}

	callerID := resolver.findCallerID(call)

	langHelper := resolver.helperFor(call)

	// Try TypeEnv receiver match
	if call.ReceiverExpr != "" {
		// Step 0: super.method() — delegate to language helper
		if relations, handled := langHelper.ResolveSuperCall(call, funcCandidates, resolver.heritage, envs, callerID); handled {
			
			return relations, nil
		}

		// Step 1: try import-based full qualified name matching
		if relations, handled := resolver.resolveImportedCall(call, envs, callerID, langHelper); handled {
			
			if len(relations) == 0 {
				return nil, nil
			}
			return relations, nil
		}

		// Step 2: variable.method — lookup receiver type from TypeEnv
		receiverType := resolver.lookupReceiverType(call, envs, langHelper)
		if receiverType != "" {
			matched := filterByOwnerClass(funcCandidates, receiverType)
			if len(matched) == 0 && len(resolver.heritage) > 0 {
				// Only search hierarchy if receiverType is a project class
				if resolver.isProjectClass(receiverType) {
					if resolvedMethod := resolver.FindMethodInHierarchy(receiverType, call.CalledName, resolver.heritage); resolvedMethod != nil {
						relation := makeRelation(callerID, resolvedMethod.ID, call, ConfidenceTypeExact, "type_hierarchy", 1)
						relation.Metadata["declared_type"] = receiverType
						receiverSymbol := resolver.findClassSymbol(receiverType)
						if receiverSymbol != nil && (receiverSymbol.Kind == "interface" || receiverSymbol.Kind == "abstract_class" || resolvedMethod.IsAbstract) {
							relation.Metadata["polymorphic"] = "true"
						}
						return []model.ResolvedRelation{relation}, nil
					}
				}
			}
			if len(matched) == 1 {
				relation := makeRelation(callerID, matched[0].ID, call, ConfidenceTypeExact, "type_exact", 1)
				relation.Metadata["declared_type"] = receiverType
				return []model.ResolvedRelation{relation}, nil
			}
			if len(matched) > 1 {
				// Narrow by language-specific scope rules
				matched = langHelper.NarrowByScope(matched, call, envs[call.FilePath], resolver.symbolTable)
				if len(matched) == 1 {
					return []model.ResolvedRelation{makeRelation(callerID, matched[0].ID, call, ConfidenceTypeExact, "type_exact", 1)}, nil
				}
				sameFile := filterByFile(matched, call.FilePath)
				if len(sameFile) == 1 {
					return []model.ResolvedRelation{makeRelation(callerID, sameFile[0].ID, call, ConfidenceSameFile, "type_same_file", 1)}, nil
				}
				argMatched := filterByArgCount(matched, call.ArgCount)
				if len(argMatched) == 1 {
					return []model.ResolvedRelation{makeRelation(callerID, argMatched[0].ID, call, ConfidenceArgCount, "arg_count", 1)}, nil
				}
				if len(argMatched) > 1 {
					typeMatched := filterByArgTypes(argMatched, resolver.enrichArgTypes(call, envs, langHelper), langHelper)
					if len(typeMatched) == 1 {
						return []model.ResolvedRelation{makeRelation(callerID, typeMatched[0].ID, call, ConfidenceArgCount, "arg_type", 1)}, nil
					}
				}
				// Use argMatched if available (narrower), otherwise matched
				finalCandidates := matched
				if len(argMatched) > 0 {
					finalCandidates = argMatched
				}
				return makeMultiRelations(callerID, finalCandidates, call, ConfidenceTypeParent, "type_multi"), nil
			}
			// FQN receiver type resolved but no match — external dependency
			if strings.Contains(receiverType, ".") {
				externalQN := receiverType + "." + call.CalledName
				externalID := "external:" + externalQN
				resolver.symbolTable.AddBatch([]model.Symbol{{
					ID: externalID, Name: call.CalledName,
					QualifiedName: externalQN, Kind: "function", FilePath: "[external]",
				}})
				return []model.ResolvedRelation{makeRelation(callerID, externalID, call, ConfidenceExternal, "external", 1)}, nil
			}
			// Short receiver type (e.g. "List") resolved but no match in SymbolTable —
			// try resolving via imports to create external node
			if env := envs[call.FilePath]; env != nil {
				for _, imp := range env.Imports {
					if imp.SymbolName == receiverType {
						externalQN := imp.ModulePath + "." + call.CalledName
						externalID := "external:" + externalQN
						resolver.symbolTable.AddBatch([]model.Symbol{{
							ID: externalID, Name: call.CalledName,
							QualifiedName: externalQN, Kind: "function", FilePath: "[external]",
						}})
						return []model.ResolvedRelation{makeRelation(callerID, externalID, call, ConfidenceExternal, "external", 1)}, nil
					}
				}
			}
			// receiverType known but no exact import match.
			// Check if it's a project class via wildcard import (import com.xxx.*)
			// or same-package class (no import needed).
			if env := envs[call.FilePath]; env != nil {
				// Extract current file's package from its symbols
				callerPkg := ""
				for _, sym := range resolver.symbolTable.FindByFile(call.FilePath) {
					if sym.Kind != "class" && sym.Kind != "interface" && sym.Kind != "abstract_class" {
						continue
					}
					if idx := strings.LastIndex(sym.QualifiedName, "."+sym.Name); idx > 0 {
						callerPkg = sym.QualifiedName[:idx]
						break
					}
				}

				for _, sym := range resolver.symbolTable.FindByName(receiverType) {
					if sym.Kind != "class" && sym.Kind != "interface" && sym.Kind != "abstract_class" {
						continue
					}
					// Same package — no import needed
					symPkg := ""
					if idx := strings.LastIndex(sym.QualifiedName, "."+sym.Name); idx > 0 {
						symPkg = sym.QualifiedName[:idx]
					}
					isSamePackage := callerPkg != "" && symPkg == callerPkg

					// Wildcard import match
					isWildcardImport := false
					for _, imp := range env.Imports {
						if strings.HasPrefix(sym.QualifiedName, imp.ModulePath+".") {
							isWildcardImport = true
							break
						}
					}

					if isSamePackage || isWildcardImport {
						matched := filterByOwnerClass(funcCandidates, sym.Name)
						if len(matched) == 1 {
							return []model.ResolvedRelation{makeRelation(callerID, matched[0].ID, call, ConfidenceTypeExact, "type_exact", 1)}, nil
						}
						if len(matched) > 1 {
							argMatched := filterByArgCount(matched, call.ArgCount)
							if len(argMatched) == 1 {
								return []model.ResolvedRelation{makeRelation(callerID, argMatched[0].ID, call, ConfidenceArgCount, "arg_count", 1)}, nil
							}
							return makeMultiRelations(callerID, matched, call, ConfidenceTypeParent, "type_multi"), nil
						}
						break
					}
				}
			}
			// Not a project class — try creating external via receiverType + import
			if env := envs[call.FilePath]; env != nil {
				for _, imp := range env.Imports {
					if imp.SymbolName == receiverType {
						externalQN := imp.ModulePath + "." + call.CalledName
						externalID := "external:" + externalQN
						resolver.symbolTable.AddBatch([]model.Symbol{{
							ID: externalID, Name: call.CalledName,
							QualifiedName: externalQN, Kind: "function", FilePath: "[external]",
						}})
						return []model.ResolvedRelation{makeRelation(callerID, externalID, call, ConfidenceExternal, "external", 1)}, nil
					}
				}
			}
			return nil, nil
		}

		// Fallback: match ReceiverExpr as package prefix in QualifiedName
		// e.g. ReceiverExpr="falkor", QN="falkor.New" → match
		matched := filterByOwnerClass(funcCandidates, call.ReceiverExpr)
		if len(matched) == 1 {
			return []model.ResolvedRelation{makeRelation(callerID, matched[0].ID, call, ConfidenceSameFile, "package_prefix", 1)}, nil
		}

		// Language-specific receiver fallback (e.g., Java same-package static call)
		if relations, handled := langHelper.ResolveReceiverFallback(call, funcCandidates, envs, callerID, resolver.symbolTable); handled {
			return relations, nil
		}

		// Language controls whether to fall through to no-receiver matching
		if !langHelper.ShouldFallthrough() {
			return nil, nil
		}
	}

	// Exclude generated symbols from fallback matching
	realCandidates := langHelper.FilterGenerated(funcCandidates)

	// Same file match
	sameFile := filterByFile(realCandidates, call.FilePath)
	if len(sameFile) == 1 {
		return []model.ResolvedRelation{makeRelation(callerID, sameFile[0].ID, call, ConfidenceSameFile, "same_file", 1)}, nil
	}

	// Arg count disambiguation
	if call.ArgCount > 0 {
		argMatched := filterByArgCount(realCandidates, call.ArgCount)
		if len(argMatched) == 1 {
			return []model.ResolvedRelation{makeRelation(callerID, argMatched[0].ID, call, ConfidenceArgCount, "arg_count", 1)}, nil
		}
		if len(argMatched) > 1 {
			typeMatched := filterByArgTypes(argMatched, resolver.enrichArgTypes(call, envs, langHelper), langHelper)
			if len(typeMatched) == 1 {
				return []model.ResolvedRelation{makeRelation(callerID, typeMatched[0].ID, call, ConfidenceArgCount, "arg_type", 1)}, nil
			}
		}
	}

	// No-receiver inherited method: check caller's class hierarchy
	if call.ReceiverExpr == "" && len(resolver.heritage) > 0 {
		callerClass := call.CallerName
		if dotIdx := strings.LastIndex(callerClass, "."); dotIdx >= 0 {
			callerClass = callerClass[:dotIdx]
		}
		if sym := resolver.FindMethodInHierarchy(callerClass, call.CalledName, resolver.heritage); sym != nil {
			return []model.ResolvedRelation{makeRelation(callerID, sym.ID, call, ConfidenceTypeExact, "type_hierarchy", 1)}, nil
		}
	}

	// Global unique name
	if len(realCandidates) == 1 {
		return []model.ResolvedRelation{makeRelation(callerID, realCandidates[0].ID, call, ConfidenceNameUnique, "name_unique", 1)}, nil
	}

	// Multiple candidates — generate unresolved hint
	if len(realCandidates) > 1 {
		return nil, classifyUnresolvedCall(call, callerID, "", realCandidates)
	}
	return nil, nil
}

// classifyUnresolvedCall creates an UnresolvedHint with the appropriate hint type.
func classifyUnresolvedCall(call model.RawCall, callerID string, receiverType string, candidates []model.Symbol) *model.UnresolvedHint {
	hint := &model.UnresolvedHint{
		Line:           call.Line,
		Method:         call.CalledName,
		ReceiverExpr:   call.ReceiverExpr,
		ReceiverType:   receiverType,
		CandidateCount: len(candidates),
		FilePath:       call.FilePath,
		CallerID:       callerID,
	}
	for i, candidate := range candidates {
		if i >= 5 {
			break
		}
		hint.Candidates = append(hint.Candidates, candidate.QualifiedName)
	}

	switch {
	case call.ReceiverExpr == "super" || strings.HasPrefix(call.ReceiverExpr, "super."):
		hint.HintType = "super_field_call"
	case call.ReceiverExpr != "" && strings.Contains(call.ReceiverExpr, "("):
		hint.HintType = "chained_call"
	case call.ReceiverExpr != "" && receiverType == "" && !strings.Contains(call.ReceiverExpr, "."):
		hint.HintType = "lambda_call"
	case call.ReceiverExpr != "" && isEnumConstantPattern(call.ReceiverExpr):
		hint.HintType = "enum_method"
	case len(candidates) <= 9:
		hint.HintType = "ambiguous_project_call"
	case call.ReceiverExpr != "" && receiverType != "":
		hint.HintType = "untyped_receiver"
	default:
		hint.HintType = "untyped_receiver"
	}
	return hint
}

func isEnumConstantPattern(expr string) bool {
	dotIdx := strings.IndexByte(expr, '.')
	if dotIdx <= 0 || dotIdx >= len(expr)-1 {
		return false
	}
	className := expr[:dotIdx]
	constName := expr[dotIdx+1:]
	if len(className) == 0 || className[0] < 'A' || className[0] > 'Z' {
		return false
	}
	for _, ch := range constName {
		if ch >= 'a' && ch <= 'z' {
			return false
		}
	}
	return true
}


// resolveImportedCall tries to resolve ReceiverExpr.CalledName as a whole via imports.
// Returns resolved relations if matched, nil if no import match (caller should continue).
// Returns empty slice if import matched but no symbol found (external dependency).
func (resolver *Resolver) resolveImportedCall(call model.RawCall, envs map[string]*model.TypeEnv, callerID string, langHelper LanguageHelper) ([]model.ResolvedRelation, bool) {
	env := envs[call.FilePath]
	if env == nil {
		return nil, false
	}
	// Find import matching ReceiverExpr
	var receiverFQN string
	for _, imp := range env.Imports {
		if imp.SymbolName == call.ReceiverExpr {
			receiverFQN = imp.ModulePath
			break
		}
	}
	if receiverFQN == "" {
		return nil, false // no import match, caller should continue
	}

	// Compose full qualified name and search SymbolTable
	composedQN := receiverFQN + "." + call.CalledName
	candidates := resolver.symbolTable.FindByName(call.CalledName)
	var matched []model.Symbol
	for _, candidate := range candidates {
		if candidate.QualifiedName == composedQN || strings.HasSuffix(candidate.QualifiedName, composedQN) {
			matched = append(matched, candidate)
		}
	}
	if len(matched) == 1 {
		return []model.ResolvedRelation{makeRelation(callerID, matched[0].ID, call, ConfidenceTypeExact, "import_exact", 1)}, true
	}
	if len(matched) > 1 {
		argMatched := filterByArgCount(matched, call.ArgCount)
		if len(argMatched) == 1 {
			return []model.ResolvedRelation{makeRelation(callerID, argMatched[0].ID, call, ConfidenceArgCount, "import_arg_count", 1)}, true
		}
		if len(argMatched) > 1 {
			typeMatched := filterByArgTypes(argMatched, resolver.enrichArgTypes(call, envs, langHelper), langHelper)
			if len(typeMatched) == 1 {
				return []model.ResolvedRelation{makeRelation(callerID, typeMatched[0].ID, call, ConfidenceArgCount, "import_arg_type", 1)}, true
			}
		}
		return makeMultiRelations(callerID, matched, call, ConfidenceTypeParent, "import_multi"), true
	}

	// 0 match — try inheritance chain
	if len(resolver.heritage) > 0 {
		if resolvedMethod := resolver.FindMethodInHierarchy(receiverFQN, call.CalledName, resolver.heritage); resolvedMethod != nil {
			relation := makeRelation(callerID, resolvedMethod.ID, call, ConfidenceTypeExact, "import_hierarchy", 1)
			relation.Metadata["declared_type"] = receiverFQN
			receiverSymbol := resolver.findClassSymbol(receiverFQN)
			if receiverSymbol != nil && (receiverSymbol.Kind == "interface" || receiverSymbol.Kind == "abstract_class" || resolvedMethod.IsAbstract) {
				relation.Metadata["polymorphic"] = "true"
			}
			return []model.ResolvedRelation{relation}, true
		}
	}

	// Import matched but symbol not found — external dependency, create virtual node
	externalQN := receiverFQN + "." + call.CalledName
	externalID := "external:" + externalQN
	resolver.symbolTable.AddBatch([]model.Symbol{{
		ID:            externalID,
		Name:          call.CalledName,
		QualifiedName: externalQN,
		Kind:          "function",
		FilePath:      "[external]",
	}})
	return []model.ResolvedRelation{makeRelation(callerID, externalID, call, ConfidenceExternal, "external", 1)}, true
}


// resolveChainedReceiver resolves "obj.method()" or "obj.method().method2()" receiver chains.
// Returns the type of the final method's return value.
func (resolver *Resolver) resolveChainedReceiver(expr string, call model.RawCall, envs map[string]*model.TypeEnv, langHelper LanguageHelper) string {
	// Cache lookup
	cacheKey := expr + ":" + call.CallerName + ":" + call.FilePath
	if resolver.chainedReceiverCache == nil {
		resolver.chainedReceiverCache = make(map[string]string)
	}
	if cached, exists := resolver.chainedReceiverCache[cacheKey]; exists {
		return cached
	}

	result := resolver.resolveChainedReceiverInternal(expr, call, envs, langHelper)
	resolver.chainedReceiverCache[cacheKey] = result
	return result
}

func (resolver *Resolver) resolveChainedReceiverInternal(expr string, call model.RawCall, envs map[string]*model.TypeEnv, langHelper LanguageHelper) string {
	// Find the last "." that separates receiver from method, respecting parentheses
	parenDepth := 0
	lastDot := -1
	for i := len(expr) - 1; i >= 0; i-- {
		switch expr[i] {
		case ')':
			parenDepth++
		case '(':
			parenDepth--
		case '.':
			if parenDepth == 0 {
				lastDot = i
			}
		}
		if lastDot >= 0 {
			break
		}
	}
	if lastDot < 0 {
		return ""
	}

	baseExpr := expr[:lastDot]
	methodPart := expr[lastDot+1:]
	// Strip trailing "()" or "(args)"
	if parenIdx := strings.IndexByte(methodPart, '('); parenIdx >= 0 {
		methodPart = methodPart[:parenIdx]
	}
	methodName := methodPart

	// Resolve base receiver type
	var baseType string
	if strings.Contains(baseExpr, ".") {
		baseType = resolver.resolveChainedReceiver(baseExpr, call, envs, langHelper)
	} else {
		// Simple variable — lookup in TypeEnv
		baseCall := model.RawCall{ReceiverExpr: baseExpr, CallerName: call.CallerName, FilePath: call.FilePath}
		baseType = resolver.lookupReceiverType(baseCall, envs, langHelper)
	}
	if baseType == "" {
		// Try enum constant pattern: ClassName.CONSTANT → type is ClassName
		if !strings.Contains(baseExpr, "(") && strings.Contains(baseExpr, ".") {
			dotIdx := strings.Index(baseExpr, ".")
			className := baseExpr[:dotIdx]
			// Check if className is an enum/class in SymbolTable
			for _, sym := range resolver.symbolTable.FindByName(className) {
				if sym.Kind == "class" || sym.ClassType == "enum" {
					baseType = className
					break
				}
			}
		}
		if baseType == "" {
			return ""
		}
	}

	// Find method return type or field type on baseType
	typeSeg := baseType
	if dotIdx := strings.LastIndex(baseType, "."); dotIdx >= 0 {
		typeSeg = baseType[dotIdx+1:]
	}

	// Check if this is a field access (no parentheses in original expr after the dot)
	isFieldAccess := !strings.Contains(expr[lastDot:], "(")
	if isFieldAccess {
		env := envs[call.FilePath]
		if env != nil {
			// Direct lookup (baseType is already fully qualified for Python/TS)
			fieldKey := baseType + ":" + methodName
			if info, exists := env.Bindings[fieldKey]; exists {
				return info.TypeName
			}
			// Short name → fully qualified name lookup (Go/Java baseType is short name)
			for _, candidate := range resolver.symbolTable.FindByName(baseType) {
				if candidate.Kind == "class" || candidate.Kind == "abstract_class" ||
					candidate.Kind == "interface" || candidate.ClassType == "struct" {
					qualifiedKey := candidate.QualifiedName + ":" + methodName
					if info, exists := env.Bindings[qualifiedKey]; exists {
						return info.TypeName
					}
				}
			}
		}
		return ""
	}

	// Method call: find return type in SymbolTable
	candidates := resolver.symbolTable.FindByName(methodName)
	for _, candidate := range candidates {
		if candidate.Kind == "function" && strings.Contains(candidate.QualifiedName, typeSeg+".") && len(candidate.ReturnTypes) > 0 {
			retType := candidate.ReturnTypes[0]
			retType = resolver.substituteGenericParam(retType, baseType, baseExpr, call, envs)
			return retType
		}
	}

	// Fallback: language-specific method return type lookup
	if methodRet, ok := langHelper.LookupMethodReturn(typeSeg, methodName); ok {
		switch methodRet {
		case "":
			return "" // terminal operation
		case "SELF":
			return baseType
		default:
			return methodRet
		}
	}

	return ""
}

// substituteGenericParam replaces a type parameter (e.g. "T") with the actual type argument
// by looking up the class's TypeParams and the receiver variable's TypeArgs.
func (resolver *Resolver) substituteGenericParam(retType, receiverType, receiverVar string, call model.RawCall, envs map[string]*model.TypeEnv) string {
	if len(retType) > 20 || strings.Contains(retType, ".") {
		return retType
	}
	typeSeg := receiverType
	if dotIdx := strings.LastIndex(receiverType, "."); dotIdx >= 0 {
		typeSeg = receiverType[dotIdx+1:]
	}
	// Find class with TypeParams
	classSymbols := resolver.symbolTable.FindByName(typeSeg)
	for _, cls := range classSymbols {
		if len(cls.TypeParams) == 0 {
			continue
		}
		for idx, tp := range cls.TypeParams {
			if tp == retType {
				// Get TypeArgs from TypeEnv
				env := envs[call.FilePath]
				if env == nil {
					return retType
				}
				typeArgs := typeinfer.LookupTypeArgs(env, call.CallerName, receiverVar)
				if idx < len(typeArgs) {
					return typeArgs[idx]
				}
			}
		}
	}
	return retType
}

// lookupFieldInHierarchy looks up a field's type by walking the inheritance chain.
// Checks current class scope first, then parent classes (may be in different files).
func (resolver *Resolver) lookupReceiverType(call model.RawCall, envs map[string]*model.TypeEnv, langHelper LanguageHelper) string {
	env := envs[call.FilePath]
	if env == nil {
		return ""
	}

	receiver := call.ReceiverExpr
	receiver = strings.TrimPrefix(receiver, "self.")
	receiver = strings.TrimPrefix(receiver, "this.")

	// Chain resolution: "callback.getData()" → resolve recursively
	if strings.Contains(receiver, ".") {
		return resolver.resolveChainedReceiver(receiver, call, envs, langHelper)
	}

	key := call.CallerName + ":" + receiver
	if info, exists := env.Bindings[key]; exists {
		return info.TypeName
	}

	// Try class scope (field TypeHint) — walk up inheritance chain
	if typeName := resolver.lookupFieldInHierarchy(call.CallerName, receiver, envs); typeName != "" {
		return typeName
	}

	// self/this receiver
	if receiver == "self" || receiver == "this" {
		selfKey := call.CallerName + ":" + receiver
		if info, exists := env.Bindings[selfKey]; exists {
			return info.TypeName
		}
	}

	return ""
}

// isProjectClass checks if a type name corresponds to a class/interface in the project.
func (resolver *Resolver) isProjectClass(typeName string) bool {
	sym := resolver.findClassSymbol(typeName)
	return sym != nil
}

// findClassSymbol returns the class/interface Symbol for a type name, or nil if not found.
func (resolver *Resolver) findClassSymbol(typeName string) *model.Symbol {
	candidates := resolver.symbolTable.FindByName(typeName)
	for _, candidate := range candidates {
		if candidate.Kind == "class" || candidate.Kind == "abstract_class" ||
			candidate.Kind == "interface" || candidate.ClassType == "struct" {
			matched := candidate
			return &matched
		}
	}
	if strings.Contains(typeName, ".") {
		candidates = resolver.symbolTable.FindByQualifiedName(typeName)
		for _, candidate := range candidates {
			if candidate.Kind == "class" || candidate.Kind == "abstract_class" ||
				candidate.Kind == "interface" || candidate.ClassType == "struct" {
				matched := candidate
				return &matched
			}
		}
	}
	return nil
}

func (resolver *Resolver) findCallerID(call model.RawCall) string {
	candidates := resolver.symbolTable.FindByName(lastSegment(call.CallerName))
	var fallback string
	for _, candidate := range candidates {
		if candidate.FilePath == call.FilePath && candidate.Kind == "function" {
			if fallback == "" {
				fallback = candidate.ID
			}
			if call.Line >= candidate.StartLine && call.Line <= candidate.EndLine {
				return candidate.ID
			}
		}
	}
	if fallback != "" {
		return fallback
	}
	return "caller:" + call.CallerName + ":" + call.FilePath
}

// ResolveImports resolves import statements into IMPORTS relationships.
func (resolver *Resolver) ResolveImports(imports []model.RawImport, allFiles []string) []model.ResolvedRelation {
	var relations []model.ResolvedRelation

	fileIndex := make(map[string]string) // basename → full path
	for _, filePath := range allFiles {
		// Strip extension first, then extract last segment
		base := filePath
		for _, ext := range []string{".java", ".py", ".go", ".ts", ".tsx", ".js", ".jsx"} {
			base = strings.TrimSuffix(base, ext)
		}
		base = lastSegment(strings.ReplaceAll(base, "/", "."))
		fileIndex[base] = filePath
	}

	for _, imp := range imports {
		sourceFileID := "file:" + imp.FilePath

		// Try to match import to a file
		targetPath := ""
		if imp.SymbolName != "" {
			if path, exists := fileIndex[imp.SymbolName]; exists {
				targetPath = path
			}
		}
		// Try module path last segment
		if targetPath == "" {
			lastSeg := lastSegment(strings.ReplaceAll(imp.ModulePath, "/", "."))
			if path, exists := fileIndex[lastSeg]; exists {
				targetPath = path
			}
		}

		if targetPath != "" {
			relations = append(relations, model.ResolvedRelation{
				SourceID:   sourceFileID,
				TargetID:   "file:" + targetPath,
				Kind:       model.RelImports,
				Confidence: ConfidenceImportPath,
				ResolvedBy: "import_path",
				Candidates: 1,
				Line:       imp.Line,
				Metadata:   map[string]string{"symbol": imp.SymbolName, "module": imp.ModulePath},
			})
		}
	}

	return relations
}

// Helper functions

func filterByOwnerClass(candidates []model.Symbol, className string) []model.Symbol {
	className = strings.TrimPrefix(className, "*")
	target := "." + className + "."
	var matched []model.Symbol
	for _, candidate := range candidates {
		qn := "." + candidate.QualifiedName
		if strings.Contains(qn, target) {
			matched = append(matched, candidate)
		}
	}
	return matched
}

func filterByFile(candidates []model.Symbol, filePath string) []model.Symbol {
	var matched []model.Symbol
	for _, candidate := range candidates {
		if candidate.FilePath == filePath {
			matched = append(matched, candidate)
		}
	}
	return matched
}

func filterByArgCount(candidates []model.Symbol, argCount int) []model.Symbol {
	var matched []model.Symbol
	for _, candidate := range candidates {
		params := parseParamTypes(candidate.Params)
		paramCount := len(params)
		isVarargs := paramCount > 0 && strings.HasSuffix(params[paramCount-1], "...")
		if isVarargs {
			// varargs: at least (paramCount - 1) fixed args
			if argCount >= paramCount-1 {
				matched = append(matched, candidate)
			}
		} else if paramCount == argCount {
			matched = append(matched, candidate)
		}
	}
	return matched
}

func countParams(paramsJSON string) int {
	if paramsJSON == "" || paramsJSON == "null" {
		return 0
	}
	var params []map[string]any
	if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
		return 0
	}
	return len(params)
}


// enrichArgTypes supplements empty ArgTypes entries using ArgExprs + TypeEnv/SymbolTable.
func (resolver *Resolver) enrichArgTypes(call model.RawCall, envs map[string]*model.TypeEnv, langHelper LanguageHelper) []string {
	result := make([]string, len(call.ArgTypes))
	copy(result, call.ArgTypes)
	env := envs[call.FilePath]
	for i, argType := range result {
		if argType != "" || i >= len(call.ArgExprs) {
			continue
		}
		result[i] = resolver.inferExprType(call.ArgExprs[i], call, env, envs, langHelper)
	}
	return result
}

func (resolver *Resolver) inferExprType(expr string, call model.RawCall, env *model.TypeEnv, envs map[string]*model.TypeEnv, langHelper LanguageHelper) string {
	if expr == "" {
		return ""
	}
	// 0. String concatenation: any expr containing + and a string literal is String
	if strings.Contains(expr, "+") && strings.Contains(expr, "\"") {
		return "String"
	}
	// 1. Simple variable — lookup in TypeEnv
	if !strings.Contains(expr, ".") && !strings.Contains(expr, "(") {
		if env != nil {
			// Method scope
			key := call.CallerName + ":" + expr
			if info, ok := env.Bindings[key]; ok {
				return extractSimpleType(info.TypeName)
			}
			// Class scope (field TypeHint) — walk up inheritance chain
			if typeName := resolver.lookupFieldInHierarchy(call.CallerName, expr, envs); typeName != "" {
				return extractSimpleType(typeName)
			}
			// Static import match
			for _, imp := range env.Imports {
				syms := resolver.symbolTable.FindByQualifiedName(imp.ModulePath + "." + expr)
				if len(syms) > 0 {
					return imp.SymbolName
				}
			}
		}
		return ""
	}
	// 2. Method call — lookup return type in SymbolTable
	if strings.HasSuffix(expr, ")") && !strings.Contains(expr, ".") {
		methodName := expr[:strings.Index(expr, "(")]
		candidates := resolver.symbolTable.FindByName(methodName)
		for _, candidate := range candidates {
			if candidate.Kind == "function" && candidate.FilePath == call.FilePath && len(candidate.ReturnTypes) > 0 {
				return extractSimpleType(candidate.ReturnTypes[0])
			}
		}
		return ""
	}
	// 3. obj.method() — resolve receiver type then lookup method return type
	if strings.HasSuffix(expr, ")") && strings.Contains(expr, ".") {
		dotIdx := strings.LastIndex(expr, ".")
		objPart := expr[:dotIdx]
		methodPart := expr[dotIdx+1:]
		if parenIdx := strings.Index(methodPart, "("); parenIdx >= 0 {
			methodName := methodPart[:parenIdx]
			// Resolve obj type — strip trailing () if obj is also a call
			objType := resolver.inferExprType(objPart, call, env, envs, langHelper)
			if objType == "" && !strings.Contains(objPart, ".") && !strings.Contains(objPart, "(") {
				// Simple variable
				objType = resolver.inferExprType(objPart, call, env, envs, langHelper)
			}
			if objType != "" {
				// Lookup method return type on objType
				candidates := resolver.symbolTable.FindByName(methodName)
				for _, candidate := range candidates {
					if candidate.Kind == "function" && strings.Contains(candidate.QualifiedName, objType+".") && len(candidate.ReturnTypes) > 0 {
						return extractSimpleType(candidate.ReturnTypes[0])
					}
				}
				// Try JDK table
				if ret, ok := langHelper.LookupMethodReturn(objType, methodName); ok && ret != "" && ret != "SELF" {
					return ret
				}
			}
		}
	}
	return ""
}

func extractSimpleType(typeName string) string {
	if dotIdx := strings.LastIndex(typeName, "."); dotIdx >= 0 {
		return typeName[dotIdx+1:]
	}
	return typeName
}
// filterByArgTypes uses exclusion to eliminate candidates whose parameter types
// are definitely incompatible with the call-site argument types.
func filterByArgTypes(candidates []model.Symbol, argTypes []string, langHelper LanguageHelper) []model.Symbol {
	if len(argTypes) == 0 {
		return candidates
	}
	var remaining []model.Symbol
	for _, candidate := range candidates {
		params := parseParamTypes(candidate.Params)
		isVarargs := len(params) > 0 && strings.HasSuffix(params[len(params)-1], "...")
		if !isVarargs && len(params) != len(argTypes) {
			continue
		}
		if isVarargs && len(argTypes) < len(params)-1 {
			continue
		}
		excluded := false
		for i, argType := range argTypes {
			if argType == "" || argType == "null" {
				continue
			}
			var paramType string
			if i < len(params) {
				paramType = params[i]
			} else if isVarargs {
				paramType = params[len(params)-1]
			} else {
				excluded = true
				break
			}
			if isSingleLetterGeneric(paramType) {
				continue
			}
			paramType = strings.TrimSuffix(paramType, "...")
			if strings.HasPrefix(argType, "!") {
				// Exclusion hint: "!ResponseCode" means definitely NOT ResponseCode
				if argType[1:] == paramType {
					excluded = true
					break
				}
			} else if !langHelper.IsTypeAssignable(argType, paramType) {
				excluded = true
				break
			}
		}
		if !excluded {
			remaining = append(remaining, candidate)
		}
	}
	if len(remaining) == 0 {
		return candidates // fallback: don't lose all candidates
	}
	// When multiple candidates remain, select the most specific match
	// (closest parameter types in inheritance hierarchy)
	// Only when at least one arg type is known
	if len(remaining) > 1 && len(argTypes) > 0 {
		hasKnownType := false
		for _, argType := range argTypes {
			if argType != "" && argType != "null" {
				hasKnownType = true
				break
			}
		}
		if hasKnownType {
			best := langHelper.ResolveOverload(remaining, argTypes)
			if best != nil {
				return []model.Symbol{*best}
			}
		}
	}
	// Prefer fixed-param overloads over varargs even without type info
	if len(remaining) > 1 {
		var fixed []model.Symbol
		for _, candidate := range remaining {
			params := parseParamTypes(candidate.Params)
			if len(params) == 0 || !strings.HasSuffix(params[len(params)-1], "...") {
				fixed = append(fixed, candidate)
			}
		}
		if len(fixed) > 0 && len(fixed) < len(remaining) {
			remaining = fixed
		}
	}
	return remaining
}

func parseParamTypes(paramsJSON string) []string {
	if paramsJSON == "" || paramsJSON == "null" {
		return nil
	}
	var params []map[string]any
	if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
		return nil
	}
	types := make([]string, len(params))
	for i, param := range params {
		if typeName, ok := param["type"].(string); ok {
			types[i] = typeName
		}
	}
	return types
}

func isSingleLetterGeneric(typeName string) bool {
	return len(typeName) == 1 && typeName[0] >= 'A' && typeName[0] <= 'Z'
}

func makeRelation(sourceID, targetID string, call model.RawCall, confidence float64, resolvedBy string, candidates int) model.ResolvedRelation {
	return model.ResolvedRelation{
		SourceID:    sourceID,
		TargetID:    targetID,
		Kind:        model.RelCalls,
		SourceKind:  "Function",
		Confidence:  confidence,
		ResolvedBy:  resolvedBy,
		Candidates:  candidates,
		Line:        call.Line,
		FlowContext: call.FlowContext,
		FlowLine:    call.FlowLine,
		Metadata: map[string]string{"file_path": call.FilePath, "arg_count": strconv.Itoa(call.ArgCount), "called_name": call.CalledName, "receiver_expr": call.ReceiverExpr},
	}
}

func makeMultiRelations(sourceID string, candidates []model.Symbol, call model.RawCall, baseConfidence float64, resolvedBy string) []model.ResolvedRelation {
	var relations []model.ResolvedRelation
	confidence := baseConfidence / float64(len(candidates))
	for _, candidate := range candidates {
		relations = append(relations, model.ResolvedRelation{
			SourceID:    sourceID,
			TargetID:    candidate.ID,
			Kind:        model.RelCalls,
			SourceKind:  "Function",
			Confidence:  confidence,
			ResolvedBy:  resolvedBy,
			Candidates:  len(candidates),
			Line:        call.Line,
			FlowContext: call.FlowContext,
			FlowLine:    call.FlowLine,
			Metadata:    map[string]string{"file_path": call.FilePath, "arg_count": strconv.Itoa(call.ArgCount), "called_name": call.CalledName, "receiver_expr": call.ReceiverExpr},
		})
	}
	return relations
}


