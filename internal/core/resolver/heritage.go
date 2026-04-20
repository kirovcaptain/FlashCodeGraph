package resolver

import (
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// ResolveHeritage resolves inheritance/implementation relationships.
func (resolver *Resolver) ResolveHeritage(heritage []model.RawHeritage) []model.ResolvedRelation {
	var relations []model.ResolvedRelation

	for _, entry := range heritage {
		// Prefer qualifiedName lookup for child (avoids cross-package mismatch)
		var child *model.Symbol
		if entry.ChildQualified != "" {
			candidates := resolver.symbolTable.FindByQualifiedName(entry.ChildQualified)
			if len(candidates) == 1 {
				candidateCopy := candidates[0]
				child = &candidateCopy
			}
		}
		if child == nil {
			childCandidates := resolver.symbolTable.FindByName(entry.ChildName)
			for _, candidate := range childCandidates {
				if candidate.FilePath == entry.FilePath {
					candidateCopy := candidate
					child = &candidateCopy
					break
				}
			}
		}
		if child == nil {
			continue
		}

		var parentCandidates []model.Symbol
		if entry.ParentQualified != "" {
			parentCandidates = resolver.symbolTable.FindByQualifiedName(entry.ParentQualified)
		}
		if len(parentCandidates) == 0 {
			parentCandidates = resolver.symbolTable.FindByName(entry.ParentName)
		}

		if len(parentCandidates) == 0 {
			continue
		}

		// Pick best parent match
		parent := &parentCandidates[0]
		confidence := ConfidenceNameUnique // multiple candidates
		if len(parentCandidates) == 1 {
			confidence = ConfidenceArgCount // unique candidate (reuses 0.85 tier)
		}

		kind := model.RelExtends
		if entry.Kind == "implements" {
			kind = model.RelImplements
		}

		relations = append(relations, model.ResolvedRelation{
			SourceID:   child.ID,
			TargetID:   parent.ID,
			Kind:       kind,
			SourceKind: child.Kind,
			Confidence: confidence,
			ResolvedBy: "heritage_" + entry.Kind,
			Candidates: len(parentCandidates),
		})
	}

	return relations
}

// DetectOverrides finds methods in child classes that share the same name as methods
// in ancestor classes, producing OVERRIDES relations. Uses BFS to walk the full
// parent chain (including grandparents), so transitive overrides are detected.
// Constructors (__init__, constructor) are excluded since they are not true overrides.
func (resolver *Resolver) DetectOverrides(heritage []model.RawHeritage) []model.ResolvedRelation {
	qnParentMap := resolver.getQualifiedParentMap(heritage)

	// Build childQualified → filePath + shortName mapping for helper selection
	childFileMap := make(map[string]string)
	childShortNameMap := make(map[string]string)
	for _, entry := range heritage {
		if entry.ChildQualified != "" {
			childFileMap[entry.ChildQualified] = entry.FilePath
			childShortNameMap[entry.ChildQualified] = entry.ChildName
		}
	}

	var relations []model.ResolvedRelation

	for childQN, parentQNs := range qnParentMap {
		childMethods := resolver.symbolTable.FindMethodsByQualifiedName(childQN)
		if len(childMethods) == 0 {
			continue
		}

		helper := resolver.helperForFile(childFileMap[childQN])
		childShortName := childShortNameMap[childQN]

		visited := make(map[string]bool)
		queue := make([]string, len(parentQNs))
		copy(queue, parentQNs)

		for len(queue) > 0 {
			parentQN := queue[0]
			queue = queue[1:]
			if visited[parentQN] {
				continue
			}
			visited[parentQN] = true

			parentMethods := resolver.symbolTable.FindMethodsByQualifiedName(parentQN)

			for _, childMethod := range childMethods {
				if helper != nil && helper.IsConstructor(childMethod, childShortName) {
					continue
				} else if helper == nil && childMethod.IsConstructor {
					continue
				}

				for _, parentMethod := range parentMethods {
					if childMethod.ID == parentMethod.ID {
						continue
					}
					matched := false
					if helper != nil {
						matched = helper.IsOverrideMatch(childMethod, parentMethod)
					} else {
						matched = childMethod.Name == parentMethod.Name &&
							countParams(childMethod.Params) == countParams(parentMethod.Params)
					}
					if matched {
						relations = append(relations, model.ResolvedRelation{
							SourceID: childMethod.ID, TargetID: parentMethod.ID,
							Kind: model.RelOverrides, SourceKind: constants.KindFunction,
							Confidence: 1.0, ResolvedBy: "override_detection", Candidates: 1,
						})
						relations = append(relations, model.ResolvedRelation{
							SourceID: parentMethod.ID, TargetID: childMethod.ID,
							Kind: model.RelDispatches, SourceKind: constants.KindFunction,
							Confidence: 1.0, ResolvedBy: "interface_dispatch", Candidates: 1,
						})
					}
				}
			}

			// Walk up: parent's parents are also in qnParentMap
			if grandParentQNs, exists := qnParentMap[parentQN]; exists {
				queue = append(queue, grandParentQNs...)
			}
		}
	}

	return relations
}

// FindMethodInHierarchy walks the inheritance chain (BFS) starting from className
// to locate the first definition of methodName. Used by the call resolver to
// resolve calls like child.parentMethod() where the method is defined in an ancestor.
// Returns nil if the method is not found anywhere in the hierarchy.
func (resolver *Resolver) FindMethodInHierarchy(className, methodName string, heritage []model.RawHeritage) *model.Symbol {
	// Result cache: same (className, methodName) → same result
	cacheKey := className + ":" + methodName
	if resolver.hierarchyCache == nil {
		resolver.hierarchyCache = make(map[string]*model.Symbol)
	}
	if cached, exists := resolver.hierarchyCache[cacheKey]; exists {
		return cached
	}

	qnParentMap := resolver.getQualifiedParentMap(heritage)

	// Resolve className to qualified name
	startQN := ""
	candidates := resolver.symbolTable.FindByName(className)
	for _, c := range candidates {
		if c.Kind == constants.KindClass || c.Kind == "abstract_class" ||
			c.Kind == constants.KindInterface || c.ClassType == "struct" {
			startQN = c.QualifiedName
			break
		}
	}
	if startQN == "" {
		startQN = className
	}

	visited := make(map[string]bool)
	queue := []string{startQN}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if visited[current] {
			continue
		}
		visited[current] = true

		methods := resolver.symbolTable.FindMethodsByQualifiedName(current)
		for _, method := range methods {
			if method.Name == methodName {
				resolver.hierarchyCache[cacheKey] = &method
				return &method
			}
		}

		if parents, exists := qnParentMap[current]; exists {
			queue = append(queue, parents...)
		}
	}

	resolver.hierarchyCache[cacheKey] = nil
	return nil
}

