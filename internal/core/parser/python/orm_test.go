package python

import (
	"strings"
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func parseAndExtractPythonSQL(t *testing.T, code string) *model.ParseResult {
	t.Helper()
	root, cleanup := parse([]byte(code))
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "test.py", Language: "python"}
	Extract(root, []byte(code), file, result)
	return result
}

func TestExtractSQL_Python_SimpleAssignment(t *testing.T) {
	code := `def query():
    sql = "select * from users"
`
	result := parseAndExtractPythonSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if query.SQLText == "select * from users" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'select * from users' query, got %d queries", len(result.Queries))
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}

func TestExtractSQL_Python_PlusConcatenation(t *testing.T) {
	code := `def query():
    sql = "select * from users" + " where status = 1"
`
	result := parseAndExtractPythonSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if query.SQLText == "select * from users where status = 1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected concatenated SQL query")
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}

func TestExtractSQL_Python_PlusEquals(t *testing.T) {
	code := `def query():
    sql = "select * from users where 1=1"
    sql += " and name like ?"
`
	result := parseAndExtractPythonSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "and name like ?") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected += appended SQL")
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}

func TestExtractSQL_Python_Reassignment(t *testing.T) {
	code := `def query():
    sql = "select * from temp"
    sql = "insert into users values(?)"
`
	result := parseAndExtractPythonSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if query.QueryType == "INSERT" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected INSERT after reassignment")
	}
}

func TestExtractSQL_Python_ConditionalBranch(t *testing.T) {
	code := `def query(name, age):
    sql = "select * from users where 1=1"
    if name:
        sql += " and name = ?"
    if age:
        sql += " and age = ?"
`
	result := parseAndExtractPythonSQL(t, code)
	var sqlQuery *model.RawQuery
	for index := range result.Queries {
		if strings.HasPrefix(result.Queries[index].SQLText, "select * from users") {
			sqlQuery = &result.Queries[index]
			break
		}
	}
	if sqlQuery == nil {
		t.Fatal("expected SQL query with conditions")
	}
	if sqlQuery.BaseSQL != "select * from users where 1=1" {
		t.Errorf("BaseSQL = %q", sqlQuery.BaseSQL)
	}
	if len(sqlQuery.Conditions) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(sqlQuery.Conditions))
	}
	if sqlQuery.Conditions[0].Fragment != " and name = ?" {
		t.Errorf("condition[0].Fragment = %q", sqlQuery.Conditions[0].Fragment)
	}
	if sqlQuery.Conditions[1].Fragment != " and age = ?" {
		t.Errorf("condition[1].Fragment = %q", sqlQuery.Conditions[1].Fragment)
	}
}

func TestExtractSQL_Python_IfElse(t *testing.T) {
	code := `def query(use_view):
    sql = "select * from "
    if use_view:
        sql += "v_user_summary"
    else:
        sql += "users"
`
	result := parseAndExtractPythonSQL(t, code)
	var sqlQuery *model.RawQuery
	for index := range result.Queries {
		if strings.HasPrefix(result.Queries[index].SQLText, "select * from ") {
			sqlQuery = &result.Queries[index]
			break
		}
	}
	if sqlQuery == nil {
		t.Fatal("expected SQL query with if/else")
	}
	if sqlQuery.BaseSQL != "select * from " {
		t.Errorf("BaseSQL = %q", sqlQuery.BaseSQL)
	}
	if len(sqlQuery.Conditions) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(sqlQuery.Conditions))
	}
	if sqlQuery.Conditions[0].IsElse {
		t.Error("condition[0] should not be IsElse")
	}
	if !sqlQuery.Conditions[1].IsElse {
		t.Error("condition[1] should be IsElse")
	}
}

