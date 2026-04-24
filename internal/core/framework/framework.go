// Package framework detects project frameworks from build files.
package framework

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Framework represents a detected framework.
type Framework struct {
	Name     string `json:"name"`               // spring, express, gin, feign, grpc, graphql, dubbo, etc.
	Category string `json:"category,omitempty"`  // route, http_client, rpc, graphql, orm
	Version  string `json:"version,omitempty"`   // if detectable
}

// Detect scans build files to identify frameworks used in the project.
func Detect(rootPath string, buildFiles []string) []Framework {
	var frameworks []Framework
	seen := make(map[string]bool)

	for _, buildFile := range buildFiles {
		fullPath := filepath.Join(rootPath, buildFile)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		text := string(content)

		detected := detectFromFile(buildFile, text)
		for _, framework := range detected {
			if !seen[framework.Name] {
				seen[framework.Name] = true
				frameworks = append(frameworks, framework)
			}
		}
	}

	return frameworks
}

func detectFromFile(fileName, content string) []Framework {
	base := filepath.Base(fileName)
	switch {
	case base == "pom.xml":
		return matchPatterns(content, javaPatterns)
	case strings.HasPrefix(base, "build.gradle"):
		return matchPatterns(content, javaPatterns)
	case base == "package.json":
		return detectNpmFrameworks(content)
	case base == "go.mod":
		return detectGoFrameworks(content)
	case base == "requirements.txt" || base == "pyproject.toml":
		return detectPythonFrameworks(content)
	case base == "Gemfile":
		return detectRubyFrameworks(content)
	case base == "composer.json":
		return detectPHPFrameworks(content)
	default:
		return nil
	}
}

// javaPatterns is the shared keyword mapping for Maven and Gradle projects.
var javaPatterns = []struct {
	keyword  string
	name     string
	category string
}{
	{"spring-boot", "spring", "route"},
	{"spring-web", "spring", "route"},
	{"spring-webmvc", "spring", "route"},
	{"org.springframework", "spring", "route"},
	{"spring-data", "spring", "orm"},
	{"javax.ws.rs", "jaxrs", "route"},
	{"jakarta.ws.rs", "jaxrs", "route"},
	{"mybatis", "mybatis", "orm"},
	{"hibernate", "hibernate", "orm"},
	{"quarkus", "quarkus", "route"},
	{"io.quarkus", "quarkus", "route"},
	{"micronaut", "micronaut", "route"},
	{"io.micronaut", "micronaut", "route"},
	{"com.android", "android", ""},
	{"openfeign", "feign", "http_client"},
	{"grpc-java", "grpc", "rpc"},
	{"io.grpc", "grpc", "rpc"},
	{"spring-graphql", "graphql", "graphql"},
	{"graphql-java", "graphql", "graphql"},
	{"dubbo", "dubbo", "rpc"},
	{"org.apache.dubbo", "dubbo", "rpc"},
	{"xxl-job", "xxl-job", "schedule"},
	{"xuxueli", "xxl-job", "schedule"},
	{"springfox", "swagger2", "doc"},
	{"springdoc", "swagger3", "doc"},
	{"swagger", "swagger2", "doc"},
	{"rocketmq", "rocketmq", "mq"},
}

func matchPatterns(content string, patterns []struct{ keyword, name, category string }) []Framework {
	var frameworks []Framework
	seen := make(map[string]bool)
	for _, p := range patterns {
		if !seen[p.name] && strings.Contains(content, p.keyword) {
			seen[p.name] = true
			frameworks = append(frameworks, Framework{Name: p.name, Category: p.category})
		}
	}
	return frameworks
}

func detectMavenFrameworks(content string) []Framework {
	return matchPatterns(content, javaPatterns)
}

func detectGradleFrameworks(content string) []Framework {
	return matchPatterns(content, javaPatterns)
}

