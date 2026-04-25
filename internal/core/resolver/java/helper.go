package java

import (
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// Helper implements resolver.LanguageHelper for Java.
type Helper struct {
	symbolTable     *resolver.SymbolTable
	heritage        []model.RawHeritage
	externalMethods *ExternalMethodManager
}

// NewHelper creates a Java language helper.
func NewHelper(symbolTable *resolver.SymbolTable, externalMethods *ExternalMethodManager) *Helper {
	return &Helper{symbolTable: symbolTable, externalMethods: externalMethods}
}

// SetHeritage implements resolver.HeritageAware.
func (javaHelper *Helper) SetHeritage(heritage []model.RawHeritage) {
	javaHelper.heritage = heritage
}

func (javaHelper *Helper) ResolveSuperCall(call model.RawCall, funcCandidates []model.Symbol, heritage []model.RawHeritage, envs map[string]*model.TypeEnv, callerID string) ([]model.ResolvedRelation, bool) {
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
			parentName := heritageItem.ParentName
			parentSymbols := javaHelper.symbolTable.FindByName(parentName)
			var resolvedParentQN string
			env := envs[call.FilePath]
			callerPkg := javaHelper.extractPackage(call.FilePath)

			for _, sym := range parentSymbols {
				if sym.Kind != "Class" && sym.Kind != "abstract_class" {
					continue
				}
				symPkg := extractSymbolPackage(sym)
				if callerPkg != "" && symPkg == callerPkg {
					resolvedParentQN = sym.QualifiedName
					break
				}
				if env != nil {
					for _, imp := range env.Imports {
						if imp.SymbolName == parentName || strings.HasPrefix(sym.QualifiedName, imp.ModulePath+".") {
							resolvedParentQN = sym.QualifiedName
							break
						}
					}
				}
				if resolvedParentQN != "" {
					break
				}
			}

			var matched []model.Symbol
			if resolvedParentQN != "" {
				prefix := resolvedParentQN + "."
				for _, candidate := range funcCandidates {
					if strings.HasPrefix(candidate.QualifiedName, prefix) {
						matched = append(matched, candidate)
					}
				}
			} else {
				matched = resolver.FilterByOwnerClass(funcCandidates, parentName)
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
			hierarchyClassName := resolvedParentQN
			if hierarchyClassName == "" {
				hierarchyClassName = parentName
			}
			if sym := resolver.FindMethodInHierarchyPublic(javaHelper.symbolTable, hierarchyClassName, call.CalledName, heritage); sym != nil {
				relations := []model.ResolvedRelation{resolver.MakeRelation(callerID, sym.ID, call, resolver.ConfidenceTypeExact, "type_hierarchy", 1)}
				setDeclaredType(relations)
				return relations, true
			}
			break
		}
	}
	return nil, false
}

func (javaHelper *Helper) NarrowByScope(matched []model.Symbol, call model.RawCall, env *model.TypeEnv, symbolTable *resolver.SymbolTable) []model.Symbol {
	if env == nil {
		return matched
	}
	callerPkg := javaHelper.extractPackage(call.FilePath)
	var narrowed []model.Symbol
	for _, sym := range matched {
		symPkg := ""
		if idx := strings.LastIndex(sym.QualifiedName, "."+sym.Name); idx > 0 {
			parts := sym.QualifiedName[:idx]
			if idx2 := strings.LastIndex(parts, "."); idx2 > 0 {
				symPkg = parts[:idx2]
			}
		}
		isSamePackage := callerPkg != "" && symPkg == callerPkg
		isImported := false
		for _, imp := range env.Imports {
			if strings.HasPrefix(sym.QualifiedName, imp.ModulePath+".") {
				rest := sym.QualifiedName[len(imp.ModulePath)+1:]
				if strings.Count(rest, ".") <= 1 {
					isImported = true
					break
				}
			}
		}
		if isSamePackage || isImported {
			narrowed = append(narrowed, sym)
		}
	}
	if len(narrowed) > 0 {
		return narrowed
	}
	return matched
}

