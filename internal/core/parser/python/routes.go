package python

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/astutil"
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
		if strings.Contains(decoratorText, ".tool(") && detectMcpFramework(result) != "" {
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

// detectCliFramework returns the CLI framework name based on imports.
func detectCliFramework(result *model.ParseResult) string {
	for _, imp := range result.Imports {
		if imp.ModulePath == "click" || strings.HasPrefix(imp.ModulePath, "click.") {
			return "click"
		}
		if imp.ModulePath == "typer" || strings.HasPrefix(imp.ModulePath, "typer.") {
			return "typer"
		}
	}
	return ""
}

// detectMcpFramework returns the MCP framework name based on imports.
func detectMcpFramework(result *model.ParseResult) string {
	for _, imp := range result.Imports {
		if imp.ModulePath == "mcp" || strings.HasPrefix(imp.ModulePath, "mcp.") {
			return "mcp"
		}
		if imp.ModulePath == "fastmcp" || strings.HasPrefix(imp.ModulePath, "fastmcp.") {
			return "fastmcp"
		}
	}
	return ""
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

// ExtractClickRoutes extracts CLI routes from click/typer decorators.
// Uses delayed generation: first collects all groups, then resolves command paths.
func ExtractClickRoutes(rootNode *tree_sitter.Node, content []byte, filePath string, result *model.ParseResult) {
	cliFramework := detectCliFramework(result)
	if cliFramework == "" {
		return
	}

	type clickCommandInfo struct {
		Name     string
		Receiver string
		Handler  string
		Line     int
	}

	clickGroups := map[string]string{}     // funcName → parentReceiver ("" = root)
	clickGroupNames := map[string]string{} // funcName → explicit group name (or func name)
	var clickCommands []clickCommandInfo

	// Walk all decorated_definitions to collect groups and commands
	astutil.WalkNamedChildren(rootNode, func(node *tree_sitter.Node) bool {
		if node.Kind() != "decorated_definition" {
			return true
		}
		// Find the function definition inside
		var funcDef *tree_sitter.Node
		for i := uint(0); i < node.NamedChildCount(); i++ {
			child := node.NamedChild(i)
			if child.Kind() == "function_definition" {
				funcDef = child
				break
			}
		}
		if funcDef == nil {
			return true
		}
		funcNameNode := funcDef.ChildByFieldName("name")
		if funcNameNode == nil {
			return true
		}
		funcName := funcNameNode.Utf8Text(content)

		// Check decorators
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child.Kind() != "decorator" {
				continue
			}
			decoratorText := child.Utf8Text(content)

			// Extract receiver (part before .group( or .command()
			dotGroupIdx := strings.Index(decoratorText, ".group(")
			dotCommandIdx := strings.Index(decoratorText, ".command(")

			if dotGroupIdx > 0 {
				// @xxx.group() or @click.group()
				receiver := decoratorText[1:dotGroupIdx] // skip @
				groupName := extractDecoratorArg(decoratorText)
				if groupName == "" {
					groupName = funcName
				}
				if receiver == "click" || receiver == "typer" {
					clickGroups[funcName] = "" // root group
				} else {
					clickGroups[funcName] = receiver
				}
				clickGroupNames[funcName] = groupName
				return true
			}

			if dotCommandIdx > 0 {
				// @xxx.command() or @click.command()
				receiver := decoratorText[1:dotCommandIdx] // skip @
				commandName := extractDecoratorArg(decoratorText)
				if commandName == "" {
					commandName = funcName
				}
				clickCommands = append(clickCommands, clickCommandInfo{
					Name:     commandName,
					Receiver: receiver,
					Handler:  funcName,
					Line:     int(child.StartPosition().Row) + 1,
				})
				return true
			}
		}
		return true
	})

	// Generate routes with resolved parent paths
	for _, command := range clickCommands {
		prefix := resolveClickCommandPath(command.Receiver, clickGroups, clickGroupNames)
		fullPath := command.Name
		if prefix != "" {
			fullPath = prefix + " " + command.Name
		}
		result.Routes = append(result.Routes, model.RawRoute{
			Method:      "CLI",
			PathPattern: fullPath,
			Handlers:    []string{command.Handler},
			Framework:   cliFramework,
			FilePath:    filePath,
			Line:        command.Line,
		})
	}
}

// resolveClickCommandPath resolves the parent path prefix for a click command receiver.
func resolveClickCommandPath(receiver string, clickGroups, clickGroupNames map[string]string) string {
	var parts []string
	current := receiver
	for {
		parent, isGroup := clickGroups[current]
		if !isGroup {
			break
		}
		parts = append([]string{clickGroupNames[current]}, parts...)
		if parent == "" {
			break // root group
		}
		current = parent
	}
	// Remove root group from path (root doesn't contribute to command path)
	if len(parts) > 0 && clickGroups[receiver] == "" {
		// receiver itself is root — no prefix
		return ""
	}
	if len(parts) > 0 {
		// First element is root group — skip it
		parts = parts[1:]
	}
	return strings.Join(parts, " ")
}
