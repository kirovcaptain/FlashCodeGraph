package resolver

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestComputeMRO_SingleInheritance(t *testing.T) {
	table := NewSymbolTable()
	resolver := NewResolver(table)

	heritage := []model.RawHeritage{
		{ChildName: "GrandChild", ParentName: "Child", Kind: "extends"},
		{ChildName: "Child", ParentName: "Base", Kind: "extends"},
	}

	mro := resolver.ComputeMRO("GrandChild", heritage)
	expected := []string{"GrandChild", "Child", "Base"}
	if len(mro) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, mro)
	}
	for i, name := range expected {
		if mro[i] != name {
			t.Fatalf("mro[%d] expected %s, got %s", i, name, mro[i])
		}
	}
	t.Log("✅ Single inheritance MRO works")
}

func TestComputeMRO_NoParent(t *testing.T) {
	table := NewSymbolTable()
	resolver := NewResolver(table)

	mro := resolver.ComputeMRO("Standalone", nil)
	if len(mro) != 1 || mro[0] != "Standalone" {
		t.Fatalf("expected [Standalone], got %v", mro)
	}
	t.Log("✅ No-parent MRO returns self only")
}

func TestComputeMRO_MultipleInheritance(t *testing.T) {
	table := NewSymbolTable()
	resolver := NewResolver(table)

	// Diamond: D → B, C → A
	heritage := []model.RawHeritage{
		{ChildName: "D", ParentName: "B", Kind: "extends"},
		{ChildName: "D", ParentName: "C", Kind: "extends"},
		{ChildName: "B", ParentName: "A", Kind: "extends"},
		{ChildName: "C", ParentName: "A", Kind: "extends"},
	}

	mro := resolver.ComputeMRO("D", heritage)
	// C3: D, B, C, A
	expected := []string{"D", "B", "C", "A"}
	if len(mro) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, mro)
	}
	for i, name := range expected {
		if mro[i] != name {
			t.Fatalf("mro[%d] expected %s, got %s", i, name, mro[i])
		}
	}
	t.Logf("✅ Diamond MRO: %v", mro)
}

func TestComputeMRO_DeepDiamond(t *testing.T) {
	table := NewSymbolTable()
	resolver := NewResolver(table)

	// E → C, D; C → A; D → B; B → A
	// Expected C3: E, C, D, B, A
	heritage := []model.RawHeritage{
		{ChildName: "E", ParentName: "C", Kind: "extends"},
		{ChildName: "E", ParentName: "D", Kind: "extends"},
		{ChildName: "C", ParentName: "A", Kind: "extends"},
		{ChildName: "D", ParentName: "B", Kind: "extends"},
		{ChildName: "B", ParentName: "A", Kind: "extends"},
	}

	mro := resolver.ComputeMRO("E", heritage)
	if mro[0] != "E" {
		t.Fatalf("first should be E, got %s", mro[0])
	}
	if mro[len(mro)-1] != "A" {
		t.Fatalf("last should be A, got %s", mro[len(mro)-1])
	}
	// C must come before D (left-to-right precedence)
	cIdx, dIdx := -1, -1
	for i, name := range mro {
		if name == "C" {
			cIdx = i
		}
		if name == "D" {
			dIdx = i
		}
	}
	if cIdx > dIdx {
		t.Fatalf("C should come before D, got %v", mro)
	}
	t.Logf("✅ Deep diamond MRO: %v", mro)
}

func TestComputeMRO_MiddleClass(t *testing.T) {
	table := NewSymbolTable()
	resolver := NewResolver(table)

	// Query MRO for a middle class, not the leaf
	heritage := []model.RawHeritage{
		{ChildName: "C", ParentName: "B", Kind: "extends"},
		{ChildName: "B", ParentName: "A", Kind: "extends"},
	}

	mro := resolver.ComputeMRO("B", heritage)
	expected := []string{"B", "A"}
	if len(mro) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, mro)
	}
	t.Log("✅ Middle class MRO works")
}

