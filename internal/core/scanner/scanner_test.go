package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectProject_Go(t *testing.T) {
	// Test against our own project
	scanner := New(&Config{RootPath: "../../../"})
	info, err := scanner.DetectProject()
	if err != nil {
		t.Fatal("detect:", err)
	}
	if info.ProjectType != "go" {
		t.Fatalf("expected go, got %s", info.ProjectType)
	}
	if !info.IsGit {
		t.Log("not a git repo (expected in test env)")
	}
	t.Logf("✅ Detected: type=%s buildFiles=%v", info.ProjectType, info.BuildFiles)
}

func TestScan(t *testing.T) {
	scanner := New(&Config{RootPath: "../../../"})
	files, skipped, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatal("scan:", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least 1 file")
	}

	// Check that all files have a language
	for _, file := range files {
		if file.Language == "" {
			t.Fatalf("file %s has no language", file.RelPath)
		}
	}

	t.Logf("✅ Scanned %d files, %d skipped", len(files), len(skipped))

	// Print language distribution
	languageCount := make(map[string]int)
	for _, file := range files {
		languageCount[file.Language]++
	}
	for language, count := range languageCount {
		t.Logf("  %s: %d files", language, count)
	}
}

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"main.go", "go"},
		{"App.java", "java"},
		{"index.ts", "typescript"},
		{"index.tsx", "typescript"},
		{"app.py", "python"},
		{"lib.rs", "rust"},
		{"main.c", "c"},
		{"main.cpp", "cpp"},
		{"Program.cs", "csharp"},
		{"app.rb", "ruby"},
		{"main.swift", "swift"},
		{"index.php", "php"},
		{"main.dart", "dart"},
		{"app.js", "javascript"},
		{"README.md", ""},
		{"config.yaml", ""},
		{"Makefile", ""},
	}

	for _, test := range tests {
		result := detectLanguage(test.path)
		if result != test.expected {
			t.Errorf("detectLanguage(%q) = %q, want %q", test.path, result, test.expected)
		}
	}
}

func TestScanSkipsLargeFiles(t *testing.T) {
	// Create temp project with one large file
	tempDir := t.TempDir()
	// Normal file
	os.WriteFile(filepath.Join(tempDir, "small.go"), []byte("package main"), 0o644)
	// Large file (over 512KB)
	largeContent := make([]byte, 600*1024)
	os.WriteFile(filepath.Join(tempDir, "large.go"), largeContent, 0o644)

	scanner := New(&Config{RootPath: tempDir})
	files, skipped, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatal("scan:", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped, got %d", len(skipped))
	}
	if skipped[0].Reason != "exceeds_max_size" {
		t.Fatalf("expected exceeds_max_size, got %s", skipped[0].Reason)
	}
}

func TestScanIgnoresDirectories(t *testing.T) {
	tempDir := t.TempDir()
	// Source file
	os.WriteFile(filepath.Join(tempDir, "main.go"), []byte("package main"), 0o644)
	// File inside node_modules (should be ignored)
	os.MkdirAll(filepath.Join(tempDir, "node_modules", "pkg"), 0o755)
	os.WriteFile(filepath.Join(tempDir, "node_modules", "pkg", "index.js"), []byte("module.exports={}"), 0o644)
	// File inside __pycache__ (should be ignored)
	os.MkdirAll(filepath.Join(tempDir, "__pycache__"), 0o755)
	os.WriteFile(filepath.Join(tempDir, "__pycache__", "mod.py"), []byte("pass"), 0o644)

	scanner := New(&Config{RootPath: tempDir})
	files, _, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatal("scan:", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file (only main.go), got %d", len(files))
	}
	if files[0].RelPath != "main.go" {
		t.Fatalf("expected main.go, got %s", files[0].RelPath)
	}
}

func TestScanEmptyDirectory(t *testing.T) {
	tempDir := t.TempDir()
	scanner := New(&Config{RootPath: tempDir})
	files, skipped, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatal("scan:", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected 0 files, got %d", len(files))
	}
	if len(skipped) != 0 {
		t.Fatalf("expected 0 skipped, got %d", len(skipped))
	}
}

