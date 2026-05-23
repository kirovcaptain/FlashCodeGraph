package model

// TypeArg represents a generic type argument, supporting nested generics.
// Example: Map<String, List<User>> → [{Name:"String"}, {Name:"List", Args:[{Name:"User"}]}]
type TypeArg struct {
	Name string    `json:"name"`
	Args []TypeArg `json:"args,omitempty"`
}

// TypeEnv maps variable names to type info within a scope.
type TypeEnv struct {
	Bindings     map[string]*TypeInfo `json:"bindings"`
	Imports      []RawImport          `json:"imports,omitempty"`
	ScopeParents map[string]string    `json:"scope_parents,omitempty"` // childScope → parentScope for chain lookup
}

// TypeInfo describes an inferred type.
type TypeInfo struct {
	TypeName      string    `json:"type_name"`
	TypeArgs      []TypeArg `json:"type_args,omitempty"` // generic args: List<User> → [{Name:"User"}]
	Tier          int       `json:"tier"`
	Scope         string    `json:"scope"`
	MultiReturnOf string    `json:"multi_return_of,omitempty"`
	ReturnIndex   int       `json:"return_index,omitempty"`
}

// ResolvedRelation is a fully resolved relationship with confidence.
type ResolvedRelation struct {
	SourceID    string            `json:"source_id"`
	TargetID    string            `json:"target_id"`
	Kind        RelationKind      `json:"kind"`
	SourceKind  string            `json:"source_kind,omitempty"`
	Confidence  float64           `json:"confidence"`
	ResolvedBy  string            `json:"resolved_by"`
	Candidates  int               `json:"candidates"`
	Line        int               `json:"line"`
	FlowContext string            `json:"flow_context,omitempty"`
	FlowLine    int               `json:"flow_line,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// UnresolvedHint describes a call that could not be precisely resolved.
type UnresolvedHint struct {
	HintType       string   `json:"hint_type"`                  // lambda_call/chained_call/super_field_call/linq_call/enum_method/builder_chain/ambiguous_project_call/untyped_receiver
	Line           int      `json:"line"`
	Method         string   `json:"method"`
	ReceiverExpr   string   `json:"receiver_expr,omitempty"`
	ReceiverType   string   `json:"receiver_type,omitempty"`
	ContainerVar   string   `json:"container_var,omitempty"`
	ContainerType  string   `json:"container_type,omitempty"`
	ChainExpr      string   `json:"chain_expr,omitempty"`
	Candidates     []string `json:"candidates,omitempty"`
	CandidateCount int      `json:"candidate_count"`
	FilePath       string   `json:"file_path"`
	CallerID       string   `json:"caller_id"`
}
