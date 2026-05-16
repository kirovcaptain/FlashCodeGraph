// Package kuzu implements GraphStore using KùzuDB.
package kuzu

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
	gokuzu "github.com/kuzudb/go-kuzu"
)

// Store implements storage.GraphStore backed by KùzuDB.
type Store struct {
	db   *gokuzu.Database
	conn *gokuzu.Connection
}

// New creates a KùzuDB store. Pass "" for dbPath to use in-memory mode.
func New(dbPath string, bufferPoolSize uint64) (*Store, error) {
	cfg := gokuzu.DefaultSystemConfig()
	if bufferPoolSize > 0 {
		cfg.BufferPoolSize = bufferPoolSize
	}

	db, err := gokuzu.OpenDatabase(dbPath, cfg)
	if err != nil {
		return nil, fmt.Errorf("kuzu: open database: %w", err)
	}
	conn, err := gokuzu.OpenConnection(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("kuzu: open connection: %w", err)
	}
	return &Store{db: db, conn: conn}, nil
}

// exec runs a parameterized query.
func (store *Store) exec(query string, params map[string]any) (*gokuzu.QueryResult, error) {
	stmt, err := store.conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	return store.conn.Execute(stmt, params)
}

// execNoParams runs a query without parameters.
func (store *Store) execNoParams(query string) error {
	result, err := store.conn.Query(query)
	if err != nil {
		return err
	}
	result.Close()
	return nil
}

// Migrate creates all node and relationship tables.
func (store *Store) Migrate(_ context.Context) error {
	// Generate CREATE TABLE statements from schema
	var stmts []string
	for kind, cols := range model.NodeColumns {
		colDefs := "id STRING"
		for _, col := range cols {
			colDefs += ", " + col.Name + " " + col.Type
		}
		stmts = append(stmts, fmt.Sprintf("CREATE NODE TABLE IF NOT EXISTS %s (%s, PRIMARY KEY (id))", kind, colDefs))
	}

	// Relationship tables (not schema-driven — relationships have custom columns)
	relStmts := []string{
		`CREATE REL TABLE IF NOT EXISTS CONTAINS (FROM Repository TO File, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS DIR_CONTAINS (FROM Directory TO File, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS FILE_CONTAINS (FROM File TO Function, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS FILE_CONTAINS_CLASS (FROM File TO Class, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS FILE_CONTAINS_IFACE (FROM File TO Interface, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS FILE_CONTAINS_VAR (FROM File TO Variable, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS CLASS_CONTAINS_FUNC (FROM Class TO Function, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS IFACE_CONTAINS_FUNC (FROM Interface TO Function, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS CLASS_CONTAINS_VAR (FROM Class TO Variable, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS CALLS (FROM Function TO Function, confidence DOUBLE, resolved_by STRING, candidates INT32, line INT32, declared_type STRING, polymorphic BOOLEAN, flow_context STRING, flow_line INT32, via_route STRING, cross_service BOOLEAN, consumer_interface STRING, target_service STRING, target_project STRING, target_branch STRING, target_handler STRING, protocol STRING, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS EXTENDS (FROM Class TO Class, confidence DOUBLE, resolved_by STRING, candidates INT32, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS IMPLEMENTS (FROM Class TO Interface, confidence DOUBLE, resolved_by STRING, candidates INT32, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS IMPORTS (FROM File TO File, symbol_name STRING, alias STRING, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS OVERRIDES (FROM Function TO Function, confidence DOUBLE, resolved_by STRING, candidates INT32, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS DISPATCHES (FROM Function TO Function, confidence DOUBLE, resolved_by STRING, candidates INT32, flow_context STRING, flow_line INT32, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS MEMBER_OF_FUNC (FROM Function TO Community, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS MEMBER_OF_CLASS (FROM Class TO Community, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS HANDLES (FROM Function TO Route, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS INJECTS (FROM Function TO Function, inject_type STRING, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS DEPENDS_ON (FROM Directory TO Directory, call_count INT32, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS REMOTE_CALLS_ROUTE (FROM Function TO Route, protocol STRING, target_url STRING, target_service STRING, confidence DOUBLE, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS REMOTE_CALLS_EXT (FROM Function TO ExternalService, protocol STRING, target_url STRING, target_service STRING, field_name STRING, confidence DOUBLE, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS EXECUTES (FROM Function TO QueryNode, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS FETCHES (FROM Function TO Route, http_method STRING, url_path STRING, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS MIDDLEWARE (FROM Route TO Function, seq INT32, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS STEP (FROM Process TO Function, seq INT32, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS HAS_ANNOTATION_FUNC (FROM Function TO Annotation, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS HAS_ANNOTATION_CLASS (FROM Class TO Annotation, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS HAS_ANNOTATION_IFACE (FROM Interface TO Annotation, MANY_MANY)`,
		`CREATE REL TABLE IF NOT EXISTS UNRESOLVED_CALL (FROM Function TO Function, hint_type STRING, line INT32, receiver_expr STRING, candidate_count INT32, MANY_MANY)`,
	}
	stmts = append(stmts, relStmts...)

	for _, stmt := range stmts {
		if err := store.execNoParams(stmt); err != nil {
			return fmt.Errorf("kuzu: migrate: %w\nstatement: %s", err, stmt)
		}
	}

	// Idempotent ALTER TABLE ADD COLUMN for schema upgrades
	// If column already exists, KùzuDB returns error — safely ignored.
	for kind, cols := range model.NodeColumns {
		for _, col := range cols {
			alter := fmt.Sprintf("ALTER TABLE %s ADD %s %s", kind, col.Name, col.Type)
			store.execNoParams(alter) // ignore errors (column already exists)
		}
	}

	// Upgrade CALLS rel table with cross-service columns added after initial schema.
	for _, column := range []struct{ name, colType string }{
		{"via_route", "STRING"},
		{"cross_service", "BOOLEAN"},
		{"consumer_interface", "STRING"},
		{"target_service", "STRING"},
		{"target_project", "STRING"},
		{"target_branch", "STRING"},
		{"target_handler", "STRING"},
		{"protocol", "STRING"},
	} {
		store.execNoParams(fmt.Sprintf("ALTER TABLE CALLS ADD %s %s", column.name, column.colType))
	}

	return nil
}

