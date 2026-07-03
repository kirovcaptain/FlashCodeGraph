package android

import (
	"bytes"
	"encoding/xml"
	"strconv"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// ManifestComponent represents a component declared in AndroidManifest.xml.
type ManifestComponent struct {
	QualifiedName string
	ComponentType string // "activity", "service", "receiver", "provider"
	IsLauncher    bool
	DeepLinks     []string
}

// ExtractManifest parses AndroidManifest.xml and extracts AppComponent symbols + REFERENCES edges.
func ExtractManifest(content []byte, relPath string, modulePackage string, result *model.ParseResult) {
	packageName := extractManifestPackage(content)
	if packageName == "" {
		packageName = modulePackage
	}

	decoder := xml.NewDecoder(bytes.NewReader(content))
	var currentComponent *ManifestComponent
	var insideIntentFilter bool
	var hasMainAction bool
	var hasLauncherCategory bool

	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}

		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "activity", "service", "receiver", "provider":
				name := getAttr(element, "name")
				if name == "" {
					continue
				}
				qualifiedName := resolveComponentName(name, packageName)
				currentComponent = &ManifestComponent{
					QualifiedName: qualifiedName,
					ComponentType: element.Name.Local,
				}
			case "intent-filter":
				if currentComponent != nil {
					insideIntentFilter = true
					hasMainAction = false
					hasLauncherCategory = false
				}
			case "action":
				if insideIntentFilter {
					if getAttr(element, "name") == "android.intent.action.MAIN" {
						hasMainAction = true
					}
				}
			case "category":
				if insideIntentFilter {
					if getAttr(element, "name") == "android.intent.category.LAUNCHER" {
						hasLauncherCategory = true
					}
				}
			case "data":
				if insideIntentFilter && currentComponent != nil {
					scheme := getAttr(element, "scheme")
					host := getAttr(element, "host")
					if scheme != "" && host != "" {
						currentComponent.DeepLinks = append(currentComponent.DeepLinks, scheme+"://"+host)
					}
				}
			}

		case xml.EndElement:
			switch element.Name.Local {
			case "intent-filter":
				if insideIntentFilter && currentComponent != nil {
					if hasMainAction && hasLauncherCategory {
						currentComponent.IsLauncher = true
					}
					insideIntentFilter = false
				}
			case "activity", "service", "receiver", "provider":
				if currentComponent != nil {
					componentID := astutil.GenerateSymbolID(relPath, currentComponent.QualifiedName, 1)
					result.Symbols = append(result.Symbols, model.Symbol{
						ID:            componentID,
						Name:          lastSegment(currentComponent.QualifiedName),
						QualifiedName: currentComponent.QualifiedName,
						Kind:          constants.KindAppComponent,
						ClassType:     currentComponent.ComponentType,
						FilePath:      relPath,
						Metadata: map[string]string{
							"is_launcher": strconv.FormatBool(currentComponent.IsLauncher),
							"deep_links":  strings.Join(currentComponent.DeepLinks, ","),
						},
					})

					// REFERENCES edge: AppComponent → Class (TargetID stores QualifiedName for later resolution)
					result.Edges = append(result.Edges, model.Edge{
						SourceID:   componentID,
						TargetID:   currentComponent.QualifiedName,
						Kind:       model.RelReferences,
						SourceKind: constants.KindAppComponent,
						TargetKind: constants.KindClass,
						Properties: map[string]any{"ref_kind": "manifest"},
					})
					currentComponent = nil
				}
			}
		}
	}
}

// extractManifestPackage reads the package attribute from <manifest>.
func extractManifestPackage(content []byte) string {
	decoder := xml.NewDecoder(bytes.NewReader(content))
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		if startElement, ok := token.(xml.StartElement); ok && startElement.Name.Local == "manifest" {
			for _, attr := range startElement.Attr {
				if attr.Name.Local == "package" {
					return attr.Value
				}
			}
			break
		}
	}
	return ""
}

// resolveComponentName converts relative name (.MainActivity) to full qualified name.
func resolveComponentName(name string, packageName string) string {
	if strings.HasPrefix(name, ".") {
		return packageName + name
	}
	if !strings.Contains(name, ".") {
		return packageName + "." + name
	}
	return name
}

// lastSegment returns the part after the last dot.
func lastSegment(qualifiedName string) string {
	if idx := strings.LastIndex(qualifiedName, "."); idx >= 0 {
		return qualifiedName[idx+1:]
	}
	return qualifiedName
}

// getAttr returns the value of an attribute by local name (ignoring namespace).
func getAttr(element xml.StartElement, localName string) string {
	for _, attr := range element.Attr {
		if attr.Name.Local == localName {
			return attr.Value
		}
	}
	return ""
}
