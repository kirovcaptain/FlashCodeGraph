# FlashCodeGraph Architecture

## 1. Overview

FlashCodeGraph (FCG) is a code knowledge graph tool that parses source code into nodes and edges in a graph database, enabling call chain tracing, impact analysis, entry point detection, and other code intelligence queries.

- Tech stack: Go 1.23 + Tree-sitter (AST parsing) + FalkorDB / KùzuDB (graph storage)
- Interfaces: CLI (cobra) and MCP Server (AI Agent integration via stdio)
- Build artifact: single binary `bin/fcg`

## 2. Language Support

### Fully Supported (Dedicated Parser + Resolver)

| Language | Parser (AST Extraction) | Resolver (Relation Resolution) | Framework Detection |
|----------|------------------------|-------------------------------|-------------------|
| Java | `parser/java/` | `resolver/java/` | Spring, MyBatis, Hibernate, Feign, Dubbo, gRPC |
| Go | `parser/golang/` | `resolver/golang/` | Gin, Echo, Fiber, gRPC |
| Python | `parser/python/` | `resolver/python/` | FastAPI, Django, Flask, Requests, Httpx, Strawberry |
| TypeScript / JavaScript | `parser/typescript/` | `resolver/typescript/` | Express, NestJS, Axios |

### Planned (Tree-sitter Grammar Imported, Dedicated Extractor / Resolver Pending)

Rust, C, C++, C#, Ruby, PHP

### Supplementary File Support

| File Type | Purpose |
|-----------|---------|
| XML | MyBatis mapper → SQL extraction (`defparser/mybatis.go`) |
| GraphQL | Schema definition extraction (`defparser/graphql.go`) |

## 3. Directory Structure

```
cmd/fcg/                  → Entry point (main.go)
internal/
├── gateway/              → Gateway layer
│   ├── cli/              →   CLI commands (cobra), grouped: index/query/manage/agent
│   └── mcp/              →   MCP Server (AI Agent stdio protocol)
├── service/              → Service orchestration layer
│   ├── indexer.go        →   Indexing pipeline orchestration (Phase 0-8)
│   ├── querier.go        →   Graph query orchestration (symbol/call chain/route chain/annotation/impact)
│   ├── analyzer.go       →   Graph analysis orchestration (entry point detection, process tracing)
│   ├── staleness.go      →   Incremental indexing change detection
│   ├── progress.go       →   Progress callback management
│   ├── dump.go           →   Debug data export (CSV)
│   └── gitignore.go      →   .gitignore rule handling
├── core/                 → Core algorithm layer
│   ├── scanner/          →   Project scanning (detect project type, submodules, source files)
│   ├── parser/           →   Tree-sitter AST parsing → ParseResult
│   │   ├── parser.go     →     Parse dispatch + AST caching
│   │   ├── astutil/      →     AST utility functions
│   │   ├── sqlutil/      →     SQL extraction utilities
│   │   ├── urlutil/      →     URL extraction utilities
│   │   ├── java/         →     Java extractor (symbols/calls/routes/ORM/remote calls/MyBatis)
│   │   ├── golang/       →     Go extractor
│   │   ├── python/       →     Python extractor
│   │   ├── typescript/   →     TypeScript/JS extractor
│   │   └── graphql/      →     GraphQL schema extractor
│   ├── resolver/         →   Relation resolution (RawCall → ResolvedRelation)
│   │   ├── resolver.go   →     Core resolution engine (multi-strategy matching + confidence scoring)
│   │   ├── symbol_table.go →   Symbol table (indexed by name/qualified name/file)
│   │   ├── heritage.go   →     Inheritance/implementation resolution + MRO
│   │   ├── exports.go    →     Exported symbol resolution
│   │   ├── route_matcher.go →  HTTP route matching
│   │   ├── grpc_matcher.go →   gRPC service matching
│   │   ├── lang.go       →     LanguageHelper interface definition
│   │   ├── java/         →     Java language helper (JDK methods, type inference)
│   │   ├── golang/       →     Go language helper
│   │   ├── python/       →     Python language helper
│   │   └── typescript/   →     TypeScript/JS language helper
│   ├── typeinfer/        →   Type inference (Tier 0-2, variable → type mapping)
│   ├── annotation/       →   Annotation whitelist + default annotation definitions
│   ├── framework/        →   Framework detection (dependency files → framework list)
│   ├── defparser/        →   Definition file parse management (MyBatis XML, GraphQL schema)
│   ├── community/        →   Community detection (reserved)
│   ├── embedder/         →   Vector embedding (reserved)
│   ├── entry/            →   Entry point detection (reserved)
│   └── process/          →   Process tracing (reserved)
├── storage/              → Storage abstraction layer
│   ├── storage.go        →   GraphStore / FingerprintStore / IndexLock / VectorStore interfaces
│   ├── factory.go        →   Storage address resolution
│   ├── registry.go       →   Project registry (~/.fcg/registry.json)
│   ├── fingerprint.go    →   File fingerprint persistence (incremental indexing)
│   ├── branch/           →   Git branch management
│   ├── lock/             →   Index lock (remote mode mutual exclusion)
│   ├── falkor/           →   FalkorDB implementation
│   ├── kuzu/             →   KùzuDB implementation
│   └── vector/           →   Vector storage (reserved)
├── model/                → Shared data models
│   ├── graph.go          →   Node / Edge / Subgraph / RelationKind / GraphStats
│   ├── symbol.go         →   Symbol (code symbol extracted by parser)
│   ├── parse.go          →   ParseResult / RawCall / RawImport / RawHeritage / RawRoute / ...
│   ├── resolve.go        →   TypeEnv / ResolvedRelation / UnresolvedHint
│   ├── schema.go         →   NodeColumns definition (drives storage table creation and queries)
│   ├── index.go          →   IndexResult / ProgressEvent
│   ├── errors.go         →   Error definitions
│   └── permission.go     →   File permission constants
├── config/               → Configuration management
│   ├── config.go         →   Two-level TOML config (global ~/.fcg/ + project .fcg/)
│   └── environment.go    →   Storage backend auto-detection
├── constants/            → Constant definitions
│   ├── symbol.go         →   Node Kind (Function/Class/Interface/Route/...)
│   ├── language.go       →   Language identifiers
│   ├── framework.go      →   Framework names
│   ├── confidence.go     →   Remote call confidence scores
│   ├── file_category.go  →   File categories (source/query_def/schema_def)
│   └── category.go       →   Framework categories (route/http_client/rpc/orm/graphql)
└── status/               → Status tracking
    └── status.go         →   Index/analyze timestamps (.fcg/status.json)
```

