package framework

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetect_Maven_Spring(t *testing.T) {
	tempDir := t.TempDir()
	pomContent := `<project>
  <dependencies>
    <dependency>
      <groupId>org.springframework.boot</groupId>
      <artifactId>spring-boot-starter-web</artifactId>
    </dependency>
  </dependencies>
</project>`
	os.WriteFile(filepath.Join(tempDir, "pom.xml"), []byte(pomContent), 0o644)

	frameworks := Detect(tempDir, []string{"pom.xml"})
	if !HasFramework(frameworks, "spring") {
		t.Fatal("expected spring framework detected")
	}
	if frameworks[0].Category != "route" {
		t.Fatalf("expected category route, got %s", frameworks[0].Category)
	}
}

func TestDetect_Npm_Express(t *testing.T) {
	tempDir := t.TempDir()
	packageJSON := `{"dependencies": {"express": "^4.18.0", "mongoose": "^7.0.0"}}`
	os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(packageJSON), 0o644)

	frameworks := Detect(tempDir, []string{"package.json"})
	found := false
	for _, framework := range frameworks {
		if framework.Name == "express" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected express detected")
	}
	t.Log("✅ npm Express detection works")
}

func TestDetect_GoMod_Gin(t *testing.T) {
	tempDir := t.TempDir()
	goMod := `module myapp
go 1.22
require github.com/gin-gonic/gin v1.9.0`
	os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goMod), 0o644)

	frameworks := Detect(tempDir, []string{"go.mod"})
	if len(frameworks) == 0 {
		t.Fatal("expected gin detected")
	}
	if frameworks[0].Name != "gin" {
		t.Fatalf("expected gin, got %s", frameworks[0].Name)
	}
	t.Log("✅ Go Gin detection works")
}

func TestDetect_Python_Flask(t *testing.T) {
	tempDir := t.TempDir()
	requirements := `flask==3.0.0
sqlalchemy==2.0.0`
	os.WriteFile(filepath.Join(tempDir, "requirements.txt"), []byte(requirements), 0o644)

	frameworks := Detect(tempDir, []string{"requirements.txt"})
	if len(frameworks) < 2 {
		t.Fatalf("expected at least 2 frameworks, got %d", len(frameworks))
	}
	t.Log("✅ Python Flask+SQLAlchemy detection works")
}

func TestDetect_NoFramework(t *testing.T) {
	tempDir := t.TempDir()
	os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module myapp\ngo 1.22"), 0o644)

	frameworks := Detect(tempDir, []string{"go.mod"})
	if len(frameworks) != 0 {
		t.Fatalf("expected 0 frameworks, got %d", len(frameworks))
	}
}

func TestDetect_Maven_Feign_gRPC_Dubbo(t *testing.T) {
	tempDir := t.TempDir()
	pom := `<project><dependencies>
		<dependency><artifactId>spring-cloud-starter-openfeign</artifactId></dependency>
		<dependency><groupId>io.grpc</groupId><artifactId>grpc-stub</artifactId></dependency>
		<dependency><groupId>org.apache.dubbo</groupId><artifactId>dubbo-spring-boot-starter</artifactId></dependency>
		<dependency><artifactId>spring-graphql</artifactId></dependency>
	</dependencies></project>`
	os.WriteFile(filepath.Join(tempDir, "pom.xml"), []byte(pom), 0o644)

	frameworks := Detect(tempDir, []string{"pom.xml"})
	for _, name := range []string{"feign", "grpc", "dubbo", "graphql"} {
		if !HasFramework(frameworks, name) {
			t.Errorf("expected %s detected", name)
		}
	}
}

func TestDetect_GoMod_gRPC_GraphQL(t *testing.T) {
	tempDir := t.TempDir()
	goMod := `module myapp
go 1.22
require (
	google.golang.org/grpc v1.60.0
	github.com/99designs/gqlgen v0.17.0
)`
	os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goMod), 0o644)

	frameworks := Detect(tempDir, []string{"go.mod"})
	if !HasFramework(frameworks, "grpc") {
		t.Error("expected grpc detected")
	}
	if !HasFramework(frameworks, "graphql") {
		t.Error("expected graphql detected")
	}
}

func TestDetect_Npm_Axios_gRPC_GraphQL(t *testing.T) {
	tempDir := t.TempDir()
	pkg := `{"dependencies":{"axios":"^1.0","@grpc/grpc-js":"^1.9","@nestjs/graphql":"^12.0"}}`
	os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(pkg), 0o644)

	frameworks := Detect(tempDir, []string{"package.json"})
	for _, name := range []string{"axios", "grpc", "graphql"} {
		if !HasFramework(frameworks, name) {
			t.Errorf("expected %s detected", name)
		}
	}
}

func TestDetect_Python_Requests_gRPC_GraphQL(t *testing.T) {
	tempDir := t.TempDir()
	req := "requests==2.31.0\ngrpcio==1.60.0\nstrawberry-graphql==0.220.0\nhttpx==0.27.0"
	os.WriteFile(filepath.Join(tempDir, "requirements.txt"), []byte(req), 0o644)

	frameworks := Detect(tempDir, []string{"requirements.txt"})
	for _, name := range []string{"requests", "grpc", "graphql", "httpx"} {
		if !HasFramework(frameworks, name) {
			t.Errorf("expected %s detected", name)
		}
	}
}

func TestHasCategory(t *testing.T) {
	frameworks := []Framework{
		{Name: "spring", Category: "route"},
		{Name: "feign", Category: "http_client"},
		{Name: "grpc", Category: "rpc"},
	}
	if !HasCategory(frameworks, "rpc") {
		t.Error("expected rpc category found")
	}
	if HasCategory(frameworks, "graphql") {
		t.Error("expected graphql category not found")
	}
}
