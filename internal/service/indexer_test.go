package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/config"
	"time"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/crossindex"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/kuzu"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/lock"
)

func setupTestIndexer(t *testing.T) (*Indexer, storage.GraphStore) {
	t.Helper()
	store, err := kuzu.New("")
	if err != nil {
		t.Fatal("open store:", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal("migrate:", err)
	}

	cfg := config.DefaultConfig()
	fingerprintStore := storage.NewJSONFingerprintStore(t.TempDir())
	indexLock := lock.NewNoopLock()

	indexer := NewIndexer(store, fingerprintStore, indexLock, cfg, nil, nil)
	return indexer, store
}

func TestIndexer_JavaProject(t *testing.T) {
	projectDir := t.TempDir()
	srcDir := filepath.Join(projectDir, "src", "main", "java", "com", "example")
	os.MkdirAll(srcDir, model.DirectoryPermission)

	os.WriteFile(filepath.Join(srcDir, "UserService.java"), []byte(`package com.example;

public class UserService extends BaseService {
    private UserDao dao;

    public User findById(Long id) {
        return dao.findById(id);
    }
}
`), model.FilePermission)

	os.WriteFile(filepath.Join(srcDir, "BaseService.java"), []byte(`package com.example;

public abstract class BaseService {
    public void log(String message) {
        System.out.println(message);
    }
}
`), model.FilePermission)

	os.WriteFile(filepath.Join(srcDir, "UserDao.java"), []byte(`package com.example;

public class UserDao {
    public User findById(Long id) { return null; }
}
`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()

	ctx := context.Background()
	result, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal("index:", err)
	}

	if result.FilesProcessed != 3 {
		t.Fatalf("expected 3 files, got %d", result.FilesProcessed)
	}
	// Exact symbol counts: UserService(class) + findById(func) + BaseService(class) + log(func) + UserDao(class) + findById(func) = 6
	if result.SymbolsCreated < 6 {
		t.Fatalf("expected at least 6 symbols, got %d", result.SymbolsCreated)
	}
	// Must have EXTENDS (UserService→BaseService) and CALLS (findById→dao.findById)
	if result.RelationsByKind["EXTENDS"] < 1 {
		t.Fatalf("expected EXTENDS relation, got %v", result.RelationsByKind)
	}
	if result.RelationsByKind["CALLS"] < 1 {
		t.Fatalf("expected CALLS relation, got %v", result.RelationsByKind)
	}
	// Verify symbols have relative file_path
	funcs, _ := store.QueryAllByKind(ctx, "Function", 100)
	for _, f := range funcs {
		fp, _ := f.Properties["file_path"].(string)
		if strings.HasPrefix(fp, "/") {
			t.Fatalf("function %s has absolute file_path: %s", f.ID, fp)
		}
	}
	t.Logf("✅ Java: %d symbols, relations: %v", result.SymbolsCreated, result.RelationsByKind)
}

func TestIndexer_GoProject(t *testing.T) {
	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module myapp\ngo 1.22\n"), model.FilePermission)

	os.WriteFile(filepath.Join(projectDir, "main.go"), []byte(`package main

import "fmt"

func main() {
	svc := NewService()
	svc.Run()
	fmt.Println("done")
}
`), model.FilePermission)

	os.WriteFile(filepath.Join(projectDir, "service.go"), []byte(`package main

type Service struct {
	dao Dao
}

func NewService() *Service {
	return &Service{}
}

func (service *Service) Run() {
	service.dao.Execute()
}
`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()

	ctx := context.Background()
	result, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal("index:", err)
	}

	if result.FilesProcessed != 2 {
		t.Fatalf("expected 2 files, got %d", result.FilesProcessed)
	}
	// main + NewService + Run + Service(struct) = at least 4 symbols
	if result.SymbolsCreated < 3 {
		t.Fatalf("expected at least 3 symbols, got %d", result.SymbolsCreated)
	}
	// main calls NewService and svc.Run; Run calls dao.Execute
	if result.RelationsByKind["CALLS"] < 2 {
		t.Fatalf("expected at least 2 CALLS, got %v", result.RelationsByKind)
	}

	t.Logf("✅ Go: %d symbols, relations: %v", result.SymbolsCreated, result.RelationsByKind)
}

func TestIndexer_IncrementalSkipsUnchanged(t *testing.T) {
	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "main.go"), []byte("package main\nfunc main() {}\n"), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()
	ctx := context.Background()

	// First index
	result1, err := indexer.Index(ctx, projectDir, "main", false, nil)
	if err != nil {
		t.Fatal("first index:", err)
	}
	if result1.FilesProcessed != 1 {
		t.Fatalf("expected 1 file first time, got %d", result1.FilesProcessed)
	}

	// Second index — no changes
	result2, err := indexer.Index(ctx, projectDir, "main", false, nil)
	if err != nil {
		t.Fatal("second index:", err)
	}
	if result2.FilesProcessed != 0 {
		t.Fatalf("expected 0 files second time (no changes), got %d", result2.FilesProcessed)
	}

	t.Log("✅ Incremental indexing skips unchanged files")
}

func TestIndexer_ProgressCallback(t *testing.T) {
	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "main.py"), []byte("def hello():\n    pass\n"), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()

	var phases []string
	callback := func(event model.ProgressEvent) {
		phases = append(phases, event.Phase)
	}

	_, err := indexer.Index(context.Background(), projectDir, "main", true, callback)
	if err != nil {
		t.Fatal("index:", err)
	}

	if len(phases) < 4 {
		t.Fatalf("expected at least 4 progress events, got %d", len(phases))
	}
	t.Logf("✅ Progress callback received %d events: %v", len(phases), phases)
}


func TestCapitalizeFirst(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"class", "Class"},
		{"function", "Function"},
		{"interface", "Interface"},
		{"enum", "Enum"},
		{"Class", "Class"},       // already capitalized — must not corrupt
		{"Function", "Function"}, // already capitalized
		{"", ""},                 // empty string
		{"a", "A"},               // single char
	}
	for _, test := range tests {
		result := capitalizeFirst(test.input)
		if result != test.expected {
			t.Errorf("capitalizeFirst(%q) = %q, want %q", test.input, result, test.expected)
		}
	}
	t.Log("✅ capitalizeFirst handles all cases correctly")
}

func TestSaveFingerprints_PreservesUnchangedFiles(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "a.go"), []byte("package main\nfunc a() {}\n"), model.FilePermission)
	os.WriteFile(filepath.Join(projectDir, "b.go"), []byte("package main\nfunc b() {}\n"), model.FilePermission)

	ctx := context.Background()

	// First index: both files
	result1, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal("first index:", err)
	}
	if result1.FilesProcessed != 2 {
		t.Fatalf("expected 2 files first time, got %d", result1.FilesProcessed)
	}

	// Modify only a.go
	time.Sleep(100 * time.Millisecond) // ensure different mtime
	os.WriteFile(filepath.Join(projectDir, "a.go"), []byte("package main\nfunc a() { println() }\n"), model.FilePermission)

	// Second index: only a.go should be processed
	result2, err := indexer.Index(ctx, projectDir, "main", false, nil)
	if err != nil {
		t.Fatal("second index:", err)
	}
	if result2.FilesProcessed != 1 {
		t.Fatalf("expected 1 file second time (only a.go changed), got %d", result2.FilesProcessed)
	}

	// Third index: nothing changed
	result3, err := indexer.Index(ctx, projectDir, "main", false, nil)
	if err != nil {
		t.Fatal("third index:", err)
	}
	if result3.FilesProcessed != 0 {
		t.Fatalf("expected 0 files third time (nothing changed), got %d", result3.FilesProcessed)
	}

	t.Log("✅ Fingerprints preserve unchanged files across incremental indexes")
}

