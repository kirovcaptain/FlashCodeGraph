package resolver


import (
	"strconv"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/typeinfer"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// Base confidence scores by match type — defined in constants/confidence.go.
const (
	ConfidenceTypeExact  = constants.ConfidenceTypeExact
	ConfidenceArgCount   = constants.ConfidenceArgCount
	ConfidenceSameFile   = constants.ConfidenceSameFile
	ConfidenceNameUnique = constants.ConfidenceNameUnique
	ConfidenceTypeParent = constants.ConfidenceTypeParent
	ConfidenceBestGuess  = constants.ConfidenceBestGuess
	ConfidenceExternal   = constants.ConfidenceExternal
	ConfidenceImportPath = constants.ConfidenceImportPath
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

// NewResolver creates a Resolver with the given SymbolTable and optional language-specific helpers.
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

// helperFor returns the language-specific helper for a call based on file extension.
// Panics if no helper is registered for the detected language.
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

// resolveCall resolves a single raw call to one or more target symbols.
// Returns resolved relations (with confidence scores) or an unresolved hint.
func (resolver *Resolver) resolveCall(call model.RawCall, envs map[string]*model.TypeEnv) ([]model.ResolvedRelation, *model.UnresolvedHint) {
	// Step 1: Find candidate symbols by name from the SymbolTable.
	candidates := resolver.symbolTable.FindByName(call.CalledName)

	// Step 2: Filter to functions and classes only (exclude variables, interfaces, etc.)
	var funcCandidates []model.Symbol
	for _, candidate := range candidates {
		if candidate.Kind == constants.KindFunction || candidate.Kind == constants.KindClass {
			funcCandidates = append(funcCandidates, candidate)
		}
	}

	// Step 3: No candidates found — the called name doesn't match any project symbol.
	// This typically means the call targets a third-party library (e.g. "jdbcTemplate.query",
	// "DateUtil.format") that is not in the indexed source code. Attempt to resolve as external.
	if len(funcCandidates) == 0 {
		return resolver.resolveCallNoCandidate(call, envs)
	}

	callerID := resolver.findCallerID(call)
	langHelper := resolver.helperFor(call)

	// Step 4: Has receiver expression — resolve using type information.
	if call.ReceiverExpr != "" {
		relations, hint, shouldFallthrough := resolver.resolveCallWithReceiver(call, envs, funcCandidates, callerID, langHelper)
		if !shouldFallthrough {
			return relations, hint
		}
		// Language helper allows fallthrough to no-receiver matching.
	}

	// Step 5: No receiver or fallthrough — use fallback strategies.
	return resolver.resolveCallFallback(call, envs, funcCandidates, callerID, langHelper)
}

// resolveCallNoCandidate handles calls where no project symbol matches the called name.
// Attempts to resolve as an external dependency via import information.
func (resolver *Resolver) resolveCallNoCandidate(call model.RawCall, envs map[string]*model.TypeEnv) ([]model.ResolvedRelation, *model.UnresolvedHint) {
	langHelper := resolver.helperFor(call)

	if call.ReceiverExpr == "" {
		return nil, nil
	}

	callerID := resolver.findCallerID(call)

	// Try ReceiverExpr as class name (static call: e.g. DateUtil.format)
	if relations, handled := resolver.resolveImportedCall(call, envs, callerID, langHelper); handled && len(relations) > 0 {
		return relations, nil
	}

	// Try receiverType from TypeEnv (instance call: e.g. jdbcTemplate.query)
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
						QualifiedName: fqn, Kind: constants.KindFunction, FilePath: constants.FilePathExternal,
					}})
					return []model.ResolvedRelation{makeRelation(callerID, externalID, call, ConfidenceExternal, "external", 1)}, nil
				}
			}
		}
	}
	return nil, nil
}

