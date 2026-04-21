package annotation

import "testing"

func TestLookupEntryType(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"Scheduled", "scheduled_task"},
		{"Cron", "scheduled_task"},
		{"XxlJob", "scheduled_task"},
		{"EventListener", "event_handler"},
		{"KafkaListener", "event_handler"},
		{"RabbitListener", "event_handler"},
		{"DubboService", "rpc_handler"},
		{"Transactional", ""},
		{"Service", ""},
		{"NonExistent", ""},
	}
	for _, tt := range tests {
		got := LookupEntryType(tt.name)
		if got != tt.expected {
			t.Errorf("LookupEntryType(%q) = %q, want %q", tt.name, got, tt.expected)
		}
	}
}

func TestListCategories(t *testing.T) {
	categories := ListCategories()
	if len(categories) == 0 {
		t.Fatal("expected non-empty categories")
	}
	// Verify sorted
	for i := 1; i < len(categories); i++ {
		if categories[i] < categories[i-1] {
			t.Errorf("categories not sorted: %v", categories)
			break
		}
	}
	// Verify fine-grained categories exist
	expected := map[string]bool{"scheduled": false, "transaction": false, "cache": false, "event_listener": false, "layer": false}
	for _, c := range categories {
		if _, ok := expected[c]; ok {
			expected[c] = true
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("expected category %q not found in %v", name, categories)
		}
	}
	// Verify "behavior" no longer exists
	for _, c := range categories {
		if c == "behavior" {
			t.Error("category 'behavior' should have been fine-grained into scheduled/transaction/etc")
		}
	}
}
