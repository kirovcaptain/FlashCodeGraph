package java

import (
	"strings"
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// TestExtractORM_NoDuplicateQueryNodes reproduces the bug where a SQL string literal
// produces two RawQuery entries — one from string_literal and one from its string_fragment
// named child — resulting in duplicate primary keys during indexing.
func TestExtractORM_NoDuplicateQueryNodes(t *testing.T) {
	code := `package com.weijin.chatting.analysis.core.dao;
import org.springframework.stereotype.Repository;
import java.util.ArrayList;
import java.util.List;

@Repository
public class DailyStatisticsUserDao {
    public Object get(String id) {
        List<Object> params = new ArrayList<>();
        String sql = "select * from daily_statistics_user where daily_statistics_id=?";
        params.add(id);
        return null;
    }
}
`
	root, cleanup := parse([]byte(code))
	defer cleanup()

	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "DailyStatisticsUserDao.java", Language: "java"}
	Extract(root, []byte(code), file, result)

	if len(result.Queries) != 1 {
		t.Errorf("expected 1 query, got %d — duplicate string_literal/string_fragment extraction bug", len(result.Queries))
		for index, query := range result.Queries {
			t.Logf("  query[%d]: caller=%s line=%d sql=%s", index, query.CallerName, query.Line, query.SQLText)
		}
	}
}

// TestStringBuilderChainAST verifies the Tree-sitter AST structure for
// StringBuilder chain calls like sb.append("A").append("B").append("C").
func TestStringBuilderChainAST(t *testing.T) {
	code := `package com.example;
public class Dao {
    public void query() {
        StringBuilder sb = new StringBuilder("select * from users");
        sb.append(" where status = 1").append(" order by id").append(" limit 10");
    }
}
`
	root, cleanup := parse([]byte(code))
	defer cleanup()

	var walk func(node *tree_sitter.Node, depth int)
	walk = func(node *tree_sitter.Node, depth int) {
		indent := ""
		for depthIndex := 0; depthIndex < depth; depthIndex++ {
			indent += "  "
		}
		if node.Kind() == "method_invocation" || node.Kind() == "string_literal" || node.Kind() == "identifier" || node.Kind() == "argument_list" {
			t.Logf("%s%s [%d:%d] text=%q", indent, node.Kind(), node.StartPosition().Row, node.StartPosition().Column, node.Utf8Text([]byte(code)))
		}
		for index := uint(0); index < node.NamedChildCount(); index++ {
			walk(node.NamedChild(index), depth+1)
		}
	}
	walk(root, 0)

	var findChainExpression func(node *tree_sitter.Node) *tree_sitter.Node
	findChainExpression = func(node *tree_sitter.Node) *tree_sitter.Node {
		if node.Kind() == "expression_statement" {
			child := node.NamedChild(0)
			if child != nil && child.Kind() == "method_invocation" {
				text := child.Utf8Text([]byte(code))
				if len(text) > 20 {
					return child
				}
			}
		}
		for index := uint(0); index < node.NamedChildCount(); index++ {
			if found := findChainExpression(node.NamedChild(index)); found != nil {
				return found
			}
		}
		return nil
	}

	chainNode := findChainExpression(root)
	if chainNode == nil {
		t.Fatal("could not find the chain expression statement")
	}

	var fragments []string
	current := chainNode
	for current != nil && current.Kind() == "method_invocation" {
		argumentsNode := current.ChildByFieldName("arguments")
		if argumentsNode != nil && argumentsNode.NamedChildCount() > 0 {
			argumentText := argumentsNode.NamedChild(0).Utf8Text([]byte(code))
			fragments = append(fragments, argumentText)
		}
		current = current.ChildByFieldName("object")
	}

	t.Logf("Fragments collected top-down: %v", fragments)

	if len(fragments) != 3 {
		t.Fatalf("expected 3 fragments, got %d", len(fragments))
	}
	if fragments[0] != `" limit 10"` {
		t.Errorf("fragments[0] = %q, expected outermost append arg", fragments[0])
	}
	if fragments[2] != `" where status = 1"` {
		t.Errorf("fragments[2] = %q, expected innermost append arg", fragments[2])
	}

	t.Log("Confirmed: Tree-sitter nests chained method_invocation outside-in")
}

func parseAndExtractSQL(t *testing.T, code string) *model.ParseResult {
	t.Helper()
	root, cleanup := parse([]byte(code))
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "Test.java", Language: "java"}
	Extract(root, []byte(code), file, result)
	return result
}

func TestExtractSQL_PlusConcatenation(t *testing.T) {
	code := `package com.example;
public class Dao {
    public void find() {
        String sql = "select * from users" + " where status = 1";
    }
}`
	result := parseAndExtractSQL(t, code)
	if len(result.Queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(result.Queries))
	}
	if result.Queries[0].SQLText != "select * from users where status = 1" {
		t.Errorf("SQLText = %q", result.Queries[0].SQLText)
	}
}

