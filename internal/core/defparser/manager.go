// Package defparser defines interfaces and managers for non-source file parsing.
package defparser

import "github.com/kirovcaptain/FlashCodeGraph/internal/model"

// DefParser parses non-source definition files (XML mappers, GraphQL schemas, etc).
type DefParser interface {
	// Extensions returns file extensions this parser handles.
	Extensions() []string

	// Detect checks if the file content belongs to this parser.
	Detect(content []byte) bool

	// Parse extracts data from the file content.
	Parse(input model.DefParseInput) *model.ParseResult
}

// Manager manages a set of DefParsers and provides unified access.
type Manager struct {
	parsers []DefParser
}

// NewManager creates an empty manager.
func NewManager() *Manager {
	return &Manager{}
}

// Register adds a parser to the manager.
func (manager *Manager) Register(p DefParser) {
	manager.parsers = append(manager.parsers, p)
}

// Extensions returns all file extensions from registered parsers (deduplicated).
func (manager *Manager) Extensions() []string {
	seen := make(map[string]bool)
	var exts []string
	for _, p := range manager.parsers {
		for _, ext := range p.Extensions() {
			if !seen[ext] {
				seen[ext] = true
				exts = append(exts, ext)
			}
		}
	}
	return exts
}

// Parse tries each registered parser: Detect then Parse. Returns nil if no parser matches.
func (manager *Manager) Parse(input model.DefParseInput) *model.ParseResult {
	for _, registeredParser := range manager.parsers {
		if registeredParser.Detect(input.Content) {
			return registeredParser.Parse(input)
		}
	}
	return nil
}

// HasParsers returns true if any parsers are registered.
func (manager *Manager) HasParsers() bool {
	return len(manager.parsers) > 0
}
