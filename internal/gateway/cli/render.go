package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func printFilteredSymbols(filterType, filterValue string, nodes []model.Node) {
	if len(nodes) == 0 {
		fmt.Printf("No symbols found for %s=%q\n", filterType, filterValue)
		return
	}
	fmt.Printf("Found %d symbols with %s=%q:\n\n", len(nodes), filterType, filterValue)
	for _, node := range nodes {
		name, _ := node.Properties["name"].(string)
		filePath, _ := node.Properties["file_path"].(string)
		startLine := node.Properties["start_line"]
		fmt.Printf("  %-12s %-30s %s:%v\n", node.Kind, name, filePath, startLine)
	}
}

func formatParamsSummary(node model.Node) string {
	paramsJSON, _ := node.Properties["params"].(string)
	if paramsJSON == "" || paramsJSON == "null" {
		return "()"
	}
	var params []map[string]any
	if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
		return ""
	}
	var parts []string
	for _, param := range params {
		typeName, _ := param["type"].(string)
		paramName, _ := param["name"].(string)
		if typeName != "" {
			parts = append(parts, typeName)
		} else if paramName != "" {
			parts = append(parts, paramName)
		}
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

type foldedChild struct {
	callChild
	implCount int
	others    []string // other candidate target IDs
}

type callChild struct {
	targetID     string
	line         int
	name         string
	flowContext  string
	flowLine     int
	declaredType string
}

// isFoldableNode returns true if a node should be folded in core mode (accessor or external).
func isFoldableNode(nodeID string, nodeMap map[string]*model.Node) bool {
	node := nodeMap[nodeID]
	if node == nil {
		return false
	}
	if node.Properties["is_getter"] == true || node.Properties["is_setter"] == true {
		return true
	}
	filePath, _ := node.Properties["file_path"].(string)
	return filePath == constants.FilePathExternal || filePath == ""
}

func printCallTree(subgraph *model.Subgraph, rootName string, maxDepth int) {
	nodeMap := make(map[string]*model.Node)
	for i := range subgraph.Nodes {
		nodeMap[subgraph.Nodes[i].ID] = &subgraph.Nodes[i]
	}

	children := make(map[string][]callChild)
	childSet := make(map[string]bool)
	for _, edge := range subgraph.Edges {
		line := 0
		if l, ok := edge.Properties["line"]; ok {
			switch v := l.(type) {
			case int:
				line = v
			case float64:
				line = int(v)
			}
		}
		name := ""
		if n := nodeMap[edge.TargetID]; n != nil {
			name, _ = n.Properties["name"].(string)
		}
		flowCtx, _ := edge.Properties["flow_context"].(string)
		flowLine := 0
		if fl, ok := edge.Properties["flow_line"]; ok {
			switch v := fl.(type) {
			case int:
				flowLine = v
			case float64:
				flowLine = int(v)
			}
		}
		declaredType, _ := edge.Properties["declared_type"].(string)
		children[edge.SourceID] = append(children[edge.SourceID], callChild{edge.TargetID, line, name, flowCtx, flowLine, declaredType})
		childSet[edge.TargetID] = true
	}

	// Sort by line number
	for k := range children {
		c := children[k]
		sort.Slice(c, func(i, j int) bool { return c[i].line < c[j].line })
		children[k] = c
	}

	var roots []string
	// First node is always the queried root (prepended by caller)
	if len(subgraph.Nodes) > 0 {
		roots = append(roots, subgraph.Nodes[0].ID)
	}
	for _, node := range subgraph.Nodes[1:] {
		if !childSet[node.ID] && node.ID != subgraph.Nodes[0].ID {
			roots = append(roots, node.ID)
		}
	}

	if len(subgraph.Edges) == 0 {
		for _, node := range subgraph.Nodes {
			qn, _ := node.Properties["qualified_name"].(string)
			fp, _ := node.Properties["file_path"].(string)
			if qn == "" {
				qn, _ = node.Properties["name"].(string)
			}
			fmt.Printf("  → %-35s %s\n", qn, fp)
		}
		return
	}

	visited := make(map[string]bool)
	for _, rootID := range roots {
		printCallNode(rootID, nodeMap, children, visited, "", true, "", 0, maxDepth)
	}
}

func printCallNode(nodeID string, nodeMap map[string]*model.Node, children map[string][]callChild, visited map[string]bool, prefix string, isLast bool, declaredType string, currentDepth int, maxDepth int) {
	if visited[nodeID] {
		// Already shown elsewhere — print reference marker
		node := nodeMap[nodeID]
		name := nodeID
		if node != nil {
			if qn, ok := node.Properties["qualified_name"].(string); ok && qn != "" {
				name = qn
			} else {
				name, _ = node.Properties["name"].(string)
			}
		}
		connector := "├── "
		if isLast {
			connector = "└── "
		}
		fmt.Printf("%s%s%-40s (↑ see above)\n", prefix, connector, name)
		return
	}
	visited[nodeID] = true

	node := nodeMap[nodeID]
	name := nodeID
	filePath := ""
	if node != nil {
		name, _ = node.Properties["name"].(string)
		if qn, ok := node.Properties["qualified_name"].(string); ok && qn != "" {
			name = qn
		}
		filePath, _ = node.Properties["file_path"].(string)
	}
	if filePath == constants.FilePathExternal {
		name = name + " [external]"
		filePath = ""
	}
	if filePath == constants.FilePathCrossProject {
		name = name + " [cross-project]"
		filePath = ""
	}
	if filePath == constants.FilePathCrossService {
		name = "🌐 " + name + " [cross-service]"
		filePath = ""
	}

	connector := "├── "
	if isLast {
		connector = "└── "
	}
	via := ""
	if declaredType != "" {
		// Only show (via X) when declared type differs from target's owner class
		shortType := declaredType
		if idx := strings.LastIndex(declaredType, "."); idx >= 0 {
			shortType = declaredType[idx+1:]
		}
		ownerClass := ""
		if qn, _ := node.Properties["qualified_name"].(string); qn != "" {
			// Extract owner class from "pkg.OwnerClass.method"
			parts := strings.Split(qn, ".")
			if len(parts) >= 2 {
				ownerClass = parts[len(parts)-2]
			}
		}
		if shortType != ownerClass {
			via = fmt.Sprintf(" (via %s)", shortType)
		}
	}
	fmt.Printf("%s%s%s%s %s\n", prefix, connector, name, via, filePath)

	// Fold: group children by target ID, keep first, fold duplicates (same target at different lines)
	var folded []foldedChild
	seen := make(map[string]int) // targetID → index
	for _, cc := range children[nodeID] {
		if idx, exists := seen[cc.targetID]; exists {
			folded[idx].implCount++
		} else {
			seen[cc.targetID] = len(folded)
			folded = append(folded, foldedChild{cc, 0, nil})
		}
	}

	childPrefix := prefix + "│   "
	if isLast {
		childPrefix = prefix + "    "
	}

	// Stop expanding children if we've reached the requested depth
	if currentDepth >= maxDepth {
		return
	}

	if callchainFlow {
		renderWithFlow(folded, nodeMap, children, visited, childPrefix, currentDepth, maxDepth)
	} else if callchainMode != "full" {
		// Core mode: fold accessor/external children into summary lines
		var coreChildren []foldedChild
		foldedCount := 0
		for _, fc := range folded {
			if isFoldableNode(fc.targetID, nodeMap) {
				foldedCount++
			} else {
				coreChildren = append(coreChildren, fc)
			}
		}
		for i, fc := range coreChildren {
			last := i == len(coreChildren)-1 && foldedCount == 0
			printCallNode(fc.targetID, nodeMap, children, visited, childPrefix, last, fc.declaredType, currentDepth+1, maxDepth)
		}
		if foldedCount > 0 {
			connector := "└── "
			fmt.Printf("%s%s[%d accessors/externals folded]\n", childPrefix, connector, foldedCount)
		}
	} else {
		for i, fc := range folded {
			last := i == len(folded)-1
			printCallNode(fc.targetID, nodeMap, children, visited, childPrefix, last, fc.declaredType, currentDepth+1, maxDepth)
		}
	}
}

func printFoldedNode(fc foldedChild, nodeMap map[string]*model.Node, prefix string, isLast bool) {
	// Collect all candidate qualified names
	var names []string
	for _, id := range append([]string{fc.targetID}, fc.others...) {
		if n := nodeMap[id]; n != nil {
			if qn, ok := n.Properties["qualified_name"].(string); ok && qn != "" {
				// Extract class name: "com.dayu.common.ResponseResult.e" → "ResponseResult"
				parts := strings.Split(qn, ".")
				if len(parts) >= 2 {
					names = append(names, parts[len(parts)-2])
				} else {
					names = append(names, parts[0])
				}
			}
		}
	}

	fNode := nodeMap[fc.targetID]
	fPath := ""
	if fNode != nil {
		fPath, _ = fNode.Properties["file_path"].(string)
	}

	// Deduplicate names
	seen := make(map[string]bool)
	var unique []string
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			unique = append(unique, n)
		}
	}

	conn := "├── "
	if isLast {
		conn = "└── "
	}
	label := fmt.Sprintf("(%s).%s", strings.Join(unique, ","), fc.name)
	fmt.Printf("%s%s%-40s %s [candidate]\n", prefix, conn, label, fPath)
}

