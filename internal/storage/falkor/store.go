// Package falkor implements GraphStore using FalkorDB (Redis-compatible graph database).
package falkor

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/kirovcaptain/FlashCodeGraph/internal/config"
	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/branch"
)

// allNodeLabels is the ordered list of all node labels, Function first for fast lookup.
var allNodeLabels = []string{"Function", "Class", "Interface", "File", "Directory",
	"Repository", "Route", "QueryNode", "Annotation", "ExternalService"}

// Store implements storage.GraphStore backed by FalkorDB.
type Store struct {
	client    *redis.Client
	graphName string
	shared    bool // true = Close() does not close client (external ownership)
}

// New creates a FalkorDB store. Supports TCP (host:port) or Unix socket.
func New(address string, graphName string) (*Store, error) {
	client, err := NewClient(address)
	if err != nil {
		return nil, err
	}
	return &Store{client: client, graphName: graphName}, nil
}

// NewWithClient creates a Store using an existing redis.Client.
// The client is NOT closed when Store.Close() is called (shared ownership).
func NewWithClient(client *redis.Client, graphName string) *Store {
	return &Store{client: client, graphName: graphName, shared: true}
}

// NewClient creates a redis.Client for the given FalkorDB address.
// The caller is responsible for closing the client.
func NewClient(address string) (*redis.Client, error) {
	opts := &redis.Options{
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 5 * time.Minute,
	}
	if strings.HasSuffix(address, ".sock") {
		opts.Network = "unix"
		opts.Addr = address
	} else {
		opts.Addr = address
	}
	client := redis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("falkor: connect %s: %w", address, err)
	}
	return client, nil
}

// ResolveGraphName computes the FalkorDB graph name for a project+branch.
func ResolveGraphName(cfg *config.Config, repoPath string) string {
	_, _, graphName := storage.ResolveStorageAddress(cfg)
	absPath, _ := filepath.Abs(repoPath)
	if absPath != "" {
		projectName := filepath.Base(absPath)
		branchName := cfg.Storage.Branch
		if branchName == "" {
			branchName = branch.DetectBranch(absPath)
		}
		graphName = graphName + "_" + projectName + "_" + branchName
		graphName = strings.NewReplacer("-", "_", "/", "_").Replace(graphName)
	}
	return graphName
}

// query executes a Cypher query and returns the raw result.
func (store *Store) query(ctx context.Context, cypher string) ([]interface{}, error) {
	result, err := store.client.Do(ctx, "GRAPH.QUERY", store.graphName, cypher).Result()
	if err != nil {
		return nil, err
	}
	rows, ok := result.([]interface{})
	if !ok {
		return nil, nil
	}
	return rows, nil
}

// Migrate creates indexes on node ID properties for fast MATCH lookups.
func (store *Store) Migrate(ctx context.Context) error {
	labels := []string{"Function", "Class", "Interface", "File", "Directory",
		"Repository", "Route", "QueryNode", "Annotation", "ExternalService"}
	for _, label := range labels {
		cypher := fmt.Sprintf("CREATE INDEX ON :%s(id)", label)
		store.query(ctx, cypher) // ignore error if already exists
	}
	// Index file_path for QueryNodesByFile (locate_function)
	for _, label := range []string{"Function", "Class", "Interface"} {
		cypher := fmt.Sprintf("CREATE INDEX ON :%s(file_path)", label)
		store.query(ctx, cypher)
	}
	// Index name for QueryNodesByName
	for _, label := range []string{"Function", "Class", "Interface", "Annotation"} {
		cypher := fmt.Sprintf("CREATE INDEX ON :%s(name)", label)
		store.query(ctx, cypher)
	}
	// Index qualified_name for QueryNodeByQualifiedName
	for _, label := range []string{"Function", "Class", "Interface"} {
		cypher := fmt.Sprintf("CREATE INDEX ON :%s(qualified_name)", label)
		store.query(ctx, cypher)
	}
	return nil
}

