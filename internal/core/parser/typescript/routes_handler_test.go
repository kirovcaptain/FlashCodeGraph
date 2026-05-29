package typescript

import (
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

func parseTSTree(t *testing.T, code string) (*tree_sitter.Node, []byte, func()) {
	t.Helper()
	content := []byte(code)
	root, cleanup := parse(content)
	return root, content, cleanup
}

func walkFindTS(node *tree_sitter.Node, fn func(*tree_sitter.Node) bool) {
	if !fn(node) {
		return
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child != nil {
			walkFindTS(child, fn)
		}
	}
}

func TestResolveTSOneArg_Identifier(t *testing.T) {
	// U-13
	code := `app.get("/path", getUser)`
	root, content, cleanup := parseTSTree(t, code)
	defer cleanup()
	var found *tree_sitter.Node
	walkFindTS(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "identifier" && n.Utf8Text(content) == "getUser" {
			found = n
			return false
		}
		return true
	})
	if found == nil {
		t.Fatal("could not find getUser identifier")
	}
	result := resolveTSOneArg(found, content, nil)
	if len(result) != 1 || result[0] != "getUser" {
		t.Errorf("expected [getUser], got %v", result)
	}
}

func TestResolveTSOneArg_MemberExpression(t *testing.T) {
	// U-14
	code := `app.get("/path", ctrl.getUser)`
	root, content, cleanup := parseTSTree(t, code)
	defer cleanup()
	var found *tree_sitter.Node
	walkFindTS(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "member_expression" && n.Utf8Text(content) == "ctrl.getUser" {
			found = n
			return false
		}
		return true
	})
	if found == nil {
		t.Fatal("could not find ctrl.getUser member_expression")
	}
	result := resolveTSOneArg(found, content, nil)
	if len(result) != 1 || result[0] != "ctrl.getUser" {
		t.Errorf("expected [ctrl.getUser], got %v", result)
	}
}

func TestResolveTSOneArg_SingleWrap(t *testing.T) {
	// U-16
	code := `app.get("/path", auth(getUser))`
	root, content, cleanup := parseTSTree(t, code)
	defer cleanup()
	var found *tree_sitter.Node
	walkFindTS(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "call_expression" {
			funcNode := n.ChildByFieldName("function")
			if funcNode != nil && funcNode.Kind() == "identifier" && funcNode.Utf8Text(content) == "auth" {
				found = n
				return false
			}
		}
		return true
	})
	if found == nil {
		t.Fatal("could not find auth(...) call")
	}
	result := resolveTSOneArg(found, content, nil)
	if len(result) != 2 || result[0] != "auth" || result[1] != "getUser" {
		t.Errorf("expected [auth, getUser], got %v", result)
	}
}

func TestResolveTSOneArg_MultiWrap(t *testing.T) {
	// U-17
	code := `app.get("/path", auth(log(getUser)))`
	root, content, cleanup := parseTSTree(t, code)
	defer cleanup()
	var found *tree_sitter.Node
	walkFindTS(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "call_expression" {
			funcNode := n.ChildByFieldName("function")
			if funcNode != nil && funcNode.Kind() == "identifier" && funcNode.Utf8Text(content) == "auth" {
				found = n
				return false
			}
		}
		return true
	})
	if found == nil {
		t.Fatal("could not find auth(log(...)) call")
	}
	result := resolveTSOneArg(found, content, nil)
	if len(result) != 3 || result[0] != "auth" || result[1] != "log" || result[2] != "getUser" {
		t.Errorf("expected [auth, log, getUser], got %v", result)
	}
}