// resolveCallWithReceiver resolves a call that has a receiver expression (e.g. "svc.findById()").
// Tries multiple strategies in priority order: super call, import match, TypeEnv lookup,
// hierarchy walk, wildcard import, and package prefix.
// Returns (relations, hint, shouldFallthrough). If fallthrough=true, caller should continue to fallback.
func (resolver *Resolver) resolveCallWithReceiver(
	call model.RawCall,
	envs map[string]*model.TypeEnv,
	funcCandidates []model.Symbol,
	callerID string,
	langHelper LanguageHelper,
) ([]model.ResolvedRelation, *model.UnresolvedHint, bool) {

	// Strategy 1: super.method() — delegate to language helper for parent class resolution.
	if relations, handled := langHelper.ResolveSuperCall(call, funcCandidates, resolver.heritage, envs, callerID); handled {
		return relations, nil, false
	}

	// Strategy 2: Import-based fully qualified name matching.
	// e.g. import "com.example.UserService" + call "UserService.findById" → match by FQN.
	if relations, handled := resolver.resolveImportedCall(call, envs, callerID, langHelper); handled {
		if len(relations) == 0 {
			return nil, nil, false
		}
		return relations, nil, false
	}

	// Strategy 3: Lookup receiver type from TypeEnv and match against candidates.
	receiverType := resolver.lookupReceiverType(call, envs, langHelper)
	if receiverType != "" {
		if relations, hint, done := resolver.resolveByReceiverType(call, envs, funcCandidates, callerID, langHelper, receiverType); done {
			return relations, hint, false
		}
	}

	// Strategy 4: Match ReceiverExpr as package prefix in QualifiedName.
	// e.g. ReceiverExpr="falkor", QN="falkor.New" → match.
	matched := filterByOwnerClass(funcCandidates, call.ReceiverExpr)
	if len(matched) == 1 {
		return []model.ResolvedRelation{makeRelation(callerID, matched[0].ID, call, ConfidenceSameFile, "package_prefix", 1)}, nil, false
	}

	// Strategy 5: Language-specific receiver fallback (e.g. Java same-package static call).
	if relations, handled := langHelper.ResolveReceiverFallback(call, funcCandidates, envs, callerID, resolver.symbolTable); handled {
		return relations, nil, false
	}

	// Language controls whether to fall through to no-receiver matching.
	if !langHelper.ShouldFallthrough() {
		return nil, nil, false
	}
	return nil, nil, true
}

// resolveByReceiverType resolves a call using the known receiver type from TypeEnv.
// Handles exact match, hierarchy walk, arg count disambiguation, wildcard imports, and external fallback.
func (resolver *Resolver) resolveByReceiverType(
	call model.RawCall,
	envs map[string]*model.TypeEnv,
	funcCandidates []model.Symbol,
	callerID string,
	langHelper LanguageHelper,
	receiverType string,
) ([]model.ResolvedRelation, *model.UnresolvedHint, bool) {

	// Resolve short type name to fully qualified name before matching and writing declared_type
	receiverType = resolver.resolveFullQualifiedType(receiverType, envs[call.FilePath])

	// Filter candidates to those belonging to the receiver's class.
	matched := filterByOwnerClass(funcCandidates, receiverType)

	if len(matched) == 0 {
		// No method found in receiverType's class.
		// Case: receiverType="Animal", call="speak()", but speak() is defined in parent "Dog"
		//   → try walking inheritance chain.
		// Case: receiverType="JdbcTemplate", not a project class
		//   → create external dependency node.
		if relations, hint, done := resolver.resolveByHierarchyWalk(call, callerID, receiverType); done {
			return relations, hint, true
		}
		return resolver.resolveAsExternalDependency(call, envs, funcCandidates, callerID, receiverType)
	}

	if len(matched) == 1 {
		// Exactly one method in the receiver's class matches.
		// Case: receiverType="UserService", only one "findById" in UserService → direct match.
		return resolver.resolveExactMatch(call, callerID, matched[0], receiverType)
	}

	// Multiple methods with the same name in the receiver's class (overloaded methods).
	// Case: receiverType="LoggerUtil", matched=[error(String,Object), error(String,Throwable)]
	//   → disambiguate by arg count, arg types, scope, etc.
	return resolver.disambiguateMultipleMatches(call, envs, matched, callerID, langHelper, receiverType)
}

// resolveByHierarchyWalk walks the inheritance chain to find a parent method matching the call.
// Returns done=false if no heritage data or no match found (caller should try other strategies).
func (resolver *Resolver) resolveByHierarchyWalk(call model.RawCall, callerID, receiverType string) ([]model.ResolvedRelation, *model.UnresolvedHint, bool) {
	if len(resolver.heritage) == 0 {
		return nil, nil, false
	}
	if !resolver.isProjectClass(receiverType) {
		return nil, nil, false
	}
	resolvedMethod := resolver.FindMethodInHierarchy(receiverType, call.CalledName, resolver.heritage)
	if resolvedMethod == nil {
		return nil, nil, false
	}
	relation := makeRelation(callerID, resolvedMethod.ID, call, ConfidenceTypeExact, "type_hierarchy", 1)
	relation.Metadata["declared_type"] = receiverType
	receiverSymbol := resolver.findClassSymbol(receiverType)
	if receiverSymbol != nil && (receiverSymbol.Kind == constants.KindInterface || receiverSymbol.Kind == "abstract_class" || resolvedMethod.IsAbstract) {
		relation.Metadata["polymorphic"] = "true"
	}
	return []model.ResolvedRelation{relation}, nil, true
}

