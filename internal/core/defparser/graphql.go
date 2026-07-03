package defparser

import (
	gqlParser "github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/graphql"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// GraphQLSchemaParser parses .graphql/.gql schema files.
type GraphQLSchemaParser struct{}

func (parser *GraphQLSchemaParser) Extensions() []string {
	return []string{".graphql", ".gql"}
}

func (parser *GraphQLSchemaParser) Detect(content []byte) bool {
	return len(content) > 0
}

func (parser *GraphQLSchemaParser) Parse(input model.DefParseInput) *model.ParseResult {
	operations := gqlParser.ParseSchema(string(input.Content))
	if len(operations) == 0 {
		return nil
	}
	result := &model.ParseResult{FilePath: input.RelPath, Language: "graphql"}
	for _, operation := range operations {
		result.Routes = append(result.Routes, model.RawRoute{
			Method:      operation.Field,
			PathPattern: operation.Type,
			Framework:   "graphql",
			Handlers:    []string{operation.Type + "." + operation.Field},
			FilePath:    input.RelPath,
		})
	}
	return result
}
