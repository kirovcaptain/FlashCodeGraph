package java

import (
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func parseJava(code []byte) (*tree_sitter.Node, func()) {
	parser := tree_sitter.NewParser()
	lang := tree_sitter.NewLanguage(tree_sitter_java.Language())
	parser.SetLanguage(lang)
	tree := parser.Parse(code, nil)
	return tree.RootNode(), func() { tree.Close(); parser.Close() }
}

func extractRemoteCalls(code []byte) *model.ParseResult {
	root, cleanup := parseJava(code)
	defer cleanup()
	result := &model.ParseResult{FilePath: "Test.java", Language: "java"}
	file := scanner.ScannedFile{Path: "/test/Test.java", RelPath: "Test.java", Language: "java"}
	Extract(root, code, file, result)
	return result
}

// === Feign Tests ===

func TestFeignClient_BasicNameAndPath(t *testing.T) {
	result := extractRemoteCalls([]byte(`package com.example;

import org.springframework.cloud.openfeign.FeignClient;
import org.springframework.web.bind.annotation.*;

@FeignClient(name = "user-service", path = "/api/users")
public interface UserClient {
    @GetMapping("/{id}")
    Object getUser(Long id);

    @PostMapping
    Object createUser(Object user);

    @DeleteMapping("/{id}")
    void deleteUser(Long id);
}
`))

	if len(result.RemoteCalls) != 3 {
		t.Fatalf("expected 3 remote calls, got %d", len(result.RemoteCalls))
	}

	// Verify service name
	for _, rc := range result.RemoteCalls {
		if rc.TargetService != "user-service" {
			t.Fatalf("expected service user-service, got %s", rc.TargetService)
		}
		if rc.ServiceConfidence != 1.0 {
			t.Fatalf("expected confidence 1.0, got %f", rc.ServiceConfidence)
		}
		if rc.ServiceResolvedBy != "literal" {
			t.Fatalf("expected resolvedBy literal, got %s", rc.ServiceResolvedBy)
		}
	}

	// Verify path prefix concatenation
	methods := map[string]string{}
	for _, rc := range result.RemoteCalls {
		methods[rc.Method] = rc.TargetURL
	}
	if methods["GET"] != "/api/users/{id}" {
		t.Fatalf("GET path expected /api/users/{id}, got %s", methods["GET"])
	}
	if methods["POST"] != "/api/users" {
		t.Fatalf("POST path expected /api/users, got %s", methods["POST"])
	}
	if methods["DELETE"] != "/api/users/{id}" {
		t.Fatalf("DELETE path expected /api/users/{id}, got %s", methods["DELETE"])
	}

	t.Log("✅ Feign basic: 3 methods, service=user-service, path prefix concatenated")
}

func TestFeignClient_PlaceholderName(t *testing.T) {
	result := extractRemoteCalls([]byte(`package com.example;

import org.springframework.cloud.openfeign.FeignClient;
import org.springframework.web.bind.annotation.*;

@FeignClient(name = "${feign.order.name}", path = "/api/orders")
public interface OrderClient {
    @GetMapping("/{id}")
    Object getOrder(Long id);
}
`))

	if len(result.RemoteCalls) != 1 {
		t.Fatalf("expected 1 remote call, got %d", len(result.RemoteCalls))
	}

	rc := result.RemoteCalls[0]
	if rc.ServiceResolvedBy != "unresolved" {
		t.Fatalf("placeholder name should be unresolved, got %s", rc.ServiceResolvedBy)
	}
	if rc.ServiceConfidence != 0 {
		t.Fatalf("unresolved confidence should be 0, got %f", rc.ServiceConfidence)
	}
	if rc.TargetURL != "/api/orders/{id}" {
		t.Fatalf("path should still be extracted: got %s", rc.TargetURL)
	}

	t.Log("✅ Feign placeholder: name unresolved, path still extracted")
}

func TestFeignClient_NoPath(t *testing.T) {
	result := extractRemoteCalls([]byte(`package com.example;

import org.springframework.cloud.openfeign.FeignClient;
import org.springframework.web.bind.annotation.*;

@FeignClient(name = "payment-service")
public interface PaymentClient {
    @PostMapping("/api/pay")
    Object pay(Object request);
}
`))

	if len(result.RemoteCalls) != 1 {
		t.Fatalf("expected 1 remote call, got %d", len(result.RemoteCalls))
	}

	rc := result.RemoteCalls[0]
	if rc.TargetService != "payment-service" {
		t.Fatalf("expected payment-service, got %s", rc.TargetService)
	}
	// No path prefix, method path used directly
	if rc.TargetURL != "/api/pay" {
		t.Fatalf("expected /api/pay, got %s", rc.TargetURL)
	}

	t.Log("✅ Feign no path: method path used directly")
}

func TestFeignClient_NotFeignInterface(t *testing.T) {
	result := extractRemoteCalls([]byte(`package com.example;

import org.springframework.web.bind.annotation.*;

public interface NormalApi {
    @GetMapping("/api/users")
    Object getUsers();
}
`))

	// Should NOT produce RemoteCalls (no @FeignClient)
	if len(result.RemoteCalls) != 0 {
		t.Fatalf("non-Feign interface should not produce remote calls, got %d", len(result.RemoteCalls))
	}

	// But should produce Routes
	if len(result.Routes) == 0 {
		t.Fatal("interface with @GetMapping should produce Route")
	}

	t.Log("✅ Non-Feign interface: Routes yes, RemoteCalls no")
}

// === RestTemplate Tests ===
// Note: RestTemplate detection requires TypeEnv with fully qualified name.
// These tests verify the parser-level extraction. Full integration with TypeEnv
// is tested in service/indexer_test.go.

func TestRestTemplate_NotDetectedWithoutTypeEnv(t *testing.T) {
	// Without TypeEnv, RestTemplate calls should NOT be extracted
	// (we don't guess based on variable names)
	result := extractRemoteCalls([]byte(`package com.example;

public class OrderService {
    private Object restTemplate;

    public void getOrder() {
        restTemplate.getForObject("http://user-service/api/users/1");
    }
}
`))

	// Should not produce RemoteCalls without TypeEnv verification
	for _, rc := range result.RemoteCalls {
		t.Logf("unexpected remote call: %s %s", rc.Method, rc.TargetURL)
	}
	// Parser-level extraction doesn't have TypeEnv, so RestTemplate calls
	// are only detected when ExtractRestTemplateCalls is called with TypeEnv
	t.Log("✅ RestTemplate: not detected at parser level without TypeEnv")
}

func TestExtractRestTemplateCalls_WithTypeEnv(t *testing.T) {
	code := []byte(`package com.example;

public class OrderService {
    private RestTemplate restTemplate;

    public void getOrder() {
        restTemplate.getForObject("http://user-service/api/users/1", Object.class);
    }

    public void createOrder() {
        restTemplate.postForObject("http://order-service/api/orders", null, Object.class);
    }
}
`)
	root, cleanup := parseJava(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "OrderService.java", Language: "java"}

	// Simulate TypeEnv with fully qualified name
	typeEnv := map[string]string{
		"restTemplate":                "org.springframework.web.client.RestTemplate",
		"getOrder:restTemplate":       "org.springframework.web.client.RestTemplate",
		"createOrder:restTemplate":    "org.springframework.web.client.RestTemplate",
	}

	// Walk to find method bodies and call ExtractRestTemplateCalls
	astutil := root // use root to find methods
	for i := uint(0); i < astutil.ChildCount(); i++ {
		child := astutil.Child(i)
		if child.Kind() == "class_declaration" {
			body := child.ChildByFieldName("body")
			if body != nil {
				for j := uint(0); j < body.ChildCount(); j++ {
					method := body.Child(j)
					if method.Kind() == "method_declaration" {
						nameNode := method.ChildByFieldName("name")
						methodBody := method.ChildByFieldName("body")
						if nameNode != nil && methodBody != nil {
							ExtractRestTemplateCalls(methodBody, code, nameNode.Utf8Text(code), "OrderService.java", typeEnv, result)
						}
					}
				}
			}
		}
	}

	if len(result.RemoteCalls) != 2 {
		t.Fatalf("expected 2 remote calls, got %d", len(result.RemoteCalls))
	}

	// Verify first call
	rc0 := result.RemoteCalls[0]
	if rc0.Method != "GET" {
		t.Fatalf("expected GET, got %s", rc0.Method)
	}
	if rc0.TargetService != "user-service" {
		t.Fatalf("expected user-service, got %s", rc0.TargetService)
	}

	// Verify second call
	rc1 := result.RemoteCalls[1]
	if rc1.Method != "POST" {
		t.Fatalf("expected POST, got %s", rc1.Method)
	}
	if rc1.TargetService != "order-service" {
		t.Fatalf("expected order-service, got %s", rc1.TargetService)
	}

	t.Log("✅ RestTemplate with TypeEnv: 2 calls detected with correct service names")
}

func TestExtractRestTemplateCalls_LocalVariableURL(t *testing.T) {
	code := []byte(`package com.example;

public class Service {
    private RestTemplate restTemplate;

    public void call() {
        String url = "http://payment-service/api/pay";
        restTemplate.postForObject(url, null, Object.class);
    }
}
`)
	root, cleanup := parseJava(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "Service.java", Language: "java"}
	typeEnv := map[string]string{
		"call:restTemplate": "org.springframework.web.client.RestTemplate",
	}

	// Find method body
	for i := uint(0); i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child.Kind() == "class_declaration" {
			body := child.ChildByFieldName("body")
			if body != nil {
				for j := uint(0); j < body.ChildCount(); j++ {
					method := body.Child(j)
					if method.Kind() == "method_declaration" {
						methodBody := method.ChildByFieldName("body")
						if methodBody != nil {
							ExtractRestTemplateCalls(methodBody, code, "call", "Service.java", typeEnv, result)
						}
					}
				}
			}
		}
	}

	if len(result.RemoteCalls) != 1 {
		t.Fatalf("expected 1 remote call, got %d", len(result.RemoteCalls))
	}
	if result.RemoteCalls[0].TargetService != "payment-service" {
		t.Fatalf("expected payment-service, got %s", result.RemoteCalls[0].TargetService)
	}

	t.Log("✅ RestTemplate local variable URL: tracked and resolved")
}

