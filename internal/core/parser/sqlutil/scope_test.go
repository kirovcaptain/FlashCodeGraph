package sqlutil

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestScopeEnvironment_LookupSet(t *testing.T) {
	parent := NewScopeEnvironment(nil)
	parent.Set("sql", &SQLVariable{Fragments: []string{"select * from users"}, Line: 1})

	child := NewScopeEnvironment(parent)
	variable := child.Lookup("sql")
	if variable == nil {
		t.Fatal("expected to find 'sql' in parent scope")
	}
	if variable.Fragments[0] != "select * from users" {
		t.Errorf("unexpected fragment: %q", variable.Fragments[0])
	}

	child.Set("sql", &SQLVariable{Fragments: []string{"insert into users"}, Line: 2})
	parentVariable := parent.Variables["sql"]
	if parentVariable.Fragments[0] != "insert into users" {
		t.Error("Set should update the variable in the parent scope where it was found")
	}

	child.Set("localVar", &SQLVariable{Fragments: []string{"select 1"}, Line: 3})
	if child.Variables["localVar"] == nil {
		t.Error("new variable should be stored in current scope")
	}
	if parent.Lookup("localVar") != nil {
		t.Error("new variable should not leak to parent scope")
	}
}

func TestScopeEnvironment_Snapshot(t *testing.T) {
	scope := NewScopeEnvironment(nil)
	scope.Set("sql", &SQLVariable{Fragments: []string{"select * from users"}, Line: 1})

	snapshot := scope.Snapshot()
	scope.Lookup("sql").Fragments = append(scope.Lookup("sql").Fragments, " where id=1")

	if len(snapshot["sql"]) != 1 {
		t.Error("snapshot should be a deep copy, not affected by later mutations")
	}
}

func TestRecordConditionalDiff(t *testing.T) {
	scope := NewScopeEnvironment(nil)
	scope.Set("sql", &SQLVariable{Fragments: []string{"select * from users where 1=1"}, Line: 1})

	snapshotBefore := scope.Snapshot()
	scope.Lookup("sql").Fragments = append(scope.Lookup("sql").Fragments, " and name = ?")

	RecordConditionalDiff(scope, snapshotBefore, "name != null", false)

	variable := scope.Lookup("sql")
	if len(variable.Conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(variable.Conditions))
	}
	if variable.Conditions[0].Fragment != " and name = ?" {
		t.Errorf("condition fragment = %q", variable.Conditions[0].Fragment)
	}
	if variable.Conditions[0].Condition != "name != null" {
		t.Errorf("condition text = %q", variable.Conditions[0].Condition)
	}
	if variable.BaseSQL != "select * from users where 1=1" {
		t.Errorf("BaseSQL = %q", variable.BaseSQL)
	}
}

func TestRestoreSnapshot(t *testing.T) {
	scope := NewScopeEnvironment(nil)
	scope.Set("sql", &SQLVariable{Fragments: []string{"select * from users"}, Line: 1})

	snapshotBefore := scope.Snapshot()
	scope.Lookup("sql").Fragments = append(scope.Lookup("sql").Fragments, " where id=1")

	RestoreSnapshot(scope, snapshotBefore)
	if len(scope.Lookup("sql").Fragments) != 1 {
		t.Error("fragments should be restored to snapshot state")
	}
}

func TestEmitQueriesFromScope(t *testing.T) {
	scope := NewScopeEnvironment(nil)
	scope.Set("sql", &SQLVariable{
		Fragments: []string{"select * from users where 1=1"},
		Line:      5,
		Conditions: []model.ConditionalFragment{
			{Condition: "name != null", Fragment: " and name = ?"},
		},
		BaseSQL: "select * from users where 1=1",
	})
	scope.Set("msg", &SQLVariable{Fragments: []string{"hello world"}, Line: 10})

	result := &model.ParseResult{}
	EmitQueriesFromScope(scope, "TestDao.find", "Test.java", result)

	if len(result.Queries) != 1 {
		t.Fatalf("expected 1 query (non-SQL should be filtered), got %d", len(result.Queries))
	}
	query := result.Queries[0]
	if query.CallerName != "TestDao.find" {
		t.Errorf("CallerName = %q", query.CallerName)
	}
	if query.BaseSQL != "select * from users where 1=1" {
		t.Errorf("BaseSQL = %q", query.BaseSQL)
	}
	if len(query.Conditions) != 1 {
		t.Errorf("expected 1 condition, got %d", len(query.Conditions))
	}
}

func TestIsSQLStub(t *testing.T) {
	tests := []struct {
		text     string
		expected bool
	}{
		{"select * from users", true},
		{"INSERT INTO orders", true},
		{"hello world", false},
		{"/api/users", false},
		{"WHERE id = 1", true},
	}
	for _, testCase := range tests {
		if IsSQLStub(testCase.text) != testCase.expected {
			t.Errorf("IsSQLStub(%q) = %v, want %v", testCase.text, !testCase.expected, testCase.expected)
		}
	}
}
