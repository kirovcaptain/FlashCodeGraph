package resolver

// Exported wrappers for use by language helper subpackages (resolver/java, etc).
// Internal callers continue using the unexported versions.

import (
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func FilterByOwnerClass(candidates []model.Symbol, className string) []model.Symbol {
	return filterByOwnerClass(candidates, className)
}

func FilterByArgCount(candidates []model.Symbol, argCount int) []model.Symbol {
	return filterByArgCount(candidates, argCount)
}

func FilterByArgTypesWithHelper(candidates []model.Symbol, argTypes []string, langHelper LanguageHelper) []model.Symbol {
	return filterByArgTypes(candidates, argTypes, langHelper)
}

func MakeRelation(sourceID, targetID string, call model.RawCall, confidence float64, resolvedBy string, candidates int) model.ResolvedRelation {
	return makeRelation(sourceID, targetID, call, confidence, resolvedBy, candidates)
}

func MakeMultiRelations(sourceID string, candidates []model.Symbol, call model.RawCall, baseConfidence float64, resolvedBy string) []model.ResolvedRelation {
	return makeMultiRelations(sourceID, candidates, call, baseConfidence, resolvedBy)
}

// ExtractCallerClassQN extracts the fully qualified class name from a caller's qualified name.
// "com.example.dao.ChildDao.methodName" → "com.example.dao.ChildDao"
// "ChildDao.methodName" → "ChildDao"
// "methodName" → ""
func ExtractCallerClassQN(callerName string) string {
	if dotIdx := strings.LastIndex(callerName, "."); dotIdx >= 0 {
		return callerName[:dotIdx]
	}
	return ""
}

func FindMethodInHierarchyPublic(symbolTable *SymbolTable, className, methodName string, heritage []model.RawHeritage) *model.Symbol {
	r := &Resolver{symbolTable: symbolTable, heritage: heritage}
	return r.FindMethodInHierarchy(className, methodName, heritage)
}

func EnrichArgTypes(call model.RawCall, envs map[string]*model.TypeEnv, symbolTable *SymbolTable, langHelper LanguageHelper) []string {
	r := &Resolver{symbolTable: symbolTable}
	return r.enrichArgTypes(call, envs, langHelper)
}

func CountParams(params []model.ParamInfo) int {
	return len(params)
}

