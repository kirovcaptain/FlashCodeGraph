package java

import (
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func parse(code []byte) (*tree_sitter.Node, func()) {
	parser := tree_sitter.NewParser()
	lang := tree_sitter.NewLanguage(tree_sitter_java.Language())
	parser.SetLanguage(lang)
	tree := parser.Parse(code, nil)
	return tree.RootNode(), func() { tree.Close(); parser.Close() }
}

func parseJavaFile(t *testing.T, code string, filename string) *model.ParseResult {
	t.Helper()
	root, cleanup := parse([]byte(code))
	defer cleanup()
	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: filename, Language: "java"}
	Extract(root, []byte(code), file, result)
	return result
}

func TestExtract_ClassAndMethods(t *testing.T) {
	code := []byte(`package com.example;

public class UserService {
    public User findById(Long id) { return null; }
    private void validate() {}
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "UserService.java", Language: "java"}
	file := scanner.ScannedFile{Path: "/test/UserService.java", RelPath: "UserService.java", Language: "java"}
	Extract(root, code, file, result)

	if len(result.Symbols) < 3 {
		t.Fatalf("expected at least 3 symbols (class + 2 methods), got %d", len(result.Symbols))
	}

	hasClass, hasFindById, hasValidate := false, false, false
	for _, sym := range result.Symbols {
		switch sym.Name {
		case "UserService":
			hasClass = true
			if sym.Kind != "Class" {
				t.Fatalf("UserService should be class, got %s", sym.Kind)
			}
		case "findById":
			hasFindById = true
		case "validate":
			hasValidate = true
		}
	}
	if !hasClass || !hasFindById || !hasValidate {
		t.Fatalf("missing symbols: class=%v findById=%v validate=%v", hasClass, hasFindById, hasValidate)
	}
	t.Log("✅ Java Extract: class + methods")
}

func TestExtract_InheritanceAndImports(t *testing.T) {
	code := []byte(`package com.example;

import com.example.BaseService;

public class ChildService extends BaseService implements Runnable {
    public void run() {}
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "ChildService.java", Language: "java"}
	file := scanner.ScannedFile{Path: "/test/ChildService.java", RelPath: "ChildService.java", Language: "java"}
	Extract(root, code, file, result)

	if len(result.Imports) < 1 {
		t.Fatalf("expected at least 1 import, got %d", len(result.Imports))
	}

	hasExtends, hasImplements := false, false
	for _, h := range result.Heritage {
		if h.Kind == "extends" && h.ParentName == "BaseService" {
			hasExtends = true
		}
		if h.Kind == "implements" && h.ParentName == "Runnable" {
			hasImplements = true
		}
	}
	if !hasExtends {
		t.Fatal("missing extends BaseService")
	}
	if !hasImplements {
		t.Fatal("missing implements Runnable")
	}
	t.Log("✅ Java Extract: imports + extends + implements")
}

func TestExtractMybatisMapper_Tables(t *testing.T) {
	xml := []byte(`<mapper namespace="com.example.UserMapper">
    <select id="find">SELECT * FROM users WHERE id = #{id}</select>
</mapper>`)

	queries := ExtractMybatisMapper(xml, "mapper/UserMapper.xml")
	if len(queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(queries))
	}
	if queries[0].CallerName != "com.example.UserMapper.find" {
		t.Fatalf("expected caller com.example.UserMapper.find, got %s", queries[0].CallerName)
	}
	if len(queries[0].Tables) == 0 || queries[0].Tables[0] != "users" {
		t.Fatalf("expected table 'users', got %v", queries[0].Tables)
	}
	t.Log("✅ MyBatis: namespace.id + table extraction")
}

func TestExtractMethod_RestTemplateIntegration(t *testing.T) {
	// Verify ExtractRestTemplateCalls is called from extractMethod
	// Note: full type resolution (short name → FQN) requires import resolution,
	// which happens at the resolver stage. Here we verify the call path works
	// when the field type matches the fully qualified name.
	code := `package com.example;

public class OrderService {
    private org.springframework.web.client.RestTemplate restTemplate;

    public void createOrder() {
        restTemplate.postForEntity("http://payment-service/api/pay", null, String.class);
    }
}
`
	result := parseJavaFile(t, code, "OrderService.java")

	found := false
	for _, rc := range result.RemoteCalls {
		if rc.TargetService == "payment-service" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected payment-service remote call, got: %+v", result.RemoteCalls)
	}
	t.Log("✅ ExtractRestTemplateCalls integrated into extractMethod")
}

