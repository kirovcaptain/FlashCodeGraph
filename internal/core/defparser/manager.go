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
	Parse(content []byte, relPath string) *model.ParseResult
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
func (m *Manager) Register(p DefParser) {
	m.parsers = append(m.parsers, p)
}

// Extensions returns all file extensions from registered parsers (deduplicated).
func (m *Manager) Extensions() []string {
	seen := make(map[string]bool)
	var exts []string
	for _, p := range m.parsers {
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
func (m *Manager) Parse(content []byte, relPath string) *model.ParseResult {
	for _, p := range m.parsers {
		if p.Detect(content) {
			return p.Parse(content, relPath)
		}
	}
	return nil
}

// HasParsers returns true if any parsers are registered.
func (m *Manager) HasParsers() bool {
	return len(m.parsers) > 0
}
