package defparser

import (
	gqlParser "github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/graphql"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// GraphQLSchemaParser parses .graphql/.gql schema files.
type GraphQLSchemaParser struct{}

func (p *GraphQLSchemaParser) Extensions() []string {
	return []string{".graphql", ".gql"}
}

func (p *GraphQLSchemaParser) Detect(content []byte) bool {
	// GraphQL schemas contain type definitions
	return len(content) > 0 // extension-based filtering is sufficient
}

func (p *GraphQLSchemaParser) Parse(content []byte, relPath string) *model.ParseResult {
	ops := gqlParser.ParseSchema(string(content))
	if len(ops) == 0 {
		return nil
	}
	pr := &model.ParseResult{FilePath: relPath, Language: "graphql"}
	for _, op := range ops {
		pr.Routes = append(pr.Routes, model.RawRoute{
			Method:      op.Field,
			PathPattern: op.Type,
			Framework:   "graphql",
			Handlers: []string{op.Type + "." + op.Field},
			FilePath:    relPath,
		})
	}
	return pr
}
