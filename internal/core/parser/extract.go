package parser

import (

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)


// extractGeneric provides basic extraction for unsupported languages. (Task 4.9)
func extractGeneric(rootNode *tree_sitter.Node, content []byte, file scanner.ScannedFile, result *model.ParseResult) {
	// TODO: implement in task 4.9
}
