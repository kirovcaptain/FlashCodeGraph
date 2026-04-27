package java

import (
	"testing"
)

func TestExtractGrpcClientField(t *testing.T) {
	code := []byte(`package com.example;

class PaymentCaller {
    @GrpcClient("payment-service")
    private PaymentServiceGrpc.PaymentServiceBlockingStub paymentStub;

    void charge(String orderId) {
        paymentStub.charge(orderId);
    }
}
`)
	result := extractRemoteCalls(code)

	found := false
	for _, pending := range result.PendingRemoteCalls {
		if pending.Protocol == "grpc" && pending.FieldName == "paymentStub" {
			found = true
			if pending.OwnerClass != "com.example.PaymentCaller" {
				t.Errorf("expected owner com.example.PaymentCaller, got %s", pending.OwnerClass)
			}
			if pending.FieldType != "PaymentServiceGrpc.PaymentServiceBlockingStub" {
				t.Errorf("expected field type from declaration (not @GrpcClient value), got %s", pending.FieldType)
			}
		}
	}
	if !found {
		t.Fatalf("expected gRPC PendingRemoteCall, got %+v", result.PendingRemoteCalls)
	}

	// Should NOT produce RawRemoteCall for @GrpcClient
	for _, remoteCall := range result.RemoteCalls {
		if remoteCall.CallerName == "PaymentCaller.paymentStub" {
			t.Error("@GrpcClient should not produce RawRemoteCall")
		}
	}
}

func TestExtractDubboReference_PendingRemoteCall(t *testing.T) {
	code := []byte(`package com.example.service;

class OrderService {
    @DubboReference(version = "1.0.0", group = "payment")
    private PaymentService paymentService;

    void process() {
        paymentService.pay();
    }
}
`)
	result := extractRemoteCalls(code)

	found := false
	for _, pending := range result.PendingRemoteCalls {
		if pending.Protocol == "dubbo" && pending.FieldName == "paymentService" {
			found = true
			if pending.FieldType != "PaymentService" {
				t.Errorf("expected field type PaymentService, got %s", pending.FieldType)
			}
			if pending.OwnerClass != "com.example.service.OrderService" {
				t.Errorf("expected owner com.example.service.OrderService, got %s", pending.OwnerClass)
			}
			// Check annotations contain DubboReference with params
			hasAnnotation := false
			for _, annotation := range pending.Annotations {
				if annotation.Name == "DubboReference" {
					hasAnnotation = true
					if annotation.Params["version"] != "1.0.0" {
						t.Errorf("expected version 1.0.0, got %s", annotation.Params["version"])
					}
					if annotation.Params["group"] != "payment" {
						t.Errorf("expected group payment, got %s", annotation.Params["group"])
					}
				}
			}
			if !hasAnnotation {
				t.Error("expected DubboReference annotation with params")
			}
		}
	}
	if !found {
		t.Fatalf("expected Dubbo PendingRemoteCall, got %+v", result.PendingRemoteCalls)
	}
}

func TestExtractPendingRemoteCall_NoUsage(t *testing.T) {
	// Field declared but never called — should still produce PendingRemoteCall
	// (actual matching happens in indexer post-processing, not parser)
	code := []byte(`package com.example;

class Unused {
    @DubboReference
    private SomeService someService;
}
`)
	result := extractRemoteCalls(code)

	if len(result.PendingRemoteCalls) != 1 {
		t.Fatalf("expected 1 PendingRemoteCall, got %d", len(result.PendingRemoteCalls))
	}
	if result.PendingRemoteCalls[0].FieldName != "someService" {
		t.Errorf("expected someService, got %s", result.PendingRemoteCalls[0].FieldName)
	}
}
