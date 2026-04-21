package mcp

import (
	"testing"
)

// TestFilterCoreForestTree_ChainedExcludedNodes verifies that when an excluded
// node's promoted grandchild is also excluded, it is recursively removed rather
// than leaking into the output.
//
// Reproduces the bug: OrderContext.getStatus (getter) → OrderState.getStatus (getter)
// After filtering OrderContext.getStatus, OrderState.getStatus was promoted but
// not re-checked, so it appeared in core mode output with is_getter=true.
func TestFilterCoreForestTree_ChainedExcludedNodes(t *testing.T) {
	tree := map[string]any{
		"name": "main", "id": "root", "file_path": "Main.java",
		"children": []map[string]any{
			{
				"name": "next", "id": "next1", "file_path": "OrderContext.java",
				"children": []map[string]any{
					{"name": "handle", "id": "handle1", "file_path": "OrderState.java"},
				},
			},
			{
				// OrderContext.getStatus — excluded (getter), child also excluded
				"name": "getStatus", "id": "ctx-getStatus", "file_path": "OrderContext.java",
				"is_getter": true,
				"children": []map[string]any{
					{
						// OrderState.getStatus — also excluded (getter)
						"name": "getStatus", "id": "state-getStatus", "file_path": "OrderState.java",
						"is_getter": true,
					},
				},
			},
		},
	}

	result := filterCoreForestTree(tree)

	children, _ := result["children"].([]map[string]any)

	// Only "next" should survive; both getStatus nodes should be gone
	for _, child := range children {
		if child["name"] == "getStatus" {
			t.Errorf("chained excluded node leaked into output: %v", child)
		}
	}
	if len(children) != 1 || children[0]["name"] != "next" {
		t.Errorf("expected only [next], got %v", names(children))
	}
}

// TestFilterCoreForestTree_PromotedGrandchildKept verifies that a non-excluded
// grandchild of an excluded node is correctly promoted.
func TestFilterCoreForestTree_PromotedGrandchildKept(t *testing.T) {
	tree := map[string]any{
		"name": "root", "id": "r", "file_path": "A.java",
		"children": []map[string]any{
			{
				"name": "getUser", "id": "g1", "file_path": "Svc.java",
				"is_getter": true,
				"children": []map[string]any{
					{"name": "validate", "id": "v1", "file_path": "Svc.java"},
				},
			},
		},
	}

	result := filterCoreForestTree(tree)
	children, _ := result["children"].([]map[string]any)

	if len(children) != 1 || children[0]["name"] != "validate" {
		t.Errorf("expected promoted [validate], got %v", names(children))
	}
}

func names(nodes []map[string]any) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i], _ = n["name"].(string)
	}
	return out
}
