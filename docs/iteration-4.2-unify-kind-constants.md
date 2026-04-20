# Iteration 4.2: Unify Kind Constants

## Problem
- `constants/symbol.go` defines lowercase kinds: `"function"`, `"class"`, `"interface"`
- Graph storage uses capitalized labels: `"Function"`, `"Class"`, `"Interface"`
- 570 occurrences of hardcoded strings across 48 files
- Indexer has unnecessary `capitalizeFirst` conversion logic

## Solution: Unify to capitalized values

### 1. Update constants

```go
// constants/symbol.go
package constants

// Kind — unified node kind, used across parser, storage, and query layers.
const (
    KindFunction  = "Function"
    KindClass     = "Class"
    KindInterface = "Interface"
)

// ParserKind — fine-grained parser output, mapped to Kind before storage.
const (
    ParserKindAbstractClass = "abstract_class"
    ParserKindEnum          = "enum"
    ParserKindVariable      = "variable"
)

// ClassType — sub-classification stored as node property.
const (
    ClassTypeClass     = "class"
    ClassTypeAbstract  = "abstract_class"
    ClassTypeInterface = "interface"
    ClassTypeEnum      = "enum"
    ClassTypeStruct    = "struct"
)
```

### 2. Simplify indexer conversion

```go
// Before: parser outputs "function" → indexer capitalizes to "Function"
// After: parser outputs "Function" directly, no conversion needed

// Only special cases need mapping:
func ParserKindToNodeKind(parserKind string) string {
    switch parserKind {
    case ParserKindAbstractClass, ParserKindEnum:
        return KindClass
    default:
        return parserKind // already "Function"/"Class"/"Interface"
    }
}
```

### 3. Replace hardcoded strings globally

Replace all `"Function"` → `constants.KindFunction`, `"Class"` → `constants.KindClass`, `"Interface"` → `constants.KindInterface` in non-test files.

## Impact
- DB data: **no change** (already stores capitalized values)
- Existing indexes: **no migration needed**
- Parser output: change from lowercase to capitalized
- `capitalizeFirst` function: **delete**

## Scope
- ~30 non-test files, ~200 replacements
- Tests: optional, can keep hardcoded strings

## Risk
- None. Pure refactor, no logic or data format change.