// resolveExactMatch handles the case where exactly one candidate matches the receiver type.
func (resolver *Resolver) resolveExactMatch(call model.RawCall, callerID string, matched model.Symbol, receiverType string) ([]model.ResolvedRelation, *model.UnresolvedHint, bool) {
	relation := makeRelation(callerID, matched.ID, call, ConfidenceTypeExact, "type_exact", 1)
	relation.Metadata["declared_type"] = receiverType
	return []model.ResolvedRelation{relation}, nil, true
}

// disambiguateMultipleMatches narrows down multiple candidates using scope rules,
// same-file proximity, argument count, and argument type matching.
// receiverType is the fully qualified class name for setting declared_type on resolved relations.
func (resolver *Resolver) disambiguateMultipleMatches(
	call model.RawCall,
	envs map[string]*model.TypeEnv,
	matched []model.Symbol,
	callerID string,
	langHelper LanguageHelper,
	receiverType string,
) ([]model.ResolvedRelation, *model.UnresolvedHint, bool) {
	// Narrow by language-specific scope rules.
	matched = langHelper.NarrowByScope(matched, call, envs[call.FilePath], resolver.symbolTable)
	if len(matched) == 1 {
		rel := makeRelation(callerID, matched[0].ID, call, ConfidenceTypeExact, "type_exact", 1)
		rel.Metadata["declared_type"] = receiverType
		return []model.ResolvedRelation{rel}, nil, true
	}
	// Same file proximity.
	sameFile := filterByFile(matched, call.FilePath)
	if len(sameFile) == 1 {
		rel := makeRelation(callerID, sameFile[0].ID, call, ConfidenceSameFile, "type_same_file", 1)
		rel.Metadata["declared_type"] = receiverType
		return []model.ResolvedRelation{rel}, nil, true
	}
	// Argument count.
	argMatched := filterByArgCount(matched, call.ArgCount)
	if len(argMatched) == 1 {
		rel := makeRelation(callerID, argMatched[0].ID, call, ConfidenceArgCount, "arg_count", 1)
		rel.Metadata["declared_type"] = receiverType
		return []model.ResolvedRelation{rel}, nil, true
	}
	// Argument type matching.
	if len(argMatched) > 1 {
		typeMatched := filterByArgTypes(argMatched, resolver.enrichArgTypes(call, envs, langHelper), langHelper)
		if len(typeMatched) == 1 {
			rel := makeRelation(callerID, typeMatched[0].ID, call, ConfidenceArgCount, "arg_type", 1)
			rel.Metadata["declared_type"] = receiverType
			return []model.ResolvedRelation{rel}, nil, true
		}
	}
	// Still ambiguous — return all with lower confidence.
	finalCandidates := matched
	if len(argMatched) > 0 {
		finalCandidates = argMatched
	}
	relations := makeMultiRelations(callerID, finalCandidates, call, ConfidenceTypeParent, "type_multi")
	for i := range relations {
		relations[i].Metadata["declared_type"] = receiverType
	}
	return relations, nil, true
}

