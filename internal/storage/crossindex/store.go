package crossindex

import "context"

// CrossProjectIndex manages cross-project symbol and route registration.
type CrossProjectIndex interface {
	// RegisterProject registers or replaces all symbols and routes for a project+branch.
	RegisterProject(ctx context.Context, entry ProjectEntry) error

	// UnregisterProject removes all data for a project+branch.
	UnregisterProject(ctx context.Context, projectPath, branch string) error

	// LookupSymbol finds symbols by qualified name, scoped to the given dependencies.
	// Returns all matches (no ranking) so the caller can decide how to handle conflicts.
	LookupSymbol(ctx context.Context, qualifiedName string, dependencies []Dependency) []SymbolMatch

	// MatchRoute finds provider routes matching HTTP method+path, scoped to the given dependencies.
	// Only returns routes with role=provider.
	MatchRoute(ctx context.Context, method, path string, dependencies []Dependency) []RouteMatch

	// MatchRouteByService finds provider routes matching a service name and framework.
	// Returns the first route per service (deduped by service name).
	MatchRouteByService(ctx context.Context, serviceName, framework string, dependencies []Dependency) []RouteMatch

	// ListProjects returns all registered project entries.
	ListProjects(ctx context.Context) []ProjectEntry

	// Load reads the index from persistent storage into memory.
	Load() error

	// Save writes the in-memory index to persistent storage.
	Save() error

	// Close releases resources.
	Close() error
}