## 4. Layered Architecture

```
┌─────────────────────────────────────────────┐
│                Gateway Layer                 │
│         CLI (cobra)  │  MCP Server (stdio)   │
└──────────┬───────────┴───────────┬───────────┘
           │                       │
┌──────────▼───────────────────────▼───────────┐
│               Service Layer                   │
│    Indexer    │    Querier    │    Analyzer    │
└──────┬───────┴───────┬───────┴───────┬───────┘
       │               │               │
┌──────▼───────────────▼───────────────▼───────┐
│                Core Layer                     │
│  Scanner │ Parser │ Resolver │ TypeInfer │ ...│
└──────────────────────┬───────────────────────┘
                       │
┌──────────────────────▼───────────────────────┐
│              Storage Layer                    │
│     FalkorDB    │    KùzuDB    │    Neo4j     │
└──────────────────────────────────────────────┘
```

Dependency rule: upper layers depend on lower layers; no cross-dependencies within the same layer. Gateway only calls Service, Service orchestrates Core, Core reads/writes graph data through Storage interfaces.

## 5. Graph Data Model

### 5.1 Node Types (13)

| Node Kind | Description | Key Properties |
|-----------|-------------|---------------|
| Function | Function / method | name, qualified_name, file_path, params, return_types, visibility, complexity, annotations |
| Class | Class / abstract class / enum / struct | name, qualified_name, file_path, class_type, is_abstract, annotations |
| Interface | Interface | name, qualified_name, file_path, annotations |
| Variable | Variable / constant | name, file_path, var_type, visibility |
| File | Source file | path, language, size, content_hash |
| Directory | Directory | path |
| Repository | Repository | name, path, branch, project_type |
| Route | HTTP route | method, path_pattern, handler_method, framework |
| QueryNode | Database query | sql_text, query_type, tables, caller |
| Annotation | Annotation / decorator | name, category, layer, framework, params |
| Community | Community (module cluster) | name, description, member_count, cohesion_score |
| Process | Execution process | name, entry_point, step_count, entry_type |
| ExternalService | External service | name, discovered_by |

### 5.2 Edge Types (19)

| Edge Kind | Direction | Description |
|-----------|-----------|-------------|
| CALLS | Function → Function | Function call (with confidence score) |
| EXTENDS | Class → Class | Class inheritance |
| IMPLEMENTS | Class → Interface | Interface implementation |
| IMPORTS | File → File/Module | Import dependency |
| OVERRIDES | Function → Function | Method override |
| DISPATCHES | Function → Function | Polymorphic dispatch (interface → implementation) |
| CONTAINS | File → Class/Function | File contains symbol |
| MEMBER_OF | Function → Class | Method belongs to class |
| HANDLES | Function → Route | Function handles route |
| INJECTS | Class → Class | Dependency injection |
| DEPENDS_ON | Module → Module | Module dependency |
| REMOTE_CALLS_ROUTE | Function → Route | Remote call matched to route |
| REMOTE_CALLS_EXT | Function → ExternalService | Remote call to external service |
| EXECUTES | Function → QueryNode | Executes database query |
| FETCHES | Function → ExternalService | Data fetching |
| MIDDLEWARE | Route → Function | Middleware association |
| STEP | Process → Function | Process step |
| HAS_ANNOTATION | Function/Class → Annotation | Has annotation |
| UNRESOLVED_CALL | Function → Function | Unresolved call (hint) |

