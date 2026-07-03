package resolver

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

var resourceIDPattern = regexp.MustCompile(`R\.id\.(\w+)`)

// ResolveResourceReferences scans all RawCalls for binding.xxx, R.id.xxx, R.layout.xxx patterns
// and generates REFERENCES edges from Function → Widget/Layout.
func (resolver *Resolver) ResolveResourceReferences(allCalls []model.RawCall) []model.ResolvedRelation {
	// First pass: build file → layout name mapping from XxxBinding.inflate() calls
	fileToLayoutName := buildFileToLayoutMapping(allCalls)

	// Second pass: match patterns and generate REFERENCES edges
	var relations []model.ResolvedRelation

	for _, call := range allCalls {
		callerID := resolver.findCallerID(call)
		if callerID == "" {
			continue
		}

		// Pattern 1: binding.xxx in ReceiverExpr
		if strings.Contains(call.ReceiverExpr, "binding.") {
			widgetName := extractBindingWidgetName(call.ReceiverExpr)
			if widgetName != "" && widgetName != "root" {
				targetSymbol := resolver.findWidgetSymbol(widgetName, fileToLayoutName[call.FilePath])
				if targetSymbol != nil {
					relations = append(relations, model.ResolvedRelation{
						SourceID:   callerID,
						TargetID:   targetSymbol.ID,
						Kind:       model.RelReferences,
						SourceKind: constants.KindFunction,
						TargetKind: constants.KindWidget,
						Line:       call.Line,
						Metadata:   map[string]string{"ref_kind": "binding"},
					})
				}
			}
		}

		// Pattern 2: R.id.xxx in ReceiverExpr (e.g. findNavController(R.id.nav_host_fragment))
		if strings.Contains(call.ReceiverExpr, "R.id.") {
			matches := resourceIDPattern.FindAllStringSubmatch(call.ReceiverExpr, -1)
			for _, match := range matches {
				widgetName := match[1]
				targetSymbol := resolver.findWidgetSymbol(widgetName, "")
				if targetSymbol != nil {
					relations = append(relations, model.ResolvedRelation{
						SourceID:   callerID,
						TargetID:   targetSymbol.ID,
						Kind:       model.RelReferences,
						SourceKind: constants.KindFunction,
						TargetKind: constants.KindWidget,
						Line:       call.Line,
						Metadata:   map[string]string{"ref_kind": "R.id"},
					})
				}
			}
		}

		// Pattern 3: R.id.xxx / R.layout.xxx in ArgExprs
		for _, argExpression := range call.ArgExprs {
			if strings.Contains(argExpression, "R.id.") {
				matches := resourceIDPattern.FindAllStringSubmatch(argExpression, -1)
				for _, match := range matches {
					widgetName := match[1]
					targetSymbol := resolver.findWidgetSymbol(widgetName, "")
					if targetSymbol != nil {
						relations = append(relations, model.ResolvedRelation{
							SourceID:   callerID,
							TargetID:   targetSymbol.ID,
							Kind:       model.RelReferences,
							SourceKind: constants.KindFunction,
							TargetKind: constants.KindWidget,
							Line:       call.Line,
							Metadata:   map[string]string{"ref_kind": "R.id"},
						})
					}
				}
			}
			if strings.Contains(argExpression, "R.layout.") {
				layoutName := extractRLayoutName(argExpression)
				if layoutName != "" {
					layoutSymbols := resolver.symbolTable.FindByName(layoutName)
					for _, layoutSymbol := range layoutSymbols {
						if layoutSymbol.Kind == constants.KindLayout {
							relations = append(relations, model.ResolvedRelation{
								SourceID:   callerID,
								TargetID:   layoutSymbol.ID,
								Kind:       model.RelReferences,
								SourceKind: constants.KindFunction,
								TargetKind: constants.KindLayout,
								Line:       call.Line,
								Metadata:   map[string]string{"ref_kind": "R.layout"},
							})
							break
						}
					}
				}
			}
		}
	}

	return relations
}

// buildFileToLayoutMapping finds XxxBinding.inflate() calls and maps file → layout name.
func buildFileToLayoutMapping(allCalls []model.RawCall) map[string]string {
	fileToLayout := make(map[string]string)
	for _, call := range allCalls {
		if call.CalledName == "inflate" && strings.HasSuffix(call.ReceiverExpr, "Binding") {
			layoutName := bindingClassToLayoutName(call.ReceiverExpr)
			if layoutName != "" {
				fileToLayout[call.FilePath] = layoutName
			}
		}
	}
	return fileToLayout
}

// bindingClassToLayoutName converts "ActivityMainBinding" → "activity_main".
func bindingClassToLayoutName(bindingClassName string) string {
	// Remove "Binding" suffix
	withoutSuffix := strings.TrimSuffix(bindingClassName, "Binding")
	if withoutSuffix == "" || withoutSuffix == bindingClassName {
		return ""
	}
	// CamelCase → snake_case
	return camelToSnake(withoutSuffix)
}

// camelToSnake converts "ActivityMain" → "activity_main".
func camelToSnake(input string) string {
	var result strings.Builder
	for i, character := range input {
		if unicode.IsUpper(character) {
			if i > 0 {
				result.WriteByte('_')
			}
			result.WriteRune(unicode.ToLower(character))
		} else {
			result.WriteRune(character)
		}
	}
	return result.String()
}

// extractBindingWidgetName extracts "submitBtn" from "binding.submitBtn" or "binding.submitBtn.text".
func extractBindingWidgetName(receiverExpression string) string {
	// Find "binding." prefix and take the next segment
	bindingIndex := strings.Index(receiverExpression, "binding.")
	if bindingIndex < 0 {
		return ""
	}
	afterBinding := receiverExpression[bindingIndex+len("binding."):]
	// Take first segment (before next '.' if any)
	dotIndex := strings.Index(afterBinding, ".")
	if dotIndex > 0 {
		return afterBinding[:dotIndex]
	}
	return afterBinding
}

// extractRLayoutName extracts "activity_main" from "R.layout.activity_main".
func extractRLayoutName(expression string) string {
	index := strings.Index(expression, "R.layout.")
	if index < 0 {
		return ""
	}
	afterPrefix := expression[index+len("R.layout."):]
	// Take word characters only
	var result strings.Builder
	for _, character := range afterPrefix {
		if character == '_' || unicode.IsLetter(character) || unicode.IsDigit(character) {
			result.WriteRune(character)
		} else {
			break
		}
	}
	return result.String()
}

// findWidgetSymbol finds a Widget symbol by name, optionally scoped to a layout.
func (resolver *Resolver) findWidgetSymbol(widgetName string, layoutName string) *model.Symbol {
	// Precise match: layout_name.widget_name
	if layoutName != "" {
		qualifiedName := layoutName + "." + widgetName
		symbols := resolver.symbolTable.FindByQualifiedName(qualifiedName)
		for i := range symbols {
			if symbols[i].Kind == constants.KindWidget {
				return &symbols[i]
			}
		}
	}
	// Fallback: search by name
	symbols := resolver.symbolTable.FindByName(widgetName)
	for i := range symbols {
		if symbols[i].Kind == constants.KindWidget {
			return &symbols[i]
		}
	}
	return nil
}
