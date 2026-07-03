// Package android provides XML parsers for Android resource files.
package android

import (
	"bytes"
	"encoding/xml"
	"path/filepath"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// ExtractLayout parses a layout XML file and extracts Layout/Widget symbols and edges.
func ExtractLayout(content []byte, relPath string, result *model.ParseResult) {
	layoutName := strings.TrimSuffix(filepath.Base(relPath), ".xml")
	layoutID := generateLayoutID(layoutName)

	result.Symbols = append(result.Symbols, model.Symbol{
		ID:            layoutID,
		Name:          layoutName,
		QualifiedName: layoutName,
		Kind:          constants.KindLayout,
		FilePath:      relPath,
		StartLine:     1,
	})

	decoder := xml.NewDecoder(bytes.NewReader(content))
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}

		startElement, ok := token.(xml.StartElement)
		if !ok {
			continue
		}

		tagName := startElement.Name.Local

		// Handle <include layout="@layout/xxx">
		if tagName == "include" {
			includedLayout := getAttr(startElement, "layout")
			if ref := extractLayoutRef(includedLayout); ref != "" {
				targetID := generateLayoutID(ref)
				result.Edges = append(result.Edges, model.Edge{
					SourceID:   layoutID,
					TargetID:   targetID,
					Kind:       model.RelIncludes,
					SourceKind: constants.KindLayout,
					TargetKind: constants.KindLayout,
				})
			}
			continue
		}

		// Extract android:id and android:name
		widgetID := extractIDValue(getAndroidAttr(startElement, "id"))
		androidName := getAndroidAttr(startElement, "name")

		if widgetID != "" {
			// Use widget name for stable ID (not dependent on token order)
			widgetSymbolID := astutil.GenerateSymbolID(relPath, layoutName+"."+widgetID, 1)
			widgetType := tagName // For custom views, tagName is already the full class name

			// If this is a fragment with android:name, use the class name as widget type
			if tagName == "fragment" && androidName != "" {
				widgetType = androidName
			}

			result.Symbols = append(result.Symbols, model.Symbol{
				ID:            widgetSymbolID,
				Name:          widgetID,
				QualifiedName: layoutName + "." + widgetID,
				Kind:          constants.KindWidget,
				ClassType:     widgetType,
				FilePath:      relPath,
			})

			// Layout CONTAINS Widget
			result.Edges = append(result.Edges, model.Edge{
				SourceID:   layoutID,
				TargetID:   widgetSymbolID,
				Kind:       model.RelContains,
				SourceKind: constants.KindLayout,
				TargetKind: constants.KindWidget,
			})

			// REFERENCES edge for custom views and fragments (widgetType contains '.')
			if strings.Contains(widgetType, ".") {
				result.Edges = append(result.Edges, model.Edge{
					SourceID:   widgetSymbolID,
					TargetID:   widgetType,
					Kind:       model.RelReferences,
					SourceKind: constants.KindWidget,
					TargetKind: constants.KindClass,
					Properties: map[string]any{"ref_kind": "custom_view"},
				})
			}
		} else if androidName != "" && strings.Contains(androidName, ".") {
			// Fragment without id but with android:name — create reference symbol + REFERENCES edge
			refID := astutil.GenerateSymbolID(relPath, "ref:"+androidName, 1)
			result.Symbols = append(result.Symbols, model.Symbol{
				ID:            refID,
				Name:          androidName,
				QualifiedName: "ref:" + androidName,
				Kind:          constants.KindWidget,
				ClassType:     "fragment",
				FilePath:      relPath,
			})
			result.Edges = append(result.Edges, model.Edge{
				SourceID:   refID,
				TargetID:   androidName,
				Kind:       model.RelReferences,
				SourceKind: constants.KindWidget,
				TargetKind: constants.KindClass,
				Properties: map[string]any{"ref_kind": "custom_view"},
			})
		}
	}
}

// getAndroidAttr returns the value of an android: namespaced attribute.
func getAndroidAttr(element xml.StartElement, localName string) string {
	for _, attr := range element.Attr {
		if attr.Name.Local == localName &&
			(attr.Name.Space == "http://schemas.android.com/apk/res/android" || attr.Name.Space == "") {
			return attr.Value
		}
	}
	return ""
}

// extractIDValue extracts the ID name from "@+id/submitBtn" or "@id/submitBtn".
func extractIDValue(value string) string {
	if strings.HasPrefix(value, "@+id/") {
		return value[5:]
	}
	if strings.HasPrefix(value, "@id/") {
		return value[4:]
	}
	return ""
}

// extractLayoutRef extracts layout name from "@layout/toolbar_common".
func extractLayoutRef(value string) string {
	if strings.HasPrefix(value, "@layout/") {
		return value[8:]
	}
	return ""
}

// generateLayoutID creates a deterministic ID for a Layout based on name only (not filePath).
// Android resources are globally unique by name across all modules.
func generateLayoutID(layoutName string) string {
	return astutil.GenerateSymbolID("", layoutName, 1)
}
