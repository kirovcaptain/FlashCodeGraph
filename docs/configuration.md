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
| annotation_nodes | []string | [] | Extra annotation names to store as graph nodes |

### [annotations]

| Key | Type | Description |
|-----|------|-------------|
| include | []string | Extra annotation names to index (without `@`) |
| exclude | []string | Annotation names to skip |

### [embedding]

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| enabled | bool | false | Enable embedding generation |

### [system]

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| goroutines | int | 0 (auto) | Number of parallel workers |
| memory_limit | string | "1GB" | Memory limit |
| log_level | string | "info" | Log level: debug, info, warn, error |

### [cross_project_index]

Global config only (`~/.fcg/config.toml`). Controls the cross-project symbol and route index used for cross-service call chain resolution.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| backend | string | "json" | Index backend: `json` (default). `sqlite` is reserved for future use |

The index file is stored at `~/.fcg/cross_project_index.json`. It is automatically updated when a project is indexed (Step 9: writeCrossProjectIndex) and consumed by dependent projects during their indexing.

### [dependencies]

Project config only (`{project}/.fcg/config.toml`). Declares which other indexed projects this project depends on, enabling cross-project symbol resolution and cross-service call chain tracing.

#### [[dependencies.projects]]

Each entry declares a dependency on another indexed project:

| Key | Type | Description |
|-----|------|-------------|
| path | string | Absolute path to the dependent project |
| branch | string | Git branch of the dependent project |

#### [dependencies.properties]

Key-value map for resolving `${placeholder}` references in `@FeignClient` service names:

| Key | Type | Description |
|-----|------|-------------|
| *(arbitrary key)* | string | Placeholder name → resolved value |

#### Example

```toml
[[dependencies.projects]]
path = "/path/to/shared-api"
branch = "master"

[dependencies.properties]
"feign.payment.name" = "payment-service"
```

With this configuration:
- Symbols from common-server (classes, interfaces, methods) are injected into the project's symbol table before resolution, enabling the resolver to recognize types from jar dependencies (e.g. `@FeignClient` interfaces).
- `@FeignClient(name = "${feign.payermax.name}")` resolves to service name `pay-service` for cross-service route matching.
- Cross-project calls appear as `[cross-project]` nodes in call chains, with hints pointing to the source project for further tracing.

Dependencies are configured interactively via `fcg setup` (select from indexed projects, choose branches, add placeholder properties).

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
