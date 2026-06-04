package java

import (
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

var javaRouteAnnotations = map[string]string{
	"GetMapping":     "GET",
	"PostMapping":    "POST",
	"PutMapping":     "PUT",
	"DeleteMapping":  "DELETE",
	"PatchMapping":   "PATCH",
	"RequestMapping": "GET", // default, actual method from annotation params
}

// ExtractRoutes extracts HTTP route definitions from Spring MVC annotations (@GetMapping, @PostMapping, @RequestMapping, etc.).
// Supports multiple methods and multiple paths via cartesian product (classRoutes × methods × paths).
func ExtractRoutes(annotations []model.StructuredAnnotation, classAnnotations []model.StructuredAnnotation, methodName, className, filePath string, startLine int, result *model.ParseResult) {
	// FeignClient routes are handled by ExtractFeignClient with correct path prefix
	if HasFeignClient(classAnnotations) {
		return
	}

	// Get class-level route prefixes from class annotations (supports multi-path)
	classRoutes := []string{""}
	for _, annotation := range classAnnotations {
		if annotation.Name == "RequestMapping" {
			parsed := parseMultiValue(annotation.Params["value"])
			if len(parsed) > 0 {
				classRoutes = parsed
			}
		}
	}

	for _, annotation := range annotations {
		defaultMethod, isRoute := javaRouteAnnotations[annotation.Name]
		if !isRoute {
			continue
		}

		// Determine methods
		var methods []string
		if annotation.Name == "RequestMapping" {
			if methodParam := annotation.Params["method"]; methodParam != "" {
				methods = mapRequestMethods(methodParam)
			}
			if len(methods) == 0 {
				methods = []string{defaultMethod}
			}
		} else {
			methods = []string{defaultMethod}
		}

		// Determine paths
		paths := parseMultiValue(annotation.Params["value"])

		// Cartesian product: classRoutes × methods × paths
		for _, classPrefix := range classRoutes {
			for _, httpMethod := range methods {
				for _, pathPattern := range paths {
					fullPath := classPrefix + pathPattern
					result.Routes = append(result.Routes, model.RawRoute{
						Method:      httpMethod,
						PathPattern: fullPath,
						Handlers:    []string{className + "." + methodName},
						Framework:   "spring",
						FilePath:    filePath,
						Line:        startLine,
					})
				}
			}
		}
	}
}

// mapRequestMethods parses a method annotation value that may contain multiple methods
// (e.g. "{RequestMethod.GET, RequestMethod.POST}") and returns the corresponding HTTP methods.
func mapRequestMethods(methodParam string) []string {
	methodPatterns := map[string]string{
		"RequestMethod.GET":    "GET",
		"RequestMethod.POST":   "POST",
		"RequestMethod.PUT":    "PUT",
		"RequestMethod.DELETE": "DELETE",
		"RequestMethod.PATCH":  "PATCH",
	}
	var results []string
	parts := parseMultiValue(methodParam)
	for _, part := range parts {
		for pattern, method := range methodPatterns {
			if strings.Contains(part, pattern) {
				results = append(results, method)
				break
			}
		}
	}
	return results
}

// parseMultiValue splits annotation array values like {"/a", "/b"} into individual strings.
// Single values like "/a" return a one-element slice. Empty input returns [""].
func parseMultiValue(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{""}
	}
	// Remove outer braces if present: {"/a", "/b"} → "/a", "/b"
	if strings.HasPrefix(raw, "{") && strings.HasSuffix(raw, "}") {
		raw = raw[1 : len(raw)-1]
	}
	parts := strings.Split(raw, ",")
	var results []string
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		trimmed = strings.Trim(trimmed, "\"")
		results = append(results, trimmed)
	}
	if len(results) == 0 {
		return []string{""}
	}
	return results
}

func extractAnnotationValue(annotation string) string {
	start := strings.Index(annotation, "(")
	if start < 0 {
		return ""
	}
	end := strings.LastIndex(annotation, ")")
	if end <= start {
		return ""
	}
	inner := strings.TrimSpace(annotation[start+1 : end])

	// Remove "value=" or "path=" prefix (with optional spaces)
	for _, prefix := range []string{"value", "path"} {
		if strings.HasPrefix(inner, prefix) {
			rest := strings.TrimPrefix(inner, prefix)
			rest = strings.TrimSpace(rest)
			if strings.HasPrefix(rest, "=") {
				inner = strings.TrimSpace(rest[1:])
				break
			}
		}
	}

	// Remove array braces: {"/path"} → "/path"
	inner = strings.TrimPrefix(inner, "{")
	inner = strings.TrimSuffix(inner, "}")

	// Extract first quoted string value
	if qStart := strings.IndexByte(inner, '"'); qStart >= 0 {
		qEnd := strings.IndexByte(inner[qStart+1:], '"')
		if qEnd >= 0 {
			return inner[qStart+1 : qStart+1+qEnd]
		}
	}

	// Remove quotes (fallback for simple cases)
	inner = strings.Trim(inner, "\"'")

	return inner
}
