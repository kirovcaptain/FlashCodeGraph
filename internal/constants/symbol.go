package constants

// Symbol Kind — determines graph node type.
const (
	KindFunction      = "function"
	KindClass         = "class"
	KindAbstractClass = "abstract_class"
	KindInterface     = "interface"
	KindEnum          = "enum"
	KindVariable      = "variable"
)

// Class Type — sub-classification for Class nodes.
const (
	ClassTypeClass    = "class"
	ClassTypeAbstract = "abstract_class"
	ClassTypeInterface = "interface"
	ClassTypeEnum     = "enum"
	ClassTypeStruct   = "struct"
)

// SourceKind — used by CONTAINS edges to route to the correct relationship table.
const (
	SourceKindFile          = "File"
	SourceKindFileClass     = "FileClass"
	SourceKindFileInterface = "FileInterface"
	SourceKindClassFunc     = "ClassFunc"
)