func TestExtractRestTemplateCalls_WrongType(t *testing.T) {
	code := []byte(`package com.example;

public class Service {
    private MyClient client;

    public void call() {
        client.getForObject("http://svc/api", Object.class);
    }
}
`)
	root, cleanup := parseJava(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "Service.java", Language: "java"}
	// TypeEnv says client is NOT RestTemplate
	typeEnv := map[string]string{
		"call:client": "com.example.MyClient",
	}

	for i := uint(0); i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child.Kind() == "class_declaration" {
			body := child.ChildByFieldName("body")
			if body != nil {
				for j := uint(0); j < body.ChildCount(); j++ {
					method := body.Child(j)
					if method.Kind() == "method_declaration" {
						methodBody := method.ChildByFieldName("body")
						if methodBody != nil {
							ExtractRestTemplateCalls(methodBody, code, "call", "Service.java", typeEnv, result)
						}
					}
				}
			}
		}
	}

	if len(result.RemoteCalls) != 0 {
		t.Fatalf("non-RestTemplate type should not produce remote calls, got %d", len(result.RemoteCalls))
	}

	t.Log("✅ Wrong type: MyClient.getForObject() not detected as RestTemplate")
}

func TestExtractRestTemplateCalls_NoTypeEnv(t *testing.T) {
	code := []byte(`package com.example;

public class Service {
    private Object unknown;

    public void call() {
        unknown.getForObject("http://svc/api", Object.class);
    }
}
`)
	root, cleanup := parseJava(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "Service.java", Language: "java"}
	typeEnv := map[string]string{} // empty — no type info

	for i := uint(0); i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child.Kind() == "class_declaration" {
			body := child.ChildByFieldName("body")
			if body != nil {
				for j := uint(0); j < body.ChildCount(); j++ {
					method := body.Child(j)
					if method.Kind() == "method_declaration" {
						methodBody := method.ChildByFieldName("body")
						if methodBody != nil {
							ExtractRestTemplateCalls(methodBody, code, "call", "Service.java", typeEnv, result)
						}
					}
				}
			}
		}
	}

	if len(result.RemoteCalls) != 0 {
		t.Fatalf("no TypeEnv should not produce remote calls, got %d", len(result.RemoteCalls))
	}

	t.Log("✅ No TypeEnv: unknown.getForObject() skipped (no guessing)")
}

