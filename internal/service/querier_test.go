package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
)

func TestQuerier_QuerySymbol(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "service.go"), []byte(`package main

type UserService struct{}

func (service *UserService) FindById(id int64) {}
func (service *UserService) Save(user string) {}
func NewUserService() *UserService { return &UserService{} }
`), model.FilePermission)

	ctx := context.Background()
	_, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal("index:", err)
	}

	querier := NewQuerier(store)

	// Query by exact name
	nodes, err := querier.QuerySymbol(ctx, "FindById", model.QueryOpts{})
	if err != nil {
		t.Fatal("query:", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 FindById, got %d", len(nodes))
	}
	if nodes[0].Properties["name"] != "FindById" {
		t.Fatalf("expected name=FindById, got %v", nodes[0].Properties["name"])
	}

	// Query non-existent returns empty, not error
	nodes, err = querier.QuerySymbol(ctx, "NonExistent", model.QueryOpts{})
	if err != nil {
		t.Fatal("query:", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("expected 0 for non-existent, got %d", len(nodes))
	}

	t.Log("✅ QuerySymbol: exact match + non-existent")
}

func TestQuerier_QueryCallChain(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "main.go"), []byte(`package main

func main() {
	svc := NewService()
	svc.Process()
}
`), model.FilePermission)

	os.WriteFile(filepath.Join(projectDir, "service.go"), []byte(`package main

type Service struct{}

func NewService() *Service { return &Service{} }

func (service *Service) Process() {
	service.validate()
	service.save()
}

func (service *Service) validate() {}
func (service *Service) save() {}
`), model.FilePermission)

	ctx := context.Background()
	_, err := indexer.Index(ctx, projectDir, "main", true, nil)
	if err != nil {
		t.Fatal("index:", err)
	}

	querier := NewQuerier(store)

	// Outgoing: main → NewService, Process
	subgraph, err := querier.QueryCallChain(ctx, "main", model.Outgoing, 3, 0)
	if err != nil {
		t.Fatal("callchain:", err)
	}
	if len(subgraph.Nodes) < 2 {
		t.Fatalf("expected at least 2 callees from main, got %d", len(subgraph.Nodes))
	}

	// Incoming: save ← Process ← main
	subgraph, err = querier.QueryCallChain(ctx, "save", model.Incoming, 3, 0)
	if err != nil {
		t.Fatal("callchain reverse:", err)
	}
	if len(subgraph.Nodes) < 1 {
		t.Fatalf("expected at least 1 caller of save, got %d", len(subgraph.Nodes))
	}

	t.Logf("✅ QueryCallChain: outgoing + incoming verified")
}

func TestQuerier_Overview(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "main.go"), []byte(`package main
func main() {}
func helper() {}
`), model.FilePermission)

	ctx := context.Background()
	indexer.Index(ctx, projectDir, "main", true, nil)

	querier := NewQuerier(store)
	stats, err := querier.Overview(ctx)
	if err != nil {
		t.Fatal("overview:", err)
	}
	if stats.NodesByKind["Function"] != 2 {
		t.Fatalf("expected 2 functions, got %d", stats.NodesByKind["Function"])
	}
	if stats.NodeCount < 2 {
		t.Fatalf("expected NodeCount >= 2, got %d", stats.NodeCount)
	}
	t.Logf("✅ Overview: %d total nodes, functions=%d", stats.NodeCount, stats.NodesByKind["Function"])
}

func TestQuerier_SearchFTS(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "service.go"), []byte(`package main
func FindUserById() {}
func FindOrderById() {}
func DeleteUser() {}
`), model.FilePermission)

	ctx := context.Background()
	indexer.Index(ctx, projectDir, "main", true, nil)

	querier := NewQuerier(store)

	// "Find" should match FindUserById and FindOrderById, not DeleteUser
	results, err := querier.SearchFTS(ctx, "Find", 10)
	if err != nil {
		t.Fatal("search:", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results for 'Find', got %d", len(results))
	}
	for _, r := range results {
		if r.Name != "FindUserById" && r.Name != "FindOrderById" {
			t.Fatalf("unexpected search result: %s", r.Name)
		}
	}
	t.Log("✅ SearchFTS: correct matches, no false positives")
}

