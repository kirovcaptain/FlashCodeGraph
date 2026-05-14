package typescript

import (
	"strings"
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func parseAndExtractTSSQL(t *testing.T, code string) *model.ParseResult {
	t.Helper()
	root, cleanup := parse([]byte(code))
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "test.ts", Language: "typescript"}
	Extract(root, []byte(code), file, result)
	return result
}

func TestExtractSQL_TS_SimpleDeclaration(t *testing.T) {
	code := `function query() {
  let sql = "select * from users";
}`
	result := parseAndExtractTSSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if query.SQLText == "select * from users" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected SQL query, got %d queries", len(result.Queries))
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}

func TestExtractSQL_TS_PlusConcatenation(t *testing.T) {
	code := `function query() {
  let sql = "select * from users" + " where status = 1";
}`
	result := parseAndExtractTSSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if query.SQLText == "select * from users where status = 1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected concatenated SQL")
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}

func TestExtractSQL_TS_PlusEquals(t *testing.T) {
	code := `function query() {
  let sql = "select * from users where 1=1";
  sql += " and name like ?";
}`
	result := parseAndExtractTSSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "and name like ?") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected += appended SQL")
	}
}

func TestExtractSQL_TS_Reassignment(t *testing.T) {
	code := `function query() {
  let sql = "select * from temp";
  sql = "insert into users values(?)";
}`
	result := parseAndExtractTSSQL(t, code)
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

func TestExtractSQL_TS_ConditionalBranch(t *testing.T) {
	code := `function query(name: string, age: number) {
  let sql = "select * from users where 1=1";
  if (name) {
    sql += " and name = ?";
  }
  if (age) {
    sql += " and age = ?";
  }
}`
	result := parseAndExtractTSSQL(t, code)
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
}

func TestExtractSQL_TS_IfElse(t *testing.T) {
	code := `function query(useView: boolean) {
  let sql = "select * from ";
  if (useView) {
    sql += "v_user_summary";
  } else {
    sql += "users";
  }
}`
	result := parseAndExtractTSSQL(t, code)
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

func TestExtractSQL_TS_NestedIf(t *testing.T) {
	code := `function query(name: string, fuzzy: boolean) {
  let sql = "select * from users where 1=1";
  if (name) {
    if (fuzzy) {
      sql += " and name like ?";
    } else {
      sql += " and name = ?";
    }
  }
}`
	result := parseAndExtractTSSQL(t, code)
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

func TestExtractSQL_TS_DirectArgument(t *testing.T) {
	code := `function query() {
  db.query("select count(*) from users");
}`
	result := parseAndExtractTSSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "query") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected db.query ORM call to be detected")
	}
}

func TestExtractSQL_TS_NonSQLIgnored(t *testing.T) {
	code := `function run() {
  let msg = "hello world";
  let path = "/api/users";
}`
	result := parseAndExtractTSSQL(t, code)
	for _, query := range result.Queries {
		if query.SQLText == "hello world" || query.SQLText == "/api/users" {
			t.Errorf("non-SQL string should not be extracted: %q", query.SQLText)
		}
	}
}

func TestExtractSQL_TS_MultipleSQLVariables(t *testing.T) {
	code := `function query() {
  let selectSQL = "select * from users";
  let insertSQL = "insert into logs values(?)";
}`
	result := parseAndExtractTSSQL(t, code)
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

func TestExtractSQL_TS_TripleConcatenation(t *testing.T) {
	code := `function query() {
  let sql = "select * " + "from users " + "where id = ?";
}`
	result := parseAndExtractTSSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "select * from users where id = ?") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected triple concatenation to resolve")
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}

func TestExtractSQL_TS_MultiplePlusEquals(t *testing.T) {
	code := `function query() {
  let sql = "select * from users where 1=1";
  sql += " and status = 1";
  sql += " and role = 'admin'";
  sql += " order by created_at";
}`
	result := parseAndExtractTSSQL(t, code)
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

func TestExtractSQL_TS_ElseIfChain(t *testing.T) {
	code := `function query(sortBy: string) {
  let sql = "select * from users where 1=1";
  if (sortBy === "name") {
    sql += " order by name";
  } else if (sortBy === "age") {
    sql += " order by age";
  } else {
    sql += " order by id";
  }
}`
	result := parseAndExtractTSSQL(t, code)
	var sqlQuery *model.RawQuery
	for index := range result.Queries {
		if strings.HasPrefix(result.Queries[index].SQLText, "select * from users") {
			sqlQuery = &result.Queries[index]
			break
		}
	}
	if sqlQuery == nil {
		t.Fatal("expected SQL query with else-if chain")
	}
	if len(sqlQuery.Conditions) < 2 {
		t.Errorf("expected at least 2 conditions for else-if chain, got %d", len(sqlQuery.Conditions))
		for index, condition := range sqlQuery.Conditions {
			t.Logf("  condition[%d]: condition=%q fragment=%q isElse=%v", index, condition.Condition, condition.Fragment, condition.IsElse)
		}
	}
}

func TestExtractSQL_TS_ForLoopBody(t *testing.T) {
	code := `function query(ids: number[]) {
  let sql = "select * from users where id in (";
  for (let i = 0; i < ids.length; i++) {
    sql += "?,";
  }
  sql += ")";
}`
	result := parseAndExtractTSSQL(t, code)
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

func TestExtractSQL_TS_SwitchCaseBody(t *testing.T) {
	code := `function query(action: string) {
  let sql = "select * from users where 1=1";
  switch (action) {
    case "active":
      sql += " and active = true";
      break;
    case "deleted":
      sql += " and deleted_at is not null";
      break;
  }
}`
	result := parseAndExtractTSSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "select * from users") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected SQL variable declared before switch to still be emitted")
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}

func TestExtractSQL_TS_ConstDeclaration(t *testing.T) {
	code := `function query() {
  const sql = "select * from users where active = true";
}`
	result := parseAndExtractTSSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "select * from users") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected const declaration to be tracked")
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}

func TestExtractSQL_TS_VarDeclaration(t *testing.T) {
	code := `function query() {
  var sql = "select * from users";
}`
	result := parseAndExtractTSSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "select * from users") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected var declaration to be tracked")
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}