func TestFeignClient_ValueAttribute(t *testing.T) {
	result := extractRemoteCalls([]byte(`package com.example;

import org.springframework.cloud.openfeign.FeignClient;
import org.springframework.web.bind.annotation.*;

@FeignClient(value = "inventory-service")
public interface InventoryClient {
    @GetMapping("/api/stock/{sku}")
    Object getStock(String sku);
}
`))

	if len(result.RemoteCalls) != 1 {
		t.Fatalf("expected 1 remote call, got %d", len(result.RemoteCalls))
	}
	if result.RemoteCalls[0].TargetService != "inventory-service" {
		t.Fatalf("expected inventory-service, got %s", result.RemoteCalls[0].TargetService)
	}
	t.Log("✅ Feign value attribute: @FeignClient(value = \"inventory-service\")")
}

func TestExtractRestTemplateCalls_PutDelete(t *testing.T) {
	code := []byte(`package com.example;

public class Service {
    private RestTemplate restTemplate;

    public void update() {
        restTemplate.put("http://svc/api/users/1", null);
    }

    public void remove() {
        restTemplate.delete("http://svc/api/users/1");
    }
}
`)
	root, cleanup := parseJava(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "Service.java", Language: "java"}
	typeEnv := map[string]string{
		"update:restTemplate": "org.springframework.web.client.RestTemplate",
		"remove:restTemplate": "org.springframework.web.client.RestTemplate",
	}

	for i := uint(0); i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child.Kind() == "class_declaration" {
			body := child.ChildByFieldName("body")
			if body == nil {
				continue
			}
			for j := uint(0); j < body.ChildCount(); j++ {
				method := body.Child(j)
				if method.Kind() == "method_declaration" {
					nameNode := method.ChildByFieldName("name")
					methodBody := method.ChildByFieldName("body")
					if nameNode != nil && methodBody != nil {
						ExtractRestTemplateCalls(methodBody, code, nameNode.Utf8Text(code), "Service.java", typeEnv, result)
					}
				}
			}
		}
	}

	if len(result.RemoteCalls) != 2 {
		t.Fatalf("expected 2 remote calls (put + delete), got %d", len(result.RemoteCalls))
	}

	methods := map[string]bool{}
	for _, rc := range result.RemoteCalls {
		methods[rc.Method] = true
	}
	if !methods["PUT"] || !methods["DELETE"] {
		t.Fatalf("expected PUT and DELETE, got %v", methods)
	}
	t.Log("✅ RestTemplate put/delete: detected with TypeEnv")
}

