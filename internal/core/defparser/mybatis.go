package defparser

import (
	"bytes"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/java"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// MybatisParser parses MyBatis XML mapper files.
type MybatisParser struct{}

func (p *MybatisParser) Extensions() []string {
	return []string{".xml"}
}

func (p *MybatisParser) Detect(content []byte) bool {
	return bytes.Contains(content, []byte("<mapper"))
}

func (p *MybatisParser) Parse(content []byte, relPath string) *model.ParseResult {
	queries := java.ExtractMybatisMapper(content, relPath)
	if len(queries) == 0 {
		return nil
	}
	return &model.ParseResult{
		FilePath: relPath,
		Language: "xml",
		Queries:  queries,
	}
}
