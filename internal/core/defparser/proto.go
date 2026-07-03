package defparser

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// ProtoService represents a gRPC service definition from a .proto file.
type ProtoService struct {
	Name    string
	Methods []ProtoRPC
	Line    int
}

// ProtoRPC represents an rpc method definition.
type ProtoRPC struct {
	Name       string
	InputType  string
	OutputType string
	Line       int
}

// ProtoParser parses .proto files to extract gRPC service and rpc definitions.
type ProtoParser struct{}

func (parser *ProtoParser) Extensions() []string {
	return []string{".proto"}
}

func (parser *ProtoParser) Detect(content []byte) bool {
	return bytes.Contains(content, []byte("service ")) && bytes.Contains(content, []byte("rpc "))
}

func (parser *ProtoParser) Parse(input model.DefParseInput) *model.ParseResult {
	services := ParseProtoServices(string(input.Content))
	if len(services) == 0 {
		return nil
	}
	result := &model.ParseResult{FilePath: input.RelPath, Language: "proto"}
	for _, service := range services {
		for _, rpc := range service.Methods {
			result.Routes = append(result.Routes, model.RawRoute{
				Method:      rpc.Name,
				PathPattern: service.Name,
				Framework:   "grpc",
				Handlers:    []string{service.Name + "." + rpc.Name},
				FilePath:    input.RelPath,
				Line:        rpc.Line,
			})
		}
	}
	return result
}

var (
	serviceHeaderRe = regexp.MustCompile(`^\s*service\s+(\w+)\s*\{`)
	rpcRe           = regexp.MustCompile(`^\s*rpc\s+(\w+)\s*\(\s*(\w+)\s*\)\s*returns\s*\(\s*(\w+)\s*\)`)
)

// ParseProtoServices extracts service and rpc definitions from proto file content.
// Handles comments and nested braces (e.g. google.api.http options).
func ParseProtoServices(content string) []ProtoService {
	lines := strings.Split(content, "\n")
	var services []ProtoService
	var currentService *ProtoService
	braceDepth := 0
	inBlockComment := false

	for lineNum, line := range lines {
		// Handle block comments
		if inBlockComment {
			if idx := strings.Index(line, "*/"); idx >= 0 {
				inBlockComment = false
				line = line[idx+2:]
			} else {
				continue
			}
		}
		if idx := strings.Index(line, "/*"); idx >= 0 {
			endIdx := strings.Index(line[idx+2:], "*/")
			if endIdx >= 0 {
				line = line[:idx] + line[idx+2+endIdx+2:]
			} else {
				inBlockComment = true
				line = line[:idx]
			}
		}

		// Strip line comments
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if currentService == nil {
			// Look for service header
			if m := serviceHeaderRe.FindStringSubmatch(line); m != nil {
				currentService = &ProtoService{
					Name: m[1],
					Line: lineNum + 1,
				}
				braceDepth = 1
			}
		} else {
			// Inside a service block — count braces
			for _, ch := range line {
				if ch == '{' {
					braceDepth++
				} else if ch == '}' {
					braceDepth--
				}
			}

			// Extract rpc at depth 1 (direct children of service)
			if m := rpcRe.FindStringSubmatch(line); m != nil {
				currentService.Methods = append(currentService.Methods, ProtoRPC{
					Name:       m[1],
					InputType:  m[2],
					OutputType: m[3],
					Line:       lineNum + 1,
				})
			}

			if braceDepth <= 0 {
				services = append(services, *currentService)
				currentService = nil
			}
		}
	}

	return services
}
