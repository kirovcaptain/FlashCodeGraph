package kotlin

import (
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// retrofitRouteAnnotations maps Retrofit HTTP method annotations to their method string.
var retrofitRouteAnnotations = map[string]string{
	"GET":    "GET",
	"POST":   "POST",
	"PUT":    "PUT",
	"DELETE": "DELETE",
	"PATCH":  "PATCH",
	"HEAD":   "HEAD",
}

// ExtractRetrofitRoutes extracts HTTP route definitions from Retrofit annotations.
func ExtractRetrofitRoutes(annotations []model.StructuredAnnotation, functionName, className, filePath string, startLine int, result *model.ParseResult) {
	for _, annotation := range annotations {
		httpMethod, isRouteAnnotation := retrofitRouteAnnotations[annotation.Name]
		if !isRouteAnnotation {
			continue
		}

		pathPattern := annotation.Params["value"]
		if pathPattern == "" {
			continue
		}

		if !strings.HasPrefix(pathPattern, "/") {
			pathPattern = "/" + pathPattern
		}

		handlerName := functionName
		if className != "" {
			handlerName = className + "." + functionName
		}

		result.Routes = append(result.Routes, model.RawRoute{
			Method:      httpMethod,
			PathPattern: pathPattern,
			Handlers:    []string{handlerName},
			Framework:   "retrofit",
			FilePath:    filePath,
			Line:        startLine,
		})
	}
}
