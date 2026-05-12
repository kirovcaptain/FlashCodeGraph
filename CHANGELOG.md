# Changelog

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