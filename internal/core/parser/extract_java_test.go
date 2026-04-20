package parser

import (
	"context"
	"strings"
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
)

func TestExtractJava_FullClass(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`package com.example;

import java.util.List;

@Service
public class UserService extends BaseService implements Serializable {
    @Autowired
    private UserDao userDao;

    @Override
    public User findById(Long id) {
        return userDao.findById(id);
    }

    public List<User> findAll() {
        return userDao.findAll();
    }
}
`)
	file := scanner.ScannedFile{Path: "/test/UserService.java", RelPath: "UserService.java", Language: "java"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	// Check imports
	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}
	if result.Imports[0].ModulePath != "java.util.List" {
		t.Fatalf("expected java.util.List, got %s", result.Imports[0].ModulePath)
	}

	// Check symbols: 1 class + 2 methods
	classCount := 0
	funcCount := 0
	for _, symbol := range result.Symbols {
		switch symbol.Kind {
		case "class":
			classCount++
			if symbol.Name != "UserService" {
				t.Fatalf("expected UserService, got %s", symbol.Name)
			}
			if symbol.QualifiedName != "com.example.UserService" {
				t.Fatalf("expected com.example.UserService, got %s", symbol.QualifiedName)
			}
		case "function":
			funcCount++
		}
	}
	if classCount != 1 {
		t.Fatalf("expected 1 class, got %d", classCount)
	}
	if funcCount != 2 {
		t.Fatalf("expected 2 functions, got %d", funcCount)
	}

	// Check heritage
	if len(result.Heritage) != 2 {
		t.Fatalf("expected 2 heritage (extends + implements), got %d", len(result.Heritage))
	}
	extendsFound := false
	implementsFound := false
	for _, heritage := range result.Heritage {
		if heritage.Kind == "extends" && heritage.ParentName == "BaseService" {
			extendsFound = true
		}
		if heritage.Kind == "implements" && heritage.ParentName == "Serializable" {
			implementsFound = true
		}
	}
	if !extendsFound {
		t.Fatal("missing extends BaseService")
	}
	if !implementsFound {
		t.Fatal("missing implements Serializable")
	}

	// Check calls
	if len(result.Calls) < 2 {
		t.Fatalf("expected at least 2 calls, got %d", len(result.Calls))
	}
	callNames := make(map[string]bool)
	for _, call := range result.Calls {
		callNames[call.CalledName] = true
	}
	if !callNames["findById"] {
		t.Fatal("missing call to findById")
	}
	if !callNames["findAll"] {
		t.Fatal("missing call to findAll")
	}

	// Check type hints (field declarations)
	if len(result.TypeHints) < 1 {
		t.Fatalf("expected at least 1 type hint, got %d", len(result.TypeHints))
	}
	foundUserDao := false
	for _, hint := range result.TypeHints {
		if hint.VarName == "userDao" && hint.TypeName == "UserDao" {
			foundUserDao = true
		}
	}
	if !foundUserDao {
		t.Fatal("missing type hint for userDao: UserDao")
	}

	t.Logf("✅ Java extraction: %d symbols, %d calls, %d heritage, %d imports, %d type hints",
		len(result.Symbols), len(result.Calls), len(result.Heritage), len(result.Imports), len(result.TypeHints))
}

func TestExtractJava_Interface(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`package com.example;

public interface UserRepository {
    User findById(Long id);
    List<User> findAll();
}
`)
	file := scanner.ScannedFile{Path: "/test/UserRepository.java", RelPath: "UserRepository.java", Language: "java"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	// Should have 1 interface + 2 methods
	interfaceCount := 0
	for _, symbol := range result.Symbols {
		if symbol.ClassType == "interface" {
			interfaceCount++
			if symbol.Name != "UserRepository" {
				t.Fatalf("expected UserRepository, got %s", symbol.Name)
			}
		}
	}
	if interfaceCount != 1 {
		t.Fatalf("expected 1 interface, got %d", interfaceCount)
	}
	t.Log("✅ Java interface extraction works")
}

func TestExtractJava_Enum(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`package com.example;

public enum Status {
    ACTIVE, INACTIVE, DELETED
}
`)
	file := scanner.ScannedFile{Path: "/test/Status.java", RelPath: "Status.java", Language: "java"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	enumCount := 0
	for _, symbol := range result.Symbols {
		if symbol.ClassType == "enum" {
			enumCount++
		}
	}
	if enumCount != 1 {
		t.Fatalf("expected 1 enum, got %d", enumCount)
	}
	t.Log("✅ Java enum extraction works")
}

