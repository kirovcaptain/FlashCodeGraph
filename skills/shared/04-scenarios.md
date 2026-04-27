## Scenarios

### API Route Analysis

**When**: debugging an API endpoint, documenting API logic, encountering route annotations.

```
# Know the exact route
query_route_chain(route="/api/orders/{id}", method="GET", path=...)

# Don't know the exact path
query_entry_points(type="http_endpoint", path=...)  → find route
query_route_chain(route="...", path=...)             → trace chain

# Full call tree from endpoints
query_call_forest(type="http_endpoint", depth=5, path=...)
```

When multiple routes match, always present the list and ask user to select.

### Search by API doc annotation

**When**: user says "api doc xxx" or "接口文档 xxx"

1. Check `overview` for frameworks:
   - `swagger2` → search `ApiOperation`
   - `swagger3` → search `Operation`
2. First try with user's original phrase as params:
   `query_by_annotation(annotation="{annotation}", params="{original phrase}", path=...)`
3. If no results, try meaningful sub-phrases (not single words).
4. Extract route from result → `query_route_chain` (if applicable)

### Field Impact Analysis

**When**: modifying, renaming, or removing a class field; assessing blast radius.

1. `grep` the field name → file:line list
2. `locate_function` with batch file:line pairs → enclosing function names
3. Deduplicate functions
4. `impact_analysis` per function → caller chains + affected routes

### Cross-Project Call Chain

**When**: tracing calls that cross project boundaries (FeignClient, Dubbo, gRPC interfaces from jar dependencies), or seeing `[cross-project]` nodes in call chain results.

1. Call chain results may contain `[cross-project]` nodes — these are symbols injected from dependency projects.
2. The response includes `cross_project_hints` with the source project path and a suggested follow-up query.
3. To trace into the dependency project, follow the hint:
   ```
   query_call_chain(function="targetMethod", path="/path/to/dependency-project")
   ```
4. For a project-level view of all cross-service dependencies:
   ```
   query_cross_chain(function="entryFunction", path=...)
   ```
   Returns aggregated results by target project, protocol (http/dubbo/grpc), and routes.

### Performance Profiling

**When**: a function has abnormal execution time, need branch-level timing.

1. `query_call_chain(function="target", depth=2)` → callee tree
2. Read target function, list ALL return paths explicitly
3. Design timing: one `branchIdx` per logical branch, default = OTHER bucket
4. Use `defer`/`finally` pattern — each call's total time assigned to exactly one exit branch
5. Validate on a fast project first, then run on the slow project
6. If one branch > 50% of time → drill down with `query_call_chain` on that sub-function