// getQualifiedParentMap returns cached qualifiedParentMap, building it on first call.
func (resolver *Resolver) getQualifiedParentMap(heritage []model.RawHeritage) map[string][]string {
	if resolver.qualifiedParentMap != nil {
		return resolver.qualifiedParentMap
	}
	resolver.qualifiedParentMap = resolver.buildQualifiedParentMap(heritage)
	return resolver.qualifiedParentMap
}

// buildQualifiedParentMap builds a mapping from child qualified name to parent qualified names.
// Uses ChildQualified from heritage and resolves parent qualified names via symbolTable.
func (resolver *Resolver) buildQualifiedParentMap(heritage []model.RawHeritage) map[string][]string {
	parentQNCache := make(map[string]string)
	for _, entry := range heritage {
		// Use ParentQualified if available (cross-package embedding)
		if entry.ParentQualified != "" {
			candidates := resolver.symbolTable.FindByQualifiedName(entry.ParentQualified)
			if len(candidates) > 0 {
				parentQNCache[entry.ParentName] = candidates[0].QualifiedName
				continue
			}
		}
		if _, exists := parentQNCache[entry.ParentName]; exists {
			continue
		}
		candidates := resolver.symbolTable.FindByName(entry.ParentName)
		for _, c := range candidates {
			if c.Kind == constants.KindClass || c.Kind == "abstract_class" ||
				c.Kind == constants.KindInterface || c.ClassType == "struct" {
				parentQNCache[entry.ParentName] = c.QualifiedName
				break
			}
		}
	}

	qnParentMap := make(map[string][]string)
	for _, entry := range heritage {
		childQN := entry.ChildQualified
		if childQN == "" {
			continue
		}
		parentQN := parentQNCache[entry.ParentName]
		if parentQN == "" {
			continue
		}
		qnParentMap[childQN] = append(qnParentMap[childQN], parentQN)
	}
	return qnParentMap
}

// lookupFieldInHierarchy walks the inheritance chain to find a field's type.
func (resolver *Resolver) lookupFieldInHierarchy(callerName, fieldName string, envs map[string]*model.TypeEnv) string {
	dotIdx := strings.LastIndex(callerName, ".")
	if dotIdx < 0 {
		return ""
	}
	classQN := callerName[:dotIdx]

	// O(1) lookup via globalBindings instead of iterating all envs
	if typeName, exists := resolver.globalBindings[classQN+":"+fieldName]; exists {
		return typeName
	}

	// Walk up parent classes using heritageByChild index
	className := classQN
	if lastDot := strings.LastIndex(classQN, "."); lastDot >= 0 {
		className = classQN[lastDot+1:]
	}
	visited := map[string]bool{className: true}
	queue := []string{className}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, heritageEntry := range resolver.heritageByChild[current] {
			if visited[heritageEntry.ParentName] {
				continue
			}
			visited[heritageEntry.ParentName] = true
			queue = append(queue, heritageEntry.ParentName)
			var parentCandidates []model.Symbol
			if heritageEntry.ParentQualified != "" {
				parentCandidates = resolver.symbolTable.FindByQualifiedName(heritageEntry.ParentQualified)
			}
			if len(parentCandidates) == 0 {
				parentCandidates = resolver.symbolTable.FindByName(heritageEntry.ParentName)
			}
			for _, parentCandidate := range parentCandidates {
				if parentCandidate.Kind == "class" || parentCandidate.Kind == "abstract_class" || parentCandidate.Kind == "interface" || parentCandidate.ClassType == "struct" {
					parentKey := parentCandidate.QualifiedName + ":" + fieldName
					if typeName, exists := resolver.globalBindings[parentKey]; exists {
						return typeName
					}
				}
			}
		}
	}
	return ""
}

// helperForFile selects a LanguageHelper by file extension.
// Returns nil if no helper is registered for the language (does NOT panic).
func (resolver *Resolver) helperForFile(filePath string) LanguageHelper {
	var lang string
	switch {
	case strings.HasSuffix(filePath, ".java"):
		lang = "java"
	case strings.HasSuffix(filePath, ".go"):
		lang = "go"
	case strings.HasSuffix(filePath, ".py"):
		lang = "python"
	case strings.HasSuffix(filePath, ".ts"), strings.HasSuffix(filePath, ".tsx"):
		lang = "typescript"
	}
	if helper, ok := resolver.langHelpers[lang]; ok {
		return helper
	}
	return nil
}
