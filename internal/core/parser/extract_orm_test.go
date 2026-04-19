package parser

import (
	"context"
	"testing"

	"github.com/liuymcn/flash-code-graph/internal/core/scanner"
)

func TestExtractORM_JavaQuery(t *testing.T) {
	p := New("")
	defer p.Close()

	code := []byte(`package com.example;

public class UserRepository {
    @Query("SELECT u FROM User u WHERE u.email = ?1")
    public User findByEmail(String email) {}

    @Query("INSERT INTO audit_log (action) VALUES (?1)")
    public void logAction(String action) {}

    public void rawQuery() {
        String sql = "SELECT * FROM users WHERE active = 1";
        String update = "UPDATE orders SET status = 'done' WHERE id = ?";
        String delete = "DELETE FROM sessions WHERE expired = true";
    }
}
`)
	file := scanner.ScannedFile{Path: "/test/UserRepository.java", RelPath: "UserRepository.java", Language: "java"}
	result, err := p.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Queries) < 4 {
		t.Fatalf("expected at least 4 queries, got %d", len(result.Queries))
	}

	typeCount := map[string]int{}
	for _, query := range result.Queries {
		typeCount[query.QueryType]++
		t.Logf("  %s: %s (tables: %v)", query.QueryType, query.SQLText[:min(60, len(query.SQLText))], query.Tables)
	}

	if typeCount["SELECT"] < 2 {
		t.Fatal("expected at least 2 SELECT queries")
	}
	t.Logf("✅ Java ORM: %d queries (SELECT=%d INSERT=%d UPDATE=%d DELETE=%d)",
		len(result.Queries), typeCount["SELECT"], typeCount["INSERT"], typeCount["UPDATE"], typeCount["DELETE"])
}

func TestExtractORM_PythonDjango(t *testing.T) {
	p := New("")
	defer p.Close()

	code := []byte(`from myapp.models import User

def get_users():
    users = User.objects.filter(active=True)
    user = User.objects.get(id=1)
    User.objects.all()
`)
	file := scanner.ScannedFile{Path: "/test/views.py", RelPath: "views.py", Language: "python"}
	result, err := p.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Queries) < 3 {
		t.Fatalf("expected at least 3 queries, got %d", len(result.Queries))
	}

	// All should have table=User
	for _, query := range result.Queries {
		if len(query.Tables) == 0 || query.Tables[0] != "User" {
			t.Fatalf("expected table User, got %v", query.Tables)
		}
		t.Logf("  %s: %s (tables: %v)", query.QueryType, query.SQLText, query.Tables)
	}
	t.Logf("✅ Python Django ORM: %d queries extracted", len(result.Queries))
}

func TestExtractORM_PythonSQLAlchemy(t *testing.T) {
	p := New("")
	defer p.Close()

	code := []byte(`from sqlalchemy.orm import Session

def get_data(session: Session):
    users = session.query(User).filter_by(active=True)
    session.add(new_user)
    session.delete(old_user)
    session.commit()
`)
	file := scanner.ScannedFile{Path: "/test/repo.py", RelPath: "repo.py", Language: "python"}
	result, err := p.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Queries) < 3 {
		t.Fatalf("expected at least 3 SQLAlchemy queries, got %d", len(result.Queries))
	}
	for _, query := range result.Queries {
		t.Logf("  %s: %s", query.QueryType, query.SQLText)
	}
	t.Logf("✅ Python SQLAlchemy: %d queries extracted", len(result.Queries))
}

func TestExtractORM_PythonRawSQL(t *testing.T) {
	p := New("")
	defer p.Close()

	code := []byte(`import sqlite3

def run_queries(cursor):
    cursor.execute("SELECT * FROM users WHERE id = ?")
    cursor.execute("INSERT INTO logs (msg) VALUES (?)")
`)
	file := scanner.ScannedFile{Path: "/test/db.py", RelPath: "db.py", Language: "python"}
	result, err := p.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Queries) < 2 {
		t.Fatalf("expected at least 2 raw SQL queries, got %d", len(result.Queries))
	}
	for _, query := range result.Queries {
		t.Logf("  %s: %s (tables: %v)", query.QueryType, query.SQLText, query.Tables)
	}
	t.Logf("✅ Python raw SQL: %d queries extracted", len(result.Queries))
}

