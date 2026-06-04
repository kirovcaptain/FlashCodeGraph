package parser

import (
	"context"
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
)

func TestExtractJavaRoutes_Spring(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`package com.example;

@RestController
@RequestMapping("/api/users")
public class UserController {

    @GetMapping("/{id}")
    public User getById(Long id) {
        return service.findById(id);
    }

    @PostMapping
    public User create(User user) {
        return service.save(user);
    }

    @DeleteMapping("/{id}")
    public void delete(Long id) {
        service.delete(id);
    }
}
`)
	file := scanner.ScannedFile{Path: "/test/UserController.java", RelPath: "UserController.java", Language: "java"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	if len(result.Routes) == 0 {
		t.Fatal("expected routes extracted")
	}

	routeMap := make(map[string]string) // method → path
	for _, route := range result.Routes {
		routeMap[route.Method] = route.PathPattern
		if route.Framework != "spring" {
			t.Fatalf("expected spring framework, got %s", route.Framework)
		}
	}

	if routeMap["GET"] != "/api/users/{id}" && routeMap["GET"] != "/{id}" {
		t.Logf("GET route: %s", routeMap["GET"])
	}

	t.Logf("✅ Java Spring routes: %d extracted", len(result.Routes))
	for _, route := range result.Routes {
		t.Logf("  %s %s → %s", route.Method, route.PathPattern, route.Handlers[len(route.Handlers)-1])
	}
}

func TestExtractPythonRoutes_Flask(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`from flask import Flask
app = Flask(__name__)

@app.route("/users")
def list_users():
    return []

@app.get("/users/<id>")
def get_user(id):
    return {}

@app.post("/users")
def create_user():
    return {}
`)
	file := scanner.ScannedFile{Path: "/test/app.py", RelPath: "app.py", Language: "python"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	if len(result.Routes) != 3 {
		t.Fatalf("expected 3 routes, got %d", len(result.Routes))
	}

	for _, route := range result.Routes {
		if route.Framework != "flask" {
			t.Fatalf("expected flask, got %s", route.Framework)
		}
	}

	t.Logf("✅ Python Flask routes: %d extracted", len(result.Routes))
	for _, route := range result.Routes {
		t.Logf("  %s %s → %s", route.Method, route.PathPattern, route.Handlers[len(route.Handlers)-1])
	}
}

func TestExtractGoRoutes_Gin(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`package main

import "github.com/gin-gonic/gin"

func setupRoutes(router *gin.Engine) {
	router.GET("/users", listUsers)
	router.POST("/users", createUser)
	router.DELETE("/users/:id", deleteUser)
}

func listUsers(c *gin.Context) {}
func createUser(c *gin.Context) {}
func deleteUser(c *gin.Context) {}
`)
	file := scanner.ScannedFile{Path: "/test/routes.go", RelPath: "routes.go", Language: "go"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	if len(result.Routes) != 3 {
		t.Fatalf("expected 3 routes, got %d", len(result.Routes))
	}

	t.Logf("✅ Go Gin routes: %d extracted", len(result.Routes))
	for _, route := range result.Routes {
		t.Logf("  %s %s → %s", route.Method, route.PathPattern, route.Handlers[len(route.Handlers)-1])
	}
}

func TestExtractTSRoutes_Express(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`import express from 'express';
const app = express();

function setupRoutes() {
    app.get('/users', listUsers);
    app.post('/users', createUser);
    app.delete('/users/:id', deleteUser);
}
`)
	file := scanner.ScannedFile{Path: "/test/routes.ts", RelPath: "routes.ts", Language: "typescript"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	if len(result.Routes) != 3 {
		t.Fatalf("expected 3 routes, got %d", len(result.Routes))
	}

	for _, route := range result.Routes {
		if route.Framework != "express" {
			t.Fatalf("expected express, got %s", route.Framework)
		}
	}

	t.Logf("✅ TS Express routes: %d extracted", len(result.Routes))
	for _, route := range result.Routes {
		t.Logf("  %s %s → %s", route.Method, route.PathPattern, route.Handlers[len(route.Handlers)-1])
	}
}

func TestExtractRoutes_NoRoutes(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`package main
func helper() { println("no routes here") }
`)
	file := scanner.ScannedFile{Path: "/test/util.go", RelPath: "util.go", Language: "go"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	if len(result.Routes) != 0 {
		t.Fatalf("expected 0 routes, got %d", len(result.Routes))
	}
	t.Log("✅ No false positive routes")
}


func TestExtractJavaRoutes_RequestMappingWithMethod(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`package com.example;

@RestController
@RequestMapping("/api")
public class ApiController {
    @RequestMapping(method = RequestMethod.POST, value = "/users")
    public User create(User user) {}

    @RequestMapping(value = "/users", method = RequestMethod.GET)
    public List<User> list() {}
}
`)
	file := scanner.ScannedFile{Path: "/test/ApiController.java", RelPath: "ApiController.java", Language: "java"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	if len(result.Routes) < 2 {
		t.Fatalf("expected at least 2 routes, got %d", len(result.Routes))
	}

	for _, route := range result.Routes {
		t.Logf("  %s %s → %s", route.Method, route.PathPattern, route.Handlers[len(route.Handlers)-1])
	}

	// Check POST method was extracted
	foundPost := false
	for _, route := range result.Routes {
		if route.Method == "POST" {
			foundPost = true
		}
	}
	if !foundPost {
		t.Fatal("expected POST method from @RequestMapping(method=RequestMethod.POST)")
	}
	t.Log("✅ Java @RequestMapping with explicit method works")
}

