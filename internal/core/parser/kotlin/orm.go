package kotlin

import (
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/sqlutil"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// roomQueryAnnotations are annotations that contain SQL text.
var roomQueryAnnotations = map[string]bool{
	"Query":    true,
	"RawQuery": true,
}

// roomDMLAnnotations maps DML annotations to their query type.
var roomDMLAnnotations = map[string]string{
	"Insert": "INSERT",
	"Upsert": "INSERT",
	"Update": "UPDATE",
	"Delete": "DELETE",
}

// ExtractRoomQueries extracts SQL queries from Room annotations.
func ExtractRoomQueries(annotations []model.StructuredAnnotation, functionName, className, filePath string, startLine int, result *model.ParseResult) {
	callerName := functionName
	if className != "" {
		callerName = className + "." + functionName
	}

	for _, annotation := range annotations {
		if roomQueryAnnotations[annotation.Name] {
			sqlText := annotation.Params["value"]
			if sqlText == "" {
				continue
			}
			queryType := sqlutil.DetectQueryType(sqlText)
			tables := sqlutil.ExtractTablesFromSQL(sqlText)
			result.Queries = append(result.Queries, model.RawQuery{
				SQLText:    sqlText,
				QueryType:  queryType,
				Tables:     tables,
				CallerName: callerName,
				FilePath:   filePath,
				Line:       startLine,
			})
		}

		if queryType, isDML := roomDMLAnnotations[annotation.Name]; isDML {
			result.Queries = append(result.Queries, model.RawQuery{
				SQLText:    queryType,
				QueryType:  queryType,
				CallerName: callerName,
				FilePath:   filePath,
				Line:       startLine,
			})
		}
	}
}