// WriteNodes writes nodes in batch.
func (store *Store) WriteNodes(ctx context.Context, nodes []model.Node) error {
	for _, node := range nodes {
		if err := store.mergeNode(ctx, node); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) CreateNodes(ctx context.Context, nodes []model.Node) error {
	const batchSize = 200
	// Group by Kind for batch CREATE
	grouped := make(map[string][]model.Node)
	for i := range nodes {
		grouped[nodes[i].Kind] = append(grouped[nodes[i].Kind], nodes[i])
	}
	for kind, kindNodes := range grouped {
		for i := 0; i < len(kindNodes); i += batchSize {
			end := i + batchSize
			if end > len(kindNodes) {
				end = len(kindNodes)
			}
			batch := kindNodes[i:end]
			var parts []string
			for j, node := range batch {
				propParts := []string{fmt.Sprintf("id: '%s'", escapeCypher(node.ID))}
				for key, value := range node.Properties {
					if value == nil {
						continue
					}
					propParts = append(propParts, fmt.Sprintf("%s: %s", key, formatCypherValue(value)))
				}
				parts = append(parts, fmt.Sprintf("(n%d:%s {%s})", j, kind, strings.Join(propParts, ", ")))
			}
			cypher := "CREATE " + strings.Join(parts, ", ")
			if _, err := store.query(ctx, cypher); err != nil {
				return err
			}
		}
	}
	return nil
}

func (store *Store) mergeNode(ctx context.Context, node model.Node) error {
	setParts := []string{}
	for key, value := range node.Properties {
		if value == nil {
			continue
		}
		setParts = append(setParts, fmt.Sprintf("n.%s = %s", key, formatCypherValue(value)))
	}
	setClause := ""
	if len(setParts) > 0 {
		setClause = " SET " + strings.Join(setParts, ", ")
	}
	cypher := fmt.Sprintf("MERGE (n:%s {id: '%s'})%s", node.Kind, escapeCypher(node.ID), setClause)
	_, err := store.query(ctx, cypher)
	return err
}

// WriteEdges writes edges in batch using Redis Pipeline.
func (store *Store) WriteEdges(ctx context.Context, edges []model.Edge) error {
	const batchSize = 200
	for i := 0; i < len(edges); i += batchSize {
		end := i + batchSize
		if end > len(edges) {
			end = len(edges)
		}
		pipe := store.client.Pipeline()
		for _, edge := range edges[i:end] {
			relType := mapRelationType(edge.Kind)
			sourceLabel, targetLabel := edgeLabels(edge.Kind, edge.SourceKind)

			propParts := []string{}
			for key, value := range edge.Properties {
				propParts = append(propParts, fmt.Sprintf("r.%s = %s", key, formatCypherValue(value)))
			}
			setClause := ""
			if len(propParts) > 0 {
				setClause = " SET " + strings.Join(propParts, ", ")
			}

			cypher := fmt.Sprintf(
				"MATCH (a:%s {id: '%s'}), (b:%s {id: '%s'}) MERGE (a)-[r:%s]->(b)%s",
				sourceLabel, escapeCypher(edge.SourceID),
				targetLabel, escapeCypher(edge.TargetID),
				relType, setClause)
			pipe.Do(ctx, "GRAPH.QUERY", store.graphName, cypher)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

// CreateEdges writes edges in batch using Redis Pipeline.
// Each edge is an independent MATCH+CREATE command; Pipeline batches them
// into a single TCP round-trip to avoid FalkorDB's UNWIND property drift bug.
func (store *Store) CreateEdges(ctx context.Context, edges []model.Edge) error {
	if len(edges) == 0 {
		return nil
	}

	const batchSize = 500
	for i := 0; i < len(edges); i += batchSize {
		end := i + batchSize
		if end > len(edges) {
			end = len(edges)
		}
		pipe := store.client.Pipeline()
		for _, edge := range edges[i:end] {
			sourceLabel, targetLabel := edgeLabels(edge.Kind, edge.SourceKind)
			relType := mapRelationType(edge.Kind)
			cypher := buildSingleEdgeCypher(sourceLabel, targetLabel, relType, edge)
			pipe.Do(ctx, "GRAPH.QUERY", store.graphName, cypher)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("pipeline batch %d: %w", i/batchSize, err)
		}
	}
	return nil
}

// buildSingleEdgeCypher builds a MATCH+CREATE cypher for one edge.
func buildSingleEdgeCypher(sourceLabel, targetLabel, relType string, edge model.Edge) string {
	var propParts []string
	for key, value := range edge.Properties {
		propParts = append(propParts, fmt.Sprintf("%s:%s", key, formatCypherValue(value)))
	}
	propClause := ""
	if len(propParts) > 0 {
		propClause = " {" + strings.Join(propParts, ",") + "}"
	}
	return fmt.Sprintf(
		"MATCH (a:%s {id:'%s'}),(b:%s {id:'%s'}) CREATE (a)-[:%s%s]->(b)",
		sourceLabel, escapeCypher(edge.SourceID),
		targetLabel, escapeCypher(edge.TargetID),
		relType, propClause)
}

// edgeLabels returns source and target node labels for a relation kind.
func edgeLabels(kind model.RelationKind, sourceKind string) (string, string) {
	switch kind {
	case model.RelCalls:
		return "Function", "Function"
	case model.RelExtends:
		return "Class", "Class"
	case model.RelImplements:
		return "Class", "Interface"
	case model.RelOverrides:
		return "Function", "Function"
	case model.RelDispatches:
		return "Function", "Function"
	case model.RelImports:
		return "File", "File"
	case model.RelHandles:
		return "Function", "Route"
	case model.RelExecutes:
		return "Function", "QueryNode"
	case model.RelHasAnnotation:
		switch sourceKind {
		case "Class":
			return "Class", "Annotation"
		case "Interface":
			return "Interface", "Annotation"
		default:
			return "Function", "Annotation"
		}
	case model.RelContains:
		switch sourceKind {
		case "Directory":
			return "Directory", "File"
		case constants.SourceKindClassFunc:
			return "Class", "Function"
		case constants.SourceKindFile, constants.SourceKindFileClass, constants.SourceKindFileInterface:
			return "File", "Function" // FalkorDB doesn't need exact target label
		default:
			return "Repository", "File"
		}
	case model.RelStep:
		return "Process", "Function"
	default:
		return "Function", "Function"
	}
}

// DeleteNodesByFile removes all nodes associated with a file path.
func (store *Store) DeleteNodesByFile(ctx context.Context, filePath string) error {
	for _, label := range []string{"Function", "Class", "Interface", "File", "Route", "QueryNode", "Annotation"} {
		cypher := fmt.Sprintf("MATCH (n:%s) WHERE n.file_path = '%s' DETACH DELETE n", label, escapeCypher(filePath))
		if _, err := store.query(ctx, cypher); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) DeleteNodeByID(ctx context.Context, id string) error {
	for _, label := range allNodeLabels {
		cypher := fmt.Sprintf("MATCH (n:%s {id: '%s'}) DETACH DELETE n", label, escapeCypher(id))
		if _, err := store.query(ctx, cypher); err != nil {
			return err
		}
	}
	return nil
}

// DeleteEdgesBySource removes all outgoing edges from a source node.
func (store *Store) DeleteEdgesBySource(ctx context.Context, sourceID string) error {
	for _, label := range allNodeLabels {
		cypher := fmt.Sprintf("MATCH (a:%s {id: '%s'})-[r]->(b) DELETE r", label, escapeCypher(sourceID))
		if _, err := store.query(ctx, cypher); err != nil {
			return err
		}
	}
	return nil
}

// DeleteEdgesByTarget removes all incoming edges to a target node.
func (store *Store) DeleteEdgesByTarget(ctx context.Context, targetID string) error {
	for _, label := range allNodeLabels {
		cypher := fmt.Sprintf("MATCH (a)-[r]->(b:%s {id: '%s'}) DELETE r", label, escapeCypher(targetID))
		if _, err := store.query(ctx, cypher); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) DeleteAllByKind(ctx context.Context, kind string) error {
	cypher := fmt.Sprintf("MATCH (n:%s) DETACH DELETE n", kind)
	_, err := store.query(ctx, cypher)
	return err
}

// QueryNodeByID returns a single node by ID.
func (store *Store) QueryNodeByID(ctx context.Context, id string) (*model.Node, error) {
	// Try each label in order (Function first — most common)
	for _, label := range allNodeLabels {
		returnClause := model.QueryReturnClause(label)
		cypher := fmt.Sprintf("MATCH (n:%s {id: '%s'}) RETURN %s LIMIT 1", label, escapeCypher(id), returnClause)
		rows, err := store.query(ctx, cypher)
		if err != nil {
			return nil, err
		}
		if len(rows) < 2 {
			continue
		}
		headerRow, ok := rows[0].([]interface{})
		if !ok {
			continue
		}
		dataRows, ok := rows[1].([]interface{})
		if !ok || len(dataRows) == 0 {
			continue
		}
		var colNames []string
		for _, h := range headerRow {
			name, _ := h.(string)
			if len(name) > 2 && name[:2] == "n." {
				name = name[2:]
			}
			colNames = append(colNames, name)
		}
		cols, ok := dataRows[0].([]interface{})
		if !ok {
			continue
		}
		props := make(map[string]any)
		nodeID := ""
		for i, col := range colNames {
			if i >= len(cols) {
				break
			}
			if col == "id" {
				nodeID = fmt.Sprint(cols[i])
			} else if cols[i] != nil {
				props[col] = cols[i]
			}
		}
		return &model.Node{ID: nodeID, Kind: label, Properties: props}, nil
	}
	return nil, nil
}

// QueryNodeByQualifiedName returns a single node by its qualified name.
func (store *Store) QueryNodeByQualifiedName(ctx context.Context, qualifiedName string) (*model.Node, error) {
	for _, label := range []string{"Function", "Class", "Interface"} {
		returnClause := model.QueryReturnClause(label)
		cypher := fmt.Sprintf("MATCH (n:%s {qualified_name: '%s'}) RETURN %s LIMIT 1", label, escapeCypher(qualifiedName), returnClause)
		rows, err := store.query(ctx, cypher)
		if err != nil {
			return nil, err
		}
		if len(rows) < 2 {
			continue
		}
		headerRow, ok := rows[0].([]interface{})
		if !ok {
			continue
		}
		dataRows, ok := rows[1].([]interface{})
		if !ok || len(dataRows) == 0 {
			continue
		}
		var colNames []string
		for _, header := range headerRow {
			columnName, _ := header.(string)
			if len(columnName) > 2 && columnName[:2] == "n." {
				columnName = columnName[2:]
			}
			colNames = append(colNames, columnName)
		}
		cols, ok := dataRows[0].([]interface{})
		if !ok {
			continue
		}
		props := make(map[string]any)
		nodeID := ""
		for i, col := range colNames {
			if i >= len(cols) {
				break
			}
			if col == "id" {
				nodeID = fmt.Sprint(cols[i])
			} else if cols[i] != nil {
				props[col] = cols[i]
			}
		}
		return &model.Node{ID: nodeID, Kind: label, Properties: props}, nil
	}
	return nil, nil
}
// QueryNodesByName returns nodes matching a name.
func (store *Store) QueryNodesByName(ctx context.Context, name string, opts model.QueryOpts) ([]model.Node, error) {

	labels := []string{"Function", "Class", "Interface"}
	if len(opts.Kinds) > 0 {
		labels = opts.Kinds
	}

	// Use schema-driven column list + labels for kind detection
	allCols := "n.id, labels(n)[0]"
	seen := map[string]bool{}
	for _, kind := range labels {
		for _, col := range model.ColumnNames(kind) {
			if !seen[col] {
				allCols += ", n." + col
				seen[col] = true
			}
		}
	}

	// UNION across labels to hit name index
	var parts []string
	for _, label := range labels {
		parts = append(parts, fmt.Sprintf(
			"MATCH (n:%s) WHERE n.name = '%s' RETURN %s",
			label, escapeCypher(name), allCols))
	}
	cypher := strings.Join(parts, " UNION ")
	if opts.Limit > 0 {
		cypher += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}

	rows, err := store.query(ctx, cypher)
	if err != nil {
		return nil, err
	}

	return parseFullNodeResults(rows), nil
}

// QueryEdges returns edges connected to a node.
func (store *Store) QueryEdges(ctx context.Context, nodeID string, nodeKind string, relKind model.RelationKind, direction model.Direction) ([]model.Edge, error) {
	relType := mapRelationType(relKind)
	label := nodeKind
	if label == "" {
		label = "Function"
	}
	var cypher string
	switch direction {
	case model.Outgoing:
		cypher = fmt.Sprintf("MATCH (a:%s {id: '%s'})-[r:%s]->(b) RETURN a.id, b.id", label, escapeCypher(nodeID), relType)
	case model.Incoming:
		cypher = fmt.Sprintf("MATCH (a)-[r:%s]->(b {id: '%s'}) RETURN a.id, b.id", relType, escapeCypher(nodeID))
	default:
		cypher = fmt.Sprintf("MATCH (a:%s {id: '%s'})-[r:%s]-(b) RETURN a.id, b.id", label, escapeCypher(nodeID), relType)
	}

	rows, err := store.query(ctx, cypher)
	if err != nil {
		return nil, err
	}

	return parseEdgeResults(rows, relKind), nil
}

// TraverseCallChain traverses CALLS relationships up to depth.
// QueryAllEdges returns all edges of a given relation kind in a single query.
func (store *Store) QueryAllEdges(ctx context.Context, relKind model.RelationKind, limit int) ([]model.Edge, error) {
	relType := mapRelationType(relKind)
	cypher := fmt.Sprintf("MATCH (a)-[r:%s]->(b) RETURN a.id, b.id, r.confidence", relType)
	if limit > 0 {
		cypher += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := store.query(ctx, cypher)
	if err != nil {
		return nil, nil
	}

	if len(rows) < 2 {
		return nil, nil
	}
	dataRows, ok := rows[1].([]interface{})
	if !ok {
		return nil, nil
	}

	var edges []model.Edge
	for _, row := range dataRows {
		rowSlice, ok := row.([]interface{})
		if !ok || len(rowSlice) < 2 {
			continue
		}
		edge := model.Edge{
			SourceID:   fmt.Sprint(rowSlice[0]),
			TargetID:   fmt.Sprint(rowSlice[1]),
			Kind:       relKind,
			Properties: make(map[string]any),
		}
		if len(rowSlice) >= 3 && rowSlice[2] != nil {
			switch c := rowSlice[2].(type) {
			case float64:
				edge.Properties["confidence"] = c
			case string:
				if f, err := strconv.ParseFloat(c, 64); err == nil {
					edge.Properties["confidence"] = f
				}
			}
		}
		edges = append(edges, edge)
	}
	return edges, nil
}

func (store *Store) TraverseCallChain(ctx context.Context, nodeID string, depth int, direction model.Direction, minConfidence float64) (*model.Subgraph, error) {
	subgraph := &model.Subgraph{}

	// Query nodes
	var nodeCypher string
	switch direction {
	case model.Outgoing:
		nodeCypher = fmt.Sprintf(
			"MATCH (a:Function {id: '%s'})-[:CALLS*1..%d]->(b) RETURN DISTINCT b.id, b.name, b.file_path, b.qualified_name",
			escapeCypher(nodeID), depth)
	case model.Incoming:
		nodeCypher = fmt.Sprintf(
			"MATCH (a)-[:CALLS*1..%d]->(b:Function {id: '%s'}) RETURN DISTINCT a.id, a.name, a.file_path, a.qualified_name",
			depth, escapeCypher(nodeID))
	default:
		nodeCypher = fmt.Sprintf(
			"MATCH (a:Function {id: '%s'})-[:CALLS*1..%d]-(b) RETURN DISTINCT b.id, b.name, b.file_path, b.qualified_name",
			escapeCypher(nodeID), depth)
	}

	rows, err := store.query(ctx, nodeCypher)
	if err != nil {
		return subgraph, nil
	}
	subgraph.Nodes = parseCallChainResults(rows)

	if len(subgraph.Nodes) == 0 {
		return subgraph, nil
	}

	// Query CALLS edges between root + all reachable nodes for tree structure
	nodeIDs := make([]string, 0, len(subgraph.Nodes)+1)
	nodeIDs = append(nodeIDs, "'"+escapeCypher(nodeID)+"'")
	for _, n := range subgraph.Nodes {
		nodeIDs = append(nodeIDs, "'"+escapeCypher(n.ID)+"'")
	}
	edgeCypher := fmt.Sprintf(
		"MATCH (a:Function)-[r:CALLS]->(b:Function) WHERE a.id IN [%s] AND b.id IN [%s] RETURN a.id, b.id, r.confidence, r.line, r.flow_context, r.flow_line, r.declared_type, r.polymorphic, type(r)",
		strings.Join(nodeIDs, ","), strings.Join(nodeIDs, ","))
	edgeRows, err := store.query(ctx, edgeCypher)
	if err == nil {
		subgraph.Edges = parseEdgeResults(edgeRows)
	}

	return subgraph, nil
}

// TraverseImpact finds all nodes affected by changes to a node.
func (store *Store) TraverseImpact(ctx context.Context, nodeID string, depth int) (*model.Subgraph, error) {
	return store.TraverseCallChain(ctx, nodeID, depth, model.Incoming, 0)
}

// BatchUpdateNodeProperties updates properties on multiple nodes using UNWIND.
func (store *Store) BatchUpdateNodeProperties(ctx context.Context, kind string, updates []storage.PropertyUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	const batchSize = 500
	for i := 0; i < len(updates); i += batchSize {
		end := i + batchSize
		if end > len(updates) {
			end = len(updates)
		}
		pipe := store.client.Pipeline()
		for _, u := range updates[i:end] {
			var setClauses []string
			for k, v := range u.Props {
				switch val := v.(type) {
				case string:
					setClauses = append(setClauses, fmt.Sprintf("n.%s = '%s'", k, escapeCypher(val)))
				case float64:
					setClauses = append(setClauses, fmt.Sprintf("n.%s = %f", k, val))
				default:
					setClauses = append(setClauses, fmt.Sprintf("n.%s = '%v'", k, v))
				}
			}
			cypher := fmt.Sprintf("MATCH (n:%s {id: '%s'}) SET %s", kind, escapeCypher(u.NodeID), strings.Join(setClauses, ", "))
			pipe.Do(ctx, "GRAPH.QUERY", store.graphName, cypher)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("batch update %d: %w", i/batchSize, err)
		}
	}
	return nil
}

// QueryNodesByFile returns Function, Class, and Interface nodes in a file.
func (store *Store) QueryNodesByFile(ctx context.Context, filePath string) ([]model.Node, error) {
	var parts []string
	for _, label := range []string{"Function", "Class", "Interface"} {
		parts = append(parts, fmt.Sprintf(
			"MATCH (n:%s) WHERE n.file_path = '%s' AND n.start_line IS NOT NULL AND n.end_line IS NOT NULL RETURN n.id, labels(n)[0], n.qualified_name, n.start_line, n.end_line",
			label, escapeCypher(filePath)))
	}
	cypher := strings.Join(parts, " UNION ")
	rows, err := store.query(ctx, cypher)
	if err != nil {
		return nil, err
	}
	return parseFullNodeResults(rows), nil
}

// QueryAllByKind returns all nodes of a specific kind.
func (store *Store) QueryAllByKind(ctx context.Context, kind string, limit int) ([]model.Node, error) {
	returnClause := model.QueryReturnClause(kind)
	limitClause := ""
	if limit > 0 {
		limitClause = fmt.Sprintf(" LIMIT %d", limit)
	}
	cypher := fmt.Sprintf("MATCH (n:%s) RETURN %s%s", kind, returnClause, limitClause)
	rows, err := store.query(ctx, cypher)
	if err != nil {
		return nil, err
	}

	if len(rows) < 2 {
		return nil, nil
	}

	// Parse header to get column names
	headerRow, ok := rows[0].([]interface{})
	if !ok {
		return parseSimpleNodeResults(rows), nil
	}
	var colNames []string
	for _, h := range headerRow {
		name, _ := h.(string)
		// Strip "n." prefix
		if len(name) > 2 && name[:2] == "n." {
			name = name[2:]
		}
		colNames = append(colNames, name)
	}

	dataRows, ok := rows[1].([]interface{})
	if !ok {
		return nil, nil
	}

	var nodes []model.Node
	for _, row := range dataRows {
		rowSlice, ok := row.([]interface{})
		if !ok {
			continue
		}
		nodeProps := make(map[string]any)
		nodeID := ""
		for i, val := range rowSlice {
			if i >= len(colNames) {
				break
			}
			if colNames[i] == "id" {
				nodeID = fmt.Sprint(val)
			} else {
				nodeProps[colNames[i]] = val
			}
		}
		nodes = append(nodes, model.Node{ID: nodeID, Kind: kind, Properties: nodeProps})
	}
	return nodes, nil
}

// SearchFTS performs full-text search on node names.
func (store *Store) SearchFTS(ctx context.Context, queryText string, limit int) ([]storage.SearchResult, error) {
	var parts []string
	for _, label := range []string{"Function", "Class", "Interface"} {
		parts = append(parts, fmt.Sprintf(
			"MATCH (n:%s) WHERE n.name CONTAINS '%s' RETURN n.id AS id, '%s' AS kind, n.name AS name, n.file_path AS file_path, n.qualified_name AS qualified_name",
			label, escapeCypher(queryText), label))
	}
	cypher := strings.Join(parts, " UNION ") + fmt.Sprintf(" LIMIT %d", limit)

	rows, err := store.query(ctx, cypher)
	if err != nil {
		return nil, err
	}

	return parseSearchResults(rows), nil
}

// GetStats returns aggregate statistics.
func (store *Store) GetStats(ctx context.Context) (*model.GraphStats, error) {
	stats := &model.GraphStats{
		NodesByKind: make(map[string]int),
		EdgesByKind: make(map[string]int),
		FilesByLang: make(map[string]int),
	}

	labels := []string{"Function", "Class", "Interface", "Variable", "File", "Route",
		"Repository", "Directory", "QueryNode", "ExternalService", "Annotation"}
	for _, label := range labels {
		cypher := fmt.Sprintf("MATCH (n:%s) RETURN count(n)", label)
		rows, err := store.query(ctx, cypher)
		if err != nil {
			continue
		}
		count := parseSingleInt(rows)
		if count > 0 {
			stats.NodesByKind[label] = count
			stats.NodeCount += count
		}
	}
	stats.FileCount = stats.NodesByKind["File"]

	// Edge counts
	edgeTypes := []string{"CALLS", "EXTENDS", "IMPLEMENTS", "OVERRIDES", "IMPORTS",
		"HANDLES", "EXECUTES", "REMOTE_CALLS", "CONTAINS", "HAS_ANNOTATION", "STEP", "UNRESOLVED_CALL"}
	for _, rel := range edgeTypes {
		cypher := fmt.Sprintf("MATCH ()-[r:%s]->() RETURN count(r)", rel)
		rows, err := store.query(ctx, cypher)
		if err != nil {
			continue
		}
		count := parseSingleInt(rows)
		if count > 0 {
			stats.EdgesByKind[rel] = count
			stats.EdgeCount += count
		}
	}

	// Files by language
	langCypher := "MATCH (n:File) WHERE n.language IS NOT NULL AND n.language <> '' RETURN n.language, count(n)"
	langRows, err := store.query(ctx, langCypher)
	if err == nil && len(langRows) >= 2 {
		if dataRows, ok := langRows[1].([]interface{}); ok {
			for _, row := range dataRows {
				if arr, ok := row.([]interface{}); ok && len(arr) == 2 {
					if lang, ok := arr[0].(string); ok {
						if cnt, ok := arr[1].(int64); ok {
							stats.FilesByLang[lang] = int(cnt)
						}
					}
				}
			}
		}
	}

	return stats, nil
}
func (store *Store) Close() error {
	if store.shared {
		return nil
	}
	return store.client.Close()
}

// DeleteGraph removes the entire graph (for testing/cleanup).
func (store *Store) DeleteGraph(ctx context.Context) error {
	_, err := store.client.Do(ctx, "GRAPH.DELETE", store.graphName).Result()
	return err
}

func (store *Store) ClearAll(ctx context.Context) error {
	return store.DeleteGraph(ctx)
}

// Helpers

func mapRelationType(kind model.RelationKind) string {
	// FalkorDB is schema-free, use the relation kind directly.
	// Merge KùzuDB's split tables back into single types.
	switch kind {
	case model.RelRemoteCallsRoute, model.RelRemoteCallsExt:
		return "REMOTE_CALLS"
	default:
		return string(kind)
	}
}

// escapeCypher escapes string values for Cypher queries.
// NOTE: FalkorDB's GRAPH.QUERY does not support parameterized queries.
// This is a known limitation. Input comes from our own parser, not user input,
// so injection risk is low. But we escape known dangerous characters.
func escapeCypher(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	return s
}

func formatCypherValue(value interface{}) string {
	switch v := value.(type) {
	case string:
		return fmt.Sprintf("'%s'", escapeCypher(v))
	case float64:
		return fmt.Sprintf("%f", v)
	case float32:
		return fmt.Sprintf("%f", v)
	case int:
		return fmt.Sprintf("%d", v)
	case int32:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("'%v'", v)
	}
}

// parseCallChainResults parses TraverseCallChain results: id, name, file_path (3 columns).
func parseCallChainResults(rows []interface{}) []model.Node {
	if len(rows) < 2 {
		return nil
	}
	dataRows, ok := rows[1].([]interface{})
	if !ok {
		return nil
	}
	var nodes []model.Node
	for _, row := range dataRows {
		cols, ok := row.([]interface{})
		if !ok || len(cols) < 4 {
			continue
		}
		nodes = append(nodes, model.Node{
			ID:   fmt.Sprint(cols[0]),
			Kind: "Function",
			Properties: map[string]interface{}{
				"name":           cols[1],
				"file_path":      cols[2],
				"qualified_name": cols[3],
			},
		})
	}
	return nodes
}

func parseSimpleNodeResults(rows []interface{}) []model.Node {
	if len(rows) < 2 {
		return nil
	}
	dataRows, ok := rows[1].([]interface{})
	if !ok {
		return nil
	}
	var nodes []model.Node
	for _, row := range dataRows {
		cols, ok := row.([]interface{})
		if !ok || len(cols) < 4 {
			continue
		}
		node := model.Node{
			ID:   fmt.Sprint(cols[0]),
			Kind: fmt.Sprint(cols[1]),
			Properties: map[string]interface{}{
				"name":       cols[2],
				"file_path":  safeIndex(cols, 3),
				"start_line": safeIndex(cols, 4),
			},
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// parseFullNodeResults parses query results with all symbol properties.
func parseFullNodeResults(rows []interface{}) []model.Node {
	if len(rows) < 2 {
		return nil
	}

	headerRow, _ := rows[0].([]interface{})
	var colNames []string
	for _, h := range headerRow {
		name, _ := h.(string)
		if len(name) > 2 && name[:2] == "n." {
			name = name[2:]
		}
		colNames = append(colNames, name)
	}

	dataRows, ok := rows[1].([]interface{})
	if !ok {
		return nil
	}
	var nodes []model.Node
	for _, row := range dataRows {
		cols, ok := row.([]interface{})
		if !ok || len(cols) < 2 {
			continue
		}
		props := make(map[string]any)
		nodeID := ""
		kind := ""
		for i, val := range cols {
			if i >= len(colNames) {
				break
			}
			switch colNames[i] {
			case "id":
				nodeID = fmt.Sprint(val)
			case "labels(n)[0]":
				kind = fmt.Sprint(val)
			default:
				if val != nil {
					props[colNames[i]] = val
				}
			}
		}
		nodes = append(nodes, model.Node{ID: nodeID, Kind: kind, Properties: props})
	}
	return nodes
}



func parseEdgeResults(rows []interface{}, defaultKind ...model.RelationKind) []model.Edge {
	if len(rows) < 2 {
		return nil
	}
	dataRows, ok := rows[1].([]interface{})
	if !ok {
		return nil
	}
	var edges []model.Edge
	for _, row := range dataRows {
		cols, ok := row.([]interface{})
		if !ok || len(cols) < 2 {
			continue
		}
		// Determine edge kind: use type(r) column if present, else use defaultKind
		edgeKind := model.RelCalls
		if len(defaultKind) > 0 {
			edgeKind = defaultKind[0]
		}
		if len(cols) >= 9 && cols[8] != nil {
			if typeName, ok := cols[8].(string); ok {
				edgeKind = model.RelationKind(typeName)
			}
		}
		edge := model.Edge{
			SourceID:   fmt.Sprint(cols[0]),
			TargetID:   fmt.Sprint(cols[1]),
			Kind:       edgeKind,
			Properties: make(map[string]any),
		}
		if len(cols) >= 3 && cols[2] != nil {
			switch c := cols[2].(type) {
			case float64:
				edge.Properties["confidence"] = c
			case string:
				if f, err := strconv.ParseFloat(c, 64); err == nil {
					edge.Properties["confidence"] = f
				}
			}
		}
		if len(cols) >= 4 && cols[3] != nil {
			switch l := cols[3].(type) {
			case float64:
				edge.Properties["line"] = int(l)
			case int64:
				edge.Properties["line"] = int(l)
			}
		}
		if len(cols) >= 5 && cols[4] != nil {
			if fc, ok := cols[4].(string); ok && fc != "" {
				edge.Properties["flow_context"] = fc
			}
		}
		if len(cols) >= 6 && cols[5] != nil {
			switch l := cols[5].(type) {
			case float64:
				edge.Properties["flow_line"] = int(l)
			case int64:
				edge.Properties["flow_line"] = int(l)
			}
		}
		if len(cols) >= 7 && cols[6] != nil {
			if dt, ok := cols[6].(string); ok && dt != "" {
				edge.Properties["declared_type"] = dt
			}
		}
		if len(cols) >= 8 && cols[7] != nil {
			switch p := cols[7].(type) {
			case bool:
				if p {
					edge.Properties["polymorphic"] = true
				}
			case string:
				if p == "true" {
					edge.Properties["polymorphic"] = true
				}
			}
		}
		edges = append(edges, edge)
	}
	return edges
}

func parseSearchResults(rows []interface{}) []storage.SearchResult {
	if len(rows) < 2 {
		return nil
	}
	dataRows, ok := rows[1].([]interface{})
	if !ok {
		return nil
	}
	var results []storage.SearchResult
	for _, row := range dataRows {
		cols, ok := row.([]interface{})
		if !ok || len(cols) < 4 {
			continue
		}
		results = append(results, storage.SearchResult{
			NodeID:        fmt.Sprint(cols[0]),
			Kind:          fmt.Sprint(cols[1]),
			Name:          fmt.Sprint(cols[2]),
			Path:          fmt.Sprint(safeIndex(cols, 3)),
			QualifiedName: fmt.Sprint(safeIndex(cols, 4)),
		})
	}
	return results
}

func parseSingleInt(rows []interface{}) int {
	if len(rows) < 2 {
		return 0
	}
	dataRows, ok := rows[1].([]interface{})
	if !ok || len(dataRows) == 0 {
		return 0
	}
	cols, ok := dataRows[0].([]interface{})
	if !ok || len(cols) == 0 {
		return 0
	}
	switch v := cols[0].(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func safeIndex(slice []interface{}, index int) interface{} {
	if index < len(slice) {
		return slice[index]
	}
	return nil
}
