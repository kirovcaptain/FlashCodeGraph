package sqlutil

import (
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// SQLVariable tracks a SQL string variable's accumulated fragments and metadata.
type SQLVariable struct {
	Fragments  []string
	Line       int
	Conditions []model.ConditionalFragment
	BaseSQL    string
}

// ScopeEnvironment tracks SQL variables within a scope, supporting parent-child nesting.
type ScopeEnvironment struct {
	Variables map[string]*SQLVariable
	Parent    *ScopeEnvironment
}

// NewScopeEnvironment creates a new scope, optionally chained to a parent.
func NewScopeEnvironment(parent *ScopeEnvironment) *ScopeEnvironment {
	return &ScopeEnvironment{
		Variables: make(map[string]*SQLVariable),
		Parent:    parent,
	}
}

// Lookup finds a variable by name, walking up the scope chain.
func (scope *ScopeEnvironment) Lookup(name string) *SQLVariable {
	if variable, exists := scope.Variables[name]; exists {
		return variable
	}
	if scope.Parent != nil {
		return scope.Parent.Lookup(name)
	}
	return nil
}

// Set stores a variable, updating the nearest scope that already contains it.
func (scope *ScopeEnvironment) Set(name string, variable *SQLVariable) {
	current := scope
	for current != nil {
		if _, exists := current.Variables[name]; exists {
			current.Variables[name] = variable
			return
		}
		current = current.Parent
	}
	scope.Variables[name] = variable
}

// Snapshot returns a deep copy of all variable fragments visible from this scope.
func (scope *ScopeEnvironment) Snapshot() map[string][]string {
	result := make(map[string][]string)
	current := scope
	for current != nil {
		for name, variable := range current.Variables {
			if _, exists := result[name]; !exists {
				fragmentsCopy := make([]string, len(variable.Fragments))
				copy(fragmentsCopy, variable.Fragments)
				result[name] = fragmentsCopy
			}
		}
		current = current.Parent
	}
	return result
}

// RecordConditionalDiff detects fragments added in a branch and records them as conditions.
func RecordConditionalDiff(scope *ScopeEnvironment, snapshotBefore map[string][]string, condition string, isElse bool) {
	current := scope
	for current != nil {
		for variableName, variable := range current.Variables {
			beforeFragments, existed := snapshotBefore[variableName]
			if !existed {
				continue
			}
			if len(variable.Fragments) > len(beforeFragments) {
				addedFragments := variable.Fragments[len(beforeFragments):]
				for _, fragment := range addedFragments {
					variable.Conditions = append(variable.Conditions, model.ConditionalFragment{
						Condition: condition,
						Fragment:  fragment,
						IsElse:   isElse,
					})
				}
				if variable.BaseSQL == "" {
					variable.BaseSQL = strings.Join(beforeFragments, "")
				}
			}
		}
		current = current.Parent
	}
}

// RestoreSnapshot restores variable fragments to a previous state.
func RestoreSnapshot(scope *ScopeEnvironment, snapshotBefore map[string][]string) {
	current := scope
	for current != nil {
		for variableName, variable := range current.Variables {
			if beforeFragments, existed := snapshotBefore[variableName]; existed {
				restoredFragments := make([]string, len(beforeFragments))
				copy(restoredFragments, beforeFragments)
				variable.Fragments = restoredFragments
			}
		}
		current = current.Parent
	}
}

// EmitQueriesFromScope emits RawQuery entries from all tracked variables in scope.
func EmitQueriesFromScope(scope *ScopeEnvironment, callerName, filePath string, result *model.ParseResult) {
	current := scope
	for current != nil {
		for _, variable := range current.Variables {
			fullSQL := strings.Join(variable.Fragments, "")
			if len(variable.Conditions) > 0 {
				for _, condition := range variable.Conditions {
					fullSQL += condition.Fragment
				}
			}
			if !IsSQLStatement(fullSQL) {
				continue
			}
			query := model.RawQuery{
				SQLText:    fullSQL,
				QueryType:  DetectQueryType(fullSQL),
				Tables:     ExtractTablesFromSQL(fullSQL),
				CallerName: callerName,
				FilePath:   filePath,
				Line:       variable.Line,
			}
			if len(variable.Conditions) > 0 {
				query.BaseSQL = variable.BaseSQL
				query.Conditions = variable.Conditions
			}
			result.Queries = append(result.Queries, query)
		}
		current = current.Parent
	}
}

// IsSQLStub checks if a string fragment contains SQL keywords, used as a loose filter
// to decide whether to start tracking a variable.
func IsSQLStub(text string) bool {
	upper := strings.ToUpper(text)
	keywords := []string{"SELECT", "INSERT", "UPDATE", "DELETE", "CREATE", "ALTER", "DROP", "WITH", "FROM", "WHERE", "JOIN", "SET", "INTO", "VALUES"}
	for _, keyword := range keywords {
		if strings.Contains(upper, keyword) {
			return true
		}
	}
	return false
}
