package python

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// Python route decorator patterns
var routeDecorators = map[string]string{
	"route": "GET",
	"get":   "GET",
	"post":  "POST",
	"put":   "PUT",
	"delete": "DELETE",
	"patch": "PATCH",
}

func ExtractRoutes(node *tree_sitter.Node, content []byte, funcName, filePath string, result *model.ParseResult) {
	// Look for decorated_definition parent or sibling decorators
	parent := node.Parent()
	if parent == nil || parent.Kind() != "decorated_definition" {
		return
	}

	for i := uint(0); i < parent.ChildCount(); i++ {
		child := parent.Child(i)
		if child.Kind() != "decorator" {
			continue
		}

		decoratorText := child.Utf8Text(content)

		// MCP tool decorator: @server.tool() or @mcp.tool()
		if strings.Contains(decoratorText, ".tool(") && hasMCPImport(result) {
			toolName := extractMCPToolName(decoratorText, funcName)
			result.Routes = append(result.Routes, model.RawRoute{
				Method:      "TOOL",
				PathPattern: toolName,
				Handlers:    []string{funcName},
				Framework:   "mcp",
				FilePath:    filePath,
				Line:        int(child.StartPosition().Row) + 1,
			})
			return
		}

		// HTTP route decorators: @app.route("/path"), @router.get("/path"), etc.
		for methodName, httpMethod := range routeDecorators {
			pattern := "." + methodName + "("
			if strings.Contains(decoratorText, pattern) {
				pathPattern := extractDecoratorArg(decoratorText)
				result.Routes = append(result.Routes, model.RawRoute{
					Method:      httpMethod,
					PathPattern: pathPattern,
					Handlers:    []string{funcName},
					Framework:   detectPythonFramework(decoratorText),
					FilePath:    filePath,
					Line:        int(child.StartPosition().Row) + 1,
				})
				break
			}
		}
	}
}

// hasMCPImport checks if the file imports the mcp package.
func hasMCPImport(result *model.ParseResult) bool {
	for _, imp := range result.Imports {
		if imp.ModulePath == "mcp" || strings.HasPrefix(imp.ModulePath, "mcp.") {
			return true
		}
	}
	return false
}

// extractMCPToolName extracts the tool name from @server.tool() decorator.
// If decorator has a name argument like @server.tool("my_tool"), use it; otherwise use function name.
func extractMCPToolName(decoratorText, funcName string) string {
	arg := extractDecoratorArg(decoratorText)
	if arg != "" {
		return arg
	}
	return funcName
}

func extractDecoratorArg(decoratorText string) string {
	start := strings.Index(decoratorText, "(")
	if start < 0 {
		return ""
	}
	end := strings.Index(decoratorText, ")")
	if end <= start {
		return ""
	}
	arg := strings.TrimSpace(decoratorText[start+1 : end])
	// Take first string argument
	if idx := strings.Index(arg, ","); idx >= 0 {
		arg = arg[:idx]
	}
	arg = strings.Trim(arg, "\"' ")
	return arg
}

func detectPythonFramework(decoratorText string) string {
	if strings.Contains(decoratorText, "app.") {
		return "flask"
	}
	if strings.Contains(decoratorText, "router.") {
		return "fastapi"
	}
	return "python"
}

func ExtractDjangoRoutes(node *tree_sitter.Node, content []byte, filePath string, result *model.ParseResult) {
	if node.Kind() != "call" {
		return
	}
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil {
		return
	}
	funcName := funcNode.Utf8Text(content)
	if funcName != "path" && funcName != "re_path" && funcName != "url" {
		return
	}

	argsNode := node.ChildByFieldName("arguments")
	if argsNode == nil {
		return
	}

	pathPattern := ""
	handlerName := ""
	argIndex := 0
	for i := uint(0); i < argsNode.ChildCount(); i++ {
		arg := argsNode.Child(i)
		if !arg.IsNamed() {
			continue
		}
		argText := arg.Utf8Text(content)
		if argIndex == 0 {
			pathPattern = strings.NewReplacer("\"", "", "'", "").Replace(argText)
		} else if argIndex == 1 {
			handlerName = argText
		}
		argIndex++
	}

	if pathPattern != "" && handlerName != "" {
		if !strings.HasPrefix(pathPattern, "/") {
			pathPattern = "/" + pathPattern
		}
		result.Routes = append(result.Routes, model.RawRoute{
			Method:      "GET",
			PathPattern: pathPattern,
			Handlers:    []string{handlerName},
			Framework:   "django",
			FilePath:    filePath,
			Line:        int(node.StartPosition().Row) + 1,
		})
	}
}
