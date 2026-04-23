# Adding a New Language to FlashCodeGraph

This guide walks through all the steps required to add full support for a new programming language. We use Go as the reference implementation.

## Overview

Adding a language requires changes in 4 layers:

```
1. Scanner     — file extension → language mapping
2. Parser      — Tree-sitter AST → ParseResult (symbols, calls, imports, heritage, routes, ORM, remote calls)
3. Resolver    — LanguageHelper for language-specific call resolution
4. Wiring      — connect everything in constants, parser dispatch, and indexer
```

## Step-by-Step

### Step 1: Register the Language

**`internal/constants/language.go`** — add a language identifier:

```go
const LangRust = "rust"
```

**`internal/core/scanner/scanner.go`** — add file extension mappings in `extensionToLanguage`:

```go
".rs": "rust",
```

**`internal/core/parser/parser.go`** — verify the Tree-sitter grammar is already in the `languages` map. For planned languages (Rust, C, C++, C#, Ruby, PHP), grammars are already imported. If adding a completely new language, add the grammar dependency to `go.mod` and register it in the `languages` map.

### Step 2: Create the Parser Extractor

Create a new package: `internal/core/parser/<lang>/`

The main entry point must implement:

```go
// internal/core/parser/<lang>/extract.go
package <lang>

func Extract(rootNode *tree_sitter.Node, content []byte, file scanner.ScannedFile, result *model.ParseResult) {
    // Walk the AST and populate result with:
    // - result.Symbols    (functions, classes, interfaces)
    // - result.Calls      (function/method calls)
    // - result.Imports    (import statements)
    // - result.Heritage   (extends/implements)
    // - result.TypeHints  (type annotations for type inference)
    // - result.PendingAssignments (unresolved assignments for fixpoint propagation)
}
```

#### What to Extract

| Field | What to Look For | Example (Go) |
|-------|-----------------|--------------|
| `Symbols` | Functions, methods, structs, interfaces, enums | `func_declaration`, `method_declaration`, `type_declaration` |
| `Calls` | Function/method calls with receiver, args | `call_expression` — extract `CalledName`, `ReceiverExpr`, `ArgCount` |
| `Imports` | Import statements | `import_declaration` → `ModulePath`, `Alias` |
| `Heritage` | Inheritance/implementation | `type A struct { B }` (embedding), `class A extends B` |
| `TypeHints` | Variable type annotations | `var x UserService`, `x: UserService` |
| `PendingAssignments` | Unresolved assignments for fixpoint | `x = y`, `x = foo()` |

#### Key Patterns

Use `astutil.WalkNamedChildren` for AST traversal:

```go
astutil.WalkNamedChildren(rootNode, func(node *tree_sitter.Node) bool {
    switch node.Kind() {
    case "function_declaration":
        // extract symbol
    case "call_expression":
        // extract call
    }
    return true // continue walking
})
```

Use `astutil.FindChildByKind` and `astutil.FindChildByFieldName` for node navigation.

Symbol IDs must be globally unique. Convention: `<file_path>::<qualified_name>`.

#### Optional Sub-Extractors

If the language has framework-specific patterns, create separate files:

| File | Purpose | When Needed |
|------|---------|-------------|
| `routes.go` | HTTP route extraction | Language has web frameworks (e.g. Gin, Express) |
| `remotecall.go` | HTTP/gRPC client call extraction | Language has HTTP client libraries |
| `orm.go` | Database query extraction | Language has ORM frameworks (e.g. GORM, SQLAlchemy) |

Each sub-extractor is called from the main `Extract` function after symbol extraction, passing the function body node for context.

### Step 3: Register Parser Dispatch

**`internal/core/parser/parser.go`** — add the language to `extractFromAST`:

```go
func extractFromAST(rootNode *tree_sitter.Node, content []byte, file scanner.ScannedFile, result *model.ParseResult) {
    switch file.Language {
    // ... existing languages ...
    case "rust":
        rust.Extract(rootNode, content, file, result)
    }
}
```

Import the new package:

```go
import "github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/rust"
```

### Step 4: Create the Resolver Helper

Create: `internal/core/resolver/<lang>/helper.go`

Implement the `resolver.LanguageHelper` interface:

```go
package <lang>

type Helper struct {
    // optional: symbolTable *resolver.SymbolTable
}

func NewHelper() *Helper {
    return &Helper{}
}
```

#### Interface Methods

| Method | Purpose | Applicable Scenarios | Guidance |
|--------|---------|---------------------|----------|
| `ResolveSuperCall` | Handle `super.method()` calls | Java `super.save()`, Python `super().__init__()`, C# `base.Method()`. Not applicable for Go (no inheritance) | Return `false` if language has no super calls |
| `NarrowByScope` | Filter candidates by visibility/import rules | Java: same package preference. Go: same directory = same package. Python: module-level imports. TS: ES module imports | Use package/module rules to narrow matches |
| `ResolveReceiverFallback` | Last-resort receiver resolution | Java: chained builder calls `builder.setName().build()`. Python: dynamic attribute access. TS: prototype chain calls | Language-specific heuristics when standard strategies fail |
| `ResolveImplicitSelfCall` | Handle implicit `this`/`self` | Java: `save()` inside a class (implicit `this.save()`). Python: methods called without `self` prefix in same class. Not applicable for Go (always explicit receiver) | Return `false` if language always requires explicit receiver |
| `ShouldFallthrough` | Allow fallback to no-receiver matching | Most languages: `true`. Java: `true` (static imports, same-class calls). Go: `true` (package-level functions) | `true` for most languages |
| `FilterGenerated` | Remove auto-generated symbols | Java: Lombok `@Data` generates getters/setters. Kotlin: data class `copy()`/`toString()`. Not applicable for Go/Python | Filter out synthetic methods to avoid false positives |
| `IsTypeAssignable` | Type compatibility check | Java: `int` ↔ `Integer` boxing, `List` assignable from `ArrayList`. Go: interface satisfaction. Python: duck typing (always `true`) | Handle boxing, inheritance, generics |
| `ResolveOverload` | Pick best overload | Java: `save(String)` vs `save(String, int)` — pick most specific. Not applicable for Go/Python (no overloading) | Return `nil` if language has no overloading |
| `InferStringConcat` | Detect string concatenation | Java: `"url" + path` should not resolve `+` as a call. JS/TS: template literals. Go: not applicable (no operator overloading) | Prevents false call resolution on `+` expressions |
| `LookupMethodReturn` | Return type of known methods | Java: `String.length()` → `int`, `List.get()` → element type. Go: `error.Error()` → `string`. Python: `dict.keys()` → `KeysView` | For standard library methods to enable chain resolution |
| `IsConstructor` | Identify constructor methods | Java: method name == class name. Python: `__init__`. Go: `NewXxx` convention. Rust: `new` / `from` | Language-specific naming convention |
| `IsOverrideMatch` | Check method override compatibility | Java: same name + compatible params + return type. Go: same method name on embedded struct. Python: same name in subclass | Compare name + params |
| `InferImplements` | Infer implicit interface implementations | Go: struct has all methods of an interface → implicit `IMPLEMENTS` edge. Not applicable for Java/Python/TS (explicit `implements` keyword) | Only needed for duck-typed languages. Return `nil` otherwise |

For a minimal starting point, most methods can return `nil`/`false` and be refined iteratively.

### Step 5: Wire the Resolver Helper

**`internal/service/indexer.go`** — add to `buildLanguageHelpers`:

```go
case constants.LangRust:
    helpers[constants.LangRust] = resolverrust.NewHelper()
```

Import the new package:

```go
import resolverrust "github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver/rust"
```

### Step 6: Add Framework Detection (Optional)

If the language has popular frameworks, add detection rules:

**`internal/constants/framework.go`** — add framework name constants:

```go
const Actix = "actix"
```

**`internal/core/framework/framework.go`** — add detection rules that map build file patterns to frameworks:

```go
// e.g., detect actix-web in Cargo.toml
```

### Step 7: Write Tests

#### Parser Tests

Create `internal/core/parser/<lang>/extract_test.go` and `internal/core/parser/extract_<lang>_test.go`:

```go
func TestExtractRustFunction(t *testing.T) {
    source := `fn hello(name: &str) -> String { ... }`
    result := parseSource(t, "rust", "test.rs", source)
    // Assert symbols, calls, imports, etc.
}
```

Test coverage should include:
- Functions, methods, structs/classes, interfaces/traits
- Function calls (with and without receiver)
- Import statements
- Inheritance/implementation
- Type annotations

#### Resolver Tests

Add test cases to `internal/core/resolver/resolver_test.go` covering:
- Same-file resolution
- Cross-file resolution with imports
- Receiver-based resolution
- Scope narrowing

### Step 8: Update Documentation

- `README.md` — move language from "Planned" to "Fully Supported" table
- `docs/architecture.md` — update Section 2 (Language Support)

## Checklist

```
[ ] constants/language.go — add LangXxx constant
[ ] scanner/scanner.go — add file extension mapping
[ ] parser/parser.go — verify grammar in languages map
[ ] parser/<lang>/extract.go — implement Extract()
[ ] parser/<lang>/routes.go — route extraction (if applicable)
[ ] parser/<lang>/remotecall.go — remote call extraction (if applicable)
[ ] parser/<lang>/orm.go — ORM query extraction (if applicable)
[ ] parser/parser.go — add case to extractFromAST
[ ] resolver/<lang>/helper.go — implement LanguageHelper
[ ] service/indexer.go — add case to buildLanguageHelpers
[ ] constants/framework.go — add framework constants (if applicable)
[ ] framework/framework.go — add detection rules (if applicable)
[ ] parser/<lang>/extract_test.go — parser tests
[ ] resolver/resolver_test.go — resolver test cases
[ ] README.md — update language support table
[ ] docs/architecture.md — update language support section
```

## Reference Implementations

| Language | Complexity | Good Reference For |
|----------|-----------|-------------------|
| Go | Simple | Minimal extractor, package-based scoping, duck-typed interfaces |
| Python | Medium | Decorator handling, implicit self, dynamic typing |
| TypeScript | Medium | Module system, shared JS/TS extractor, class + function styles |
| Java | Complex | Annotations, overloading, generics, deep framework integration |