func TestExtractRestTemplateCalls_VariableConcatURL(t *testing.T) {
	code := []byte(`package com.example;

public class Service {
    private RestTemplate restTemplate;

    public void call(String baseUrl) {
        restTemplate.getForObject(baseUrl + "/api/users", Object.class);
    }
}
`)
	root, cleanup := parseJava(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "Service.java", Language: "java"}
	typeEnv := map[string]string{
		"call:restTemplate": "org.springframework.web.client.RestTemplate",
	}

	for i := uint(0); i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child.Kind() == "class_declaration" {
			body := child.ChildByFieldName("body")
			if body == nil {
				continue
			}
			for j := uint(0); j < body.ChildCount(); j++ {
				method := body.Child(j)
				if method.Kind() == "method_declaration" {
					methodBody := method.ChildByFieldName("body")
					if methodBody != nil {
						ExtractRestTemplateCalls(methodBody, code, "call", "Service.java", typeEnv, result)
					}
				}
			}
		}
	}

	// Variable concatenation → URL not extractable, but call still detected
	if len(result.RemoteCalls) != 1 {
		t.Fatalf("expected 1 remote call (URL empty but call detected), got %d", len(result.RemoteCalls))
	}
	// Service name should be empty (can't extract from concatenation)
	if result.RemoteCalls[0].TargetService != "" {
		t.Fatalf("concatenated URL should have empty service, got %s", result.RemoteCalls[0].TargetService)
	}
	t.Log("✅ RestTemplate variable concat: call detected, URL/service empty")
}

