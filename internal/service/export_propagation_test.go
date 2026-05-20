package service

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestDerivePackage(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"pkg-a/index.ts", "pkg-a"},
		{"pkg-a/models/User.ts", "pkg-a.models.User"},
		{"src/utils/index.tsx", "src.utils"},
		{"index.ts", "index"},
		{"src\\utils\\date.ts", "src.utils.date"},
		{"src/components/Button.jsx", "src.components.Button"},
		{"lib/index.js", "lib"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := derivePackage(tt.input)
			if result != tt.expected {
				t.Errorf("derivePackage(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestBuildImportPathIndex(t *testing.T) {
	t.Run("nil tsconfig returns nil", func(t *testing.T) {
		result := buildImportPathIndex(nil, []string{"src/models/User.ts"})
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})
}

func TestPropagateExports_SingleLayerNamedReexport(t *testing.T) {
	symbolTable := resolver.NewSymbolTable()
	symbolTable.Add(model.Symbol{
		ID: "sym-user", Name: "User", QualifiedName: "src.models.User.User",
		Kind: "class", FilePath: "src/models/User.ts", IsExported: true,
	})

	parseResults := []model.ParseResult{
		{
			FilePath: "src/models/User.ts",
			Symbols: []model.Symbol{
				{ID: "sym-user", Name: "User", IsExported: true},
			},
		},
		{
			FilePath: "src/models/index.ts",
			Imports: []model.RawImport{
				{ModulePath: "./User", SymbolName: "User", LocalName: "User", FilePath: "src/models/index.ts", IsReexport: true},
			},
		},
	}

	indexer := &Indexer{}
	allFiles := []string{"src/models/User.ts", "src/models/index.ts"}
	indexer.propagateExports(parseResults, symbolTable, allFiles, nil)

	// reExportIndex should map "src.models.User" (barrel's package + localName) to sym-user
	targetID, exists := symbolTable.GetReExport("src.models.User")
	if !exists {
		t.Fatal("expected reExportIndex entry for 'src.models.User'")
	}
	if targetID != "sym-user" {
		t.Errorf("expected target ID 'sym-user', got %q", targetID)
	}
}

func TestPropagateExports_RenameReexport(t *testing.T) {
	symbolTable := resolver.NewSymbolTable()
	symbolTable.Add(model.Symbol{
		ID: "sym-internal-logger", Name: "InternalLogger", QualifiedName: "src.internal.Logger.InternalLogger",
		Kind: "class", FilePath: "src/internal/Logger.ts", IsExported: true,
	})

	parseResults := []model.ParseResult{
		{
			FilePath: "src/internal/Logger.ts",
			Symbols: []model.Symbol{
				{ID: "sym-internal-logger", Name: "InternalLogger", IsExported: true},
			},
		},
		{
			FilePath: "src/public-api.ts",
			Imports: []model.RawImport{
				{ModulePath: "./internal/Logger", SymbolName: "InternalLogger", LocalName: "Logger", FilePath: "src/public-api.ts", IsReexport: true},
			},
		},
	}

	indexer := &Indexer{}
	allFiles := []string{"src/internal/Logger.ts", "src/public-api.ts"}
	indexer.propagateExports(parseResults, symbolTable, allFiles, nil)

	targetID, exists := symbolTable.GetReExport("src.public-api.Logger")
	if !exists {
		t.Fatal("expected reExportIndex entry for 'src.public-api.Logger'")
	}
	if targetID != "sym-internal-logger" {
		t.Errorf("expected target ID 'sym-internal-logger', got %q", targetID)
	}
}

func TestPropagateExports_MultiLayerChain_ReverseOrder(t *testing.T) {
	// A re-exports from B, B re-exports from C. C has the definition.
	// Parse order: A → B → C (reverse dependency order)
	symbolTable := resolver.NewSymbolTable()
	symbolTable.Add(model.Symbol{
		ID: "sym-user", Name: "User", QualifiedName: "pkg.models.User.User",
		Kind: "class", FilePath: "pkg/models/User.ts", IsExported: true,
	})

	parseResults := []model.ParseResult{
		{
			FilePath: "pkg/index.ts",
			Imports: []model.RawImport{
				{ModulePath: "./models", SymbolName: "User", LocalName: "User", FilePath: "pkg/index.ts", IsReexport: true},
			},
		},
		{
			FilePath: "pkg/models/index.ts",
			Imports: []model.RawImport{
				{ModulePath: "./User", SymbolName: "User", LocalName: "User", FilePath: "pkg/models/index.ts", IsReexport: true},
			},
		},
		{
			FilePath: "pkg/models/User.ts",
			Symbols: []model.Symbol{
				{ID: "sym-user", Name: "User", IsExported: true},
			},
		},
	}

	indexer := &Indexer{}
	allFiles := []string{"pkg/index.ts", "pkg/models/index.ts", "pkg/models/User.ts"}
	indexer.propagateExports(parseResults, symbolTable, allFiles, nil)

	// pkg/models/index.ts should resolve
	targetID, exists := symbolTable.GetReExport("pkg.models.User")
	if !exists {
		t.Fatal("expected reExportIndex entry for 'pkg.models.User'")
	}
	if targetID != "sym-user" {
		t.Errorf("expected 'sym-user', got %q", targetID)
	}

	// pkg/index.ts should also resolve via waitingFor cascade
	targetID, exists = symbolTable.GetReExport("pkg.User")
	if !exists {
		t.Fatal("expected reExportIndex entry for 'pkg.User'")
	}
	if targetID != "sym-user" {
		t.Errorf("expected 'sym-user', got %q", targetID)
	}
}

func TestPropagateExports_WildcardReexport(t *testing.T) {
	symbolTable := resolver.NewSymbolTable()
	symbolTable.Add(model.Symbol{
		ID: "sym-format-date", Name: "formatDate", QualifiedName: "src.utils.date.formatDate",
		Kind: "function", FilePath: "src/utils/date.ts", IsExported: true,
	})
	symbolTable.Add(model.Symbol{
		ID: "sym-format-string", Name: "formatString", QualifiedName: "src.utils.string.formatString",
		Kind: "function", FilePath: "src/utils/string.ts", IsExported: true,
	})

	parseResults := []model.ParseResult{
		{
			FilePath: "src/utils/date.ts",
			Symbols:  []model.Symbol{{ID: "sym-format-date", Name: "formatDate", IsExported: true}},
		},
		{
			FilePath: "src/utils/string.ts",
			Symbols:  []model.Symbol{{ID: "sym-format-string", Name: "formatString", IsExported: true}},
		},
		{
			FilePath: "src/utils/index.ts",
			Imports: []model.RawImport{
				{ModulePath: "./date", FilePath: "src/utils/index.ts", IsReexport: true, IsWildcard: true},
				{ModulePath: "./string", FilePath: "src/utils/index.ts", IsReexport: true, IsWildcard: true},
			},
		},
	}

	indexer := &Indexer{}
	allFiles := []string{"src/utils/date.ts", "src/utils/string.ts", "src/utils/index.ts"}
	indexer.propagateExports(parseResults, symbolTable, allFiles, nil)

	if id, exists := symbolTable.GetReExport("src.utils.formatDate"); !exists || id != "sym-format-date" {
		t.Errorf("expected 'src.utils.formatDate' → 'sym-format-date', got exists=%v id=%q", exists, id)
	}
	if id, exists := symbolTable.GetReExport("src.utils.formatString"); !exists || id != "sym-format-string" {
		t.Errorf("expected 'src.utils.formatString' → 'sym-format-string', got exists=%v id=%q", exists, id)
	}
}

func TestPropagateExports_LocalPriorityOverNamed(t *testing.T) {
	symbolTable := resolver.NewSymbolTable()
	symbolTable.Add(model.Symbol{
		ID: "sym-local-user", Name: "User", QualifiedName: "barrel.User",
		Kind: "class", FilePath: "barrel/index.ts", IsExported: true,
	})
	symbolTable.Add(model.Symbol{
		ID: "sym-sub-user", Name: "User", QualifiedName: "sub.User",
		Kind: "class", FilePath: "sub/User.ts", IsExported: true,
	})

	parseResults := []model.ParseResult{
		{
			FilePath: "barrel/index.ts",
			Symbols:  []model.Symbol{{ID: "sym-local-user", Name: "User", IsExported: true}},
			Imports: []model.RawImport{
				{ModulePath: "../sub/User", SymbolName: "User", LocalName: "User", FilePath: "barrel/index.ts", IsReexport: true},
			},
		},
		{
			FilePath: "sub/User.ts",
			Symbols:  []model.Symbol{{ID: "sym-sub-user", Name: "User", IsExported: true}},
		},
	}

	indexer := &Indexer{}
	allFiles := []string{"barrel/index.ts", "sub/User.ts"}
	indexer.propagateExports(parseResults, symbolTable, allFiles, nil)

	// Local definition should win (Pass 1 registers first)
	targetID, exists := symbolTable.GetReExport("barrel.User")
	if !exists {
		t.Fatal("expected reExportIndex entry for 'barrel.User'")
	}
	if targetID != "sym-local-user" {
		t.Errorf("expected local 'sym-local-user', got %q", targetID)
	}
}

func TestPropagateExports_CircularReexport(t *testing.T) {
	symbolTable := resolver.NewSymbolTable()
	// No actual definition of "Ghost" anywhere

	parseResults := []model.ParseResult{
		{
			FilePath: "a/index.ts",
			Imports: []model.RawImport{
				{ModulePath: "../b", SymbolName: "Ghost", LocalName: "Ghost", FilePath: "a/index.ts", IsReexport: true},
			},
		},
		{
			FilePath: "b/index.ts",
			Imports: []model.RawImport{
				{ModulePath: "../a", SymbolName: "Ghost", LocalName: "Ghost", FilePath: "b/index.ts", IsReexport: true},
			},
		},
	}

	indexer := &Indexer{}
	allFiles := []string{"a/index.ts", "b/index.ts"}
	indexer.propagateExports(parseResults, symbolTable, allFiles, nil)

	// Neither should be registered (no definition exists)
	if symbolTable.HasReExport("a.Ghost") {
		t.Error("circular re-export should not register 'a.Ghost'")
	}
	if symbolTable.HasReExport("b.Ghost") {
		t.Error("circular re-export should not register 'b.Ghost'")
	}
}

func TestPropagateExports_ImportFileMap(t *testing.T) {
	symbolTable := resolver.NewSymbolTable()
	symbolTable.Add(model.Symbol{
		ID: "sym-user", Name: "User", QualifiedName: "pkg.models.User.User",
		Kind: "class", FilePath: "pkg/models/User.ts", IsExported: true,
	})

	parseResults := []model.ParseResult{
		{
			FilePath: "pkg/models/User.ts",
			Symbols:  []model.Symbol{{ID: "sym-user", Name: "User", IsExported: true}},
		},
		{
			FilePath: "pkg/models/index.ts",
			Imports: []model.RawImport{
				{ModulePath: "./User", SymbolName: "User", LocalName: "User", FilePath: "pkg/models/index.ts", IsReexport: true},
			},
		},
		{
			FilePath: "app.ts",
			Imports: []model.RawImport{
				{ModulePath: "./pkg/models", SymbolName: "User", FilePath: "app.ts"},
			},
		},
	}

	indexer := &Indexer{}
	allFiles := []string{"pkg/models/User.ts", "pkg/models/index.ts", "app.ts"}
	importFileMap := indexer.propagateExports(parseResults, symbolTable, allFiles, nil)

	// app.ts importing User from ./pkg/models should map to pkg/models/index.ts
	key := resolver.ImportFileKey{FilePath: "app.ts", SymbolName: "User"}
	targetFile, exists := importFileMap[key]
	if !exists {
		t.Fatal("expected importFileMap entry for app.ts:User")
	}
	if targetFile != "pkg/models/index.ts" {
		t.Errorf("expected 'pkg/models/index.ts', got %q", targetFile)
	}
}