func TestDetectOverrides(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "base_speak", Name: "speak", QualifiedName: "Animal.speak", Kind: "Function", FilePath: "animal.py"},
		{ID: "base_class", Name: "Animal", QualifiedName: "Animal", Kind: "Class", FilePath: "animal.py"},
		{ID: "child_speak", Name: "speak", QualifiedName: "Dog.speak", Kind: "Function", FilePath: "dog.py"},
		{ID: "child_class", Name: "Dog", QualifiedName: "Dog", Kind: "Class", FilePath: "dog.py"},
		{ID: "child_fetch", Name: "fetch", QualifiedName: "Dog.fetch", Kind: "Function", FilePath: "dog.py"},
	})
	resolver := NewResolver(table)

	heritage := []model.RawHeritage{
		{ChildName: "Dog", ChildQualified: "Dog", ParentName: "Animal", Kind: "extends", FilePath: "dog.py"},
	}

	overrides := resolver.DetectOverrides(heritage)
	if len(overrides) != 2 {
		t.Fatalf("expected 2 relations (1 override + 1 dispatch), got %d", len(overrides))
	}
	if overrides[0].SourceID != "child_speak" {
		t.Fatalf("expected source child_speak, got %s", overrides[0].SourceID)
	}
	if overrides[0].TargetID != "base_speak" {
		t.Fatalf("expected target base_speak, got %s", overrides[0].TargetID)
	}
	if overrides[0].Kind != model.RelOverrides {
		t.Fatalf("expected OVERRIDES, got %s", overrides[0].Kind)
	}
	t.Log("✅ DetectOverrides works")
}

func TestDetectOverrides_TransitiveGrandparent(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "base_class", Name: "Base", QualifiedName: "Base", Kind: "Class", FilePath: "base.py"},
		{ID: "base_run", Name: "run", QualifiedName: "Base.run", Kind: "Function", FilePath: "base.py"},
		{ID: "mid_class", Name: "Middle", QualifiedName: "Middle", Kind: "Class", FilePath: "mid.py"},
		// Middle does NOT override run
		{ID: "leaf_class", Name: "Leaf", QualifiedName: "Leaf", Kind: "Class", FilePath: "leaf.py"},
		{ID: "leaf_run", Name: "run", QualifiedName: "Leaf.run", Kind: "Function", FilePath: "leaf.py"},
	})
	resolver := NewResolver(table)

	heritage := []model.RawHeritage{
		{ChildName: "Leaf", ChildQualified: "Leaf", ParentName: "Middle", Kind: "extends"},
		{ChildName: "Middle", ChildQualified: "Middle", ParentName: "Base", Kind: "extends"},
	}

	overrides := resolver.DetectOverrides(heritage)
	// Leaf.run should override Base.run (skipping Middle which has no run)
	found := false
	for _, override := range overrides {
		if override.SourceID == "leaf_run" && override.TargetID == "base_run" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Leaf.run → Base.run override, got %v", overrides)
	}
	t.Log("✅ Transitive grandparent override detected")
}

func TestDetectOverrides_SkipsConstructors(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "base_class", Name: "Base", QualifiedName: "Base", Kind: "Class", FilePath: "base.py"},
		{ID: "base_init", Name: "__init__", QualifiedName: "Base.__init__", Kind: "Function", FilePath: "base.py", IsConstructor: true},
		{ID: "child_class", Name: "Child", QualifiedName: "Child", Kind: "Class", FilePath: "child.py"},
		{ID: "child_init", Name: "__init__", QualifiedName: "Child.__init__", Kind: "Function", FilePath: "child.py", IsConstructor: true},
	})
	resolver := NewResolver(table)

	heritage := []model.RawHeritage{
		{ChildName: "Child", ChildQualified: "Child", ParentName: "Base", Kind: "extends", FilePath: "child.py"},
	}

	overrides := resolver.DetectOverrides(heritage)
	if len(overrides) != 0 {
		t.Fatalf("expected 0 overrides (constructors skipped), got %d", len(overrides))
	}
	t.Log("✅ Constructors excluded from overrides")
}

func TestDetectOverrides_MultipleParents(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "a_class", Name: "A", QualifiedName: "A", Kind: "Class", FilePath: "a.py"},
		{ID: "a_run", Name: "run", QualifiedName: "A.run", Kind: "Function", FilePath: "a.py"},
		{ID: "b_class", Name: "B", QualifiedName: "B", Kind: "Class", FilePath: "b.py"},
		{ID: "b_run", Name: "run", QualifiedName: "B.run", Kind: "Function", FilePath: "b.py"},
		{ID: "c_class", Name: "C", QualifiedName: "C", Kind: "Class", FilePath: "c.py"},
		{ID: "c_run", Name: "run", QualifiedName: "C.run", Kind: "Function", FilePath: "c.py"},
	})
	resolver := NewResolver(table)

	heritage := []model.RawHeritage{
		{ChildName: "C", ChildQualified: "C", ParentName: "A", Kind: "extends"},
		{ChildName: "C", ChildQualified: "C", ParentName: "B", Kind: "extends"},
	}

	overrides := resolver.DetectOverrides(heritage)
	// C.run should override both A.run and B.run → 2 OVERRIDES + 2 DISPATCHES = 4
	if len(overrides) < 4 {
		t.Fatalf("expected at least 4 relations (2 overrides + 2 dispatches), got %d", len(overrides))
	}
	t.Logf("✅ Multiple parent overrides: %d detected", len(overrides))
}

