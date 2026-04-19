package parser

import (
	"context"
	"testing"

	"github.com/liuymcn/flash-code-graph/internal/core/scanner"
	"github.com/liuymcn/flash-code-graph/internal/core/typeinfer"
)

func TestLambdaParameterTypeInference(t *testing.T) {
	p := New("")
	defer p.Close()

	code := []byte(`package com.example;

import java.util.List;

public class UserService {
    private List<User> users;

    public void processAll() {
        users.forEach(user -> {
            user.save();
        });
    }
}
`)
	file := scanner.ScannedFile{Path: "/test/UserService.java", RelPath: "UserService.java", Language: "java"}
	result, err := p.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	t.Log("=== TypeHints ===")
	for _, h := range result.TypeHints {
		t.Logf("  scope=%s var=%s type=%s typeArgs=%v", h.Scope, h.VarName, h.TypeName, h.TypeArgs)
	}

	t.Log("=== Calls ===")
	for _, c := range result.Calls {
		t.Logf("  caller=%s called=%s receiver=%s line=%d", c.CallerName, c.CalledName, c.ReceiverExpr, c.Line)
	}

	infer := typeinfer.New()
	env := infer.InferLocal(result)

	t.Log("=== TypeEnv Bindings ===")
	for key, info := range env.Bindings {
		t.Logf("  %s → type=%s typeArgs=%v tier=%d", key, info.TypeName, info.TypeArgs, info.Tier)
	}

	// Check: does "user" have type info?
	found := false
	for key, info := range env.Bindings {
		if key == "UserService.processAll:user" && info.TypeName == "User" {
			found = true
			t.Log("✅ Lambda parameter 'user' inferred as User")
		}
	}
	if !found {
		t.Log("❌ Lambda parameter 'user' NOT inferred — gap to fix")
		// Check if 'users' field has TypeArgs
		for key, info := range env.Bindings {
			if info.TypeName == "List" || info.TypeName == "java.util.List" {
				t.Logf("  ℹ️  Container found: %s → %s typeArgs=%v", key, info.TypeName, info.TypeArgs)
			}
		}
	}
}