type vnode struct {
	label    string
	fc       *foldedChild
	children []*vnode
}

func renderWithFlow(folded []foldedChild, nodeMap map[string]*model.Node, children map[string][]callChild, visited map[string]bool, prefix string, currentDepth int, maxDepth int) {
	// Build a virtual tree: flow context segments become virtual nodes
	root := &vnode{}

	// Insert each folded child into the virtual tree based on flow context path
	for i := range folded {
		fc := &folded[i]
		segments := splitFlowContext(fc.flowContext)
		parent := root
		for _, seg := range segments {
			// Find or create virtual child
			var found *vnode
			for _, ch := range parent.children {
				if ch.label == seg && ch.fc == nil {
					found = ch
					break
				}
			}
			if found == nil {
				found = &vnode{label: seg}
				parent.children = append(parent.children, found)
			}
			parent = found
		}
		// Add real node as leaf
		parent.children = append(parent.children, &vnode{fc: fc})
	}

	// Render the virtual tree
	renderVNode(root, nodeMap, children, visited, prefix, currentDepth, maxDepth)
}

func splitFlowContext(ctx string) []string {
	if ctx == "" {
		return nil
	}
	return strings.Split(ctx, " > ")
}

func renderVNode(node *vnode, nodeMap map[string]*model.Node, children map[string][]callChild, visited map[string]bool, prefix string, currentDepth int, maxDepth int) {
	for i, ch := range node.children {
		last := i == len(node.children)-1
		if ch.fc != nil {
			// Real node
			if ch.fc.implCount > 0 {
				printFoldedNode(*ch.fc, nodeMap, prefix, last)
			} else {
				printCallNode(ch.fc.targetID, nodeMap, children, visited, prefix, last, ch.fc.declaredType, currentDepth+1, maxDepth)
			}
		} else {
			// Virtual flow node
			conn := "├── "
			if last {
				conn = "└── "
			}
			fmt.Printf("%s%s[%s]\n", prefix, conn, ch.label)
			childPrefix := prefix + "│   "
			if last {
				childPrefix = prefix + "    "
			}
			renderVNode(ch, nodeMap, children, visited, childPrefix, currentDepth, maxDepth)
		}
	}
}


