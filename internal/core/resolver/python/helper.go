package python

import (
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// Helper implements resolver.LanguageHelper for Python.
type Helper struct{}

func NewHelper() *Helper { return &Helper{} }

// ResolveSuperCall handles Python's super().method() pattern.
// Python super() resolves to the parent class in MRO order.
func (pythonHelper *Helper) ResolveSuperCall(call model.RawCall, funcCandidates []model.Symbol, heritage []model.RawHeritage, _ map[string]*model.TypeEnv, callerID string) ([]model.ResolvedRelation, bool) {
	if call.ReceiverExpr != "super" || len(heritage) == 0 {
		return nil, false
	}
	callerClassQN := resolver.ExtractCallerClassQN(call.CallerName)
	callerClass := callerClassQN
	if dotIdx := strings.LastIndex(callerClass, "."); dotIdx >= 0 {
		callerClass = callerClass[dotIdx+1:]
	}
	for _, heritageItem := range heritage {
		if heritageItem.ChildName == callerClass && heritageItem.Kind == "extends" && heritageItem.FilePath == call.FilePath {
			matched := resolver.FilterByOwnerClass(funcCandidates, heritageItem.ParentName)
			if len(matched) == 1 {
				rel := resolver.MakeRelation(callerID, matched[0].ID, call, resolver.ConfidenceTypeExact, "type_exact", 1)
				if callerClassQN != "" {
					rel.Metadata["declared_type"] = callerClassQN
				}
				return []model.ResolvedRelation{rel}, true
			}
			break
		}
	}
	return nil, false
}

// NarrowByScope narrows candidates using Python's module import paths.
// Matches candidates whose qualified name starts with an imported module path.
func (pythonHelper *Helper) NarrowByScope(matched []model.Symbol, call model.RawCall, env *model.TypeEnv, _ *resolver.SymbolTable) []model.Symbol {
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
			if strings.HasPrefix(candidate.QualifiedName, imp.ModulePath+".") {
				imported = append(imported, candidate)
				break
			}
			if imp.SymbolName != "" && imp.SymbolName == candidate.Name &&
				strings.HasPrefix(candidate.QualifiedName, imp.ModulePath) {
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

// ResolveReceiverFallback — Python receiver is typically self/cls, handled by
// the generic resolver's TypeEnv lookup.
func (pythonHelper *Helper) ResolveReceiverFallback(_ model.RawCall, _ []model.Symbol, _ map[string]*model.TypeEnv, _ string, _ *resolver.SymbolTable) ([]model.ResolvedRelation, bool) {
	return nil, false
}

// ResolveImplicitSelfCall — Python always uses explicit self, no implicit self calls.
func (pythonHelper *Helper) ResolveImplicitSelfCall(_ model.RawCall, _ []model.Symbol, _ map[string]*model.TypeEnv, _ string, _ *resolver.SymbolTable) ([]model.ResolvedRelation, bool) {
	return nil, false
}

// ShouldFallthrough — Python uses module-level functions freely.
func (pythonHelper *Helper) ShouldFallthrough() bool { return true }

// FilterGenerated — Python has no annotation-based code generation like Lombok.
func (pythonHelper *Helper) FilterGenerated(candidates []model.Symbol) []model.Symbol {
	return candidates
}

// IsTypeAssignable — Python uses duck typing; only exact match is reliable statically.
func (pythonHelper *Helper) IsTypeAssignable(argType, paramType string) bool {
	return argType == paramType
}

// ResolveOverload — Python does not support method overloading.
func (pythonHelper *Helper) ResolveOverload(_ []model.Symbol, _ []string) *model.Symbol {
	return nil
}

// InferStringConcat — Python uses + for concatenation and f-strings.
func (pythonHelper *Helper) InferStringConcat(expr string) bool {
	if strings.Contains(expr, "+") && (strings.Contains(expr, "\"") || strings.Contains(expr, "'")) {
		return true
	}
	// f-string: f"..."
	return strings.HasPrefix(expr, "f\"") || strings.HasPrefix(expr, "f'")
}

// LookupMethodReturn — no built-in method return table for Python.
func (pythonHelper *Helper) LookupMethodReturn(_, _ string, _ []string) (model.ReturnType, bool) {
	return model.ReturnType{}, false
}

// BuildExternalQualifiedName constructs a qualified name for an external Python method.
func (pythonHelper *Helper) BuildExternalQualifiedName(typeName, methodName string) string {
	return typeName + "." + methodName
}

// LookupClassTypeParams — no built-in class type params for Python.
func (pythonHelper *Helper) LookupClassTypeParams(_ string) []string { return nil }

// IsConstructor returns true if the method is a Python __init__.
func (pythonHelper *Helper) IsConstructor(method model.Symbol, _ string) bool {
	return method.Name == "__init__"
}

// IsOverrideMatch returns true if childMethod overrides parentMethod (same name + param count).
func (pythonHelper *Helper) IsOverrideMatch(childMethod, parentMethod model.Symbol) bool {
	return childMethod.Name == parentMethod.Name &&
		resolver.CountParams(childMethod.Params) == resolver.CountParams(parentMethod.Params)
}

// InferImplements is a no-op for Python (no interface concept).
func (pythonHelper *Helper) InferImplements() []model.ResolvedRelation { return nil }
