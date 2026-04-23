---
name: fcg
description: Use FlashCodeGraph MCP tools for code navigation, call chain tracing, impact analysis, route tracing, and annotation queries. Prefer FCG tools over grep/read for symbol lookup in indexed projects.
---

# FlashCodeGraph Skill

FlashCodeGraph (FCG) builds a code knowledge graph from source code. Use its MCP tools for structural code queries instead of grep/read.

## Project Path Resolution

1. Call `list_projects` to get all indexed projects
2. Match the user's project reference to an entry (name, abbreviation, or fuzzy match)
3. If ambiguous → ask user to clarify
4. If no match → ask user for the path
5. If user does not specify a project → ask which project

## Tool Selection Priority

| Task | FCG Tool | Instead of |
|------|----------|------------|
| Find function/class definition | `query_symbol` | grep + read |
| Find symbol by partial name | `search` | grep with regex |
| Find who calls a function | `query_call_chain` (reverse=true) | grep for function name |
| Find what a function calls | `query_call_chain` | read entire function |
| Assess refactoring impact | `impact_analysis` | manual grep + trace |
| List class methods | `query_class_methods` | grep + read file |
| Find interface implementations | `query_dependencies` (kind=IMPLEMENTS, reverse=true) | grep |
| Find by annotation | `query_by_annotation` | grep for annotation |
| Find by architectural layer | `query_by_layer` | grep for package names |
| List HTTP endpoints | `query_entry_points` (type="http_endpoint") | grep for route annotations |
| Trace API request chain | `query_route_chain` | manual read through layers |
| View full call trees from entries | `query_call_forest` | manual trace |

## Decision Rules

**Any time you encounter a class, interface, enum, method, or struct name in an indexed project, use FCG tools first.**

- ❌ Using `grep` to find class/method names in indexed projects
- ❌ Using `code search_symbols` when FCG `query_symbol`/`search` is available
- ❌ Asking user for file paths when `query_symbol` can resolve them
- ❌ Picking a result on behalf of the user when multiple candidates are returned — always present the list and ask

## Mode Parameter

`query_call_chain`, `query_route_chain`, and `query_call_forest` support a `mode` parameter:

| Mode | Philosophy | What You See |
|------|-----------|-------------|
| `dry` | **Signal over noise.** (Default) | Removes log/exception calls, prunes unrelated dispatch branches, trims properties. |
| `core` | **Code you own.** | Strips accessor methods and external library nodes. |
| `compact` | **Minimal footprint.** | Everything in `dry`, plus merges duplicate edges. |
| `full` | **Nothing hidden.** | All nodes and edges for debugging. |

Use default `dry` unless user asks for more detail.

## Annotation Aliases

When users describe annotations in natural language, map to the correct name:

| User says | Annotation | Framework |
|-----------|-----------|-----------|
| xxl / xxljob / scheduled task | XxlJob | xxl-job |
| scheduled / cron (Spring) | Scheduled | Spring |
| cron (NestJS) | Cron | NestJS |
| rabbit / rabbitmq | RabbitListener | RabbitMQ |
| kafka / kafka listener | KafkaListener | Kafka |
| rocketmq / rocket listener | RocketMQMessageListener | RocketMQ |
| event listener | EventListener | Spring |
| dubbo service | DubboService | Dubbo |
| grpc service | GrpcService | gRPC |
| dubbo reference / dubbo client | DubboReference | Dubbo |
| transactional | Transactional | Spring |
| async | Async | Spring |
| cacheable | Cacheable | Spring |
| preauthorize | PreAuthorize | Spring Security |
| graphql query | QueryMapping | GraphQL |
| graphql mutation | MutationMapping | GraphQL |
| deprecated | Deprecated | common |
| mapper / mybatis | Mapper | MyBatis |

If a compound keyword returns no results, split and retry with the root word (≥4 chars).

## Scenarios

### API Route Analysis

**When**: debugging an API endpoint, documenting API logic, encountering route annotations.

```
# Know the exact route
query_route_chain(route="/api/orders/{id}", method="GET", path=...)

# Don't know the exact path
query_entry_points(type="http_endpoint", path=...)  → find route
query_route_chain(route="...", path=...)             → trace chain

# Search by API doc annotation
query_by_annotation(annotation="ApiOperation", params="{keyword}", path=...)
→ extract route from result → query_route_chain

# Full call tree from endpoints
query_call_forest(type="http_endpoint", depth=5, path=...)
```

When multiple routes match, always present the list and ask user to select.

### Field Impact Analysis

**When**: modifying, renaming, or removing a class field; assessing blast radius.

1. `grep` the field name → file:line list
2. `locate_function` with batch file:line pairs → enclosing function names
3. Deduplicate functions
4. `impact_analysis` per function → caller chains + affected routes

### Performance Profiling

**When**: a function has abnormal execution time, need branch-level timing.

1. `query_call_chain(function="target", depth=2)` → callee tree
2. Read target function, list ALL return paths explicitly
3. Design timing: one `branchIdx` per logical branch, default = OTHER bucket
4. Use `defer`/`finally` pattern — each call's total time assigned to exactly one exit branch
5. Validate on a fast project first, then run on the slow project
6. If one branch > 50% of time → drill down with `query_call_chain` on that sub-function

## Inheritance Fallback

When querying a method on a parent class called via a child class, FCG walks the EXTENDS chain upward (max 5 levels). Results are filtered by `declared_type` for the specific subclass.

If multiple classes share the same short name, the tool returns candidates — present the list and ask user to select.

## Impact Analysis Notes

`impact_analysis` returns:
- **nodes + edges**: function-level caller tree
- **affected_routes**: API endpoints affected (requires `analyze_repository`)
- **hint**: suggests running `analyze_repository` if missing

## When NOT to Use FCG

- Searching for literal strings in comments/configs → use grep
- Reading raw file content for editing → use fs_read
- Project is not indexed → check `list_projects` first
- Index may be stale → check `check_index_status` first
- Never silently fall back to grep — inform user and ask
