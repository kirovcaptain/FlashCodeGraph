package resolver

import (
	"fmt"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/urlutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// MatchRoute checks if a remote call matches a route.
// For REST: normalized URL path segment comparison with {param} wildcards.
// For gRPC/GraphQL/Dubbo: exact match on service+method (case-insensitive).
func MatchRoute(normalizedPath, method, routePath, routeMethod, routeFramework string) bool {
	switch routeFramework {
	case "grpc":
		// Support both "ServiceName" and "ServiceName/MethodName" formats in normalizedPath
		normalizedService := normalizedPath
		normalizedMethod := method
		if idx := strings.Index(normalizedPath, "/"); idx > 0 {
			normalizedService = normalizedPath[:idx]
			normalizedMethod = normalizedPath[idx+1:]
		}
		return strings.EqualFold(normalizedService, routePath) &&
			strings.EqualFold(normalizedMethod, routeMethod)
	case "graphql":
		return strings.EqualFold(normalizedPath, routePath) &&
			strings.EqualFold(method, routeMethod)
	case "dubbo":
		return normalizedPath == routePath && method == routeMethod
	default:
		return matchRESTRoute(normalizedPath, method, routePath, routeMethod)
	}
}

func matchRESTRoute(normalizedPath, method, routePath, routeMethod string) bool {
	// HTTP method must match (empty method matches any)
	if method != "" && routeMethod != "" && !strings.EqualFold(method, routeMethod) {
		return false
	}

	// Normalize route path to same format, but preserve catch-all markers
	routeNormalized := urlutil.NormalizePathParams(routePath)

	fetchParts := splitPath(normalizedPath)
	routeParts := splitPath(routeNormalized)
	rawRouteParts := splitPath(routePath) // un-normalized for catch-all detection

	// Check for catch-all at the end of route (check raw, before normalization)
	if len(rawRouteParts) > 0 {
		last := rawRouteParts[len(rawRouteParts)-1]
		if isCatchAll(last) {
			// Catch-all: fetch must have at least as many segments as route minus catch-all
			if len(fetchParts) < len(routeParts)-1 {
				return false
			}
			// Compare segments before catch-all
			for i := 0; i < len(routeParts)-1; i++ {
				if !segmentMatches(fetchParts[i], routeParts[i]) {
					return false
				}
			}
			return true
		}
	}

	// Segment count must match
	if len(fetchParts) != len(routeParts) {
		return false
	}

	// Compare each segment
	for i := range fetchParts {
		if !segmentMatches(fetchParts[i], routeParts[i]) {
			return false
		}
	}
	return true
}

// FindMatchingRoutes finds all Route nodes that match the given normalized path and method.
// Returns matched Route node IDs.
func FindMatchingRoutes(normalizedPath, method string, routes []model.Node) []string {
	var matched []string
	for _, route := range routes {
		routePath := fmt.Sprint(route.Properties["path_pattern"])
		routeMethod := fmt.Sprint(route.Properties["method"])
		routeFramework := fmt.Sprint(route.Properties["framework"])
		if MatchRoute(normalizedPath, method, routePath, routeMethod, routeFramework) {
			matched = append(matched, route.ID)
		}
	}
	return matched
}

func splitPath(path string) []string {
	parts := strings.Split(path, "/")
	var result []string
	for _, part := range parts {
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func segmentMatches(fetchSeg, routeSeg string) bool {
	if fetchSeg == "{param}" || routeSeg == "{param}" {
		return true
	}
	return fetchSeg == routeSeg
}

// isCatchAll detects catch-all route patterns.
// Supports: **, [...param], [[...param]]
func isCatchAll(segment string) bool {
	return segment == "**" || segment == "*" ||
		strings.HasPrefix(segment, "[...") ||
		strings.HasPrefix(segment, "[[...")
}