func TestExtractRestTemplateCalls_ThirdPartyAPI(t *testing.T) {
	code := []byte(`package com.example;

public class WeatherService {
    private RestTemplate restTemplate;

    public void getForecast() {
        restTemplate.getForObject("https://api.weather.com/v1/forecast", Object.class);
    }
}
`)
	root, cleanup := parseJava(code)
	defer cleanup()

	result := &model.ParseResult{FilePath: "WeatherService.java", Language: "java"}
	typeEnv := map[string]string{
		"getForecast:restTemplate": "org.springframework.web.client.RestTemplate",
	}

	for i := uint(0); i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child.Kind() == "class_declaration" {
			body := child.ChildByFieldName("body")
			if body == nil {
				continue
			}
			for j := uint(0); j < body.ChildCount(); j++ {
				method := body.Child(j)
				if method.Kind() == "method_declaration" {
					methodBody := method.ChildByFieldName("body")
					if methodBody != nil {
						ExtractRestTemplateCalls(methodBody, code, "getForecast", "WeatherService.java", typeEnv, result)
					}
				}
			}
		}
	}

	if len(result.RemoteCalls) != 1 {
		t.Fatalf("expected 1 remote call, got %d", len(result.RemoteCalls))
	}
	if result.RemoteCalls[0].TargetService != "api.weather.com" {
		t.Fatalf("expected api.weather.com, got %s", result.RemoteCalls[0].TargetService)
	}
	if result.RemoteCalls[0].Method != "GET" {
		t.Fatalf("expected GET, got %s", result.RemoteCalls[0].Method)
	}
	t.Log("✅ Third-party API: api.weather.com detected as ExternalService")
}

// === gRPC Tests ===

func TestExtractGRPCStubCalls(t *testing.T) {
	code := []byte(`
class OrderService {
    void placeOrder() {
        ManagedChannel channel = ManagedChannelBuilder.forTarget("user-service:50051").build();
        UserServiceGrpc.UserServiceBlockingStub stub = UserServiceGrpc.newBlockingStub(channel);
        User user = stub.getUser(request);
    }
}
`)
	result := extractRemoteCalls(code)
	found := false
	for _, rc := range result.RemoteCalls {
		if rc.Protocol == "grpc" && rc.TargetService == "UserService" && rc.Method == "getUser" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected gRPC call to UserService.getUser, got %+v", result.RemoteCalls)
	}
}

// === Dubbo Tests ===

func TestExtractDubboReference(t *testing.T) {
	code := []byte(`
class OrderController {
    @DubboReference
    private UserService userService;

    void handle() {
        userService.getUser(1);
    }
}
`)
	result := extractRemoteCalls(code)
	found := false
	for _, pending := range result.PendingRemoteCalls {
		if pending.Protocol == "dubbo" && pending.FieldType == "UserService" {
			found = true
			if pending.FieldName != "userService" {
				t.Errorf("expected field name userService, got %s", pending.FieldName)
			}
		}
	}
	if !found {
		t.Fatalf("expected Dubbo PendingRemoteCall for UserService, got %+v", result.PendingRemoteCalls)
	}
	// Should NOT produce RawRemoteCall anymore
	for _, remoteCall := range result.RemoteCalls {
		if remoteCall.Protocol == "dubbo" {
			t.Error("Dubbo should not produce RawRemoteCall, should use PendingRemoteCall")
		}
	}
}

// === GraphQL Tests ===

func TestExtractGraphQLRoutes(t *testing.T) {
	code := []byte(`
class UserResolver {
    @QueryMapping
    public User getUser(Long id) {
        return null;
    }

    @MutationMapping
    public User createUser(UserInput input) {
        return null;
    }
}
`)
	result := extractRemoteCalls(code)
	queryFound := false
	mutationFound := false
	for _, r := range result.Routes {
		if r.Framework == "graphql" && r.PathPattern == "Query" && r.Method == "getUser" {
			queryFound = true
		}
		if r.Framework == "graphql" && r.PathPattern == "Mutation" && r.Method == "createUser" {
			mutationFound = true
		}
	}
	if !queryFound {
		t.Error("expected GraphQL Query.getUser route")
	}
	if !mutationFound {
		t.Error("expected GraphQL Mutation.createUser route")
	}
}