## 6. Storage Backends

Unified behind the `GraphStore` interface, backend is selected at runtime via configuration:

| Backend | Type | Status | Use Case | Config |
|---------|------|--------|----------|--------|
| FalkorDB | Remote (Redis protocol) | ✅ Implemented | Default, team sharing | `storage.database = "falkordb"` |
| KùzuDB | Local embedded | ✅ Implemented | No external dependencies, single machine | `storage.database = "kuzu"` |
| Neo4j | Remote | 🚧 Planned | Existing Neo4j infrastructure | `storage.database = "neo4j"` |

Configuration hierarchy: global `~/.fcg/config.toml` → project `.fcg/config.toml` (project overrides global).

Project registry `~/.fcg/registry.json` tracks all indexed projects with their paths, storage backends, and branch info.

## 7. Core Workflows

> To be documented after review.

### 7.1 Indexing Pipeline (Indexer)

Two modes: Full Index (first run / forced) and Incremental Index (delta).

#### Shared Phases (Phase 0–1)

**Phase 0 — Project Detection**
- Scanner detects project type, submodules, and build files
- Framework detection (infers Spring/Gin/FastAPI/etc. from build files)
- Builds annotation whitelist from detected frameworks + user config
- Builds DefParser managers (MyBatis XML, GraphQL schema)

**Phase 1 — File Scanning**
- Scans source files + definition files (XML/GraphQL)
- Filters by primary language (TS/JS treated as same family)
- Records skipped files (too large, unsupported language, etc.)

#### Full Index Flow

**Phase 2 — Clean**
- Clears entire graph + parse cache, recreates indexes

**Phase 3 — Parse**
- Concurrent Tree-sitter parsing of all files → ParseResult + SymbolTable
- Also parses def files (MyBatis XML, GraphQL schema) via DefParser managers

**Phase 4 — Write**
- Structural nodes: Repository / Directory / File + CONTAINS edges
- Semantic nodes: Function / Class / Interface / Route / QueryNode / Annotation
- RemoteCall edges (REMOTE_CALLS_ROUTE / REMOTE_CALLS_EXT)

**Phase 5 — Resolve**

| Step | Description | Output |
|------|-------------|--------|
| A. Import resolution | Match raw imports against source file paths | IMPORTS edges |
| B. Local type inference | Per-file TypeEnv from constructors + type annotations | variable → type mapping |
| C. Multi-return inference | Resolve variables from multi-return functions (e.g. Go) | extended TypeEnv |
| D. Fixpoint propagation | Iteratively resolve transitive assignments (x = y) | final TypeEnv |
| E. Call resolution | Match raw calls against SymbolTable using TypeEnv | CALLS edges (with confidence) |
| F. Heritage resolution | Match extends/implements declarations | EXTENDS / IMPLEMENTS edges |
| G. Override detection | Find child methods overriding parent methods | OVERRIDES / DISPATCHES edges |
| H. Cross-file propagation | Propagate types along import edges, re-resolve affected calls | improved CALLS edges |
| I. Write results | External nodes + all relation edges + UNRESOLVED_CALL hints | graph edges |

Cross-file propagation (Step H) is conditional — only triggered when best_guess ratio > 3% of total calls, balancing resolution quality against computation cost.

Call resolution confidence scores:

| Strategy | Confidence | Condition |
|----------|-----------|-----------|
| type_exact | 0.95 | Receiver type known from TypeEnv → exact match |
| arg_count | 0.85 | Unique function name + matching argument count |
| same_file | 0.85 | Unique function name within the same file |
| name_unique | 0.70 | Globally unique function name |
| type_parent | 0.65 | Match via inheritance hierarchy |
| best_guess | 0.25 | Multiple candidates, pick first |

**Phase 6 — Save Fingerprints**

#### Incremental Index Flow

**Phase 2 — Detect Changes**
- Compares file fingerprints (mod time + size + content hash) to find changed + deleted files

**Phase 2.5 — Find Affected**
- Queries IMPORTS edges to find downstream files that import changed/deleted files

**Phase 3 — Clean**
- Deletes old nodes/edges for changed, deleted, and affected files
- Cleans empty directory nodes left by deletions

**Phase 4–6 — Same as Full Index**, but only processes changed + affected files.
- Additionally loads existing symbols from graph to complete SymbolTable for cross-file resolution

### 7.2 Query Workflow (Querier)

#### Symbol Resolution

