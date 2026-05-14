package golang

import (
	"strings"
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func parseAndExtractGoSQL(t *testing.T, code string) *model.ParseResult {
	t.Helper()
	root, cleanup := parse([]byte(code))
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "test.go", Language: "go"}
	Extract(root, []byte(code), file, result)
	return result
}

func TestExtractSQL_Go_SimpleAssignment(t *testing.T) {
	code := `package main
func query() {
	sql := "select * from users"
	_ = sql
}`
	result := parseAndExtractGoSQL(t, code)
	if len(result.Queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(result.Queries))
	}
	if result.Queries[0].SQLText != "select * from users" {
		t.Errorf("SQLText = %q", result.Queries[0].SQLText)
	}
}

func TestExtractSQL_Go_PlusConcatenation(t *testing.T) {
	code := `package main
func query() {
	sql := "select * from users" + " where status = 1"
	_ = sql
}`
	result := parseAndExtractGoSQL(t, code)
	if len(result.Queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(result.Queries))
	}
	if result.Queries[0].SQLText != "select * from users where status = 1" {
		t.Errorf("SQLText = %q", result.Queries[0].SQLText)
	}
}

func TestExtractSQL_Go_PlusEquals(t *testing.T) {
	code := `package main
func query() {
	sql := "select * from users where 1=1"
	sql += " and name like ?"
	_ = sql
}`
	result := parseAndExtractGoSQL(t, code)
	if len(result.Queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(result.Queries))
	}
	if !strings.Contains(result.Queries[0].SQLText, "and name like ?") {
		t.Errorf("SQLText missing += part: %q", result.Queries[0].SQLText)
	}
}

func TestExtractSQL_Go_Reassignment(t *testing.T) {
	code := `package main
func query() {
	sql := "select * from temp"
	sql = "insert into users values(?)"
	_ = sql
}`
	result := parseAndExtractGoSQL(t, code)
	if len(result.Queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(result.Queries))
	}
	if result.Queries[0].QueryType != "INSERT" {
		t.Errorf("expected INSERT, got %s", result.Queries[0].QueryType)
	}
}

func TestExtractSQL_Go_ConditionalBranch(t *testing.T) {
	code := `package main
func query(name string, age int) {
	sql := "select * from users where 1=1"
	if name != "" {
		sql += " and name = ?"
	}
	if age > 0 {
		sql += " and age = ?"
	}
	_ = sql
}`
	result := parseAndExtractGoSQL(t, code)
	if len(result.Queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(result.Queries))
	}
	query := result.Queries[0]
	if query.BaseSQL != "select * from users where 1=1" {
		t.Errorf("BaseSQL = %q", query.BaseSQL)
	}
	if len(query.Conditions) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(query.Conditions))
	}
	if query.Conditions[0].Fragment != " and name = ?" {
		t.Errorf("condition[0].Fragment = %q", query.Conditions[0].Fragment)
	}
	if query.Conditions[1].Fragment != " and age = ?" {
		t.Errorf("condition[1].Fragment = %q", query.Conditions[1].Fragment)
	}
}

func TestExtractSQL_Go_IfElse(t *testing.T) {
	code := `package main
func query(useView bool) {
	sql := "select * from "
	if useView {
		sql += "v_user_summary"
	} else {
		sql += "users"
	}
	_ = sql
}`
	result := parseAndExtractGoSQL(t, code)
	if len(result.Queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(result.Queries))
	}
	query := result.Queries[0]
	if query.BaseSQL != "select * from " {
		t.Errorf("BaseSQL = %q", query.BaseSQL)
	}
	if len(query.Conditions) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(query.Conditions))
	}
	if query.Conditions[0].IsElse {
		t.Error("condition[0] should not be IsElse")
	}
	if !query.Conditions[1].IsElse {
		t.Error("condition[1] should be IsElse")
	}
}

