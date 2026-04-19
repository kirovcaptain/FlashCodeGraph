package java

import (
	"encoding/xml"
	"strings"

	"github.com/liuymcn/flash-code-graph/internal/core/parser/sqlutil"
	"github.com/liuymcn/flash-code-graph/internal/model"
)

// mybatisMapper represents the root <mapper> element in a MyBatis XML file.
type mybatisMapper struct {
	XMLName   xml.Name       `xml:"mapper"`
	Namespace string         `xml:"namespace,attr"`
	Selects   []mybatisQuery `xml:"select"`
	Inserts   []mybatisQuery `xml:"insert"`
	Updates   []mybatisQuery `xml:"update"`
	Deletes   []mybatisQuery `xml:"delete"`
}

// mybatisQuery represents a single SQL statement element (<select>, <insert>, etc).
type mybatisQuery struct {
	ID  string `xml:"id,attr"`
	SQL string `xml:",chardata"`
}

// ExtractMybatisMapper parses a MyBatis XML mapper file and extracts SQL queries.
// Returns nil if the file is not a valid MyBatis mapper (wrong root element or no namespace).
func ExtractMybatisMapper(content []byte, filePath string) []model.RawQuery {
	var mapper mybatisMapper
	if err := xml.Unmarshal(content, &mapper); err != nil {
		return nil
	}
	if mapper.Namespace == "" {
		return nil
	}

	var queries []model.RawQuery

	for _, query := range mapper.Selects {
		queries = append(queries, buildMybatisQuery(mapper.Namespace, query, "SELECT", filePath))
	}
	for _, query := range mapper.Inserts {
		queries = append(queries, buildMybatisQuery(mapper.Namespace, query, "INSERT", filePath))
	}
	for _, query := range mapper.Updates {
		queries = append(queries, buildMybatisQuery(mapper.Namespace, query, "UPDATE", filePath))
	}
	for _, query := range mapper.Deletes {
		queries = append(queries, buildMybatisQuery(mapper.Namespace, query, "DELETE", filePath))
	}

	return queries
}

func buildMybatisQuery(namespace string, query mybatisQuery, queryType, filePath string) model.RawQuery {
	sqlText := cleanSQL(query.SQL)
	tables := sqlutil.ExtractTablesFromSQL(sqlText)

	// CallerName = namespace.id (e.g., "com.example.mapper.UserMapper.findById")
	callerName := namespace + "." + query.ID

	return model.RawQuery{
		SQLText:    sqlText,
		QueryType:  queryType,
		Tables:     tables,
		CallerName: callerName,
		FilePath:   filePath,
		Line:       0, // XML parsing doesn't provide line numbers
	}
}

// cleanSQL removes excessive whitespace and MyBatis parameter placeholders from SQL.
func cleanSQL(sql string) string {
	sql = strings.TrimSpace(sql)
	// Collapse multiple whitespace/newlines into single space
	fields := strings.Fields(sql)
	return strings.Join(fields, " ")
}
