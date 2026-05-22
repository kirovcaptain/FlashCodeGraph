// Package astutil provides shared AST utility functions for all language extractors.
package astutil

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

// WalkNamedChildren iterates named children recursively.
// Return false from visitor to skip children of current node.
func WalkNamedChildren(node *tree_sitter.Node, visitor func(child *tree_sitter.Node) bool) {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if !child.IsNamed() {
			continue
		}
		if visitor(child) {
			WalkNamedChildren(child, visitor)
		}
	}
}

// NodeFieldText gets the text of a named field child.
func NodeFieldText(node *tree_sitter.Node, fieldName string, content []byte) string {
	child := node.ChildByFieldName(fieldName)
	if child == nil {
		return ""
	}
	return child.Utf8Text(content)
}

// FindChildByKind finds the first child with the given kind.
func FindChildByKind(node *tree_sitter.Node, kind string) *tree_sitter.Node {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == kind {
			return child
		}
	}
	return nil
}

// CollectChildrenByKind returns all children with the given kind.
func CollectChildrenByKind(node *tree_sitter.Node, kind string) []*tree_sitter.Node {
	var result []*tree_sitter.Node
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == kind {
			result = append(result, child)
		}
	}
	return result
}

// GenerateSymbolID creates a deterministic ID for a symbol.
func GenerateSymbolID(filePath, qualifiedName string, startLine int) string {
	raw := fmt.Sprintf("%s:%s:%d", filePath, qualifiedName, startLine)
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:8])
}