- **QuerySymbol** — Find symbols by name with optional Kind filtering
- **ResolveFunction** — Find function by short name or qualified name; returns candidate list on ambiguous match
- **ResolveFunctionWithInheritance** — Falls back to parent class methods via EXTENDS chain when direct lookup fails

#### Call Chain

**QueryCallChain / QueryCallChainEx** — Traverse call graph forward (callees) or reverse (callers).

Parameters:
- `depth` — max traversal depth
- `minConfidence` — edge confidence threshold (default 0.70, filters out best_guess and type_parent)
- `includeUnresolved` — include UNRESOLVED_CALL hint edges

When resolved via inheritance fallback, reverse queries are filtered by `declared_type` to show only callers of the specific subclass.

Display mode filter chain: `full → core → dry → compact`

| Mode | Filters Applied |
|------|----------------|
| full | No filtering, raw subgraph |
| core | Remove getter/setter + external (`[external]`) nodes |
| dry | core + remove log/exception methods + prune declared_type dispatches + trim verbose properties |
| compact | dry + merge duplicate edges (same source+target+kind), `line` → `lines` array |

#### Route Chain

**QueryRouteChain** — Trace HTTP route through its full processing chain.

Flow: Route → HANDLES → BFS CALLS → EXECUTES

- Each node annotated with architectural layer (controller/service/repository) via annotation edges
- Collects QueryNode (SQL queries) via EXECUTES edges
- Supports HTTP method filter and maxDepth

#### Impact Analysis

- **ImpactAnalysis** — Reverse traversal via CALLS + DISPATCHES edges to find all affected callers
- **QueryAffectedRoutes** — From affected function IDs, reverse-lookup associated Process/entry points (requires analyze data)

#### Annotation / Layer Query

- **QueryByAnnotation** — Find symbols by annotation name + optional params substring match
- **QueryByLayer** — Find symbols by architectural layer (controller/service/repository/model)
- **QueryByAnnotationCategory** — Find symbols by annotation category (security/behavior/etc.)

#### Class Methods

- **QueryClassMethods** — Find all methods belonging to a class via CONTAINS edges; supports short name and qualified name

#### Other

- **SearchFTS** — Full-text search across all indexed symbols
- **Overview** — Graph statistics (node/edge/file counts by kind)
- **LocateFunction** — Map file+line pairs to enclosing symbol (Function > Class > Interface > File)
- **Report** — Data quality diagnostics (duplicate nodes, missing file_path, empty names)

### 7.3 Analysis Workflow (Analyzer)

Analyzer performs post-indexing graph analysis: entry point detection and process tracing. Requires indexed graph data. Two scopes: `entries` (entry points only) and `all` (entries + processes).

#### Phase 1 — Build Call Forest

Loads all CALLS edges + all Function nodes into memory, builds an in-memory directed graph with in-degree/out-degree maps.

#### Phase 2 — Load Metadata

Batch-loads supporting data for classification:

| Data | Source | Purpose |
|------|--------|---------|
| HANDLES edges + Route nodes | Graph | Identify HTTP endpoint handlers |
| HAS_ANNOTATION edges | Graph | Annotation-based classification (e.g. `@Scheduled`, `@XxlJob`) |
| IMPLEMENTS edges + Class nodes | Graph | Identify interface implementations |
| Class annotations | Node properties | Detect `@RestController` to distinguish endpoints from remote clients |

#### Phase 3 — Classify Roots

Finds root nodes (in-degree = 0 in call forest + handler functions not in forest) and classifies each:

| Entry Type | Condition | Score |
|------------|-----------|-------|
| `http_endpoint` | Has HANDLES edge to Route node | 0.95 |
| `remote_client` | Has HANDLES edge to Feign/gRPC route (not on @RestController class) | 0.90 |
| annotation-based | Has annotation with defined entry type (e.g. `@Scheduled` → `scheduled_task`) | 0.85 |
| `cli_command` | Function named `main` / `Main` | 0.80 |
| `interface_impl` | Belongs to a class that implements an interface | 0.70 |
| `unknown_entry` | In-degree=0, has callees | 0.50 |
| `suspected_dead` | In-degree=0, no callees | 0.30 |

Results are written back to Function nodes as `entry_type` and `entry_point_score` properties.

#### Phase 4 — Trace Processes (scope=all only)

For each non-dead entry point, DFS traversal through the call forest:
- Builds a `ProcessStep` tree with depth, layer, and confidence
- Flattens tree into ordered step list
- Creates `Process` node + `STEP` edges to each function in the chain
- Skips `suspected_dead`, `unknown_entry`, and `interface_impl` entries

Each Process node stores: name, entry_point, step_count, entry_type, file_path, route_method, route_path.

#### Phase 5 — Persist

Writes all Process nodes and STEP edges to the graph. Updates `.fcg/status.json` with analyze timestamp.
