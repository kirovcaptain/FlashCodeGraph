package model

// RawCall represents an unresolved function call extracted from AST.
type RawCall struct {
	CalledName   string `json:"called_name"`
	CallerName   string `json:"caller_name"`
	CallerScope  string `json:"caller_scope,omitempty"` // block-level scope key for TypeEnv lookup
	CallerKind   string `json:"caller_kind"`
	Language     string `json:"language"`
	FilePath     string `json:"file_path"`
	Line         int    `json:"line"`
	ArgCount     int      `json:"arg_count"`
	ArgTypes     []string `json:"arg_types,omitempty"`
	ArgExprs     []string `json:"arg_exprs,omitempty"`
	ReceiverExpr string `json:"receiver_expr,omitempty"`
	FlowContext  string `json:"flow_context,omitempty"` // if/else/loop/defer/switch
	FlowLine     int    `json:"flow_line,omitempty"`    // line of the control flow statement
}

// RawImport represents an import statement extracted from AST.
type RawImport struct {
	ModulePath string `json:"module_path"`
	SymbolName string `json:"symbol_name,omitempty"`
	Alias      string `json:"alias,omitempty"`
	FilePath   string `json:"file_path"`
	Line       int    `json:"line"`
	IsReexport bool   `json:"is_reexport,omitempty"` // true for "export { X } from '...'" statements
	LocalName  string `json:"local_name,omitempty"`  // exported name (may differ from SymbolName when renamed)
	IsWildcard bool   `json:"is_wildcard,omitempty"` // true for "export * from '...'" statements
}

// RawHeritage represents an inheritance/implementation relationship from AST.
type RawHeritage struct {
	ChildName       string   `json:"child_name"`
	ChildQualified  string   `json:"child_qualified,omitempty"`
	ParentName      string   `json:"parent_name"`
	ParentQualified string   `json:"parent_qualified,omitempty"`
	Kind            string   `json:"kind"`                       // extends / implements / embedding
	FilePath        string   `json:"file_path"`
	TypeArgs        []string `json:"type_args,omitempty"`        // generic args: extends Base<User, Long> → ["User", "Long"]
}

// RawRoute represents a route declaration extracted from AST.
type RawRoute struct {
	Method      string `json:"method"`       // GET/POST/PUT/DELETE
	PathPattern string `json:"path_pattern"`
	HandlerName string `json:"handler_name"`
	Framework   string `json:"framework"`
	FilePath    string `json:"file_path"`
	Line        int    `json:"line"`
}

// RawRemoteCall represents an HTTP/RPC client call extracted from AST.
type RawRemoteCall struct {
	Method            string  `json:"method"`                        // GET/POST/PUT/DELETE/UNKNOWN
	TargetURL         string  `json:"target_url"`
	TargetService     string  `json:"target_service,omitempty"`
	ServiceResolvedBy string  `json:"service_resolved_by,omitempty"` // literal/config_mapping/unresolved
	ServiceConfidence float64 `json:"service_confidence"`            // 0.0~1.0
	Protocol          string  `json:"protocol"`                      // http / grpc
	CallerName        string  `json:"caller_name"`
	FilePath          string  `json:"file_path"`
	Line              int     `json:"line"`
}

// ConditionalFragment represents a SQL fragment appended inside a conditional branch.
type ConditionalFragment struct {
	Condition string `json:"condition"`
	Fragment  string `json:"fragment"`
	IsElse    bool   `json:"is_else"`
}

// RawQuery represents a database query extracted from AST.
type RawQuery struct {
	SQLText    string               `json:"sql_text"`
	QueryType  string               `json:"query_type"` // SELECT/INSERT/UPDATE/DELETE
	Tables     []string             `json:"tables"`
	CallerName string               `json:"caller_name"`
	FilePath   string               `json:"file_path"`
	Line       int                  `json:"line"`
	BaseSQL    string               `json:"base_sql,omitempty"`
	Conditions []ConditionalFragment `json:"conditions,omitempty"`
}

// RawMiddleware represents a middleware declaration extracted from AST.
type RawMiddleware struct {
	Name     string `json:"name"`
	Order    int    `json:"order"`
	RoutePath string `json:"route_path,omitempty"`
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
}

