package android

import (
	"bytes"
	"encoding/xml"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// pendingNavigation holds an action's widget ID and its destination reference for deferred edge creation.
type pendingNavigation struct {
	actionWidgetID  string
	destinationRef  string
}

// pendingReference holds a destination widget ID and its implementation class name for deferred edge creation.
type pendingReference struct {
	widgetID  string
	className string
}

// ExtractNavigation parses a navigation XML file and extracts Widget nodes for destinations and actions,
// NAVIGATES_TO edges for action→destination, REFERENCES edges for destination→class, and Route nodes.
func ExtractNavigation(content []byte, relPath string, result *model.ParseResult) {
	decoder := xml.NewDecoder(bytes.NewReader(content))

	destinationWidgetIDs := make(map[string]string) // destination id name → widget symbol ID
	var pendingNavigations []pendingNavigation
	var pendingReferences []pendingReference

	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}

		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "fragment", "activity", "dialog", "navigation":
				destinationID := extractIDValue(getAttr(element, "id"))
				if destinationID == "" {
					break
				}

				// Widget node for destination
				widgetSymbolID := astutil.GenerateSymbolID(relPath, "nav:"+destinationID, 1)
				destinationWidgetIDs[destinationID] = widgetSymbolID

				result.Symbols = append(result.Symbols, model.Symbol{
					ID:            widgetSymbolID,
					Name:          destinationID,
					QualifiedName: "nav:" + destinationID,
					Kind:          constants.KindWidget,
					ClassType:     element.Name.Local,
					FilePath:      relPath,
				})

				// Route (existing behavior) — navigation containers are not navigable destinations
				if element.Name.Local != "navigation" {
					label := getAttr(element, "label")
					routePath := destinationID
					if label != "" {
						routePath = label
					}
					result.Routes = append(result.Routes, model.RawRoute{
						Method:      "NAVIGATE",
						PathPattern: routePath,
						Handlers:    nil,
						Framework:   "android-navigation",
						FilePath:    relPath,
					})
				}

				// Defer REFERENCES edge: destination → implementation class
				className := getAttr(element, "name")
				if className != "" {
					pendingReferences = append(pendingReferences, pendingReference{
						widgetID:  widgetSymbolID,
						className: className,
					})
				}

			case "action":
				actionID := extractIDValue(getAttr(element, "id"))
				if actionID == "" {
					break
				}

				var destinationRef string
				for _, attr := range element.Attr {
					switch attr.Name.Local {
					case "destination":
						destinationRef = extractIDValue(attr.Value)
					case "popUpTo":
						if destinationRef == "" {
							destinationRef = extractIDValue(attr.Value)
						}
					}
				}

				// Widget node for action
				actionWidgetID := astutil.GenerateSymbolID(relPath, "nav:"+actionID, 1)
				result.Symbols = append(result.Symbols, model.Symbol{
					ID:            actionWidgetID,
					Name:          actionID,
					QualifiedName: "nav:" + actionID,
					Kind:          constants.KindWidget,
					ClassType:     "nav_action",
					FilePath:      relPath,
				})

				// Route (existing behavior)
				if destinationRef != "" {
					result.Routes = append(result.Routes, model.RawRoute{
						Method:      "ACTION",
						PathPattern: actionID,
						Handlers:    nil,
						Framework:   "android-navigation",
						FilePath:    relPath,
					})
				}

				// Defer NAVIGATES_TO edge
				if destinationRef != "" {
					pendingNavigations = append(pendingNavigations, pendingNavigation{
						actionWidgetID: actionWidgetID,
						destinationRef: destinationRef,
					})
				}

			case "deepLink":
				uri := getAttr(element, "uri")
				if uri != "" {
					result.Routes = append(result.Routes, model.RawRoute{
						Method:      "DEEP_LINK",
						PathPattern: uri,
						Handlers:    nil,
						Framework:   "android-navigation",
						FilePath:    relPath,
					})
				}
			}

		case xml.EndElement:
			// Reserved for future use (e.g. deepLink → destination association)
		}
	}

	// Generate NAVIGATES_TO edges
	for _, pending := range pendingNavigations {
		targetWidgetID, exists := destinationWidgetIDs[pending.destinationRef]
		if exists {
			result.Edges = append(result.Edges, model.Edge{
				SourceID:   pending.actionWidgetID,
				TargetID:   targetWidgetID,
				Kind:       model.RelNavigatesTo,
				SourceKind: constants.KindWidget,
				TargetKind: constants.KindWidget,
			})
		}
	}

	// Generate REFERENCES edges (destination → implementation class)
	for _, pending := range pendingReferences {
		result.Edges = append(result.Edges, model.Edge{
			SourceID:   pending.widgetID,
			TargetID:   pending.className,
			Kind:       model.RelReferences,
			SourceKind: constants.KindWidget,
			TargetKind: constants.KindClass,
			Properties: map[string]any{"ref_kind": "nav_destination"},
		})
	}

	// Start destination route
	startDestination := extractStartDestination(content)
	if startDestination != "" {
		result.Routes = append(result.Routes, model.RawRoute{
			Method:      "START_DESTINATION",
			PathPattern: startDestination,
			Handlers:    nil,
			Framework:   "android-navigation",
			FilePath:    relPath,
		})
	}
}

// extractStartDestination reads app:startDestination from <navigation>.
func extractStartDestination(content []byte) string {
	decoder := xml.NewDecoder(bytes.NewReader(content))
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		if startElement, ok := token.(xml.StartElement); ok && startElement.Name.Local == "navigation" {
			for _, attr := range startElement.Attr {
				if attr.Name.Local == "startDestination" {
					return extractIDValue(attr.Value)
				}
			}
			break
		}
	}
	return ""
}
