# Changelog

## [1.0.6] - 2026-06-15

### Feature
- FeignClient multi-path support — interfaces with multiple class-level `@RequestMapping` paths now correctly generate all route combinations
- Interface-extends-Interface indexing — inheritance chains between interfaces are now tracked and traversable
- upgrade go-ladybug version to v0.17.0

### Performance
- Faster LadybugDB CSV writes — buffered I/O with streaming escape replaces per-row syscalls
- Eliminated redundant JSON serialization during node export — direct property access with slice reuse
- Annotation data stored as structured types internally — removes repeated JSON parse/serialize cycles during indexing

### Fix
- LadybugDB recovery now removes corrupted database files on open failure (previously only cleared WAL, leaving unrecoverable state)

## [1.0.5] - 2026-06-10

### Feature
- Go `go.work` multi-module project parsing support
- Java `@RequestMapping` multi-method/path support + route ID dedup + trace interactive selection
- Usages fuzzy search by qualified_name CONTAINS
- `--profile` toolchain — CPU profile + memory trace + phase timeline to `.fcg/profile/`
- `analysis/perf_report.py` — one-command performance analysis report
- Configurable `gc_percent` in `[system]` config (default 300)

### Performance
- SymbolTable memory optimization — store symbols once with int32 index references
- GOGC=300 default reduces GC frequency during indexing
- Reduce GC pressure in ResolveCalls + resolveCallWithReceiver earlyExit
- Refactor resolveFullQualifiedType three-layer strategy + FQN fast path
- FalkorDB createNode batch size increase

### Fix
- Heritage import disambiguation + external node Kind fix
- UNRESOLVED_CALL multi-target + debug dump enhancement
- LadybugDB stability fixes

## [1.0.4] - 2026-06-02

### Feature
- TS/JS builtin type method resolution — Set/Map/Array/String/Number methods now resolve via globals.json
- TypeName normalization `T[]` → `Array<T>` at parser source for consistent type matching
- Primitive type mapping in ExternalMethodManager (`string→String`, `number→Number`, `boolean→Boolean`)
- Import-based narrowing in resolveCallFallback for no-receiver TS/JS calls
- Lambda block scope chain fix — correct scope parent tracking for lambdas inside for/if/try blocks
- Unified edge schema definition (`model.EdgeColumns`) parallel to `NodeColumns`

### Performance
- LadybugDB CSV COPY FROM for CreateNodes and CreateEdges — 10-50x faster edge writes
- Original per-row logic preserved as legacy fallback for in-memory mode

### Internal
- Migrate() in ladybug and kuzu now schema-driven from `model.EdgeColumns`
- Multi CLI and MCP improvements
- Fix staleness false positive detection after indexing dirty files

## [1.0.3] - 2026-05-18

### Feature
- search support the kind of var,annotation
- support qualified name lookup in query_symbol

### Fix
- Rewrite FalkorDB TraverseCallChain to batch BFS with declared_type filtering
- Add DISPATCHES support to LadybugDB/KuzuDB traverseBFS
- Delete PruneDeclaredTypeDispatches (now handled at traversal time)
- Fix dry mode pruning of inherited methods without overrides
- Fix Java parser to resolve ParentQualified from imports
- Fix buildQualifiedParentMap same-name class disambiguation

## [1.0.2] - 2026-05-15

### Feature
- Multi-language SQL extraction with shared variable tracking framework (Java, Go, Python, TypeScript)
- Java SQL extraction rewrite with variable tracking and conditional branch support
- Memory optimization — GOMEMLIMIT + phased ParseResult release
- Embedded storage configuration wizard — `fcg init` now guides data_dir and buffer_pool_size for kuzu/ladybug
- Configurable buffer pool size for embedded databases (default 3GB)
- Persist embedded database data_dir in config

### Fix
- Windows 0 imports — `ResolveImports` now handles `\` path separators from `filepath.Rel`
- LadybugDB two-phase CreateNodes to avoid LIST column type conflict in UNWIND
- LadybugDB sanitizeSliceForLadybug complete numeric type coverage (`[]int64`, `[]float64`, `[]bool`, etc.)
- LadybugDB replace nil with empty list for array-type columns in UNWIND batch
- Show actual data_dir instead of "in-memory" for embedded databases in storage info
- Route chain bugfixes and Python ORM extraction

### Performance
- LadybugDB CreateEdges PreparedStatement caching — 286K edges from 286K Prepare calls down to 3-5
- LadybugDB PreparedStatement leak fix, atomic mergeNode, UNWIND batch CreateNodes

## [1.0.1] - 2026-05-12

### Feature 
- add LadybugDB as embedded storage backend alongside KuzuDB

### Fix
- eliminate duplicate Lombok accessor IDs for nested inner classes with same name


## [1.0.0] - 2026-05-01
The first verion

### Feature
- Multi-language — Java, Go, Python, TypeScript/JavaScript with framework-aware parsing
- Graph-based code intelligence — call chains, impact analysis, route tracing, annotation queries
- Cross-project call chains — trace calls across project boundaries through dependency configuration (FeignClient, Dubbo, gRPC)
- Incremental indexing — fingerprint-based change detection, only re-parses modified files
- Confidence-scored resolution — multi-strategy call resolution with 6-level confidence scoring
- Dual interface — CLI for interactive use, MCP Server for AI agent integration
- Pluggable storage — FalkorDB (remote) or KùzuDB (local embedded)