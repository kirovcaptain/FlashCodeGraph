package typeinfer

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestLookupReturnType_ResolvesToFQN(t *testing.T) {
	findByName := func(name string) []model.Symbol {
		switch name {
		case "createService":
			return []model.Symbol{{Name: "createService", Kind: constants.KindFunction, ReturnTypes: []string{"MyService"}}}
		case "MyService":
			return []model.Symbol{{Name: "MyService", Kind: constants.KindInterface, QualifiedName: "pkg.services.MyService"}}
		}
		return nil
	}

	result := lookupReturnType("createService", findByName, nil)
	if result != "pkg.services.MyService" {
		t.Errorf("expected FQN 'pkg.services.MyService', got %q", result)
	}
}

func TestLookupReturnType_NoMatchFallsBackToShortName(t *testing.T) {
	findByName := func(name string) []model.Symbol {
		if name == "getData" {
			return []model.Symbol{{Name: "getData", Kind: constants.KindFunction, ReturnTypes: []string{"string"}}}
		}
		return nil
	}

	result := lookupReturnType("getData", findByName, nil)
	if result != "string" {
		t.Errorf("expected 'string', got %q", result)
	}
}

func TestResolveFixpoint_CallResultFQN(t *testing.T) {
	infer := New()

	env := &model.TypeEnv{
		Bindings: make(map[string]*model.TypeInfo),
		Imports: []model.RawImport{
			{SymbolName: "MyService", ModulePath: "pkg.services.MyService"},
		},
	}

	pendings := []model.PendingAssignment{
		{Kind: "call_result", LHS: "svc", Scope: "main", Callee: "createService"},
	}

	findByName := func(name string) []model.Symbol {
		switch name {
		case "createService":
			return []model.Symbol{{Name: "createService", Kind: constants.KindFunction, ReturnTypes: []string{"MyService"}}}
		case "MyService":
			return []model.Symbol{{Name: "MyService", Kind: constants.KindInterface, QualifiedName: "pkg.services.MyService"}}
		}
		return nil
	}

	infer.ResolveFixpoint(env, pendings, findByName)

	key := scopedKey("main", "svc")
	info, exists := env.Bindings[key]
	if !exists {
		t.Fatal("svc not resolved")
	}
	if info.TypeName != "pkg.services.MyService" {
		t.Errorf("svc type = %q, want 'pkg.services.MyService'", info.TypeName)
	}
}
