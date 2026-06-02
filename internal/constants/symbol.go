package constants

// Kind — unified node kind, used across parser, storage, and query layers.
const (
	KindFunction        = "Function"
	KindClass           = "Class"
	KindInterface       = "Interface"
	KindVariable        = "Variable"
	KindFile            = "File"
	KindDirectory       = "Directory"
	KindRepository      = "Repository"
	KindRoute           = "Route"
	KindQueryNode       = "QueryNode"
	KindCommunity       = "Community"
	KindProcess         = "Process"
	KindAnnotation      = "Annotation"
	KindExternalService = "ExternalService"
)

// AllNodeKinds is the complete list of all node kinds in the graph.
var AllNodeKinds = []string{
	KindFunction, KindClass, KindInterface, KindVariable,
	KindFile, KindDirectory, KindRepository,
	KindRoute, KindQueryNode, KindCommunity, KindProcess,
	KindAnnotation, KindExternalService,
}

// BaseSymbolKinds is the list of base code symbol kinds (Function, Class, Interface).
var BaseSymbolKinds = []string{KindFunction, KindClass, KindInterface}

// QualifiedNameKinds includes all node kinds that have a qualified_name property.
var QualifiedNameKinds = []string{KindFunction, KindClass, KindInterface, KindVariable}

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
	SourceKindFileVar       = "FileVar"
	SourceKindClassFunc      = "ClassFunc"
	SourceKindInterfaceFunc = "InterfaceFunc"
)

// FilePath special markers — used to distinguish non-source nodes in the graph.
const (
	// FilePathExternal marks nodes from third-party libraries (not in any indexed project).
	FilePathExternal = "[external]"

	// FilePathCrossProject marks nodes injected from cross-project index (dependency project symbols).
	FilePathCrossProject = "[cross-project]"

	// FilePathCrossService marks placeholder nodes created by Step 8 matchConsumerToProvider.
	FilePathCrossService = "[cross-service]"
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
	case ParserKindVariable:
		return KindVariable
	default:
		return parserKind
	}
}
