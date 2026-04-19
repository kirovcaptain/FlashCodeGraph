package annotation

import "strings"

// BuildWhitelist constructs the annotation whitelist from detected frameworks + user config.
func BuildWhitelist(frameworks []string, include, exclude []string) map[string]AnnotationDef {
	wl := make(map[string]AnnotationDef)

	// Always-on annotations
	for _, def := range DefaultAnnotations["_always"] {
		wl[def.Name] = def
	}
	// Test annotations
	for _, def := range DefaultAnnotations["_test"] {
		wl[def.Name] = def
	}
	// Framework-specific
	for _, fw := range frameworks {
		for _, def := range DefaultAnnotations[fw] {
			wl[def.Name] = def
		}
	}
	// User include
	for _, name := range include {
		if _, exists := wl[name]; !exists {
			wl[name] = AnnotationDef{Name: name, Category: "custom", Framework: "custom"}
		}
	}
	// User exclude
	for _, name := range exclude {
		delete(wl, name)
	}
	return wl
}

// ExtractBaseName extracts the simple annotation name from a full annotation string.
//
//	"@Service" → "Service"
//	"@Transactional(readOnly=true)" → "Transactional"
//	"@org.springframework.stereotype.Service" → "Service"
//	"@strawberry.field" → "strawberry.field"
func ExtractBaseName(ann string) string {
	ann = strings.TrimPrefix(ann, "@")
	// Strip parameters: "Transactional(readOnly=true)" → "Transactional"
	if idx := strings.Index(ann, "("); idx >= 0 {
		ann = ann[:idx]
	}
	ann = strings.TrimSpace(ann)
	// For Java fully-qualified: take last segment only if 3+ segments (package.class.Annotation)
	parts := strings.Split(ann, ".")
	if len(parts) >= 3 {
		return parts[len(parts)-1]
	}
	// For 2-segment (strawberry.field, app.route): keep as-is
	return ann
}

// ExtractParams extracts annotation parameters as a string.
//
//	"@Cacheable(value=\"users\", key=\"#id\")" → "value=\"users\", key=\"#id\""
//	"@Service" → ""
func ExtractParams(ann string) string {
	start := strings.Index(ann, "(")
	if start < 0 {
		return ""
	}
	end := strings.LastIndex(ann, ")")
	if end <= start {
		return ""
	}
	return strings.TrimSpace(ann[start+1 : end])
}