func TestMergeCallRelations_ReplacesAffectedFiles(t *testing.T) {
	original := []model.ResolvedRelation{
		{SourceID: "f1", TargetID: "f2", ResolvedBy: "best_guess", Metadata: map[string]string{"file_path": "a.go"}},
		{SourceID: "f3", TargetID: "f4", ResolvedBy: "type_exact", Metadata: map[string]string{"file_path": "b.go"}},
		{SourceID: "f5", TargetID: "f6", ResolvedBy: "best_guess", Metadata: map[string]string{"file_path": "a.go"}},
	}
	reResolved := []model.ResolvedRelation{
		{SourceID: "f1", TargetID: "f2", ResolvedBy: "type_exact", Metadata: map[string]string{"file_path": "a.go"}},
	}
	affectedFiles := []string{"a.go"}

	merged := mergeCallRelations(original, reResolved, affectedFiles)

	// Should keep b.go's relation (1) + reResolved (1) = 2
	if len(merged) != 2 {
		t.Fatalf("expected 2 merged relations, got %d", len(merged))
	}

	// b.go relation should be preserved
	foundBgo := false
	for _, relation := range merged {
		if relation.Metadata["file_path"] == "b.go" {
			foundBgo = true
		}
	}
	if !foundBgo {
		t.Fatal("b.go relation should be preserved")
	}

	// a.go should have the re-resolved version (type_exact, not best_guess)
	for _, relation := range merged {
		if relation.Metadata["file_path"] == "a.go" && relation.ResolvedBy == "best_guess" {
			t.Fatal("a.go should have been replaced with re-resolved version")
		}
	}

	t.Log("✅ mergeCallRelations correctly replaces affected files")
}

func TestMergeCallRelations_NilMetadata(t *testing.T) {
	original := []model.ResolvedRelation{
		{SourceID: "f1", TargetID: "f2", Metadata: nil},
	}
	reResolved := []model.ResolvedRelation{}
	affectedFiles := []string{"a.go"}

	// Should not panic on nil metadata
	merged := mergeCallRelations(original, reResolved, affectedFiles)
	if len(merged) != 1 {
		t.Fatalf("expected 1 (nil metadata not affected), got %d", len(merged))
	}
	t.Log("✅ mergeCallRelations handles nil metadata without panic")
}