func TestExtractSQL_TS_QueryTypeDetection(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected string
	}{
		{"SELECT", `function f() { let sql = "select * from users"; }`, "SELECT"},
		{"INSERT", `function f() { let sql = "insert into users(name) values(?)"; }`, "INSERT"},
		{"UPDATE", `function f() { let sql = "update users set name = ? where id = ?"; }`, "UPDATE"},
		{"DELETE", `function f() { let sql = "delete from users where id = ?"; }`, "DELETE"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			result := parseAndExtractTSSQL(t, testCase.code)
			if len(result.Queries) == 0 {
				t.Fatal("expected at least 1 query")
			}
			if result.Queries[0].QueryType != testCase.expected {
				t.Errorf("expected QueryType=%s, got %s", testCase.expected, result.Queries[0].QueryType)
			}
		})
	}
}

func TestExtractSQL_TS_NoDuplicate(t *testing.T) {
	code := `function query() {
  let sql = "select * from users where id = ?";
}`
	result := parseAndExtractTSSQL(t, code)
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

func TestExtractSQL_TS_OnlyIfBranchModifiesSQL(t *testing.T) {
	code := `function query(limit: number) {
  let sql = "select * from users where 1=1";
  if (limit > 0) {
    sql += " limit ?";
  }
}`
	result := parseAndExtractTSSQL(t, code)
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

func TestExtractSQL_TS_ReassignmentResetsFragments(t *testing.T) {
	code := `function query() {
  let sql = "select * from users";
  sql += " where active = true";
  sql = "delete from users where id = ?";
}`
	result := parseAndExtractTSSQL(t, code)
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

func TestExtractSQL_TS_DynamicPlusEqualsPlaceholder(t *testing.T) {
	code := `function query() {
  let sql = "select * from users where 1=1";
  sql += buildCondition();
}`
	result := parseAndExtractTSSQL(t, code)
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

func TestExtractSQL_TS_PlusEqualsUntrackedIgnored(t *testing.T) {
	code := `function query() {
  let msg = "hello";
  msg += "select * from users";
}`
	result := parseAndExtractTSSQL(t, code)
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "select * from users") {
			t.Errorf("untracked non-SQL variable should not produce SQL query: %q", query.SQLText)
		}
	}
}

func TestExtractSQL_TS_MultiLineConcat(t *testing.T) {
	code := `function query() {
  let sql = "select u.id, u.name " +
    "from users u " +
    "join orders o on u.id = o.user_id " +
    "where o.total > 100";
}`
	result := parseAndExtractTSSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "join orders") && strings.Contains(query.SQLText, "select u.id") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected multi-line concatenated SQL to resolve")
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}
