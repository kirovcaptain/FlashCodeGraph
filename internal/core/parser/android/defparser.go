package android

import (
	"bytes"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// ManifestDefParser implements defparser.DefParser for AndroidManifest.xml.
type ManifestDefParser struct{}

func (parser *ManifestDefParser) Extensions() []string { return []string{".xml"} }

func (parser *ManifestDefParser) Detect(content []byte) bool {
	return bytes.Contains(content, []byte("<manifest"))
}

func (parser *ManifestDefParser) Parse(input model.DefParseInput) *model.ParseResult {
	result := &model.ParseResult{FilePath: input.RelPath, Language: "xml"}
	ExtractManifest(input.Content, input.RelPath, input.ModulePackage, result)
	if len(result.Symbols) == 0 {
		return nil
	}
	return result
}

// NavigationDefParser implements defparser.DefParser for navigation XML.
type NavigationDefParser struct{}

func (parser *NavigationDefParser) Extensions() []string { return []string{".xml"} }

func (parser *NavigationDefParser) Detect(content []byte) bool {
	return bytes.Contains(content, []byte("<navigation"))
}

func (parser *NavigationDefParser) Parse(input model.DefParseInput) *model.ParseResult {
	result := &model.ParseResult{FilePath: input.RelPath, Language: "xml"}
	ExtractNavigation(input.Content, input.RelPath, result)
	if len(result.Routes) == 0 {
		return nil
	}
	return result
}

// LayoutDefParser implements defparser.DefParser for layout XML.
type LayoutDefParser struct{}

func (parser *LayoutDefParser) Extensions() []string { return []string{".xml"} }

func (parser *LayoutDefParser) Detect(content []byte) bool {
	if bytes.Contains(content, []byte("<manifest")) || bytes.Contains(content, []byte("<navigation")) {
		return false
	}
	return bytes.Contains(content, []byte("xmlns:android"))
}

func (parser *LayoutDefParser) Parse(input model.DefParseInput) *model.ParseResult {
	result := &model.ParseResult{FilePath: input.RelPath, Language: "xml"}
	ExtractLayout(input.Content, input.RelPath, result)
	if len(result.Symbols) == 0 {
		return nil
	}
	return result
}