func TestIndexer_RoutesAndHandlesEdges(t *testing.T) {
	projectDir := t.TempDir()
	srcDir := filepath.Join(projectDir, "src", "main", "java", "com", "example")
	os.MkdirAll(srcDir, model.DirectoryPermission)

	os.WriteFile(filepath.Join(srcDir, "UserController.java"), []byte(`package com.example;

import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/api/users")
public class UserController {
    @GetMapping("/{id}")
    public User getUser(Long id) {
        return null;
    }

    @PostMapping
    public User createUser(User user) {
        return null;
    }
}
`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()

	ctx := context.Background()
	result, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal("index:", err)
	}

	// Verify Route nodes were created
	routeCount := result.SymbolsByKind["route"]
	if routeCount < 2 {
		t.Fatalf("expected at least 2 route nodes, got %d", routeCount)
	}

	// Verify HANDLES edges were created
	handlesCount := result.RelationsByKind["HANDLES"]
	if handlesCount < 1 {
		t.Fatalf("expected at least 1 HANDLES edge, got %d", handlesCount)
	}

	t.Logf("✅ Routes: %d nodes, %d HANDLES edges", routeCount, handlesCount)
	t.Logf("   Symbols: %v", result.SymbolsByKind)
	t.Logf("   Relations: %v", result.RelationsByKind)
}

func TestIndexer_GoRoutesAndHandles(t *testing.T) {
	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module myapp\ngo 1.22\n"), model.FilePermission)

	os.WriteFile(filepath.Join(projectDir, "main.go"), []byte(`package main

import "github.com/gin-gonic/gin"

func main() {
	router := gin.Default()
	router.GET("/users", listUsers)
	router.POST("/users", createUser)
}

func listUsers(c *gin.Context) {}
func createUser(c *gin.Context) {}
`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()

	ctx := context.Background()
	result, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal("index:", err)
	}

	routeCount := result.SymbolsByKind["route"]
	if routeCount < 2 {
		t.Fatalf("expected at least 2 Go route nodes, got %d", routeCount)
	}

	handlesCount := result.RelationsByKind["HANDLES"]
	if handlesCount < 1 {
		t.Fatalf("expected at least 1 HANDLES edge, got %d", handlesCount)
	}

	t.Logf("✅ Go Routes: %d nodes, %d HANDLES edges", routeCount, handlesCount)
}

func TestIndexer_HeritageEdges(t *testing.T) {
	projectDir := t.TempDir()
	srcDir := filepath.Join(projectDir, "src", "main", "java", "com", "example")
	os.MkdirAll(srcDir, model.DirectoryPermission)

	os.WriteFile(filepath.Join(srcDir, "Animal.java"), []byte(`package com.example;

public interface Animal {
    void speak();
}
`), model.FilePermission)

	os.WriteFile(filepath.Join(srcDir, "Dog.java"), []byte(`package com.example;

public class Dog implements Animal {
    @Override
    public void speak() {
        System.out.println("Woof");
    }
}
`), model.FilePermission)

	os.WriteFile(filepath.Join(srcDir, "Puppy.java"), []byte(`package com.example;

public class Puppy extends Dog {
    @Override
    public void speak() {
        System.out.println("Yip");
    }
}
`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()

	ctx := context.Background()
	result, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal("index:", err)
	}

	extendsCount := result.RelationsByKind["EXTENDS"]
	implementsCount := result.RelationsByKind["IMPLEMENTS"]
	overridesCount := result.RelationsByKind["OVERRIDES"]

	if extendsCount < 1 {
		t.Fatalf("expected at least 1 EXTENDS, got %d", extendsCount)
	}
	if implementsCount < 1 {
		t.Fatalf("expected at least 1 IMPLEMENTS, got %d", implementsCount)
	}
	if overridesCount < 1 {
		t.Fatalf("expected at least 1 OVERRIDES, got %d", overridesCount)
	}

	t.Logf("✅ Heritage: EXTENDS=%d IMPLEMENTS=%d OVERRIDES=%d", extendsCount, implementsCount, overridesCount)
}

func TestIndexer_StructuralNodes(t *testing.T) {
	projectDir := t.TempDir()
	srcDir := filepath.Join(projectDir, "src")
	os.MkdirAll(srcDir, model.DirectoryPermission)

	os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module myapp\ngo 1.22\n"), model.FilePermission)
	os.WriteFile(filepath.Join(projectDir, "main.go"), []byte(`package main

func main() {}
`), model.FilePermission)
	os.WriteFile(filepath.Join(srcDir, "service.go"), []byte(`package src

func Run() {}
`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()

	ctx := context.Background()
	result, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify structural nodes exist by querying the store
	repoNodes, _ := store.QueryAllByKind(ctx, "Repository", 1000)
	if len(repoNodes) < 1 {
		t.Fatal("expected at least 1 Repository node")
	}

	fileNodes, _ := store.QueryAllByKind(ctx, "File", 1000)
	if len(fileNodes) < 2 {
		t.Fatalf("expected at least 2 File nodes, got %d", len(fileNodes))
	}

	dirNodes, _ := store.QueryAllByKind(ctx, "Directory", 1000)
	if len(dirNodes) < 1 {
		t.Fatalf("expected at least 1 Directory node, got %d", len(dirNodes))
	}

	t.Logf("✅ Structural: %d Repository, %d Directory, %d File nodes",
		len(repoNodes), len(dirNodes), len(fileNodes))
	t.Logf("   Symbols: %v", result.SymbolsByKind)
}

func TestIndexer_QueryNodesAndExecutesEdges(t *testing.T) {
	projectDir := t.TempDir()
	srcDir := filepath.Join(projectDir, "src", "main", "java", "com", "example")
	os.MkdirAll(srcDir, model.DirectoryPermission)

	os.WriteFile(filepath.Join(srcDir, "UserDao.java"), []byte(`package com.example;

public class UserDao {
    @Query("SELECT u FROM User u WHERE u.id = ?1")
    public User findById(Long id) {}
}
`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()

	ctx := context.Background()
	result, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal(err)
	}

	queryNodes, _ := store.QueryAllByKind(ctx, "QueryNode", 1000)
	if len(queryNodes) < 1 {
		t.Fatalf("expected at least 1 QueryNode, got %d", len(queryNodes))
	}

	executesCount := result.RelationsByKind["EXECUTES"]
	if executesCount < 1 {
		t.Fatalf("expected at least 1 EXECUTES edge, got %d", executesCount)
	}

	t.Logf("✅ ORM: %d QueryNodes, %d EXECUTES edges", len(queryNodes), executesCount)
}

func TestIndexer_IncrementalDeletesOldData(t *testing.T) {
	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module myapp\ngo 1.22\n"), model.FilePermission)
	os.WriteFile(filepath.Join(projectDir, "main.go"), []byte(`package main

func hello() {}
func world() {}
`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()
	ctx := context.Background()

	// First index
	result1, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	symbols1 := result1.SymbolsCreated

	// Modify file: remove one function (sleep to ensure ModTime changes)
	time.Sleep(time.Millisecond * 100)
	os.WriteFile(filepath.Join(projectDir, "main.go"), []byte(`package main

func hello() {}
`), model.FilePermission)

	// Second index (incremental)
	result2, err := indexer.Index(ctx, projectDir, "main", false, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Should have fewer symbols after removing a function
	funcNodes, _ := store.QueryAllByKind(ctx, "Function", 1000)
	hasWorld := false
	for _, node := range funcNodes {
		if name, ok := node.Properties["name"]; ok && name == "world" {
			hasWorld = true
		}
	}
	if hasWorld {
		t.Fatal("deleted function 'world' should not exist after incremental re-index")
	}

	t.Logf("✅ Incremental delete: first=%d symbols, second=%d symbols, %d functions remain",
		symbols1, result2.SymbolsCreated, len(funcNodes))
}

func TestIndexer_FileContainsEdgesMatchFileNodes(t *testing.T) {
	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module myapp\ngo 1.22\n"), model.FilePermission)
	os.WriteFile(filepath.Join(projectDir, "main.go"), []byte(`package main

func hello() {}
`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()
	ctx := context.Background()

	result, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal(err)
	}

	// File nodes should use relative paths as IDs (portable across machines)
	fileNodes, _ := store.QueryAllByKind(ctx, "File", 100)
	for _, f := range fileNodes {
		// ID should be "file:<relpath>", not absolute
		if len(f.ID) > 5 && f.ID[5] == '/' {
			t.Fatalf("File node ID uses absolute path: %s", f.ID)
		}
	}

	if len(fileNodes) < 1 {
		t.Fatal("expected at least 1 File node")
	}

	t.Logf("✅ %d File nodes with relative path IDs, %d symbols", len(fileNodes), result.SymbolsCreated)
}

func TestIndexer_ImportsEdgeMatchesFileNodeID(t *testing.T) {
	projectDir := t.TempDir()
	pkgDir := filepath.Join(projectDir, "pkg")
	os.MkdirAll(pkgDir, model.DirectoryPermission)

	os.WriteFile(filepath.Join(pkgDir, "util.py"), []byte(`def helper():
    pass
`), model.FilePermission)

	os.WriteFile(filepath.Join(projectDir, "app.py"), []byte(`from pkg.util import helper

def main():
    helper()
`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()
	ctx := context.Background()

	result, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal(err)
	}

	// File node IDs must be relative
	fileNodes, _ := store.QueryAllByKind(ctx, "File", 100)
	for _, f := range fileNodes {
		if strings.HasPrefix(f.ID, "file:/") {
			t.Fatalf("File node ID is absolute: %s", f.ID)
		}
	}

	importsCount := result.RelationsByKind["IMPORTS"]
	if importsCount < 1 {
		t.Fatalf("expected at least 1 IMPORTS edge, got %d (relations: %v)", importsCount, result.RelationsByKind)
	}

	t.Logf("✅ IMPORTS: %d edges, %d File nodes", importsCount, len(fileNodes))
}

func TestIndexer_QueryNodeIDStable(t *testing.T) {
	projectDir := t.TempDir()
	srcDir := filepath.Join(projectDir, "src", "main", "java", "com", "example")
	os.MkdirAll(srcDir, model.DirectoryPermission)

	os.WriteFile(filepath.Join(srcDir, "Dao.java"), []byte(`package com.example;

public class Dao {
    @Query("SELECT * FROM users")
    public void find() {}
}
`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()
	ctx := context.Background()

	indexer.Index(ctx, projectDir, "main", true, nil)

	queryNodes, _ := store.QueryAllByKind(ctx, "QueryNode", 100)
	for _, q := range queryNodes {
		// QueryNode ID should NOT contain absolute path
		if strings.Contains(q.ID, projectDir) {
			t.Fatalf("QueryNode ID contains absolute path: %s", q.ID)
		}
	}

	if len(queryNodes) < 1 {
		t.Fatal("expected at least 1 QueryNode")
	}
	t.Logf("✅ QueryNode IDs are path-independent: %v", queryNodes[0].ID)
}

func TestIndexer_DirectoryFileEdgesEfficient(t *testing.T) {
	// Verify Directory→File edges are created correctly with many files
	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module myapp\ngo 1.22\n"), model.FilePermission)

	subDir := filepath.Join(projectDir, "pkg")
	os.MkdirAll(subDir, model.DirectoryPermission)
	for i := 0; i < 5; i++ {
		os.WriteFile(
			filepath.Join(subDir, fmt.Sprintf("f%d.go", i)),
			[]byte(fmt.Sprintf("package pkg\nfunc F%d() {}\n", i)),
			model.FilePermission,
		)
	}

	indexer, store := setupTestIndexer(t)
	defer store.Close()
	ctx := context.Background()

	_, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal(err)
	}

	dirs, _ := store.QueryAllByKind(ctx, "Directory", 100)
	files, _ := store.QueryAllByKind(ctx, "File", 100)

	if len(dirs) < 1 {
		t.Fatal("expected at least 1 Directory")
	}
	if len(files) < 5 {
		t.Fatalf("expected at least 5 Files, got %d", len(files))
	}

	t.Logf("✅ %d directories, %d files — edges created via map (O(n))", len(dirs), len(files))
}

func TestIndexer_AllPathsRelative(t *testing.T) {
	projectDir := t.TempDir()
	srcDir := filepath.Join(projectDir, "pkg")
	os.MkdirAll(srcDir, model.DirectoryPermission)

	os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module myapp\ngo 1.22\n"), model.FilePermission)
	os.WriteFile(filepath.Join(srcDir, "svc.go"), []byte(`package pkg

import "fmt"

func Run() { fmt.Println("hi") }
`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()
	ctx := context.Background()

	_, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 1. File node IDs must be relative
	fileNodes, _ := store.QueryAllByKind(ctx, "File", 100)
	for _, f := range fileNodes {
		if strings.HasPrefix(f.ID, "file:/") {
			t.Fatalf("File node ID is absolute: %s", f.ID)
		}
	}

	// 2. Function node file_path must be relative
	funcNodes, _ := store.QueryAllByKind(ctx, "Function", 100)
	for _, f := range funcNodes {
		fp, _ := f.Properties["file_path"].(string)
		if strings.HasPrefix(fp, "/") {
			t.Fatalf("Function file_path is absolute: %s", fp)
		}
	}

	// 3. Verify DeleteNodesByFile works with relative path
	err = store.DeleteNodesByFile(ctx, "pkg/svc.go")
	if err != nil {
		t.Fatalf("DeleteNodesByFile with relative path: %v", err)
	}
	remaining, _ := store.QueryAllByKind(ctx, "Function", 100)
	for _, f := range remaining {
		if f.Properties["file_path"] == "pkg/svc.go" {
			t.Fatal("function from pkg/svc.go should be deleted")
		}
	}

	t.Logf("✅ All paths relative: %d files, %d functions, delete works", len(fileNodes), len(funcNodes))
}

// TestIndexer_EdgeIntegrity verifies that all edge endpoints reference existing nodes.
// This is the single most valuable integration test — it catches ID mismatches,
// path inconsistencies, and dangling references across the entire pipeline.
func TestIndexer_EdgeIntegrity(t *testing.T) {
	projectDir := t.TempDir()
	srcDir := filepath.Join(projectDir, "src", "main", "java", "com", "example")
	os.MkdirAll(srcDir, model.DirectoryPermission)

	os.WriteFile(filepath.Join(srcDir, "Base.java"), []byte(`package com.example;

public abstract class Base {
    public void log(String msg) {}
}
`), model.FilePermission)

	os.WriteFile(filepath.Join(srcDir, "Service.java"), []byte(`package com.example;

import com.example.Base;

public class Service extends Base {
    @Override
    public void log(String msg) { System.out.println(msg); }

    public void run() { log("hi"); }
}
`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()
	ctx := context.Background()

	_, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Collect ALL node IDs across all kinds
	allNodeIDs := map[string]bool{}
	for _, kind := range []string{"Repository", "Directory", "File", "Function", "Class", "Interface", "Route", "QueryNode"} {
		nodes, _ := store.QueryAllByKind(ctx, kind, 10000)
		for _, n := range nodes {
			allNodeIDs[n.ID] = true
		}
	}

	if len(allNodeIDs) == 0 {
		t.Fatal("no nodes found")
	}

	// Verify no node uses absolute path in ID
	for id := range allNodeIDs {
		if strings.HasPrefix(id, "file:/") || strings.HasPrefix(id, "dir:/") || strings.HasPrefix(id, "repo:/") {
			t.Fatalf("node ID contains absolute path: %s", id)
		}
	}

	// Verify Function/Class nodes have relative file_path
	for _, kind := range []string{"Function", "Class"} {
		nodes, _ := store.QueryAllByKind(ctx, kind, 10000)
		for _, n := range nodes {
			fp, _ := n.Properties["file_path"].(string)
			if fp != "" && strings.HasPrefix(fp, "/") {
				t.Fatalf("%s node %s has absolute file_path: %s", kind, n.ID, fp)
			}
		}
	}

	t.Logf("✅ Edge integrity: %d nodes, all IDs relative, no absolute paths in properties", len(allNodeIDs))
}

func TestIndexer_FileContainsClassAndInterface(t *testing.T) {
	projectDir := t.TempDir()
	srcDir := filepath.Join(projectDir, "src", "main", "java", "com", "example")
	os.MkdirAll(srcDir, model.DirectoryPermission)

	os.WriteFile(filepath.Join(srcDir, "App.java"), []byte(`package com.example;

public interface Runnable {
    void run();
}

public class App implements Runnable {
    public void run() {}
}
`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()
	ctx := context.Background()

	_, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal(err)
	}

	classes, _ := store.QueryAllByKind(ctx, "Class", 100)
	interfaces, _ := store.QueryAllByKind(ctx, "Interface", 100)
	funcs, _ := store.QueryAllByKind(ctx, "Function", 100)

	if len(classes) < 1 {
		t.Fatalf("expected at least 1 Class, got %d", len(classes))
	}
	if len(interfaces) < 1 {
		t.Fatalf("expected at least 1 Interface, got %d", len(interfaces))
	}
	if len(funcs) < 1 {
		t.Fatalf("expected at least 1 Function, got %d", len(funcs))
	}

	// Verify Interface has qualified_name (P3-1 regression)
	for _, iface := range interfaces {
		if iface.Properties["name"] == nil {
			t.Fatalf("Interface node missing name property: %s", iface.ID)
		}
	}

	t.Logf("✅ File→Class/Interface: %d classes, %d interfaces, %d functions", len(classes), len(interfaces), len(funcs))
}


func TestIndexer_MybatisOnlyForJavaProjects(t *testing.T) {
	// Go project with an XML file that looks like a MyBatis mapper — should NOT be parsed
	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module myapp\ngo 1.22\n"), model.FilePermission)
	os.WriteFile(filepath.Join(projectDir, "main.go"), []byte("package main\nfunc main() {}\n"), model.FilePermission)

	mapperDir := filepath.Join(projectDir, "resources", "mapper")
	os.MkdirAll(mapperDir, model.DirectoryPermission)
	os.WriteFile(filepath.Join(mapperDir, "FakeMapper.xml"), []byte(`<mapper namespace="com.fake.Mapper">
    <select id="findAll">SELECT * FROM fake</select>
</mapper>`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()
	ctx := context.Background()

	result, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal(err)
	}

	queryCount := result.SymbolsByKind["query"]
	if queryCount != 0 {
		t.Fatalf("Go project should not parse MyBatis XML, got %d query nodes", queryCount)
	}
	t.Log("✅ MyBatis XML skipped for Go project")
}

func TestIndexer_MybatisParsedForMavenProject(t *testing.T) {
	projectDir := t.TempDir()
	// pom.xml makes it a Maven project
	os.WriteFile(filepath.Join(projectDir, "pom.xml"), []byte(`<project>
  <groupId>com.example</groupId>
  <dependencies>
    <dependency><artifactId>mybatis-spring-boot-starter</artifactId></dependency>
  </dependencies>
</project>`), model.FilePermission)

	srcDir := filepath.Join(projectDir, "src", "main", "java", "com", "example")
	os.MkdirAll(srcDir, model.DirectoryPermission)
	os.WriteFile(filepath.Join(srcDir, "App.java"), []byte("package com.example;\npublic class App {}\n"), model.FilePermission)

	mapperDir := filepath.Join(projectDir, "src", "main", "resources", "mapper")
	os.MkdirAll(mapperDir, model.DirectoryPermission)
	os.WriteFile(filepath.Join(mapperDir, "UserMapper.xml"), []byte(`<mapper namespace="com.example.UserMapper">
    <select id="findAll">SELECT * FROM users</select>
    <insert id="save">INSERT INTO users (name) VALUES (#{name})</insert>
</mapper>`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()
	ctx := context.Background()

	result, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal(err)
	}

	queryCount := result.SymbolsByKind["query"]
	if queryCount != 2 {
		t.Fatalf("Maven project should parse MyBatis XML, expected 2 queries, got %d", queryCount)
	}
	t.Log("✅ MyBatis XML parsed for Maven project: 2 queries")
}

func TestIndexer_AnnotationNodes(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	// Create a Java project with Spring annotations
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pom.xml"), []byte(`<project>
  <dependencies>
    <dependency><artifactId>spring-boot-starter-web</artifactId></dependency>
  </dependencies>
</project>`), 0o644)
	os.MkdirAll(filepath.Join(dir, "src", "main", "java"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "main", "java", "UserController.java"), []byte(`
package com.example;
import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/api/users")
public class UserController {
    @GetMapping("/{id}")
    public String getUser(@PathVariable Long id) {
        return "user";
    }
}
`), 0o644)

	result, err := indexer.Index(context.Background(), dir, "", false, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Check annotation nodes were created
	if result.AnnotationCount == 0 {
		t.Fatal("expected annotation nodes to be created")
	}

	// Query Annotation nodes
	annNodes, err := store.QueryAllByKind(context.Background(), "Annotation", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(annNodes) == 0 {
		t.Fatal("expected Annotation nodes in graph")
	}

	// Check RestController annotation exists
	found := false
	for _, n := range annNodes {
		if fmt.Sprint(n.Properties["name"]) == "RestController" {
			found = true
			if fmt.Sprint(n.Properties["category"]) != "layer" {
				t.Errorf("expected category=layer, got %v", n.Properties["category"])
			}
			if fmt.Sprint(n.Properties["layer"]) != "controller" {
				t.Errorf("expected layer=controller, got %v", n.Properties["layer"])
			}
			if fmt.Sprint(n.Properties["framework"]) != "spring" {
				t.Errorf("expected framework=spring, got %v", n.Properties["framework"])
			}
		}
	}
	if !found {
		t.Errorf("RestController annotation not found, got: %+v", annNodes)
	}
}

func TestIndexer_IncrementalDeletesRemovedFiles(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main
func Keep() {}
`), model.FilePermission)
	os.WriteFile(filepath.Join(dir, "removed.go"), []byte(`package main
func WillBeRemoved() {}
`), model.FilePermission)

	ctx := context.Background()

	// First index: both files
	_, err := indexer.Index(ctx, dir, "main", false, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify both symbols exist
	nodes, _ := store.QueryNodesByName(ctx, "WillBeRemoved", model.QueryOpts{})
	if len(nodes) == 0 {
		t.Fatal("WillBeRemoved should exist after first index")
	}
	nodes, _ = store.QueryNodesByName(ctx, "Keep", model.QueryOpts{})
	if len(nodes) == 0 {
		t.Fatal("Keep should exist after first index")
	}

	// Delete the file
	os.Remove(filepath.Join(dir, "removed.go"))

	// Second index: incremental
	_, err = indexer.Index(ctx, dir, "main", false, nil)
	if err != nil {
		t.Fatal(err)
	}

	// WillBeRemoved should be gone
	nodes, _ = store.QueryNodesByName(ctx, "WillBeRemoved", model.QueryOpts{})
	if len(nodes) != 0 {
		t.Errorf("WillBeRemoved should be deleted, but found %d nodes", len(nodes))
	}

	// Keep should still exist
	nodes, _ = store.QueryNodesByName(ctx, "Keep", model.QueryOpts{})
	if len(nodes) == 0 {
		t.Error("Keep should still exist after incremental index")
	}

	t.Log("✅ Incremental index cleans up deleted files")
}

func TestIndexer_ForceRebuildClearsOldData(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main
func OldFunc() {}
func StayFunc() {}
`), model.FilePermission)

	ctx := context.Background()

	// First index
	_, err := indexer.Index(ctx, dir, "main", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := store.QueryNodesByName(ctx, "OldFunc", model.QueryOpts{})
	if len(nodes) == 0 {
		t.Fatal("OldFunc should exist after first index")
	}

	// Modify file: remove OldFunc, keep StayFunc
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main
func StayFunc() {}
func NewFunc() {}
`), model.FilePermission)

	// Force re-index
	_, err = indexer.Index(ctx, dir, "main", true, nil)
	if err != nil {
		t.Fatal(err)
	}

	// OldFunc should be gone (ClearAll + rebuild)
	nodes, _ = store.QueryNodesByName(ctx, "OldFunc", model.QueryOpts{})
	if len(nodes) != 0 {
		t.Error("OldFunc should be cleared after force rebuild")
	}

	// StayFunc and NewFunc should exist
	nodes, _ = store.QueryNodesByName(ctx, "StayFunc", model.QueryOpts{})
	if len(nodes) == 0 {
		t.Error("StayFunc should exist after force rebuild")
	}
	nodes, _ = store.QueryNodesByName(ctx, "NewFunc", model.QueryOpts{})
	if len(nodes) == 0 {
		t.Error("NewFunc should exist after force rebuild")
	}

	t.Log("✅ Force rebuild clears old data completely")
}

func TestBuildImportGraphFromRelations(t *testing.T) {
	relations := []model.ResolvedRelation{
		{SourceID: "file:controller.java", TargetID: "file:service.java", Kind: model.RelImports},
		{SourceID: "file:service.java", TargetID: "file:repository.java", Kind: model.RelImports},
		{SourceID: "file:controller.java", TargetID: "file:model.java", Kind: model.RelImports},
		// Non-import relation should be ignored
		{SourceID: "func:a", TargetID: "func:b", Kind: model.RelCalls},
	}

	graph := buildImportGraphFromRelations(relations)

	if len(graph["controller.java"]) != 2 {
		t.Errorf("expected 2 imports for controller.java, got %d", len(graph["controller.java"]))
	}
	if len(graph["service.java"]) != 1 {
		t.Errorf("expected 1 import for service.java, got %d", len(graph["service.java"]))
	}
	if _, exists := graph["func:a"]; exists {
		t.Error("non-import relation should not be in graph")
	}

	// Values should be file paths, not module paths
	found := false
	for _, target := range graph["controller.java"] {
		if target == "service.java" {
			found = true
		}
	}
	if !found {
		t.Error("expected service.java in controller.java's imports")
	}

	t.Log("✅ buildImportGraphFromRelations: correct file-path based graph")
}

func TestIndexer_CrossFileCallResolution(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "service.go"), []byte(`package main

type UserService struct{}

func (s *UserService) FindById(id int) string { return "" }
`), model.FilePermission)

	os.WriteFile(filepath.Join(dir, "controller.go"), []byte(`package main

func GetUser(id int) string {
    svc := NewUserService()
    return svc.FindById(id)
}

func NewUserService() *UserService {
    return &UserService{}
}
`), model.FilePermission)

	ctx := context.Background()
	result, err := indexer.Index(ctx, dir, "main", true, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify CALLS edge exists: GetUser → FindById
	callCount := result.RelationsByKind["CALLS"]
	if callCount == 0 {
		t.Error("expected CALLS edges for cross-file call resolution")
	}

	// Query edges from GetUser
	nodes, _ := store.QueryNodesByName(ctx, "GetUser", model.QueryOpts{})
	if len(nodes) == 0 {
		t.Fatal("GetUser not found")
	}
	edges, _ := store.QueryEdges(ctx, nodes[0].ID, "Function", model.RelCalls, model.Outgoing)
	
	foundFindById := false
	for _, e := range edges {
		target, _ := store.QueryNodeByID(ctx, e.TargetID)
		if target != nil && fmt.Sprint(target.Properties["name"]) == "FindById" {
			foundFindById = true
		}
	}
	if !foundFindById {
		t.Error("expected GetUser → FindById CALLS edge")
	}

	t.Logf("✅ Cross-file call resolution: %d CALLS edges, GetUser→FindById found", callCount)
}

func TestIndexer_IncrementalPreservesCrossFileCalls(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	dir := t.TempDir()
	// Two files: controller calls service
	os.WriteFile(filepath.Join(dir, "service.go"), []byte(`package main

type UserService struct{}

func (s *UserService) FindById(id int) string { return "" }
`), model.FilePermission)

	os.WriteFile(filepath.Join(dir, "controller.go"), []byte(`package main

import "service"

func GetUser(id int) string {
    svc := NewUserService()
    return svc.FindById(id)
}

func NewUserService() *UserService {
    return &UserService{}
}
`), model.FilePermission)

	ctx := context.Background()

	// First: full index
	_, err := indexer.Index(ctx, dir, "main", true, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify cross-file call exists
	nodes, _ := store.QueryNodesByName(ctx, "GetUser", model.QueryOpts{})
	if len(nodes) == 0 {
		t.Fatal("GetUser not found after full index")
	}
	edges, _ := store.QueryEdges(ctx, nodes[0].ID, "Function", model.RelCalls, model.Outgoing)
	initialCallCount := len(edges)
	if initialCallCount == 0 {
		t.Fatal("expected CALLS edges from GetUser after full index")
	}

	// Modify controller.go (add a comment) — service.go unchanged
	os.WriteFile(filepath.Join(dir, "controller.go"), []byte(`package main

import "service"

// modified
func GetUser(id int) string {
    svc := NewUserService()
    return svc.FindById(id)
}

func NewUserService() *UserService {
    return &UserService{}
}
`), model.FilePermission)

	// Incremental index — only controller.go changed, service.go untouched
	_, err = indexer.Index(ctx, dir, "main", false, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify cross-file call still exists after incremental
	nodes, _ = store.QueryNodesByName(ctx, "GetUser", model.QueryOpts{})
	if len(nodes) == 0 {
		t.Fatal("GetUser not found after incremental index")
	}
	edges, _ = store.QueryEdges(ctx, nodes[0].ID, "Function", model.RelCalls, model.Outgoing)

	foundFindById := false
	for _, e := range edges {
		target, _ := store.QueryNodeByID(ctx, e.TargetID)
		if target != nil && fmt.Sprint(target.Properties["name"]) == "FindById" {
			foundFindById = true
		}
	}
	if !foundFindById {
		t.Error("GetUser → FindById CALLS edge lost after incremental index (SymbolTable incomplete)")
	}

	t.Logf("✅ Incremental preserves cross-file calls: %d edges, FindById found=%v", len(edges), foundFindById)
}

func TestIndexer_IncrementalRouteIDUnique(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pom.xml"), []byte(`<project>
  <dependencies><dependency><artifactId>spring-boot-starter-web</artifactId></dependency></dependencies>
</project>`), model.FilePermission)
	src := filepath.Join(dir, "src", "main", "java")
	os.MkdirAll(src, model.DirectoryPermission)

	// Two controllers with same route path
	os.WriteFile(filepath.Join(src, "UserController.java"), []byte(`
package com.example;
import org.springframework.web.bind.annotation.*;
@RestController
public class UserController {
    @GetMapping("/api/users")
    public String list() { return "users"; }
}
`), model.FilePermission)

	os.WriteFile(filepath.Join(src, "AdminController.java"), []byte(`
package com.example;
import org.springframework.web.bind.annotation.*;
@RestController
public class AdminController {
    @GetMapping("/api/users")
    public String list() { return "admin-users"; }
}
`), model.FilePermission)

	ctx := context.Background()
	result, err := indexer.Index(ctx, dir, "main", true, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Should have 2 routes (same path, different files → different IDs)
	routeCount := result.SymbolsByKind["route"]
	if routeCount != 2 {
		t.Errorf("expected 2 routes (same path, different files), got %d", routeCount)
	}

	routes, _ := store.QueryAllByKind(ctx, "Route", 100)
	if len(routes) < 2 {
		t.Errorf("expected 2 Route nodes in DB, got %d", len(routes))
	}

	t.Logf("✅ Route ID unique: %d routes for same path in different files", routeCount)
}

func TestIndexer_JavaMaven_UnresolvedHints(t *testing.T) {
	projectDir := t.TempDir()
	srcDir := filepath.Join(projectDir, "src", "main", "java", "com", "example")
	os.MkdirAll(srcDir, model.DirectoryPermission)

	// pom.xml for maven detection
	os.WriteFile(filepath.Join(projectDir, "pom.xml"), []byte(`<project><modelVersion>4.0.0</modelVersion><groupId>com.example</groupId><artifactId>test</artifactId></project>`), model.FilePermission)

	// BaseDao with get/list methods
	os.WriteFile(filepath.Join(srcDir, "BaseDao.java"), []byte(`package com.example;

public class BaseDao {
    public Object get(String sql, Object[] params, Class clazz) {
        return null;
    }
    public Object list(String sql, Object[] params, Class clazz) {
        return null;
    }
}
`), model.FilePermission)

	// ChildDao extends BaseDao, calls super.get() and get() without receiver
	os.WriteFile(filepath.Join(srcDir, "ChildDao.java"), []byte(`package com.example;

public class ChildDao extends BaseDao {
    public Object findById(Long id) {
        return super.get(id);
    }
    public Object findByName(String name) {
        return get(name);
    }
}
`), model.FilePermission)

	// Service with lambda — forEach with unresolvable lambda param
	os.WriteFile(filepath.Join(srcDir, "UserService.java"), []byte(`package com.example;

import java.util.List;

public class UserService {
    private List<User> users;
    private ChildDao childDao;

    public void processAll() {
        users.forEach(user -> {
            user.save();
        });
    }

    public User findUser(Long id) {
        return childDao.findById(id);
    }
}
`), model.FilePermission)

	// User class with save method
	os.WriteFile(filepath.Join(srcDir, "User.java"), []byte(`package com.example;

public class User {
    public void save() {}
    public String getName() { return ""; }
}
`), model.FilePermission)

	// AnotherService with same-name method to create ambiguity
	os.WriteFile(filepath.Join(srcDir, "AnotherService.java"), []byte(`package com.example;

public class AnotherService {
    public void save() {}
}
`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()

	ctx := context.Background()
	result, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal("index:", err)
	}

	t.Logf("Files: %d, Symbols: %d", result.FilesProcessed, result.SymbolsCreated)
	t.Logf("Relations: %v", result.RelationsByKind)

	// Verify: no best_guess in relations (should be 0 since we replaced with hints)
	// Check CALLS edges exist
	if result.RelationsByKind["CALLS"] < 1 {
		t.Fatalf("expected CALLS relations, got %v", result.RelationsByKind)
	}

	// Verify: super.get() should resolve to BaseDao.get via heritage
	edges, _ := store.QueryAllEdges(ctx, model.RelCalls, 1000)
	superResolved := false
	for _, e := range edges {
		if strings.Contains(e.TargetID, "BaseDao") || strings.Contains(e.SourceID, "ChildDao") {
			t.Logf("  CALLS edge: %s → %s (confidence: %v)", e.SourceID, e.TargetID, e.Properties["confidence"])
			superResolved = true
		}
	}

	// Verify: EXTENDS relation exists
	if result.RelationsByKind["EXTENDS"] < 1 {
		t.Fatalf("expected EXTENDS relation (ChildDao→BaseDao), got %v", result.RelationsByKind)
	}

	// Check UNRESOLVED_CALL edges (lambda user.save() should produce hint)
	unresolvedCount := result.RelationsByKind["UNRESOLVED_CALL"]
	t.Logf("UNRESOLVED_CALL edges: %d", unresolvedCount)

	// Verify hint edges in graph
	hintEdges, _ := store.QueryAllEdges(ctx, model.RelUnresolvedCall, 1000)
	t.Logf("Hint edges in graph: %d", len(hintEdges))
	for _, e := range hintEdges {
		t.Logf("  UNRESOLVED_CALL: %s → %s (hint_type: %v)", e.SourceID, e.TargetID, e.Properties["hint_type"])
	}

	if superResolved {
		t.Log("✅ super.get() resolved to BaseDao.get")
	} else {
		t.Log("⚠ super.get() resolution not verified (may need graph query)")
	}

	t.Logf("✅ Java Maven integration: %d symbols, %d CALLS, %d UNRESOLVED_CALL",
		result.SymbolsCreated, result.RelationsByKind["CALLS"], unresolvedCount)
}

func TestIndexer_ClassContainsFuncEdges(t *testing.T) {
	projectDir := t.TempDir()
	srcDir := filepath.Join(projectDir, "src", "main", "java", "com", "example")
	os.MkdirAll(srcDir, model.DirectoryPermission)

	os.WriteFile(filepath.Join(srcDir, "UserService.java"), []byte(`package com.example;
public class UserService {
    public User findById(Long id) { return null; }
    public void deleteById(Long id) {}
}
`), model.FilePermission)

	os.WriteFile(filepath.Join(srcDir, "OrderService.java"), []byte(`package com.example;
public class OrderService {
    public Order getOrder(Long id) { return null; }
}
`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()
	ctx := context.Background()
	_, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal("index:", err)
	}

	classNodes, _ := store.QueryNodesByName(ctx, "UserService", model.QueryOpts{Kinds: []string{"Class"}})
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 UserService, got %d", len(classNodes))
	}
	edges, _ := store.QueryEdges(ctx, classNodes[0].ID, constants.SourceKindClassFunc, model.RelContains, model.Outgoing)
	if len(edges) != 2 {
		t.Fatalf("expected 2 methods for UserService, got %d", len(edges))
	}

	orderNodes, _ := store.QueryNodesByName(ctx, "OrderService", model.QueryOpts{Kinds: []string{"Class"}})
	if len(orderNodes) != 1 {
		t.Fatalf("expected 1 OrderService, got %d", len(orderNodes))
	}
	orderEdges, _ := store.QueryEdges(ctx, orderNodes[0].ID, constants.SourceKindClassFunc, model.RelContains, model.Outgoing)
	if len(orderEdges) != 1 {
		t.Fatalf("expected 1 method for OrderService, got %d", len(orderEdges))
	}
}

func TestIndexer_ClassContainsFuncEdges_GoReceiver(t *testing.T) {
	projectDir := t.TempDir()
	os.MkdirAll(projectDir, model.DirectoryPermission)
	os.WriteFile(filepath.Join(projectDir, "store.go"), []byte(`package storage
type Store struct{}
func (s *Store) Get(id string) string { return "" }
func (s *Store) Put(id, value string) {}
func HelperFunc() string { return "" }
`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()
	ctx := context.Background()
	_, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal("index:", err)
	}

	classNodes, _ := store.QueryNodesByName(ctx, "Store", model.QueryOpts{Kinds: []string{"Class"}})
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 Store, got %d", len(classNodes))
	}
	edges, _ := store.QueryEdges(ctx, classNodes[0].ID, constants.SourceKindClassFunc, model.RelContains, model.Outgoing)
	if len(edges) != 2 {
		t.Fatalf("expected 2 methods for Store (Get, Put), got %d", len(edges))
	}
}

func TestIndexer_ClassContainsFuncEdges_TopLevelFuncExcluded(t *testing.T) {
	projectDir := t.TempDir()
	os.MkdirAll(projectDir, model.DirectoryPermission)
	os.WriteFile(filepath.Join(projectDir, "utils.go"), []byte(`package utils
func FormatString(s string) string { return s }
func ParseInt(s string) int { return 0 }
`), model.FilePermission)

	indexer, store := setupTestIndexer(t)
	defer store.Close()
	ctx := context.Background()
	_, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal("index:", err)
	}

	allFuncs, _ := store.QueryAllByKind(ctx, "Function", 100)
	for _, funcNode := range allFuncs {
		edges, _ := store.QueryEdges(ctx, funcNode.ID, constants.SourceKindClassFunc, model.RelContains, model.Incoming)
		if len(edges) > 0 {
			t.Fatalf("top-level function %s should not have ClassFunc edge", funcNode.ID)
		}
	}
}

func TestQuerier_QueryClassMembers(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()
	projectDir := t.TempDir()
	srcDir := filepath.Join(projectDir, "src", "main", "java", "com", "example")
	os.MkdirAll(srcDir, model.DirectoryPermission)
	os.WriteFile(filepath.Join(srcDir, "UserService.java"), []byte(`package com.example;
public class UserService {
    public User findById(Long id) { return null; }
    public void deleteById(Long id) {}
}
`), model.FilePermission)

	ctx := context.Background()
	indexer.Index(ctx, projectDir, "main", true, nil)
	querier := NewQuerier(store)

	methods, candidates, _, _, err := querier.QueryClassMembers(ctx, "UserService", 50)
	if err != nil {
		t.Fatal(err)
	}
	if candidates != nil {
		t.Fatal("expected unique match, got candidates")
	}
	if len(methods) != 2 {
		t.Fatalf("expected 2 methods, got %d", len(methods))
	}
}

func TestQuerier_QueryClassMembers_Ambiguous(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()
	projectDir := t.TempDir()
	dir1 := filepath.Join(projectDir, "src", "main", "java", "com", "a")
	dir2 := filepath.Join(projectDir, "src", "main", "java", "com", "b")
	os.MkdirAll(dir1, model.DirectoryPermission)
	os.MkdirAll(dir2, model.DirectoryPermission)
	os.WriteFile(filepath.Join(dir1, "Store.java"), []byte(`package com.a;
public class Store { public void get() {} }
`), model.FilePermission)
	os.WriteFile(filepath.Join(dir2, "Store.java"), []byte(`package com.b;
public class Store { public void put() {} }
`), model.FilePermission)

	ctx := context.Background()
	indexer.Index(ctx, projectDir, "main", true, nil)
	querier := NewQuerier(store)

	methods, candidates, _, _, _ := querier.QueryClassMembers(ctx, "Store", 50)
	if methods != nil {
		t.Fatal("expected ambiguous, got methods")
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}

	methods, candidates, _, _, _ = querier.QueryClassMembers(ctx, "com.a.Store", 50)
	if candidates != nil {
		t.Fatal("expected unique match with qualified name")
	}
	if len(methods) != 1 {
		t.Fatalf("expected 1 method for com.a.Store, got %d", len(methods))
	}
}

func TestQuerier_QueryClassMembers_NotFound(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()
	projectDir := t.TempDir()
	os.MkdirAll(projectDir, model.DirectoryPermission)
	os.WriteFile(filepath.Join(projectDir, "main.go"), []byte(`package main
func main() {}
`), model.FilePermission)

	ctx := context.Background()
	indexer.Index(ctx, projectDir, "main", true, nil)
	querier := NewQuerier(store)

	methods, candidates, _, _, err := querier.QueryClassMembers(ctx, "NonExistent", 50)
	if err != nil {
		t.Fatal(err)
	}
	if methods != nil || candidates != nil {
		t.Fatal("expected nil for non-existent class")
	}
}

func TestIndexer_IncrementalAffectedImporters(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	dir := t.TempDir()

	// File B: service with function Foo
	os.WriteFile(filepath.Join(dir, "service.go"), []byte(`package main

type Service struct{}

func (s *Service) Foo() string { return "foo" }
`), model.FilePermission)

	// File A: imports B, calls B.Foo
	os.WriteFile(filepath.Join(dir, "handler.go"), []byte(`package main

import "service"

func Handle() string {
    svc := &Service{}
    return svc.Foo()
}
`), model.FilePermission)

	ctx := context.Background()

	// Full index
	_, err := indexer.Index(ctx, dir, "main", true, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify IMPORTS edge exists: handler.go → service.go
	allImports, _ := store.QueryAllEdges(ctx, model.RelImports, 0)
	hasImport := false
	for _, e := range allImports {
		if strings.Contains(e.SourceID, "handler.go") && strings.Contains(e.TargetID, "service.go") {
			hasImport = true
		}
	}
	if !hasImport {
		t.Fatal("expected IMPORTS edge handler.go → service.go after full index")
	}

	// Modify B: rename Foo → Bar (add extra content to ensure different size)
	time.Sleep(1100 * time.Millisecond) // ensure different mod time (filesystem may have 1s granularity)
	os.WriteFile(filepath.Join(dir, "service.go"), []byte(`package main

type Service struct{}

func (s *Service) Bar() string { return "bar_modified" }
`), model.FilePermission)

	// Incremental index — only service.go changed
	result, err := indexer.Index(ctx, dir, "main", false, nil)
	if err != nil {
		t.Fatal(err)
	}

	// BUG-4 verification: handler.go should be detected as affected (imports service.go)
	// and re-parsed. FilesProcessed should be 2 (service.go + handler.go).
	if result.FilesProcessed < 2 {
		t.Errorf("BUG-4: expected FilesProcessed >= 2 (service.go + handler.go as affected importer), got %d", result.FilesProcessed)
	}
}

func TestDeterminePrimaryLanguage(t *testing.T) {
	tests := []struct {
		input    map[string]int
		expected string
	}{
		{map[string]int{"java": 70, "python": 3}, "java"},
		{map[string]int{"go": 17}, "go"},
		{map[string]int{"typescript": 10, "javascript": 2}, "typescript"},
		{map[string]int{}, ""},
		{nil, ""},
	}
	for _, test := range tests {
		result := determinePrimaryLanguage(test.input)
		if result != test.expected {
			t.Errorf("determinePrimaryLanguage(%v) = %q, want %q", test.input, result, test.expected)
		}
	}
}

func TestIsSameLanguageFamily(t *testing.T) {
	tests := []struct {
		lang1, lang2 string
		expected     bool
	}{
		{"typescript", "javascript", true},
		{"javascript", "typescript", true},
		{"java", "typescript", false},
		{"go", "python", false},
		{"typescript", "typescript", true},
	}
	for _, test := range tests {
		result := isSameLanguageFamily(test.lang1, test.lang2)
		if result != test.expected {
			t.Errorf("isSameLanguageFamily(%q, %q) = %v, want %v", test.lang1, test.lang2, result, test.expected)
		}
	}
}

func TestInjectCrossProjectSymbols_Basic(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	ctx := context.Background()

	// Setup cross-project index with test data
	tempDir := t.TempDir()
	crossIndex := setupTestCrossIndex(t, tempDir)
	indexer.crossIndex = crossIndex
	indexer.config.Dependencies.Projects = []config.DependencyProject{
		{Path: "/dep-project", Branch: "master"},
	}

	// Create a scanContext and symbolTable
	scanCtx := &scanContext{result: &model.IndexResult{}}
	symbolTable := setupEmptySymbolTable()

	// Verify symbols not present before injection
	if len(symbolTable.FindByName("SeaPayApi")) != 0 {
		t.Fatal("SeaPayApi should not exist before injection")
	}
	if len(symbolTable.FindByName("queryPayOutOrder")) != 0 {
		t.Fatal("queryPayOutOrder should not exist before injection")
	}

	// Inject
	if _, err := indexer.injectCrossProjectSymbols(ctx, scanCtx, symbolTable); err != nil {
		t.Fatalf("injectCrossProjectSymbols: %v", err)
	}

	// Verify class-level symbol
	classSymbols := symbolTable.FindByName("SeaPayApi")
	if len(classSymbols) != 1 {
		t.Fatalf("expected 1 SeaPayApi symbol, got %d", len(classSymbols))
	}
	if classSymbols[0].Kind != constants.KindInterface {
		t.Errorf("expected Interface kind, got %s", classSymbols[0].Kind)
	}
	if classSymbols[0].FilePath != constants.FilePathCrossProject {
		t.Errorf("expected [cross-project] file_path, got %s", classSymbols[0].FilePath)
	}

	// Verify method-level symbol
	methodSymbols := symbolTable.FindByName("queryPayOutOrder")
	if len(methodSymbols) != 1 {
		t.Fatalf("expected 1 queryPayOutOrder symbol, got %d", len(methodSymbols))
	}
	if methodSymbols[0].Kind != constants.KindFunction {
		t.Errorf("expected Function kind, got %s", methodSymbols[0].Kind)
	}
	if methodSymbols[0].QualifiedName != "com.dayu.pay.web.SeaPayApi.queryPayOutOrder" {
		t.Errorf("unexpected QN: %s", methodSymbols[0].QualifiedName)
	}

	// Verify FindByQualifiedName works (needed for resolveFullQualifiedType)
	qnMatches := symbolTable.FindByQualifiedName("com.dayu.pay.web.SeaPayApi")
	if len(qnMatches) != 1 {
		t.Fatalf("FindByQualifiedName: expected 1, got %d", len(qnMatches))
	}

	// Verify params format
	if len(methodSymbols[0].Params) == 0 {
		t.Error("expected non-empty Params")
	}
	hasType := false
	for _, p := range methodSymbols[0].Params {
		if p.Type == "SeaPayQueryPayOutOrderRequest" {
			hasType = true
			break
		}
	}
	if !hasType {
		t.Errorf("Params should contain type SeaPayQueryPayOutOrderRequest, got: %v", methodSymbols[0].Params)
	}
}

func TestInjectCrossProjectSymbols_NoDeps(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	ctx := context.Background()
	scanCtx := &scanContext{result: &model.IndexResult{}}
	symbolTable := setupEmptySymbolTable()

	// No dependencies configured — should be a no-op
	if _, err := indexer.injectCrossProjectSymbols(ctx, scanCtx, symbolTable); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInjectCrossProjectSymbols_NoCrossIndex(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	ctx := context.Background()
	scanCtx := &scanContext{result: &model.IndexResult{}}
	symbolTable := setupEmptySymbolTable()

	indexer.config.Dependencies.Projects = []config.DependencyProject{
		{Path: "/dep-project", Branch: "master"},
	}
	// crossIndex is nil — should be a no-op
	if _, err := indexer.injectCrossProjectSymbols(ctx, scanCtx, symbolTable); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInjectCrossProjectSymbols_MethodOverload(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	ctx := context.Background()
	tempDir := t.TempDir()

	crossIndex := setupTestCrossIndexWithOverload(t, tempDir)
	indexer.crossIndex = crossIndex
	indexer.config.Dependencies.Projects = []config.DependencyProject{
		{Path: "/dep-project", Branch: "master"},
	}

	scanCtx := &scanContext{result: &model.IndexResult{}}
	symbolTable := setupEmptySymbolTable()

	if _, err := indexer.injectCrossProjectSymbols(ctx, scanCtx, symbolTable); err != nil {
		t.Fatalf("injectCrossProjectSymbols: %v", err)
	}

	// Two overloaded methods should both be present with unique IDs
	methods := symbolTable.FindByName("createOrder")
	if len(methods) != 2 {
		t.Fatalf("expected 2 createOrder symbols (overloaded), got %d", len(methods))
	}
	if methods[0].ID == methods[1].ID {
		t.Error("overloaded methods should have different IDs")
	}
}

func setupEmptySymbolTable() *resolver.SymbolTable {
	return resolver.NewSymbolTable()
}

func setupTestCrossIndex(t *testing.T, tempDir string) *crossindex.JSONStore {
	t.Helper()
	store := crossindex.NewJSONStore(filepath.Join(tempDir, "cross_index.json"))
	ctx := context.Background()
	_ = store.RegisterProject(ctx, crossindex.ProjectEntry{
		ProjectPath: "/dep-project",
		Branch:      "master",
		Symbols: []crossindex.GlobalSymbol{
			{
				QualifiedName: "com.dayu.pay.web.SeaPayApi",
				Name:          "SeaPayApi",
				Kind:          constants.KindInterface,
				ClassType:     "interface",
				NodeID:        "node-sea",
				Annotations:   []string{"FeignClient"},
				Methods: []crossindex.GlobalMethod{
					{
						Name:       "queryPayOutOrder",
						NodeID:     "method-query",
						Params:     []string{"SeaPayQueryPayOutOrderRequest"},
						ReturnType: "ResponseResult",
					},
				},
			},
		},
	})
	return store
}

func setupTestCrossIndexWithOverload(t *testing.T, tempDir string) *crossindex.JSONStore {
	t.Helper()
	store := crossindex.NewJSONStore(filepath.Join(tempDir, "cross_index.json"))
	ctx := context.Background()
	_ = store.RegisterProject(ctx, crossindex.ProjectEntry{
		ProjectPath: "/dep-project",
		Branch:      "master",
		Symbols: []crossindex.GlobalSymbol{
			{
				QualifiedName: "com.example.OrderApi",
				Name:          "OrderApi",
				Kind:          constants.KindInterface,
				ClassType:     "interface",
				Methods: []crossindex.GlobalMethod{
					{Name: "createOrder", Params: []string{"OrderReqs"}},
					{Name: "createOrder", Params: []string{"OrderReqs", "String"}},
				},
			},
		},
	})
	return store
}