func TestExtractSQL_Python_NestedIf(t *testing.T) {
	code := `def query(name, fuzzy):
    sql = "select * from users where 1=1"
    if name:
        if fuzzy:
            sql += " and name like ?"
        else:
            sql += " and name = ?"
`
	result := parseAndExtractPythonSQL(t, code)
	var sqlQuery *model.RawQuery
	for index := range result.Queries {
		if strings.HasPrefix(result.Queries[index].SQLText, "select * from users") {
			sqlQuery = &result.Queries[index]
			break
		}
	}
	if sqlQuery == nil {
		t.Fatal("expected SQL query with nested if")
	}
	if len(sqlQuery.Conditions) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(sqlQuery.Conditions))
	}
	if !strings.Contains(sqlQuery.Conditions[0].Condition, "&&") {
		t.Errorf("expected nested condition with &&, got %q", sqlQuery.Conditions[0].Condition)
	}
}

func TestExtractSQL_Python_TripleQuote(t *testing.T) {
	code := `def query():
    sql = """select * from users where status = 1"""
`
	result := parseAndExtractPythonSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "select * from users") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected triple-quoted SQL to be extracted")
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}

func TestExtractSQL_Python_NonSQLIgnored(t *testing.T) {
	code := `def run():
    msg = "hello world"
    path = "/api/users"
`
	result := parseAndExtractPythonSQL(t, code)
	for _, query := range result.Queries {
		if query.SQLText == "hello world" || query.SQLText == "/api/users" {
			t.Errorf("non-SQL string should not be extracted: %q", query.SQLText)
		}
	}
}

func TestExtractSQL_Python_NoDuplicate(t *testing.T) {
	code := `def query():
    sql = "select * from users where id = ?"
`
	result := parseAndExtractPythonSQL(t, code)
	count := 0
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "select * from users") {
			count++
		}
	}
	if count > 1 {
		t.Errorf("expected no duplicate SQL queries, got %d", count)
	}
}

func TestExtractSQL_Python_MultipleSQLVariables(t *testing.T) {
	code := `def query():
    select_sql = "select * from users"
    insert_sql = "insert into logs values(?)"
`
	result := parseAndExtractPythonSQL(t, code)
	foundSelect := false
	foundInsert := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "select * from users") {
			foundSelect = true
		}
		if strings.Contains(query.SQLText, "insert into logs") {
			foundInsert = true
		}
	}
	if !foundSelect {
		t.Error("expected SELECT query")
	}
	if !foundInsert {
		t.Error("expected INSERT query")
	}
}

func TestExtractSQL_Python_MultiplePlusEquals(t *testing.T) {
	code := `def query():
    sql = "select * from users where 1=1"
    sql += " and status = 1"
    sql += " and role = 'admin'"
    sql += " order by created_at"
`
	result := parseAndExtractPythonSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "status = 1") &&
			strings.Contains(query.SQLText, "role = 'admin'") &&
			strings.Contains(query.SQLText, "order by created_at") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected all += fragments to be joined")
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}

func TestExtractSQL_Python_ElifChain(t *testing.T) {
	code := `def query(sort_by):
    sql = "select * from users where 1=1"
    if sort_by == "name":
        sql += " order by name"
    elif sort_by == "age":
        sql += " order by age"
    else:
        sql += " order by id"
`
	result := parseAndExtractPythonSQL(t, code)
	var sqlQuery *model.RawQuery
	for index := range result.Queries {
		if strings.HasPrefix(result.Queries[index].SQLText, "select * from users") {
			sqlQuery = &result.Queries[index]
			break
		}
	}
	if sqlQuery == nil {
		t.Fatal("expected SQL query with elif chain")
	}
	if len(sqlQuery.Conditions) < 2 {
		t.Errorf("expected at least 2 conditions for elif chain, got %d", len(sqlQuery.Conditions))
		for index, condition := range sqlQuery.Conditions {
			t.Logf("  condition[%d]: condition=%q fragment=%q isElse=%v", index, condition.Condition, condition.Fragment, condition.IsElse)
		}
	}
}

func TestExtractSQL_Python_ForLoopBody(t *testing.T) {
	code := `def query(ids):
    sql = "select * from users where id in ("
    for i in ids:
        sql += "?,"
    sql += ")"
`
	result := parseAndExtractPythonSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "select * from users") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected SQL variable declared before for-loop to still be emitted")
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}

