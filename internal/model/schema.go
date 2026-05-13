// Package model defines the shared data model for FlashCodeGraph.
// schema.go defines node column schemas used by both storage backends.
// Add new fields here — Migrate auto-adds columns, queries auto-include them.
package model

import "strings"

// ColumnDef defines a single column in a node table.
type ColumnDef struct {
	Name string
	Type string // STRING, INT32, INT64, BOOLEAN, DOUBLE
}

// NodeColumns defines the columns for each node kind (excluding "id" which is always the primary key).
// Used by: KùzuDB Migrate (CREATE TABLE + ALTER TABLE), QueryNodesByName, QueryAllByKind.
// FalkorDB uses column names for query generation (schema-free, no CREATE TABLE needed).
var NodeColumns = map[string][]ColumnDef{
	"Function": {
		{"name", "STRING"},
		{"qualified_name", "STRING"},
		{"file_path", "STRING"},
		{"start_line", "INT32"},
		{"end_line", "INT32"},
		{"params", "STRING"},
		{"return_types", "STRING[]"},
		{"visibility", "STRING"},
		{"is_exported", "BOOLEAN"},
		{"is_abstract", "BOOLEAN"},
		{"is_async", "BOOLEAN"},
		{"is_static", "BOOLEAN"},
		{"is_synthetic", "BOOLEAN"},
		{"is_constructor", "BOOLEAN"},
		{"is_generator", "BOOLEAN"},
		{"is_lambda", "BOOLEAN"},
		{"is_getter", "BOOLEAN"},
		{"is_setter", "BOOLEAN"},
		{"lambda_context", "STRING"},
		{"complexity", "INT32"},
		{"class_type", "STRING"},
		{"docstring", "STRING"},
		{"annotations", "STRING"},
		{"entry_point_score", "DOUBLE"},
		{"entry_type", "STRING"},
		{"source_project", "STRING"},
		{"source_branch", "STRING"},
	},
	"Class": {
		{"name", "STRING"},
		{"qualified_name", "STRING"},
		{"file_path", "STRING"},
		{"start_line", "INT32"},
		{"end_line", "INT32"},
		{"class_type", "STRING"},
		{"is_abstract", "BOOLEAN"},
		{"is_final", "BOOLEAN"},
		{"is_exported", "BOOLEAN"},
		{"complexity", "INT32"},
		{"params", "STRING"},
		{"docstring", "STRING"},
		{"annotations", "STRING"},
		{"fields", "STRING"},
		{"source_project", "STRING"},
		{"source_branch", "STRING"},
	},
	"Interface": {
		{"name", "STRING"},
		{"qualified_name", "STRING"},
		{"file_path", "STRING"},
		{"start_line", "INT32"},
		{"end_line", "INT32"},
		{"is_exported", "BOOLEAN"},
		{"class_type", "STRING"},
		{"annotations", "STRING"},
		{"fields", "STRING"},
		{"source_project", "STRING"},
		{"source_branch", "STRING"},
	},
	"Variable": {
		{"name", "STRING"},
		{"file_path", "STRING"},
		{"line", "INT32"},
		{"var_type", "STRING"},
		{"visibility", "STRING"},
		{"is_final", "BOOLEAN"},
		{"is_static", "BOOLEAN"},
		{"is_exported", "BOOLEAN"},
	},
	"File": {
		{"path", "STRING"},
		{"language", "STRING"},
		{"size", "INT64"},
		{"mod_time", "INT64"},
		{"content_hash", "STRING"},
	},
	"Directory": {
		{"path", "STRING"},
	},
	"Repository": {
		{"name", "STRING"},
		{"path", "STRING"},
		{"branch", "STRING"},
		{"project_type", "STRING"},
		{"indexed_at", "INT64"},
	},
	"Route": {
		{"method", "STRING"},
		{"path_pattern", "STRING"},
		{"handler_method", "STRING"},
		{"response_shape", "STRING"},
		{"framework", "STRING"},
		{"file_path", "STRING"},
	},
	"QueryNode": {
		{"sql_text", "STRING"},
		{"query_type", "STRING"},
		{"tables", "STRING"},
		{"caller", "STRING"},
		{"file_path", "STRING"},
		{"base_sql", "STRING"},
		{"conditions", "STRING"},
	},
	"Community": {
		{"name", "STRING"},
		{"description", "STRING"},
		{"member_count", "INT32"},
		{"cohesion_score", "DOUBLE"},
	},
	"Process": {
		{"name", "STRING"},
		{"entry_point", "STRING"},
		{"step_count", "INT32"},
		{"entry_type", "STRING"},
		{"file_path", "STRING"},
		{"route_method", "STRING"},
		{"route_path", "STRING"},
	},
	"Annotation": {
		{"name", "STRING"},
		{"category", "STRING"},
		{"layer", "STRING"},
		{"framework", "STRING"},
		{"params", "STRING"},
		{"file_path", "STRING"},
		{"line", "INT32"},
	},
	"ExternalService": {
		{"name", "STRING"},
		{"discovered_by", "STRING"},
		{"file_path", "STRING"},
	},
}

// ColumnNames returns the column names for a node kind (for query generation).
func ColumnNames(kind string) []string {
	cols, ok := NodeColumns[kind]
	if !ok {
		return []string{"name", "file_path"}
	}
	names := make([]string, len(cols))
	for i, col := range cols {
		names[i] = col.Name
	}
	return names
}

// GetColumnType returns the type of a column for the given node kind.
// Returns empty string if the column is not found.
func GetColumnType(kind, name string) string {
	for _, col := range NodeColumns[kind] {
		if col.Name == name {
			return col.Type
		}
	}
	return ""
}

// QueryReturnClause generates "n.id, n.name, n.qualified_name, ..." for a node kind.
func QueryReturnClause(kind string) string {
	cols := NodeColumns[kind]
	parts := make([]string, 0, len(cols)+1)
	parts = append(parts, "n.id")
	for _, col := range cols {
		parts = append(parts, "n."+col.Name)
	}
	return strings.Join(parts, ", ")
}
