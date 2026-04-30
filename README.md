# FlashCodeGraph

**Turn source code into a queryable knowledge graph.**

FlashCodeGraph (FCG) statically analyzes codebases and builds a rich graph of functions, classes, call relationships, HTTP routes, database queries, and annotations — all stored in a graph database for instant traversal.

## Why

Modern codebases are too large for humans to hold in their heads, and too interconnected for text search to navigate reliably. LLMs can read code but lack structural awareness — they don't know what calls what, which API triggers which database query, or how a single change ripples through the system.

FCG bridges this gap by providing a **structural layer** between source code and the tools that reason about it:

- **For developers** — "What calls this function?", "Which APIs are affected if I change this service?", "Trace this HTTP request from controller to database."
- **For AI agents** — LLMs are powerful code readers but structurally blind. They can't reliably answer "what calls this?" or "what breaks if I change this?" by reading files alone. FCG's MCP Server feeds precise, pre-computed structural data — call chains, impact graphs, entry point maps, route-to-database traces — directly into the agent's context. This turns vague "search and guess" into exact graph traversal, dramatically improving accuracy for code review, refactoring planning, and change impact assessment.
- **For teams** — Shared graph database means everyone queries the same up-to-date code structure, no local setup per developer.

## Vision

Code understanding should be as fast as a database query. FCG aims to be the **structural backbone** for code intelligence — a single index that powers IDE navigation, AI-assisted development, code review automation, and architecture visualization, across any language and any scale.

## Features

- **Multi-language** — Java, Go, Python, TypeScript/JavaScript with framework-aware parsing
- **Graph-based code intelligence** — call chains, impact analysis, route tracing, annotation queries
- **Cross-project call chains** — trace calls across project boundaries through dependency configuration (FeignClient, Dubbo, gRPC)
- **Incremental indexing** — fingerprint-based change detection, only re-parses modified files
- **Confidence-scored resolution** — multi-strategy call resolution with 6-level confidence scoring
- **Dual interface** — CLI for interactive use, MCP Server for AI agent integration
- **Pluggable storage** — FalkorDB (remote) or KùzuDB (local embedded)

## Quick Start

```bash
# Initialize global config (storage backend selection)
fcg init

# Index a project
fcg index /path/to/project

# Query a symbol
fcg query UserService

# Trace call chain (who calls this?)
fcg callchain UserService.findById --reverse

# Trace HTTP route from controller to database
fcg trace /api/users --method GET

# Impact analysis (what breaks if I change this?)
fcg impact UserService.save

# Analyze entry points
fcg analyze

# Start MCP Server (for AI agents)
fcg mcp serve
```

## Supported Languages

### Fully Supported (Dedicated Parser + Resolver)

| Language | Framework Detection |
|----------|-------------------|
| Java | Spring, MyBatis, Hibernate, Feign, Dubbo, gRPC, Protobuf |
| Go | Gin, Echo, Fiber, gRPC |
| Python | FastAPI, Django, Flask, Requests, Httpx, Strawberry |
| TypeScript / JavaScript | Express, NestJS, Axios |

### Planned

Rust, C, C++, C#, Ruby, PHP

## CLI Commands

### Command Overview

| Group | Command | Description |
|-------|---------|-------------|
| Indexing | `fcg index [path]` | Index a code repository |
| Querying | `fcg query [symbol]` | Query symbols by name, annotation, layer, or category |
| Querying | `fcg callchain <function>` | Query call chain for a function |
| Querying | `fcg trace <route>` | Trace route from entry point to database |
| Querying | `fcg impact <function>` | Analyze impact of changes to a function |
| Querying | `fcg search <query>` | Full-text search across symbols |
| Querying | `fcg routes` | List all HTTP routes |
| Querying | `fcg analyze [scope]` | Detect entry points and trace processes |
| Querying | `fcg list-entries [type]` | List detected entry points |
| Management | `fcg init` | Initialize global configuration (~/.fcg/config.toml) |
| Management | `fcg setup [path]` | Interactive project setup — generates `.fcg/config.toml` |
| Management | `fcg overview` | Show project statistics |
| Management | `fcg status` | Show index status for current project |
| Management | `fcg list` | List all indexed projects |
| Management | `fcg report` | Data quality report |
| Management | `fcg remove` | Remove project index data |
| AI Agent | `fcg skill install [platform]` | Install FCG skill and MCP config for a platform |
| AI Agent | `fcg skill list` | List available platforms |
| AI Agent | `fcg mcp serve` | Start MCP Server (stdio transport) |

