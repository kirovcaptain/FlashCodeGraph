package model

// Symbol represents a code symbol extracted from source.
type Symbol struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	QualifiedName   string `json:"qualified_name"`
	Kind            string `json:"kind"` // function, class, interface, variable
	FilePath        string `json:"file_path"`
	StartLine       int    `json:"start_line"`
	EndLine         int    `json:"end_line"`
	Params          string `json:"params,omitempty"`          // JSON
	ReturnTypes     []string `json:"return_types,omitempty"`
	Visibility      string `json:"visibility,omitempty"`
	ClassType       string   `json:"class_type,omitempty"`      // class/abstract_class/interface/enum/struct
	TypeParams      []string `json:"type_params,omitempty"`     // generic type parameters: ["T", "U"]
	Annotations     string `json:"annotations,omitempty"`     // JSON
	Docstring       string `json:"docstring,omitempty"`
	IsExported      bool   `json:"is_exported,omitempty"`
	IsAbstract      bool   `json:"is_abstract,omitempty"`
	IsFinal         bool   `json:"is_final,omitempty"`
	IsAsync         bool   `json:"is_async,omitempty"`
	IsStatic        bool   `json:"is_static,omitempty"`
	IsSynthetic     bool   `json:"is_synthetic,omitempty"`
	IsConstructor   bool   `json:"is_constructor,omitempty"`
	IsGenerator     bool   `json:"is_generator,omitempty"`
	IsLambda        bool   `json:"is_lambda,omitempty"`
	IsGetter        bool   `json:"is_getter,omitempty"`        // true if method is a simple accessor getter (getXxx/isXxx or @property)
	IsSetter        bool   `json:"is_setter,omitempty"`        // true if method is a simple accessor setter (setXxx or @xxx.setter)
	LambdaContext   string `json:"lambda_context,omitempty"`
	Complexity      int    `json:"complexity,omitempty"`
	EntryPointScore float64 `json:"entry_point_score,omitempty"`
	EntryType       string `json:"entry_type,omitempty"`
}

// StructuredAnnotation represents a parsed annotation with name and parameters.
type StructuredAnnotation struct {
	Name   string            `json:"name"`             // e.g. "RestController", "DubboReference"
	Params map[string]string `json:"params,omitempty"` // e.g. {"version": "1.0.0", "group": "payment"}
}

// FieldInfo represents a class/struct field declaration.
type FieldInfo struct {
	Name        string                 `json:"name"`                  // e.g. "graphStore"
	Type        string                 `json:"type"`                  // e.g. "storage.GraphStore"
	Visibility  string                 `json:"visibility,omitempty"`  // public/private/protected/package
	Annotations []StructuredAnnotation `json:"annotations,omitempty"` // e.g. [{"name":"Autowired"}]
	IsStatic    bool                   `json:"is_static,omitempty"`
}

// FieldDeclaration extends FieldInfo with owner and location info for parser output.
type FieldDeclaration struct {
	FieldInfo
	OwnerQualifiedName string `json:"owner_qualified_name"` // owning class qualified_name
	FilePath           string `json:"file_path"`
	Line               int    `json:"line"`
}
