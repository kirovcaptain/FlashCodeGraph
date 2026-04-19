package graphql

import "testing"

func TestParseSchema(t *testing.T) {
	schema := `
type Query {
    getUser(id: ID!): User
    listUsers(page: Int): [User]
}

type Mutation {
    createUser(input: UserInput!): User
    deleteUser(id: ID!): Boolean
}

type Subscription {
    onUserCreated: User
}

type User {
    id: ID!
    name: String
}
`
	ops := ParseSchema(schema)
	want := map[string]string{
		"Query.getUser":                "",
		"Query.listUsers":             "",
		"Mutation.createUser":          "",
		"Mutation.deleteUser":          "",
		"Subscription.onUserCreated":   "",
	}
	for _, op := range ops {
		key := op.Type + "." + op.Field
		if _, ok := want[key]; !ok {
			t.Errorf("unexpected operation: %s", key)
		}
		delete(want, key)
	}
	for key := range want {
		t.Errorf("missing operation: %s", key)
	}
}

func TestParseSchema_Empty(t *testing.T) {
	ops := ParseSchema("type User { id: ID! name: String }")
	if len(ops) != 0 {
		t.Errorf("expected 0 ops from non-Query type, got %d", len(ops))
	}
}

func TestParseSchema_NestedBraces(t *testing.T) {
	schema := `
type Query {
    getUser(filter: { name: String }): User
    listOrders: [Order]
}
`
	ops := ParseSchema(schema)
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops, got %d", len(ops))
	}
	if ops[0].Field != "getUser" || ops[1].Field != "listOrders" {
		t.Errorf("unexpected fields: %v", ops)
	}
}

func TestIsGraphQLFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"schema.graphql", true},
		{"schema.gql", true},
		{"schema.GRAPHQL", true},
		{"schema.go", false},
		{"graphql.ts", false},
	}
	for _, tt := range tests {
		if got := IsGraphQLFile(tt.path); got != tt.want {
			t.Errorf("IsGraphQLFile(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
