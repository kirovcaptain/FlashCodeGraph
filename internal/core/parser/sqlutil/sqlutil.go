// Package sqlutil provides shared SQL detection and parsing utilities.
package sqlutil

import "strings"

// DetectQueryType returns SELECT/INSERT/UPDATE/DELETE/UNKNOWN from a SQL string.
func DetectQueryType(sql string) string {
	upper := strings.ToUpper(strings.TrimSpace(sql))
	switch {
	case strings.HasPrefix(upper, "SELECT"), strings.HasPrefix(upper, "FROM"):
		return "SELECT"
	case strings.HasPrefix(upper, "INSERT"):
		return "INSERT"
	case strings.HasPrefix(upper, "UPDATE"):
		return "UPDATE"
	case strings.HasPrefix(upper, "DELETE"):
		return "DELETE"
	default:
		return "UNKNOWN"
	}
}

// ExtractTablesFromSQL extracts table names from SQL using keyword positions.
func ExtractTablesFromSQL(sql string) []string {
	upper := strings.ToUpper(sql)
	var tables []string
	// Handle UPDATE at start of statement
	if strings.HasPrefix(upper, "UPDATE ") {
		rest := strings.TrimSpace(sql[len("UPDATE "):])
		parts := strings.FieldsFunc(rest, func(r rune) bool {
			return r == ' ' || r == ',' || r == '(' || r == ')' || r == ';'
		})
		if len(parts) > 0 {
			tables = append(tables, parts[0])
		}
	}
	for _, keyword := range []string{" FROM ", " INTO ", " JOIN "} {
		idx := strings.Index(upper, keyword)
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(sql[idx+len(keyword):])
		parts := strings.FieldsFunc(rest, func(r rune) bool {
			return r == ' ' || r == ',' || r == '(' || r == ')' || r == ';'
		})
		if len(parts) > 0 {
			tables = append(tables, parts[0])
		}
	}
	return tables
}

// IsSQLStatement checks if a string looks like a SQL statement.
func IsSQLStatement(text string) bool {
	upper := strings.ToUpper(strings.TrimSpace(text))
	return strings.HasPrefix(upper, "SELECT ") ||
		strings.HasPrefix(upper, "INSERT ") ||
		strings.HasPrefix(upper, "UPDATE ") ||
		strings.HasPrefix(upper, "DELETE ")
}