func TestExtractJavaRoutes_MultiMethod(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`package com.example;

@RestController
@RequestMapping("/api")
public class ApiController {
    @RequestMapping(method = {RequestMethod.GET, RequestMethod.POST}, value = "/users")
    public Object handleUsers() {}
}
`)
	file := scanner.ScannedFile{Path: "/test/ApiController.java", RelPath: "ApiController.java", Language: "java"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	if len(result.Routes) != 2 {
		t.Fatalf("expected 2 routes for multi-method, got %d", len(result.Routes))
	}

	methodSet := map[string]bool{}
	for _, route := range result.Routes {
		methodSet[route.Method] = true
		t.Logf("  %s %s → %s", route.Method, route.PathPattern, route.Handlers[0])
	}
	if !methodSet["GET"] || !methodSet["POST"] {
		t.Fatal("expected both GET and POST methods")
	}
	t.Log("✅ Java @RequestMapping multi-method works")
}

func TestExtractJavaRoutes_MultiPath(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`package com.example;

@RestController
public class ApiController {
    @RequestMapping(value = {"/api/foo", "/api/bar"}, method = RequestMethod.GET)
    public Object handle() {}
}
`)
	file := scanner.ScannedFile{Path: "/test/ApiController.java", RelPath: "ApiController.java", Language: "java"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	if len(result.Routes) != 2 {
		t.Fatalf("expected 2 routes for multi-path, got %d", len(result.Routes))
	}

	pathSet := map[string]bool{}
	for _, route := range result.Routes {
		pathSet[route.PathPattern] = true
		t.Logf("  %s %s → %s", route.Method, route.PathPattern, route.Handlers[0])
	}
	if !pathSet["/api/foo"] || !pathSet["/api/bar"] {
		t.Fatal("expected both /api/foo and /api/bar paths")
	}
	t.Log("✅ Java @RequestMapping multi-path works")
}

func TestExtractJavaRoutes_MultiMethodMultiPath(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`package com.example;

@RestController
public class ApiController {
    @RequestMapping(method = {RequestMethod.GET, RequestMethod.POST}, value = {"/api/foo", "/api/bar"})
    public Object handle() {}
}
`)
	file := scanner.ScannedFile{Path: "/test/ApiController.java", RelPath: "ApiController.java", Language: "java"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	if len(result.Routes) != 4 {
		t.Fatalf("expected 4 routes (2 methods × 2 paths), got %d", len(result.Routes))
	}

	for _, route := range result.Routes {
		t.Logf("  %s %s → %s", route.Method, route.PathPattern, route.Handlers[0])
	}
	t.Log("✅ Java @RequestMapping multi-method × multi-path cartesian product works")
}

func TestExtractJavaRoutes_ClassPrefixCombination(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`package com.example;

@RestController
@RequestMapping("/api/users")
public class UserController {
    @GetMapping("/{id}")
    public User getById(Long id) {}
}
`)
	file := scanner.ScannedFile{Path: "/test/UserController.java", RelPath: "UserController.java", Language: "java"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	if len(result.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(result.Routes))
	}

	route := result.Routes[0]
	if route.PathPattern != "/api/users/{id}" {
		t.Fatalf("expected /api/users/{id}, got %s", route.PathPattern)
	}
	t.Logf("✅ Class prefix + method path: %s %s", route.Method, route.PathPattern)
}

func TestExtractPythonRoutes_FastAPI(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`from fastapi import APIRouter
router = APIRouter()

@router.get("/users/{user_id}")
def get_user(user_id: int):
    return {}

@router.post("/users")
def create_user():
    return {}
`)
	file := scanner.ScannedFile{Path: "/test/routes.py", RelPath: "routes.py", Language: "python"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	if len(result.Routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(result.Routes))
	}

	for _, route := range result.Routes {
		if route.Framework != "fastapi" {
			t.Fatalf("expected fastapi, got %s", route.Framework)
		}
	}
	t.Logf("✅ Python FastAPI routes: %d extracted", len(result.Routes))
}

func TestExtractPythonRoutes_Django(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`from django.urls import path
from . import views

def setup_urls():
    urlpatterns = [
        path('users/', views.list_users),
        path('users/<int:pk>/', views.get_user),
    ]
`)
	file := scanner.ScannedFile{Path: "/test/urls.py", RelPath: "urls.py", Language: "python"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	if len(result.Routes) != 2 {
		t.Fatalf("expected 2 Django routes, got %d", len(result.Routes))
	}

	for _, route := range result.Routes {
		if route.Framework != "django" {
			t.Fatalf("expected django, got %s", route.Framework)
		}
		t.Logf("  %s %s → %s", route.Method, route.PathPattern, route.Handlers[len(route.Handlers)-1])
	}
	t.Log("✅ Python Django URL patterns extracted")
}