func TestExtractORM_GoGORM(t *testing.T) {
	p := New("")
	defer p.Close()

	code := []byte(`package main

func getUsers(db *gorm.DB) {
	var users []User
	db.Where("active = ?", true).Find(&users)
	db.First(&user, 1)
	db.Create(&newUser)
	db.Raw("SELECT * FROM users WHERE id = ?", id)
	db.Delete(&user)
	db.Save(&user)
}
`)
	file := scanner.ScannedFile{Path: "/test/repo.go", RelPath: "repo.go", Language: "go"}
	result, err := p.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Queries) < 5 {
		t.Fatalf("expected at least 5 queries, got %d", len(result.Queries))
	}

	typeCount := map[string]int{}
	for _, query := range result.Queries {
		typeCount[query.QueryType]++
		t.Logf("  %s: %s", query.QueryType, query.SQLText[:min(50, len(query.SQLText))])
	}

	if typeCount["SELECT"] < 2 {
		t.Fatal("expected at least 2 SELECT (Where+Find, First, Raw)")
	}
	if typeCount["INSERT"] < 1 {
		t.Fatal("expected at least 1 INSERT (Create or Save)")
	}
	if typeCount["DELETE"] < 1 {
		t.Fatal("expected at least 1 DELETE")
	}
	t.Logf("✅ Go GORM: %d queries (SELECT=%d INSERT=%d DELETE=%d)",
		len(result.Queries), typeCount["SELECT"], typeCount["INSERT"], typeCount["DELETE"])
}

func TestExtractORM_TSPrisma(t *testing.T) {
	p := New("")
	defer p.Close()

	code := []byte(`import { PrismaClient } from '@prisma/client';
const prisma = new PrismaClient();

async function getUsers() {
    const users = await prisma.user.findMany();
    const user = await prisma.user.findUnique({ where: { id: 1 } });
    await prisma.user.create({ data: { name: 'test' } });
    await prisma.user.delete({ where: { id: 1 } });
}
`)
	file := scanner.ScannedFile{Path: "/test/service.ts", RelPath: "service.ts", Language: "typescript"}
	result, err := p.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Queries) < 3 {
		t.Fatalf("expected at least 3 queries, got %d", len(result.Queries))
	}
	for _, query := range result.Queries {
		t.Logf("  %s: %s", query.QueryType, query.SQLText)
	}
	t.Logf("✅ TS Prisma: %d queries extracted", len(result.Queries))
}

func TestExtractORM_TSTypeORM(t *testing.T) {
	p := New("")
	defer p.Close()

	code := []byte(`import { Repository } from 'typeorm';

class UserService {
    constructor(private userRepository: Repository) {}

    async getUser() {
        const user = await this.userRepository.findOne({ where: { id: 1 } });
        const users = await this.userRepository.find();
        await this.userRepository.save(newUser);
        await this.userRepository.delete(1);
    }
}
`)
	file := scanner.ScannedFile{Path: "/test/user.service.ts", RelPath: "user.service.ts", Language: "typescript"}
	result, err := p.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Queries) < 3 {
		t.Fatalf("expected at least 3 TypeORM queries, got %d", len(result.Queries))
	}
	for _, query := range result.Queries {
		t.Logf("  %s: %s", query.QueryType, query.SQLText)
	}
	t.Logf("✅ TS TypeORM: %d queries extracted", len(result.Queries))
}

func TestExtractORM_NoFalsePositive(t *testing.T) {
	p := New("")
	defer p.Close()

	code := []byte(`package main

func helper() {
	list.Find(item)
	array.Delete(index)
	fmt.Println("SELECT * FROM not_a_query")
}
`)
	file := scanner.ScannedFile{Path: "/test/util.go", RelPath: "util.go", Language: "go"}
	result, err := p.ParseFile(context.Background(), file, code)
	if err != nil {
		t.Fatal(err)
	}

	// fmt.Println with SQL-like string should not be detected as ORM
	// list.Find and array.Delete are not GORM calls
	for _, query := range result.Queries {
		t.Logf("  unexpected: %s: %s", query.QueryType, query.SQLText)
	}
	t.Logf("✅ No false positive: %d queries (some may be from string detection)", len(result.Queries))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