func TestExtractSQL_Go_NestedIf(t *testing.T) {
	code := `package main
func query(name string, fuzzy bool) {
	sql := "select * from users where 1=1"
	if name != "" {
		if fuzzy {
			sql += " and name like ?"
		} else {
			sql += " and name = ?"
		}
	}
	_ = sql
}`
	result := parseAndExtractGoSQL(t, code)
	if len(result.Queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(result.Queries))
	}
	query := result.Queries[0]
	if len(query.Conditions) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(query.Conditions))
	}
	if !strings.Contains(query.Conditions[0].Condition, "&&") {
		t.Errorf("expected nested condition with &&, got %q", query.Conditions[0].Condition)
	}
}

func TestExtractSQL_Go_RawStringLiteral(t *testing.T) {
	code := "package main\nfunc query() {\n\tsql := `select * from users where status = 1`\n\t_ = sql\n}"
	result := parseAndExtractGoSQL(t, code)
	if len(result.Queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(result.Queries))
	}
	if result.Queries[0].SQLText != "select * from users where status = 1" {
		t.Errorf("SQLText = %q", result.Queries[0].SQLText)
	}
}

func TestExtractSQL_Go_NonSQLIgnored(t *testing.T) {
	code := `package main
func run() {
	msg := "hello world"
	path := "/api/users"
	_ = msg
	_ = path
}`
	result := parseAndExtractGoSQL(t, code)
	sqlCount := 0
	for _, query := range result.Queries {
		if query.SQLText == "hello world" || query.SQLText == "/api/users" {
			sqlCount++
		}
	}
	if sqlCount != 0 {
		t.Fatalf("expected 0 non-SQL queries, got %d", sqlCount)
	}
}

func TestExtractSQL_Go_MultiVarDeclaration(t *testing.T) {
	code := `package main
func query() {
	var dummy, sqlStr = 1, "select * from orders where id = ?"
	_ = dummy
	_ = sqlStr
}`
	result := parseAndExtractGoSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "select * from orders") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected SQL from second variable in multi-var declaration")
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}

func TestExtractSQL_Go_MultiShortVarDeclaration(t *testing.T) {
	code := `package main
func query() {
	err, sqlStr := check(), "select * from accounts where active = 1"
	_ = err
	_ = sqlStr
}`
	result := parseAndExtractGoSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "select * from accounts") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected SQL from second variable in multi-short-var declaration")
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}

func TestExtractSQL_Go_MultiAssignment(t *testing.T) {
	code := `package main
func query() {
	var sqlStr string
	var err error
	err, sqlStr = check(), "select * from products"
	_ = err
	_ = sqlStr
}`
	result := parseAndExtractGoSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "select * from products") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected SQL from second variable in multi-assignment")
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}

func TestExtractSQL_Go_PlusEqualsDynamic(t *testing.T) {
	code := `package main
func query() {
	sql := "select * from users where 1=1"
	sql += getDynamicWhere()
	sql += " order by id"
}`
	result := parseAndExtractGoSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "select * from users") && strings.Contains(query.SQLText, "order by id") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected SQL to survive dynamic += and capture subsequent static +=")
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}

func TestExtractSQL_Go_PlusEqualsUntrackedIgnored(t *testing.T) {
	code := `package main
func query() {
	msg := "hello"
	msg += "select * from users"
}`
	result := parseAndExtractGoSQL(t, code)
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "select * from users") {
			t.Errorf("untracked non-SQL variable should not produce SQL query: %q", query.SQLText)
		}
	}
}

func TestExtractSQL_Go_GORMRawDirect(t *testing.T) {
	code := `package main
func query() {
	db.Raw("select count(*) from users").Scan(&result)
}`
	result := parseAndExtractGoSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if query.SQLText == "select count(*) from users" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected GORM Raw direct argument to be extracted")
	}
}