---

### Indexing

**`fcg index [path]`**

| Flag | Default | Description |
|------|---------|-------------|
| `--force` | false | Force full re-index, ignore incremental cache |
| `--branch` | auto-detect | Git branch name to index |
| `--debug` | false | Dump debug CSV files to `.fcg/debug/` |

```bash
fcg index                          # index current directory
fcg index /path/to/project         # index a specific project
fcg index --force                  # full re-index
fcg index --branch feature/login   # index specific branch
fcg index --debug                  # with debug output
```

### Querying Symbols

**`fcg query [symbol]`**

| Flag | Default | Description |
|------|---------|-------------|
| `--kinds` | all | Filter by kind, comma-separated: `Function`, `Class`, `Interface` |
| `--limit` | 20 | Max results |
| `--annotation` | | Filter by annotation name (e.g. `Service`, `XxlJob`) |
| `--params` | | Filter by annotation params (substring match, used with `--annotation`) |
| `--layer` | | Filter by architectural layer: `controller`, `service`, `repository`, `model` |
| `--category` | | Filter by annotation category (e.g. `security`, `behavior`) |
| `--list-categories` | false | List all available annotation categories |
| `--methods` | false | List methods of a class |

```bash
fcg query UserService                                  # find symbol by name
fcg query save --kinds Function                        # filter by kind
fcg query UserService --methods                        # list class methods
fcg query --annotation Service                         # find by annotation
fcg query --annotation XxlJob --params "dailyReport"  # annotation + params filter
fcg query --layer controller                           # find by layer
fcg query --category security                          # find by category
fcg query --list-categories                            # show available categories
```

### Call Chain

**`fcg callchain <function>`**

| Flag | Default | Description |
|------|---------|-------------|
| `--reverse` | false | Show callers instead of callees |
| `--depth` | 3 | Max traversal depth |
| `--min-confidence` | 0 | Min confidence threshold (0.0–1.0) |
| `--mode` | `dry` | Display mode: `full`, `core`, `dry`, `compact` |
| `--flow` | false | Show control flow context — groups callees under their if/else/loop/defer/switch branches |

Display modes:

| Mode | Philosophy | What You See |
|------|-----------|-------------|
| `full` | **Nothing hidden.** Complete raw graph for debugging and verification. | All nodes and edges including getters, setters, external dependencies, log calls, and exception constructors. |
| `core` | **Code you own.** Focus on project-internal business logic. | Strips accessor methods (get/set) and external library nodes (`[external]`), keeping only your source code. |
| `dry` | **Signal over noise.** (Default) Show only what matters for understanding the logic flow. | Everything in `core`, plus: removes log/exception calls, prunes unrelated polymorphic dispatch branches, trims verbose edge properties. |
| `compact` | **Minimal footprint.** Densest representation for large call chains. | Everything in `dry`, plus: merges duplicate edges between the same pair of nodes into one, aggregating call sites into a `lines` array. |

```bash
fcg callchain UserService.findById                # callees (what does it call?)
fcg callchain UserService.findById --reverse      # callers (who calls it?)
fcg callchain UserService.findById --depth 5      # deeper traversal
fcg callchain save --mode full                    # show everything
fcg callchain save --mode compact                 # most concise output
fcg callchain save --flow                         # show if/else/loop/defer branching structure
```

When a symbol name matches multiple functions, an interactive prompt lets you pick the exact one:

```
Multiple functions match "save":

  [1] com.example.UserService.save       (String, int)    UserService.java
  [2] com.example.OrderService.save      (Order)          OrderService.java
  [q] quit

Select: 1
```

### Route Tracing

**`fcg trace <route-path>`**

| Flag | Default | Description |
|------|---------|-------------|
| `--method` | all | HTTP method filter: `GET`, `POST`, `PUT`, `DELETE` |
| `--depth` | 10 | Max traversal depth |
| `--mode` | `dry` | Display mode: `full`, `core`, `dry`, `compact` |

