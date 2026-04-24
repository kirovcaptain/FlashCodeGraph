# Configuration

FCG uses TOML configuration files at two levels:

- Global: `~/.fcg/config.toml` — applies to all projects
- Project: `{project}/.fcg/config.toml` — overrides global settings

## Sections

### [project]

| Key | Type | Description |
|-----|------|-------------|
| name | string | Project name |
| type | string | Project type: maven, gradle, npm, go, cargo, dotnet, unknown |

### [storage]

| Key | Type | Description |
|-----|------|-------------|
| database | string | Storage backend: `falkordb` (default if available), `kuzu` |
| falkordb_uri | string | FalkorDB address (default: `localhost:6379`) |
| falkordb_graph | string | Graph name prefix (default: `fcg`) |
| kuzu_path | string | KùzuDB data directory |

### [index]

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| languages | []string | auto-detected | Languages to index |
| max_file_size | int | 524288 (512KB) | Skip files larger than this |
| exclude_tests | bool | true | Exclude test files from indexing |
| ignore | []string | [] | Additional glob patterns to ignore |

### [annotations]

| Key | Type | Description |
|-----|------|-------------|
| include | []string | Extra annotation names to index (without `@`) |
| exclude | []string | Annotation names to skip |

### [system]

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| goroutines | int | 0 (auto) | Number of parallel workers |
| memory_limit | string | "1GB" | Memory limit |
| log_level | string | "info" | Log level: debug, info, warn, error |

## Priority

Project config overrides global config. Unset fields fall back to global, then to defaults.
