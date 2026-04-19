// Package graphql parses .graphql schema files to extract Query/Mutation/Subscription operations.
package graphql

import (
	"regexp"
	"strings"
)

// Operation represents a GraphQL operation extracted from a schema file.
type Operation struct {
	Type  string // "Query", "Mutation", "Subscription"
	Field string // field name, e.g. "getUser"
}

var typeBlockRe = regexp.MustCompile(`(?m)^type\s+(Query|Mutation|Subscription)\s*\{`)
var fieldRe = regexp.MustCompile(`(?m)^\s+(\w+)\s*[\(:]`)

// ParseSchema extracts operations from a .graphql schema file content.
func ParseSchema(content string) []Operation {
	var ops []Operation
	blocks := typeBlockRe.FindAllStringIndex(content, -1)
	for _, loc := range blocks {
		// Extract type name
		match := typeBlockRe.FindStringSubmatch(content[loc[0]:loc[1]])
		if len(match) < 2 {
			continue
		}
		typeName := match[1]

		// Find matching closing brace
		body := extractBlock(content[loc[1]:])
		for _, fm := range fieldRe.FindAllStringSubmatch(body, -1) {
			if len(fm) >= 2 {
				ops = append(ops, Operation{Type: typeName, Field: fm[1]})
			}
		}
	}
	return ops
}

// extractBlock returns content between the current position and the matching '}'.
func extractBlock(s string) string {
	depth := 1
	for i, ch := range s {
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[:i]
			}
		}
	}
	return s
}

// IsGraphQLFile checks if a file path is a GraphQL schema file.
func IsGraphQLFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".graphql") || strings.HasSuffix(lower, ".gql")
}