func detectNpmFrameworks(content string) []Framework {
	var packageJSON struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal([]byte(content), &packageJSON); err != nil {
		return nil
	}

	allDeps := make(map[string]string)
	for key, value := range packageJSON.Dependencies {
		allDeps[key] = value
	}
	for key, value := range packageJSON.DevDependencies {
		allDeps[key] = value
	}

	type depPattern struct {
		dep      string
		name     string
		category string
	}
	patterns := []depPattern{
		{"express", "express", "route"},
		{"koa", "koa", "route"},
		{"fastify", "fastify", "route"},
		{"hono", "hono", "route"},
		{"next", "nextjs", "route"},
		{"nuxt", "nuxt", "route"},
		{"@nestjs/core", "nestjs", "route"},
		{"react", "react", ""},
		{"vue", "vue", ""},
		{"angular", "angular", ""},
		{"@angular/core", "angular", ""},
		{"axios", "axios", "http_client"},
		{"@grpc/grpc-js", "grpc", "rpc"},
		{"@nestjs/graphql", "graphql", "graphql"},
		{"apollo-server", "graphql", "graphql"},
		{"@apollo/client", "graphql", "graphql"},
		{"@apollo/server", "graphql", "graphql"},
	}

	var frameworks []Framework
	seen := make(map[string]bool)
	for _, p := range patterns {
		if _, exists := allDeps[p.dep]; exists && !seen[p.name] {
			seen[p.name] = true
			frameworks = append(frameworks, Framework{Name: p.name, Category: p.category})
		}
	}
	return frameworks
}

func detectGoFrameworks(content string) []Framework {
	var frameworks []Framework
	patterns := []struct {
		keyword  string
		name     string
		category string
	}{
		{"github.com/gin-gonic/gin", "gin", "route"},
		{"github.com/labstack/echo", "echo", "route"},
		{"github.com/gofiber/fiber", "fiber", "route"},
		{"google.golang.org/grpc", "grpc", "rpc"},
		{"github.com/gorilla/mux", "gorilla", "route"},
		{"gorm.io/gorm", "gorm", "orm"},
		{"github.com/beego/beego", "beego", "route"},
		{"github.com/99designs/gqlgen", "graphql", "graphql"},
		{"github.com/graphql-go/graphql", "graphql", "graphql"},
	}
	seen := make(map[string]bool)
	for _, p := range patterns {
		if !seen[p.name] && strings.Contains(content, p.keyword) {
			seen[p.name] = true
			frameworks = append(frameworks, Framework{Name: p.name, Category: p.category})
		}
	}
	return frameworks
}

func detectPythonFrameworks(content string) []Framework {
	var frameworks []Framework
	patterns := []struct {
		keyword  string
		name     string
		category string
	}{
		{"flask", "flask", "route"},
		{"Flask", "flask", "route"},
		{"django", "django", "route"},
		{"Django", "django", "route"},
		{"fastapi", "fastapi", "route"},
		{"FastAPI", "fastapi", "route"},
		{"sqlalchemy", "sqlalchemy", "orm"},
		{"SQLAlchemy", "sqlalchemy", "orm"},
		{"celery", "celery", ""},
		{"tornado", "tornado", "route"},
		{"requests", "requests", "http_client"},
		{"httpx", "httpx", "http_client"},
		{"grpcio", "grpc", "rpc"},
		{"strawberry-graphql", "graphql", "graphql"},
	}
	seen := make(map[string]bool)
	for _, p := range patterns {
		if !seen[p.name] && strings.Contains(content, p.keyword) {
			seen[p.name] = true
			frameworks = append(frameworks, Framework{Name: p.name, Category: p.category})
		}
	}
	return frameworks
}

// HasFramework checks if a named framework was detected.
func HasFramework(frameworks []Framework, name string) bool {
	for _, f := range frameworks {
		if f.Name == name {
			return true
		}
	}
	return false
}

// HasCategory checks if any framework with the given category was detected.
func HasCategory(frameworks []Framework, category string) bool {
	for _, f := range frameworks {
		if f.Category == category {
			return true
		}
	}
	return false
}

func detectRubyFrameworks(content string) []Framework {
	var frameworks []Framework
	if strings.Contains(content, "rails") || strings.Contains(content, "Rails") {
		frameworks = append(frameworks, Framework{Name: "rails"})
	}
	if strings.Contains(content, "sinatra") {
		frameworks = append(frameworks, Framework{Name: "sinatra"})
	}
	return frameworks
}

func detectPHPFrameworks(content string) []Framework {
	var frameworks []Framework
	patterns := map[string]string{
		"laravel/framework": "laravel",
		"symfony/symfony":   "symfony",
		"slim/slim":         "slim",
	}
	for pattern, name := range patterns {
		if strings.Contains(content, pattern) {
			frameworks = append(frameworks, Framework{Name: name})
		}
	}
	return frameworks
}
