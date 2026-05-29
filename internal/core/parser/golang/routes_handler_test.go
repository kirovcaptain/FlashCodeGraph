package golang

import (
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

func parseGoTree(t *testing.T, code string) (*tree_sitter.Node, []byte, func()) {
	t.Helper()
	content := []byte(code)
	root, cleanup := parseGoCode(content)
	return root, content, cleanup
}

func TestResolveOneArg_Identifier(t *testing.T) {
	code := `package main
func setup() { r.GET("/path", GetUser) }`
	root, content, cleanup := parseGoTree(t, code)
	defer cleanup()
	
	// Find the identifier "GetUser" in arguments
	
	// Walk to find call_expression → arguments → identifier
	var found *tree_sitter.Node
	walkFind(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "identifier" && n.Utf8Text(content) == "GetUser" {
			found = n
			return false
		}
		return true
	})
	if found == nil {
		t.Fatal("could not find GetUser identifier")
	}
	result := resolveOneArg(found, content, nil)
	if len(result) != 1 || result[0] != "GetUser" {
		t.Errorf("expected [GetUser], got %v", result)
	}
}

func TestResolveOneArg_Selector(t *testing.T) {
	code := `package main
func setup() { r.GET("/path", ctrl.GetUser) }`
	root, content, cleanup := parseGoTree(t, code)
	defer cleanup()
	
	var found *tree_sitter.Node
	walkFind(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "selector_expression" && n.Utf8Text(content) == "ctrl.GetUser" {
			found = n
			return false
		}
		return true
	})
	if found == nil {
		t.Fatal("could not find ctrl.GetUser selector")
	}
	result := resolveOneArg(found, content, nil)
	if len(result) != 1 || result[0] != "ctrl.GetUser" {
		t.Errorf("expected [ctrl.GetUser], got %v", result)
	}
}

func TestResolveOneArg_CallExpression_SingleWrap(t *testing.T) {
	code := `package main
func setup() { r.GET("/path", auth(GetUser)) }`
	root, content, cleanup := parseGoTree(t, code)
	defer cleanup()
	
	// Find the outer call_expression "auth(GetUser)"
	var found *tree_sitter.Node
	walkFind(root, func(n *tree_sitter.Node) bool {
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
	result := resolveOneArg(found, content, nil)
	if len(result) != 2 || result[0] != "auth" || result[1] != "GetUser" {
		t.Errorf("expected [auth, GetUser], got %v", result)
	}
}

func TestResolveOneArg_CallExpression_MultiWrap(t *testing.T) {
	code := `package main
func setup() { r.GET("/path", auth(log(GetUser))) }`
	root, content, cleanup := parseGoTree(t, code)
	defer cleanup()
	
	var found *tree_sitter.Node
	walkFind(root, func(n *tree_sitter.Node) bool {
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
	result := resolveOneArg(found, content, nil)
	if len(result) != 3 || result[0] != "auth" || result[1] != "log" || result[2] != "GetUser" {
		t.Errorf("expected [auth, log, GetUser], got %v", result)
	}
}

func TestResolveOneArg_UnaryExpression(t *testing.T) {
	code := `package main
func setup() { pb.RegisterUserServiceServer(srv, &userHandler{}) }`
	root, content, cleanup := parseGoTree(t, code)
	defer cleanup()
	
	// Find the unary_expression "&userHandler{}"
	var found *tree_sitter.Node
	walkFind(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "unary_expression" && n.Utf8Text(content) == "&userHandler{}" {
			found = n
			return false
		}
		return true
	})
	if found == nil {
		t.Fatal("could not find &userHandler{}")
	}
	result := resolveOneArg(found, content, nil)
	if len(result) != 1 || result[0] != "userHandler" {
		t.Errorf("expected [userHandler], got %v", result)
	}
}

func TestResolveOneArg_CompositeLiteral(t *testing.T) {
	code := `package main
func setup() { pb.RegisterUserServiceServer(srv, userHandler{}) }`
	root, content, cleanup := parseGoTree(t, code)
	defer cleanup()
	
	var found *tree_sitter.Node
	walkFind(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "composite_literal" && n.Utf8Text(content) == "userHandler{}" {
			found = n
			return false
		}
		return true
	})
	if found == nil {
		t.Fatal("could not find userHandler{}")
	}
	result := resolveOneArg(found, content, nil)
	if len(result) != 1 || result[0] != "userHandler" {
		t.Errorf("expected [userHandler], got %v", result)
	}
}

func TestResolveHandlerArgs_MultipleParams(t *testing.T) {
	code := `package main
func setup() { r.GET("/path", auth, rateLimit, GetUser) }`
	root, content, cleanup := parseGoTree(t, code)
	defer cleanup()
	
	// Find the arguments node of r.GET(...)
	var argsNode *tree_sitter.Node
	walkFind(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "call_expression" {
			funcNode := n.ChildByFieldName("function")
			if funcNode != nil && funcNode.Kind() == "selector_expression" {
				field := funcNode.ChildByFieldName("field")
				if field != nil && field.Utf8Text(content) == "GET" {
					argsNode = n.ChildByFieldName("arguments")
					return false
				}
			}
		}
		return true
	})
	if argsNode == nil {
		t.Fatal("could not find r.GET arguments")
	}
	result := ResolveHandlerArgs(argsNode, content, nil)
	if len(result) != 3 || result[0] != "auth" || result[1] != "rateLimit" || result[2] != "GetUser" {
		t.Errorf("expected [auth, rateLimit, GetUser], got %v", result)
	}
}

func TestResolveHandlerArgs_WrapPlusSelector(t *testing.T) {
	code := `package main
func setup() { r.POST("/pay", auth(svc.Pay)) }`
	root, content, cleanup := parseGoTree(t, code)
	defer cleanup()
	
	var argsNode *tree_sitter.Node
	walkFind(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "call_expression" {
			funcNode := n.ChildByFieldName("function")
			if funcNode != nil && funcNode.Kind() == "selector_expression" {
				field := funcNode.ChildByFieldName("field")
				if field != nil && field.Utf8Text(content) == "POST" {
					argsNode = n.ChildByFieldName("arguments")
					return false
				}
			}
		}
		return true
	})
	if argsNode == nil {
		t.Fatal("could not find r.POST arguments")
	}
	result := ResolveHandlerArgs(argsNode, content, nil)
	if len(result) != 2 || result[0] != "auth" || result[1] != "svc.Pay" {
		t.Errorf("expected [auth, svc.Pay], got %v", result)
	}
}