// resolveAsExternalDependency creates virtual external nodes when the receiver type
// doesn't match any project symbol. Tries FQN, import lookup, wildcard import, and same-package.
func (resolver *Resolver) resolveAsExternalDependency(
	call model.RawCall,
	envs map[string]*model.TypeEnv,
	funcCandidates []model.Symbol,
	callerID string,
	receiverType string,
) ([]model.ResolvedRelation, *model.UnresolvedHint, bool) {
	// FQN receiver type (e.g. "com.example.UserService") — create external node directly.
	if strings.Contains(receiverType, ".") {
		externalQN := receiverType + "." + call.CalledName
		externalID := "external:" + externalQN
		resolver.symbolTable.AddBatch([]model.Symbol{{
			ID: externalID, Name: call.CalledName,
			QualifiedName: externalQN, Kind: constants.KindFunction, FilePath: constants.FilePathExternal,
		}})
		return []model.ResolvedRelation{makeRelation(callerID, externalID, call, ConfidenceExternal, "external", 1)}, nil, true
	}

	// Short receiver type (e.g. "List") — try resolving via direct import match.
	if env := envs[call.FilePath]; env != nil {
		for _, imp := range env.Imports {
			if imp.SymbolName == receiverType {
				externalQN := imp.ModulePath + "." + call.CalledName
				externalID := "external:" + externalQN
				resolver.symbolTable.AddBatch([]model.Symbol{{
					ID: externalID, Name: call.CalledName,
					QualifiedName: externalQN, Kind: constants.KindFunction, FilePath: constants.FilePathExternal,
				}})
				return []model.ResolvedRelation{makeRelation(callerID, externalID, call, ConfidenceExternal, "external", 1)}, nil, true
			}
		}
	}

	// Try wildcard import or same-package resolution.
	if relations, hint, done := resolver.resolveByWildcardOrSamePackage(call, envs, funcCandidates, callerID, receiverType); done {
		return relations, hint, true
	}

	// Last attempt: external via import for short receiverType.
	return resolver.resolveExternalByImport(call, envs, callerID, receiverType)
}

// resolveByWildcardOrSamePackage checks if the receiver type is a project class accessible
// via wildcard import (import com.xxx.*) or same-package visibility (no import needed).
func (resolver *Resolver) resolveByWildcardOrSamePackage(
	call model.RawCall,
	envs map[string]*model.TypeEnv,
	funcCandidates []model.Symbol,
	callerID string,
	receiverType string,
) ([]model.ResolvedRelation, *model.UnresolvedHint, bool) {
	env := envs[call.FilePath]
	if env == nil {
		return nil, nil, false
	}

	// Determine caller's package from its class symbols.
	callerPkg := ""
	for _, sym := range resolver.symbolTable.FindByFile(call.FilePath) {
		if sym.Kind != constants.KindClass && sym.Kind != constants.KindInterface && sym.Kind != "abstract_class" {
			continue
		}
		if idx := strings.LastIndex(sym.QualifiedName, "."+sym.Name); idx > 0 {
			callerPkg = sym.QualifiedName[:idx]
			break
		}
	}

	// Check each class matching receiverType for package/import accessibility.
	for _, sym := range resolver.symbolTable.FindByName(receiverType) {
		if sym.Kind != constants.KindClass && sym.Kind != constants.KindInterface && sym.Kind != "abstract_class" {
			continue
		}
		symPkg := ""
		if idx := strings.LastIndex(sym.QualifiedName, "."+sym.Name); idx > 0 {
			symPkg = sym.QualifiedName[:idx]
		}
		isSamePackage := callerPkg != "" && symPkg == callerPkg

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
				return []model.ResolvedRelation{makeRelation(callerID, matched[0].ID, call, ConfidenceTypeExact, "type_exact", 1)}, nil, true
			}
			if len(matched) > 1 {
				argMatched := filterByArgCount(matched, call.ArgCount)
				if len(argMatched) == 1 {
					return []model.ResolvedRelation{makeRelation(callerID, argMatched[0].ID, call, ConfidenceArgCount, "arg_count", 1)}, nil, true
				}
				return makeMultiRelations(callerID, matched, call, ConfidenceTypeParent, "type_multi"), nil, true
			}
			break
		}
	}
	return nil, nil, false
}

// resolveExternalByImport creates an external node by matching the receiver type against imports.
// This is the final fallback when all other strategies fail.
func (resolver *Resolver) resolveExternalByImport(
	call model.RawCall,
	envs map[string]*model.TypeEnv,
	callerID string,
	receiverType string,
) ([]model.ResolvedRelation, *model.UnresolvedHint, bool) {
	env := envs[call.FilePath]
	if env == nil {
		return nil, nil, true
	}
	for _, imp := range env.Imports {
		if imp.SymbolName == receiverType {
			externalQN := imp.ModulePath + "." + call.CalledName
			externalID := "external:" + externalQN
			resolver.symbolTable.AddBatch([]model.Symbol{{
				ID: externalID, Name: call.CalledName,
				QualifiedName: externalQN, Kind: constants.KindFunction, FilePath: constants.FilePathExternal,
			}})
			return []model.ResolvedRelation{makeRelation(callerID, externalID, call, ConfidenceExternal, "external", 1)}, nil, true
		}
	}
	return nil, nil, true
}

