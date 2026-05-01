package crossindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS projects (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_path TEXT NOT NULL,
    branch TEXT NOT NULL,
    updated_at INTEGER NOT NULL DEFAULT 0,
    UNIQUE(project_path, branch)
);

CREATE TABLE IF NOT EXISTS symbols (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    qualified_name TEXT NOT NULL,
    name TEXT NOT NULL,
    kind TEXT NOT NULL DEFAULT '',
    class_type TEXT NOT NULL DEFAULT '',
    node_id TEXT NOT NULL DEFAULT '',
    annotations TEXT NOT NULL DEFAULT '[]',
    file_path TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS methods (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    symbol_id INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    node_id TEXT NOT NULL DEFAULT '',
    params TEXT NOT NULL DEFAULT '[]',
    return_type TEXT NOT NULL DEFAULT '',
    annotations TEXT NOT NULL DEFAULT '[]',
    route_method TEXT NOT NULL DEFAULT '',
    route_path TEXT NOT NULL DEFAULT '',
    is_getter INTEGER NOT NULL DEFAULT 0,
    is_setter INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS routes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    method TEXT NOT NULL,
    path TEXT NOT NULL,
    handler_name TEXT NOT NULL DEFAULT '',
    handler_id TEXT NOT NULL DEFAULT '',
    framework TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT 'provider'
);

CREATE INDEX IF NOT EXISTS idx_symbols_project_id ON symbols(project_id);
CREATE INDEX IF NOT EXISTS idx_symbols_qualified_name ON symbols(qualified_name);
CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols(name);
CREATE INDEX IF NOT EXISTS idx_methods_symbol_id ON methods(symbol_id);
CREATE INDEX IF NOT EXISTS idx_routes_project_id ON routes(project_id);
CREATE INDEX IF NOT EXISTS idx_routes_method_path ON routes(method, path);
CREATE INDEX IF NOT EXISTS idx_routes_framework_path ON routes(framework, path);
`

// SQLiteStore implements CrossProjectIndex using SQLite.
type SQLiteStore struct {
	dbPath string
	db     *sql.DB
}

// NewSQLiteStore creates a SQLiteStore at the given database path.
// Call Load() to open the connection and initialize schema.
func NewSQLiteStore(dbPath string) *SQLiteStore {
	return &SQLiteStore{dbPath: dbPath}
}

// Load opens the SQLite database and initializes the schema.
func (s *SQLiteStore) Load() error {
	db, err := sql.Open("sqlite3", s.dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON")
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return fmt.Errorf("init schema: %w", err)
	}
	s.db = db
	return nil
}

// Save is a no-op for SQLiteStore (data is written on each mutation).
func (s *SQLiteStore) Save() error { return nil }

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// RegisterProject registers or replaces all symbols and routes for a project+branch.
func (s *SQLiteStore) RegisterProject(_ context.Context, entry ProjectEntry) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Delete existing project (CASCADE deletes symbols, methods, routes)
	tx.Exec("DELETE FROM projects WHERE project_path = ? AND branch = ?", entry.ProjectPath, entry.Branch)

	// Insert project
	res, err := tx.Exec("INSERT INTO projects (project_path, branch, updated_at) VALUES (?, ?, ?)",
		entry.ProjectPath, entry.Branch, entry.UpdatedAt)
	if err != nil {
		return err
	}
	projectID, _ := res.LastInsertId()

	// Insert symbols and methods
	for _, sym := range entry.Symbols {
		annotations, _ := json.Marshal(sym.Annotations)
		symRes, err := tx.Exec(
			"INSERT INTO symbols (project_id, qualified_name, name, kind, class_type, node_id, annotations, file_path) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			projectID, sym.QualifiedName, sym.Name, sym.Kind, sym.ClassType, sym.NodeID, string(annotations), sym.FilePath)
		if err != nil {
			return err
		}
		symbolID, _ := symRes.LastInsertId()

		for _, m := range sym.Methods {
			params, _ := json.Marshal(m.Params)
			mAnnotations, _ := json.Marshal(m.Annotations)
			isGetter, isSetter := 0, 0
			if m.IsGetter {
				isGetter = 1
			}
			if m.IsSetter {
				isSetter = 1
			}
			if _, err := tx.Exec(
				"INSERT INTO methods (symbol_id, name, node_id, params, return_type, annotations, route_method, route_path, is_getter, is_setter) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
				symbolID, m.Name, m.NodeID, string(params), m.ReturnType, string(mAnnotations), m.RouteMethod, m.RoutePath, isGetter, isSetter); err != nil {
				return err
			}
		}
	}

	// Insert routes
	for _, r := range entry.Routes {
		if _, err := tx.Exec(
			"INSERT INTO routes (project_id, method, path, handler_name, handler_id, framework, role) VALUES (?, ?, ?, ?, ?, ?, ?)",
			projectID, r.Method, r.Path, r.HandlerName, r.HandlerID, r.Framework, r.Role); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// UnregisterProject removes all data for a project+branch.
func (s *SQLiteStore) UnregisterProject(_ context.Context, projectPath, branch string) error {
	_, err := s.db.Exec("DELETE FROM projects WHERE project_path = ? AND branch = ?", projectPath, branch)
	return err
}

// LookupSymbol finds symbols by qualified name, scoped to the given dependencies.
func (s *SQLiteStore) LookupSymbol(_ context.Context, qualifiedName string, dependencies []Dependency) []SymbolMatch {
	projectIDs := s.resolveProjectIDs(dependencies)
	if len(projectIDs) == 0 {
		return nil
	}

	placeholders, args := inClause(projectIDs)
	suffixPattern := "%." + qualifiedName

	query := fmt.Sprintf(
		`SELECT s.qualified_name, s.name, s.kind, s.class_type, s.node_id, s.annotations, s.file_path, s.id, p.project_path, p.branch
		 FROM symbols s JOIN projects p ON s.project_id = p.id
		 WHERE s.project_id IN (%s) AND (s.qualified_name = ? OR s.name = ? OR s.qualified_name LIKE ?)`, placeholders)
	args = append(args, qualifiedName, qualifiedName, suffixPattern)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var matches []SymbolMatch
	for rows.Next() {
		var sym GlobalSymbol
		var symbolID int64
		var annotationsJSON string
		var projectPath, branch string
		if err := rows.Scan(&sym.QualifiedName, &sym.Name, &sym.Kind, &sym.ClassType, &sym.NodeID, &annotationsJSON, &sym.FilePath, &symbolID, &projectPath, &branch); err != nil {
			continue
		}
		json.Unmarshal([]byte(annotationsJSON), &sym.Annotations)
		sym.Methods = s.loadMethods(symbolID)
		matches = append(matches, SymbolMatch{Symbol: sym, ProjectPath: projectPath, Branch: branch})
	}
	return matches
}

// MatchRoute finds provider routes matching HTTP method+path, scoped to the given dependencies.
func (s *SQLiteStore) MatchRoute(_ context.Context, method, path string, dependencies []Dependency) []RouteMatch {
	projectIDs := s.resolveProjectIDs(dependencies)
	if len(projectIDs) == 0 {
		return nil
	}

	placeholders, args := inClause(projectIDs)
	normalizedPath := normalizeRoutePath(path)
	upperMethod := strings.ToUpper(method)

	query := fmt.Sprintf(
		`SELECT r.method, r.path, r.handler_name, r.handler_id, r.framework, r.role, p.project_path, p.branch
		 FROM routes r JOIN projects p ON r.project_id = p.id
		 WHERE r.project_id IN (%s) AND r.role = 'provider' AND UPPER(r.method) = ? AND LOWER(r.path) = ?`, placeholders)
	args = append(args, upperMethod, normalizedPath)

	return s.queryRoutes(query, args)
}

// MatchRouteByService finds provider routes matching a service name and framework.
func (s *SQLiteStore) MatchRouteByService(_ context.Context, serviceName, framework string, dependencies []Dependency) []RouteMatch {
	projectIDs := s.resolveProjectIDs(dependencies)
	if len(projectIDs) == 0 {
		return nil
	}

	placeholders, args := inClause(projectIDs)
	lowerService := strings.ToLower(serviceName)

	query := fmt.Sprintf(
		`SELECT r.method, r.path, r.handler_name, r.handler_id, r.framework, r.role, p.project_path, p.branch
		 FROM routes r JOIN projects p ON r.project_id = p.id
		 WHERE r.project_id IN (%s) AND r.role = 'provider' AND r.framework = ? AND LOWER(r.path) = ?`, placeholders)
	args = append(args, framework, lowerService)

	// Dedup: one match per project
	allMatches := s.queryRoutes(query, args)
	seen := map[string]bool{}
	var deduped []RouteMatch
	for _, m := range allMatches {
		key := projectKey(m.ProjectPath, m.Branch)
		if !seen[key] {
			seen[key] = true
			deduped = append(deduped, m)
		}
	}
	return deduped
}

// ListProjects returns all registered project entries.
func (s *SQLiteStore) ListProjects(_ context.Context) []ProjectEntry {
	rows, err := s.db.Query("SELECT id, project_path, branch, updated_at FROM projects")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var entries []ProjectEntry
	for rows.Next() {
		var id int64
		var entry ProjectEntry
		if err := rows.Scan(&id, &entry.ProjectPath, &entry.Branch, &entry.UpdatedAt); err != nil {
			continue
		}
		entry.Symbols = s.loadSymbols(id)
		entry.Routes = s.loadRoutes(id)
		entries = append(entries, entry)
	}
	return entries
}

// GetDependencySymbols returns all symbols from projects matching the given dependencies.
func (s *SQLiteStore) GetDependencySymbols(_ context.Context, dependencies []Dependency) []GlobalSymbol {
	projectIDs := s.resolveProjectIDs(dependencies)
	if len(projectIDs) == 0 {
		return nil
	}

	placeholders, args := inClause(projectIDs)
	query := fmt.Sprintf(
		"SELECT id, qualified_name, name, kind, class_type, node_id, annotations, file_path FROM symbols WHERE project_id IN (%s)", placeholders)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var symbols []GlobalSymbol
	for rows.Next() {
		var sym GlobalSymbol
		var symbolID int64
		var annotationsJSON string
		if err := rows.Scan(&symbolID, &sym.QualifiedName, &sym.Name, &sym.Kind, &sym.ClassType, &sym.NodeID, &annotationsJSON, &sym.FilePath); err != nil {
			continue
		}
		json.Unmarshal([]byte(annotationsJSON), &sym.Annotations)
		sym.Methods = s.loadMethods(symbolID)
		symbols = append(symbols, sym)
	}
	return symbols
}

// --- internal helpers ---

func (s *SQLiteStore) resolveProjectIDs(dependencies []Dependency) []int64 {
	if len(dependencies) == 0 {
		return nil
	}
	// Build query: WHERE (project_path = ? AND branch = ?) OR ...
	var conditions []string
	var args []any
	for _, d := range dependencies {
		conditions = append(conditions, "(project_path = ? AND branch = ?)")
		args = append(args, d.Path, d.Branch)
	}
	query := "SELECT id FROM projects WHERE " + strings.Join(conditions, " OR ")
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *SQLiteStore) loadSymbols(projectID int64) []GlobalSymbol {
	rows, err := s.db.Query(
		"SELECT id, qualified_name, name, kind, class_type, node_id, annotations, file_path FROM symbols WHERE project_id = ?", projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var symbols []GlobalSymbol
	for rows.Next() {
		var sym GlobalSymbol
		var symbolID int64
		var annotationsJSON string
		if err := rows.Scan(&symbolID, &sym.QualifiedName, &sym.Name, &sym.Kind, &sym.ClassType, &sym.NodeID, &annotationsJSON, &sym.FilePath); err != nil {
			continue
		}
		json.Unmarshal([]byte(annotationsJSON), &sym.Annotations)
		sym.Methods = s.loadMethods(symbolID)
		symbols = append(symbols, sym)
	}
	return symbols
}

func (s *SQLiteStore) loadMethods(symbolID int64) []GlobalMethod {
	rows, err := s.db.Query(
		"SELECT name, node_id, params, return_type, annotations, route_method, route_path, is_getter, is_setter FROM methods WHERE symbol_id = ?", symbolID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var methods []GlobalMethod
	for rows.Next() {
		var m GlobalMethod
		var paramsJSON, annotationsJSON string
		var isGetter, isSetter int
		if err := rows.Scan(&m.Name, &m.NodeID, &paramsJSON, &m.ReturnType, &annotationsJSON, &m.RouteMethod, &m.RoutePath, &isGetter, &isSetter); err != nil {
			continue
		}
		json.Unmarshal([]byte(paramsJSON), &m.Params)
		json.Unmarshal([]byte(annotationsJSON), &m.Annotations)
		m.IsGetter = isGetter == 1
		m.IsSetter = isSetter == 1
		methods = append(methods, m)
	}
	return methods
}

func (s *SQLiteStore) loadRoutes(projectID int64) []GlobalRoute {
	rows, err := s.db.Query(
		"SELECT method, path, handler_name, handler_id, framework, role FROM routes WHERE project_id = ?", projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var routes []GlobalRoute
	for rows.Next() {
		var r GlobalRoute
		if err := rows.Scan(&r.Method, &r.Path, &r.HandlerName, &r.HandlerID, &r.Framework, &r.Role); err != nil {
			continue
		}
		routes = append(routes, r)
	}
	return routes
}

func (s *SQLiteStore) queryRoutes(query string, args []any) []RouteMatch {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var matches []RouteMatch
	for rows.Next() {
		var r GlobalRoute
		var projectPath, branch string
		if err := rows.Scan(&r.Method, &r.Path, &r.HandlerName, &r.HandlerID, &r.Framework, &r.Role, &projectPath, &branch); err != nil {
			continue
		}
		matches = append(matches, RouteMatch{Route: r, ProjectPath: projectPath, Branch: branch})
	}
	return matches
}

// inClause builds a SQL IN clause placeholder string and args from int64 IDs.
func inClause(ids []int64) (string, []any) {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	return strings.Join(placeholders, ","), args
}

// DefaultSQLitePath returns the default SQLite database path.
func DefaultSQLitePath(globalDir string) string {
	return filepath.Join(globalDir, "cross_project_index.db")
}
