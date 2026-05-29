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
// Currently only supports Spring framework annotations. Framework field is hardcoded to "spring".
// To support other frameworks (e.g. JAX-RS @Path/@GET), add new route annotation mappings and detect framework dynamically.
func ExtractRoutes(annotations []model.StructuredAnnotation, classAnnotations []model.StructuredAnnotation, methodName, className, filePath string, startLine int, result *model.ParseResult) {
	// FeignClient routes are handled by ExtractFeignClient with correct path prefix
	if HasFeignClient(classAnnotations) {
		return
	}

	// Get class-level route prefix from class annotations
	classRoute := ""
	for _, annotation := range classAnnotations {
		if annotation.Name == "RequestMapping" {
			classRoute = annotation.Params["value"]
		}
	}

	for _, annotation := range annotations {
		httpMethod, isRoute := javaRouteAnnotations[annotation.Name]
		if !isRoute {
			continue
		}

		// For @RequestMapping, check explicit method param
		if annotation.Name == "RequestMapping" {
			if methodParam := annotation.Params["method"]; methodParam != "" {
				if mapped := mapRequestMethod(methodParam); mapped != "" {
					httpMethod = mapped
				}
			}
		}

		pathPattern := annotation.Params["value"]
		if classRoute != "" {
			pathPattern = classRoute + pathPattern
		}

		result.Routes = append(result.Routes, model.RawRoute{
			Method:      httpMethod,
			PathPattern: pathPattern,
			Handlers: []string{className + "." + methodName},
			Framework:   "spring",
			FilePath:    filePath,
			Line:        startLine,
		})
	}
}

func mapRequestMethod(methodParam string) string {
	methodPatterns := map[string]string{
		"RequestMethod.GET":    "GET",
		"RequestMethod.POST":   "POST",
		"RequestMethod.PUT":    "PUT",
		"RequestMethod.DELETE": "DELETE",
		"RequestMethod.PATCH":  "PATCH",
	}
	for pattern, method := range methodPatterns {
		if strings.Contains(methodParam, pattern) {
			return method
		}
	}
	return ""
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
