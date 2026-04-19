package constants

// File category constants used by scanner and indexer.
const (
	FileSource    = "source"     // .java/.go/.py/.ts — AST parsed
	FileQueryDef  = "query_def"  // .xml (MyBatis mapper, etc.) — SQL extraction
	FileSchemaDef = "schema_def" // .graphql/.gql/.proto — schema extraction
)
