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
			if h.ParentQualified != "com.example.BaseService" {
				t.Fatalf("expected ParentQualified=com.example.BaseService, got %s", h.ParentQualified)
			}
		}
		if h.Kind == "implements" && h.ParentName == "Runnable" {
			hasImplements = true
			// Runnable has no import → falls back to same package
			if h.ParentQualified != "com.example.Runnable" {
				t.Fatalf("expected ParentQualified=com.example.Runnable, got %s", h.ParentQualified)
			}
		}
	}
	if !hasExtends {
		t.Fatal("missing extends BaseService")
	}
	if !hasImplements {
		t.Fatal("missing implements Runnable")
	}
	t.Log("✅ Java Extract: imports + extends + implements with ParentQualified")
}

func TestExtract_HeritageParentQualifiedDisambiguation(t *testing.T) {
	// When multiple classes share the same short name (e.g. BaseDao in different modules),
	// ParentQualified must resolve via the import statement, not pick arbitrarily.
	code := []byte(`package com.weijin.chatting.biz.core.dao.withdraw;

import com.weijin.chatting.biz.core.dao.BaseDao;
import java.io.Serializable;

@Repository
public class BroadcasterIdentityVerificationReviewDao extends BaseDao implements Serializable {
}
`)
	root, cleanup := parse(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "BroadcasterIdentityVerificationReviewDao.java", Language: "java"}
	file := scanner.ScannedFile{
		Path:     "/test/BroadcasterIdentityVerificationReviewDao.java",
		RelPath:  "BroadcasterIdentityVerificationReviewDao.java",
		Language: "java",
	}
	Extract(root, code, file, result)

	for _, h := range result.Heritage {
		if h.Kind == "extends" && h.ParentName == "BaseDao" {
			if h.ParentQualified != "com.weijin.chatting.biz.core.dao.BaseDao" {
				t.Fatalf("expected ParentQualified from import, got %s", h.ParentQualified)
			}
			t.Log("✅ Java Extract: ParentQualified resolved via import for ambiguous class name")
			return
		}
	}
	t.Fatal("missing extends BaseDao heritage entry")
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
	if flowMap["rollback"] != "catch" {
		t.Errorf("rollback: expected try > catch, got %q", flowMap["rollback"])
	}
	if flowMap["cleanup"] != "finally" {
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

func TestExtract_AccessorDetection(t *testing.T) {
	code := []byte(`package com.example;

public class User {
    private String name;
    private int age;
    private boolean active;

    public String getName() {
        return this.name;
    }

    public void setName(String name) {
        this.name = name;
    }

    public boolean isActive() {
        return this.active;
    }

    public void setAge(int age) {
        this.age = age;
    }
}
`)
	result := parseJavaFile(t, string(code), "User.java")

	accessors := map[string]struct{ getter, setter bool }{}
	for _, sym := range result.Symbols {
		if sym.Kind != "Function" {
			continue
		}
		accessors[sym.Name] = struct{ getter, setter bool }{sym.IsGetter, sym.IsSetter}
	}

	// Simple getters
	if !accessors["getName"].getter {
		t.Error("getName should be IsGetter=true")
	}
	if !accessors["isActive"].getter {
		t.Error("isActive should be IsGetter=true")
	}
	// Simple setters
	if !accessors["setName"].setter {
		t.Error("setName should be IsSetter=true")
	}
	if !accessors["setAge"].setter {
		t.Error("setAge should be IsSetter=true")
	}
	// Getters should not be setters and vice versa
	if accessors["getName"].setter {
		t.Error("getName should not be IsSetter")
	}
	if accessors["setName"].getter {
		t.Error("setName should not be IsGetter")
	}
	t.Log("✅ Java accessor detection: simple getters/setters")
}

func TestExtract_NonAccessor_ComplexGetter(t *testing.T) {
	code := []byte(`package com.example;

public class OrderService {
    public Order getOrder(Long id) {
        Order order = repository.findById(id);
        if (order == null) {
            throw new NotFoundException();
        }
        return order;
    }

    public void setStatus(Long id, String status) {
        Order order = repository.findById(id);
        order.setStatus(status);
        repository.save(order);
    }
}
`)
	result := parseJavaFile(t, string(code), "OrderService.java")

	for _, sym := range result.Symbols {
		if sym.Name == "getOrder" && sym.IsGetter {
			t.Error("getOrder has complex logic, should not be IsGetter")
		}
		if sym.Name == "setStatus" && sym.IsSetter {
			t.Error("setStatus has complex logic, should not be IsSetter")
		}
	}
	t.Log("✅ Java non-accessor: complex getXxx/setXxx not marked")
}

func TestExtract_LombokAccessorMarking(t *testing.T) {
	code := []byte(`package com.example;

import lombok.Data;

@Data
public class UserDTO {
    private String name;
    private int age;
}
`)
	result := parseJavaFile(t, string(code), "UserDTO.java")

	found := map[string]struct{ getter, setter, synthetic bool }{}
	for _, sym := range result.Symbols {
		if sym.Kind != "Function" {
			continue
		}
		found[sym.Name] = struct{ getter, setter, synthetic bool }{sym.IsGetter, sym.IsSetter, sym.IsSynthetic}
	}

	if !found["getName"].getter || !found["getName"].synthetic {
		t.Error("Lombok getName should be IsGetter=true, IsSynthetic=true")
	}
	if !found["setName"].setter || !found["setName"].synthetic {
		t.Error("Lombok setName should be IsSetter=true, IsSynthetic=true")
	}
	if !found["getAge"].getter {
		t.Error("Lombok getAge should be IsGetter=true")
	}
	if !found["setAge"].setter {
		t.Error("Lombok setAge should be IsSetter=true")
	}
	t.Log("✅ Lombok @Data: getter/setter + synthetic marked")
}

func TestExtract_ChainedCallArgExprs(t *testing.T) {
	code := `package com.example;
public class Controller {
    private Dao dao;
    public void changeBasicInfo(Reqs reqs) {
        dao.getInvitationCode(reqs.getInvitationCode().trim().toUpperCase(), InvitationCodeType.Guide.getCode());
    }
}
`
	result := parseJavaFile(t, code, "Controller.java")
	for _, call := range result.Calls {
		if call.CalledName == "getInvitationCode" && call.ReceiverExpr == "dao" {
			t.Logf("ArgCount=%d ArgTypes=%v ArgExprs=%v", call.ArgCount, call.ArgTypes, call.ArgExprs)
			if call.ArgCount != 2 {
				t.Fatalf("expected 2 args, got %d", call.ArgCount)
			}
			if call.ArgExprs[0] != "reqs.getInvitationCode().trim().toUpperCase()" {
				t.Errorf("arg0 expr = %q, want full chain", call.ArgExprs[0])
			}
			if call.ArgExprs[1] != "InvitationCodeType.Guide.getCode()" {
				t.Errorf("arg1 expr = %q, want enum chain", call.ArgExprs[1])
			}
			return
		}
	}
	t.Fatal("dao.getInvitationCode call not found")
}

func TestExtract_EnumMethods(t *testing.T) {
	code := `package com.example;
public enum CodeType {
    Guide(1), Common(2);
    private final int code;
    CodeType(int code) { this.code = code; }
    public Integer getCode() { return this.code; }
    public static CodeType getEnumByCode(Integer code) { return null; }
}
`
	result := parseJavaFile(t, code, "CodeType.java")

	methods := map[string]bool{}
	for _, sym := range result.Symbols {
		if sym.Kind == "Function" {
			methods[sym.Name] = true
			t.Logf("  Function: %s (qn=%s)", sym.Name, sym.QualifiedName)
		}
	}
	for _, expected := range []string{"CodeType", "getCode", "getEnumByCode"} {
		if !methods[expected] {
			t.Errorf("expected enum method %q to be extracted", expected)
		}
	}
}

func TestExtract_LombokAccessorInnerClassIDUnique(t *testing.T) {
	code := []byte(`package com.example;
import lombok.Data;

@Data
public class Outer {
    @Data
    public static class GroupA {
        @Data
        public static class Item {
            private double score;
        }
    }
    @Data
    public static class GroupB {
        @Data
        public static class Item {
            private double score;
        }
    }
}
`)
	result := parseJavaFile(t, string(code), "Outer.java")

	seenIDs := map[string]string{} // id -> qualifiedName
	for _, sym := range result.Symbols {
		if sym.Kind != "Function" || !sym.IsSynthetic {
			continue
		}
		if existingQualifiedName, exists := seenIDs[sym.ID]; exists {
			t.Errorf("duplicate synthetic accessor ID %q: %q and %q", sym.ID, existingQualifiedName, sym.QualifiedName)
		}
		seenIDs[sym.ID] = sym.QualifiedName
	}

	foundGroupAItemGetter := false
	foundGroupBItemGetter := false
	for _, sym := range result.Symbols {
		if sym.Name != "getScore" || !sym.IsSynthetic {
			continue
		}
		if sym.QualifiedName == "com.example.Outer.GroupA.Item.getScore" {
			foundGroupAItemGetter = true
		}
		if sym.QualifiedName == "com.example.Outer.GroupB.Item.getScore" {
			foundGroupBItemGetter = true
		}
	}
	if !foundGroupAItemGetter {
		t.Error("missing GroupA.Item.getScore")
	}
	if !foundGroupBItemGetter {
		t.Error("missing GroupB.Item.getScore")
	}
	t.Log("✅ 同名内部类合成 accessor ID 唯一性验证通过")
}

// TestExtract_EnumConstants verifies enum constant extraction as Variable symbols.
func TestExtract_EnumConstants(t *testing.T) {
	code := `package com.example;
public enum OrderStatus {
    PENDING,
    ACTIVE(1),
    CANCELLED("desc");
    private final String label;
    public String getLabel() { return label; }
}`
	result := parseJavaFile(t, code, "OrderStatus.java")

	// Verify 3 enum constants as Variable symbols
	enumConstants := []string{"PENDING", "ACTIVE", "CANCELLED"}
	foundConstants := make(map[string]bool)
	for _, sym := range result.Symbols {
		if sym.Kind == "Variable" && sym.IsStatic && sym.IsFinal {
			for _, name := range enumConstants {
				if sym.Name == name {
					foundConstants[name] = true
					expectedQN := "com.example.OrderStatus." + name
					if sym.QualifiedName != expectedQN {
						t.Errorf("constant %s: expected qualified_name=%s, got %s", name, expectedQN, sym.QualifiedName)
					}
				}
			}
		}
	}
	for _, name := range enumConstants {
		if !foundConstants[name] {
			t.Errorf("missing enum constant Variable: %s", name)
		}
	}

	// Verify getLabel method exists
	hasGetLabel := false
	for _, sym := range result.Symbols {
		if sym.Kind == "Function" && sym.Name == "getLabel" {
			hasGetLabel = true
		}
	}
	if !hasGetLabel {
		t.Error("missing getLabel method")
	}
	t.Log("✅ Enum constants extracted as Variable symbols")
}

// TestExtract_InterfaceConstants verifies interface constant extraction with implicit modifiers.
func TestExtract_InterfaceConstants(t *testing.T) {
	code := `package com.example;
public interface PayChannelType {
    int ALIPAY = 1;
    int WECHAT = 2;
    int UNIONPAY = 3;
}`
	result := parseJavaFile(t, code, "PayChannelType.java")

	// Debug: print all symbols
	t.Logf("Total symbols: %d", len(result.Symbols))
	for _, sym := range result.Symbols {
		t.Logf("Symbol: Kind=%s Name=%s QualifiedName=%s IsStatic=%v IsFinal=%v Visibility=%s",
			sym.Kind, sym.Name, sym.QualifiedName, sym.IsStatic, sym.IsFinal, sym.Visibility)
	}

	// Debug: print TypeHints
	t.Logf("Total TypeHints: %d", len(result.TypeHints))
	for _, hint := range result.TypeHints {
		t.Logf("TypeHint: VarName=%s TypeName=%s Scope=%s", hint.VarName, hint.TypeName, hint.Scope)
	}

	constants := []string{"ALIPAY", "WECHAT", "UNIONPAY"}
	foundConstants := make(map[string]bool)
	for _, sym := range result.Symbols {
		if sym.Kind == "Variable" {
			for _, name := range constants {
				if sym.Name == name {
					foundConstants[name] = true
					if !sym.IsStatic || !sym.IsFinal {
						t.Errorf("interface constant %s: expected IsStatic=true IsFinal=true, got IsStatic=%v IsFinal=%v", name, sym.IsStatic, sym.IsFinal)
					}
					if sym.Visibility != "public" {
						t.Errorf("interface constant %s: expected Visibility=public, got %s", name, sym.Visibility)
					}
					expectedQN := "com.example.PayChannelType." + name
					if sym.QualifiedName != expectedQN {
						t.Errorf("constant %s: expected qualified_name=%s, got %s", name, expectedQN, sym.QualifiedName)
					}
				}
			}
		}
	}
	for _, name := range constants {
		if !foundConstants[name] {
			t.Errorf("missing interface constant Variable: %s", name)
		}
	}
	if len(foundConstants) == 3 {
		t.Log("✅ Interface constants extracted with implicit public static final")
	}
}

// TestExtract_ClassStaticFinalFields verifies class static final field extraction.
func TestExtract_ClassStaticFinalFields(t *testing.T) {
	code := `package com.example;
public class Constants {
    public static final int MAX_RETRY = 3;
    public static final String DEFAULT_ENCODING = "UTF-8";
    private int instanceField = 0;
}`
	result := parseJavaFile(t, code, "Constants.java")

	// Verify static final fields as Variable symbols
	staticFinalFields := map[string]bool{"MAX_RETRY": false, "DEFAULT_ENCODING": false}
	for _, sym := range result.Symbols {
		if sym.Kind == "Variable" && sym.IsStatic && sym.IsFinal {
			if _, exists := staticFinalFields[sym.Name]; exists {
				staticFinalFields[sym.Name] = true
			}
		}
	}
	for name, found := range staticFinalFields {
		if !found {
			t.Errorf("missing static final field Variable: %s", name)
		}
	}

	// Verify instanceField is NOT a static final Variable
	for _, sym := range result.Symbols {
		if sym.Name == "instanceField" && sym.Kind == "Variable" && sym.IsStatic && sym.IsFinal {
			t.Error("instanceField should not be static final Variable")
		}
	}
	t.Log("✅ Class static final fields extracted as Variable symbols")
}

// TestExtract_PatternA_FieldAccess verifies Pattern A constant reference extraction.
func TestExtract_PatternA_FieldAccess(t *testing.T) {
	code := `package com.example;
public class OrderService {
    void process(OrderStatus status) {
        if (status == OrderStatus.PENDING) { doA(); }
        int channel = PayChannelType.ALIPAY;
        return OrderStatus.ACTIVE;
    }
}`
	result := parseJavaFile(t, code, "OrderService.java")

	expectedRefs := []struct {
		objectExpr string
		constName  string
	}{
		{"OrderStatus", "PENDING"},
		{"PayChannelType", "ALIPAY"},
		{"OrderStatus", "ACTIVE"},
	}

	if len(result.ConstRefs) < len(expectedRefs) {
		t.Errorf("expected at least %d ConstRefs, got %d", len(expectedRefs), len(result.ConstRefs))
	}

	foundRefs := make(map[string]bool)
	for _, ref := range result.ConstRefs {
		if ref.RefKind == "field_access" {
			key := ref.ObjectExpr + "." + ref.ConstName
			foundRefs[key] = true
		}
	}

	for _, expected := range expectedRefs {
		key := expected.objectExpr + "." + expected.constName
		if !foundRefs[key] {
			t.Errorf("missing field_access ref: %s", key)
		}
	}
	t.Log("✅ Pattern A field_access references extracted")
}

// TestExtract_PatternA_NestedFieldAccess verifies nested field_access extraction.
func TestExtract_PatternA_NestedFieldAccess(t *testing.T) {
	code := `package com.example;
public class Service {
    void process() {
        int type = CoinGoodsSubType.Type.EQUITY;
    }
}`
	result := parseJavaFile(t, code, "Service.java")

	found := false
	for _, ref := range result.ConstRefs {
		if ref.RefKind == "field_access" && ref.ObjectExpr == "CoinGoodsSubType.Type" && ref.ConstName == "EQUITY" {
			found = true
			break
		}
	}
	if !found {
		t.Error("missing nested field_access ref: CoinGoodsSubType.Type.EQUITY")
	}
	t.Log("✅ Pattern A nested field_access extracted")
}

// TestExtract_PatternB_SwitchVariable verifies Pattern B switch-case extraction with variable condition.
func TestExtract_PatternB_SwitchVariable(t *testing.T) {
	code := `package com.example;
public class PaymentService {
    void handle(OrderStatus status) {
        switch (status) {
            case PENDING: doA(); break;
            case ACTIVE: doB(); break;
        }
    }
}`
	result := parseJavaFile(t, code, "PaymentService.java")

	expectedCases := []string{"PENDING", "ACTIVE"}
	foundCases := make(map[string]bool)
	for _, ref := range result.ConstRefs {
		if ref.RefKind == "switch_case" && ref.SwitchConditionKind == "variable" && ref.SwitchVariableName == "status" {
			foundCases[ref.ConstName] = true
		}
	}

	for _, caseName := range expectedCases {
		if !foundCases[caseName] {
			t.Errorf("missing switch_case ref: %s", caseName)
		}
	}
	t.Log("✅ Pattern B switch-case with variable condition extracted")
}

// TestExtract_PatternB_SwitchMethodCall verifies Pattern B with method call condition.
func TestExtract_PatternB_SwitchMethodCall(t *testing.T) {
	code := `package com.example;
public class PaymentService {
    void handle(Order order) {
        switch (order.getStatus()) {
            case PENDING: doA(); break;
        }
    }
}`
	result := parseJavaFile(t, code, "PaymentService.java")

	found := false
	for _, ref := range result.ConstRefs {
		if ref.RefKind == "switch_case" && ref.SwitchConditionKind == "method_call" &&
			ref.SwitchMethodReceiver == "order" && ref.SwitchMethodName == "getStatus" && ref.ConstName == "PENDING" {
			found = true
			break
		}
	}
	if !found {
		t.Error("missing switch_case ref with method_call condition")
	}
	t.Log("✅ Pattern B switch-case with method call condition extracted")
}

func TestExtractMethod_TypeParams(t *testing.T) {
	tests := []struct {
		name           string
		code           string
		methodName     string
		expectedParams []string
	}{
		{
			name: "single generic parameter",
			code: `package com.example;
class Foo {
    public <T> T getBean(Class<T> clazz) { return null; }
}`,
			methodName:     "getBean",
			expectedParams: []string{"T"},
		},
		{
			name: "multiple generic parameters",
			code: `package com.example;
class Foo {
    public <K, V> V transform(K key, V def) { return def; }
}`,
			methodName:     "transform",
			expectedParams: []string{"K", "V"},
		},
		{
			name: "generic with bounds",
			code: `package com.example;
class Foo {
    public <T extends Comparable<T>> T max(T a, T b) { return a; }
}`,
			methodName:     "max",
			expectedParams: []string{"T"},
		},
		{
			name: "no generic parameters",
			code: `package com.example;
class Foo {
    public void save(String name) {}
}`,
			methodName:     "save",
			expectedParams: nil,
		},
		{
			name: "constructor has no generics",
			code: `package com.example;
class Foo {
    public Foo() {}
}`,
			methodName:     "Foo",
			expectedParams: nil,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			result := parseJavaFile(t, testCase.code, "Foo.java")
			var found *model.Symbol
			for i := range result.Symbols {
				if result.Symbols[i].Name == testCase.methodName && result.Symbols[i].Kind == "Function" {
					found = &result.Symbols[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("method %q not found in symbols", testCase.methodName)
			}
			if len(found.TypeParams) != len(testCase.expectedParams) {
				t.Fatalf("TypeParams length: got %d, want %d (got %v)", len(found.TypeParams), len(testCase.expectedParams), found.TypeParams)
			}
			for i, expected := range testCase.expectedParams {
				if found.TypeParams[i] != expected {
					t.Errorf("TypeParams[%d]: got %q, want %q", i, found.TypeParams[i], expected)
				}
			}
		})
	}
}

func TestExtractClass_TypeParams_OnlyName(t *testing.T) {
	code := `package com.example;
class Repo<T extends Entity, ID extends Serializable> {
    public void save(T entity) {}
}`
	result := parseJavaFile(t, code, "Repo.java")
	var classSymbol *model.Symbol
	for i := range result.Symbols {
		if result.Symbols[i].Kind == "Class" {
			classSymbol = &result.Symbols[i]
			break
		}
	}
	if classSymbol == nil {
		t.Fatal("class symbol not found")
	}
	expected := []string{"T", "ID"}
	if len(classSymbol.TypeParams) != len(expected) {
		t.Fatalf("TypeParams length: got %d, want %d (got %v)", len(classSymbol.TypeParams), len(expected), classSymbol.TypeParams)
	}
	for i, expectedName := range expected {
		if classSymbol.TypeParams[i] != expectedName {
			t.Errorf("TypeParams[%d]: got %q, want %q", i, classSymbol.TypeParams[i], expectedName)
		}
	}
}
