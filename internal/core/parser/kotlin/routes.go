package kotlin

import (
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
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

// composeRouteCalledNames lists function names that define Compose Navigation routes.
var composeRouteCalledNames = map[string]bool{
	"composable":  true,
	"dialog":      true,
	"bottomSheet": true,
}

// ExtractComposeRoutes extracts Compose Navigation routes, @Destination annotations,
// and navigate() calls from already-parsed RawCalls and Symbols.
func ExtractComposeRoutes(result *model.ParseResult) {
	// 1. Check if file imports androidx.navigation
	hasNavigationImport := fileHasNavigationImport(result)

	// 2. Extract composable/dialog/bottomSheet/navigation → Route nodes
	for _, call := range result.Calls {
		if composeRouteCalledNames[call.CalledName] {
			routePath := extractRouteFromArgs(call.ArgExprs)
			if routePath == "" {
				continue
			}
			handler := findComposeHandler(call, result.Calls)
			var handlers []string
			if handler != "" {
				handlers = []string{handler}
			}
			result.Routes = append(result.Routes, model.RawRoute{
				Method:      "NAVIGATE",
				PathPattern: routePath,
				Handlers:    handlers,
				Framework:   "compose-navigation",
				FilePath:    call.FilePath,
				Line:        call.Line,
			})
		} else if call.CalledName == "navigation" {
			routePath := extractNamedArg(call.ArgExprs, "route")
			if routePath == "" {
				continue
			}
			result.Routes = append(result.Routes, model.RawRoute{
				Method:      "NAVIGATE",
				PathPattern: routePath,
				Framework:   "compose-navigation",
				FilePath:    call.FilePath,
				Line:        call.Line,
			})
		}
	}

	// 3. @Destination annotation routes
	extractDestinationAnnotationRoutes(result)

	// 4. Extract navigate() calls → RawNavigation (only if file has navigation import)
	if hasNavigationImport {
		extractNavigateCalls(result)
	}
}

// fileHasNavigationImport checks if the file imports androidx.navigation.
func fileHasNavigationImport(result *model.ParseResult) bool {
	for _, importEntry := range result.Imports {
		if strings.HasPrefix(importEntry.ModulePath, "androidx.navigation") {
			return true
		}
	}
	return false
}

// extractDestinationAnnotationRoutes finds @Destination-annotated functions and generates Route nodes.
func extractDestinationAnnotationRoutes(result *model.ParseResult) {
	for _, symbol := range result.Symbols {
		if symbol.Kind != constants.KindFunction {
			continue
		}
		for _, annotation := range symbol.Annotations {
			if annotation.Name != "Destination" {
				continue
			}
			routePath := annotation.Params["route"]
			if routePath == "" {
				routePath = annotation.Params["value"]
			}
			if routePath == "" {
				routePath = strings.ToLower(symbol.Name)
			}
			routePath = stripQuotes(routePath)
			result.Routes = append(result.Routes, model.RawRoute{
				Method:      "NAVIGATE",
				PathPattern: routePath,
				Handlers:    []string{symbol.QualifiedName},
				Framework:   "compose-destinations",
				FilePath:    symbol.FilePath,
				Line:        symbol.StartLine,
			})
		}
	}
}

// extractNavigateCalls finds navigate() calls and stores them as RawNavigation.
func extractNavigateCalls(result *model.ParseResult) {
	for _, call := range result.Calls {
		if call.CalledName != "navigate" || len(call.ArgExprs) == 0 {
			continue
		}
		targetRoute := extractNavigateTarget(call.ArgExprs[0])
		if targetRoute == "" {
			continue
		}
		result.Navigations = append(result.Navigations, model.RawNavigation{
			CallerName:  call.CallerName,
			TargetRoute: targetRoute,
			FilePath:    call.FilePath,
			Line:        call.Line,
		})
	}
}

// extractNavigateTarget extracts the route string from a navigate() argument.
// Handles: "settings" (quoted string), Detail(itemId = "123") (type-safe constructor), Settings (object)
func extractNavigateTarget(argExpression string) string {
	// Quoted string: "settings" → settings
	stripped := stripQuotes(argExpression)
	if stripped != argExpression {
		return stripped
	}
	// Type-safe constructor: Detail(itemId = "123") → Detail
	if parenthesisIndex := strings.Index(argExpression, "("); parenthesisIndex > 0 {
		return argExpression[:parenthesisIndex]
	}
	// Object reference: Settings → Settings (if it starts with uppercase)
	if len(argExpression) > 0 && argExpression[0] >= 'A' && argExpression[0] <= 'Z' {
		return argExpression
	}
	return ""
}

// findComposeHandler finds the Composable handler function inside a composable's trailing lambda.
func findComposeHandler(composableCall model.RawCall, allCalls []model.RawCall) string {
	// Step 1: Find the composable call's position in allCalls
	composableIndex := -1
	for i, call := range allCalls {
		if call.CallerName == composableCall.CallerName &&
			call.CalledName == composableCall.CalledName &&
			call.Line == composableCall.Line &&
			call.FilePath == composableCall.FilePath {
			composableIndex = i
			break
		}
	}
	if composableIndex < 0 {
		return ""
	}

	// Step 2: Find the trailing lambda (next IsPreResolved call with matching LambdaOwnerMethod)
	var lambdaQualifiedName string
	for i := composableIndex + 1; i < len(allCalls); i++ {
		call := allCalls[i]
		if call.CallerName != composableCall.CallerName {
			continue
		}
		if call.IsPreResolved && call.LambdaOwnerMethod == composableCall.CalledName {
			lambdaQualifiedName = call.CalledName
			break
		}
		// Stop if we hit another composable/dialog/navigation call (past boundary)
		if composeRouteCalledNames[call.CalledName] || call.CalledName == "navigation" {
			break
		}
	}
	if lambdaQualifiedName == "" {
		return ""
	}

	// Step 3: Find first Composable call inside the lambda (uppercase, no receiver)
	for _, call := range allCalls {
		if call.CallerName == lambdaQualifiedName &&
			call.ReceiverExpr == "" &&
			len(call.CalledName) > 0 &&
			call.CalledName[0] >= 'A' && call.CalledName[0] <= 'Z' {
			return call.CalledName
		}
	}
	return ""
}

// extractRouteFromArgs extracts the route string from the first positional argument.
func extractRouteFromArgs(argExpressions []string) string {
	if len(argExpressions) == 0 {
		return ""
	}
	return stripQuotes(argExpressions[0])
}

// extractNamedArg finds a named argument value from ArgExprs (e.g. "route = \"auth\"" → "auth").
func extractNamedArg(argExpressions []string, key string) string {
	for _, argExpression := range argExpressions {
		name, value := parseNamedArg(argExpression)
		if name == key {
			return stripQuotes(value)
		}
	}
	return ""
}

// parseNamedArg splits "key = value" into (key, value). Returns ("", "") if not a named arg.
func parseNamedArg(argExpression string) (string, string) {
	equalsIndex := strings.Index(argExpression, "=")
	if equalsIndex < 0 {
		return "", ""
	}
	name := strings.TrimSpace(argExpression[:equalsIndex])
	value := strings.TrimSpace(argExpression[equalsIndex+1:])
	return name, value
}

// stripQuotes removes surrounding double quotes from a string.
func stripQuotes(value string) string {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		return value[1 : len(value)-1]
	}
	return value
}