```bash
fcg routes                              # list all HTTP routes first
fcg trace /api/users                    # trace route to database
fcg trace /api/users --method GET       # filter by HTTP method
fcg trace /api/orders --depth 15        # deeper traversal
fcg trace /api/users --mode full        # include getters/setters/externals
```

### Impact Analysis

**`fcg impact <function>`**

| Flag | Default | Description |
|------|---------|-------------|
| `--depth` | 3 | Max traversal depth |
| `--min-confidence` | 0 | Min confidence threshold (0.0–1.0) |

Output includes affected caller tree + affected entry points (API routes, scheduled tasks, etc.).

```bash
fcg impact UserService.save             # what breaks if I change this?
fcg impact UserService.save --depth 5   # deeper analysis
```

### Full-Text Search

**`fcg search <query>`**

| Flag | Default | Description |
|------|---------|-------------|
| `--limit` | 20 | Max results |

```bash
fcg search "order process"
```

### Entry Point Analysis

**`fcg analyze [scope]`**

| Flag | Default | Description |
|------|---------|-------------|
| `--depth` | 10 | Max trace depth for process analysis |
| `--force` | false | Re-analyze even if up-to-date |

| Scope | Behavior |
|-------|----------|
| (none) | Full analysis: entry points + process tracing |
| `entries` | Only detect and classify entry points (faster) |
| `process` | Only trace processes |

**`fcg list-entries [type]`**

| Type Filter | Description |
|-------------|-------------|
| `http_endpoint` | HTTP route handlers |
| `remote_client` | Feign/gRPC client methods |
| `cli_command` | main functions |
| `scheduled_task` | @Scheduled / @XxlJob annotated |
| `interface_impl` | Interface implementation methods |
| `unknown_entry` | In-degree=0 with callees |
| `suspected_dead` | In-degree=0, no callees |

```bash
fcg analyze                        # full analysis
fcg analyze entries                # only entry points (faster)
fcg analyze --force                # re-analyze
fcg list-entries                   # list all entry points
fcg list-entries http_endpoint     # filter by type
fcg list-entries suspected_dead    # find dead code
```

### Management

```bash
# Interactive setup — detects project type, storage, submodules; generates config
fcg setup

fcg overview                       # project statistics (nodes, edges, files by kind)
fcg status                         # index status for current project
fcg list                           # list all indexed projects
fcg report                         # data quality report (saves to .fcg/report.json + .fcg/report.md)
fcg remove --id 1 --force          # remove by project ID (from 'fcg list')
fcg remove --graph --force         # only delete graph data
fcg remove --cache --force         # only delete cache and fingerprints
```

### MCP Server

```bash
# Install skill + MCP config for your AI coding assistant
fcg skill install              # interactive platform selection
fcg skill install kiro         # direct install for Kiro
fcg skill install claude       # direct install for Claude Code
fcg skill install copilot      # direct install for GitHub Copilot
fcg skill install claude       # direct install for Claude Code
fcg skill list                 # list available platforms

# Start MCP Server (stdio transport)
fcg mcp serve
```

The MCP Server exposes the following tools for AI agent integration:

| Tool | Description |
|------|-------------|
| `list_projects` | List all indexed projects with paths, branches, and storage backends |
| `index_repository` | Index a code repository to build the knowledge graph |
| `check_index_status` | Check if index is up-to-date, returns added/modified/deleted file counts |
| `query_symbol` | Find symbol by exact name, returns file path, kind, and properties |
| `query_call_chain` | Traverse call chain — callees or callers with depth/confidence/mode control |
| `query_cross_chain` | Query cross-service call relationships — aggregated by target project, protocol, and routes |
| `query_class_members` | List all fields and methods of a class |
| `query_dependencies` | Query IMPORTS/EXTENDS/IMPLEMENTS/CALLS edges for a symbol |
| `query_by_annotation` | Find symbols by annotation name and optional params filter |
| `query_by_layer` | Find symbols by architectural layer (controller/service/repository/model) |
| `query_route_chain` | Trace HTTP route from controller through service to repository |
| `query_entry_points` | List detected entry points (HTTP endpoints, CLI commands, dead code) |
| `query_call_forest` | Query call forest from entry points with tree structure |
| `impact_analysis` | Find all direct and indirect callers affected by a symbol change |
| `search` | Fuzzy/partial name search across all symbols |
| `locate_function` | Map file+line locations to enclosing function/class (for grep → symbol resolution) |
| `analyze_repository` | Run entry point detection and process tracing |
| `overview` | Get project statistics (node/edge/file counts) |