func TestDetectMavenModules(t *testing.T) {
	tempDir := t.TempDir()
	pomContent := `<?xml version="1.0"?>
<project>
  <modules>
    <module>api</module>
    <module>core</module>
  </modules>
</project>`
	os.WriteFile(filepath.Join(tempDir, "pom.xml"), []byte(pomContent), 0o644)

	scanner := New(&Config{RootPath: tempDir})
	info, err := scanner.DetectProject()
	if err != nil {
		t.Fatal("detect:", err)
	}
	if info.ProjectType != "maven" {
		t.Fatalf("expected maven, got %s", info.ProjectType)
	}
	if len(info.SubModules) != 2 {
		t.Fatalf("expected 2 submodules, got %d", len(info.SubModules))
	}
	if info.SubModules[0].Name != "api" {
		t.Fatalf("expected api, got %s", info.SubModules[0].Name)
	}
	if info.SubModules[1].Name != "core" {
		t.Fatalf("expected core, got %s", info.SubModules[1].Name)
	}
}

func TestDetectGradleModules(t *testing.T) {
	tempDir := t.TempDir()
	settingsContent := `rootProject.name = 'my-app'
include ':api', ':core', ':web'`
	os.WriteFile(filepath.Join(tempDir, "settings.gradle"), []byte(settingsContent), 0o644)

	scanner := New(&Config{RootPath: tempDir})
	info, err := scanner.DetectProject()
	if err != nil {
		t.Fatal("detect:", err)
	}
	if info.ProjectType != "gradle" {
		t.Fatalf("expected gradle, got %s", info.ProjectType)
	}
	if len(info.SubModules) != 3 {
		t.Fatalf("expected 3 submodules, got %d", len(info.SubModules))
	}
}

func TestDetectNpmWorkspaces(t *testing.T) {
	tempDir := t.TempDir()
	// Create workspace dirs
	os.MkdirAll(filepath.Join(tempDir, "packages", "client"), 0o755)
	os.MkdirAll(filepath.Join(tempDir, "packages", "server"), 0o755)
	packageContent := `{"workspaces": ["packages/*"]}`
	os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(packageContent), 0o644)

	scanner := New(&Config{RootPath: tempDir})
	info, err := scanner.DetectProject()
	if err != nil {
		t.Fatal("detect:", err)
	}
	if info.ProjectType != "npm" {
		t.Fatalf("expected npm, got %s", info.ProjectType)
	}
	if len(info.SubModules) != 2 {
		t.Fatalf("expected 2 submodules, got %d", len(info.SubModules))
	}
}

func TestDetectMavenModules_SubModuleBuildFiles(t *testing.T) {
	dir := t.TempDir()
	// Parent pom with modules B and C
	os.WriteFile(filepath.Join(dir, "pom.xml"), []byte(`<project>
  <modules>
    <module>B</module>
    <module>C</module>
  </modules>
</project>`), 0o644)
	// Sub-module poms
	os.MkdirAll(filepath.Join(dir, "B"), 0o755)
	os.WriteFile(filepath.Join(dir, "B", "pom.xml"), []byte(`<project>
  <dependencies><dependency><artifactId>spring-web</artifactId></dependency></dependencies>
</project>`), 0o644)
	os.MkdirAll(filepath.Join(dir, "C"), 0o755)
	os.WriteFile(filepath.Join(dir, "C", "pom.xml"), []byte(`<project>
  <dependencies><dependency><groupId>org.apache.dubbo</groupId></dependency></dependencies>
</project>`), 0o644)
	// Undeclared directory D
	os.MkdirAll(filepath.Join(dir, "D", "src", "main", "java"), 0o755)
	os.WriteFile(filepath.Join(dir, "D", "src", "main", "java", "Orphan.java"), []byte("class Orphan{}"), 0o644)

	s := New(&Config{RootPath: dir})
	info, err := s.DetectProject()
	if err != nil {
		t.Fatal(err)
	}

	// BuildFiles should include sub-module poms
	hasBPom, hasCPom := false, false
	for _, bf := range info.BuildFiles {
		if bf == filepath.Join("B", "pom.xml") {
			hasBPom = true
		}
		if bf == filepath.Join("C", "pom.xml") {
			hasCPom = true
		}
	}
	if !hasBPom {
		t.Error("expected B/pom.xml in BuildFiles")
	}
	if !hasCPom {
		t.Error("expected C/pom.xml in BuildFiles")
	}
}