func TestExtractSQL_PlusEquals(t *testing.T) {
	code := `package com.example;
public class Dao {
    public void search() {
        String sql = "select * from users where 1=1";
        sql += " and name like ?";
    }
}`
	result := parseAndExtractSQL(t, code)
	if len(result.Queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(result.Queries))
	}
	if !strings.Contains(result.Queries[0].SQLText, "and name like ?") {
		t.Errorf("SQLText missing += part: %q", result.Queries[0].SQLText)
	}
}

func TestExtractSQL_Reassignment(t *testing.T) {
	code := `package com.example;
public class Dao {
    public void process() {
        String sql = "select * from temp";
        sql = "insert into users values(?)";
    }
}`
	result := parseAndExtractSQL(t, code)
	if len(result.Queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(result.Queries))
	}
	if result.Queries[0].QueryType != "INSERT" {
		t.Errorf("expected INSERT, got %s", result.Queries[0].QueryType)
	}
}

func TestExtractSQL_StringBuilder(t *testing.T) {
	code := `package com.example;
public class Dao {
    public void query() {
        StringBuilder sb = new StringBuilder("select * from users");
        sb.append(" where status = 1");
        sb.append(" order by id");
    }
}`
	result := parseAndExtractSQL(t, code)
	if len(result.Queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(result.Queries))
	}
	expectedSQL := "select * from users where status = 1 order by id"
	if result.Queries[0].SQLText != expectedSQL {
		t.Errorf("SQLText = %q, want %q", result.Queries[0].SQLText, expectedSQL)
	}
}

func TestExtractSQL_ConditionalBranch(t *testing.T) {
	code := `package com.example;
public class Dao {
    public void search(String name, Integer age) {
        String sql = "select * from users where 1=1";
        if (name != null) {
            sql += " and name = ?";
        }
        if (age != null) {
            sql += " and age = ?";
        }
    }
}`
	result := parseAndExtractSQL(t, code)
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

func TestExtractSQL_IfElse(t *testing.T) {
	code := `package com.example;
public class Dao {
    public void getData(boolean useView) {
        String sql = "select * from ";
        if (useView) {
            sql += "v_user_summary";
        } else {
            sql += "users";
        }
    }
}`
	result := parseAndExtractSQL(t, code)
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

func TestExtractSQL_DirectArgument(t *testing.T) {
	code := `package com.example;
public class Dao {
    public void run() {
        jdbcTemplate.query("select count(*) from users");
    }
}`
	result := parseAndExtractSQL(t, code)
	if len(result.Queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(result.Queries))
	}
	if result.Queries[0].SQLText != "select count(*) from users" {
		t.Errorf("SQLText = %q", result.Queries[0].SQLText)
	}
}

func TestExtractSQL_AnyVarName(t *testing.T) {
	code := `package com.example;
public class Dao {
    public void run() {
        String querySql = "select * from orders where status=?";
    }
}`
	result := parseAndExtractSQL(t, code)
	if len(result.Queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(result.Queries))
	}
}

func TestExtractSQL_MethodReturnNotTracked(t *testing.T) {
	code := `package com.example;
public class Dao {
    public void run() {
        String sql = buildSql(param);
    }
}`
	result := parseAndExtractSQL(t, code)
	if len(result.Queries) != 0 {
		t.Fatalf("expected 0 queries, got %d", len(result.Queries))
	}
}

func TestExtractSQL_NonSQLIgnored(t *testing.T) {
	code := `package com.example;
public class Service {
    public void run() {
        String msg = "hello world";
        String path = "/api/users";
    }
}`
	result := parseAndExtractSQL(t, code)
	if len(result.Queries) != 0 {
		t.Fatalf("expected 0 queries, got %d", len(result.Queries))
	}
}

func TestExtractSQL_NestedIf(t *testing.T) {
	code := `package com.example;
public class Dao {
    public void search(String name, boolean fuzzyMatch) {
        String sql = "select * from users where 1=1";
        if (name != null) {
            if (fuzzyMatch) {
                sql += " and name like ?";
            } else {
                sql += " and name = ?";
            }
        }
    }
}`
	result := parseAndExtractSQL(t, code)
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
	if !strings.Contains(query.Conditions[0].Condition, "&&") {
		t.Errorf("expected nested condition with &&, got %q", query.Conditions[0].Condition)
	}
	if query.Conditions[0].Fragment != " and name like ?" {
		t.Errorf("condition[0].Fragment = %q", query.Conditions[0].Fragment)
	}
	if !query.Conditions[1].IsElse {
		t.Error("condition[1] should be IsElse")
	}
}

func TestExtractSQL_MultiTableJoin(t *testing.T) {
	code := `package com.example;
public class Dao {
    public void report() {
        String sql = "select u.name, r.role_name from users u join roles r on u.role_id = r.id";
    }
}`
	result := parseAndExtractSQL(t, code)
	if len(result.Queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(result.Queries))
	}
	tables := result.Queries[0].Tables
	if len(tables) < 2 {
		t.Fatalf("expected at least 2 tables, got %v", tables)
	}
}