func TestDetectRoute_ValidRoute(t *testing.T) {
	code := `package main
func setup() { r.GET("/users", GetUser) }`
	root, content, cleanup := parseGoTree(t, code)
	defer cleanup()
	
	var callNode *tree_sitter.Node
	walkFind(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "call_expression" {
			funcNode := n.ChildByFieldName("function")
			if funcNode != nil && funcNode.Kind() == "selector_expression" {
				field := funcNode.ChildByFieldName("field")
				if field != nil && field.Utf8Text(content) == "GET" {
					callNode = n
					return false
				}
			}
		}
		return true
	})
	if callNode == nil {
		t.Fatal("could not find r.GET call")
	}
	detection := DetectRoute(callNode, content, nil)
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

func TestDetectRoute_NonRoute(t *testing.T) {
	code := `package main
func setup() { svc.Process(ctx) }`
	root, content, cleanup := parseGoTree(t, code)
	defer cleanup()
	
	var callNode *tree_sitter.Node
	walkFind(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "call_expression" {
			callNode = n
			return false
		}
		return true
	})
	if callNode == nil {
		t.Fatal("could not find call")
	}
	detection := DetectRoute(callNode, content, nil)
	if detection != nil {
		t.Errorf("expected nil for non-route call, got %+v", detection)
	}
}

func TestDetectRoute_WithGroupPrefix(t *testing.T) {
	code := `package main
func setup() { ios.POST("/verify", handler) }`
	root, content, cleanup := parseGoTree(t, code)
	defer cleanup()
	
	var callNode *tree_sitter.Node
	walkFind(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "call_expression" {
			funcNode := n.ChildByFieldName("function")
			if funcNode != nil && funcNode.Kind() == "selector_expression" {
				field := funcNode.ChildByFieldName("field")
				if field != nil && field.Utf8Text(content) == "POST" {
					callNode = n
					return false
				}
			}
		}
		return true
	})
	if callNode == nil {
		t.Fatal("could not find ios.POST call")
	}
	prefixes := GroupPrefixes{"ios": "/ios"}
	detection := DetectRoute(callNode, content, prefixes)
	if detection == nil {
		t.Fatal("expected route detection, got nil")
	}
	if detection.PathPattern != "/ios/verify" {
		t.Errorf("expected /ios/verify, got %s", detection.PathPattern)
	}
}