MCP resource: `fcg://overview` — project statistics as JSON.

## Configuration

Two-level TOML configuration: global `~/.fcg/config.toml` → project `.fcg/config.toml` (project overrides global).

```toml
[project]
name = "my-project"
type = "maven"          # maven | gradle | npm | go | cargo | dotnet

[storage]
database = "falkordb"   # falkordb | kuzu

[index]
max_file_size = 524288  # skip files > 512KB
exclude_tests = true
```

For cross-project call chain support, configure dependencies in the project config:

```toml
[[dependencies.projects]]
path = "/path/to/shared-api"
branch = "master"

[dependencies.properties]
"feign.payment.name" = "payment-service"
```

See [docs/configuration.md](docs/configuration.md) ([example](docs/config.example.toml)) for all options.

## Storage Backends

| Backend | Type | Status | Use Case |
|---------|------|--------|----------|
| FalkorDB | Remote (Redis protocol) | ✅ Implemented | Default, team sharing |
| KùzuDB | Local embedded | ✅ Implemented | No external dependencies, single machine |
| Neo4j | Remote | 🚧 Planned | Existing Neo4j infrastructure |

## Architecture

See [docs/architecture.md](docs/architecture.md) for detailed architecture documentation.

## Development

### Prerequisites

| Requirement | Version | Notes |
|-------------|---------|-------|
| Go | ≥ 1.23 | |
| GCC / C compiler | any | Required — Tree-sitter and KùzuDB are C libraries, `CGO_ENABLED=1` is mandatory |
| golangci-lint | latest | Optional, for linting |

CGO dependencies (linked automatically via Go modules):
- **tree-sitter** + language grammars (C) — AST parsing
- **go-kuzu** (C++) — KùzuDB embedded storage backend

### Build

```bash
make build          # → bin/fcg
make test           # run all tests
make lint           # golangci-lint
make clean          # remove build artifacts
```

### Cross-Platform Build

FCG requires `CGO_ENABLED=1` due to Tree-sitter and KùzuDB C bindings. Cross-compilation needs a C cross-compiler for the target platform.

| Target | C Compiler | Build Command |
|--------|-----------|---------------|
| Linux amd64 (native) | gcc | `GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build -o fcg ./cmd/fcg/` |
| macOS arm64 (native) | clang (Xcode) | `GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -o fcg ./cmd/fcg/` |
| Windows amd64 | mingw-w64 | `GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc go build -o fcg.exe ./cmd/fcg/` |
| Linux arm64 (cross) | aarch64-linux-gnu-gcc | `GOOS=linux GOARCH=arm64 CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc go build -o fcg ./cmd/fcg/` |

CI/CD uses GitHub Actions with per-platform native runners to avoid cross-compilation complexity (see `.github/workflows/release.yml`).

### Database Setup

#### FalkorDB (Default for WSL / Remote)

FalkorDB is a Redis-compatible graph database. Recommended for team sharing or WSL environments.

**Docker (quickest for local development):**

```bash
docker run -d --name falkordb -p 6379:6379 falkordb/falkordb:latest

# Verify
redis-cli -p 6379 PING    # → PONG
```

**FalkorDB Cloud (managed, no ops):**