func TestExtract_FeignClientRoutePrefix(t *testing.T) {
	code := `package com.example;

import org.springframework.cloud.openfeign.FeignClient;
import org.springframework.web.bind.annotation.*;

@FeignClient(name = "order-service", path = "/api/orders")
public interface OrderClient {
    @GetMapping("/{id}")
    Object getOrder(@PathVariable Long id);

    @PostMapping
    Object createOrder(@RequestBody Object order);
}
`
	result := parseJavaFile(t, code, "OrderClient.java")

	if len(result.Routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(result.Routes))
	}

	routeMap := make(map[string]string)
	for _, r := range result.Routes {
		routeMap[r.HandlerName] = r.Method + " " + r.PathPattern
	}

	if routeMap["OrderClient.getOrder"] != "GET /api/orders/{id}" {
		t.Fatalf("expected GET /api/orders/{id}, got %s", routeMap["OrderClient.getOrder"])
	}
	if routeMap["OrderClient.createOrder"] != "POST /api/orders" {
		t.Fatalf("expected POST /api/orders, got %s", routeMap["OrderClient.createOrder"])
	}

	// Verify framework is "feign" not "spring"
	for _, r := range result.Routes {
		if r.Framework != "feign" {
			t.Fatalf("expected framework feign, got %s", r.Framework)
		}
	}
	t.Log("✅ FeignClient routes have correct path prefix and framework=feign")
}

func TestExtractMethod_RestTemplate_ShortNameResolved(t *testing.T) {
	// With import + short name (typical Java code), RestTemplate type is resolved
	// via import→FQN mapping in buildTypeEnv.
	code := `package com.example;

import org.springframework.web.client.RestTemplate;

public class OrderService {
    private RestTemplate restTemplate;

    public void createOrder() {
        restTemplate.postForEntity("http://payment-service/api/pay", null, String.class);
    }
}
`
	result := parseJavaFile(t, code, "OrderService.java")

	found := false
	for _, rc := range result.RemoteCalls {
		if rc.TargetService == "payment-service" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected payment-service remote call with short name + import, got: %+v", result.RemoteCalls)
	}
	t.Log("✅ RestTemplate short name resolved via import→FQN mapping")
}

func TestJavaFlowContext(t *testing.T) {
	code := `package com.example;

public class Service {
    public void process() {
        validate();
        if (true) {
            save();
        } else {
            logError();
        }
        for (int i = 0; i < 10; i++) {
            update();
        }
        try {
            commit();
        } catch (Exception e) {
            rollback();
        } finally {
            cleanup();
        }
    }
}
`
	result := parseJavaFile(t, code, "Service.java")

	flowMap := make(map[string]string)
	for _, call := range result.Calls {
		flowMap[call.CalledName] = call.FlowContext
	}

	if flowMap["validate"] != "" {
		t.Errorf("validate: expected empty, got %q", flowMap["validate"])
	}
	if flowMap["save"] != "if" {
		t.Errorf("save: expected if, got %q", flowMap["save"])
	}
	if flowMap["logError"] != "else" {
		t.Errorf("logError: expected else, got %q", flowMap["logError"])
	}
	if flowMap["update"] != "loop" {
		t.Errorf("update: expected loop, got %q", flowMap["update"])
	}
	if flowMap["commit"] != "try" {
		t.Errorf("commit: expected try, got %q", flowMap["commit"])
	}
	if flowMap["rollback"] != "try > catch" {
		t.Errorf("rollback: expected try > catch, got %q", flowMap["rollback"])
	}
	if flowMap["cleanup"] != "try > finally" {
		t.Errorf("cleanup: expected try > finally, got %q", flowMap["cleanup"])
	}
	t.Log("✅ Java FlowContext: if/else/loop/try/catch/finally")
}

func TestStaticMethodCall_ReceiverExpr(t *testing.T) {
	code := `
package com.example;
import cn.hutool.core.bean.BeanUtil;
public class MyService {
    public void doWork() {
        Map<String, Object> m = BeanUtil.beanToMap(request, true, true);
    }
}
`
	result := parseJavaFile(t, code, "MyService.java")
	for _, c := range result.Calls {
		t.Logf("CalledName=%q ReceiverExpr=%q CallerName=%q", c.CalledName, c.ReceiverExpr, c.CallerName)
	}
	found := false
	for _, c := range result.Calls {
		if c.CalledName == "beanToMap" {
			found = true
			if c.ReceiverExpr != "BeanUtil" {
				t.Errorf("expected ReceiverExpr=BeanUtil, got %q", c.ReceiverExpr)
			}
		}
	}
	if !found {
		t.Fatal("beanToMap call not found")
	}
}

