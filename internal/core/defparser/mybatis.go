package defparser

import (
	"bytes"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/java"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// MybatisParser parses MyBatis XML mapper files.
type MybatisParser struct{}

func (parser *MybatisParser) Extensions() []string {
	return []string{".xml"}
}

func (parser *MybatisParser) Detect(content []byte) bool {
	return bytes.Contains(content, []byte("<mapper"))
}

func (parser *MybatisParser) Parse(input model.DefParseInput) *model.ParseResult {
	queries := java.ExtractMybatisMapper(input.Content, input.RelPath)
	if len(queries) == 0 {
		return nil
	}
	return &model.ParseResult{
		FilePath: input.RelPath,
		Language: "xml",
		Queries:  queries,
	}
}
