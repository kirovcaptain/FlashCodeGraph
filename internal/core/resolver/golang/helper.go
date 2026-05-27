package golang

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// Helper implements resolver.LanguageHelper for Go.
type Helper struct {
	symbolTable *resolver.SymbolTable
}

func NewHelper(symbolTable *resolver.SymbolTable) *Helper {
	return &Helper{symbolTable: symbolTable}
}

// ResolveSuperCall — Go has no class inheritance, always returns false.
func (goHelper *Helper) ResolveSuperCall(_ model.RawCall, _ []model.Symbol, _ []model.RawHeritage, _ map[string]*model.TypeEnv, _ string) ([]model.ResolvedRelation, bool) {
	return nil, false
}

// NarrowByScope narrows candidates to those in the same Go package directory
// or whose package path matches an import in the caller's file.
func (goHelper *Helper) NarrowByScope(matched []model.Symbol, call model.RawCall, env *model.TypeEnv, _ *resolver.SymbolTable) []model.Symbol {
	if len(matched) <= 1 {
		return matched
	}
	callerDir := filepath.Dir(call.FilePath)
	// Prefer same-package candidates
	var samePackage []model.Symbol
	for _, candidate := range matched {
		if filepath.Dir(candidate.FilePath) == callerDir {
			samePackage = append(samePackage, candidate)
		}
	}
	if len(samePackage) > 0 {
		return samePackage
	}
	// Fall back to import-matched candidates
	if env == nil {
		return matched
	}
	var imported []model.Symbol
	for _, candidate := range matched {
		for _, imp := range env.Imports {
			if strings.HasSuffix(candidate.FilePath, imp.ModulePath) ||
				strings.Contains(candidate.QualifiedName, imp.ModulePath) {
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

// ResolveReceiverFallback — Go receiver is a struct type; the generic resolver
// already handles struct.Method matching via TypeEnv, no extra logic needed.
func (goHelper *Helper) ResolveReceiverFallback(_ model.RawCall, _ []model.Symbol, _ map[string]*model.TypeEnv, _ string, _ *resolver.SymbolTable) ([]model.ResolvedRelation, bool) {
	return nil, false
}

// ResolveImplicitSelfCall — Go always requires explicit receiver, no implicit self.
func (goHelper *Helper) ResolveImplicitSelfCall(_ model.RawCall, _ []model.Symbol, _ map[string]*model.TypeEnv, _ string, _ *resolver.SymbolTable) ([]model.ResolvedRelation, bool) {
	return nil, false
}

// ShouldFallthrough — Go uses package-level functions freely, so receiver-based
// matching should fall through to no-receiver matching.
func (goHelper *Helper) ShouldFallthrough() bool { return true }

// FilterGenerated — Go has no code generation annotations like Lombok.
func (goHelper *Helper) FilterGenerated(candidates []model.Symbol) []model.Symbol {
	return candidates
}

// IsTypeAssignable — Go uses exact type matching (no implicit boxing or hierarchy).
func (goHelper *Helper) IsTypeAssignable(argType, paramType string) bool {
	return argType == paramType
}

// ResolveOverload — Go does not support method overloading.
func (goHelper *Helper) ResolveOverload(_ []model.Symbol, _ []string) *model.Symbol {
	return nil
}

// InferStringConcat — Go uses + for string concatenation.
func (goHelper *Helper) InferStringConcat(expr string) bool {
	return strings.Contains(expr, "+") && strings.Contains(expr, "\"")
}

// LookupMethodReturn — no built-in method return table for Go.
func (goHelper *Helper) LookupMethodReturn(_, _ string) (model.ReturnType, bool) {
	return model.ReturnType{}, false
}

// BuildExternalQualifiedName constructs a qualified name for an external Go method.
func (goHelper *Helper) BuildExternalQualifiedName(typeName, methodName string) string {
	return typeName + "." + methodName
}

// LookupClassTypeParams — no built-in class type params for Go.
func (goHelper *Helper) LookupClassTypeParams(_ string) []string { return nil }

// IsConstructor — Go does not have constructors.
func (goHelper *Helper) IsConstructor(_ model.Symbol, _ string) bool {
	return false
}

// IsOverrideMatch returns true if childMethod overrides parentMethod (same name + param count).
func (goHelper *Helper) IsOverrideMatch(childMethod, parentMethod model.Symbol) bool {
	return childMethod.Name == parentMethod.Name &&
		resolver.CountParams(childMethod.Params) == resolver.CountParams(parentMethod.Params)
}

// InferImplements infers IMPLEMENTS relationships for Go by matching
// struct method sets against interface method signatures (name + param count).
func (goHelper *Helper) InferImplements() []model.ResolvedRelation {
	// Collect interface info keyed by QualifiedName (not short name)
	ifaceMethods := make(map[string]map[string]bool) // ifaceQN → signatures
	ifaceIDs := make(map[string]string)              // ifaceQN → symbol ID

	for _, sym := range goHelper.symbolTable.All() {
		if sym.Kind == constants.KindInterface {
			ifaceIDs[sym.QualifiedName] = sym.ID
			ifaceMethods[sym.QualifiedName] = make(map[string]bool)
		}
	}

	// Collect interface method signatures using QualifiedName prefix match
	for ifaceQN := range ifaceMethods {
		for _, method := range goHelper.symbolTable.FindMethodsByQualifiedName(ifaceQN) {
			sig := method.Name + ":" + strconv.Itoa(resolver.CountParams(method.Params))
			ifaceMethods[ifaceQN][sig] = true
		}
	}

	// Remove empty interfaces
	for qn, methods := range ifaceMethods {
		if len(methods) == 0 {
			delete(ifaceMethods, qn)
			delete(ifaceIDs, qn)
		}
	}

	// Collect struct info keyed by QualifiedName
	type structInfo struct {
		id            string
		qualifiedName string
		methods       map[string]bool
	}
	structs := make(map[string]*structInfo)

	for _, sym := range goHelper.symbolTable.All() {
		if sym.Kind == constants.KindClass || sym.ClassType == constants.ClassTypeStruct {
			structs[sym.QualifiedName] = &structInfo{
				id:            sym.ID,
				qualifiedName: sym.QualifiedName,
				methods:       make(map[string]bool),
			}
		}
	}

	for structQN, info := range structs {
		for _, method := range goHelper.symbolTable.FindMethodsByQualifiedName(structQN) {
			sig := method.Name + ":" + strconv.Itoa(resolver.CountParams(method.Params))
			info.methods[sig] = true
		}
	}

	// Match: struct methods ⊇ interface methods → IMPLEMENTS + method-level OVERRIDES/DISPATCHES
	var relations []model.ResolvedRelation
	for ifaceQN, ifaceSigs := range ifaceMethods {
		for _, info := range structs {
			if info.qualifiedName == ifaceQN {
				continue
			}
			if !isSubset(ifaceSigs, info.methods) {
				continue
			}
			relations = append(relations, model.ResolvedRelation{
				SourceID:   info.id,
				TargetID:   ifaceIDs[ifaceQN],
				Kind:       model.RelImplements,
				SourceKind: constants.KindClass,
				ResolvedBy: "inferred_implements",
				Confidence: constants.ConfidenceArgCount,
			})

			for _, ifaceMethod := range goHelper.symbolTable.FindMethodsByQualifiedName(ifaceQN) {
				for _, structMethod := range goHelper.symbolTable.FindMethodsByQualifiedName(info.qualifiedName) {
					if ifaceMethod.Name == structMethod.Name &&
						resolver.CountParams(ifaceMethod.Params) == resolver.CountParams(structMethod.Params) {
						relations = append(relations, model.ResolvedRelation{
							SourceID: structMethod.ID, TargetID: ifaceMethod.ID,
							Kind: model.RelOverrides, SourceKind: constants.KindFunction,
							Confidence: constants.ConfidenceArgCount, ResolvedBy: "inferred_override", Candidates: 1,
						})
						relations = append(relations, model.ResolvedRelation{
							SourceID: ifaceMethod.ID, TargetID: structMethod.ID,
							Kind: model.RelDispatches, SourceKind: constants.KindFunction,
							Confidence: constants.ConfidenceArgCount, ResolvedBy: "inferred_dispatch", Candidates: 1,
						})
					}
				}
			}
		}
	}

	return relations
}

func isSubset(required, actual map[string]bool) bool {
	for sig := range required {
		if !actual[sig] {
			return false
		}
	}
	return true
}
