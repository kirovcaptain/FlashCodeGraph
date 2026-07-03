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
		{"type_params", "STRING[]"},
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
		{"type_params", "STRING[]"},
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
		{"qualified_name", "STRING"},
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
		{"middlewares", "STRING"},
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
	"Layout": {
		{"name", "STRING"},
		{"file_path", "STRING"},
	},
	"Widget": {
		{"name", "STRING"},
		{"widget_type", "STRING"},
		{"file_path", "STRING"},
		{"line", "INT32"},
	},
	"AppComponent": {
		{"name", "STRING"},
		{"qualified_name", "STRING"},
		{"component_type", "STRING"},
		{"is_launcher", "BOOLEAN"},
		{"deep_links", "STRING"},
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

// EdgePair defines a source-target node table pair for a relationship.
type EdgePair struct {
	FromKind string // Source node table (e.g. "Function")
	ToKind   string // Target node table (e.g. "Function")
}

// EdgeTableDef defines the schema for a relationship table.
type EdgeTableDef struct {
	EdgePairs []EdgePair  // All supported (from, to) node table combinations
	Columns   []ColumnDef // Property columns (excluding from/to)
}

// EdgeColumns defines the columns for each edge table.
// Used by: Migrate (CREATE REL TABLE), CSV COPY FROM column generation.
var EdgeColumns = map[string]EdgeTableDef{
	"CALLS": {EdgePairs: []EdgePair{{"Function", "Function"}, {"Function", "Class"}, {"Class", "Function"}, {"Class", "Class"}, {"Variable", "Function"}, {"Variable", "Class"}}, Columns: []ColumnDef{
		{"confidence", "DOUBLE"},
		{"resolved_by", "STRING"},
		{"candidates", "INT32"},
		{"line", "INT32"},
		{"declared_type", "STRING"},
		{"polymorphic", "BOOLEAN"},
		{"flow_context", "STRING"},
		{"flow_line", "INT32"},
		{"via_route", "STRING"},
		{"cross_service", "BOOLEAN"},
		{"consumer_interface", "STRING"},
		{"target_service", "STRING"},
		{"target_project", "STRING"},
		{"target_branch", "STRING"},
		{"target_handler", "STRING"},
		{"protocol", "STRING"},
		{"event_type", "STRING"},
		{"chain_id", "INT32"},
		{"chain_depth", "INT32"},
	}},
	"EXTENDS": {EdgePairs: []EdgePair{{"Class", "Class"}, {"Interface", "Interface"}}, Columns: []ColumnDef{
		{"confidence", "DOUBLE"},
		{"resolved_by", "STRING"},
		{"candidates", "INT32"},
	}},
	"IMPLEMENTS": {EdgePairs: []EdgePair{{"Class", "Interface"}}, Columns: []ColumnDef{
		{"confidence", "DOUBLE"},
		{"resolved_by", "STRING"},
		{"candidates", "INT32"},
	}},
	"IMPORTS": {EdgePairs: []EdgePair{{"File", "File"}}, Columns: []ColumnDef{
		{"symbol_name", "STRING"},
		{"alias", "STRING"},
	}},
	"OVERRIDES": {EdgePairs: []EdgePair{{"Function", "Function"}}, Columns: []ColumnDef{
		{"confidence", "DOUBLE"},
		{"resolved_by", "STRING"},
		{"candidates", "INT32"},
	}},
	"DISPATCHES": {EdgePairs: []EdgePair{{"Function", "Function"}}, Columns: []ColumnDef{
		{"confidence", "DOUBLE"},
		{"resolved_by", "STRING"},
		{"candidates", "INT32"},
		{"flow_context", "STRING"},
		{"flow_line", "INT32"},
	}},
	"CONTAINS":            {EdgePairs: []EdgePair{{"Repository", "File"}}, Columns: nil},
	"DIR_CONTAINS":        {EdgePairs: []EdgePair{{"Directory", "File"}}, Columns: nil},
	"FILE_CONTAINS":       {EdgePairs: []EdgePair{{"File", "Function"}}, Columns: nil},
	"FILE_CONTAINS_CLASS": {EdgePairs: []EdgePair{{"File", "Class"}}, Columns: nil},
	"FILE_CONTAINS_IFACE": {EdgePairs: []EdgePair{{"File", "Interface"}}, Columns: nil},
	"FILE_CONTAINS_VAR":   {EdgePairs: []EdgePair{{"File", "Variable"}}, Columns: nil},
	"CLASS_CONTAINS_FUNC": {EdgePairs: []EdgePair{{"Class", "Function"}}, Columns: nil},
	"IFACE_CONTAINS_FUNC": {EdgePairs: []EdgePair{{"Interface", "Function"}}, Columns: nil},
	"CLASS_CONTAINS_VAR":  {EdgePairs: []EdgePair{{"Class", "Variable"}}, Columns: nil},
	"MEMBER_OF_FUNC":      {EdgePairs: []EdgePair{{"Function", "Community"}}, Columns: nil},
	"MEMBER_OF_CLASS":     {EdgePairs: []EdgePair{{"Class", "Community"}}, Columns: nil},
	"HANDLES": {EdgePairs: []EdgePair{{"Function", "Route"}}, Columns: []ColumnDef{
		{"handler_order", "INT32"},
	}},
	"INJECTS": {EdgePairs: []EdgePair{{"Function", "Function"}}, Columns: []ColumnDef{
		{"inject_type", "STRING"},
	}},
	"DEPENDS_ON": {EdgePairs: []EdgePair{{"Directory", "Directory"}}, Columns: []ColumnDef{
		{"call_count", "INT32"},
	}},
	"REMOTE_CALLS_ROUTE": {EdgePairs: []EdgePair{{"Function", "Route"}}, Columns: []ColumnDef{
		{"protocol", "STRING"},
		{"target_url", "STRING"},
		{"target_service", "STRING"},
		{"confidence", "DOUBLE"},
	}},
	"REMOTE_CALLS_EXT": {EdgePairs: []EdgePair{{"Function", "ExternalService"}}, Columns: []ColumnDef{
		{"protocol", "STRING"},
		{"target_url", "STRING"},
		{"target_service", "STRING"},
		{"field_name", "STRING"},
		{"confidence", "DOUBLE"},
	}},
	"EXECUTES":              {EdgePairs: []EdgePair{{"Function", "QueryNode"}}, Columns: nil},
	"FETCHES": {EdgePairs: []EdgePair{{"Function", "Route"}}, Columns: []ColumnDef{
		{"http_method", "STRING"},
		{"url_path", "STRING"},
	}},
	"STEP": {EdgePairs: []EdgePair{{"Process", "Function"}}, Columns: []ColumnDef{
		{"seq", "INT32"},
	}},
	"HAS_ANNOTATION_FUNC":  {EdgePairs: []EdgePair{{"Function", "Annotation"}}, Columns: nil},
	"HAS_ANNOTATION_CLASS": {EdgePairs: []EdgePair{{"Class", "Annotation"}}, Columns: nil},
	"HAS_ANNOTATION_IFACE": {EdgePairs: []EdgePair{{"Interface", "Annotation"}}, Columns: nil},
	"HAS_ANNOTATION_VAR":   {EdgePairs: []EdgePair{{"Variable", "Annotation"}}, Columns: nil},
	"INCLUDES":             {EdgePairs: []EdgePair{{"Layout", "Layout"}}, Columns: nil},
	"LAYOUT_CONTAINS":      {EdgePairs: []EdgePair{{"Layout", "Widget"}}, Columns: nil},
	"NAVIGATES_TO":         {EdgePairs: []EdgePair{{"Widget", "Widget"}, {"Function", "Route"}}, Columns: nil},
	"REFERENCES":           {EdgePairs: []EdgePair{{"AppComponent", "Class"}, {"Function", "Widget"}, {"Function", "Layout"}, {"Layout", "Class"}, {"Widget", "Class"}}, Columns: []ColumnDef{
		{"ref_kind", "STRING"},
		{"line", "INT32"},
	}},
	"UNRESOLVED_CALL": {EdgePairs: []EdgePair{{"Function", "Function"}, {"Function", "Class"}}, Columns: []ColumnDef{
		{"hint_type", "STRING"},
		{"line", "INT32"},
		{"receiver_expr", "STRING"},
		{"candidate_count", "INT32"},
	}},
	"USES": {EdgePairs: []EdgePair{{"Function", "Variable"}}, Columns: []ColumnDef{
		{"line", "INT32"},
		{"ref_kind", "STRING"},
	}},
}

// EdgeColumnNames returns the property column names for an edge table.
func EdgeColumnNames(edgeTable string) []string {
	def, ok := EdgeColumns[edgeTable]
	if !ok {
		return nil
	}
	names := make([]string, len(def.Columns))
	for i, col := range def.Columns {
		names[i] = col.Name
	}
	return names
}
