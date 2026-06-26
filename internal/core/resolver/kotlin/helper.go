// Package kotlin provides a Kotlin LanguageHelper for resolver dispatch.
package kotlin

import (
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// externalPackagePrefixes lists package prefixes that are external dependencies.
var externalPackagePrefixes = []string{
	"kotlin.", "kotlinx.", "android.", "androidx.",
	"java.", "javax.", "com.google.android.",
	"io.ktor.", "org.jetbrains.",
}

// Helper is the Kotlin LanguageHelper implementation.
type Helper struct {
	externalMethods *ExternalMethodManager
	symbolTable     *resolver.SymbolTable
}

// NewHelper creates a new Kotlin resolver helper.
func NewHelper(symbolTable *resolver.SymbolTable, externalMethods *ExternalMethodManager) *Helper {
	return &Helper{
		externalMethods: externalMethods,
		symbolTable:     symbolTable,
	}
}

func (helper *Helper) ResolveSuperCall(call model.RawCall, funcCandidates []model.Symbol, heritage []model.RawHeritage, envs map[string]*model.TypeEnv, callerID string) ([]model.ResolvedRelation, bool) {
	if call.ReceiverExpr != "super" || len(heritage) == 0 {
		return nil, false
	}
	// Find caller's class from callerName
	callerClass := extractClassFromQualifiedName(call.CallerName)
	if callerClass == "" {
		return nil, false
	}
	// Find parent class from heritage
	for _, heritageEntry := range heritage {
		if heritageEntry.ChildName == callerClass || heritageEntry.ChildQualified == call.CallerName || strings.HasSuffix(heritageEntry.ChildQualified, "."+callerClass) {
			// Look for method in parent
			for _, candidate := range funcCandidates {
				if strings.Contains(candidate.QualifiedName, heritageEntry.ParentName+"."+call.CalledName) {
					relation := resolver.MakeRelation(callerID, candidate.ID, call, 0.95, "super_call", 1, candidate.Kind)
					return []model.ResolvedRelation{relation}, true
				}
			}
		}
	}
	return nil, false
}

func (helper *Helper) NarrowByScope(matched []model.Symbol, call model.RawCall, env *model.TypeEnv, symbolTable *resolver.SymbolTable) []model.Symbol {
	if env == nil || len(matched) <= 1 {
		return matched
	}

	callerPackage := extractPackageFromQualifiedName(call.CallerName)
	var narrowed []model.Symbol

	for _, symbol := range matched {
		symbolPackage := extractPackageFromQualifiedName(symbol.QualifiedName)

		isSamePackage := callerPackage != "" && symbolPackage == callerPackage
		isSameFile := symbol.FilePath == call.FilePath

		isImported := false
		for _, importEntry := range env.Imports {
			if strings.HasSuffix(importEntry.ModulePath, "."+symbol.Name) ||
				importEntry.ModulePath == symbol.QualifiedName {
				isImported = true
				break
			}
		}

		if isSameFile || isSamePackage || isImported {
			narrowed = append(narrowed, symbol)
		}
	}

	if len(narrowed) > 0 {
		return narrowed
	}
	return matched
}

func (helper *Helper) ResolveReceiverFallback(call model.RawCall, funcCandidates []model.Symbol, envs map[string]*model.TypeEnv, callerID string, symbolTable *resolver.SymbolTable) ([]model.ResolvedRelation, bool) {
	// Try ReceiverExpr as class name (Kotlin companion object static-like call or object call)
	classSymbols := symbolTable.FindByName(call.ReceiverExpr)
	for _, classSymbol := range classSymbols {
		if classSymbol.Kind != constants.KindClass && classSymbol.Kind != constants.KindInterface {
			continue
		}
		prefix := classSymbol.QualifiedName + "."
		var staticMatched []model.Symbol
		for _, candidate := range funcCandidates {
			if strings.HasPrefix(candidate.QualifiedName, prefix) {
				staticMatched = append(staticMatched, candidate)
			}
		}
		if len(staticMatched) == 1 {
			relation := resolver.MakeRelation(callerID, staticMatched[0].ID, call, 0.85, "type_exact", 1, staticMatched[0].Kind)
			return []model.ResolvedRelation{relation}, true
		}
		if len(staticMatched) > 1 {
			argMatched := resolver.FilterByArgCount(staticMatched, call.ArgCount)
			if len(argMatched) == 1 {
				relation := resolver.MakeRelation(callerID, argMatched[0].ID, call, 0.85, "arg_count", 1, argMatched[0].Kind)
				return []model.ResolvedRelation{relation}, true
			}
		}
		// Also check Companion sub-object
		companionPrefix := classSymbol.QualifiedName + ".Companion."
		for _, candidate := range funcCandidates {
			if strings.HasPrefix(candidate.QualifiedName, companionPrefix) && strings.HasSuffix(candidate.QualifiedName, "."+call.CalledName) {
				relation := resolver.MakeRelation(callerID, candidate.ID, call, 0.85, "companion_call", 1, candidate.Kind)
				return []model.ResolvedRelation{relation}, true
			}
		}
	}
	return nil, false
}

func (helper *Helper) ResolveImplicitSelfCall(call model.RawCall, funcCandidates []model.Symbol, envs map[string]*model.TypeEnv, callerID string, symbolTable *resolver.SymbolTable) ([]model.ResolvedRelation, bool) {
	// Same-class method call (no receiver, no this)
	callerClass := extractClassFromQualifiedName(call.CallerName)
	if callerClass == "" {
		return nil, false
	}

	var sameClassMatched []model.Symbol
	for _, candidate := range funcCandidates {
		candidateClass := extractClassFromQualifiedName(candidate.QualifiedName)
		if candidateClass == callerClass {
			sameClassMatched = append(sameClassMatched, candidate)
		}
	}

	if len(sameClassMatched) == 1 {
		relation := resolver.MakeRelation(callerID, sameClassMatched[0].ID, call, 0.85, "same_class", 1, sameClassMatched[0].Kind)
		return []model.ResolvedRelation{relation}, true
	}
	if len(sameClassMatched) > 1 {
		argMatched := resolver.FilterByArgCount(sameClassMatched, call.ArgCount)
		if len(argMatched) == 1 {
			relation := resolver.MakeRelation(callerID, argMatched[0].ID, call, 0.85, "arg_count", 1, argMatched[0].Kind)
			return []model.ResolvedRelation{relation}, true
		}
	}
	return nil, false
}

func (helper *Helper) ShouldFallthrough() bool {
	return false
}

func (helper *Helper) FilterGenerated(candidates []model.Symbol) []model.Symbol {
	return candidates
}

func (helper *Helper) IsTypeAssignable(argumentType, parameterType string) bool {
	if argumentType == parameterType {
		return true
	}
	// Nullable compatibility: T can be assigned to T?
	if parameterType == argumentType+"?" {
		return true
	}
	return false
}

func (helper *Helper) ResolveOverload(candidates []model.Symbol, argumentTypes []string) *model.Symbol {
	if len(candidates) <= 1 {
		return nil
	}
	// Use arg count for disambiguation
	argCount := len(argumentTypes)
	var matched []model.Symbol
	for _, candidate := range candidates {
		if len(candidate.Params) == argCount {
			matched = append(matched, candidate)
		}
	}
	if len(matched) == 1 {
		return &matched[0]
	}
	return nil
}

func (helper *Helper) InferStringConcat(expression string) bool {
	return strings.Contains(expression, "+") && strings.Contains(expression, "\"")
}

func (helper *Helper) LookupMethodReturn(typeName, methodName string, argumentTypes []string) (model.ReturnType, bool) {
	return helper.externalMethods.Lookup(typeName, methodName, argumentTypes)
}

func (helper *Helper) BuildExternalQualifiedName(typeName, methodName string) string {
	return typeName + "." + methodName
}

func (helper *Helper) LookupClassTypeParams(typeName string) []string {
	return helper.externalMethods.LookupClassTypeParams(typeName)
}

func (helper *Helper) IsExternalPackage(receiverName string) bool {
	for _, prefix := range externalPackagePrefixes {
		if strings.HasPrefix(receiverName, prefix) {
			return true
		}
	}
	return helper.externalMethods.HasPackage(receiverName)
}

func (helper *Helper) IsConstructor(method model.Symbol, className string) bool {
	return method.IsConstructor || method.Name == "<init>"
}

func (helper *Helper) IsOverrideMatch(childMethod, parentMethod model.Symbol) bool {
	return childMethod.Name == parentMethod.Name && len(childMethod.Params) == len(parentMethod.Params)
}

func (helper *Helper) InferImplements() []model.ResolvedRelation {
	return nil
}

func (helper *Helper) IsImportAccessible(candidate model.Symbol, callerFilePath string, env *model.TypeEnv) bool {
	return true
}

// ── Helpers ──

func extractClassFromQualifiedName(qualifiedName string) string {
	// "com.example.MyClass.method" → "MyClass"
	// "com.example.MyClass.Inner.method" → "Inner"
	parts := strings.Split(qualifiedName, ".")
	if len(parts) < 2 {
		return ""
	}
	// Walk backwards to find the class part (capitalized, not the method)
	for i := len(parts) - 2; i >= 0; i-- {
		if len(parts[i]) > 0 && parts[i][0] >= 'A' && parts[i][0] <= 'Z' {
			return parts[i]
		}
	}
	return ""
}

func extractPackageFromQualifiedName(qualifiedName string) string {
	// "com.example.MyClass.method" → "com.example"
	parts := strings.Split(qualifiedName, ".")
	var packageParts []string
	for _, part := range parts {
		if len(part) > 0 && part[0] >= 'A' && part[0] <= 'Z' {
			break
		}
		packageParts = append(packageParts, part)
	}
	return strings.Join(packageParts, ".")
}
