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

## Custom Method Mappings

FCG uses method return type mappings to improve call chain resolution and overload disambiguation for Java projects. Built-in mappings cover JDK, Spring, Guava, Apache Commons, and Hutool (loaded automatically based on framework detection).

To add custom mappings for frameworks not covered by built-in files, create JSON files in:

```
{project}/.fcg/external/*.json
```

### JSON Format

```json
{
  "ClassName.methodName": "ReturnType"
}
```

### Return Type Conventions

| Value | Meaning |
|-------|---------|
| `"String"` | Returns that concrete type, chain continues |
| `"SELF"` | Returns receiver type (builder/fluent pattern) |
| `"T"` | Returns container's first generic type arg |
| `"V"` | Returns Map's value type arg |
| `""` | Terminal operation (void/boolean/int), chain ends |

### Example

```json
{
  "MyUtils.toJson": "String",
  "MyBuilder.withName": "SELF",
  "MyBuilder.build": "MyObject",
  "MyUtils.isEmpty": ""
}
```

All `.json` files in `.fcg/external/` are loaded unconditionally (no framework detection required). User-defined mappings override built-in mappings for the same key.
