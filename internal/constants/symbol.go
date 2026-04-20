package constants

// Kind — unified node kind, used across parser, storage, and query layers.
const (
	KindFunction  = "Function"
	KindClass     = "Class"
	KindInterface = "Interface"
)

// ParserKind — fine-grained parser output, mapped to Kind before storage.
const (
	ParserKindFunction      = "function"
	ParserKindClass         = "class"
	ParserKindInterface     = "interface"
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

// SourceKind — used by CONTAINS edges to route to the correct relationship table.
const (
	SourceKindFile          = "File"
	SourceKindFileClass     = "FileClass"
	SourceKindFileInterface = "FileInterface"
	SourceKindClassFunc     = "ClassFunc"
)

// ParserKindToNodeKind maps parser-level kind to graph node kind.
func ParserKindToNodeKind(parserKind string) string {
	switch parserKind {
	case "function":
		return KindFunction
	case "class":
		return KindClass
	case "interface":
		return KindInterface
	case ParserKindAbstractClass, ParserKindEnum:
		return KindClass
	default:
		return parserKind
	}
}