func TestScan_MultiModule_SkipsUndeclaredDirs(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pom.xml"), []byte(`<project>
  <modules>
    <module>B</module>
  </modules>
</project>`), 0o644)
	// Declared module B with a Java file
	os.MkdirAll(filepath.Join(dir, "B", "src", "main", "java"), 0o755)
	os.WriteFile(filepath.Join(dir, "B", "src", "main", "java", "App.java"), []byte("class App{}"), 0o644)
	os.WriteFile(filepath.Join(dir, "B", "pom.xml"), []byte("<project/>"), 0o644)
	// Undeclared directory D with a Java file
	os.MkdirAll(filepath.Join(dir, "D", "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "D", "src", "Orphan.java"), []byte("class Orphan{}"), 0o644)
	// Root src (should be scanned)
	os.MkdirAll(filepath.Join(dir, "src", "main", "java"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "main", "java", "Root.java"), []byte("class Root{}"), 0o644)

	s := New(&Config{RootPath: dir})
	s.DetectProject()
	files, _, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	foundB, foundD, foundRoot := false, false, false
	for _, f := range files {
		if f.RelPath == filepath.Join("B", "src", "main", "java", "App.java") {
			foundB = true
		}
		if f.RelPath == filepath.Join("D", "src", "Orphan.java") {
			foundD = true
		}
		if f.RelPath == filepath.Join("src", "main", "java", "Root.java") {
			foundRoot = true
		}
	}
	if !foundB {
		t.Error("expected B/src/.../App.java to be scanned")
	}
	if foundD {
		t.Error("D/src/Orphan.java should NOT be scanned (undeclared module)")
	}
	if !foundRoot {
		t.Error("expected src/.../Root.java to be scanned (root src)")
	}
}

func TestScan_SingleModule_NoRestriction(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pom.xml"), []byte("<project/>"), 0o644)
	os.MkdirAll(filepath.Join(dir, "src", "main", "java"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "main", "java", "App.java"), []byte("class App{}"), 0o644)
	os.MkdirAll(filepath.Join(dir, "scripts"), 0o755)
	os.WriteFile(filepath.Join(dir, "scripts", "tool.py"), []byte("print('hi')"), 0o644)

	s := New(&Config{RootPath: dir})
	s.DetectProject()
	files, _, _ := s.Scan(context.Background())

	foundJava, foundPy := false, false
	for _, f := range files {
		if f.Language == "java" {
			foundJava = true
		}
		if f.Language == "python" {
			foundPy = true
		}
	}
	if !foundJava {
		t.Error("expected Java file scanned in single-module")
	}
	if !foundPy {
		t.Error("expected Python file scanned in single-module (no restriction)")
	}
}

func TestBuildValidTopDirs(t *testing.T) {
	// nil info
	if dirs := buildValidTopDirs(nil); dirs != nil {
		t.Error("nil info should return nil")
	}
	// no submodules
	if dirs := buildValidTopDirs(&ProjectInfo{}); dirs != nil {
		t.Error("no submodules should return nil")
	}
	// with submodules
	info := &ProjectInfo{
		SubModules: []SubModule{
			{Name: "api"}, {Name: "core"},
		},
	}
	dirs := buildValidTopDirs(info)
	if !dirs["api"] || !dirs["core"] || !dirs["src"] {
		t.Errorf("expected api, core, src in valid dirs, got %v", dirs)
	}
	if dirs["legacy"] {
		t.Error("legacy should not be in valid dirs")
	}
}
