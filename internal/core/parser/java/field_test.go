package java

import (
	"encoding/json"
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestExtractAnnotations_Structured(t *testing.T) {
	code := []byte(`package com.example;

@RestController
@RequestMapping("/api/users")
@FeignClient(name = "user-service", path = "/api")
public class UserController {
}`)
	result := extractRemoteCalls(code)

	// Find UserController symbol and check annotations
	for _, symbol := range result.Symbols {
		if symbol.Name != "UserController" {
			continue
		}
		var annotations []model.StructuredAnnotation
		if err := json.Unmarshal([]byte(symbol.Annotations), &annotations); err != nil {
			t.Fatalf("failed to parse annotations: %v", err)
		}

		if len(annotations) < 3 {
			t.Fatalf("expected at least 3 annotations, got %d: %v", len(annotations), annotations)
		}

		// Check RestController (no params)
		found := false
		for _, annotation := range annotations {
			if annotation.Name == "RestController" && len(annotation.Params) == 0 {
				found = true
			}
		}
		if !found {
			t.Error("expected @RestController with no params")
		}

		// Check RequestMapping (single value param)
		found = false
		for _, annotation := range annotations {
			if annotation.Name == "RequestMapping" && annotation.Params["value"] == "/api/users" {
				found = true
			}
		}
		if !found {
			t.Error("expected @RequestMapping with value=/api/users")
		}

		// Check FeignClient (multiple params)
		found = false
		for _, annotation := range annotations {
			if annotation.Name == "FeignClient" && annotation.Params["name"] == "user-service" && annotation.Params["path"] == "/api" {
				found = true
			}
		}
		if !found {
			t.Error("expected @FeignClient with name=user-service, path=/api")
		}
		return
	}
	t.Fatal("UserController symbol not found")
}

func TestExtractField_FieldDeclaration(t *testing.T) {
	code := []byte(`package com.example;
public class OrderService {
    private String name;
    private PaymentClient paymentClient;
    private static final int MAX = 100;

    @Autowired
    private UserService userService;

    public void create() {}
}`)
	result := extractRemoteCalls(code)
	t.Logf("Fields: %d", len(result.Fields))
	for _, field := range result.Fields {
		t.Logf("  %s: %s (owner=%s, vis=%s, static=%v, annotations=%v)",
			field.Name, field.Type, field.OwnerQualifiedName, field.Visibility, field.IsStatic, field.Annotations)
	}

	if len(result.Fields) == 0 {
		t.Fatal("expected fields to be extracted")
	}

	// static final MAX should be filtered
	for _, field := range result.Fields {
		if field.Name == "MAX" {
			t.Error("static final constant MAX should be filtered out")
		}
	}

	// paymentClient should be present with correct type
	found := false
	for _, field := range result.Fields {
		if field.Name == "paymentClient" && field.Type == "PaymentClient" && field.Visibility == "private" {
			found = true
		}
	}
	if !found {
		t.Error("expected paymentClient: PaymentClient (private)")
	}

	// userService should have @Autowired annotation
	found = false
	for _, field := range result.Fields {
		if field.Name == "userService" && len(field.Annotations) > 0 && field.Annotations[0].Name == "Autowired" {
			found = true
		}
	}
	if !found {
		t.Error("expected userService with @Autowired annotation")
	}
}

func TestExtractField_OwnerQualifiedName(t *testing.T) {
	code := []byte(`package com.example.service;
public class PaymentController {
    private PaymentServiceImpl paymentService;
}`)
	result := extractRemoteCalls(code)
	if len(result.Fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(result.Fields))
	}
	if result.Fields[0].OwnerQualifiedName != "com.example.service.PaymentController" {
		t.Errorf("expected owner com.example.service.PaymentController, got %s", result.Fields[0].OwnerQualifiedName)
	}
}