Sign up at [falkordb.com/cloud](https://app.falkordb.cloud) for a free-tier instance, then configure the URI:

```toml
[storage]
database = "falkordb"
falkordb_uri = "your-instance.falkordb.io:6379"
```

**Unix Socket (lightweight local deployment):**

FalkorDB is a Redis module — it requires a Redis server with the `falkordb.so` module loaded. Unix socket provides lower latency and no TCP overhead:

```bash
# Option 1: Extract module from Docker image
docker create --name tmp falkordb/falkordb:latest
docker cp tmp:/FalkorDB/bin/linux-x64-release/src/falkordb.so ./falkordb.so
docker rm tmp

# Option 2: Build from source (https://github.com/FalkorDB/FalkorDB)

# Start Redis with FalkorDB module on Unix socket
redis-server --loadmodule ./falkordb.so --unixsocket ~/.fcg/falkordb.sock --port 0
```

```toml
[storage]
database = "falkordb"
falkordb_uri = "~/.fcg/falkordb.sock"
```

FCG auto-detects `~/.fcg/falkordb.sock` — if the socket file exists, it is used automatically without any config.

FCG connects to `localhost:6379` by default. Override in config:

```toml
[storage]
database = "falkordb"
falkordb_uri = "localhost:6379"     # TCP address
# falkordb_uri = "~/.fcg/falkordb.sock"  # or Unix socket
falkordb_graph = "fcg"              # graph name (default: "fcg")
```

#### KùzuDB (Default for Native Linux / macOS / Windows)

KùzuDB is an embedded graph database — no external process needed. Data stored in memory, no setup required.

```toml
[storage]
database = "kuzu"
```

> **Note:** KùzuDB disk mode is unreliable under WSL, so FCG auto-detects WSL and defaults to FalkorDB in that environment.

### Storage Backend Auto-Detection

FCG automatically selects the default backend based on environment:

| Environment | Default Backend | Reason |
|-------------|----------------|--------|
| Native Linux / macOS / Windows | KùzuDB | Zero setup, embedded |
| WSL (Windows Subsystem for Linux) | FalkorDB | KùzuDB disk I/O issues on WSL |

Override anytime via `storage.database` in config.

> **Recommendation:** FalkorDB is the most thoroughly tested backend and is recommended for production use. KùzuDB works well for quick local experiments but FalkorDB has proven more stable across diverse projects and scales.

## Usage Scenarios

### Cross-Service Call Tracing

A developer needs to understand how order shipment tracking works across two microservices: `mall-admin` (e-commerce backend) and `logistics-server` (shipping infrastructure).

**Step 1 — Find the entry point**

```bash
$ fcg callchain getShipmentStatus --reverse
```

```
SyncShipmentStatusJob.execute()        ← scheduled task
  └→ OrderService.getShipmentStatus()
```

The method is called by a scheduled job that periodically syncs shipment status from the logistics provider.

**Step 2 — Trace what it calls**

```bash
$ fcg callchain getShipmentStatus --depth 2
```

```
OrderService.getShipmentStatus()
  └→ logisticsApi.queryShipment()  [cross-project]
       hint: query_call_chain(function="queryShipment", path="/path/to/logistics-server")
```

FCG detects the Feign client call crosses project boundaries and generates a hint to continue tracing.

**Step 3 — Follow the cross-service call**

```bash
$ fcg callchain LogisticsController.queryShipment --depth 2 --project logistics-server
```

```
LogisticsApi (Feign interface)
  │ DISPATCHES
  ▼
LogisticsController.queryShipment()
  └→ LogisticsService.queryShipment()
       ├→ SignUtil.generateSign()              // request signing
       └→ LogisticsService.buildHttpHeaders()  // HTTP headers
```

**Full picture in 3 queries:**

```
mall-admin                                     logistics-server
──────────                                     ────────────────

SyncShipmentStatusJob.execute()
  └→ OrderService.getShipmentStatus()
       └→ logisticsApi.queryShipment()  ─Feign─→  LogisticsApi (interface)
                                                     │ DISPATCHES
                                                     ▼
                                                   LogisticsController.queryShipment()
                                                     └→ LogisticsService.queryShipment()
                                                          ├→ SignUtil.generateSign()
                                                          └→ LogisticsService.buildHttpHeaders()
```

Without FCG, this would require manually searching across two repositories, reading Feign interface definitions, finding the matching controller, and tracing through service layers. With FCG, the full cross-service flow is traced in 3 queries.

<details>
<summary>💬 Same scenario with AI Agent (MCP)</summary>

> **User:** `getShipmentStatus` — what's the call logic?

**AI:** *(calls `query_call_chain(function="getShipmentStatus", reverse=true)`)*

`getShipmentStatus` is called by `SyncShipmentStatusJob.execute()` — a scheduled task. Let me trace what it does.

*(calls `query_call_chain(function="getShipmentStatus", depth=2)`)*

It calls `logisticsApi.queryShipment()` which is a cross-project Feign call to `logistics-server`.

*(calls `query_call_chain(function="queryShipment", path="/path/to/logistics-server")`)*

The full chain:

```
SyncShipmentStatusJob → OrderService.getShipmentStatus()
  → logisticsApi.queryShipment()  ──Feign──→  LogisticsController
                                                 └→ LogisticsService.queryShipment()
                                                      ├→ SignUtil.generateSign()
                                                      └→ LogisticsService.buildHttpHeaders()
```

*(reads source)* The method builds a signed HTTP request to the third-party logistics provider, queries shipment status by tracking number, and maps external status codes to internal enum values.

</details>

### Refactoring Impact Assessment

Before renaming `UserRepository.findByEmail`, check what breaks:

```bash
$ fcg impact UserRepository.findByEmail --depth 3
```

```
UserRepository.findByEmail
  ← UserService.getUserByEmail          (UserService.java:45)
  │   ← UserController.login            (UserController.java:32)    ← 🌐 POST /api/auth/login
  │   ← UserController.resetPassword    (UserController.java:78)    ← 🌐 POST /api/auth/reset-password
  ← NotificationService.resolveRecipient (NotificationService.java:112)
      ← NotificationJob.execute          (NotificationJob.java:28)   ← ⏰ @XxlJob("sendNotification")

Affected entry points:
  🌐 POST /api/auth/login
  🌐 POST /api/auth/reset-password
  ⏰  @XxlJob("sendNotification")
```

One command shows: 2 API endpoints and 1 scheduled task will be affected. Notify the relevant owners before refactoring.

<details>
<summary>💬 Same scenario with AI Agent (MCP)</summary>

> **User:** What breaks if I change `UserRepository.findByEmail`?

**AI:** *(calls `impact_analysis(symbol="UserRepository.findByEmail", depth=3)`)*

3 entry points will be affected:

- 🌐 POST /api/auth/login
- 🌐 POST /api/auth/reset-password
- ⏰ @XxlJob("sendNotification")

The call paths are: `UserController.login` and `resetPassword` both go through `UserService.getUserByEmail`, and `NotificationJob` goes through `NotificationService.resolveRecipient`. All changes are contained within these 3 callers.

</details>

### Discovering Project Entry Points

Get a bird's-eye view of all entry points in a project:

```bash
$ fcg list-entries http_endpoint
```

```
HTTP Endpoints (12):
  GET    /api/users              → UserController.list
  POST   /api/users              → UserController.create
  GET    /api/users/{id}         → UserController.getById
  GET    /api/orders             → OrderController.list
  POST   /api/orders             → OrderController.create
  POST   /api/orders/{id}/cancel → OrderController.cancel
  ...
```

```bash
$ fcg list-entries scheduled_task
```

```
Scheduled Tasks (3):
  @XxlJob("syncInventory")      → InventoryJob.execute        InventoryJob.java:15
  @XxlJob("sendNotification")   → NotificationJob.execute     NotificationJob.java:28
  @Scheduled(cron="0 0 2 * *")  → ReportJob.generateDaily     ReportJob.java:22
```

```bash
$ fcg list-entries suspected_dead
```

```
Suspected Dead Code (2):
  Function  MigrationHelper.migrateV1Data    MigrationHelper.java:10
  Function  DebugUtil.dumpGraph              DebugUtil.java:5
```

Combine entry types to get a complete picture of how the system is triggered — HTTP requests, scheduled tasks, message listeners, and code that nothing calls.

<details>
<summary>💬 Same scenario with AI Agent (MCP)</summary>

> **User:** What are the entry points in this project?

**AI:** *(calls `query_entry_points(type="http_endpoint")`)* Found 12 HTTP endpoints.

*(calls `query_entry_points(type="scheduled_task")`)* Found 3 scheduled tasks: `syncInventory`, `sendNotification`, `generateDaily`.

*(calls `query_entry_points(type="suspected_dead")`)* Found 2 functions with no callers: `MigrationHelper.migrateV1Data` and `DebugUtil.dumpGraph` — these might be dead code worth cleaning up.

</details>

### Exploring Architecture Layers

Get a quick overview of a project's structure by architectural layer:

```bash
$ fcg query --layer controller
```

```
  Class  UserController              @RestController  /api/users
  Class  OrderController             @RestController  /api/orders
  Class  PaymentController           @RestController  /api/payments
  ...
```

```bash
$ fcg query --layer service
```

```
  Class  UserService                 @Service         UserService.java
  Class  OrderService                @Service         OrderService.java
  Class  PaymentService              @Service         PaymentService.java
  ...
```

```bash
$ fcg query --layer repository
```

```
  Class  UserRepository              @Repository      UserRepository.java
  Class  OrderRepository             @Repository      OrderRepository.java
  ...
```

Useful when onboarding to a new project — see all controllers to understand the API surface, all repositories to understand the data layer, without reading a single file.

<details>
<summary>💬 Same scenario with AI Agent (MCP)</summary>

> **User:** What's the structure of this project?

**AI:** *(calls `query_by_layer(layer="controller")`)* 12 controllers — the API surface covers users, orders, payments, inventory, and reports.

*(calls `query_by_layer(layer="service")`)* 18 services. Core business logic is in `OrderService` (395 lines) and `PaymentService` (280 lines).

*(calls `query_by_layer(layer="repository")`)* 15 repositories, all using `@Repository`. Data access is in the `dao` package.

The project follows a standard 3-layer architecture: controller → service → repository.

</details>

### Annotation-Driven Query

**Find all scheduled tasks:**

```bash
$ fcg query --annotation XxlJob
```

```
  Function  InventoryJob.execute        @XxlJob("syncInventory")       InventoryJob.java:15
  Function  NotificationJob.execute     @XxlJob("sendNotification")    NotificationJob.java:28
  Function  SettlementJob.run           @XxlJob("dailySettlement")     SettlementJob.java:12
```

Narrow down with params:

```bash
$ fcg query --annotation XxlJob --params "settlement"
```

```
  Function  SettlementJob.run           @XxlJob("dailySettlement")     SettlementJob.java:12
```

**Find API documentation by description:**

```bash
$ fcg query --annotation ApiOperation --params "create order"
```

```
  Function  OrderController.create      @ApiOperation("create order")  OrderController.java:45
```

Then trace the full request chain:

```bash
$ fcg trace /api/orders --method POST
```

```
🌐 POST /api/orders
  → OrderController.create()
    → OrderService.createOrder()
      → InventoryService.deductStock()
      → OrderRepository.save()            ← 💾 INSERT INTO orders
      → PaymentService.initPayment()
```

From API doc annotation to database query in 2 commands.

<details>
<summary>💬 Same scenario with AI Agent (MCP)</summary>

> **User:** Find the "create order" API and trace the full chain.

**AI:** *(calls `query_by_annotation(annotation="ApiOperation", params="create order")`)*

Found `OrderController.create` annotated with `@ApiOperation("create order")`.

*(calls `query_route_chain(route="/api/orders", method="POST")`)*

The full chain:

```
POST /api/orders → OrderController.create()
  → OrderService.createOrder()
    → InventoryService.deductStock()
    → OrderRepository.save()            ← INSERT INTO orders
    → PaymentService.initPayment()
```

The API validates the request, deducts inventory, persists the order, and initiates payment — all in a single transaction.

</details>

### API Route Tracing

Trace a GET request from controller to database:

```bash
$ fcg trace /api/users/{id} --method GET
```

```
🌐 GET /api/users/{id}
  → UserController.getById()
    → UserService.findById()
      → UserRepository.findById()         ← 💾 SELECT * FROM users WHERE id = ?
      → PermissionService.getUserRoles()
        → RoleRepository.findByUserId()   ← 💾 SELECT * FROM roles WHERE user_id = ?
```

See exactly which tables are hit and through what code path — useful for performance debugging and SQL review.

<details>
<summary>💬 Same scenario with AI Agent (MCP)</summary>

> **User:** `GET /api/users/{id}` is slow. What queries does it hit?

**AI:** *(calls `query_route_chain(route="/api/users/{id}", method="GET")`)*

It hits 2 tables:

1. `UserRepository.findById()` → `SELECT * FROM users WHERE id = ?`
2. `RoleRepository.findByUserId()` → `SELECT * FROM roles WHERE user_id = ?`

The second query is inside `PermissionService.getUserRoles()`. If the `roles` table lacks an index on `user_id`, that's likely your bottleneck.

</details>



## License

[PolyForm Noncommercial 1.0.0](LICENSE)

## Links

- [Changelog](CHANGELOG.md)
- [Roadmap](docs/roadmap.md)
- [Configuration](docs/configuration.md) ([example](docs/config.example.toml))
- [Architecture](docs/architecture.md)
- [Language Integration Guide](docs/language-integration-guide.md)