// RawConstRef represents a reference to a static constant (enum/interface/class) found in a method body.
type RawConstRef struct {
	// Pattern A (RefKind="field_access")
	ObjectExpr string `json:"object_expr,omitempty"`

	// Pattern B (RefKind="switch_case")
	SwitchConditionKind  string `json:"switch_condition_kind,omitempty"`
	SwitchVariableName   string `json:"switch_variable_name,omitempty"`
	SwitchMethodReceiver string `json:"switch_method_receiver,omitempty"`
	SwitchMethodName     string `json:"switch_method_name,omitempty"`

	ConstName  string `json:"const_name"`
	CallerName string `json:"caller_name"`
	FilePath   string `json:"file_path"`
	Line       int    `json:"line"`
	RefKind    string `json:"ref_kind"`
}

// TypeBinding represents a Tier 0/1 type hint from AST.
type TypeBinding struct {
	VarName       string   `json:"var_name"`
	TypeName      string   `json:"type_name"`
	TypeArgs      []string `json:"type_args,omitempty"`
	Tier          int      `json:"tier"`
	Scope         string   `json:"scope"`
	FilePath      string   `json:"file_path"`
	MultiReturnOf string   `json:"multi_return_of,omitempty"`
	ReturnIndex   int      `json:"return_index,omitempty"`
}

// PendingAssignment represents an unresolved variable assignment for fixpoint propagation.
type PendingAssignment struct {
	Kind            string `json:"kind"`                        // copy, call_result, field_access, method_call_result, destructure
	LHS             string `json:"lhs"`                         // variable being assigned
	Scope           string `json:"scope"`                       // enclosing block/function scope
	RHS             string `json:"rhs,omitempty"`               // copy: source variable
	Callee          string `json:"callee,omitempty"`            // call_result/destructure: function name
	Receiver        string `json:"receiver,omitempty"`          // field_access/method_call_result: receiver variable
	Field           string `json:"field,omitempty"`             // field_access: field name
	Method          string `json:"method,omitempty"`            // method_call_result: method name
	DestructuredKey string `json:"destructured_key,omitempty"`  // destructure: field name or index ("0", "1")
}

// ParseResult holds all extracted data from a single file.
type ParseResult struct {
	FilePath    string          `json:"file_path"`
	Language    string          `json:"language"`
	Symbols     []Symbol        `json:"symbols"`
	Calls       []RawCall       `json:"calls"`
	Imports     []RawImport     `json:"imports"`
	Heritage    []RawHeritage   `json:"heritage"`
	Routes      []RawRoute      `json:"routes"`
	RemoteCalls []RawRemoteCall `json:"remote_calls"`
	Queries     []RawQuery      `json:"queries"`
	Middlewares []RawMiddleware  `json:"middlewares"`
	PendingRemoteCalls  []PendingRemoteCall  `json:"pending_remote_calls,omitempty"`
	Fields              []FieldDeclaration   `json:"fields,omitempty"`
	TypeHints           []TypeBinding       `json:"type_hints"`
	PendingAssignments  []PendingAssignment  `json:"pending_assignments,omitempty"`
	ScopeParents        map[string]string    `json:"scope_parents,omitempty"` // childScope → parentScope
	ConstRefs           []RawConstRef        `json:"const_refs,omitempty"`
	Errors      []ParseError    `json:"errors,omitempty"`
}

// ParseError records a non-fatal parse error.
type ParseError struct {
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Message  string `json:"message"`
}

// PendingRemoteCall records a field-level remote call declaration.
// The actual caller method is unknown at parse time; it will be resolved
// in the indexer post-processing phase by matching against callRelations.
type PendingRemoteCall struct {
	FieldName   string                 `json:"field_name"`    // e.g. paymentDubboService
	FieldType   string                 `json:"field_type"`    // e.g. PaymentDubboService
	Protocol    string                 `json:"protocol"`      // dubbo / grpc
	OwnerClass  string                 `json:"owner_class"`   // e.g. com.example.PaymentDubboCaller
	Annotations []StructuredAnnotation `json:"annotations"`   // e.g. @DubboReference(version="1.0")
	FilePath    string                 `json:"file_path"`
	Line        int                    `json:"line"`
}
