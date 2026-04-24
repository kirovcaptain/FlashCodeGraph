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

| What you're looking for | Use | Never use |
|------------------------|-----|-----------|
| Class/Interface/Enum definition | `query_symbol` | grep for class name |
| Method/Function definition | `query_symbol` | grep for method name |
| Annotation-based lookup | `query_by_annotation` | grep for annotation |
| Layer-based lookup | `query_by_layer` | grep for package path |
| Who calls a function | `query_call_chain(reverse=true)` | grep for function name |
| HTTP route full chain | `query_route_chain` | grep for route path |
| All HTTP endpoints | `query_entry_points(type="http_endpoint")` | grep for @GetMapping etc. |

## Mode Parameter

`query_call_chain`, `query_route_chain`, and `query_call_forest` support a `mode` parameter:

| Mode | Philosophy | What You See |
|------|-----------|-------------|
| `dry` | **Signal over noise.** (Default) Show only what matters for understanding the logic flow. | Removes log/exception calls, prunes unrelated polymorphic dispatch branches, trims verbose properties. |
| `core` | **Code you own.** Focus on project-internal business logic. | Strips accessor methods (get/set) and external library nodes. |
| `compact` | **Minimal footprint.** Densest representation for large call chains. | Everything in `dry`, plus merges duplicate edges into one with a `lines` array. |
| `full` | **Nothing hidden.** Complete raw graph for debugging. | All nodes and edges including getters, setters, external dependencies, log calls. |

Use default `dry` mode unless the user explicitly asks for more or less detail.

## Inheritance Fallback

When querying a method that exists on a parent class but is called via a child class, FCG automatically walks the EXTENDS chain upward (max 5 levels) to find the method definition. Results are filtered by `declared_type` to only return callers relevant to the specified child class.

If multiple classes share the same short name, the tool returns a candidate list — present it to the user and ask which one to use, then retry with the selected fully qualified name.

## Impact Analysis

`impact_analysis` returns:
- **nodes + edges**: function-level caller tree (always available)
- **affected_routes**: list of API endpoints affected by the change (requires `analyze_repository` first)
- **hint**: if no analyze data exists, suggests running `analyze_repository`

## When NOT to Use FCG

- Searching for literal strings in comments/configs (use grep)
- Reading raw file content for editing (use fs_read)
- The project is not indexed (check `list_projects` first)
- Index may be stale (check `check_index_status` first)
- Never silently fall back to grep when FCG returns no results — inform the user and ask
