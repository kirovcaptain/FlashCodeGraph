// Package parser provides tree-sitter based source code parsing.
package parser

import (
	"context"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_c "github.com/tree-sitter/tree-sitter-c/bindings/go"
	tree_sitter_cpp "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
	tree_sitter_csharp "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_php "github.com/tree-sitter/tree-sitter-php/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_ruby "github.com/tree-sitter/tree-sitter-ruby/bindings/go"
	tree_sitter_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

	// Languages without official Go bindings use smacker's

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/java"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/python"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/golang"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/typescript"
)

// newLanguage wraps unsafe.Pointer into a tree-sitter Language.
func newLanguage(ptr unsafe.Pointer) *tree_sitter.Language {
	return tree_sitter.NewLanguage(ptr)
}

// languages maps language names to tree-sitter Language objects.
// Grammar versions are locked via go.mod.
var languages = map[string]*tree_sitter.Language{
	"java":       newLanguage(tree_sitter_java.Language()),
	"python":     newLanguage(tree_sitter_python.Language()),
	"go":         newLanguage(tree_sitter_go.Language()),
	"javascript": newLanguage(tree_sitter_javascript.Language()),
	"typescript": newLanguage(unsafe.Pointer(tree_sitter_typescript.LanguageTypescript())),
	"rust":       newLanguage(tree_sitter_rust.Language()),
	"c":          newLanguage(tree_sitter_c.Language()),
	"cpp":        newLanguage(tree_sitter_cpp.Language()),
	"csharp":     newLanguage(tree_sitter_csharp.Language()),
	"ruby":       newLanguage(tree_sitter_ruby.Language()),
	"php":        newLanguage(unsafe.Pointer(tree_sitter_php.LanguagePHP())),
}

// Parser parses source files into ParseResult using tree-sitter.
// Each goroutine must use its own Parser instance (tree-sitter is not thread-safe).
type Parser struct {
	sitterParser *tree_sitter.Parser
	cacheDir     string // empty string disables caching
}

// New creates a Parser. Pass cacheDir="" to disable AST caching.
func New(cacheDir string) *Parser {
	return &Parser{
		sitterParser: tree_sitter.NewParser(),
		cacheDir:     cacheDir,
	}
}

// Close releases the parser's resources.
func (parser *Parser) Close() {
	parser.sitterParser.Close()
}

// SupportedLanguage returns true if the language has a tree-sitter grammar.
func SupportedLanguage(language string) bool {
	_, ok := languages[language]
	return ok
}

// ParseFile parses a single source file and returns extracted symbols, calls, etc.
func (parser *Parser) ParseFile(ctx context.Context, file scanner.ScannedFile, content []byte) (*model.ParseResult, error) {
	// XML files: handle without tree-sitter (MyBatis mapper detection)
	if file.Language == "xml" {
		return parser.parseXMLFile(file, content), nil
	}

	language, ok := languages[file.Language]
	if !ok {
		return nil, fmt.Errorf("parser: unsupported language: %s", file.Language)
	}

	// Check AST cache
	contentHash := astutil.ComputeHash(content)
	if parser.cacheDir != "" {
		if cached, err := parser.loadCache(contentHash); err == nil {
			cached.FilePath = file.RelPath
			return cached, nil
		}
	}

	// Parse with tree-sitter
	if err := parser.sitterParser.SetLanguage(language); err != nil {
		return nil, fmt.Errorf("parser: set language %s: %w", file.Language, err)
	}

	tree := parser.sitterParser.Parse(content, nil)
	if tree == nil {
		return nil, fmt.Errorf("parser: failed to parse %s", file.RelPath)
	}
	defer tree.Close()

	rootNode := tree.RootNode()

	result := &model.ParseResult{
		FilePath: file.RelPath,
		Language: file.Language,
	}

	extractFromAST(rootNode, content, file, result)

	// Stamp language on all RawCalls for resolver dispatch
	for i := range result.Calls {
		result.Calls[i].Language = file.Language
	}

	// Save to cache
	if parser.cacheDir != "" {
		parser.saveCache(contentHash, result)
	}

	return result, nil
}

// extractFromAST dispatches to language-specific extractors.
func extractFromAST(rootNode *tree_sitter.Node, content []byte, file scanner.ScannedFile, result *model.ParseResult) {
	switch file.Language {
	case "java":
		java.Extract(rootNode, content, file, result)
	case "python":
		python.Extract(rootNode, content, file, result)
	case "go":
		golang.Extract(rootNode, content, file, result)
	case "typescript", "javascript":
		typescript.Extract(rootNode, content, file, result)
	default:
		extractGeneric(rootNode, content, file, result)
	}
}


// Cache operations

func (parser *Parser) parseXMLFile(file scanner.ScannedFile, content []byte) *model.ParseResult {
	result := &model.ParseResult{
		FilePath: file.RelPath,
		Language: "xml",
	}
	// Try MyBatis mapper extraction
	queries := java.ExtractMybatisMapper(content, file.RelPath)
	result.Queries = append(result.Queries, queries...)
	return result
}

func (parser *Parser) cachePath(contentHash string) string {
	return filepath.Join(parser.cacheDir, contentHash[:2], contentHash+".gob")
}

func (parser *Parser) loadCache(contentHash string) (*model.ParseResult, error) {
	file, err := os.Open(parser.cachePath(contentHash))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var result model.ParseResult
	if err := gob.NewDecoder(file).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (parser *Parser) saveCache(contentHash string, result *model.ParseResult) {
	cachePath := parser.cachePath(contentHash)
	if err := os.MkdirAll(filepath.Dir(cachePath), model.DirectoryPermission); err != nil {
		return
	}
	file, err := os.Create(cachePath)
	if err != nil {
		return
	}
	defer file.Close()
	gob.NewEncoder(file).Encode(result)
}
