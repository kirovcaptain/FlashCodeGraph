package typescript

import (
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// Helper implements resolver.LanguageHelper for TypeScript and JavaScript.
type Helper struct{}

func NewHelper() *Helper { return &Helper{} }

// ResolveSuperCall handles TS/JS super.method() and super() constructor calls.
func (tsHelper *Helper) ResolveSuperCall(call model.RawCall, funcCandidates []model.Symbol, heritage []model.RawHeritage, _ map[string]*model.TypeEnv, callerID string) ([]model.ResolvedRelation, bool) {
	if call.ReceiverExpr != "super" || len(heritage) == 0 {
		return nil, false
	}
	callerClassQN := resolver.ExtractCallerClassQN(call.CallerName)
	callerClass := callerClassQN
	if dotIdx := strings.LastIndex(callerClass, "."); dotIdx >= 0 {
		callerClass = callerClass[dotIdx+1:]
	}
	setDeclaredType := func(relations []model.ResolvedRelation) {
		if callerClassQN != "" {
			for i := range relations {
				relations[i].Metadata["declared_type"] = callerClassQN
			}
		}
	}
	for _, heritageItem := range heritage {
		if heritageItem.ChildName == callerClass && heritageItem.Kind == "extends" && heritageItem.FilePath == call.FilePath {
			// super() constructor call — CalledName is empty
			calledName := call.CalledName
			if calledName == "" {
				calledName = "constructor"
			}
			var matched []model.Symbol
			parentPrefix := heritageItem.ParentName + "."
			for _, candidate := range funcCandidates {
				if strings.Contains(candidate.QualifiedName, parentPrefix) && candidate.Name == calledName {
					matched = append(matched, candidate)
				}
			}
			if len(matched) == 0 {
				matched = resolver.FilterByOwnerClass(funcCandidates, heritageItem.ParentName)
			}
			if len(matched) == 1 {
				relations := []model.ResolvedRelation{resolver.MakeRelation(callerID, matched[0].ID, call, resolver.ConfidenceTypeExact, "type_exact", 1)}
				setDeclaredType(relations)
				return relations, true
			}
			if len(matched) > 1 {
				argMatched := resolver.FilterByArgCount(matched, call.ArgCount)
				if len(argMatched) == 1 {
					relations := []model.ResolvedRelation{resolver.MakeRelation(callerID, argMatched[0].ID, call, resolver.ConfidenceArgCount, "arg_count", 1)}
					setDeclaredType(relations)
					return relations, true
				}
				relations := resolver.MakeMultiRelations(callerID, matched, call, resolver.ConfidenceTypeParent, "type_multi")
				setDeclaredType(relations)
				return relations, true
			}
			break
		}
	}
	return nil, false
}

// NarrowByScope narrows candidates using TS/JS import paths.
// Matches candidates whose file path or qualified name aligns with an import.
func (tsHelper *Helper) NarrowByScope(matched []model.Symbol, call model.RawCall, env *model.TypeEnv, _ *resolver.SymbolTable) []model.Symbol {
	if len(matched) <= 1 || env == nil {
		return matched
	}
	// Prefer same-file candidates
	var sameFile []model.Symbol
	for _, candidate := range matched {
		if candidate.FilePath == call.FilePath {
			sameFile = append(sameFile, candidate)
		}
	}
	if len(sameFile) > 0 {
		return sameFile
	}
	// Match via imports
	var imported []model.Symbol
	for _, candidate := range matched {
		for _, imp := range env.Imports {
			if strings.Contains(candidate.FilePath, imp.ModulePath) {
				imported = append(imported, candidate)
				break
			}
			if imp.SymbolName != "" && imp.SymbolName == candidate.Name &&
				strings.Contains(candidate.FilePath, imp.ModulePath) {
				imported = append(imported, candidate)
				break
			}
		}
	}
	if len(imported) > 0 {
		return imported
	}
	return matched
}

// ResolveReceiverFallback — for TS/JS, when the receiver is a known global object
// (console, Math, JSON, etc.), return handled=true to prevent fallthrough to
// project-level matching (name_unique, same_file) which would produce false positives.
func (tsHelper *Helper) ResolveReceiverFallback(call model.RawCall, _ []model.Symbol, _ map[string]*model.TypeEnv, _ string, _ *resolver.SymbolTable) ([]model.ResolvedRelation, bool) {
	if IsGlobalObject(call.ReceiverExpr) {
		return nil, true // handled: skip fallthrough, no CALLS edge
	}
	return nil, false
}

// ResolveImplicitSelfCall — TS/JS uses explicit this, no implicit self.
func (tsHelper *Helper) ResolveImplicitSelfCall(_ model.RawCall, _ []model.Symbol, _ map[string]*model.TypeEnv, _ string, _ *resolver.SymbolTable) ([]model.ResolvedRelation, bool) {
	return nil, false
}

// ShouldFallthrough — TS/JS module imports appear as receivers (e.g., utils.helper()),
// so receiver-based matching should fall through to no-receiver matching.
func (tsHelper *Helper) ShouldFallthrough() bool { return true }

// FilterGenerated — TS/JS has no annotation-based code generation.
func (tsHelper *Helper) FilterGenerated(candidates []model.Symbol) []model.Symbol {
	return candidates
}

// IsTypeAssignable — TS uses structural typing; only exact match is reliable statically.
func (tsHelper *Helper) IsTypeAssignable(argType, paramType string) bool {
	return argType == paramType
}

// ResolveOverload — TS supports overload signatures but resolution requires
// full type checking; not implemented statically.
func (tsHelper *Helper) ResolveOverload(_ []model.Symbol, _ []string) *model.Symbol {
	return nil
}

// InferStringConcat — TS/JS uses + for concatenation and template literals.
func (tsHelper *Helper) InferStringConcat(expr string) bool {
	if strings.Contains(expr, "+") && strings.Contains(expr, "\"") {
		return true
	}
	// Template literal: `...`
	return strings.Contains(expr, "`")
}

// LookupMethodReturn — no built-in method return table for TS/JS.
func (tsHelper *Helper) LookupMethodReturn(_, _ string) (string, bool) {
	return "", false
}

// IsConstructor returns true if the method is a TS/JS constructor.
func (tsHelper *Helper) IsConstructor(method model.Symbol, _ string) bool {
	return method.Name == "constructor"
}

// IsOverrideMatch returns true if childMethod overrides parentMethod (same name + param count).
func (tsHelper *Helper) IsOverrideMatch(childMethod, parentMethod model.Symbol) bool {
	return childMethod.Name == parentMethod.Name &&
		resolver.CountParams(childMethod.Params) == resolver.CountParams(parentMethod.Params)
}

// InferImplements is a no-op for TypeScript (explicit implements keyword).
func (tsHelper *Helper) InferImplements() []model.ResolvedRelation { return nil }