// walkFind walks the AST and calls fn for each node. Stops when fn returns false.
func walkFind(node *tree_sitter.Node, fn func(*tree_sitter.Node) bool) {
	if !fn(node) {
		return
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child != nil {
			walkFind(child, fn)
		}
	}
}

func TestResolveHandlerArgs_MixedParamsAndWrap(t *testing.T) {
	// U-10: r.GET("/path", log, auth(GetUser))
	code := `package main
func setup() { r.GET("/path", log, auth(GetUser)) }`
	root, content, cleanup := parseGoTree(t, code)
	defer cleanup()
	var argsNode *tree_sitter.Node
	walkFind(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "call_expression" {
			funcNode := n.ChildByFieldName("function")
			if funcNode != nil && funcNode.Kind() == "selector_expression" {
				field := funcNode.ChildByFieldName("field")
				if field != nil && field.Utf8Text(content) == "GET" {
					argsNode = n.ChildByFieldName("arguments")
					return false
				}
			}
		}
		return true
	})
	if argsNode == nil {
		t.Fatal("could not find r.GET arguments")
	}
	result := ResolveHandlerArgs(argsNode, content, nil)
	if len(result) != 3 || result[0] != "log" || result[1] != "auth" || result[2] != "GetUser" {
		t.Errorf("expected [log, auth, GetUser], got %v", result)
	}
}

func TestResolveOneArg_GRPCRegister_AddressOf(t *testing.T) {
	// U-29: pb.RegisterUserServiceServer(srv, &userHandler{})
	code := `package main
func main() { pb.RegisterUserServiceServer(srv, &userHandler{}) }`
	root, content, cleanup := parseGoTree(t, code)
	defer cleanup()
	// Find the unary_expression &userHandler{}
	var found *tree_sitter.Node
	walkFind(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "unary_expression" {
			found = n
			return false
		}
		return true
	})
	if found == nil {
		t.Fatal("could not find &userHandler{}")
	}
	result := resolveOneArg(found, content, nil)
	if len(result) != 1 || result[0] != "userHandler" {
		t.Errorf("U-29: expected [userHandler], got %v", result)
	}
}

func TestResolveOneArg_GRPCRegister_Variable(t *testing.T) {
	// U-30: pb.RegisterUserServiceServer(srv, handler)
	code := `package main
func main() { pb.RegisterUserServiceServer(srv, handler) }`
	root, content, cleanup := parseGoTree(t, code)
	defer cleanup()
	// Find the second argument "handler" identifier
	var found *tree_sitter.Node
	walkFind(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "identifier" && n.Utf8Text(content) == "handler" {
			found = n
			return false
		}
		return true
	})
	if found == nil {
		t.Fatal("could not find handler identifier")
	}
	result := resolveOneArg(found, content, nil)
	if len(result) != 1 || result[0] != "handler" {
		t.Errorf("U-30: expected [handler], got %v", result)
	}
}

func TestResolveOneArg_GRPCRegister_Selector(t *testing.T) {
	// U-31: pb.RegisterUserServiceServer(srv, svc.handler)
	code := `package main
func main() { pb.RegisterUserServiceServer(srv, svc.handler) }`
	root, content, cleanup := parseGoTree(t, code)
	defer cleanup()
	var found *tree_sitter.Node
	walkFind(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "selector_expression" && n.Utf8Text(content) == "svc.handler" {
			found = n
			return false
		}
		return true
	})
	if found == nil {
		t.Fatal("could not find svc.handler")
	}
	result := resolveOneArg(found, content, nil)
	if len(result) != 1 || result[0] != "svc.handler" {
		t.Errorf("U-31: expected [svc.handler], got %v", result)
	}
}