// resolveCallFallback resolves a call without receiver type information.
// Uses progressively weaker strategies: same file, arg count, inherited method, global unique name.
func (resolver *Resolver) resolveCallFallback(
	call model.RawCall,
	envs map[string]*model.TypeEnv,
	funcCandidates []model.Symbol,
	callerID string,
	langHelper LanguageHelper,
) ([]model.ResolvedRelation, *model.UnresolvedHint) {

	// Exclude generated symbols (e.g. synthetic getters/setters) from fallback matching.
	realCandidates := langHelper.FilterGenerated(funcCandidates)

	// Strategy 1: Same file — if only one candidate is in the same file, high confidence.
	sameFile := filterByFile(realCandidates, call.FilePath)
	if len(sameFile) == 1 {
		return []model.ResolvedRelation{makeRelation(callerID, sameFile[0].ID, call, ConfidenceSameFile, "same_file", 1)}, nil
	}

	// Strategy 2: Arg count disambiguation — narrow by matching argument count.
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

	// Strategy 3: No-receiver inherited method — check caller's class hierarchy.
	if call.ReceiverExpr == "" && len(resolver.heritage) > 0 {
		callerClass := call.CallerName
		if dotIdx := strings.LastIndex(callerClass, "."); dotIdx >= 0 {
			callerClass = callerClass[:dotIdx]
		}
		if sym := resolver.FindMethodInHierarchy(callerClass, call.CalledName, resolver.heritage); sym != nil {
			return []model.ResolvedRelation{makeRelation(callerID, sym.ID, call, ConfidenceTypeExact, "type_hierarchy", 1)}, nil
		}
	}

	// Strategy 4: Global unique name — only one candidate exists in the entire project.
	if len(realCandidates) == 1 {
		return []model.ResolvedRelation{makeRelation(callerID, realCandidates[0].ID, call, ConfidenceNameUnique, "name_unique", 1)}, nil
	}

	// Multiple candidates remain — cannot resolve confidently, generate unresolved hint.
	if len(realCandidates) > 1 {
		return nil, classifyUnresolvedCall(call, callerID, "", realCandidates)
	}
	return nil, nil
}

// classifyUnresolvedCall creates an UnresolvedHint with the appropriate hint type.
// classifyUnresolvedCall creates an UnresolvedHint for a call that could not be confidently resolved.
// Classifies the hint type based on the call pattern (super, chained, lambda, enum, ambiguous).
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

