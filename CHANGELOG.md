# Changelog

## [1.0.3] - 2026-05-15

### Feature
- Embedded storage configuration wizard — `fcg init` now guides data_dir and buffer_pool_size for kuzu/ladybug
- Configurable buffer pool size for embedded databases (default 3GB)
- Persist embedded database data_dir in config

### Fix
- Windows 0 imports — `ResolveImports` now handles `\` path separators from `filepath.Rel`
- LadybugDB two-phase CreateNodes to avoid LIST column type conflict in UNWIND
- LadybugDB sanitizeSliceForLadybug complete numeric type coverage (`[]int64`, `[]float64`, `[]bool`, etc.)
- LadybugDB replace nil with empty list for array-type columns in UNWIND batch
- Show actual data_dir instead of "in-memory" for embedded databases in storage info

### Performance
- LadybugDB CreateEdges PreparedStatement caching — 286K edges from 286K Prepare calls down to 3-5
- LadybugDB PreparedStatement leak fix, atomic mergeNode, UNWIND batch CreateNodes

## [1.0.2] - 2026-05-13

### Feature
- Multi-language SQL extraction with shared variable tracking framework (Java, Go, Python, TypeScript)
- Java SQL extraction rewrite with variable tracking and conditional branch support
- Memory optimization — GOMEMLIMIT + phased ParseResult release

### Fix
- Route chain bugfixes and Python ORM extraction

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