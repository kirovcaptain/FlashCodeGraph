// Package falkor implements GraphStore using FalkorDB (Redis-compatible graph database).
package falkor

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
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
var allNodeLabels = constants.AllNodeKinds

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
	// Remove FalkorDB default result set size limit (default 10000) to avoid truncation on large graphs.
	client.Do(context.Background(), "GRAPH.CONFIG", "SET", "RESULTSET_SIZE", -1)
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
		graphName = strings.ReplaceAll(graphName, "/", "_")
	}
	return graphName
}

// GraphExists checks if the graph exists in FalkorDB by listing all graphs.
// Use this before queries to prevent FalkorDB from auto-creating empty graphs.
func (store *Store) GraphExists(ctx context.Context) bool {
	result, err := store.client.Do(ctx, "GRAPH.LIST").Result()
	if err != nil {
		return false
	}
	graphs, ok := result.([]interface{})
	if !ok {
		return false
	}
	for _, g := range graphs {
		if name, ok := g.(string); ok && name == store.graphName {
			return true
		}
	}
	return false
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

// cypherParam represents a single parameter for parameterized Cypher queries.
// Use ordered slice instead of map to ensure stable parameter order for execution plan cache hits.
type cypherParam struct {
	Key   string
	Value any
}

// buildParamsHeader builds the CYPHER params prefix for parameterized queries.
// FalkorDB's CYPHER prefix uses a non-standard format: map keys are unquoted.
// Example: buildParamsHeader([]cypherParam{{"sourceID", "abc"}}) → `CYPHER sourceID="abc" `
func buildParamsHeader(params []cypherParam) string {
	if len(params) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("CYPHER ")
	for _, param := range params {
		builder.WriteString(param.Key)
		builder.WriteByte('=')
		formatFalkorValue(&builder, param.Value)
		builder.WriteByte(' ')
	}
	return builder.String()
}

// formatFalkorValue writes a value in FalkorDB's CYPHER param format.
// Strings use json.Marshal (double-quoted with escaping).
// Maps use unquoted keys: {id:"abc"} not {"id":"abc"}.
// Arrays of maps: [{id:"abc"},{id:"def"}].
func formatFalkorValue(builder *strings.Builder, value any) {
	switch v := value.(type) {
	case string:
		encoded, _ := json.Marshal(v)
		builder.Write(encoded)
	case int:
		fmt.Fprint(builder, v)
	case int64:
		fmt.Fprint(builder, v)
	case float64:
		fmt.Fprint(builder, v)
	case bool:
		if v {
			builder.WriteString("true")
		} else {
			builder.WriteString("false")
		}
	case []map[string]any:
		builder.WriteByte('[')
		for i, m := range v {
			if i > 0 {
				builder.WriteByte(',')
			}
			formatFalkorMap(builder, m)
		}
		builder.WriteByte(']')
	case map[string]any:
		formatFalkorMap(builder, v)
	case []string:
		builder.WriteByte('[')
		for i, s := range v {
			if i > 0 {
				builder.WriteByte(',')
			}
			encoded, _ := json.Marshal(s)
			builder.Write(encoded)
		}
		builder.WriteByte(']')
	case nil:
		builder.WriteString("null")
	default:
		encoded, _ := json.Marshal(v)
		builder.Write(encoded)
	}
}

// formatFalkorMap writes a map with unquoted keys in FalkorDB format.
func formatFalkorMap(builder *strings.Builder, m map[string]any) {
	builder.WriteByte('{')
	first := true
	for k, v := range m {
		if !first {
			builder.WriteByte(',')
		}
		first = false
		builder.WriteString(k)
		builder.WriteByte(':')
		formatFalkorValue(builder, v)
	}
	builder.WriteByte('}')
}

// queryWithParams executes a parameterized Cypher query.
func (store *Store) queryWithParams(ctx context.Context, cypher string, params []cypherParam) ([]interface{}, error) {
	return store.query(ctx, buildParamsHeader(params)+cypher)
}

// Migrate creates indexes on node ID properties for fast MATCH lookups.
// Called only during full index (after ClearAll), not during incremental index.
func (store *Store) Migrate(ctx context.Context) error {
	// Safe: label names are from hardcoded constants, not user input.
	for _, label := range constants.AllNodeKinds {
		store.query(ctx, "CREATE INDEX ON :"+label+"(id)")
	}
	for _, label := range constants.BaseSymbolKinds {
		store.query(ctx, "CREATE INDEX ON :"+label+"(file_path)")
	}
	for _, label := range []string{constants.KindFunction, constants.KindClass, constants.KindInterface, constants.KindVariable, constants.KindAnnotation} {
		store.query(ctx, "CREATE INDEX ON :"+label+"(name)")
	}
	for _, label := range constants.QualifiedNameKinds {
		store.query(ctx, "CREATE INDEX ON :"+label+"(qualified_name)")
	}
	for _, label := range constants.AllNodeKinds {
		// GRAPH.CONSTRAINT CREATE is a Redis command, not Cypher.
		// Errors are discarded: constraint already exists is a normal idempotent case.
		store.client.Do(ctx, "GRAPH.CONSTRAINT", "CREATE", store.graphName, "UNIQUE", "NODES", label, "PROPERTIES", "1", "id")
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
	// Group by Kind for UNWIND batch CREATE
	grouped := make(map[string][]model.Node)
	for i := range nodes {
		grouped[nodes[i].Kind] = append(grouped[nodes[i].Kind], nodes[i])
	}
	for kind, kindNodes := range grouped {
		// Build fixed UNWIND template per kind using schema columns
		cols := model.ColumnNames(kind)
		var propParts []string
		propParts = append(propParts, "id: node.id")
		for _, col := range cols {
			propParts = append(propParts, col+": node."+col)
		}
		template := "UNWIND $nodes AS node CREATE (n:" + kind + " {" + strings.Join(propParts, ", ") + "})"

		for i := 0; i < len(kindNodes); i += batchSize {
			end := i + batchSize
			if end > len(kindNodes) {
				end = len(kindNodes)
			}
			nodeParams := make([]map[string]any, end-i)
			for j, node := range kindNodes[i:end] {
				m := map[string]any{"id": node.ID}
				for _, col := range cols {
					m[col] = node.Properties[col]
				}
				nodeParams[j] = m
			}
			if _, err := store.queryWithParams(ctx, template, []cypherParam{{"nodes", nodeParams}}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (store *Store) mergeNode(ctx context.Context, node model.Node) error {
	cols := model.ColumnNames(node.Kind)
	params := []cypherParam{{"nodeID", node.ID}}
	var setParts []string
	for _, col := range cols {
		paramName := "prop_" + col
		setParts = append(setParts, "n."+col+" = $"+paramName)
		params = append(params, cypherParam{paramName, node.Properties[col]})
	}
	setClause := ""
	if len(setParts) > 0 {
		setClause = " SET " + strings.Join(setParts, ", ")
	}
	cypher := "MERGE (n:" + node.Kind + " {id: $nodeID})" + setClause
	_, err := store.queryWithParams(ctx, cypher, params)
	return err
}

// WriteEdges writes edges in batch using Redis Pipeline.
func (store *Store) WriteEdges(ctx context.Context, edges []model.Edge) error {
	const batchSize = 5000
	pipe := store.client.Pipeline()
	for i, edge := range edges {
		relType := mapRelationType(edge.Kind)
		sourceLabel, targetLabel := edgeLabels(edge.Kind, edge.SourceKind)

		params := []cypherParam{{"sourceID", edge.SourceID}, {"targetID", edge.TargetID}}
		paramSetClause := buildEdgeSetClause(edge.Properties, &params)

		cypher := "MATCH (a:" + sourceLabel + " {id: $sourceID}), (b:" + targetLabel + " {id: $targetID}) MERGE (a)-[r:" + relType + "]->(b)" + paramSetClause
		pipe.Do(ctx, "GRAPH.QUERY", store.graphName, buildParamsHeader(params)+cypher)
		if (i+1)%batchSize == 0 {
			if _, err := pipe.Exec(ctx); err != nil {
				return err
			}
			pipe = store.client.Pipeline()
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	return nil
}

// CreateEdges writes edges in batch using Redis Pipeline.
func (store *Store) CreateEdges(ctx context.Context, edges []model.Edge) error {
	if len(edges) == 0 {
		return nil
	}

	const batchSize = 5000
	pipe := store.client.Pipeline()
	for i, edge := range edges {
		sourceLabel, targetLabel := edgeLabels(edge.Kind, edge.SourceKind)
		relType := mapRelationType(edge.Kind)

		params := []cypherParam{{"sourceID", edge.SourceID}, {"targetID", edge.TargetID}}
		propClause := buildEdgePropClause(edge.Properties, &params)

		cypher := "MATCH (a:" + sourceLabel + " {id:$sourceID}),(b:" + targetLabel + " {id:$targetID}) CREATE (a)-[:" + relType + propClause + "]->(b)"
		pipe.Do(ctx, "GRAPH.QUERY", store.graphName, buildParamsHeader(params)+cypher)
		if (i+1)%batchSize == 0 {
			if _, err := pipe.Exec(ctx); err != nil {
				return fmt.Errorf("pipeline batch %d: %w", i/batchSize, err)
			}
			pipe = store.client.Pipeline()
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("pipeline final: %w", err)
	}
	return nil
}

// buildEdgeSetClause builds a parameterized SET clause for edge properties.
// Keys are sorted for stable template order.
func buildEdgeSetClause(props map[string]any, params *[]cypherParam) string {
	if len(props) == 0 {
		return ""
	}
	keys := sortedKeys(props)
	var parts []string
	for _, key := range keys {
		paramName := "ep_" + key
		parts = append(parts, "r."+key+" = $"+paramName)
		*params = append(*params, cypherParam{paramName, props[key]})
	}
	return " SET " + strings.Join(parts, ", ")
}

// buildEdgePropClause builds a parameterized property clause for CREATE edge.
// Keys are sorted for stable template order.
func buildEdgePropClause(props map[string]any, params *[]cypherParam) string {
	if len(props) == 0 {
		return ""
	}
	keys := sortedKeys(props)
	var parts []string
	for _, key := range keys {
		paramName := "ep_" + key
		parts = append(parts, key+": $"+paramName)
		*params = append(*params, cypherParam{paramName, props[key]})
	}
	return " {" + strings.Join(parts, ", ") + "}"
}

// sortedKeys returns map keys in sorted order for stable Cypher template generation.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// edgeLabels returns source and target node labels for a relation kind.
func edgeLabels(kind model.RelationKind, sourceKind string) (string, string) {
	switch kind {
	case model.RelCalls:
		return constants.KindFunction, constants.KindFunction
	case model.RelExtends:
		return constants.KindClass, constants.KindClass
	case model.RelImplements:
		return constants.KindClass, constants.KindInterface
	case model.RelOverrides:
		return constants.KindFunction, constants.KindFunction
	case model.RelDispatches:
		return constants.KindFunction, constants.KindFunction
	case model.RelImports:
		return constants.KindFile, constants.KindFile
	case model.RelHandles:
		return constants.KindFunction, constants.KindRoute
	case model.RelRemoteCallsRoute:
		return constants.KindFunction, constants.KindRoute
	case model.RelRemoteCallsExt:
		return constants.KindFunction, constants.KindExternalService
	case model.RelExecutes:
		return constants.KindFunction, constants.KindQueryNode
	case model.RelHasAnnotation:
		switch sourceKind {
		case constants.KindClass:
			return constants.KindClass, constants.KindAnnotation
		case constants.KindInterface:
			return constants.KindInterface, constants.KindAnnotation
		default:
			return constants.KindFunction, constants.KindAnnotation
		}
	case model.RelContains:
		switch sourceKind {
		case constants.KindDirectory:
			return constants.KindDirectory, constants.KindFile
		case constants.SourceKindClassFunc:
			return constants.KindClass, constants.KindFunction
		case constants.SourceKindInterfaceFunc:
			return constants.KindInterface, constants.KindFunction
		case constants.SourceKindFile, constants.SourceKindFileClass, constants.SourceKindFileInterface:
			return constants.KindFile, constants.KindFunction // FalkorDB doesn't need exact target label
		default:
			return constants.KindRepository, constants.KindFile
		}
	case model.RelStep:
		return constants.KindProcess, constants.KindFunction
	case model.RelUses:
		return constants.KindFunction, constants.KindVariable
	default:
		return constants.KindFunction, constants.KindFunction
	}
}

// DeleteNodesByFile removes all nodes associated with a file path.
func (store *Store) DeleteNodesByFile(ctx context.Context, filePath string) error {
	for _, label := range []string{constants.KindFunction, constants.KindClass, constants.KindInterface, constants.KindFile, constants.KindRoute, constants.KindQueryNode, constants.KindAnnotation} {
		cypher := "MATCH (n:" + label + ") WHERE n.file_path = $filePath DETACH DELETE n"
		if _, err := store.queryWithParams(ctx, cypher, []cypherParam{{"filePath", filePath}}); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) DeleteNodeByID(ctx context.Context, id string) error {
	for _, label := range allNodeLabels {
		cypher := "MATCH (n:" + label + " {id: $nodeID}) DETACH DELETE n"
		if _, err := store.queryWithParams(ctx, cypher, []cypherParam{{"nodeID", id}}); err != nil {
			return err
		}
	}
	return nil
}

// DeleteEdgesBySource removes all outgoing edges from a source node.
func (store *Store) DeleteEdgesBySource(ctx context.Context, sourceID string) error {
	for _, label := range allNodeLabels {
		cypher := "MATCH (a:" + label + " {id: $nodeID})-[r]->(b) DELETE r"
		if _, err := store.queryWithParams(ctx, cypher, []cypherParam{{"nodeID", sourceID}}); err != nil {
			return err
		}
	}
	return nil
}

// DeleteEdgesByTarget removes all incoming edges to a target node.
func (store *Store) DeleteEdgesByTarget(ctx context.Context, targetID string) error {
	for _, label := range allNodeLabels {
		cypher := "MATCH (a)-[r]->(b:" + label + " {id: $nodeID}) DELETE r"
		if _, err := store.queryWithParams(ctx, cypher, []cypherParam{{"nodeID", targetID}}); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) DeleteAllByKind(ctx context.Context, kind string) error {
	// Safe: kind is from hardcoded constants, not user input.
	_, err := store.query(ctx, "MATCH (n:"+kind+") DETACH DELETE n")
	return err
}

// QueryNodeByID returns a single node by ID.
func (store *Store) QueryNodeByID(ctx context.Context, id string) (*model.Node, error) {
	// Try each label in order (Function first — most common)
	for _, label := range allNodeLabels {
		returnClause := model.QueryReturnClause(label)
		cypher := "MATCH (n:" + label + " {id: $nodeID}) RETURN " + returnClause + " LIMIT 1"
		rows, err := store.queryWithParams(ctx, cypher, []cypherParam{{"nodeID", id}})
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
				nodeID = asString(cols[i])
			} else if cols[i] != nil {
				props[col] = convertByType(cols[i], model.GetColumnType(label, col))
			}
		}
		return &model.Node{ID: nodeID, Kind: label, Properties: props}, nil
	}
	return nil, nil
}

// QueryNodeByQualifiedName returns a single node by its qualified name.
func (store *Store) QueryNodeByQualifiedName(ctx context.Context, qualifiedName string) (*model.Node, error) {
	for _, label := range constants.QualifiedNameKinds {
		returnClause := model.QueryReturnClause(label)
		cypher := "MATCH (n:" + label + " {qualified_name: $qualifiedName}) RETURN " + returnClause + " LIMIT 1"
		rows, err := store.queryWithParams(ctx, cypher, []cypherParam{{"qualifiedName", qualifiedName}})
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
				nodeID = asString(cols[i])
			} else if cols[i] != nil {
				props[col] = convertByType(cols[i], model.GetColumnType(label, col))
			}
		}
		return &model.Node{ID: nodeID, Kind: label, Properties: props}, nil
	}
	return nil, nil
}
// QueryNodesByName returns nodes matching a name.
func (store *Store) QueryNodesByName(ctx context.Context, name string, opts model.QueryOpts) ([]model.Node, error) {

	labels := constants.BaseSymbolKinds
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
	// If name contains ".", treat it as a qualified_name (all supported languages use "." as separator)
	nameField := "n.name"
	if strings.Contains(name, ".") {
		nameField = "n.qualified_name"
	}
	var parts []string
	for _, label := range labels {
		parts = append(parts, "MATCH (n:"+label+") WHERE "+nameField+" = $nodeName RETURN "+allCols)
	}
	cypher := strings.Join(parts, " UNION ")
	if opts.Limit > 0 {
		cypher += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}

	rows, err := store.queryWithParams(ctx, cypher, []cypherParam{{"nodeName", name}})
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
		label = constants.KindFunction
	}

	// USES edges have custom properties (line, ref_kind) — use dedicated query path
	if relKind == model.RelUses {
		var cypher string
		switch direction {
		case model.Outgoing:
			cypher = "MATCH (a:" + label + " {id: $nodeID})-[r:" + relType + "]->(b) RETURN a.id, b.id, r.line, r.ref_kind"
		case model.Incoming:
			cypher = "MATCH (a)-[r:" + relType + "]->(b {id: $nodeID}) RETURN a.id, b.id, r.line, r.ref_kind"
		default:
			cypher = "MATCH (a:" + label + " {id: $nodeID})-[r:" + relType + "]-(b) RETURN a.id, b.id, r.line, r.ref_kind"
		}
		rows, err := store.queryWithParams(ctx, cypher, []cypherParam{{"nodeID", nodeID}})
		if err != nil {
			return nil, err
		}
		return parseUsesEdgeResults(rows), nil
	}

	returnCols := "a.id, b.id, r.confidence, r.line, r.flow_context, r.flow_line, r.declared_type, r.polymorphic, type(r)"
	var cypher string
	switch direction {
	case model.Outgoing:
		cypher = "MATCH (a:" + label + " {id: $nodeID})-[r:" + relType + "]->(b) RETURN " + returnCols
	case model.Incoming:
		cypher = "MATCH (a)-[r:" + relType + "]->(b {id: $nodeID}) RETURN " + returnCols
	default:
		cypher = "MATCH (a:" + label + " {id: $nodeID})-[r:" + relType + "]-(b) RETURN " + returnCols
	}

	rows, err := store.queryWithParams(ctx, cypher, []cypherParam{{"nodeID", nodeID}})
	if err != nil {
		return nil, err
	}

	return parseEdgeResults(rows, relKind), nil
}

// TraverseCallChain traverses CALLS relationships up to depth.
// QueryAllEdges returns all edges of a given relation kind in a single query.
func (store *Store) QueryAllEdges(ctx context.Context, relKind model.RelationKind, limit int) ([]model.Edge, error) {
	relType := mapRelationType(relKind)
	// Safe: relType is from hardcoded mapping, not user input.
	cypher := "MATCH (a)-[r:" + relType + "]->(b) RETURN a.id, b.id, r.confidence"
	if limit > 0 {
		cypher += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := store.query(ctx, cypher)
	if err != nil {
		return nil, err
	}

	if len(rows) < 2 {
		return nil, nil
	}
	dataRows, ok := rows[1].([]interface{})
	if !ok {
		return nil, nil
	}

	edges := make([]model.Edge, 0, len(dataRows))
	for _, row := range dataRows {
		rowSlice, ok := row.([]interface{})
		if !ok || len(rowSlice) < 2 {
			continue
		}
		edge := model.Edge{
			SourceID:   asString(rowSlice[0]),
			TargetID:   asString(rowSlice[1]),
			Kind:       relKind,
			Properties: make(map[string]any),
		}
		if len(rowSlice) >= 3 && rowSlice[2] != nil {
			if f, ok := asFloat(rowSlice[2]); ok {
				edge.Properties["confidence"] = f
			}
		}
		edges = append(edges, edge)
	}
	return edges, nil
}

// QueryNodesByIDs returns nodes matching any of the given IDs.
// Uses explicit column projection per kind to avoid RESP2 serializing properties() as a string.
func (store *Store) QueryNodesByIDs(ctx context.Context, ids []string) ([]model.Node, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var nodes []model.Node
	for _, kind := range []string{
		constants.KindFunction, constants.KindClass, constants.KindInterface,
		constants.KindRoute, constants.KindQueryNode, constants.KindAnnotation,
	} {
		returnClause := model.QueryReturnClause(kind)
		cypher := fmt.Sprintf("MATCH (n:%s) WHERE n.id IN $ids RETURN %s", kind, returnClause)
		rows, err := store.queryWithParams(ctx, cypher, []cypherParam{{"ids", ids}})
		if err != nil {
			continue
		}
		colNames := append([]string{"id"}, model.ColumnNames(kind)...)
		nodes = append(nodes, parseExplicitNodeResults(rows, kind, colNames)...)
	}
	return nodes, nil
}

// parseExplicitNodeResults parses node query results with explicit column projection.
// colNames[0] = "id", colNames[1..] = schema columns in QueryReturnClause order.
func parseExplicitNodeResults(rows []interface{}, kind string, colNames []string) []model.Node {
	if len(rows) < 2 {
		return nil
	}
	dataRows, ok := rows[1].([]interface{})
	if !ok {
		return nil
	}
	var nodes []model.Node
	for _, row := range dataRows {
		rowSlice, ok := row.([]interface{})
		if !ok || len(rowSlice) < len(colNames) {
			continue
		}
		id, _ := rowSlice[0].(string)
		if id == "" {
			continue
		}
		node := model.Node{ID: id, Kind: kind, Properties: make(map[string]any)}
		for i := 1; i < len(colNames) && i < len(rowSlice); i++ {
			if rowSlice[i] != nil {
				node.Properties[colNames[i]] = rowSlice[i]
			}
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// QueryEdgesByNodeIDs returns edges where source (Outgoing) or target (Incoming) is in nodeIDs.
func (store *Store) QueryEdgesByNodeIDs(ctx context.Context, nodeIDs []string, relKind model.RelationKind, dir model.Direction) ([]model.Edge, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	relType := mapRelationType(relKind)
	var cypher string
	switch dir {
	case model.Outgoing:
		cypher = fmt.Sprintf("MATCH (a)-[r:%s]->(b) WHERE a.id IN $nodeIDs RETURN a.id, b.id, type(r), properties(r)", relType)
	case model.Incoming:
		cypher = fmt.Sprintf("MATCH (a)-[r:%s]->(b) WHERE b.id IN $nodeIDs RETURN a.id, b.id, type(r), properties(r)", relType)
	default:
		cypher = fmt.Sprintf("MATCH (a)-[r:%s]-(b) WHERE a.id IN $nodeIDs RETURN a.id, b.id, type(r), properties(r)", relType)
	}
	rows, err := store.queryWithParams(ctx, cypher, []cypherParam{{"nodeIDs", nodeIDs}})
	if err != nil {
		return nil, err
	}
	return parseBatchEdgeResults(rows, relKind), nil
}

// parseGenericNodeResults parses node query results into model.Node slice.
func parseGenericNodeResults(rows []interface{}) []model.Node {
	if len(rows) < 2 {
		return nil
	}
	dataRows, ok := rows[1].([]interface{})
	if !ok {
		return nil
	}
	var nodes []model.Node
	for _, row := range dataRows {
		rowSlice, ok := row.([]interface{})
		if !ok || len(rowSlice) < 3 {
			continue
		}
		id, _ := rowSlice[0].(string)
		kind, _ := rowSlice[1].(string)
		props, _ := rowSlice[2].(map[string]interface{})
		if id == "" {
			continue
		}
		node := model.Node{ID: id, Kind: kind, Properties: make(map[string]any)}
		for k, v := range props {
			node.Properties[k] = v
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// parseBatchEdgeResults parses edge query results into model.Edge slice.
func parseBatchEdgeResults(rows []interface{}, defaultKind model.RelationKind) []model.Edge {
	if len(rows) < 2 {
		return nil
	}
	dataRows, ok := rows[1].([]interface{})
	if !ok {
		return nil
	}
	var edges []model.Edge
	for _, row := range dataRows {
		rowSlice, ok := row.([]interface{})
		if !ok || len(rowSlice) < 4 {
			continue
		}
		sourceID, _ := rowSlice[0].(string)
		targetID, _ := rowSlice[1].(string)
		relType, _ := rowSlice[2].(string)
		props, _ := rowSlice[3].(map[string]interface{})
		kind := defaultKind
		if relType != "" {
			kind = model.RelationKind(relType)
		}
		edge := model.Edge{SourceID: sourceID, TargetID: targetID, Kind: kind, Properties: make(map[string]any)}
		for k, v := range props {
			edge.Properties[k] = v
		}
		edges = append(edges, edge)
	}
	return edges
}

// TraverseCallChain traverses the call graph from nodeID up to the given depth
// using level-by-level BFS. Each level queries CALLS edges first, then queries
// DISPATCHES edges from the new CALLS targets and filters them by declared_type.
//
// The returned Nodes contain only reachable neighbors (callees/callers),
// NOT the root node itself.
func (store *Store) TraverseCallChain(ctx context.Context, nodeID string, depth int, direction model.Direction, minConfidence float64) (*model.Subgraph, error) {
	subgraph := &model.Subgraph{}
	visited := map[string]bool{nodeID: true}
	queue := []string{nodeID}

	confidenceFilter := ""
	if minConfidence > 0 {
		confidenceFilter = fmt.Sprintf(" AND (r.confidence IS NULL OR r.confidence >= %f)", minConfidence)
	}

	var callsCypher, dispatchesCypher string
	switch direction {
	case model.Outgoing:
		callsCypher = "MATCH (a:Function)-[r:CALLS]->(b:Function) WHERE a.id IN $ids" + confidenceFilter +
			" RETURN a.id, b.id, r.confidence, r.line, r.flow_context, r.flow_line, r.declared_type, r.polymorphic, 'CALLS', r.cross_service, r.via_route, r.target_project, r.target_handler," +
			" b.name, b.file_path, b.qualified_name, b.is_getter, b.is_setter, b.is_constructor, b.source_project, b.source_branch, b.start_line, b.end_line, r.resolved_by, r.event_type"
		dispatchesCypher = "MATCH (a:Function)-[r:DISPATCHES]->(b:Function) WHERE a.id IN $ids" +
			" RETURN a.id, b.id, r.confidence, NULL AS line, NULL AS flow_context, NULL AS flow_line, NULL AS declared_type, NULL AS polymorphic, 'DISPATCHES', NULL AS cross_service, NULL AS via_route, NULL AS target_project, NULL AS target_handler," +
			" b.name, b.file_path, b.qualified_name, b.is_getter, b.is_setter, b.is_constructor, b.source_project, b.source_branch, b.start_line, b.end_line, r.resolved_by, r.event_type"
	case model.Incoming:
		callsCypher = "MATCH (a:Function)-[r:CALLS]->(b:Function) WHERE b.id IN $ids" + confidenceFilter +
			" RETURN b.id, a.id, r.confidence, r.line, r.flow_context, r.flow_line, r.declared_type, r.polymorphic, 'CALLS', r.cross_service, r.via_route, r.target_project, r.target_handler," +
			" a.name, a.file_path, a.qualified_name, a.is_getter, a.is_setter, a.is_constructor, a.source_project, a.source_branch, a.start_line, a.end_line, r.resolved_by, r.event_type"
		dispatchesCypher = "MATCH (a:Function)-[r:DISPATCHES]->(b:Function) WHERE b.id IN $ids" +
			" RETURN b.id, a.id, r.confidence, NULL AS line, NULL AS flow_context, NULL AS flow_line, NULL AS declared_type, NULL AS polymorphic, 'DISPATCHES', NULL AS cross_service, NULL AS via_route, NULL AS target_project, NULL AS target_handler," +
			" a.name, a.file_path, a.qualified_name, a.is_getter, a.is_setter, a.is_constructor, a.source_project, a.source_branch, a.start_line, a.end_line, r.resolved_by, r.event_type"
	default:
		callsCypher = "MATCH (a:Function)-[r:CALLS]-(b:Function) WHERE a.id IN $ids" + confidenceFilter +
			" RETURN a.id, b.id, r.confidence, r.line, r.flow_context, r.flow_line, r.declared_type, r.polymorphic, 'CALLS', r.cross_service, r.via_route, r.target_project, r.target_handler," +
			" b.name, b.file_path, b.qualified_name, b.is_getter, b.is_setter, b.is_constructor, b.source_project, b.source_branch, b.start_line, b.end_line, r.resolved_by, r.event_type"
		dispatchesCypher = "MATCH (a:Function)-[r:DISPATCHES]-(b:Function) WHERE a.id IN $ids" +
			" RETURN a.id, b.id, r.confidence, NULL AS line, NULL AS flow_context, NULL AS flow_line, NULL AS declared_type, NULL AS polymorphic, 'DISPATCHES', NULL AS cross_service, NULL AS via_route, NULL AS target_project, NULL AS target_handler," +
			" b.name, b.file_path, b.qualified_name, b.is_getter, b.is_setter, b.is_constructor, b.source_project, b.source_branch, b.start_line, b.end_line, r.resolved_by, r.event_type"
	}

	for level := 0; level < depth && len(queue) > 0; level++ {
		// Step 1: Batch query CALLS edges from queue nodes
		callsRows, err := store.queryWithParams(ctx, callsCypher, []cypherParam{{"ids", queue}})
		if err != nil {
			return subgraph, err
		}

		var nextQueue []string
		// Track declared_type prefixes per CALLS target for DISPATCHES filtering
		declaredTypePrefixes := map[string]map[string]bool{}

		callsEdgesAndNodes := parseBFSResults(callsRows)
		for _, result := range callsEdgesAndNodes {
			subgraph.Edges = append(subgraph.Edges, result.edge)

			// Collect declared_type for the CALLS target
			if declaredType, ok := result.edge.Properties["declared_type"].(string); ok && declaredType != "" {
				neighborID := result.edge.TargetID
				if direction == model.Incoming {
					neighborID = result.edge.SourceID
				}
				if declaredTypePrefixes[neighborID] == nil {
					declaredTypePrefixes[neighborID] = map[string]bool{}
				}
				declaredTypePrefixes[neighborID][declaredType+"."] = true
			}

			if !visited[result.node.ID] {
				visited[result.node.ID] = true
				nextQueue = append(nextQueue, result.node.ID)
				subgraph.Nodes = append(subgraph.Nodes, result.node)
			}
		}

		// Step 2: Batch query DISPATCHES edges from new CALLS targets, filter by declared_type
		if len(nextQueue) > 0 {
			sourceQualifiedNameByID := map[string]string{}
			for _, node := range subgraph.Nodes {
				if qualifiedName, ok := node.Properties["qualified_name"].(string); ok {
					sourceQualifiedNameByID[node.ID] = qualifiedName
				}
			}

			dispatchRows, dispatchErr := store.queryWithParams(ctx, dispatchesCypher, []cypherParam{{"ids", nextQueue}})
			if dispatchErr == nil {
				dispatchEdgesAndNodes := parseBFSResults(dispatchRows)
				for _, result := range dispatchEdgesAndNodes {
					dispatchSourceID := result.edge.SourceID
					if direction == model.Incoming {
						dispatchSourceID = result.edge.TargetID
					}

					prefixes := declaredTypePrefixes[dispatchSourceID]
					if len(prefixes) > 0 {
						// When declared_type matches the source's own class, the caller
						// uses the interface/abstract type — keep all implementations
						sourceQualifiedName := sourceQualifiedNameByID[dispatchSourceID]
						declaredTypeMatchesSource := false
						for prefix := range prefixes {
							if strings.HasPrefix(sourceQualifiedName, prefix) {
								declaredTypeMatchesSource = true
								break
							}
						}
						if !declaredTypeMatchesSource {
							targetQualifiedName, _ := result.node.Properties["qualified_name"].(string)
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
					}

					subgraph.Edges = append(subgraph.Edges, result.edge)
					if !visited[result.node.ID] {
						visited[result.node.ID] = true
						nextQueue = append(nextQueue, result.node.ID)
						subgraph.Nodes = append(subgraph.Nodes, result.node)
					}
				}
			}
		}

		queue = nextQueue
	}

	// Detect truncated nodes: queue contains boundary nodes whose callees were not explored
	if len(queue) > 0 {
		allVisitedIDs := make([]string, 0, len(visited))
		for visitedID := range visited {
			allVisitedIDs = append(allVisitedIDs, visitedID)
		}
		subgraph.TruncatedNodes = store.queryTruncatedNodes(ctx, queue, allVisitedIDs, direction)
	}

	return subgraph, nil
}

// bfsEdgeNodeResult holds a paired edge and its neighbor node from a BFS query row.
type bfsEdgeNodeResult struct {
	edge model.Edge
	node model.Node
}

// parseBFSResults parses rows from BFS queries that return both edge and node columns.
// Expected column layout: [0-12] edge columns (same as parseEdgeResults), [13-22] node columns.
func parseBFSResults(rows []interface{}) []bfsEdgeNodeResult {
	if len(rows) < 2 {
		return nil
	}
	dataRows, ok := rows[1].([]interface{})
	if !ok {
		return nil
	}
	results := make([]bfsEdgeNodeResult, 0, len(dataRows))
	for _, row := range dataRows {
		cols, ok := row.([]interface{})
		if !ok || len(cols) < 16 {
			continue
		}

		// Parse edge (columns 0-12) — reuse parseEdgeResults layout
		edgeKind := model.RelCalls
		if len(cols) >= 9 && cols[8] != nil {
			if typeName, ok := cols[8].(string); ok {
				edgeKind = model.RelationKind(typeName)
			}
		}
		edge := model.Edge{
			SourceID:   asString(cols[0]),
			TargetID:   asString(cols[1]),
			Kind:       edgeKind,
			Properties: make(map[string]any),
		}
		if cols[2] != nil {
			if confidence, ok := asFloat(cols[2]); ok {
				edge.Properties["confidence"] = confidence
			}
		}
		if cols[3] != nil {
			if lineNumber, ok := asInt(cols[3]); ok {
				edge.Properties["line"] = lineNumber
			}
		}
		if cols[4] != nil {
			if flowContext, ok := cols[4].(string); ok && flowContext != "" {
				edge.Properties["flow_context"] = flowContext
			}
		}
		if cols[5] != nil {
			if flowLineNumber, ok := asInt(cols[5]); ok {
				edge.Properties["flow_line"] = flowLineNumber
			}
		}
		if cols[6] != nil {
			if declaredType, ok := cols[6].(string); ok && declaredType != "" {
				edge.Properties["declared_type"] = declaredType
			}
		}
		if cols[7] != nil {
			switch polymorphicValue := cols[7].(type) {
			case bool:
				if polymorphicValue {
					edge.Properties["polymorphic"] = true
				}
			case string:
				if polymorphicValue == "true" {
					edge.Properties["polymorphic"] = true
				}
			}
		}
		if len(cols) >= 10 && cols[9] != nil {
			switch crossServiceValue := cols[9].(type) {
			case bool:
				edge.Properties["cross_service"] = crossServiceValue
			case string:
				edge.Properties["cross_service"] = crossServiceValue == "true"
			}
		}
		if len(cols) >= 11 && cols[10] != nil {
			if viaRoute, ok := cols[10].(string); ok && viaRoute != "" {
				edge.Properties["via_route"] = viaRoute
			}
		}
		if len(cols) >= 12 && cols[11] != nil {
			if targetProject, ok := cols[11].(string); ok && targetProject != "" {
				edge.Properties["target_project"] = targetProject
			}
		}
		if len(cols) >= 13 && cols[12] != nil {
			if targetHandler, ok := cols[12].(string); ok && targetHandler != "" {
				edge.Properties["target_handler"] = targetHandler
			}
		}

		// Parse neighbor node (columns 13-22)
		neighborID := edge.TargetID
		if edge.SourceID != "" && edge.TargetID != "" {
			// For incoming direction, the neighbor is the source
			// But parseBFSResults always gets the neighbor in cols[1] position
			// because the Cypher query is structured to return (anchor, neighbor, ...)
			neighborID = asString(cols[1])
		}
		nodeProperties := map[string]any{
			"name":      cols[13],
			"file_path": cols[14],
		}
		if cols[15] != nil {
			nodeProperties["qualified_name"] = cols[15]
		}
		if len(cols) > 16 && cols[16] != nil {
			nodeProperties["is_getter"] = convertByType(cols[16], "BOOLEAN")
		}
		if len(cols) > 17 && cols[17] != nil {
			nodeProperties["is_setter"] = convertByType(cols[17], "BOOLEAN")
		}
		if len(cols) > 18 && cols[18] != nil {
			nodeProperties["is_constructor"] = convertByType(cols[18], "BOOLEAN")
		}
		if len(cols) > 19 && cols[19] != nil {
			nodeProperties["source_project"] = cols[19]
		}
		if len(cols) > 20 && cols[20] != nil {
			nodeProperties["source_branch"] = cols[20]
		}
		if len(cols) > 21 && cols[21] != nil {
			nodeProperties["start_line"] = cols[21]
		}
		if len(cols) > 22 && cols[22] != nil {
			nodeProperties["end_line"] = cols[22]
		}

		// Parse resolved_by and event_type (columns 23-24, appended after node columns)
		if len(cols) > 23 && cols[23] != nil {
			if resolvedBy, ok := cols[23].(string); ok && resolvedBy != "" {
				edge.Properties["resolved_by"] = resolvedBy
			}
		}
		if len(cols) > 24 && cols[24] != nil {
			if eventType, ok := cols[24].(string); ok && eventType != "" {
				edge.Properties["event_type"] = eventType
			}
		}

		results = append(results, bfsEdgeNodeResult{
			edge: edge,
			node: model.Node{
				ID:         neighborID,
				Kind:       constants.KindFunction,
				Properties: nodeProperties,
			},
		})
	}
	return results
}

// TraverseImpact finds all nodes affected by changes to a node.
func (store *Store) TraverseImpact(ctx context.Context, nodeID string, depth int) (*model.Subgraph, error) {
	return store.TraverseCallChain(ctx, nodeID, depth, model.Incoming, 0)
}

// queryTruncatedNodes queries boundary nodes to find how many callees are not expanded.
func (store *Store) queryTruncatedNodes(ctx context.Context, boundaryIDs []string, existingIDs []string, direction model.Direction) []string {
	var cypher string
	switch direction {
	case model.Incoming:
		cypher = "MATCH (a:Function)-[:CALLS|DISPATCHES]->(b:Function) WHERE b.id IN $boundaryIDs AND NOT a.id IN $existingIDs RETURN b.id, b.qualified_name, count(a)"
	default:
		cypher = "MATCH (a:Function)-[:CALLS|DISPATCHES]->(b:Function) WHERE a.id IN $boundaryIDs AND NOT b.id IN $existingIDs RETURN a.id, a.qualified_name, count(b)"
	}
	rows, err := store.queryWithParams(ctx, cypher, []cypherParam{
		{"boundaryIDs", boundaryIDs},
		{"existingIDs", existingIDs},
	})
	if err != nil {
		return nil
	}
	var truncated []string
	if len(rows) < 2 {
		return nil
	}
	dataRows, ok := rows[1].([]interface{})
	if !ok {
		return nil
	}
	for _, row := range dataRows {
		cols, ok := row.([]interface{})
		if !ok || len(cols) < 3 {
			continue
		}
		qualifiedName, _ := cols[1].(string)
		count := 0
		if n, ok := model.ToInt(cols[2]); ok {
			count = n
		}
		if count > 0 && qualifiedName != "" {
			truncated = append(truncated, fmt.Sprintf("%s (%d direct callees not expanded)", qualifiedName, count))
		}
	}
	return truncated
}


// BatchUpdateNodeProperties updates properties on multiple nodes using UNWIND.
func (store *Store) BatchUpdateNodeProperties(ctx context.Context, kind string, updates []storage.PropertyUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	const batchSize = 5000
	pipe := store.client.Pipeline()
	count := 0
	for _, u := range updates {
		keys := sortedKeys(u.Props)
		params := []cypherParam{{"nodeID", u.NodeID}}
		var setParts []string
		for i, k := range keys {
			paramName := fmt.Sprintf("pv_%d", i)
			setParts = append(setParts, "n."+k+" = $"+paramName)
			params = append(params, cypherParam{paramName, u.Props[k]})
		}
		cypher := "MATCH (n:" + kind + " {id: $nodeID}) SET " + strings.Join(setParts, ", ")
		pipe.Do(ctx, "GRAPH.QUERY", store.graphName, buildParamsHeader(params)+cypher)
		count++
		if count%batchSize == 0 {
			if _, err := pipe.Exec(ctx); err != nil {
				return err
			}
			pipe = store.client.Pipeline()
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	return nil
}

// QueryNodesByFile returns Function, Class, and Interface nodes in a file.
func (store *Store) QueryNodesByFile(ctx context.Context, filePath string) ([]model.Node, error) {
	var parts []string
	for _, label := range constants.BaseSymbolKinds {
		parts = append(parts,
			"MATCH (n:"+label+") WHERE n.file_path = $filePath AND n.start_line IS NOT NULL AND n.end_line IS NOT NULL RETURN n.id, labels(n)[0], n.qualified_name, n.start_line, n.end_line")
	}
	cypher := strings.Join(parts, " UNION ")
	rows, err := store.queryWithParams(ctx, cypher, []cypherParam{{"filePath", filePath}})
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
	// Safe: kind and returnClause are from hardcoded constants.
	cypher := "MATCH (n:" + kind + ") RETURN " + returnClause + limitClause
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

	nodes := make([]model.Node, 0, len(dataRows))
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
				nodeID = asString(val)
			} else if val != nil {
				nodeProps[colNames[i]] = convertByType(val, model.GetColumnType(kind, colNames[i]))
			}
		}
		nodes = append(nodes, model.Node{ID: nodeID, Kind: kind, Properties: nodeProps})
	}
	return nodes, nil
}

// QueryNodesByProperty returns nodes of a specific kind where the given property matches the value.
// matchMode: "exact" for equality, "contains" for substring match.
func (store *Store) QueryNodesByProperty(ctx context.Context, kind string, key string, value string, matchMode string, limit int) ([]model.Node, error) {
	returnClause := model.QueryReturnClause(kind)
	var whereClause string
	var params []cypherParam
	switch matchMode {
	case storage.MatchContains:
		whereClause = fmt.Sprintf("WHERE n.%s CONTAINS $propertyValue", key)
		params = []cypherParam{{"propertyValue", value}}
	case storage.MatchNotEmpty:
		whereClause = fmt.Sprintf("WHERE n.%s IS NOT NULL AND n.%s <> ''", key, key)
	default: // exact
		whereClause = fmt.Sprintf("WHERE n.%s = $propertyValue", key)
		params = []cypherParam{{"propertyValue", value}}
	}
	limitClause := ""
	if limit > 0 {
		limitClause = fmt.Sprintf(" LIMIT %d", limit)
	}
	cypher := "MATCH (n:" + kind + ") " + whereClause + " RETURN " + returnClause + limitClause
	rows, err := store.queryWithParams(ctx, cypher, params)
	if err != nil {
		return nil, err
	}

	if len(rows) < 2 {
		return nil, nil
	}

	headerRow, ok := rows[0].([]interface{})
	if !ok {
		return parseSimpleNodeResults(rows), nil
	}
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
		return nil, nil
	}

	nodes := make([]model.Node, 0, len(dataRows))
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
				nodeID = asString(val)
			} else if val != nil {
				nodeProps[colNames[i]] = convertByType(val, model.GetColumnType(kind, colNames[i]))
			}
		}
		nodes = append(nodes, model.Node{ID: nodeID, Kind: kind, Properties: nodeProps})
	}
	return nodes, nil
}

// SearchFTS performs full-text search on node names, qualified names, class/interface fields, and annotation params.
func (store *Store) SearchFTS(ctx context.Context, queryText string, limit int) ([]storage.SearchResult, error) {
	// Phase 1: Function / Class / Interface (name + qualified_name) and Annotation (params) via UNION.
	var unionParts []string
	for _, label := range constants.QualifiedNameKinds {
		var startLineExpr, endLineExpr string
		if label == constants.KindVariable {
			startLineExpr = "n.line AS start_line"
			endLineExpr = "n.line AS end_line"
		} else {
			startLineExpr = "n.start_line AS start_line"
			endLineExpr = "n.end_line AS end_line"
		}
		returnCols := fmt.Sprintf("n.id AS id, '%s' AS kind, n.name AS name, n.file_path AS file_path, n.qualified_name AS qualified_name, %s, %s", label, startLineExpr, endLineExpr)
		unionParts = append(unionParts,
			"MATCH (n:"+label+") WHERE n.name CONTAINS $searchText RETURN "+returnCols)
		unionParts = append(unionParts,
			"MATCH (n:"+label+") WHERE n.qualified_name CONTAINS $searchText RETURN "+returnCols)
	}
	unionParts = append(unionParts,
		"MATCH (n:Annotation) WHERE n.params CONTAINS $searchText "+
			"RETURN n.id AS id, 'Annotation' AS kind, n.name AS name, n.file_path AS file_path, '' AS qualified_name, n.line AS start_line, n.line AS end_line")

	unionCypher := strings.Join(unionParts, " UNION ")
	if limit > 0 {
		unionCypher += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := store.queryWithParams(ctx, unionCypher, []cypherParam{{"searchText", queryText}})
	if err != nil {
		return nil, err
	}
	results := parseSearchResults(rows)

	// Phase 2: Class / Interface fields JSON attribute search.
	fieldResults, _ := store.searchClassAndInterfaceFields(ctx, queryText, limit)
	results = append(results, fieldResults...)
	return results, nil
}

// searchClassAndInterfaceFields searches the fields JSON attribute of Class and Interface nodes.
// It returns one SearchResult per field whose name or type contains queryText.
func (store *Store) searchClassAndInterfaceFields(ctx context.Context, queryText string, limit int) ([]storage.SearchResult, error) {
	var results []storage.SearchResult
	for _, nodeKind := range []string{"Class", "Interface"} {
		limitClause := ""
		if limit > 0 {
			limitClause = fmt.Sprintf(" LIMIT %d", limit)
		}
		cypher := fmt.Sprintf(
			"MATCH (n:%s) WHERE n.fields CONTAINS $searchText "+
				"RETURN n.id, n.file_path, n.qualified_name, n.start_line, n.end_line, n.fields%s",
			nodeKind, limitClause)
		rows, err := store.queryWithParams(ctx, cypher, []cypherParam{{"searchText", queryText}})
		if err != nil {
			continue
		}
		dataRows, ok := rows[1].([]interface{})
		if !ok {
			continue
		}
		for _, rawRow := range dataRows {
			cols, ok := rawRow.([]interface{})
			if !ok || len(cols) < 6 {
				continue
			}
			classNodeID := asString(cols[0])
			classFilePath := asString(cols[1])
			classQualifiedName := asString(cols[2])
			classStartLine, _ := asInt(cols[3])
			classEndLine, _ := asInt(cols[4])
			fieldsJSON := asString(cols[5])
			results = append(results,
				extractFieldResults(classNodeID, classQualifiedName, classFilePath, classStartLine, classEndLine, fieldsJSON, queryText)...)
		}
	}
	return results, nil
}

// extractFieldResults parses the fields JSON of a Class/Interface node and returns one SearchResult
// per field whose name or type contains queryText.
// startLine and endLine are the enclosing class/interface node's lines (field-level line is not stored).
func extractFieldResults(classNodeID, classQualifiedName, classFilePath string, startLine, endLine int, fieldsJSON, queryText string) []storage.SearchResult {
	if fieldsJSON == "" || fieldsJSON == "null" || fieldsJSON == "[]" {
		return nil
	}
	var fields []model.FieldInfo
	if err := json.Unmarshal([]byte(fieldsJSON), &fields); err != nil {
		return nil
	}
	var results []storage.SearchResult
	for _, field := range fields {
		if strings.Contains(field.Name, queryText) || strings.Contains(field.Type, queryText) {
			results = append(results, storage.SearchResult{
				NodeID:        classNodeID + "::field::" + field.Name,
				Name:          field.Name,
				Kind:          "Field",
				Path:          classFilePath,
				QualifiedName: classQualifiedName + "." + field.Name,
				StartLine:     startLine,
				EndLine:       endLine,
			})
		}
	}
	return results
}

// GetStats returns aggregate statistics.
func (store *Store) GetStats(ctx context.Context) (*model.GraphStats, error) {
	stats := &model.GraphStats{
		NodesByKind: make(map[string]int),
		EdgesByKind: make(map[string]int),
		FilesByLang: make(map[string]int),
	}

	// Safe: label and rel names are from hardcoded constants, not user input.
	for _, label := range constants.AllNodeKinds {
		rows, err := store.query(ctx, "MATCH (n:"+label+") RETURN count(n)")
		if err != nil {
			continue
		}
		count := parseSingleInt(rows)
		if count > 0 {
			stats.NodesByKind[label] = count
			stats.NodeCount += count
		}
	}
	stats.FileCount = stats.NodesByKind[constants.KindFile]

	// Edge counts
	edgeTypes := []string{"CALLS", "EXTENDS", "IMPLEMENTS", "OVERRIDES", "IMPORTS",
		"HANDLES", "EXECUTES", "REMOTE_CALLS", "CONTAINS", "HAS_ANNOTATION", "STEP", "UNRESOLVED_CALL", "USES"}
	for _, rel := range edgeTypes {
		rows, err := store.query(ctx, "MATCH ()-[r:"+rel+"]->() RETURN count(r)")
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

	// Frameworks from Repository node
	fwRows, err := store.query(ctx, "MATCH (n:Repository) RETURN n.frameworks LIMIT 1")
	if err == nil && len(fwRows) >= 2 {
		if dataRows, ok := fwRows[1].([]interface{}); ok && len(dataRows) > 0 {
			if arr, ok := dataRows[0].([]interface{}); ok && len(arr) > 0 {
				if fwStr, ok := arr[0].(string); ok && fwStr != "" {
					stats.Frameworks = strings.Split(fwStr, ",")
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

// convertByType converts a FalkorDB result value to the proper Go type based on schema column type.
func convertByType(val interface{}, colType string) interface{} {
	switch colType {
	case "BOOLEAN":
		switch b := val.(type) {
		case bool:
			return b
		case string:
			return b == "true"
		}
		return false
	default:
		return val
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
	nodes := make([]model.Node, 0, len(dataRows))
	for _, row := range dataRows {
		cols, ok := row.([]interface{})
		if !ok || len(cols) < 4 {
			continue
		}
		props := map[string]interface{}{
			"name":           cols[1],
			"file_path":      cols[2],
			"qualified_name": cols[3],
		}
		if len(cols) > 4 && cols[4] != nil {
			props["is_getter"] = convertByType(cols[4], "BOOLEAN")
		}
		if len(cols) > 5 && cols[5] != nil {
			props["is_setter"] = convertByType(cols[5], "BOOLEAN")
		}
		if len(cols) > 6 && cols[6] != nil {
			props["is_constructor"] = convertByType(cols[6], "BOOLEAN")
		}
		if len(cols) > 7 && cols[7] != nil {
			props["source_project"] = cols[7]
		}
		if len(cols) > 8 && cols[8] != nil {
			props["source_branch"] = cols[8]
		}
		if len(cols) > 9 && cols[9] != nil {
			props["start_line"] = cols[9]
		}
		if len(cols) > 10 && cols[10] != nil {
			props["end_line"] = cols[10]
		}
		nodes = append(nodes, model.Node{
			ID:         asString(cols[0]),
			Kind:       "Function",
			Properties: props,
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
	nodes := make([]model.Node, 0, len(dataRows))
	for _, row := range dataRows {
		cols, ok := row.([]interface{})
		if !ok || len(cols) < 4 {
			continue
		}
		node := model.Node{
			ID:   asString(cols[0]),
			Kind: asString(cols[1]),
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
	nodes := make([]model.Node, 0, len(dataRows))
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
				nodeID = asString(val)
			default:
				if strings.Contains(colNames[i], "labels(") {
					kind = asString(val)
				} else if val != nil {
					props[colNames[i]] = val
				}
			}
		}
		nodes = append(nodes, model.Node{ID: nodeID, Kind: kind, Properties: props})
	}
	return nodes
}



func parseUsesEdgeResults(rows []interface{}) []model.Edge {
	if len(rows) < 2 {
		return nil
	}
	dataRows, ok := rows[1].([]interface{})
	if !ok {
		return nil
	}
	edges := make([]model.Edge, 0, len(dataRows))
	for _, row := range dataRows {
		cols, ok := row.([]interface{})
		if !ok || len(cols) < 2 {
			continue
		}
		edge := model.Edge{
			SourceID:   asString(cols[0]),
			TargetID:   asString(cols[1]),
			Kind:       model.RelUses,
			Properties: make(map[string]any),
		}
		if len(cols) >= 3 && cols[2] != nil {
			if l, ok := asInt(cols[2]); ok {
				edge.Properties["line"] = l
			}
		}
		if len(cols) >= 4 && cols[3] != nil {
			if rk, ok := cols[3].(string); ok {
				edge.Properties["ref_kind"] = rk
			}
		}
		edges = append(edges, edge)
	}
	return edges
}

func parseEdgeResults(rows []interface{}, defaultKind ...model.RelationKind) []model.Edge {
	if len(rows) < 2 {
		return nil
	}
	dataRows, ok := rows[1].([]interface{})
	if !ok {
		return nil
	}
	edges := make([]model.Edge, 0, len(dataRows))
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
			SourceID:   asString(cols[0]),
			TargetID:   asString(cols[1]),
			Kind:       edgeKind,
			Properties: make(map[string]any),
		}
		if len(cols) >= 3 && cols[2] != nil {
			if f, ok := asFloat(cols[2]); ok {
				edge.Properties["confidence"] = f
			}
		}
		if len(cols) >= 4 && cols[3] != nil {
			if l, ok := asInt(cols[3]); ok {
				edge.Properties["line"] = l
			}
		}
		if len(cols) >= 5 && cols[4] != nil {
			if fc, ok := cols[4].(string); ok && fc != "" {
				edge.Properties["flow_context"] = fc
			}
		}
		if len(cols) >= 6 && cols[5] != nil {
			if l, ok := asInt(cols[5]); ok {
				edge.Properties["flow_line"] = l
			}
		}
		if len(cols) >= 7 && cols[6] != nil {
			if dt, ok := cols[6].(string); ok && dt != "" {
				edge.Properties["declared_type"] = dt
			}
		}
		if len(cols) >= 8 && cols[7] != nil {
			switch polymorphicValue := cols[7].(type) {
			case bool:
				if polymorphicValue {
					edge.Properties["polymorphic"] = true
				}
			case string:
				if polymorphicValue == "true" {
					edge.Properties["polymorphic"] = true
				}
			}
		}
		// Cross-service properties (cols 9-12)
		if len(cols) >= 10 && cols[9] != nil {
			switch crossServiceValue := cols[9].(type) {
			case bool:
				edge.Properties["cross_service"] = crossServiceValue
			case string:
				edge.Properties["cross_service"] = crossServiceValue == "true"
			}
		}
		if len(cols) >= 11 && cols[10] != nil {
			if viaRoute, ok := cols[10].(string); ok && viaRoute != "" {
				edge.Properties["via_route"] = viaRoute
			}
		}
		if len(cols) >= 12 && cols[11] != nil {
			if targetProject, ok := cols[11].(string); ok && targetProject != "" {
				edge.Properties["target_project"] = targetProject
			}
		}
		if len(cols) >= 13 && cols[12] != nil {
			if targetHandler, ok := cols[12].(string); ok && targetHandler != "" {
				edge.Properties["target_handler"] = targetHandler
			}
		}
		// Cross-service properties (cols 9-12)
		if len(cols) >= 10 && cols[9] != nil {
			switch cs := cols[9].(type) {
			case bool:
				edge.Properties["cross_service"] = cs
			case string:
				edge.Properties["cross_service"] = cs == "true"
			}
		}
		if len(cols) >= 11 && cols[10] != nil {
			if vr, ok := cols[10].(string); ok && vr != "" {
				edge.Properties["via_route"] = vr
			}
		}
		if len(cols) >= 12 && cols[11] != nil {
			if tp, ok := cols[11].(string); ok && tp != "" {
				edge.Properties["target_project"] = tp
			}
		}
		if len(cols) >= 13 && cols[12] != nil {
			if th, ok := cols[12].(string); ok && th != "" {
				edge.Properties["target_handler"] = th
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
	results := make([]storage.SearchResult, 0, len(dataRows))
	for _, row := range dataRows {
		cols, ok := row.([]interface{})
		if !ok || len(cols) < 4 {
			continue
		}
		result := storage.SearchResult{
			NodeID:        asString(cols[0]),
			Kind:          asString(cols[1]),
			Name:          asString(cols[2]),
			Path:          asString(safeIndex(cols, 3)),
			QualifiedName: asString(safeIndex(cols, 4)),
		}
		if len(cols) > 5 && cols[5] != nil {
			if startLine, ok := asInt(cols[5]); ok {
				result.StartLine = startLine
			}
		}
		if len(cols) > 6 && cols[6] != nil {
			if endLine, ok := asInt(cols[6]); ok {
				result.EndLine = endLine
			}
		}
		results = append(results, result)
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

// asString converts an interface{} to string with type assertion fast path.
// Avoids fmt.Sprint reflection overhead for the common case where the value is already a string.
func asString(value interface{}) string {
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprint(value)
}

// asFloat extracts a float64 from an interface{} that may be float64 or numeric string.
func asFloat(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int64:
		return float64(v), true
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// asInt extracts an int from an interface{} that may be int64, float64, or numeric string.
func asInt(value interface{}) (int, bool) {
	switch v := value.(type) {
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i, true
		}
	}
	return 0, false
}