// isEnumConstantPattern checks if an expression looks like "ClassName.CONSTANT_NAME"
// (uppercase class + all-uppercase constant after dot).
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
			if receiverSymbol != nil && (receiverSymbol.Kind == constants.KindInterface || receiverSymbol.Kind == "abstract_class" || resolvedMethod.IsAbstract) {
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
		Kind:          constants.KindFunction,
		FilePath:      constants.FilePathExternal,
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

// resolveChainedReceiverInternal recursively resolves a dotted receiver expression (e.g. "obj.getService().findById")
// by walking the chain left-to-right, resolving each segment's return type to determine the next receiver type.
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
				if sym.Kind == constants.KindClass || sym.ClassType == "enum" {
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
				if candidate.Kind == constants.KindClass || candidate.Kind == "abstract_class" ||
					candidate.Kind == constants.KindInterface || candidate.ClassType == "struct" {
					qualifiedKey := candidate.QualifiedName + ":" + methodName
					if info, exists := env.Bindings[qualifiedKey]; exists {
						return info.TypeName
					}
				}
			}
		}
		// Fallback: lookup field type from class declaration via SymbolTable
		for _, candidate := range resolver.symbolTable.FindByName(baseType) {
			if candidate.Kind == constants.KindClass || candidate.Kind == "abstract_class" ||
				candidate.Kind == constants.KindInterface || candidate.ClassType == "struct" {
				if fieldInfo := resolver.symbolTable.FindFieldByOwner(candidate.QualifiedName, methodName); fieldInfo != nil && fieldInfo.Type != "" {
					return fieldInfo.Type
				}
			}
		}
		return ""
	}

	// Method call: find return type in SymbolTable
	candidates := resolver.symbolTable.FindByName(methodName)
	for _, candidate := range candidates {
		if candidate.Kind == constants.KindFunction && strings.Contains(candidate.QualifiedName, typeSeg+".") && len(candidate.ReturnTypes) > 0 {
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
// substituteGenericParam replaces a generic return type (e.g. "T") with the actual type argument
// from the receiver's instantiation. Example: List<String>.get() returns "T" → substituted to "String".
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

// lookupReceiverType determines the type of a call's receiver expression.
// Checks TypeEnv bindings, class field hierarchy, and chained receiver resolution.
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

// resolveFullQualifiedType resolves a short class name to its fully qualified name
// using the file's import list and the symbol table for verification.
// If the name already contains ".", it is returned as-is.
// Returns the original name if resolution fails.
func (resolver *Resolver) resolveFullQualifiedType(typeName string, env *model.TypeEnv) string {
	if env == nil || strings.Contains(typeName, ".") {
		return typeName
	}
	for _, imp := range env.Imports {
		if imp.SymbolName == typeName {
			return imp.ModulePath
		}
		// Wildcard import: try ModulePath + "." + typeName and verify in symbolTable
		candidateQN := imp.ModulePath + "." + typeName
		for _, sym := range resolver.symbolTable.FindByQualifiedName(candidateQN) {
			if sym.Kind == constants.KindClass || sym.Kind == "abstract_class" ||
				sym.Kind == constants.KindInterface || sym.ClassType == "struct" {
				return candidateQN
			}
		}
	}
	return typeName
}

// findClassSymbol returns the class/interface Symbol for a type name, or nil if not found.
func (resolver *Resolver) findClassSymbol(typeName string) *model.Symbol {
	candidates := resolver.symbolTable.FindByName(typeName)
	for _, candidate := range candidates {
		if candidate.Kind == constants.KindClass || candidate.Kind == "abstract_class" ||
			candidate.Kind == constants.KindInterface || candidate.ClassType == "struct" {
			matched := candidate
			return &matched
		}
	}
	if strings.Contains(typeName, ".") {
		candidates = resolver.symbolTable.FindByQualifiedName(typeName)
		for _, candidate := range candidates {
			if candidate.Kind == constants.KindClass || candidate.Kind == "abstract_class" ||
				candidate.Kind == constants.KindInterface || candidate.ClassType == "struct" {
				matched := candidate
				return &matched
			}
		}
	}
	return nil
}

// findCallerID locates the Function node ID for the caller of a raw call.
// Matches by file path and line range; falls back to first function in same file.
func (resolver *Resolver) findCallerID(call model.RawCall) string {
	candidates := resolver.symbolTable.FindByName(lastSegment(call.CallerName))
	var fallback string
	for _, candidate := range candidates {
		if candidate.FilePath == call.FilePath && candidate.Kind == constants.KindFunction {
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
		base = strings.ReplaceAll(base, "\\", ".")
		base = strings.ReplaceAll(base, "/", ".")
		base = lastSegment(base)
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

// filterByOwnerClass returns candidates whose QualifiedName contains ".className." as a segment.
// Used to match methods belonging to a specific class.
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

// filterByFile returns candidates defined in the specified file.
func filterByFile(candidates []model.Symbol, filePath string) []model.Symbol {
	var matched []model.Symbol
	for _, candidate := range candidates {
		if candidate.FilePath == filePath {
			matched = append(matched, candidate)
		}
	}
	return matched
}

// filterByArgCount returns candidates whose parameter count matches the call's argument count.
// Supports varargs: a varargs function matches if argCount >= (paramCount - 1).
// Supports default/optional parameters: matches if argCount >= requiredParamCount && argCount <= totalParamCount.
func filterByArgCount(candidates []model.Symbol, argCount int) []model.Symbol {
	var matched []model.Symbol
	for _, candidate := range candidates {
		params := candidate.Params
		paramCount := len(params)
		if paramCount > 0 && strings.HasSuffix(params[paramCount-1].Type, "...") {
			// varargs: at least (paramCount - 1) fixed args
			if argCount >= paramCount-1 {
				matched = append(matched, candidate)
			}
			continue
		}
		requiredParamCount := 0
		for _, param := range params {
			if !param.HasDefault {
				requiredParamCount++
			}
		}
		if argCount >= requiredParamCount && argCount <= paramCount {
			matched = append(matched, candidate)
		}
	}
	return matched
}

// paramTypes extracts type strings from a ParamInfo slice.
func paramTypes(params []model.ParamInfo) []string {
	types := make([]string, len(params))
	for i, p := range params {
		types[i] = p.Type
	}
	return types
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

// inferExprType infers the type of an expression used as a function argument.
// Handles string literals, variables, method calls, field access, and constructor calls.
func (resolver *Resolver) inferExprType(expr string, call model.RawCall, env *model.TypeEnv, envs map[string]*model.TypeEnv, langHelper LanguageHelper) string {
	if expr == "" {
		return ""
	}
	// Strip outer parentheses: (expr) → expr
	for strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") {
		expr = expr[1 : len(expr)-1]
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
			// Static import symbol match: e.g. import static ExceptionCode.Safety → Safety's type is ExceptionCode
			for _, imp := range env.Imports {
				if imp.SymbolName == expr {
					classPath := strings.TrimSuffix(imp.ModulePath, "."+imp.SymbolName)
					if classPath != imp.ModulePath {
						className := extractSimpleType(classPath)
						if len(className) > 0 && className[0] >= 'A' && className[0] <= 'Z' {
							return className
						}
					}
				}
			}
			// Static import qualified name match (legacy)
			for _, imp := range env.Imports {
				syms := resolver.symbolTable.FindByQualifiedName(imp.ModulePath + "." + expr)
				if len(syms) > 0 {
					return imp.SymbolName
				}
			}
		}
		// Fallback: uppercase first letter → Java class name convention (e.g. DateUtil, StringUtils)
		if len(expr) > 0 && expr[0] >= 'A' && expr[0] <= 'Z' {
			return expr
		}
		return ""
	}
	// 2. Method call — lookup return type in SymbolTable
	if strings.HasSuffix(expr, ")") && !strings.Contains(expr, ".") {
		methodName := expr[:strings.Index(expr, "(")]
		candidates := resolver.symbolTable.FindByName(methodName)
		for _, candidate := range candidates {
			if candidate.Kind == constants.KindFunction && candidate.FilePath == call.FilePath && len(candidate.ReturnTypes) > 0 {
				return extractSimpleType(candidate.ReturnTypes[0])
			}
		}
		return ""
	}
	// 3. obj.method() — resolve receiver type then lookup method return type
	if strings.HasSuffix(expr, ")") && strings.Contains(expr, ".") {
		dotIdx := lastDotOutsideParens(expr)
		if dotIdx < 0 {
			return ""
		}
		objPart := expr[:dotIdx]
		methodPart := expr[dotIdx+1:]
		if parenIdx := strings.Index(methodPart, "("); parenIdx >= 0 {
			methodName := methodPart[:parenIdx]
			// Resolve obj type — strip trailing () if obj is also a call
			objType := resolver.inferExprType(objPart, call, env, envs, langHelper)
			if objType != "" {
				// Lookup method return type on objType
				candidates := resolver.symbolTable.FindByName(methodName)
				for _, candidate := range candidates {
					if candidate.Kind == constants.KindFunction && strings.Contains(candidate.QualifiedName, objType+".") && len(candidate.ReturnTypes) > 0 {
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

// lastDotOutsideParens finds the last '.' that is not inside parentheses.
// Handles nested method calls like "DateUtil.parseDate(reqs.getToDate()).getTime()".
func lastDotOutsideParens(expr string) int {
	depth := 0
	for i := len(expr) - 1; i >= 0; i-- {
		switch expr[i] {
		case ')':
			depth++
		case '(':
			depth--
		case '.':
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// extractSimpleType strips pointer prefix and package path, returning the short type name.
// Example: "*com.example.UserService" → "UserService"
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
		params := paramTypes(candidate.Params)
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
			params := paramTypes(candidate.Params)
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

func isSingleLetterGeneric(typeName string) bool {
	return len(typeName) == 1 && typeName[0] >= 'A' && typeName[0] <= 'Z'
}

// isSingleLetterGeneric returns true if the type name is a single uppercase letter (generic type parameter).
func makeRelation(sourceID, targetID string, call model.RawCall, confidence float64, resolvedBy string, candidates int) model.ResolvedRelation {
	return model.ResolvedRelation{
		SourceID:    sourceID,
		TargetID:    targetID,
		Kind:        model.RelCalls,
// makeRelation constructs a ResolvedRelation with standard metadata (line, flow_context, declared_type).
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
// makeMultiRelations creates a ResolvedRelation for each candidate when multiple matches exist.
// Uses best_guess confidence since the resolver cannot determine which is correct.
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


