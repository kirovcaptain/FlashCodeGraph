package cli

import (
	"strings"
	"testing"

	"github.com/liuymcn/flash-code-graph/internal/model"
)

func TestGenerateReportMarkdown_Basic(t *testing.T) {
	report := &model.GraphReport{
		NodeCounts: map[string]int{"Function": 10, "Class": 3},
		EdgeCounts: map[string]int{"CALLS": 15, "EXTENDS": 2},
		RouteDetails: []model.RouteDetail{
			{Method: "GET", PathPattern: "/api/users", Handler: "UserController.list"},
		},
		QueryDetails: []model.QueryDetail{
			{SQLText: "SELECT * FROM users", QueryType: "SELECT", Tables: "users", Caller: "repo.findAll"},
		},
	}

	md := generateReportMarkdown(report)

	checks := []string{
		"# FCG Data Quality Report",
		"| Function | 10 |",
		"| Class | 3 |",
		"| **Total** | **13** |",
		"| CALLS | 15 |",
		"## Routes (1)",
		"| GET | /api/users | UserController.list |",
		"## ORM Queries (1)",
		"| SELECT | users | repo.findAll |",
		"✅ No issues found",
	}
	for _, want := range checks {
		if !strings.Contains(md, want) {
			t.Errorf("missing in markdown: %q", want)
		}
	}
}

func TestGenerateReportMarkdown_WithIssues(t *testing.T) {
	report := &model.GraphReport{
		NodeCounts:     map[string]int{"Function": 5},
		DuplicateNodes: []string{"dup-1", "dup-2"},
		MissingFilePath: []string{"miss-1"},
		Issues:         []string{"2 duplicate nodes", "1 missing file_path"},
	}

	md := generateReportMarkdown(report)

	if strings.Contains(md, "✅ No issues found") {
		t.Error("should not show 'no issues' when issues exist")
	}
	if !strings.Contains(md, "⚠ 2 duplicate nodes") {
		t.Error("missing issue text")
	}
	if !strings.Contains(md, "`dup-1`") {
		t.Error("missing duplicate node detail")
	}
	if !strings.Contains(md, "`miss-1`") {
		t.Error("missing file_path detail")
	}
}

func TestGenerateReportMarkdown_Empty(t *testing.T) {
	report := &model.GraphReport{
		NodeCounts: map[string]int{},
	}
	md := generateReportMarkdown(report)
	if !strings.Contains(md, "| **Total** | **0** |") {
		t.Error("empty report should show total 0")
	}
}

func TestGenerateReportMarkdown_SQLPipeEscape(t *testing.T) {
	report := &model.GraphReport{
		NodeCounts: map[string]int{},
		QueryDetails: []model.QueryDetail{
			{SQLText: "SELECT a|b FROM t", QueryType: "SELECT", Tables: "t", Caller: "f"},
		},
	}
	md := generateReportMarkdown(report)
	if strings.Contains(md, "| a|b |") {
		t.Error("pipe in SQL should be escaped")
	}
	if !strings.Contains(md, `a\|b`) {
		t.Error("pipe should be escaped to \\|")
	}
}
