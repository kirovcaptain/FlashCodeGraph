package typescript

import "testing"

func TestExternalMethodManager_Lookup(t *testing.T) {
	manager := NewExternalMethodManager([]string{"react", "vue", "express", "axios"}, "/nonexistent")

	tests := []struct {
		className  string
		methodName string
		expected   string
	}{
		{"Router", "get", "Router"},
		{"Router", "post", "Router"},
		{"Response", "json", "Response"},
		{"Response", "status", "Response"},
		{"axios", "get", "AxiosResponse"},
		{"AxiosInstance", "post", "AxiosResponse"},
	}

	for _, tc := range tests {
		result, found := manager.Lookup(tc.className, tc.methodName)
		if !found {
			t.Errorf("Lookup(%s, %s): not found", tc.className, tc.methodName)
			continue
		}
		if result.Name != tc.expected {
			t.Errorf("Lookup(%s, %s): expected %q, got %q", tc.className, tc.methodName, tc.expected, result)
		}
	}
	t.Log("✅ ExternalMethodManager Lookup works for express/axios")
}

func TestExternalMethodManager_NotLoaded(t *testing.T) {
	// Only load react, should not find express methods
	manager := NewExternalMethodManager([]string{"react"}, "/nonexistent")

	_, found := manager.Lookup("Router", "get")
	if found {
		t.Error("should not find express Router.get when only react is loaded")
	}
	t.Log("✅ ExternalMethodManager correctly filters by framework")
}

func TestExternalMethodManager_Nil(t *testing.T) {
	var manager *ExternalMethodManager

	_, found := manager.Lookup("Router", "get")
	if found {
		t.Error("nil manager should return not found")
	}
	t.Log("✅ ExternalMethodManager nil-safe")
}