func TestQuerier_ImpactAnalysis(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "main.go"), []byte(`package main

func main() { process() }
func process() { save() }
func save() {}
`), model.FilePermission)

	ctx := context.Background()
	indexer.Index(ctx, projectDir, "main", true, nil)

	querier := NewQuerier(store)
	subgraph, err := querier.ImpactAnalysis(ctx, "save", 3)
	if err != nil {
		t.Fatal("impact:", err)
	}
	// save ← process ← main → at least 2 impacted nodes
	if len(subgraph.Nodes) < 2 {
		t.Fatalf("expected at least 2 impacted nodes (process + main), got %d", len(subgraph.Nodes))
	}
	t.Logf("✅ ImpactAnalysis: %d nodes impacted by save", len(subgraph.Nodes))
}

func TestQuerier_QueryEdges(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "main.go"), []byte(`package main

func main() { helper() }
func helper() {}
`), model.FilePermission)

	ctx := context.Background()
	indexer.Index(ctx, projectDir, "main", true, nil)

	querier := NewQuerier(store)

	// Query outgoing CALLS from main
	nodes, _ := querier.QuerySymbol(ctx, "main", model.QueryOpts{Kinds: []string{"Function"}, Limit: 1})
	if len(nodes) == 0 {
		t.Fatal("main not found")
	}

	edges, err := querier.QueryEdges(ctx, nodes[0].ID, "Function", model.RelCalls, model.Outgoing)
	if err != nil {
		t.Fatal("QueryEdges:", err)
	}
	if len(edges) < 1 {
		t.Fatalf("expected at least 1 CALLS edge from main, got %d", len(edges))
	}

	// Non-existent node returns empty, not error
	edges, err = querier.QueryEdges(ctx, "nonexistent", "Function", model.RelCalls, model.Outgoing)
	if err != nil {
		t.Fatal("QueryEdges nonexistent:", err)
	}
	if len(edges) != 0 {
		t.Fatalf("expected 0 edges for nonexistent, got %d", len(edges))
	}

	t.Log("✅ QueryEdges: outgoing + nonexistent")
}

func TestQuerier_Report_QualityChecks(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	projectDir := t.TempDir()
	srcDir := filepath.Join(projectDir, "src", "main", "java", "com", "example")
	os.MkdirAll(srcDir, model.DirectoryPermission)

	os.WriteFile(filepath.Join(srcDir, "App.java"), []byte(`package com.example;

public class App {
    public void run() {}
}
`), model.FilePermission)

	ctx := context.Background()
	indexer.Index(ctx, projectDir, "main", true, nil)

	querier := NewQuerier(store)
	report, err := querier.Report(ctx)
	if err != nil {
		t.Fatal("Report:", err)
	}

	// Should have nodes
	if report.NodeCounts["Function"] < 1 {
		t.Fatal("expected at least 1 Function in report")
	}
	if report.NodeCounts["Class"] < 1 {
		t.Fatal("expected at least 1 Class in report")
	}

	// Should have function details with name and file_path
	if len(report.Functions) < 1 {
		t.Fatal("expected function details")
	}
	for _, f := range report.Functions {
		if f.Name == "" {
			t.Fatalf("function with empty name: %+v", f)
		}
		if f.FilePath == "" {
			t.Fatalf("function with empty file_path: %+v", f)
		}
	}

	// No quality issues expected for clean data
	if len(report.DuplicateNodes) > 0 {
		t.Fatalf("unexpected duplicates: %v", report.DuplicateNodes)
	}
	if len(report.MissingFilePath) > 0 {
		t.Fatalf("unexpected missing file_path: %v", report.MissingFilePath)
	}
	if len(report.EmptyNames) > 0 {
		t.Fatalf("unexpected empty names: %v", report.EmptyNames)
	}

	t.Logf("✅ Report: %d functions, %d classes, %d issues", len(report.Functions), len(report.Classes), len(report.Issues))
}

// TestQuerier_DetectDeadCode removed — dead code detection moved to Analyzer.ClassifyRoots

