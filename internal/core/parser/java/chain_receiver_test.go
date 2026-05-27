package java

import (
	"testing"
)

func TestExtractCalls_ChainedCallReceiverExpr(t *testing.T) {
	// Verify how parser extracts "repo.findAll().get(0).getName()" as RawCalls
	code := `
package com.example;
import java.util.List;

public class Consumer {
    private Repository repo;

    public void directChainTest() {
        repo.findAll().get(0).getName();
    }
}
`
	result := parseJavaFile(t, code, "Consumer.java")

	for _, call := range result.Calls {
		if call.CalledName == "getName" {
			t.Logf("getName: ReceiverExpr=%q, CallerName=%q", call.ReceiverExpr, call.CallerName)
			return
		}
	}
	t.Fatal("getName call not found in parsed calls")
}
