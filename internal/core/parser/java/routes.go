package java

import (
	"strings"

	"github.com/liuymcn/flash-code-graph/internal/model"
)

var javaRouteAnnotations = map[string]string{
	"GetMapping":     "GET",
	"PostMapping":    "POST",
	"PutMapping":     "PUT",
	"DeleteMapping":  "DELETE",
	"PatchMapping":   "PATCH",
	"RequestMapping": "GET", // default, actual method from annotation params
}

func ExtractRoutes(annotations []string, classAnnotations []string, methodName, className, filePath string, startLine int, result *model.ParseResult) {
	// FeignClient routes are handled by ExtractFeignClient with correct path prefix
	if HasFeignClient(classAnnotations) {
		return
	}

	// Get class-level route prefix from class annotations
	classRoute := ""
	for _, annotation := range classAnnotations {
		if strings.Contains(annotation, "RequestMapping") {
			classRoute = extractAnnotationValue(annotation)
		}
	}

	for _, annotation := range annotations {
		annotationName := strings.TrimPrefix(annotation, "@")
		baseName := annotationName
		if idx := strings.Index(baseName, "("); idx >= 0 {
			baseName = baseName[:idx]
		}

		httpMethod, isRoute := javaRouteAnnotations[baseName]
		if !isRoute {
			continue
		}

		// For @RequestMapping, check explicit method param
		if baseName == "RequestMapping" {
			if m := extractRequestMappingMethod(annotation); m != "" {
				httpMethod = m
			}
		}

		pathPattern := extractAnnotationValue(annotation)
		if classRoute != "" {
			pathPattern = classRoute + pathPattern
		}

		result.Routes = append(result.Routes, model.RawRoute{
			Method:      httpMethod,
			PathPattern: pathPattern,
			HandlerName: className + "." + methodName,
			Framework:   "spring",
			FilePath:    filePath,
			Line:        startLine,
		})
	}
}

func extractRequestMappingMethod(annotation string) string {
	methodPatterns := map[string]string{
		"RequestMethod.GET":    "GET",
		"RequestMethod.POST":   "POST",
		"RequestMethod.PUT":    "PUT",
		"RequestMethod.DELETE": "DELETE",
		"RequestMethod.PATCH":  "PATCH",
	}
	for pattern, method := range methodPatterns {
		if strings.Contains(annotation, pattern) {
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