// WriteNodes writes nodes in batch using merged SET statements.
func (store *Store) WriteNodes(_ context.Context, nodes []model.Node) error {
	for _, node := range nodes {
		if err := store.mergeNode(node.Kind, node.ID, node.Properties); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) CreateNodes(_ context.Context, nodes []model.Node) error {
	for _, node := range nodes {
		if err := store.createNode(node.Kind, node.ID, node.Properties); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) mergeNode(label, id string, props map[string]any) error {
	// MERGE by id
	mergeQuery := fmt.Sprintf("MERGE (n:%s {id: $id})", label)
	result, err := store.exec(mergeQuery, map[string]any{"id": id})
	if err != nil {
		return fmt.Errorf("kuzu: merge %s: %w", label, err)
	}
	result.Close()

	// Build a single SET statement with all properties
	// JSON round-trip normalizes Go types for kuzu driver parameter binding
	// (e.g. int→float64, []string→[]any). Do NOT remove without verifying driver type support.
	propsJSON, _ := json.Marshal(props)
	var propsMap map[string]any
	json.Unmarshal(propsJSON, &propsMap)

	setParts := []string{}
	params := map[string]any{"id": id}
	paramIndex := 0
	for key, value := range propsMap {
		if key == "id" {
			continue
		}
		paramName := fmt.Sprintf("v%d", paramIndex)
		setParts = append(setParts, fmt.Sprintf("n.%s = $%s", key, paramName))
		params[paramName] = value
		paramIndex++
	}

	if len(setParts) == 0 {
		return nil
	}

	setQuery := fmt.Sprintf("MATCH (n:%s) WHERE n.id = $id SET %s", label, strings.Join(setParts, ", "))
	result, err = store.exec(setQuery, params)
	if err != nil {
		// Fallback: set properties one by one for schema mismatches
		for key, value := range propsMap {
			if key == "id" {
				continue
			}
			fallbackQuery := fmt.Sprintf("MATCH (n:%s) WHERE n.id = $id SET n.%s = $val", label, key)
			r, e := store.exec(fallbackQuery, map[string]any{"id": id, "val": value})
			if e != nil {
				continue // skip properties not in schema
			}
			r.Close()
		}
		return nil
	}
	result.Close()
	return nil
}

func (store *Store) createNode(label, id string, props map[string]any) error {
	// Filter properties to only schema-defined columns
	validCols := make(map[string]bool)
	for _, col := range model.NodeColumns[label] {
		validCols[col.Name] = true
	}

	// JSON round-trip normalizes Go types for kuzu driver parameter binding
	// (e.g. int→float64, []string→[]any). Do NOT remove without verifying driver type support.
	propsJSON, _ := json.Marshal(props)
	var propsMap map[string]any
	json.Unmarshal(propsJSON, &propsMap)

	params := map[string]any{"id": id}
	propDefs := []string{"id: $id"}
	paramIndex := 0
	for key, value := range propsMap {
		if key == "id" || !validCols[key] {
			continue
		}
		paramName := fmt.Sprintf("v%d", paramIndex)
		propDefs = append(propDefs, fmt.Sprintf("%s: $%s", key, paramName))
		params[paramName] = value
		paramIndex++
	}

	query := fmt.Sprintf("CREATE (n:%s {%s})", label, strings.Join(propDefs, ", "))
	result, err := store.exec(query, params)
	if err != nil {
		return fmt.Errorf("kuzu: create %s: %w", label, err)
	}
	result.Close()
	return nil
}

// WriteEdges writes edges in batch. Uses MERGE to prevent duplicates.
func (store *Store) WriteEdges(_ context.Context, edges []model.Edge) error {
	for _, edge := range edges {
		if err := store.writeEdge(edge); err != nil {
			return err
		}
	}
	return nil
}

// CreateEdges writes edges in batch using CREATE (no duplicate check, faster).
func (store *Store) CreateEdges(_ context.Context, edges []model.Edge) error {
	for _, edge := range edges {
		if err := store.createEdge(edge); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) createEdge(edge model.Edge) error {
	relTable, sourceLabel, targetLabel := mapRelation(edge.Kind, edge.SourceKind)
	if relTable == "" {
		return fmt.Errorf("kuzu: unknown relation kind: %s", edge.Kind)
	}

	params := map[string]any{"source_id": edge.SourceID, "target_id": edge.TargetID}
	propDefs := []string{}
	paramIndex := 0
	for key, value := range edge.Properties {
		paramName := fmt.Sprintf("p%d", paramIndex)
		propDefs = append(propDefs, fmt.Sprintf("%s: $%s", key, paramName))
		params[paramName] = value
		paramIndex++
	}

	propClause := ""
	if len(propDefs) > 0 {
		propClause = " {" + strings.Join(propDefs, ", ") + "}"
	}

	query := fmt.Sprintf(
		"MATCH (a:%s), (b:%s) WHERE a.id = $source_id AND b.id = $target_id CREATE (a)-[r:%s%s]->(b)",
		sourceLabel, targetLabel, relTable, propClause)
	result, err := store.exec(query, params)
	if err != nil {
		return fmt.Errorf("kuzu: create edge %s: %w", edge.Kind, err)
	}
	result.Close()
	return nil
}

func (store *Store) writeEdge(edge model.Edge) error {
	relTable, sourceLabel, targetLabel := mapRelation(edge.Kind, edge.SourceKind)
	if relTable == "" {
		return fmt.Errorf("kuzu: unknown relation kind: %s", edge.Kind)
	}

	params := map[string]any{"source_id": edge.SourceID, "target_id": edge.TargetID}

	// MERGE the edge first
	mergeQuery := fmt.Sprintf(
		"MATCH (a:%s), (b:%s) WHERE a.id = $source_id AND b.id = $target_id MERGE (a)-[r:%s]->(b)",
		sourceLabel, targetLabel, relTable)
	result, err := store.exec(mergeQuery, params)
	if err != nil {
		return fmt.Errorf("kuzu: write edge %s: %w", edge.Kind, err)
	}
	result.Close()

	// SET properties if any
	if len(edge.Properties) > 0 {
		setParts := []string{}
		setParams := map[string]any{"source_id": edge.SourceID, "target_id": edge.TargetID}
		paramIndex := 0
		for key, value := range edge.Properties {
			paramName := fmt.Sprintf("p%d", paramIndex)
			setParts = append(setParts, fmt.Sprintf("r.%s = $%s", key, paramName))
			setParams[paramName] = value
			paramIndex++
		}
		setQuery := fmt.Sprintf(
			"MATCH (a:%s)-[r:%s]->(b:%s) WHERE a.id = $source_id AND b.id = $target_id SET %s",
			sourceLabel, relTable, targetLabel, strings.Join(setParts, ", "))
		result, err := store.exec(setQuery, setParams)
		if err != nil {
			// Non-fatal: edge created but properties may not match schema
			return nil
		}
		result.Close()
	}
	return nil
}

// allRelTypes lists all relationship types with their source and target labels.
var allRelTypes = []struct {
	relTable    string
	sourceLabel string
	targetLabel string
}{
	{"CALLS", constants.KindFunction, constants.KindFunction},
	{"OVERRIDES", constants.KindFunction, constants.KindFunction},
	{"INJECTS", constants.KindFunction, constants.KindFunction},
	{"HANDLES", constants.KindFunction, constants.KindRoute},
	{"EXECUTES", constants.KindFunction, constants.KindQueryNode},
	{"EXTENDS", constants.KindClass, constants.KindClass},
	{"IMPLEMENTS", constants.KindClass, constants.KindInterface},
	{"IMPORTS", constants.KindFile, constants.KindFile},
	{"CONTAINS", constants.KindRepository, constants.KindFile},
	{"FILE_CONTAINS", constants.KindFile, constants.KindFunction},
	{"FILE_CONTAINS_CLASS", constants.KindFile, constants.KindClass},
	{"FILE_CONTAINS_IFACE", constants.KindFile, constants.KindInterface},
	{"CLASS_CONTAINS_FUNC", constants.KindClass, constants.KindFunction},
	{"IFACE_CONTAINS_FUNC", constants.KindInterface, constants.KindFunction},
	{"FILE_CONTAINS_VAR", constants.KindFile, constants.KindVariable},
	{"CLASS_CONTAINS_FUNC", constants.KindClass, constants.KindFunction},
	{"IFACE_CONTAINS_FUNC", constants.KindInterface, constants.KindFunction},
	{"CLASS_CONTAINS_VAR", constants.KindClass, constants.KindVariable},
	{"MEMBER_OF_FUNC", constants.KindFunction, constants.KindCommunity},
	{"MEMBER_OF_CLASS", constants.KindClass, constants.KindCommunity},
	{"DEPENDS_ON", constants.KindDirectory, constants.KindDirectory},
	{"REMOTE_CALLS_ROUTE", constants.KindFunction, constants.KindRoute},
	{"REMOTE_CALLS_EXT", constants.KindFunction, constants.KindExternalService},
	{"FETCHES", constants.KindFunction, constants.KindRoute},
	{"MIDDLEWARE", constants.KindRoute, constants.KindFunction},
	{"STEP", constants.KindProcess, constants.KindFunction},
	{"HAS_ANNOTATION_FUNC", constants.KindFunction, constants.KindAnnotation},
	{"HAS_ANNOTATION_CLASS", constants.KindClass, constants.KindAnnotation},
	{"HAS_ANNOTATION_IFACE", constants.KindInterface, constants.KindAnnotation},
	{"DISPATCHES", constants.KindFunction, constants.KindFunction},
	{"UNRESOLVED_CALL", constants.KindFunction, constants.KindFunction},
}

// DeleteNodesByFile removes all nodes associated with a file path.
func (store *Store) DeleteNodesByFile(_ context.Context, filePath string) error {
	tables := []string{constants.KindFunction, constants.KindClass, constants.KindInterface, constants.KindVariable, constants.KindRoute, constants.KindQueryNode, constants.KindAnnotation, constants.KindExternalService}
	for _, table := range tables {
		query := fmt.Sprintf("MATCH (n:%s) WHERE n.file_path = $filePath DETACH DELETE n", table)
		result, err := store.exec(query, map[string]any{"filePath": filePath})
		if err != nil {
			continue
		}
		result.Close()
	}
	return nil
}

func (store *Store) DeleteNodeByID(_ context.Context, id string) error {
	for kind := range model.NodeColumns {
		query := fmt.Sprintf("MATCH (n:%s) WHERE n.id = $id DETACH DELETE n", kind)
		result, err := store.exec(query, map[string]any{"id": id})
		if err != nil {
			continue
		}
		result.Close()
	}
	return nil
}

// DeleteEdgesBySource removes all outgoing edges from a source node.
func (store *Store) DeleteEdgesBySource(_ context.Context, sourceID string) error {
	for _, rel := range allRelTypes {
		query := fmt.Sprintf("MATCH (a:%s)-[r:%s]->(b:%s) WHERE a.id = $id DELETE r",
			rel.sourceLabel, rel.relTable, rel.targetLabel)
		result, err := store.exec(query, map[string]any{"id": sourceID})
		if err != nil {
			continue
		}
		result.Close()
	}
	return nil
}

// DeleteEdgesByTarget removes all incoming edges to a target node.
func (store *Store) DeleteEdgesByTarget(_ context.Context, targetID string) error {
	for _, rel := range allRelTypes {
		query := fmt.Sprintf("MATCH (a:%s)-[r:%s]->(b:%s) WHERE b.id = $id DELETE r",
			rel.sourceLabel, rel.relTable, rel.targetLabel)
		result, err := store.exec(query, map[string]any{"id": targetID})
		if err != nil {
			continue
		}
		result.Close()
	}
	return nil
}

func (store *Store) DeleteAllByKind(_ context.Context, kind string) error {
	query := fmt.Sprintf("MATCH (n:%s) DETACH DELETE n", kind)
	result, err := store.conn.Query(query)
	if err != nil {
		return nil // table may not exist
	}
	result.Close()
	return nil
}

func (store *Store) ClearAll(_ context.Context) error {
	for kind := range model.NodeColumns {
		query := fmt.Sprintf("MATCH (n:%s) DETACH DELETE n", kind)
		result, err := store.conn.Query(query)
		if err != nil {
			continue
		}
		result.Close()
	}
	return nil
}

// QueryNodeByID returns a single node by ID.
func (store *Store) QueryNodeByID(_ context.Context, id string) (*model.Node, error) {
	for _, table := range constants.AllNodeKinds {
		returnClause := model.QueryReturnClause(table)
		colNames := append([]string{"id"}, model.ColumnNames(table)...)

		query := fmt.Sprintf("MATCH (n:%s) WHERE n.id = $id RETURN %s LIMIT 1", table, returnClause)
		result, err := store.exec(query, map[string]any{"id": id})
		if err != nil {
			continue
		}
		if !result.HasNext() {
			result.Close()
			continue
		}
		row, _ := result.Next()
		props := make(map[string]any)
		nodeID := ""
		for i, col := range colNames {
			val, _ := row.GetValue(uint64(i))
			if col == "id" {
				nodeID = fmt.Sprint(val)
			} else if val != nil {
				props[col] = val
			}
		}
		result.Close()
		return &model.Node{ID: nodeID, Kind: table, Properties: props}, nil
	}
	return nil, nil
}

// QueryNodesByName returns nodes matching a name.

// QueryNodeByQualifiedName returns a single node by its qualified name.
func (store *Store) QueryNodeByQualifiedName(_ context.Context, qualifiedName string) (*model.Node, error) {
	for _, table := range constants.BaseSymbolKinds {
		returnClause := model.QueryReturnClause(table)
		colNames := append([]string{"id"}, model.ColumnNames(table)...)

		query := fmt.Sprintf("MATCH (n:%s) WHERE n.qualified_name = $qn RETURN %s LIMIT 1", table, returnClause)
		result, err := store.exec(query, map[string]any{"qn": qualifiedName})
		if err != nil {
			continue
		}
		if !result.HasNext() {
			result.Close()
			continue
		}
		row, _ := result.Next()
		props := make(map[string]any)
		nodeID := ""
		for i, col := range colNames {
			val, _ := row.GetValue(uint64(i))
			if col == "id" {
				nodeID = fmt.Sprint(val)
			} else if val != nil {
				props[col] = val
			}
		}
		result.Close()
		return &model.Node{ID: nodeID, Kind: table, Properties: props}, nil
	}
	return nil, nil
}
func (store *Store) QueryNodesByName(_ context.Context, name string, opts model.QueryOpts) ([]model.Node, error) {
	tables := constants.BaseSymbolKinds
	if len(opts.Kinds) > 0 {
		tables = opts.Kinds
	}
	limit := 100
	if opts.Limit > 0 {
		limit = opts.Limit
	}

	var nodes []model.Node
	for _, table := range tables {
		returnClause := model.QueryReturnClause(table)
		colNames := append([]string{"id"}, model.ColumnNames(table)...)

		query := fmt.Sprintf("MATCH (n:%s) WHERE n.name = $name RETURN %s LIMIT %d", table, returnClause, limit)
		result, err := store.exec(query, map[string]any{"name": name})
		if err != nil {
			continue
		}
		for result.HasNext() {
			row, _ := result.Next()
			props := make(map[string]any)
			nodeID := ""
			for i, col := range colNames {
				val, _ := row.GetValue(uint64(i))
				if col == "id" {
					nodeID = fmt.Sprint(val)
				} else if val != nil {
					props[col] = val
				}
			}
			nodes = append(nodes, model.Node{ID: nodeID, Kind: table, Properties: props})
		}
		result.Close()
	}
	return nodes, nil
}

// QueryEdges returns edges connected to a node.
func (store *Store) QueryEdges(_ context.Context, nodeID string, nodeKind string, kind model.RelationKind, direction model.Direction) ([]model.Edge, error) {
	relTable, sourceLabel, targetLabel := mapRelation(kind, nodeKind)
	if relTable == "" {
		return nil, fmt.Errorf("kuzu: unknown relation kind: %s", kind)
	}

	var query string
	switch direction {
	case model.Outgoing:
		query = fmt.Sprintf("MATCH (a:%s)-[r:%s]->(b:%s) WHERE a.id = $id RETURN a.id, b.id", sourceLabel, relTable, targetLabel)
	case model.Incoming:
		query = fmt.Sprintf("MATCH (a:%s)-[r:%s]->(b:%s) WHERE b.id = $id RETURN a.id, b.id", sourceLabel, relTable, targetLabel)
	default:
		query = fmt.Sprintf("MATCH (a:%s)-[r:%s]-(b:%s) WHERE a.id = $id RETURN a.id, b.id", sourceLabel, relTable, targetLabel)
	}

	result, err := store.exec(query, map[string]any{"id": nodeID})
	if err != nil {
		return nil, fmt.Errorf("kuzu: query edges: %w", err)
	}
	defer result.Close()

	var edges []model.Edge
	for result.HasNext() {
		row, _ := result.Next()
		sourceID, _ := row.GetValue(0)
		targetID, _ := row.GetValue(1)
		edges = append(edges, model.Edge{
			SourceID: fmt.Sprint(sourceID),
			TargetID: fmt.Sprint(targetID),
			Kind:     kind,
		})
	}
	return edges, nil
}

// TraverseCallChain traverses CALLS relationships up to depth.
// minConfidence filtering is applied post-query since KùzuDB doesn't support ALL() on recursive rels.
// QueryAllEdges returns all edges of a given relation kind in a single query.
// For single-table relations (CALLS, EXTENDS, etc.), queries the table directly.
// For multi-table relations (HAS_ANNOTATION, CONTAINS, etc.), queries all physical
// tables and merges results.
func (store *Store) QueryAllEdges(_ context.Context, relKind model.RelationKind, limit int) ([]model.Edge, error) {
	// Multi-table relation mappings
	type relDef struct{ rel, src, tgt string }
	multiTable := map[model.RelationKind][]relDef{
		model.RelHasAnnotation: {
			{"HAS_ANNOTATION_FUNC", constants.KindFunction, constants.KindAnnotation},
			{"HAS_ANNOTATION_CLASS", constants.KindClass, constants.KindAnnotation},
			{"HAS_ANNOTATION_IFACE", constants.KindInterface, constants.KindAnnotation},
		},
		model.RelContains: {
			{"CONTAINS", constants.KindRepository, constants.KindFile},
			{"DIR_CONTAINS", constants.KindDirectory, constants.KindFile},
			{"FILE_CONTAINS", constants.KindFile, constants.KindFunction},
			{"FILE_CONTAINS_CLASS", constants.KindFile, constants.KindClass},
			{"FILE_CONTAINS_IFACE", constants.KindFile, constants.KindInterface},
			{"CLASS_CONTAINS_FUNC", constants.KindClass, constants.KindFunction},
			{"IFACE_CONTAINS_FUNC", constants.KindInterface, constants.KindFunction},
		},
		model.RelRemoteCallsRoute: {{"REMOTE_CALLS_ROUTE", constants.KindFunction, constants.KindRoute}},
		model.RelRemoteCallsExt:   {{"REMOTE_CALLS_EXT", constants.KindFunction, constants.KindExternalService}},
		model.RelStep:             {{"STEP", constants.KindProcess, constants.KindFunction}},
	}

	tables, isMulti := multiTable[relKind]
	if !isMulti {
		// Single-table: query directly
		tables = []relDef{{string(relKind), "", ""}}
	}

	limitClause := ""
	if limit > 0 {
		limitClause = fmt.Sprintf(" LIMIT %d", limit)
	}

	var edges []model.Edge
	for _, t := range tables {
		query := fmt.Sprintf("MATCH (a)-[r:%s]->(b) RETURN a.id, b.id%s", t.rel, limitClause)
		result, err := store.conn.Query(query)
		if err != nil {
			continue
		}
		for result.HasNext() {
			row, _ := result.Next()
			srcID, _ := row.GetValue(0)
			tgtID, _ := row.GetValue(1)
			edges = append(edges, model.Edge{
				SourceID: fmt.Sprint(srcID),
				TargetID: fmt.Sprint(tgtID),
				Kind:     relKind,
			})
		}
		result.Close()
	}
	return edges, nil
}

// QueryNodesByIDs returns nodes matching any of the given IDs.
func (store *Store) QueryNodesByIDs(_ context.Context, ids []string) ([]model.Node, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var nodes []model.Node
	for _, kind := range []string{constants.KindFunction, constants.KindClass, constants.KindInterface, constants.KindRoute, constants.KindQueryNode, constants.KindAnnotation} {
		returnClause := model.QueryReturnClause(kind)
		query := fmt.Sprintf("MATCH (n:%s) WHERE n.id IN $ids RETURN %s", kind, returnClause)
		result, err := store.exec(query, map[string]any{"ids": ids})
		if err != nil {
			continue
		}
		colNames := append([]string{"id"}, model.ColumnNames(kind)...)
		for result.HasNext() {
			row, _ := result.Next()
			props := make(map[string]any)
			for i, col := range colNames {
				val, _ := row.GetValue(uint64(i))
				if val != nil {
					props[col] = val
				}
			}
			id, _ := props["id"].(string)
			if id == "" {
				continue
			}
			delete(props, "id")
			nodes = append(nodes, model.Node{ID: id, Kind: kind, Properties: props})
		}
		result.Close()
	}
	return nodes, nil
}

// QueryEdgesByNodeIDs returns edges where source (Outgoing) or target (Incoming) is in nodeIDs.
func (store *Store) QueryEdgesByNodeIDs(_ context.Context, nodeIDs []string, relKind model.RelationKind, dir model.Direction) ([]model.Edge, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	relTable, sourceLabel, targetLabel := mapRelation(relKind, "")
	if relTable == "" {
		return nil, fmt.Errorf("kuzu: unknown relation kind: %s", relKind)
	}
	var query string
	switch dir {
	case model.Outgoing:
		query = fmt.Sprintf("MATCH (a:%s)-[r:%s]->(b:%s) WHERE a.id IN $nodeIDs RETURN a.id, b.id, r.confidence, r.line", sourceLabel, relTable, targetLabel)
	case model.Incoming:
		query = fmt.Sprintf("MATCH (a:%s)-[r:%s]->(b:%s) WHERE b.id IN $nodeIDs RETURN a.id, b.id, r.confidence, r.line", sourceLabel, relTable, targetLabel)
	default:
		query = fmt.Sprintf("MATCH (a:%s)-[r:%s]-(b:%s) WHERE a.id IN $nodeIDs RETURN a.id, b.id, r.confidence, r.line", sourceLabel, relTable, targetLabel)
	}
	result, err := store.exec(query, map[string]any{"nodeIDs": nodeIDs})
	if err != nil {
		return nil, err
	}
	defer result.Close()
	var edges []model.Edge
	for result.HasNext() {
		row, _ := result.Next()
		sourceID, _ := row.GetValue(0)
		targetID, _ := row.GetValue(1)
		confidence, _ := row.GetValue(2)
		line, _ := row.GetValue(3)
		props := map[string]any{}
		if confidence != nil {
			props["confidence"] = confidence
		}
		if line != nil {
			props["line"] = line
		}
		edges = append(edges, model.Edge{SourceID: fmt.Sprint(sourceID), TargetID: fmt.Sprint(targetID), Kind: relKind, Properties: props})
	}
	return edges, nil
}

// TraverseCallChain traverses CALLS relationships up to depth.
// When minConfidence == 0, uses KùzuDB recursive query for best performance.
// When minConfidence > 0, uses application-level BFS with per-hop confidence filtering
// to ensure every edge on the path meets the threshold.
func (store *Store) TraverseCallChain(_ context.Context, nodeID string, depth int, direction model.Direction, minConfidence float64) (*model.Subgraph, error) {
	if minConfidence > 0 {
		return store.traverseBFS(nodeID, depth, direction, minConfidence)
	}
	return store.traverseRecursive(nodeID, depth, direction)
}

// traverseRecursive uses KùzuDB recursive path query (fast, no confidence filtering).
func (store *Store) traverseRecursive(nodeID string, depth int, direction model.Direction) (*model.Subgraph, error) {
	// Use BFS to collect both nodes and edges (recursive Cypher can't return per-hop edges easily)
	return store.traverseBFS(nodeID, depth, direction, 0)
}

// traverseBFS uses application-level BFS with per-hop confidence filtering.
// Each level queries CALLS edges first, then DISPATCHES edges from the new
// CALLS targets, filtering DISPATCHES by declared_type prefix matching.
func (store *Store) traverseBFS(nodeID string, depth int, direction model.Direction, minConfidence float64) (*model.Subgraph, error) {
	subgraph := &model.Subgraph{}
	visited := map[string]bool{nodeID: true}
	queue := []string{nodeID}

	confidenceFilter := "AND (r.confidence IS NULL OR r.confidence >= $minConf)"
	var callsQueryTemplate, dispatchQueryTemplate string
	switch direction {
	case model.Outgoing:
		callsQueryTemplate = fmt.Sprintf("MATCH (a:Function)-[r:CALLS]->(b:Function) WHERE a.id = $id %s RETURN a.id, b.id, b.name, b.file_path, r.confidence, r.line, r.declared_type, b.is_getter, b.is_setter, b.qualified_name, b.is_constructor, b.source_project, b.source_branch, b.start_line, b.end_line", confidenceFilter)
		dispatchQueryTemplate = fmt.Sprintf("MATCH (a:Function)-[r:DISPATCHES]->(b:Function) WHERE a.id = $id %s RETURN a.id, b.id, b.name, b.file_path, r.confidence, b.is_getter, b.is_setter, b.qualified_name, b.is_constructor, b.source_project, b.source_branch, b.start_line, b.end_line", confidenceFilter)
	case model.Incoming:
		callsQueryTemplate = fmt.Sprintf("MATCH (a:Function)-[r:CALLS]->(b:Function) WHERE b.id = $id %s RETURN b.id, a.id, a.name, a.file_path, r.confidence, r.line, r.declared_type, a.is_getter, a.is_setter, a.qualified_name, a.is_constructor, a.source_project, a.source_branch, a.start_line, a.end_line", confidenceFilter)
		dispatchQueryTemplate = fmt.Sprintf("MATCH (a:Function)-[r:DISPATCHES]->(b:Function) WHERE b.id = $id %s RETURN b.id, a.id, a.name, a.file_path, r.confidence, a.is_getter, a.is_setter, a.qualified_name, a.is_constructor, a.source_project, a.source_branch, a.start_line, a.end_line", confidenceFilter)
	default:
		callsQueryTemplate = fmt.Sprintf("MATCH (a:Function)-[r:CALLS]-(b:Function) WHERE a.id = $id %s RETURN a.id, b.id, b.name, b.file_path, r.confidence, r.line, r.declared_type, b.is_getter, b.is_setter, b.qualified_name, b.is_constructor, b.source_project, b.source_branch, b.start_line, b.end_line", confidenceFilter)
		dispatchQueryTemplate = fmt.Sprintf("MATCH (a:Function)-[r:DISPATCHES]-(b:Function) WHERE a.id = $id %s RETURN a.id, b.id, b.name, b.file_path, r.confidence, b.is_getter, b.is_setter, b.qualified_name, b.is_constructor, b.source_project, b.source_branch, b.start_line, b.end_line", confidenceFilter)
	}

	for level := 0; level < depth && len(queue) > 0; level++ {
		var nextQueue []string
		declaredTypePrefixes := map[string]map[string]bool{}

		// Step 1: Query CALLS edges for all queue nodes
		for _, currentID := range queue {
			result, err := store.exec(callsQueryTemplate, map[string]any{"id": currentID, "minConf": minConfidence})
			if err != nil {
				continue
			}
			for result.HasNext() {
				row, _ := result.Next()
				sourceID, _ := row.GetValue(0)
				targetID, _ := row.GetValue(1)
				name, _ := row.GetValue(2)
				filePath, _ := row.GetValue(3)
				confidence, _ := row.GetValue(4)
				line, _ := row.GetValue(5)
				declaredType, _ := row.GetValue(6)
				isGetter, _ := row.GetValue(7)
				isSetter, _ := row.GetValue(8)
				qualifiedName, _ := row.GetValue(9)
				isConstructor, _ := row.GetValue(10)
				sourceProject, _ := row.GetValue(11)
				sourceBranch, _ := row.GetValue(12)
				startLine, _ := row.GetValue(13)
				endLine, _ := row.GetValue(14)

				neighborID := fmt.Sprint(targetID)
				edgeProperties := map[string]any{"confidence": confidence}
				if line != nil {
					edgeProperties["line"] = line
				}
				if declaredTypeStr, ok := declaredType.(string); ok && declaredTypeStr != "" {
					edgeProperties["declared_type"] = declaredTypeStr
					if declaredTypePrefixes[neighborID] == nil {
						declaredTypePrefixes[neighborID] = map[string]bool{}
					}
					declaredTypePrefixes[neighborID][declaredTypeStr+"."] = true
				}
				subgraph.Edges = append(subgraph.Edges, model.Edge{
					SourceID:   fmt.Sprint(sourceID),
					TargetID:   neighborID,
					Kind:       model.RelCalls,
					Properties: edgeProperties,
				})

				if visited[neighborID] {
					continue
				}
				visited[neighborID] = true
				nextQueue = append(nextQueue, neighborID)
				nodeProperties := map[string]any{"name": name, "file_path": filePath, "is_getter": isGetter, "is_setter": isSetter}
				if qualifiedNameStr, ok := qualifiedName.(string); ok && qualifiedNameStr != "" {
					nodeProperties["qualified_name"] = qualifiedNameStr
				}
				if isConstructorBool, ok := isConstructor.(bool); ok {
					nodeProperties["is_constructor"] = isConstructorBool
				}
				if sourceProjectStr, ok := sourceProject.(string); ok && sourceProjectStr != "" {
					nodeProperties["source_project"] = sourceProjectStr
				}
				if sourceBranchStr, ok := sourceBranch.(string); ok && sourceBranchStr != "" {
					nodeProperties["source_branch"] = sourceBranchStr
				}
				if startLine != nil {
					nodeProperties["start_line"] = startLine
				}
				if endLine != nil {
					nodeProperties["end_line"] = endLine
				}
				subgraph.Nodes = append(subgraph.Nodes, model.Node{
					ID:         neighborID,
					Kind:       constants.KindFunction,
					Properties: nodeProperties,
				})
			}
			result.Close()
		}

		// Step 2: Query DISPATCHES edges from new CALLS targets, filter by declared_type
		sourceQualifiedNameByID := map[string]string{}
		for _, node := range subgraph.Nodes {
			if qualifiedName, ok := node.Properties["qualified_name"].(string); ok {
				sourceQualifiedNameByID[node.ID] = qualifiedName
			}
		}
		callsTargets := make([]string, len(nextQueue))
		copy(callsTargets, nextQueue)
		for _, dispatchSourceID := range callsTargets {
			result, err := store.exec(dispatchQueryTemplate, map[string]any{"id": dispatchSourceID, "minConf": minConfidence})
			if err != nil {
				continue
			}
			prefixes := declaredTypePrefixes[dispatchSourceID]
			skipFiltering := false
			if len(prefixes) > 0 {
				sourceQualifiedName := sourceQualifiedNameByID[dispatchSourceID]
				for prefix := range prefixes {
					if strings.HasPrefix(sourceQualifiedName, prefix) {
						skipFiltering = true
						break
					}
				}
			}
			for result.HasNext() {
				row, _ := result.Next()
				sourceID, _ := row.GetValue(0)
				targetID, _ := row.GetValue(1)
				name, _ := row.GetValue(2)
				filePath, _ := row.GetValue(3)
				confidence, _ := row.GetValue(4)
				isGetter, _ := row.GetValue(5)
				isSetter, _ := row.GetValue(6)
				qualifiedName, _ := row.GetValue(7)
				isConstructor, _ := row.GetValue(8)
				sourceProject, _ := row.GetValue(9)
				sourceBranch, _ := row.GetValue(10)
				startLine, _ := row.GetValue(11)
				endLine, _ := row.GetValue(12)

				if len(prefixes) > 0 && !skipFiltering {
					targetQualifiedName, _ := qualifiedName.(string)
					matched := false
					for prefix := range prefixes {
						if strings.HasPrefix(targetQualifiedName, prefix) {
							matched = true
							break
						}
					}
					if !matched {
						continue
					}
				}

				neighborID := fmt.Sprint(targetID)
				edgeProperties := map[string]any{"confidence": confidence}
				subgraph.Edges = append(subgraph.Edges, model.Edge{
					SourceID:   fmt.Sprint(sourceID),
					TargetID:   neighborID,
					Kind:       model.RelDispatches,
					Properties: edgeProperties,
				})

				if visited[neighborID] {
					continue
				}
				visited[neighborID] = true
				nextQueue = append(nextQueue, neighborID)
				nodeProperties := map[string]any{"name": name, "file_path": filePath, "is_getter": isGetter, "is_setter": isSetter}
				if qualifiedNameStr, ok := qualifiedName.(string); ok && qualifiedNameStr != "" {
					nodeProperties["qualified_name"] = qualifiedNameStr
				}
				if isConstructorBool, ok := isConstructor.(bool); ok {
					nodeProperties["is_constructor"] = isConstructorBool
				}
				if sourceProjectStr, ok := sourceProject.(string); ok && sourceProjectStr != "" {
					nodeProperties["source_project"] = sourceProjectStr
				}
				if sourceBranchStr, ok := sourceBranch.(string); ok && sourceBranchStr != "" {
					nodeProperties["source_branch"] = sourceBranchStr
				}
				if startLine != nil {
					nodeProperties["start_line"] = startLine
				}
				if endLine != nil {
					nodeProperties["end_line"] = endLine
				}
				subgraph.Nodes = append(subgraph.Nodes, model.Node{
					ID:         neighborID,
					Kind:       constants.KindFunction,
					Properties: nodeProperties,
				})
			}
			result.Close()
		}

		queue = nextQueue
	}

	// Detect truncated nodes: query both CALLS and DISPATCHES for boundary nodes
	if len(queue) > 0 {
		qualifiedNameByID := map[string]string{}
		for _, node := range subgraph.Nodes {
			if qualifiedName, _ := node.Properties["qualified_name"].(string); qualifiedName != "" {
				qualifiedNameByID[node.ID] = qualifiedName
			}
		}
		for _, boundaryID := range queue {
			remainingCount := 0
			for _, template := range []string{callsQueryTemplate, dispatchQueryTemplate} {
				result, err := store.exec(template, map[string]any{"id": boundaryID, "minConf": minConfidence})
				if err != nil {
					continue
				}
				for result.HasNext() {
					row, _ := result.Next()
					neighborID, _ := row.GetValue(1)
					if !visited[fmt.Sprint(neighborID)] {
						remainingCount++
					}
				}
				result.Close()
			}
			if remainingCount > 0 {
				if qualifiedName := qualifiedNameByID[boundaryID]; qualifiedName != "" {
					subgraph.TruncatedNodes = append(subgraph.TruncatedNodes,
						fmt.Sprintf("%s (%d direct callees not expanded)", qualifiedName, remainingCount))
				}
			}
		}
	}

	return subgraph, nil
}

// TraverseImpact finds all nodes affected by changes to a node.
func (store *Store) TraverseImpact(ctx context.Context, nodeID string, depth int) (*model.Subgraph, error) {
	return store.TraverseCallChain(ctx, nodeID, depth, model.Incoming, 0)
}

// BatchUpdateNodeProperties updates properties on multiple nodes.
func (store *Store) BatchUpdateNodeProperties(_ context.Context, kind string, updates []storage.PropertyUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	for _, u := range updates {
		params := map[string]any{"nodeID": u.NodeID}
		var setClauses []string
		i := 0
		for k, v := range u.Props {
			paramName := fmt.Sprintf("v%d", i)
			setClauses = append(setClauses, fmt.Sprintf("n.%s = $%s", k, paramName))
			params[paramName] = v
			i++
		}
		cypher := fmt.Sprintf("MATCH (n:%s) WHERE n.id = $nodeID SET %s", kind, strings.Join(setClauses, ", "))
		result, err := store.exec(cypher, params)
		if err != nil {
			return err
		}
		result.Close()
	}
	return nil
}

// QueryNodesByFile returns Function, Class, and Interface nodes in a file using UNION ALL.
func (store *Store) QueryNodesByFile(_ context.Context, filePath string) ([]model.Node, error) {
	query := `MATCH (n:Function) WHERE n.file_path = $filePath RETURN n.id, n.qualified_name, n.start_line, n.end_line, 'Function' AS kind
UNION ALL
MATCH (n:Class) WHERE n.file_path = $filePath RETURN n.id, n.qualified_name, n.start_line, n.end_line, 'Class' AS kind
UNION ALL
MATCH (n:Interface) WHERE n.file_path = $filePath RETURN n.id, n.qualified_name, n.start_line, n.end_line, 'Interface' AS kind`
	result, err := store.exec(query, map[string]any{"filePath": filePath})
	if err != nil {
		return nil, fmt.Errorf("kuzu: query nodes by file: %w", err)
	}
	defer result.Close()

	var nodes []model.Node
	for result.HasNext() {
		row, _ := result.Next()
		id, _ := row.GetValue(0)
		qualifiedName, _ := row.GetValue(1)
		startLine, _ := row.GetValue(2)
		endLine, _ := row.GetValue(3)
		nodeKind, _ := row.GetValue(4)
		nodes = append(nodes, model.Node{
			ID:   fmt.Sprint(id),
			Kind: fmt.Sprint(nodeKind),
			Properties: map[string]any{
				"qualified_name": qualifiedName,
				"start_line":     startLine,
				"end_line":       endLine,
			},
		})
	}
	return nodes, nil
}

// QueryAllByKind returns all nodes of a specific kind.
func (store *Store) QueryAllByKind(_ context.Context, kind string, limit int) ([]model.Node, error) {
	returnClause := model.QueryReturnClause(kind)
	colNames := append([]string{"id"}, model.ColumnNames(kind)...)

	limitClause := ""
	if limit > 0 {
		limitClause = fmt.Sprintf(" LIMIT %d", limit)
	}
	query := fmt.Sprintf("MATCH (n:%s) RETURN %s%s", kind, returnClause, limitClause)
	result, err := store.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer result.Close()

	var nodes []model.Node
	for result.HasNext() {
		row, _ := result.Next()
		props := make(map[string]any)
		nodeID := ""
		for i, col := range colNames {
			val, _ := row.GetValue(uint64(i))
			if col == "id" {
				nodeID = fmt.Sprint(val)
			} else if val != nil {
				props[col] = val
			}
		}
		nodes = append(nodes, model.Node{ID: nodeID, Kind: kind, Properties: props})
	}
	return nodes, nil
}

// QueryNodesByProperty returns nodes of a specific kind where the given property matches the value.
// matchMode: "exact" for equality, "contains" for substring match.
func (store *Store) QueryNodesByProperty(_ context.Context, kind string, key string, value string, matchMode string, limit int) ([]model.Node, error) {
	returnClause := model.QueryReturnClause(kind)
	colNames := append([]string{"id"}, model.ColumnNames(kind)...)

	var whereClause string
	params := map[string]any{"propertyValue": value}
	switch matchMode {
	case storage.MatchContains:
		whereClause = fmt.Sprintf("WHERE n.%s CONTAINS $propertyValue", key)
	case storage.MatchNotEmpty:
		whereClause = fmt.Sprintf("WHERE n.%s IS NOT NULL AND n.%s <> ''", key, key)
		params = map[string]any{}
	default: // exact
		whereClause = fmt.Sprintf("WHERE n.%s = $propertyValue", key)
	}
	limitClause := ""
	if limit > 0 {
		limitClause = fmt.Sprintf(" LIMIT %d", limit)
	}
	query := fmt.Sprintf("MATCH (n:%s) %s RETURN %s%s", kind, whereClause, returnClause, limitClause)
	result, err := store.exec(query, params)
	if err != nil {
		return nil, err
	}
	defer result.Close()

	var nodes []model.Node
	for result.HasNext() {
		row, _ := result.Next()
		props := make(map[string]any)
		nodeID := ""
		for i, col := range colNames {
			val, _ := row.GetValue(uint64(i))
			if col == "id" {
				nodeID = fmt.Sprint(val)
			} else if val != nil {
				props[col] = val
			}
		}
		nodes = append(nodes, model.Node{ID: nodeID, Kind: kind, Properties: props})
	}
	return nodes, nil
}

// SearchFTS performs full-text search on node names.
func (store *Store) SearchFTS(_ context.Context, query string, limit int) ([]storage.SearchResult, error) {
	var results []storage.SearchResult
	for _, table := range constants.BaseSymbolKinds {
		cypherQuery := fmt.Sprintf("MATCH (n:%s) WHERE n.name CONTAINS $query RETURN n.id, n.name, n.file_path, n.qualified_name, n.start_line, n.end_line LIMIT %d", table, limit)
		result, err := store.exec(cypherQuery, map[string]any{"query": query})
		if err != nil {
			continue
		}
		for result.HasNext() {
			row, _ := result.Next()
			nodeID, _ := row.GetValue(0)
			nodeName, _ := row.GetValue(1)
			filePath, _ := row.GetValue(2)
			qualifiedName, _ := row.GetValue(3)
			startLine, _ := row.GetValue(4)
			endLine, _ := row.GetValue(5)
			searchResult := storage.SearchResult{
				NodeID:        fmt.Sprint(nodeID),
				Name:          fmt.Sprint(nodeName),
				Kind:          table,
				Path:          fmt.Sprint(filePath),
				QualifiedName: fmt.Sprint(qualifiedName),
			}
			if startLineInt, ok := startLine.(int64); ok {
				searchResult.StartLine = int(startLineInt)
			}
			if endLineInt, ok := endLine.(int64); ok {
				searchResult.EndLine = int(endLineInt)
			}
			results = append(results, searchResult)
		}
		result.Close()
	}
	return results, nil
}

// GetStats returns aggregate statistics.
func (store *Store) GetStats(_ context.Context) (*model.GraphStats, error) {
	stats := &model.GraphStats{
		NodesByKind: make(map[string]int),
		EdgesByKind: make(map[string]int),
		FilesByLang: make(map[string]int),
	}
	for _, table := range constants.AllNodeKinds {
		result, err := store.conn.Query(fmt.Sprintf("MATCH (n:%s) RETURN count(n)", table))
		if err != nil {
			continue
		}
		if result.HasNext() {
			row, _ := result.Next()
			count, _ := row.GetValue(0)
			if value, ok := count.(int64); ok && value > 0 {
				stats.NodesByKind[table] = int(value)
				stats.NodeCount += int(value)
			}
		}
		result.Close()
	}
	stats.FileCount = stats.NodesByKind[constants.KindFile]

	// Edge counts
	edgeTypes := []string{"CALLS", "EXTENDS", "IMPLEMENTS", "OVERRIDES", "IMPORTS",
		"HANDLES", "EXECUTES", "CONTAINS"}
	for _, rel := range edgeTypes {
		result, err := store.conn.Query(fmt.Sprintf("MATCH ()-[r:%s]->() RETURN count(r)", rel))
		if err != nil {
			continue
		}
		if result.HasNext() {
			row, _ := result.Next()
			count, _ := row.GetValue(0)
			if value, ok := count.(int64); ok && value > 0 {
				stats.EdgesByKind[rel] = int(value)
				stats.EdgeCount += int(value)
			}
		}
		result.Close()
	}

	// File language counts
	langResult, err := store.conn.Query("MATCH (f:File) RETURN f.language, count(f)")
	if err == nil {
		for langResult.HasNext() {
			row, _ := langResult.Next()
			lang, _ := row.GetValue(0)
			count, _ := row.GetValue(1)
			if l, ok := lang.(string); ok && l != "" {
				if v, ok := count.(int64); ok {
					stats.FilesByLang[l] = int(v)
				}
			}
		}
		langResult.Close()
	}

	// Frameworks from Repository node
	fwResult, err := store.conn.Query("MATCH (n:Repository) RETURN n.frameworks LIMIT 1")
	if err == nil {
		if fwResult.HasNext() {
			row, _ := fwResult.Next()
			val, _ := row.GetValue(0)
			if fwStr, ok := val.(string); ok && fwStr != "" {
				stats.Frameworks = strings.Split(fwStr, ",")
			}
		}
		fwResult.Close()
	}

	return stats, nil
}

// Close releases all resources.
func (store *Store) Close() error {
	store.conn.Close()
	store.db.Close()
	return nil
}

// mapRelation maps a RelationKind to KùzuDB table name and endpoint labels.
func mapRelation(kind model.RelationKind, sourceKind string) (relTable, sourceLabel, targetLabel string) {
	switch kind {
	case model.RelCalls:
		return "CALLS", constants.KindFunction, constants.KindFunction
	case model.RelExtends:
		return "EXTENDS", constants.KindClass, constants.KindClass
	case model.RelImplements:
		return "IMPLEMENTS", constants.KindClass, constants.KindInterface
	case model.RelImports:
		return "IMPORTS", constants.KindFile, constants.KindFile
	case model.RelOverrides:
		return "OVERRIDES", constants.KindFunction, constants.KindFunction
	case model.RelDispatches:
		return "DISPATCHES", constants.KindFunction, constants.KindFunction
	case model.RelHandles:
		return "HANDLES", constants.KindFunction, constants.KindRoute
	case model.RelExecutes:
		return "EXECUTES", constants.KindFunction, constants.KindQueryNode
	case model.RelContains:
		switch sourceKind {
		case constants.KindDirectory:
			return "DIR_CONTAINS", constants.KindDirectory, constants.KindFile
		case constants.SourceKindClassFunc, constants.KindClass:
			return "CLASS_CONTAINS_FUNC", constants.KindClass, constants.KindFunction
		case constants.SourceKindInterfaceFunc, constants.KindInterface:
			return "IFACE_CONTAINS_FUNC", constants.KindInterface, constants.KindFunction
		case constants.SourceKindFile:
			return "FILE_CONTAINS", constants.KindFile, constants.KindFunction
		case constants.SourceKindFileClass:
			return "FILE_CONTAINS_CLASS", constants.KindFile, constants.KindClass
		case constants.SourceKindFileInterface:
			return "FILE_CONTAINS_IFACE", constants.KindFile, constants.KindInterface
		default:
			return "CONTAINS", constants.KindRepository, constants.KindFile
		}
	case model.RelMemberOf:
		switch sourceKind {
		case constants.KindClass:
			return "MEMBER_OF_CLASS", constants.KindClass, constants.KindCommunity
		default:
			return "MEMBER_OF_FUNC", constants.KindFunction, constants.KindCommunity
		}
	case model.RelDependsOn:
		return "DEPENDS_ON", constants.KindDirectory, constants.KindDirectory
	case model.RelFetches:
		return "FETCHES", constants.KindFunction, constants.KindRoute
	case model.RelMiddleware:
		return "MIDDLEWARE", constants.KindRoute, constants.KindFunction
	case model.RelStep:
		return "STEP", constants.KindProcess, constants.KindFunction
	case model.RelInjects:
		return "INJECTS", constants.KindFunction, constants.KindFunction
	case model.RelRemoteCallsRoute:
		return "REMOTE_CALLS_ROUTE", constants.KindFunction, constants.KindRoute
	case model.RelRemoteCallsExt:
		return "REMOTE_CALLS_EXT", constants.KindFunction, constants.KindExternalService
	case model.RelUnresolvedCall:
		return "UNRESOLVED_CALL", constants.KindFunction, constants.KindFunction
	case model.RelHasAnnotation:
		switch sourceKind {
		case constants.KindClass:
			return "HAS_ANNOTATION_CLASS", constants.KindClass, constants.KindAnnotation
		case constants.KindInterface:
			return "HAS_ANNOTATION_IFACE", constants.KindInterface, constants.KindAnnotation
		default:
			return "HAS_ANNOTATION_FUNC", constants.KindFunction, constants.KindAnnotation
		}
	default:
		return "", "", ""
	}
}

func resultToNode(result *gokuzu.QueryResult, kind string) *model.Node {
	if !result.HasNext() {
		return nil
	}
	row, err := result.Next()
	if err != nil {
		return nil
	}
	value, err := row.GetValue(0)
	if err != nil {
		return nil
	}
	props := make(map[string]any)
	if nodeMap, ok := value.(map[string]any); ok {
		props = nodeMap
	}
	id := ""
	if idValue, ok := props["id"]; ok {
		id = fmt.Sprint(idValue)
	}
	return &model.Node{ID: id, Kind: kind, Properties: props}
}