func TestExtractSQL_Python_ImplicitConcatenation(t *testing.T) {
	code := `def query():
    sql = ("select u.id, u.name "
           "from users u "
           "where u.active = true")
`
	result := parseAndExtractPythonSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "select u.id") && strings.Contains(query.SQLText, "from users") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected implicit string concatenation to resolve")
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}

func TestExtractSQL_Python_QueryTypeDetection(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected string
	}{
		{"SELECT", "def f():\n    sql = \"select * from users\"\n", "SELECT"},
		{"INSERT", "def f():\n    sql = \"insert into users(name) values(?)\"\n", "INSERT"},
		{"UPDATE", "def f():\n    sql = \"update users set name = ? where id = ?\"\n", "UPDATE"},
		{"DELETE", "def f():\n    sql = \"delete from users where id = ?\"\n", "DELETE"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			result := parseAndExtractPythonSQL(t, testCase.code)
			if len(result.Queries) == 0 {
				t.Fatal("expected at least 1 query")
			}
			if result.Queries[0].QueryType != testCase.expected {
				t.Errorf("expected QueryType=%s, got %s", testCase.expected, result.Queries[0].QueryType)
			}
		})
	}
}

func TestExtractSQL_Python_OnlyIfBranchModifiesSQL(t *testing.T) {
	code := `def query(limit):
    sql = "select * from users where 1=1"
    if limit > 0:
        sql += " limit ?"
`
	result := parseAndExtractPythonSQL(t, code)
	var sqlQuery *model.RawQuery
	for index := range result.Queries {
		if strings.HasPrefix(result.Queries[index].SQLText, "select * from users") {
			sqlQuery = &result.Queries[index]
			break
		}
	}
	if sqlQuery == nil {
		t.Fatal("expected SQL query")
	}
	if len(sqlQuery.Conditions) != 1 {
		t.Fatalf("expected 1 condition (no else), got %d", len(sqlQuery.Conditions))
	}
	if sqlQuery.Conditions[0].Fragment != " limit ?" {
		t.Errorf("condition fragment = %q", sqlQuery.Conditions[0].Fragment)
	}
}

func TestExtractSQL_Python_ReassignmentResetsFragments(t *testing.T) {
	code := `def query():
    sql = "select * from users"
    sql += " where active = true"
    sql = "delete from users where id = ?"
`
	result := parseAndExtractPythonSQL(t, code)
	if len(result.Queries) == 0 {
		t.Fatal("expected at least 1 query")
	}
	lastQuery := result.Queries[len(result.Queries)-1]
	if lastQuery.QueryType != "DELETE" {
		t.Errorf("expected final query to be DELETE after reassignment, got %s", lastQuery.QueryType)
	}
	if strings.Contains(lastQuery.SQLText, "active = true") {
		t.Error("reassignment should have reset fragments, but old fragment persists")
	}
}

func TestExtractSQL_Python_DynamicPlusEqualsPlaceholder(t *testing.T) {
	code := `def query():
    sql = "select * from users where 1=1"
    sql += build_condition()
`
	result := parseAndExtractPythonSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "select * from users") {
			found = true
			if !strings.Contains(query.SQLText, "?") {
				t.Error("expected dynamic += to insert ? placeholder")
			}
			break
		}
	}
	if !found {
		t.Error("expected SQL to be emitted even with dynamic +=")
	}
}

func TestExtractSQL_Python_PlusEqualsUntrackedIgnored(t *testing.T) {
	code := `def query():
    msg = "hello"
    msg += "select * from users"
`
	result := parseAndExtractPythonSQL(t, code)
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "select * from users") {
			t.Errorf("untracked non-SQL variable should not produce SQL query: %q", query.SQLText)
		}
	}
}

func TestExtractSQL_Python_MultiLineTripleQuote(t *testing.T) {
	code := `def query():
    sql = """
        select u.id, u.name
        from users u
        join orders o on u.id = o.user_id
        where o.total > 100
    """
`
	result := parseAndExtractPythonSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "select u.id") && strings.Contains(query.SQLText, "join orders") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected multi-line triple-quoted SQL to be extracted")
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}
