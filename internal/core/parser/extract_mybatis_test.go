package parser

import (
	"context"
	"testing"

	"github.com/liuymcn/flash-code-graph/internal/core/parser/java"
	"github.com/liuymcn/flash-code-graph/internal/core/scanner"
)

func TestExtractMybatisMapper_ValidMapper(t *testing.T) {
	xml := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE mapper PUBLIC "-//mybatis.org//DTD Mapper 3.0//EN" "http://mybatis.org/dtd/mybatis-3-mapper.dtd">
<mapper namespace="com.example.mapper.UserMapper">
    <select id="findById" resultType="com.example.model.User">
        SELECT id, name, email FROM users WHERE id = #{id}
    </select>
    <select id="findByEmail" resultType="com.example.model.User">
        SELECT * FROM users WHERE email = #{email}
    </select>
    <insert id="save">
        INSERT INTO users (name, email) VALUES (#{name}, #{email})
    </insert>
    <update id="updateName">
        UPDATE users SET name = #{name} WHERE id = #{id}
    </update>
    <delete id="deleteById">
        DELETE FROM users WHERE id = #{id}
    </delete>
</mapper>`)

	queries := java.ExtractMybatisMapper(xml, "src/main/resources/mapper/UserMapper.xml")

	if len(queries) != 5 {
		t.Fatalf("expected 5 queries, got %d", len(queries))
	}

	// Verify each query type
	typeCount := map[string]int{}
	for _, q := range queries {
		typeCount[q.QueryType]++
	}
	if typeCount["SELECT"] != 2 {
		t.Fatalf("expected 2 SELECT, got %d", typeCount["SELECT"])
	}
	if typeCount["INSERT"] != 1 {
		t.Fatalf("expected 1 INSERT, got %d", typeCount["INSERT"])
	}
	if typeCount["UPDATE"] != 1 {
		t.Fatalf("expected 1 UPDATE, got %d", typeCount["UPDATE"])
	}
	if typeCount["DELETE"] != 1 {
		t.Fatalf("expected 1 DELETE, got %d", typeCount["DELETE"])
	}

	// Verify CallerName format: namespace.id
	if queries[0].CallerName != "com.example.mapper.UserMapper.findById" {
		t.Fatalf("expected CallerName com.example.mapper.UserMapper.findById, got %s", queries[0].CallerName)
	}

	// Verify table extraction from SQL
	if len(queries[0].Tables) == 0 || queries[0].Tables[0] != "users" {
		t.Fatalf("expected table 'users', got %v", queries[0].Tables)
	}

	// Verify SQL is cleaned (no excessive whitespace)
	if queries[0].SQLText != "SELECT id, name, email FROM users WHERE id = #{id}" {
		t.Fatalf("SQL not cleaned: %q", queries[0].SQLText)
	}

	// Verify FilePath is preserved
	if queries[0].FilePath != "src/main/resources/mapper/UserMapper.xml" {
		t.Fatalf("expected relative FilePath, got %s", queries[0].FilePath)
	}

	t.Logf("✅ MyBatis mapper: 5 queries (2 SELECT, 1 INSERT, 1 UPDATE, 1 DELETE)")
}

func TestExtractMybatisMapper_NotAMapper(t *testing.T) {
	cases := []struct {
		name string
		xml  []byte
	}{
		{"pom.xml", []byte(`<project><groupId>com.example</groupId></project>`)},
		{"spring config", []byte(`<beans><bean id="ds" class="DataSource"/></beans>`)},
		{"empty namespace", []byte(`<mapper><select id="x">SELECT 1</select></mapper>`)},
		{"invalid xml", []byte(`not xml at all`)},
		{"logback", []byte(`<configuration><appender name="STDOUT"/></configuration>`)},
	}

	for _, tc := range cases {
		queries := java.ExtractMybatisMapper(tc.xml, tc.name)
		if len(queries) != 0 {
			t.Fatalf("%s: expected 0 queries for non-mapper XML, got %d", tc.name, len(queries))
		}
	}
	t.Log("✅ Non-mapper XML files correctly skipped (5 cases)")
}

func TestExtractMybatisMapper_ComplexSQL(t *testing.T) {
	xml := []byte(`<mapper namespace="com.example.OrderMapper">
    <select id="findWithJoin">
        SELECT o.id, o.total, u.name
        FROM orders o
        JOIN users u ON o.user_id = u.id
        WHERE o.status = #{status}
    </select>
</mapper>`)

	queries := java.ExtractMybatisMapper(xml, "mapper/OrderMapper.xml")

	if len(queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(queries))
	}

	// Should extract both tables from JOIN
	tables := queries[0].Tables
	if len(tables) < 2 {
		t.Fatalf("expected at least 2 tables (orders, users), got %v", tables)
	}

	hasOrders, hasUsers := false, false
	for _, table := range tables {
		if table == "orders" || table == "o" {
			hasOrders = true
		}
		if table == "users" || table == "u" {
			hasUsers = true
		}
	}
	if !hasOrders || !hasUsers {
		t.Fatalf("expected orders and users tables, got %v", tables)
	}

	t.Logf("✅ Complex SQL with JOIN: tables=%v", tables)
}

func TestParseFile_XML_MybatisMapper(t *testing.T) {
	p := New("")
	defer p.Close()

	xml := []byte(`<mapper namespace="com.example.UserMapper">
    <select id="findAll">SELECT * FROM users</select>
    <insert id="save">INSERT INTO users (name) VALUES (#{name})</insert>
</mapper>`)

	file := scanner.ScannedFile{
		Path:     "/project/src/main/resources/mapper/UserMapper.xml",
		RelPath:  "src/main/resources/mapper/UserMapper.xml",
		Language: "xml",
	}

	result, err := p.ParseFile(context.Background(), file, xml)
	if err != nil {
		t.Fatal("ParseFile XML:", err)
	}

	if result.FilePath != "src/main/resources/mapper/UserMapper.xml" {
		t.Fatalf("expected relative FilePath, got %s", result.FilePath)
	}
	if result.Language != "xml" {
		t.Fatalf("expected language xml, got %s", result.Language)
	}
	if len(result.Queries) != 2 {
		t.Fatalf("expected 2 queries from mapper, got %d", len(result.Queries))
	}
	if result.Queries[0].QueryType != "SELECT" {
		t.Fatalf("expected SELECT, got %s", result.Queries[0].QueryType)
	}
	if result.Queries[1].QueryType != "INSERT" {
		t.Fatalf("expected INSERT, got %s", result.Queries[1].QueryType)
	}

	t.Log("✅ ParseFile dispatches XML to MyBatis parser correctly")
}

func TestParseFile_XML_NonMapper(t *testing.T) {
	p := New("")
	defer p.Close()

	xml := []byte(`<project><groupId>com.example</groupId></project>`)

	file := scanner.ScannedFile{
		Path:     "/project/pom.xml",
		RelPath:  "pom.xml",
		Language: "xml",
	}

	result, err := p.ParseFile(context.Background(), file, xml)
	if err != nil {
		t.Fatal("ParseFile XML:", err)
	}

	if len(result.Queries) != 0 {
		t.Fatalf("expected 0 queries for non-mapper XML, got %d", len(result.Queries))
	}
	if len(result.Symbols) != 0 {
		t.Fatalf("expected 0 symbols for non-mapper XML, got %d", len(result.Symbols))
	}

	t.Log("✅ Non-mapper XML produces empty result")
}