func TestExtractSQL_Go_MultipleSQLVariables(t *testing.T) {
	code := `package main
func query() {
	selectSQL := "select * from users"
	insertSQL := "insert into logs values(?)"
	_ = selectSQL
	_ = insertSQL
}`
	result := parseAndExtractGoSQL(t, code)
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

func TestExtractSQL_Go_TripleConcatenation(t *testing.T) {
	code := `package main
func query() {
	sql := "select * " + "from users " + "where id = ?"
	_ = sql
}`
	result := parseAndExtractGoSQL(t, code)
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

func TestExtractSQL_Go_MultiplePlusEquals(t *testing.T) {
	code := `package main
func query() {
	sql := "select * from users where 1=1"
	sql += " and status = 1"
	sql += " and role = 'admin'"
	sql += " order by created_at"
}`
	result := parseAndExtractGoSQL(t, code)
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

func TestExtractSQL_Go_ElseIfChain(t *testing.T) {
	code := `package main
func query(sortBy string) {
	sql := "select * from users where 1=1"
	if sortBy == "name" {
		sql += " order by name"
	} else if sortBy == "age" {
		sql += " order by age"
	} else {
		sql += " order by id"
	}
}`
	result := parseAndExtractGoSQL(t, code)
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

func TestExtractSQL_Go_ForLoopBody(t *testing.T) {
	code := `package main
func query(ids []int) {
	sql := "select * from users where id in ("
	for i := 0; i < len(ids); i++ {
		sql += "?"
	}
	sql += ")"
}`
	result := parseAndExtractGoSQL(t, code)
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

func TestExtractSQL_Go_SwitchCaseBody(t *testing.T) {
	code := `package main
func query(action string) {
	sql := "select * from users where 1=1"
	switch action {
	case "active":
		sql += " and active = true"
	case "deleted":
		sql += " and deleted_at is not null"
	}
}`
	result := parseAndExtractGoSQL(t, code)
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

func TestExtractSQL_Go_MultiLineConcat(t *testing.T) {
	code := `package main
func query() {
	sql := "select u.id, u.name, u.email " +
		"from users u " +
		"join orders o on u.id = o.user_id " +
		"where o.total > 100"
	_ = sql
}`
	result := parseAndExtractGoSQL(t, code)
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

func TestExtractSQL_Go_QueryTypeDetection(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected string
	}{
		{"SELECT", `package main
func f() { sql := "select * from users"; _ = sql }`, "SELECT"},
		{"INSERT", `package main
func f() { sql := "insert into users(name) values(?)"; _ = sql }`, "INSERT"},
		{"UPDATE", `package main
func f() { sql := "update users set name = ? where id = ?"; _ = sql }`, "UPDATE"},
		{"DELETE", `package main
func f() { sql := "delete from users where id = ?"; _ = sql }`, "DELETE"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			result := parseAndExtractGoSQL(t, testCase.code)
			if len(result.Queries) == 0 {
				t.Fatal("expected at least 1 query")
			}
			if result.Queries[0].QueryType != testCase.expected {
				t.Errorf("expected QueryType=%s, got %s", testCase.expected, result.Queries[0].QueryType)
			}
		})
	}
}

func TestExtractSQL_Go_NoDuplicateFromTracking(t *testing.T) {
	code := `package main
func query() {
	sql := "select * from users where id = ?"
	_ = sql
}`
	result := parseAndExtractGoSQL(t, code)
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

func TestExtractSQL_Go_OnlyIfBranchModifiesSQL(t *testing.T) {
	code := `package main
func query(limit int) {
	sql := "select * from users where 1=1"
	if limit > 0 {
		sql += " limit ?"
	}
}`
	result := parseAndExtractGoSQL(t, code)
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

func TestExtractSQL_Go_VarDeclWithTypeAnnotation(t *testing.T) {
	code := `package main
func query() {
	var sql string = "select * from categories"
	_ = sql
}`
	result := parseAndExtractGoSQL(t, code)
	found := false
	for _, query := range result.Queries {
		if strings.Contains(query.SQLText, "select * from categories") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected var with type annotation to be tracked")
		for index, query := range result.Queries {
			t.Logf("  query[%d]: %q", index, query.SQLText)
		}
	}
}

func TestExtractSQL_Go_ReassignmentResetsFragments(t *testing.T) {
	code := `package main
func query() {
	sql := "select * from users"
	sql += " where active = true"
	sql = "delete from users where id = ?"
}`
	result := parseAndExtractGoSQL(t, code)
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

func TestExtractSQL_Go_DynamicPlusEqualsPlaceholder(t *testing.T) {
	code := `package main
func query() {
	sql := "select * from users where 1=1"
	sql += buildCondition()
}`
	result := parseAndExtractGoSQL(t, code)
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