func (javaHelper *Helper) ResolveReceiverFallback(call model.RawCall, funcCandidates []model.Symbol, envs map[string]*model.TypeEnv, callerID string, symbolTable *resolver.SymbolTable) ([]model.ResolvedRelation, bool) {
	classSyms := symbolTable.FindByName(call.ReceiverExpr)
	callerPkg := javaHelper.extractPackage(call.FilePath)

	for _, cls := range classSyms {
		if cls.Kind != constants.KindClass && cls.Kind != constants.KindInterface && cls.Kind != "abstract_class" {
			continue
		}
		clsPkg := extractSymbolPackage(cls)
		if callerPkg == "" || clsPkg != callerPkg {
			continue
		}
		prefix := cls.QualifiedName + "."
		var staticMatched []model.Symbol
		for _, candidate := range funcCandidates {
			if strings.HasPrefix(candidate.QualifiedName, prefix) {
				staticMatched = append(staticMatched, candidate)
			}
		}
		if len(staticMatched) == 1 {
			rel := resolver.MakeRelation(callerID, staticMatched[0].ID, call, resolver.ConfidenceTypeExact, "type_exact", 1)
			rel.Metadata["declared_type"] = cls.QualifiedName
			return []model.ResolvedRelation{rel}, true
		}
		if len(staticMatched) > 1 {
			argMatched := resolver.FilterByArgCount(staticMatched, call.ArgCount)
			if len(argMatched) == 1 {
				rel := resolver.MakeRelation(callerID, argMatched[0].ID, call, resolver.ConfidenceArgCount, "arg_count", 1)
				rel.Metadata["declared_type"] = cls.QualifiedName
				return []model.ResolvedRelation{rel}, true
			}
			if len(argMatched) > 1 {
				typeMatched := resolver.FilterByArgTypesWithHelper(argMatched, resolver.EnrichArgTypes(call, envs, symbolTable, javaHelper), javaHelper)
				if len(typeMatched) == 1 {
					rel := resolver.MakeRelation(callerID, typeMatched[0].ID, call, resolver.ConfidenceArgCount, "arg_type", 1)
					rel.Metadata["declared_type"] = cls.QualifiedName
					return []model.ResolvedRelation{rel}, true
				}
			}
			finalCandidates := staticMatched
			if len(argMatched) > 0 {
				finalCandidates = argMatched
			}
			relations := resolver.MakeMultiRelations(callerID, finalCandidates, call, resolver.ConfidenceTypeParent, "type_multi")
			for i := range relations {
				relations[i].Metadata["declared_type"] = cls.QualifiedName
			}
			return relations, true
		}
		break
	}
	return nil, false
}

func (javaHelper *Helper) ResolveImplicitSelfCall(_ model.RawCall, _ []model.Symbol, _ map[string]*model.TypeEnv, _ string, _ *resolver.SymbolTable) ([]model.ResolvedRelation, bool) {
	return nil, false
}

func (javaHelper *Helper) ShouldFallthrough() bool { return false }

func (javaHelper *Helper) FilterGenerated(candidates []model.Symbol) []model.Symbol {
	var result []model.Symbol
	for _, candidate := range candidates {
		if !candidate.IsSynthetic {
			result = append(result, candidate)
		}
	}
	return result
}

func (javaHelper *Helper) IsTypeAssignable(argType, paramType string) bool {
	return IsTypeCompatible(argType, paramType)
}

func (javaHelper *Helper) ResolveOverload(candidates []model.Symbol, argTypes []string) *model.Symbol {
	return SelectMostSpecific(candidates, argTypes)
}

func (javaHelper *Helper) InferStringConcat(expr string) bool {
	return strings.Contains(expr, "+") && strings.Contains(expr, "\"")
}

func (javaHelper *Helper) LookupMethodReturn(typeName, methodName string) (string, bool) {
	return javaHelper.externalMethods.Lookup(typeName, methodName)
}

func (javaHelper *Helper) extractPackage(filePath string) string {
	for _, sym := range javaHelper.symbolTable.FindByFile(filePath) {
		if sym.Kind != constants.KindClass && sym.Kind != constants.KindInterface && sym.Kind != "abstract_class" {
			continue
		}
		if idx := strings.LastIndex(sym.QualifiedName, "."+sym.Name); idx > 0 {
			return sym.QualifiedName[:idx]
		}
	}
	return ""
}

func extractSymbolPackage(sym model.Symbol) string {
	if idx := strings.LastIndex(sym.QualifiedName, "."+sym.Name); idx > 0 {
		return sym.QualifiedName[:idx]
	}
	return ""
}

// IsConstructor returns true if the method is a Java constructor.
func (javaHelper *Helper) IsConstructor(method model.Symbol, className string) bool {
	return method.IsConstructor || method.Name == className
}

// IsOverrideMatch returns true if childMethod overrides parentMethod (same name + param count).
func (javaHelper *Helper) IsOverrideMatch(childMethod, parentMethod model.Symbol) bool {
	return childMethod.Name == parentMethod.Name &&
		resolver.CountParams(childMethod.Params) == resolver.CountParams(parentMethod.Params)
}

// InferImplements is a no-op for Java (explicit implements keyword).
func (javaHelper *Helper) InferImplements() []model.ResolvedRelation { return nil }
