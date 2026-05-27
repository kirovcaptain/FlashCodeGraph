package typeinfer

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestResolveFixpoint_MethodCallResult_PropagatesTypeArgs(t *testing.T) {
	// Scenario: var users = repo.findAll()
	// findAll() returns List<User> → TypeEnv should have users={TypeName:"List", TypeArgs:[{Name:"User"}]}
	env := &model.TypeEnv{
		Bindings: map[string]*model.TypeInfo{
			"testMethod:repo": {TypeName: "Repository", Tier: 1},
		},
	}

	pendings := []model.PendingAssignment{
		{
			LHS:      "users",
			Kind:     "method_call_result",
			Receiver: "repo",
			Method:   "findAll",
			Scope:    "testMethod",
		},
	}

	findByName := func(name string) []model.Symbol {
		switch name {
		case "findAll":
			return []model.Symbol{{
				Name:          "findAll",
				QualifiedName: "com.example.Repository.findAll",
				Kind:          "Function",
				ReturnTypes:   []model.ReturnType{{Name: "List", Args: []model.TypeArg{{Name: "User"}}}},
			}}
		case "Repository":
			return []model.Symbol{{
				Name:          "Repository",
				QualifiedName: "com.example.Repository",
				Kind:          "Class",
			}}
		case "List":
			return []model.Symbol{{
				Name:          "List",
				QualifiedName: "java.util.List",
				Kind:          "Interface",
				TypeParams:    []string{"T"},
			}}
		}
		return nil
	}

	infer := &TypeInfer{}
	infer.ResolveFixpoint(env, pendings, findByName)

	// Verify: users should have TypeName containing "List"
	binding := lookupBindingInEnv(env, "testMethod", "users")
	if binding == nil {
		t.Fatal("users binding not found in TypeEnv")
	}
	if binding.TypeName == "" {
		t.Fatal("users TypeName is empty")
	}
	t.Logf("users TypeName=%q, TypeArgs=%v", binding.TypeName, binding.TypeArgs)

	// Key assertion: TypeArgs should contain [{Name:"User"}]
	if len(binding.TypeArgs) == 0 {
		t.Fatal("users TypeArgs is empty — method_call_result did not propagate ReturnType.Args")
	}
	if binding.TypeArgs[0].Name != "User" {
		t.Errorf("users TypeArgs[0].Name=%q, want \"User\"", binding.TypeArgs[0].Name)
	}

	t.Log("✅ method_call_result propagates TypeArgs from ReturnType")
}