func TestExtractJava_SuperCallKeepsSuperReceiver(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`package com.example;

public class ChildDao extends BaseDao {
    public Object getById(Long id) {
        return super.get("select * from t where id=?", new Object[]{id}, Object.class);
    }
}
`)
	file := scanner.ScannedFile{Path: "/test/ChildDao.java", RelPath: "ChildDao.java", Language: "java"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	// ReceiverExpr should remain "super" — resolver handles the resolution
	found := false
	for _, call := range result.Calls {
		if call.CalledName == "get" {
			if call.ReceiverExpr != "super" {
				t.Fatalf("expected ReceiverExpr=super, got %s", call.ReceiverExpr)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected a call to 'get' but none found")
	}
	t.Log("✅ super.get() keeps ReceiverExpr=super for resolver to handle")
}

func TestExtractJava_ConstructorArgCount(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`package com.example;

public class Service {
    public void create() {
        PagedData data = new PagedData(1, 10, 100, new ArrayList<>());
        User user = new User();
        Order order = new Order("abc", 42);
    }
}
`)
	file := scanner.ScannedFile{Path: "/test/Service.java", RelPath: "Service.java", Language: "java"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	expected := map[string]int{
		"PagedData": 4,
		"User":      0,
		"Order":     2,
		"ArrayList": 0,
	}

	for _, call := range result.Calls {
		if exp, ok := expected[call.CalledName]; ok {
			if call.ArgCount != exp {
				t.Fatalf("constructor %s: expected ArgCount=%d, got %d", call.CalledName, exp, call.ArgCount)
			}
			delete(expected, call.CalledName)
		}
	}
	if len(expected) > 0 {
		t.Fatalf("missing constructor calls: %v", expected)
	}
	t.Log("✅ Constructor ArgCount: PagedData(4), User(0), Order(2), ArrayList(0)")
}

func TestExtractJava_CatchParamTypeHint(t *testing.T) {
	p := New("")
	defer p.Close()

	code := []byte(`package com.example;
public class RedisUtil {
    private LoggerUtil logger;
    public Long zCard(String key) {
        try {
            return Long.valueOf(key);
        } catch (Exception e) {
            logger.error(e.getMessage(), e);
            return null;
        }
    }
}`)
	file := scanner.ScannedFile{Path: "/test/RedisUtil.java", RelPath: "RedisUtil.java", Language: "java"}
	result, err := p.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}
	found := false
	for _, th := range result.TypeHints {
		t.Logf("TypeHint: scope=%s var=%s type=%s", th.Scope, th.VarName, th.TypeName)
		if th.VarName == "e" && th.TypeName == "Exception" {
			found = true
		}
	}
	if !found {
		t.Fatal("catch parameter 'e' with type 'Exception' not found in TypeHints")
	}
	t.Log("✅ catch parameter extracted into TypeHints")
}

func TestExtractJava_ForEachTypeHint(t *testing.T) {
	p := New("")
	defer p.Close()

	code := []byte(`package com.example;
public class Dao {
    public void process(List<GuildPermission> perms) {
        for (GuildPermission gp : perms) {
            gp.getRegions();
        }
    }
}`)
	file := scanner.ScannedFile{Path: "/test/Dao.java", RelPath: "Dao.java", Language: "java"}
	result, err := p.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}
	found := false
	for _, th := range result.TypeHints {
		if th.VarName == "gp" && th.TypeName == "GuildPermission" {
			found = true
			t.Logf("✅ for-each var: scope=%s var=%s type=%s", th.Scope, th.VarName, th.TypeName)
		}
	}
	if !found {
		t.Fatal("for-each variable 'gp' with type 'GuildPermission' not found in TypeHints")
	}
}

func TestExtractJava_InnerClass(t *testing.T) {
	p := New("")
	defer p.Close()

	code := []byte(`package com.example;
public class Outer {
    public static class Inner {
        private Long userId;
        public Long getUserId() { return userId; }
    }
    public void doWork(Inner req) {
        req.getUserId();
    }
}`)
	file := scanner.ScannedFile{Path: "/test/Outer.java", RelPath: "Outer.java", Language: "java"}
	result, err := p.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}
	foundClass := false
	foundMethod := false
	for _, s := range result.Symbols {
		if s.Name == "Inner" && s.Kind == "class" {
			foundClass = true
			t.Logf("✅ Inner class: %s", s.QualifiedName)
		}
		if s.Name == "getUserId" && strings.Contains(s.QualifiedName, "Inner") {
			foundMethod = true
			t.Logf("✅ Inner method: %s return=%v", s.QualifiedName, s.ReturnTypes)
		}
	}
	if !foundClass {
		t.Fatal("Inner class not found")
	}
	if !foundMethod {
		t.Fatal("Inner.getUserId method not found")
	}
}