func TestImportExtraction(t *testing.T) {
	code := `
package com.example;
import cn.hutool.core.bean.BeanUtil;
import com.example.MyService;
public class Test {}
`
	result := parseJavaFile(t, code, "Test.java")
	for _, imp := range result.Imports {
		t.Logf("SymbolName=%q ModulePath=%q", imp.SymbolName, imp.ModulePath)
	}
	if len(result.Imports) != 2 {
		t.Fatalf("expected 2 imports, got %d", len(result.Imports))
	}
}

func TestJavaPendingAssignment(t *testing.T) {
	code := `
package com.example;
public class Service {
    public void process() {
        User user = getUser();
        Address addr = user.getAddress();
        String name = addr.name;
        User alias = user;
    }
}
`
	result := parseJavaFile(t, code, "Service.java")
	kinds := make(map[string]string)
	for _, p := range result.PendingAssignments {
		kinds[p.LHS] = p.Kind
	}
	if kinds["user"] != "call_result" {
		t.Errorf("user: expected call_result, got %q", kinds["user"])
	}
	if kinds["addr"] != "method_call_result" {
		t.Errorf("addr: expected method_call_result, got %q", kinds["addr"])
	}
	if kinds["name"] != "field_access" {
		t.Errorf("name: expected field_access, got %q", kinds["name"])
	}
	if kinds["alias"] != "copy" {
		t.Errorf("alias: expected copy, got %q", kinds["alias"])
	}
	t.Log("✅ Java PendingAssignment: 4 kinds extracted")
}

func TestJavaArgTypeInference(t *testing.T) {
	code := `
package com.example;
public class Service {
    public void process() {
        ResponseResult.e(200, "msg");
        ResponseResult.e(ResponseCode.FAIL, null);
        ResponseResult.e(ResponseCode.FAIL.code, body);
        foo(new User());
    }
}
`
	result := parseJavaFile(t, code, "Service.java")
	for _, c := range result.Calls {
		if c.CalledName == "e" || c.CalledName == "foo" {
			t.Logf("CalledName=%q ArgTypes=%v", c.CalledName, c.ArgTypes)
		}
	}
	// e(200, "msg") → ["int", "String"]
	for _, c := range result.Calls {
		if c.CalledName == "e" && c.ArgCount == 2 && len(c.ArgTypes) == 2 && c.ArgTypes[0] == "int" && c.ArgTypes[1] == "String" {
			t.Log("✅ e(200, \"msg\") → [int, String]")
			return
		}
	}
	t.Fatal("expected e(200, \"msg\") with ArgTypes [int, String]")
}

func TestLocalVarTypeHint(t *testing.T) {
	code := `
package com.example;
public class Service {
    public void process() {
        UserService svc = new UserService();
        List<String> names = new ArrayList<>();
    }
}
`
	result := parseJavaFile(t, code, "Service.java")
	hints := make(map[string]string)
	for _, h := range result.TypeHints {
		if h.Scope == "com.example.Service.process" {
			hints[h.VarName] = h.TypeName
		}
	}
	if hints["svc"] != "UserService" {
		t.Errorf("svc: expected UserService, got %q", hints["svc"])
	}
	if hints["names"] != "List" {
		t.Errorf("names: expected List, got %q", hints["names"])
	}
	t.Log("✅ Local variable TypeHint: explicit types extracted")
}

func TestLocalVarTypeHint_NoScopeConflict(t *testing.T) {
	code := `
package com.example;
public class Foo {
    private OrderService svc;
    public void bar() {
        UserService svc = new UserService();
    }
    public void baz() {
        LogService svc = new LogService();
    }
}
`
	result := parseJavaFile(t, code, "Foo.java")
	byScope := make(map[string]string)
	for _, h := range result.TypeHints {
		if h.VarName == "svc" {
			byScope[h.Scope] = h.TypeName
		}
	}
	if byScope["com.example.Foo"] != "OrderService" {
		t.Errorf("field scope com.example.Foo: expected OrderService, got %q", byScope["com.example.Foo"])
	}
	if byScope["com.example.Foo.bar"] != "UserService" {
		t.Errorf("method scope com.example.Foo.bar: expected UserService, got %q", byScope["com.example.Foo.bar"])
	}
	if byScope["com.example.Foo.baz"] != "LogService" {
		t.Errorf("method scope com.example.Foo.baz: expected LogService, got %q", byScope["com.example.Foo.baz"])
	}
	t.Log("✅ Local variable TypeHint: no scope conflict between field and methods")
}