// ComputeHash returns SHA256 hex of content.
func ComputeHash(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

// FlowContext describes the control flow context of a call site.
type FlowContext struct {
	Kind string // "if", "else", "loop", "defer", "switch", "case xxx", ""
	Line int    // line of the control flow statement
}

// DetectFlowContext walks up from a node to find the nearest control flow ancestor.
// Collects nested contexts (e.g. "case \"kuzu\" > if").
func DetectFlowContext(node *tree_sitter.Node, content []byte) FlowContext {
	var contexts []FlowContext
	for n := node.Parent(); n != nil; n = n.Parent() {
		switch n.Kind() {
		case "if_statement", "if_expression":
			alt := n.ChildByFieldName("alternative")
			if alt != nil && isDescendant(node, alt) {
				contexts = append(contexts, FlowContext{"else", int(n.StartPosition().Row) + 1})
			} else {
				contexts = append(contexts, FlowContext{"if", int(n.StartPosition().Row) + 1})
			}
		case "for_statement", "for_range_statement", "enhanced_for_statement",
			"while_statement", "do_statement", "for_in_statement":
			contexts = append(contexts, FlowContext{"loop", int(n.StartPosition().Row) + 1})
		case "defer_statement":
			contexts = append(contexts, FlowContext{"defer", int(n.StartPosition().Row) + 1})
		case "lambda_expression":
			contexts = append(contexts, FlowContext{"lambda", int(n.StartPosition().Row) + 1})
		case "with_statement":
			contexts = append(contexts, FlowContext{"with", int(n.StartPosition().Row) + 1})
		case "elif_clause":
			contexts = append(contexts, FlowContext{"elif", int(n.StartPosition().Row) + 1})
		case "try_statement", "try_with_resources_statement":
			// Only add "try" if we haven't already added "catch" or "finally"
			// (catch/finally are children of try_statement, so skip the parent)
			alreadyInCatchOrFinally := false
			for _, ctx := range contexts {
				if ctx.Kind == "catch" || ctx.Kind == "finally" {
					alreadyInCatchOrFinally = true
					break
				}
			}
			if !alreadyInCatchOrFinally {
				contexts = append(contexts, FlowContext{"try", int(n.StartPosition().Row) + 1})
			}
		case "catch_clause", "except_clause":
			contexts = append(contexts, FlowContext{"catch", int(n.StartPosition().Row) + 1})
		case "finally_clause":
			contexts = append(contexts, FlowContext{"finally", int(n.StartPosition().Row) + 1})
		case "switch_statement", "switch_expression", "select_statement",
			"match_expression", "match_statement",
			"expression_switch_statement", "type_switch_statement":
			contexts = append(contexts, FlowContext{"switch", int(n.StartPosition().Row) + 1})
		case "expression_case", "type_case", "switch_case", "case_clause", "case_statement":
			label := "case"
			for j := uint(0); j < n.ChildCount(); j++ {
				c := n.Child(j)
				if c.IsNamed() {
					text := c.Utf8Text(content)
					if len(text) > 20 {
						text = text[:20] + "..."
					}
					label = "case " + text
					break
				}
			}
			contexts = append(contexts, FlowContext{label, int(n.StartPosition().Row) + 1})
		case "default_case":
			contexts = append(contexts, FlowContext{"default", int(n.StartPosition().Row) + 1})
		case "function_declaration", "method_declaration", "function_definition",
			"arrow_function", "lambda", "func_literal":
			goto done
		}
	}
done:
	if len(contexts) == 0 {
		return FlowContext{}
	}
	// Reverse: outermost first
	// Combine into single context: "case \"kuzu\" > if"
	combined := ""
	line := contexts[len(contexts)-1].Line
	for i := len(contexts) - 1; i >= 0; i-- {
		if combined != "" {
			combined += " - "
		}
		combined += contexts[i].Kind
	}
	return FlowContext{combined, line}
}

func isDescendant(child, ancestor *tree_sitter.Node) bool {
	aStart := ancestor.StartByte()
	aEnd := ancestor.EndByte()
	cStart := child.StartByte()
	return cStart >= aStart && cStart < aEnd
}

// BlockScope describes the block-level scope of a node within a function.
type BlockScope struct {
	ScopeKey     string            // e.g. "UserService.process#L3"
	ScopeParents map[string]string // new parent relationships discovered
}

// DetectBlockScope walks up from a node to determine its block-level scope key.
// Stops at function boundaries. Returns the callerName unchanged if not inside any block.
func DetectBlockScope(node *tree_sitter.Node, callerName string) BlockScope {
	var blockLines []int
	for current := node.Parent(); current != nil; current = current.Parent() {
		switch current.Kind() {
		case "if_statement", "if_expression":
			alternative := current.ChildByFieldName("alternative")
			if alternative != nil && isDescendant(node, alternative) {
				blockLines = append(blockLines, int(alternative.StartPosition().Row)+1)
			} else {
				consequence := current.ChildByFieldName("consequence")
				if consequence != nil {
					blockLines = append(blockLines, int(consequence.StartPosition().Row)+1)
				} else {
					blockLines = append(blockLines, int(current.StartPosition().Row)+1)
				}
			}
		case "elif_clause":
			blockLines = append(blockLines, int(current.StartPosition().Row)+1)
		case "try_statement", "try_with_resources_statement":
			body := current.ChildByFieldName("body")
			if body != nil && isDescendant(node, body) {
				blockLines = append(blockLines, int(body.StartPosition().Row)+1)
			}
		case "catch_clause", "except_clause", "finally_clause":
			blockLines = append(blockLines, int(current.StartPosition().Row)+1)
		case "for_statement", "for_range_statement", "enhanced_for_statement",
			"while_statement", "do_statement", "for_in_statement":
			blockLines = append(blockLines, int(current.StartPosition().Row)+1)
		case "switch_case", "case_clause", "expression_case", "type_case", "default_case":
			blockLines = append(blockLines, int(current.StartPosition().Row)+1)
		case "with_statement":
			blockLines = append(blockLines, int(current.StartPosition().Row)+1)
		case "function_declaration", "method_declaration", "function_definition",
			"arrow_function", "lambda", "func_literal":
			goto done
		}
	}
done:
	if len(blockLines) == 0 {
		return BlockScope{ScopeKey: callerName}
	}
	// blockLines collected inner-to-outer; reverse to build chain outer-to-inner
	parents := make(map[string]string)
	currentKey := callerName
	for i := len(blockLines) - 1; i >= 0; i-- {
		nextKey := currentKey + "#L" + strconv.Itoa(blockLines[i])
		parents[nextKey] = currentKey
		currentKey = nextKey
	}
	return BlockScope{ScopeKey: currentKey, ScopeParents: parents}
}