func TestExtractGoRoutes_Echo(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`package main

import "github.com/labstack/echo/v4"

func setupRoutes(e *echo.Echo) {
	e.GET("/users", listUsers)
	e.POST("/users", createUser)
}
`)
	file := scanner.ScannedFile{Path: "/test/routes.go", RelPath: "routes.go", Language: "go"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	if len(result.Routes) != 2 {
		t.Fatalf("expected 2 Echo routes, got %d", len(result.Routes))
	}
	t.Logf("✅ Go Echo routes: %d extracted", len(result.Routes))
}

func TestExtractTSRoutes_NestJS(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`import { Controller, Get, Post } from '@nestjs/common';

@Controller('users')
export class UserController {
    @Get(':id')
    findOne(id: string) {}

    @Post()
    create() {}
}
`)
	file := scanner.ScannedFile{Path: "/test/user.controller.ts", RelPath: "user.controller.ts", Language: "typescript"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	t.Logf("Routes found: %d", len(result.Routes))
	for _, route := range result.Routes {
		t.Logf("  %s %s → %s (%s)", route.Method, route.PathPattern, route.Handlers[len(route.Handlers)-1], route.Framework)
	}

	if len(result.Routes) < 1 {
		t.Fatal("expected at least 1 NestJS route")
	}
	t.Log("✅ TS NestJS decorator routes extracted")
}

func TestExtractTSRoutes_Hono(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`import { Hono } from 'hono';
const app = new Hono();

function setup() {
    app.get('/users', (c) => c.json([]));
    app.post('/users', (c) => c.json({}));
}
`)
	file := scanner.ScannedFile{Path: "/test/app.ts", RelPath: "app.ts", Language: "typescript"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	if len(result.Routes) != 2 {
		t.Fatalf("expected 2 Hono routes, got %d", len(result.Routes))
	}
	t.Logf("✅ TS Hono routes: %d extracted", len(result.Routes))
}

func TestExtractGoRoutes_GroupPrefix(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`package main

import "github.com/gin-gonic/gin"

func setupRoutes(router *gin.Engine) {
	v1 := router.Group("/api/v1")
	v1.GET("/users", listUsers)
	v1.POST("/users", createUser)

	admin := v1.Group("/admin")
	admin.GET("/stats", getStats)
	admin.DELETE("/users/:id", deleteUser)
}
`)
	file := scanner.ScannedFile{Path: "/test/routes.go", RelPath: "routes.go", Language: "go"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	if len(result.Routes) != 4 {
		t.Fatalf("expected 4 routes, got %d", len(result.Routes))
	}

	routeMap := make(map[string]string)
	for _, route := range result.Routes {
		key := route.Method + " " + route.PathPattern
		routeMap[key] = route.Handlers[len(route.Handlers)-1]
		t.Logf("  %s %s → %s", route.Method, route.PathPattern, route.Handlers[len(route.Handlers)-1])
	}

	// v1.GET("/users") → /api/v1/users
	if _, exists := routeMap["GET /api/v1/users"]; !exists {
		t.Fatal("missing GET /api/v1/users")
	}
	// admin.GET("/stats") → /api/v1/admin/stats
	if _, exists := routeMap["GET /api/v1/admin/stats"]; !exists {
		t.Fatal("missing GET /api/v1/admin/stats")
	}
	// admin.DELETE("/users/:id") → /api/v1/admin/users/:id
	if _, exists := routeMap["DELETE /api/v1/admin/users/:id"]; !exists {
		t.Fatal("missing DELETE /api/v1/admin/users/:id")
	}

	t.Log("✅ Go route group prefix chaining works")
}

func TestExtractTSRoutes_ChainedRoute(t *testing.T) {
	parser := New("")
	defer parser.Close()

	code := []byte(`import express from 'express';
const router = express.Router();

function setup() {
    router.route('/users')
        .get(listUsers)
        .post(createUser);

    router.route('/users/:id')
        .get(getUser)
        .put(updateUser)
        .delete(deleteUser);
}
`)
	file := scanner.ScannedFile{Path: "/test/routes.ts", RelPath: "routes.ts", Language: "typescript"}
	result, err := parser.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal("parse:", err)
	}

	// Should extract 5 routes from 2 chains
	chainRoutes := 0
	for _, route := range result.Routes {
		t.Logf("  %s %s → %s", route.Method, route.PathPattern, route.Handlers[len(route.Handlers)-1])
		if route.PathPattern == "/users" || route.PathPattern == "/users/:id" {
			chainRoutes++
		}
	}

	if chainRoutes < 5 {
		t.Fatalf("expected at least 5 chained routes, got %d", chainRoutes)
	}
	t.Logf("✅ TS chained routes: %d extracted", chainRoutes)
}
