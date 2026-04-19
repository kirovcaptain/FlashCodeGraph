package sqlutil

import (
	"testing"
)

func TestDetectQueryType(t *testing.T) {
	cases := []struct {
		sql  string
		want string
	}{
		{"SELECT * FROM users", "SELECT"},
		{"  select id from orders", "SELECT"},
		{"FROM users WHERE id = 1", "SELECT"},
		{"INSERT INTO users (name) VALUES ('test')", "INSERT"},
		{"UPDATE users SET active = true", "UPDATE"},
		{"DELETE FROM users WHERE id = 1", "DELETE"},
		{"TRUNCATE TABLE users", "UNKNOWN"},
		{"", "UNKNOWN"},
	}
	for _, tc := range cases {
		got := DetectQueryType(tc.sql)
		if got != tc.want {
			t.Errorf("DetectQueryType(%q) = %q, want %q", tc.sql, got, tc.want)
		}
	}
}

func TestExtractTablesFromSQL(t *testing.T) {
	cases := []struct {
		sql    string
		tables []string
	}{
		{"SELECT * FROM users WHERE id = 1", []string{"users"}},
		{"INSERT INTO orders (id) VALUES (1)", []string{"orders"}},
		{"UPDATE products SET price = 10", []string{"products"}},
		{"SELECT u.* FROM users u JOIN orders o ON u.id = o.user_id", []string{"users", "orders"}},
		{"DELETE FROM sessions WHERE expired = true", []string{"sessions"}},
		{"TRUNCATE TABLE foo", nil},
	}
	for _, tc := range cases {
		got := ExtractTablesFromSQL(tc.sql)
		if len(got) != len(tc.tables) {
			t.Errorf("ExtractTablesFromSQL(%q) = %v, want %v", tc.sql, got, tc.tables)
			continue
		}
		for i := range got {
			if got[i] != tc.tables[i] {
				t.Errorf("ExtractTablesFromSQL(%q)[%d] = %q, want %q", tc.sql, i, got[i], tc.tables[i])
			}
		}
	}
}

func TestIsSQLStatement(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"SELECT * FROM users", true},
		{"INSERT INTO users VALUES (1)", true},
		{"UPDATE users SET name = 'x'", true},
		{"DELETE FROM users", true},
		{"SELECT count FROM stats", true},
		{"hello world", false},
		{"SELECTFROM", false},
		{"", false},
		{"CREATE TABLE users", false},
	}
	for _, tc := range cases {
		got := IsSQLStatement(tc.text)
		if got != tc.want {
			t.Errorf("IsSQLStatement(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}