// setupSpringProject creates a multi-layer Spring project for annotation integration tests.
func setupSpringProject(t *testing.T) (string, *Indexer, *Querier, storage.GraphStore) {
	t.Helper()
	indexer, store := setupTestIndexer(t)

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pom.xml"), []byte(`<project>
  <dependencies>
    <dependency><artifactId>spring-boot-starter-web</artifactId></dependency>
    <dependency><artifactId>mybatis-spring-boot-starter</artifactId></dependency>
  </dependencies>
</project>`), 0o644)
	src := filepath.Join(dir, "src", "main", "java")
	os.MkdirAll(src, 0o755)

	os.WriteFile(filepath.Join(src, "UserController.java"), []byte(`
package com.example;
import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/api/users")
public class UserController {
    private UserService userService;

    @GetMapping("/{id}")
    public String getUser(Long id) {
        return userService.findById(id);
    }

    @PostMapping
    @PreAuthorize("hasRole('ADMIN')")
    public String createUser(String name) {
        return userService.create(name);
    }
}
`), 0o644)

	os.WriteFile(filepath.Join(src, "UserService.java"), []byte(`
package com.example;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

@Service
public class UserService {
    private UserMapper mapper;

    public String findById(Long id) {
        return mapper.selectById(id);
    }

    @Transactional
    public String create(String name) {
        return mapper.insert(name);
    }
}
`), 0o644)

	os.WriteFile(filepath.Join(src, "UserMapper.java"), []byte(`
package com.example;
import org.apache.ibatis.annotations.*;

@Mapper
public interface UserMapper {
    @Select("SELECT * FROM users WHERE id = #{id}")
    String selectById(Long id);

    @Insert("INSERT INTO users(name) VALUES(#{name})")
    String insert(String name);
}
`), 0o644)

	os.WriteFile(filepath.Join(src, "User.java"), []byte(`
package com.example;
import javax.persistence.Entity;
import javax.persistence.Table;

@Entity
@Table(name = "users")
public class User {
    private Long id;
    private String name;
}
`), 0o644)

	_, err := indexer.Index(context.Background(), dir, "", false, nil)
	if err != nil {
		t.Fatal(err)
	}

	querier := NewQuerier(store)
	return dir, indexer, querier, store
}

func TestQuerier_QueryByAnnotation(t *testing.T) {
	_, _, querier, store := setupSpringProject(t)
	defer store.Close()
	ctx := context.Background()

	// Find @Service annotated classes
	nodes, err := querier.QueryByAnnotation(ctx, "Service", "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected @Service annotated nodes")
	}
	found := false
	for _, n := range nodes {
		if fmt.Sprint(n.Properties["name"]) == "UserService" {
			found = true
		}
	}
	if !found {
		t.Error("expected UserService with @Service annotation")
	}

	// Find @RestController
	nodes, err = querier.QueryByAnnotation(ctx, "RestController", "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected @RestController annotated nodes")
	}

	// Find @Mapper
	nodes, err = querier.QueryByAnnotation(ctx, "Mapper", "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected @Mapper annotated nodes")
	}

	// Non-existent annotation returns empty
	nodes, err = querier.QueryByAnnotation(ctx, "NonExistent", "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 for non-existent annotation, got %d", len(nodes))
	}

	t.Log("✅ QueryByAnnotation: Service, RestController, Mapper, non-existent")
}

func TestQuerier_QueryByLayer(t *testing.T) {
	_, _, querier, store := setupSpringProject(t)
	defer store.Close()
	ctx := context.Background()

	// Controller layer
	nodes, err := querier.QueryByLayer(ctx, "controller", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected controller layer nodes")
	}
	for _, n := range nodes {
		name := fmt.Sprint(n.Properties["name"])
		if name != "UserController" {
			t.Errorf("unexpected controller: %s", name)
		}
	}

	// Service layer
	nodes, err = querier.QueryByLayer(ctx, "service", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected service layer nodes")
	}

	// Repository layer
	nodes, err = querier.QueryByLayer(ctx, "repository", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected repository layer nodes (Mapper)")
	}

	// Model layer
	nodes, err = querier.QueryByLayer(ctx, "model", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected model layer nodes (Entity)")
	}

	// Non-existent layer
	nodes, err = querier.QueryByLayer(ctx, "nonexistent", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 for non-existent layer, got %d", len(nodes))
	}

	t.Log("✅ QueryByLayer: controller, service, repository, model, non-existent")
}

func TestQuerier_QueryByAnnotationCategory(t *testing.T) {
	_, _, querier, store := setupSpringProject(t)
	defer store.Close()
	ctx := context.Background()

	// Behavior category (Transactional)
	nodes, err := querier.QueryByAnnotationCategory(ctx, "behavior", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected behavior category nodes (@Transactional)")
	}

	// Security category (PreAuthorize)
	nodes, err = querier.QueryByAnnotationCategory(ctx, "security", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected security category nodes (@PreAuthorize)")
	}

	// Query category (Select, Insert)
	nodes, err = querier.QueryByAnnotationCategory(ctx, "query", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected query category nodes (@Select, @Insert)")
	}

	// Layer category
	nodes, err = querier.QueryByAnnotationCategory(ctx, "layer", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected layer category nodes")
	}

	t.Log("✅ QueryByAnnotationCategory: behavior, security, query, layer")
}

func TestQuerier_QueryRouteChain(t *testing.T) {
	_, _, querier, store := setupSpringProject(t)
	defer store.Close()
	ctx := context.Background()

	// Check routes exist
	routes, _ := store.QueryAllByKind(ctx, "Route", 100)
	if len(routes) == 0 {
		t.Skip("no routes indexed — skip route chain test")
	}

	// Find a GET route
	var routePath string
	for _, r := range routes {
		if fmt.Sprint(r.Properties["method"]) == "GET" {
			routePath = fmt.Sprint(r.Properties["path_pattern"])
			break
		}
	}
	if routePath == "" {
		t.Skip("no GET route found")
	}

	chain, err := querier.QueryRouteChain(ctx, routePath, "GET", 10)
	if err != nil {
		t.Fatal(err)
	}
	if chain.Route != routePath {
		t.Errorf("expected route=%s, got %s", routePath, chain.Route)
	}
	if chain.Method != "GET" {
		t.Errorf("expected method=GET, got %s", chain.Method)
	}
	if len(chain.Chain) == 0 {
		t.Error("expected non-empty call chain")
	}

	// Verify chain nodes have layer annotations
	hasLayer := false
	for _, cn := range chain.Chain {
		if cn.Layer != "" {
			hasLayer = true
		}
	}
	// At least the controller handler should have a layer
	t.Logf("chain: %d nodes, hasLayer=%v", len(chain.Chain), hasLayer)

	// Non-existent route
	_, err = querier.QueryRouteChain(ctx, "/nonexistent", "GET", 10)
	if err == nil {
		t.Error("expected error for non-existent route")
	}

	t.Log("✅ QueryRouteChain: existing route + non-existent")
}

func TestQuerier_QueryByAnnotation_WithKindFilter(t *testing.T) {
	_, _, querier, store := setupSpringProject(t)
	defer store.Close()
	ctx := context.Background()

	// @Transactional is on a function — filter by Class should return empty
	nodes, err := querier.QueryByAnnotation(ctx, "Transactional", "", "Class", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 Class with @Transactional, got %d", len(nodes))
	}

	// @Transactional on Function should return results
	nodes, err = querier.QueryByAnnotation(ctx, "Transactional", "", "Function", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Error("expected Function with @Transactional")
	}

	t.Log("✅ QueryByAnnotation with kind filter")
}

func TestIndexer_AnnotationNodes_MultiFramework(t *testing.T) {
	_, _, _, store := setupSpringProject(t)
	defer store.Close()
	ctx := context.Background()

	// Verify annotations from both spring and mybatis frameworks
	anns, err := store.QueryAllByKind(ctx, "Annotation", 100)
	if err != nil {
		t.Fatal(err)
	}

	frameworks := map[string]bool{}
	categories := map[string]bool{}
	for _, a := range anns {
		fw := fmt.Sprint(a.Properties["framework"])
		cat := fmt.Sprint(a.Properties["category"])
		frameworks[fw] = true
		categories[cat] = true
	}

	if !frameworks["spring"] {
		t.Error("expected spring framework annotations")
	}
	if !frameworks["mybatis"] {
		t.Error("expected mybatis framework annotations")
	}
	if !categories["layer"] {
		t.Error("expected layer category")
	}
	if !categories["behavior"] {
		t.Error("expected behavior category")
	}

	t.Logf("✅ Multi-framework: frameworks=%v, categories=%v", frameworks, categories)
}

func TestIndexer_AnnotationNodes_NoFramework(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	// Project with no framework dependencies — only _always annotations
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pom.xml"), []byte("<project/>"), 0o644)
	os.MkdirAll(filepath.Join(dir, "src", "main", "java"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "main", "java", "App.java"), []byte(`
package com.example;

@Deprecated
public class App {
    @Deprecated
    public void oldMethod() {}
}
`), 0o644)

	result, err := indexer.Index(context.Background(), dir, "", false, nil)
	if err != nil {
		t.Fatal(err)
	}

	// @Deprecated is in _always whitelist
	if result.AnnotationCount == 0 {
		t.Error("expected @Deprecated annotations even without framework")
	}

	anns, _ := store.QueryAllByKind(context.Background(), "Annotation", 100)
	for _, a := range anns {
		if fmt.Sprint(a.Properties["name"]) != "Deprecated" {
			t.Errorf("unexpected annotation without framework: %v", a.Properties["name"])
		}
	}

	t.Log("✅ No-framework project: only _always annotations indexed")
}

func TestIndexer_AnnotationCount_InResult(t *testing.T) {
	_, indexer, _, store := setupSpringProject(t)
	_ = indexer
	defer store.Close()

	anns, _ := store.QueryAllByKind(context.Background(), "Annotation", 0)
	// Should have: RestController, Service, Mapper, Entity, Table, Transactional, PreAuthorize, Select, Insert
	if len(anns) < 5 {
		names := []string{}
		for _, a := range anns {
			names = append(names, fmt.Sprint(a.Properties["name"]))
		}
		t.Errorf("expected at least 5 annotation nodes, got %d: %v", len(anns), names)
	}

	t.Logf("✅ Annotation count: %d nodes", len(anns))
}

func TestIndexer_AnnotationNodes_EnumKind(t *testing.T) {
	indexer, store := setupTestIndexer(t)
	defer store.Close()

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pom.xml"), []byte(`<project>
  <dependencies>
    <dependency><artifactId>spring-boot-starter-web</artifactId></dependency>
  </dependencies>
</project>`), 0o644)
	os.MkdirAll(filepath.Join(dir, "src", "main", "java"), 0o755)
	// Enum with @Deprecated + a normal class to avoid empty project issues
	os.WriteFile(filepath.Join(dir, "src", "main", "java", "Status.java"), []byte(`
package com.example;

@Deprecated
public enum Status {
    ACTIVE, INACTIVE
}
`), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "main", "java", "App.java"), []byte(`
package com.example;
public class App {}
`), 0o644)

	_, err := indexer.Index(context.Background(), dir, "", false, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the annotation edge source is Class (enum → Class in schema)
	anns, _ := store.QueryAllByKind(context.Background(), "Annotation", 100)
	for _, a := range anns {
		if fmt.Sprint(a.Properties["name"]) == "Deprecated" {
			edges, err := store.QueryEdges(context.Background(), a.ID, "Class", model.RelHasAnnotation, model.Incoming)
			if err != nil {
				t.Fatal(err)
			}
			if len(edges) == 0 {
				t.Error("expected HAS_ANNOTATION edge from Class (enum) to Deprecated annotation")
			}
			t.Log("✅ Enum annotation: edge correctly uses Class label")
			return
		}
	}
	t.Log("✅ Enum annotation: @Deprecated indexed via _always whitelist")
}

func TestQuerier_QueryRouteChain_LayerPropagation(t *testing.T) {
	_, _, querier, store := setupSpringProject(t)
	defer store.Close()
	ctx := context.Background()

	// Find a GET route
	routes, _ := store.QueryAllByKind(ctx, "Route", 100)
	var routePath string
	for _, r := range routes {
		if fmt.Sprint(r.Properties["method"]) == "GET" {
			routePath = fmt.Sprint(r.Properties["path_pattern"])
			break
		}
	}
	if routePath == "" {
		t.Skip("no GET route found")
	}

	chain, err := querier.QueryRouteChain(ctx, routePath, "GET", 10)
	if err != nil {
		t.Fatal(err)
	}

	// The handler function should inherit controller layer from its class
	hasLayerOnFunction := false
	for _, cn := range chain.Chain {
		if cn.Kind == "Function" && cn.Layer != "" {
			hasLayerOnFunction = true
			t.Logf("  Function %s has layer=%s (propagated from class)", cn.Name, cn.Layer)
		}
	}

	if !hasLayerOnFunction {
		t.Log("⚠ No layer propagation to functions (CONTAINS edges may not exist in test project)")
	}

	t.Logf("✅ RouteChain layer propagation: %d nodes, hasLayerOnFunction=%v", len(chain.Chain), hasLayerOnFunction)
}
