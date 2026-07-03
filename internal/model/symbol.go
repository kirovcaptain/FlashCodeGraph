package model

// ParamInfo describes a function/method parameter.
type ParamInfo struct {
	Name       string    `json:"name"`
	Type       string    `json:"type,omitempty"`
	TypeArgs   []TypeArg `json:"type_args,omitempty"` // generic type arguments: Class<T> → [{Name:"T"}]
	HasDefault bool      `json:"default,omitempty"`
}

// ReturnType describes a function/method return type with optional generic type arguments.
type ReturnType struct {
	Name string    `json:"name"`
	Args []TypeArg `json:"args,omitempty"`
}

// Symbol represents a code symbol extracted from source.
type Symbol struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	QualifiedName   string `json:"qualified_name"`
	Kind            string `json:"kind"` // function, class, interface, variable
	FilePath        string `json:"file_path"`
	StartLine       int    `json:"start_line"`
	EndLine         int    `json:"end_line"`
	Params          []ParamInfo `json:"params,omitempty"`
	ReturnTypes     []ReturnType `json:"return_types,omitempty"`
	Visibility      string `json:"visibility,omitempty"`
	ClassType       string   `json:"class_type,omitempty"`      // class/abstract_class/interface/enum/struct
	TypeParams      []string `json:"type_params,omitempty"`     // generic type parameters: ["T", "U"]
	Annotations     []StructuredAnnotation `json:"annotations,omitempty"`
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
	IsDefaultExport bool   `json:"is_default_export,omitempty"` // true for "export default class/function" in TS/JS
	LambdaContext   string `json:"lambda_context,omitempty"`
	Complexity      int    `json:"complexity,omitempty"`
	EntryPointScore float64 `json:"entry_point_score,omitempty"`
	EntryType       string `json:"entry_type,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"` // Kind-specific extension properties
}

// StructuredAnnotation represents a parsed annotation with name and parameters.
type StructuredAnnotation struct {
	Name   string            `json:"name"`             // e.g. "RestController", "DubboReference"
	Params map[string]string `json:"params,omitempty"` // e.g. {"version": "1.0.0", "group": "payment"}
	Line   int              `json:"line,omitempty"`   // Source line number of the annotation
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

// StringsToReturnTypes converts a []string to []ReturnType (each string parsed for generics).
func StringsToReturnTypes(typeNames []string) []ReturnType {
	if len(typeNames) == 0 {
		return nil
	}
	result := make([]ReturnType, len(typeNames))
	for i, name := range typeNames {
		result[i] = ParseReturnType(name)
	}
	return result
}

// FormatReturnType formats a ReturnType as a human-readable string (e.g. "List<User>").
func FormatReturnType(returnType ReturnType) string {
	if len(returnType.Args) == 0 {
		return returnType.Name
	}
	argStrings := make([]string, len(returnType.Args))
	for i, arg := range returnType.Args {
		argStrings[i] = FormatReturnType(ReturnType{Name: arg.Name, Args: arg.Args})
	}
	return returnType.Name + "<" + joinStrings(argStrings, ", ") + ">"
}

// FormatReturnTypes formats a slice of ReturnType as a string slice for display.
func FormatReturnTypes(returnTypes []ReturnType) []string {
	if len(returnTypes) == 0 {
		return nil
	}
	result := make([]string, len(returnTypes))
	for i, returnType := range returnTypes {
		result[i] = FormatReturnType(returnType)
	}
	return result
}

// ParseReturnType parses a type string (e.g. "List<User>") into a ReturnType struct.
// Handles nested generics: "Map<String, List<User>>" → {Name:"Map", Args:[{Name:"String"}, {Name:"List", Args:[{Name:"User"}]}]}
func ParseReturnType(typeString string) ReturnType {
	idx := indexOfGenericOpen(typeString)
	if idx < 0 {
		return ReturnType{Name: typeString}
	}
	baseName := typeString[:idx]
	inner := typeString[idx+1 : len(typeString)-1]
	argStrings := splitTopLevelCommas(inner)
	args := make([]TypeArg, len(argStrings))
	for i, argString := range argStrings {
		parsed := ParseReturnType(argString)
		args[i] = TypeArg{Name: parsed.Name, Args: parsed.Args}
	}
	return ReturnType{Name: baseName, Args: args}
}

// splitTopLevelCommas splits a string by commas not inside angle brackets.
func splitTopLevelCommas(s string) []string {
	var result []string
	depth := 0
	start := 0
	for i, ch := range s {
		switch ch {
		case '<':
			depth++
		case '>':
			depth--
		case ',':
			if depth == 0 {
				result = append(result, trimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	result = append(result, trimSpace(s[start:]))
	return result
}

func indexOfGenericOpen(s string) int {
	for i, ch := range s {
		if ch == '<' {
			return i
		}
	}
	return -1
}

func joinStrings(parts []string, separator string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for _, part := range parts[1:] {
		result += separator + part
	}
	return result
}

func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && s[start] == ' ' {
		start++
	}
	for end > start && s[end-1] == ' ' {
		end--
	}
	return s[start:end]
}