func TestDetectOverrides_NoMethods(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "base_class", Name: "Base", QualifiedName: "Base", Kind: "Class", FilePath: "base.py"},
		{ID: "child_class", Name: "Child", QualifiedName: "Child", Kind: "Class", FilePath: "child.py"},
	})
	resolver := NewResolver(table)

	heritage := []model.RawHeritage{
		{ChildName: "Child", ChildQualified: "Child", ParentName: "Base", Kind: "extends"},
	}

	overrides := resolver.DetectOverrides(heritage)
	if len(overrides) != 0 {
		t.Fatalf("expected 0 overrides for classes with no methods, got %d", len(overrides))
	}
	t.Log("✅ No overrides when classes have no methods")
}

func TestFindMethodInHierarchy(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "base_save", Name: "save", QualifiedName: "models.BaseDao.save", Kind: "Function", FilePath: "base.py"},
		{ID: "base_class", Name: "BaseDao", QualifiedName: "models.BaseDao", Kind: "Class", FilePath: "base.py"},
		{ID: "child_class", Name: "UserDao", QualifiedName: "models.UserDao", Kind: "Class", FilePath: "user.py"},
	})
	resolver := NewResolver(table)

	heritage := []model.RawHeritage{
		{ChildName: "UserDao", ParentName: "BaseDao", Kind: "extends", ChildQualified: "models.UserDao"},
	}

	// UserDao doesn't have save, but BaseDao does
	method := resolver.FindMethodInHierarchy("UserDao", "save", heritage)
	if method == nil {
		t.Fatal("expected to find save in hierarchy")
	}
	if method.ID != "base_save" {
		t.Fatalf("expected base_save, got %s", method.ID)
	}

	// Non-existent method
	method = resolver.FindMethodInHierarchy("UserDao", "nonexistent", heritage)
	if method != nil {
		t.Fatal("expected nil for nonexistent method")
	}

	t.Log("✅ FindMethodInHierarchy works")
}

func TestFindMethodInHierarchy_PrefersClosestAncestor(t *testing.T) {
	table := NewSymbolTable()
	table.AddBatch([]model.Symbol{
		{ID: "base_class", Name: "Base", QualifiedName: "pkg.Base", Kind: "Class", FilePath: "base.py"},
		{ID: "base_run", Name: "run", QualifiedName: "pkg.Base.run", Kind: "Function", FilePath: "base.py"},
		{ID: "mid_class", Name: "Middle", QualifiedName: "pkg.Middle", Kind: "Class", FilePath: "mid.py"},
		{ID: "mid_run", Name: "run", QualifiedName: "pkg.Middle.run", Kind: "Function", FilePath: "mid.py"},
		{ID: "leaf_class", Name: "Leaf", QualifiedName: "pkg.Leaf", Kind: "Class", FilePath: "leaf.py"},
	})
	resolver := NewResolver(table)

	heritage := []model.RawHeritage{
		{ChildName: "Leaf", ParentName: "Middle", Kind: "extends", ChildQualified: "pkg.Leaf"},
		{ChildName: "Middle", ParentName: "Base", Kind: "extends", ChildQualified: "pkg.Middle"},
	}

	// BFS should find Middle.run before Base.run
	method := resolver.FindMethodInHierarchy("Leaf", "run", heritage)
	if method == nil {
		t.Fatal("expected to find run")
	}
	if method.ID != "mid_run" {
		t.Fatalf("expected mid_run (closest ancestor), got %s", method.ID)
	}
	t.Log("✅ FindMethodInHierarchy prefers closest ancestor")
}

func TestFindMethodInHierarchy_DisconnectedClass(t *testing.T) {
	table := NewSymbolTable()
	resolver := NewResolver(table)

	// No heritage at all
	method := resolver.FindMethodInHierarchy("Orphan", "run", nil)
	if method != nil {
		t.Fatal("expected nil for disconnected class")
	}
	t.Log("✅ Disconnected class returns nil")
}