func TestResolveTSHandlerArgs_MultipleParams(t *testing.T) {
	// U-18
	code := `app.get("/path", auth, validate, getUser)`
	root, content, cleanup := parseTSTree(t, code)
	defer cleanup()
	var argsNode *tree_sitter.Node
	walkFindTS(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "call_expression" {
			funcNode := n.ChildByFieldName("function")
			if funcNode != nil && funcNode.Kind() == "member_expression" {
				propNode := funcNode.ChildByFieldName("property")
				if propNode != nil && propNode.Utf8Text(content) == "get" {
					argsNode = n.ChildByFieldName("arguments")
					return false
				}
			}
		}
		return true
	})
	if argsNode == nil {
		t.Fatal("could not find app.get arguments")
	}
	result := ResolveTSHandlerArgs(argsNode, content, nil)
	if len(result) != 3 || result[0] != "auth" || result[1] != "validate" || result[2] != "getUser" {
		t.Errorf("expected [auth, validate, getUser], got %v", result)
	}
}

func TestResolveTSOneArg_AsyncHandler(t *testing.T) {
	// U-19
	code := `app.get("/path", asyncHandler(getUser))`
	root, content, cleanup := parseTSTree(t, code)
	defer cleanup()
	var found *tree_sitter.Node
	walkFindTS(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "call_expression" {
			funcNode := n.ChildByFieldName("function")
			if funcNode != nil && funcNode.Kind() == "identifier" && funcNode.Utf8Text(content) == "asyncHandler" {
				found = n
				return false
			}
		}
		return true
	})
	if found == nil {
		t.Fatal("could not find asyncHandler(...) call")
	}
	result := resolveTSOneArg(found, content, nil)
	if len(result) != 2 || result[0] != "asyncHandler" || result[1] != "getUser" {
		t.Errorf("expected [asyncHandler, getUser], got %v", result)
	}
}

func TestResolveTSHandlerArgs_WrapPlusMember(t *testing.T) {
	// U-20
	code := `app.post("/pay", auth(ctrl.pay))`
	root, content, cleanup := parseTSTree(t, code)
	defer cleanup()
	var argsNode *tree_sitter.Node
	walkFindTS(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "call_expression" {
			funcNode := n.ChildByFieldName("function")
			if funcNode != nil && funcNode.Kind() == "member_expression" {
				propNode := funcNode.ChildByFieldName("property")
				if propNode != nil && propNode.Utf8Text(content) == "post" {
					argsNode = n.ChildByFieldName("arguments")
					return false
				}
			}
		}
		return true
	})
	if argsNode == nil {
		t.Fatal("could not find app.post arguments")
	}
	result := ResolveTSHandlerArgs(argsNode, content, nil)
	if len(result) != 2 || result[0] != "auth" || result[1] != "ctrl.pay" {
		t.Errorf("expected [auth, ctrl.pay], got %v", result)
	}
}

func TestDetectTSRoute_Valid(t *testing.T) {
	code := `app.get("/users", getUser)`
	root, content, cleanup := parseTSTree(t, code)
	defer cleanup()
	var callNode *tree_sitter.Node
	walkFindTS(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "call_expression" {
			callNode = n
			return false
		}
		return true
	})
	if callNode == nil {
		t.Fatal("could not find call_expression")
	}
	detection := DetectTSRoute(callNode, content)
	if detection == nil {
		t.Fatal("expected route detection, got nil")
	}
	if detection.Method != "GET" {
		t.Errorf("expected GET, got %s", detection.Method)
	}
	if detection.PathPattern != "/users" {
		t.Errorf("expected /users, got %s", detection.PathPattern)
	}
}

func TestDetectTSRoute_InvalidReceiver(t *testing.T) {
	code := `svc.get("/path", handler)`
	root, content, cleanup := parseTSTree(t, code)
	defer cleanup()
	var callNode *tree_sitter.Node
	walkFindTS(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "call_expression" {
			callNode = n
			return false
		}
		return true
	})
	if callNode == nil {
		t.Fatal("could not find call_expression")
	}
	detection := DetectTSRoute(callNode, content)
	if detection != nil {
		t.Errorf("expected nil for invalid receiver, got %+v", detection)
	}
}
