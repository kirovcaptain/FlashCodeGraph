// Package kotlin provides a minimal Kotlin LanguageHelper for resolver dispatch.
// Full implementation will be added in a subsequent iteration.
package kotlin

import (
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// Helper is a minimal Kotlin LanguageHelper that enables basic resolver flow.
type Helper struct{}

// NewHelper creates a new Kotlin resolver helper.
func NewHelper() *Helper {
	return &Helper{}
}

func (helper *Helper) ResolveSuperCall(call model.RawCall, funcCandidates []model.Symbol, heritage []model.RawHeritage, envs map[string]*model.TypeEnv, callerID string) ([]model.ResolvedRelation, bool) {
	return nil, false
}

func (helper *Helper) NarrowByScope(matched []model.Symbol, call model.RawCall, env *model.TypeEnv, symbolTable *resolver.SymbolTable) []model.Symbol {
	return matched
}

func (helper *Helper) ResolveReceiverFallback(call model.RawCall, funcCandidates []model.Symbol, envs map[string]*model.TypeEnv, callerID string, symbolTable *resolver.SymbolTable) ([]model.ResolvedRelation, bool) {
	return nil, false
}

func (helper *Helper) ResolveImplicitSelfCall(call model.RawCall, funcCandidates []model.Symbol, envs map[string]*model.TypeEnv, callerID string, symbolTable *resolver.SymbolTable) ([]model.ResolvedRelation, bool) {
	return nil, false
}

func (helper *Helper) ShouldFallthrough() bool {
	return false
}

func (helper *Helper) FilterGenerated(candidates []model.Symbol) []model.Symbol {
	return candidates
}

func (helper *Helper) IsTypeAssignable(argumentType, parameterType string) bool {
	return argumentType == parameterType
}

func (helper *Helper) ResolveOverload(candidates []model.Symbol, argumentTypes []string) *model.Symbol {
	return nil
}

func (helper *Helper) InferStringConcat(expression string) bool {
	return false
}

func (helper *Helper) LookupMethodReturn(typeName, methodName string, argumentTypes []string) (model.ReturnType, bool) {
	return model.ReturnType{}, false
}

func (helper *Helper) BuildExternalQualifiedName(typeName, methodName string) string {
	return typeName + "." + methodName
}

func (helper *Helper) LookupClassTypeParams(typeName string) []string {
	return nil
}

func (helper *Helper) IsExternalPackage(receiverName string) bool {
	return false
}

func (helper *Helper) InferImplements() []model.ResolvedRelation {
	return nil
}

func (helper *Helper) IsImportAccessible(candidate model.Symbol, callerFilePath string, env *model.TypeEnv) bool {
	return true
}

func (helper *Helper) IsConstructor(method model.Symbol, className string) bool {
	return method.IsConstructor
}

func (helper *Helper) IsOverrideMatch(childMethod, parentMethod model.Symbol) bool {
	return childMethod.Name == parentMethod.Name && len(childMethod.Params) == len(parentMethod.Params)
}
