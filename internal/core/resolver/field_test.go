package resolver

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestSymbolTable_FieldByOwner(t *testing.T) {
	table := NewSymbolTable()

	table.AddField("com.example.OrderService", model.FieldInfo{
		Name: "paymentClient", Type: "PaymentClient", Visibility: "private",
	})
	table.AddField("com.example.OrderService", model.FieldInfo{
		Name: "userService", Type: "UserService", Visibility: "private",
	})
	table.AddField("com.example.UserController", model.FieldInfo{
		Name: "orderService", Type: "OrderService", Visibility: "private",
	})

	// FindFieldByOwner - exact match
	field := table.FindFieldByOwner("com.example.OrderService", "paymentClient")
	if field == nil {
		t.Fatal("expected to find paymentClient")
	}
	if field.Type != "PaymentClient" {
		t.Errorf("expected type PaymentClient, got %s", field.Type)
	}

	// FindFieldByOwner - not found
	field = table.FindFieldByOwner("com.example.OrderService", "nonExistent")
	if field != nil {
		t.Error("expected nil for non-existent field")
	}

	// FindFieldByOwner - wrong owner
	field = table.FindFieldByOwner("com.example.UserController", "paymentClient")
	if field != nil {
		t.Error("expected nil for wrong owner")
	}

	// FindFieldsByOwner - all fields
	fields := table.FindFieldsByOwner("com.example.OrderService")
	if len(fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(fields))
	}

	// FindFieldsByOwner - empty
	fields = table.FindFieldsByOwner("com.example.NonExistent")
	if len(fields) != 0 {
		t.Errorf("expected 0 fields, got %d", len(fields))
	}
}
