package java

import (
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// IsTypeCompatible checks Java type assignability: exact match + boxing + JDK hierarchy.
func IsTypeCompatible(argType, paramType string) bool {
	if argType == paramType {
		return true
	}
	boxMap := map[string]string{
		"int": "Integer", "Integer": "int",
		"long": "Long", "Long": "long",
		"double": "Double", "Double": "double",
		"float": "Float", "Float": "float",
		"boolean": "Boolean", "Boolean": "boolean",
		"char": "Character", "Character": "char",
		"byte": "Byte", "Byte": "byte",
		"short": "Short", "Short": "short",
	}
	if boxed, ok := boxMap[argType]; ok && boxed == paramType {
		return true
	}
	if boxed, ok := boxMap[argType]; ok && argType[0] >= 'a' && argType[0] <= 'z' {
		if isJDKSubtype(boxed, paramType) {
			return true
		}
	}
	if isJDKSubtype(argType, paramType) {
		return true
	}
	return false
}

// SelectMostSpecific picks the Java overload with closest parameter types in JDK hierarchy.
func SelectMostSpecific(candidates []model.Symbol, argTypes []string) *model.Symbol {
	bestIdx := -1
	bestScore := -1
	bestIsVarargs := true

	for i, candidate := range candidates {
		params := extractParamTypes(candidate.Params)
		if len(params) == 0 && len(argTypes) > 0 {
			continue
		}
		isVarargs := len(params) > 0 && strings.HasSuffix(params[len(params)-1], "...")
		score := 0
		valid := true
		for j, argType := range argTypes {
			if argType == "" || argType == "null" {
				continue
			}
			var paramType string
			if j < len(params) {
				paramType = strings.TrimSuffix(params[j], "...")
			} else if isVarargs {
				paramType = strings.TrimSuffix(params[len(params)-1], "...")
			} else {
				valid = false
				break
			}
			if isGenericTypeParam(candidate, paramType) {
				score += 100
				continue
			}
			if argType == paramType {
				continue
			}
			depth := jdkTypeDepth(argType, paramType)
			if depth < 0 {
				valid = false
				break
			}
			score += depth
		}
		if !valid {
			continue
		}
		if bestIdx < 0 || score < bestScore || (score == bestScore && !isVarargs && bestIsVarargs) {
			bestIdx = i
			bestScore = score
			bestIsVarargs = isVarargs
		}
	}
	if bestIdx >= 0 {
		return &candidates[bestIdx]
	}
	return nil
}

// isGenericTypeParam returns true if paramType is a generic type parameter of the candidate.
func isGenericTypeParam(candidate model.Symbol, paramType string) bool {
	if len(candidate.TypeParams) > 0 {
		for _, typeParam := range candidate.TypeParams {
			if typeParam == paramType {
				return true
			}
		}
		return false
	}
	return isSingleLetterGeneric(paramType)
}

func isSingleLetterGeneric(typeName string) bool {
	return len(typeName) == 1 && typeName[0] >= 'A' && typeName[0] <= 'Z'
}

func extractParamTypes(params []model.ParamInfo) []string {
	types := make([]string, len(params))
	for i, p := range params {
		types[i] = p.Type
	}
	return types
}
