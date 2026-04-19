// Package urlutil provides URL normalization and path parameter unification.
package urlutil

import (
	"net/url"
	"regexp"
	"strings"
)

// Patterns for path parameters across frameworks
var paramPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\{[^}]+\}`),       // Spring: {id}
	regexp.MustCompile(`:[a-zA-Z_]\w*`),   // Express/Gin: :id
	regexp.MustCompile(`<[^>]+>`),          // Flask: <int:id>
	regexp.MustCompile(`\[[^\]]+\]`),       // Next.js: [id]
	regexp.MustCompile(`\$\{[^}]+\}`),      // Template literal: ${id}
}

// Numeric path segment (e.g., /users/123)
var numericSegment = regexp.MustCompile(`^\d+$`)

// NormalizeURL extracts the path and service name from a raw URL.
// Input:  "http://user-service/api/users/{id}?page=1"
// Output: path="/api/users/{param}", serviceName="user-service"
func NormalizeURL(rawURL string) (path string, serviceName string) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", ""
	}

	// Remove template literal backticks
	rawURL = strings.Trim(rawURL, "`")

	// Try parsing as URL to extract host and path
	if strings.Contains(rawURL, "://") {
		parsed, err := url.Parse(rawURL)
		if err == nil {
			serviceName = parsed.Hostname()
			path = parsed.Path
		}
	}

	// If no scheme, treat as path
	if path == "" {
		path = strings.SplitN(rawURL, "?", 2)[0] // strip query string
	} else {
		path = strings.SplitN(path, "?", 2)[0]
	}

	// Filter out non-path strings (contain dynamic concatenation markers)
	if strings.Contains(path, "+") || strings.Contains(path, "concat") {
		return "", serviceName
	}

	// Must start with /
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	// Normalize path parameters
	path = NormalizePathParams(path)

	// Filter localhost/IP as service name (not useful)
	if serviceName == "localhost" || serviceName == "127.0.0.1" || serviceName == "0.0.0.0" {
		serviceName = ""
	}

	return path, serviceName
}

// NormalizePathParams unifies all path parameter formats to {param}.
// {id}, :id, <int:id>, [id], ${id}, 123 → {param}
func NormalizePathParams(path string) string {
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		if seg == "" {
			continue
		}
		// Check known parameter patterns
		replaced := false
		for _, pattern := range paramPatterns {
			if pattern.MatchString(seg) {
				segments[i] = "{param}"
				replaced = true
				break
			}
		}
		// Numeric segments are likely IDs
		if !replaced && numericSegment.MatchString(seg) {
			segments[i] = "{param}"
		}
	}
	return strings.Join(segments, "/")
}
