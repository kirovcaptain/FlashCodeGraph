package java

import (
	"bufio"
	"os"
	"sort"
	"strings"
	"testing"
)

// TestJDKMethodKeysCompleteness (INT-17) verifies that all keys from the original
// jdkMethodReturns Go map are present in the new builtin JSON files.
func TestJDKMethodKeysCompleteness(t *testing.T) {
	// Load fixture: original jdkMethodReturns keys
	fixtureFile := "testdata/jdk_method_keys.txt"
	file, err := os.Open(fixtureFile)
	if err != nil {
		t.Fatalf("cannot open fixture %s: %v", fixtureFile, err)
	}
	defer file.Close()

	var expectedKeys []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			expectedKeys = append(expectedKeys, line)
		}
	}

	if len(expectedKeys) == 0 {
		t.Fatal("fixture file is empty")
	}

	// Load all builtin JSONs via ExternalMethodManager
	manager := NewExternalMethodManager(nil, "")

	// Build set of short-name keys from loaded methods (ClassName.MethodName format)
	loadedKeys := make(map[string]bool)
	for _, entry := range manager.methods {
		shortKey := shortClassName(entry.ClassName) + "." + entry.MethodName
		loadedKeys[shortKey] = true
	}

	// Check each fixture key exists in loaded data
	var missing []string
	for _, key := range expectedKeys {
		if !loadedKeys[key] {
			missing = append(missing, key)
		}
	}

	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("INT-17: %d keys from original jdkMethodReturns not found in builtin JSON:\n  %s",
			len(missing), strings.Join(missing[:min(20, len(missing))], "\n  "))
		if len(missing) > 20 {
			t.Errorf("  ... and %d more", len(missing)-20)
		}
	} else {
		t.Logf("✅ INT-17: all %d original jdkMethodReturns keys present in builtin JSON", len(expectedKeys))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
