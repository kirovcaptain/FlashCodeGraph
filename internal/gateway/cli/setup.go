package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/config"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/service"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:     "setup [path]",
		Short:   "Interactive project setup — generates .fcg.toml",
		GroupID: "manage",
		Args:    cobra.MaximumNArgs(1),
		RunE:    runSetup,
	})
}

func runSetup(cmd *cobra.Command, args []string) error {
	projectPath := "."
	if len(args) > 0 {
		projectPath = args[0]
	}
	absPath, _ := filepath.Abs(projectPath)
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("\n🔧 FCG Project Setup")
	fmt.Println()

	// Step 1: Detect project
	sc := scanner.New(&scanner.Config{RootPath: absPath})
	projectInfo, _ := sc.DetectProject()
	projectName := filepath.Base(absPath)
	projectType := "unknown"
	if projectInfo != nil {
		projectType = projectInfo.ProjectType
	}
	fmt.Printf("  Project: %s (%s)\n", projectName, projectType)

	// Step 2: Select storage
	database, falkordbURI := selectStorage(reader, absPath)
	fmt.Printf("  Storage: %s", database)
	if falkordbURI != "" {
		fmt.Printf(" (%s)", falkordbURI)
	}
	fmt.Println()
	fmt.Println()

	// Step 3: Select modules to ignore
	var ignore []string
	if projectInfo != nil && len(projectInfo.SubModules) > 0 {
		ignore = selectIgnoreModules(reader, projectInfo.SubModules)
	} else {
		ignore = selectIgnoreDirectories(reader, absPath)
	}

	// Step 4: Exclude tests
	excludeTests := promptYesNo(reader, "Exclude test files?", true)

	// Step 4.5: Cross-project index backend (inherit global)
	crossBackend, crossSQLitePath := selectSetupCrossProjectIndex(reader)

	// Step 5: Select dependent projects (toggle mode)
	existingConfig, _ := config.Load(absPath)
	dependencies, properties := selectDependencies(reader, absPath, existingConfig)

	// Step 5: Generate config
	fmt.Println()
	configPath := config.ProjectConfigPath(absPath)
	cfg := buildSetupConfig(projectName, projectType, database, falkordbURI, ignore, excludeTests, crossBackend, crossSQLitePath, dependencies, properties)
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(configPath, []byte(cfg), 0644); err != nil {
		return err
	}
	fmt.Printf("  ✅ Generated %s\n\n", configPath)

	// Step 6: Add .fcg/ to .gitignore
	if !service.IsFcgIgnored(absPath) {
		if promptYesNo(reader, "Add '.fcg/' to .gitignore?", true) {
			if modified, err := service.EnsureFcgIgnored(absPath); err != nil {
				fmt.Fprintf(os.Stderr, "  ⚠️  Failed to update .gitignore: %v\n", err)
			} else if modified {
				fmt.Println("  ✅ Added '.fcg/' to .gitignore")
			}
		}
	}
	fmt.Println()

	// Step 7: Index now? (force full index since setup may have changed config)
	if promptYesNo(reader, "Run fcg index now?", true) {
		fmt.Println()
		indexForce = true
		return runIndex(cmd, []string{projectPath})
	}
	return nil
}

// selectStorage lets user choose storage backend, respecting global config.
func selectStorage(reader *bufio.Reader, projectPath string) (database string, falkordbURI string) {
	// Check global config first
	globalConfig, _ := config.Load("")
	if globalConfig != nil && globalConfig.Storage.Database != "" {
		globalDB := globalConfig.Storage.Database
		globalURI := globalConfig.Storage.FalkorDBURI
		display := globalDB
		if globalURI != "" {
			display += " (" + globalURI + ")"
		}
		fmt.Printf("  Global storage: %s\n", display)
		if promptYesNo(reader, "Use same?", true) {
			return globalDB, globalURI
		}
	}

	// Check existing project config
	existingConfig, _ := config.Load(projectPath)
	if existingConfig != nil && existingConfig.Storage.Database != "" {
		existingDB := existingConfig.Storage.Database
		existingURI := existingConfig.Storage.FalkorDBURI
		display := existingDB
		if existingURI != "" {
			display += " (" + existingURI + ")"
		}
		fmt.Printf("  Current storage: %s\n", display)
		if promptYesNo(reader, "Keep?", true) {
			return existingDB, existingURI
		}
	}

	// Manual selection
	fmt.Println("  Select storage:")
	fmt.Println("    [1] falkordb (recommended if FalkorDB is running)")
	fmt.Println("    [2] ladybug (recommended — embedded, no external dependency)")
	fmt.Println("    [3] kuzu (embedded, legacy)")
	if config.IsWSL() {
		fmt.Println("    ⚠️  WSL detected — falkordb recommended (embedded disk mode may be unreliable)")
	}
	fmt.Print("  Storage: ")
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)

	switch line {
	case "1", "falkordb":
		falkordbURI = selectFalkorDBConnection(reader)
		return "falkordb", falkordbURI
	case "2", "ladybug":
		return "ladybug", ""
	case "3", "kuzu":
		return "kuzu", ""
	default:
		// Auto-detect fallback
		return detectStorage(), ""
	}
}


// detectStorage auto-detects storage backend (fallback for non-interactive mode).
func detectStorage() string {
	socketPath := storage.DefaultFalkorDBSocket()
	if _, err := os.Stat(socketPath); err == nil {
		return "falkordb"
	}
	return config.DetectDefaultDatabase()
}
// selectFalkorDBConnection lets user choose FalkorDB connection method.
func selectFalkorDBConnection(reader *bufio.Reader) string {
	socketPath := storage.DefaultFalkorDBSocket()
	hasSocket := false
	if _, err := os.Stat(socketPath); err == nil {
		hasSocket = true
	}

	fmt.Println("  FalkorDB connection:")
	fmt.Println("    [1] TCP localhost:6379 (default)")
	if hasSocket {
		fmt.Printf("    [2] Unix socket %s\n", socketPath)
	}
	fmt.Println("    [3] Custom address")
	fmt.Print("  Connection: ")
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)

	switch line {
	case "1", "":
		return "localhost:6379"
	case "2":
		if hasSocket {
			return "unix://" + socketPath
		}
		return "localhost:6379"
	case "3":
		fmt.Print("  Address (host:port or unix:///path): ")
		address, _ := reader.ReadString('\n')
		address = strings.TrimSpace(address)
		if address != "" {
			return address
		}
		return "localhost:6379"
	default:
		return "localhost:6379"
	}
}

func selectIgnoreModules(reader *bufio.Reader, modules []scanner.SubModule) []string {
	fmt.Printf("  Found %d submodules:\n", len(modules))
	for i, m := range modules {
		fmt.Printf("    [%d] %s\n", i+1, m.Name)
	}
	fmt.Print("\n  Select modules to IGNORE (comma-separated, or Enter to skip): ")
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	var ignore []string
	for _, part := range strings.Split(line, ",") {
		idx := strings.TrimSpace(part)
		var n int
		if _, err := fmt.Sscanf(idx, "%d", &n); err == nil && n >= 1 && n <= len(modules) {
			ignore = append(ignore, modules[n-1].Name)
		}
	}
	if len(ignore) > 0 {
		fmt.Printf("  Ignoring: %s\n", strings.Join(ignore, ", "))
	}
	return ignore
}

func selectIgnoreDirectories(reader *bufio.Reader, absPath string) []string {
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			if !scanner.DefaultIgnoredDirectories[e.Name()] {
				dirs = append(dirs, e.Name())
			}
		}
	}
	if len(dirs) == 0 {
		return nil
	}
	fmt.Printf("  Top-level directories:\n")
	for i, d := range dirs {
		fmt.Printf("    [%d] %s\n", i+1, d)
	}
	fmt.Print("\n  Select directories to IGNORE (comma-separated, or Enter to skip): ")
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	var ignore []string
	for _, part := range strings.Split(line, ",") {
		idx := strings.TrimSpace(part)
		var n int
		if _, err := fmt.Sscanf(idx, "%d", &n); err == nil && n >= 1 && n <= len(dirs) {
			ignore = append(ignore, dirs[n-1])
		}
	}
	if len(ignore) > 0 {
		fmt.Printf("  Ignoring: %s\n", strings.Join(ignore, ", "))
	}
	return ignore
}

func promptYesNo(reader *bufio.Reader, prompt string, defaultYes bool) bool {
	hint := "[Y/n]"
	if !defaultYes {
		hint = "[y/N]"
	}
	fmt.Printf("  %s %s: ", prompt, hint)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return defaultYes
	}
	return line == "y" || line == "yes"
}

func buildSetupConfig(projectName, projectType, database, falkordbURI string, ignore []string, excludeTests bool, crossBackend, crossSQLitePath string, dependencies []config.DependencyProject, properties map[string]string) string {
	var setupBuilder strings.Builder
	setupBuilder.WriteString(fmt.Sprintf("[project]\nname = %q\ntype = %q\n\n", projectName, projectType))
	setupBuilder.WriteString(fmt.Sprintf("[storage]\ndatabase = %q\n", database))
	if falkordbURI != "" {
		setupBuilder.WriteString(fmt.Sprintf("falkordb_uri = %q\n", falkordbURI))
	}
	setupBuilder.WriteString("\n[index]\n")
	if len(ignore) > 0 {
		setupBuilder.WriteString(fmt.Sprintf("ignore = [%s]\n", formatStringSlice(ignore)))
	}
	if excludeTests {
		setupBuilder.WriteString("exclude_tests = true\n")
	}
	setupBuilder.WriteString(fmt.Sprintf("\n[cross_project_index]\nbackend = %q\n", crossBackend))
	if crossSQLitePath != "" {
		setupBuilder.WriteString(fmt.Sprintf("sqlite_path = %q\n", crossSQLitePath))
	}
	if len(dependencies) > 0 {
		setupBuilder.WriteString("\n")
		for _, dependency := range dependencies {
			setupBuilder.WriteString(fmt.Sprintf("[[dependencies.projects]]\npath = %q\nbranch = %q\n\n", dependency.Path, dependency.Branch))
		}
	}
	if len(properties) > 0 {
		setupBuilder.WriteString("[dependencies.properties]\n")
		for key, value := range properties {
			setupBuilder.WriteString(fmt.Sprintf("%q = %q\n", key, value))
		}
	}
	return setupBuilder.String()
}

func formatStringSlice(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = fmt.Sprintf("%q", item)
	}
	return strings.Join(quoted, ", ")
}

// selectDependencies handles Step 4.5 (dependency toggle) and Step 4.6 (properties editing).
func selectDependencies(reader *bufio.Reader, absPath string, existingConfig *config.Config) ([]config.DependencyProject, map[string]string) {
	registry, err := storage.NewRegistry(config.GlobalDir())
	if err != nil {
		return nil, nil
	}

	// Collect available projects (exclude current)
	type projectInfo struct {
		name     string
		path     string
		branches []string
	}
	projectMap := make(map[string]*projectInfo)
	for _, entry := range registry.List() {
		entryAbsPath, _ := filepath.Abs(entry.Path)
		if entryAbsPath == absPath {
			continue
		}
		if existing, ok := projectMap[entry.Path]; ok {
			existing.branches = append(existing.branches, entry.Branch)
		} else {
			projectMap[entry.Path] = &projectInfo{
				name:     entry.Name,
				path:     entry.Path,
				branches: []string{entry.Branch},
			}
		}
	}

	if len(projectMap) == 0 {
		fmt.Println("\n  No other indexed projects found. Skip dependency selection.")
		properties := editProperties(reader, existingConfig)
		return nil, properties
	}

	// Build ordered list
	var availableProjects []*projectInfo
	for _, projectEntry := range projectMap {
		availableProjects = append(availableProjects, projectEntry)
	}

	// Build selected set from existing config
	selectedSet := make(map[string]string) // path → branch
	if existingConfig != nil {
		for _, dependency := range existingConfig.Dependencies.Projects {
			selectedSet[dependency.Path] = dependency.Branch
		}
	}

	// Display toggle list
	fmt.Println("\n  Dependent projects (* = selected):")
	for i, project := range availableProjects {
		marker := " "
		branchInfo := ""
		if branch, ok := selectedSet[project.path]; ok {
			marker = "*"
			branchInfo = fmt.Sprintf(" (%s)", branch)
		}
		fmt.Printf("    [%d] %s %s (%s)%s\n", i+1, marker, project.name, project.path, branchInfo)
		if _, ok := selectedSet[project.path]; !ok {
			fmt.Printf("         Branches: %s\n", strings.Join(project.branches, ", "))
		}
	}

	fmt.Print("\n  Toggle (comma-separated, or Enter to keep): ")
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)

	if line != "" {
		for _, part := range strings.Split(line, ",") {
			index, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil || index < 1 || index > len(availableProjects) {
				continue
			}
			project := availableProjects[index-1]
			if _, isSelected := selectedSet[project.path]; isSelected {
				// Toggle off
				delete(selectedSet, project.path)
				fmt.Printf("  Removed: %s\n", project.name)
			} else {
				// Toggle on — ask for branch
				branch := selectBranch(reader, project.name, project.branches)
				selectedSet[project.path] = branch
				fmt.Printf("  Added: %s (%s)\n", project.name, branch)
			}
		}
	}

	// Build result
	var dependencies []config.DependencyProject
	for projectPath, branch := range selectedSet {
		dependencies = append(dependencies, config.DependencyProject{Path: projectPath, Branch: branch})
	}

	if len(dependencies) > 0 {
		fmt.Println("\n  Dependencies:")
		for _, dependency := range dependencies {
			fmt.Printf("    - %s (%s)\n", filepath.Base(dependency.Path), dependency.Branch)
		}
	}

	// Step 4.6: Properties
	properties := editProperties(reader, existingConfig)

	return dependencies, properties
}

// selectBranch prompts user to select a branch from available branches.
func selectBranch(reader *bufio.Reader, projectName string, branches []string) string {
	if len(branches) == 1 {
		return branches[0]
	}
	fmt.Printf("  Select branch for %s:\n", projectName)
	for i, branch := range branches {
		fmt.Printf("    [%d] %s\n", i+1, branch)
	}
	fmt.Print("  Branch: ")
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	index, err := strconv.Atoi(line)
	if err == nil && index >= 1 && index <= len(branches) {
		return branches[index-1]
	}
	return branches[0]
}

// editProperties handles Step 4.6 — key-value property editing.
func editProperties(reader *bufio.Reader, existingConfig *config.Config) map[string]string {
	properties := make(map[string]string)
	if existingConfig != nil && len(existingConfig.Dependencies.Properties) > 0 {
		for key, value := range existingConfig.Dependencies.Properties {
			properties[key] = value
		}
	}

	// Build ordered key list for stable display
	orderedKeys := make([]string, 0, len(properties))
	for key := range properties {
		orderedKeys = append(orderedKeys, key)
	}

	if len(properties) > 0 {
		fmt.Println("\n  Current properties:")
		for i, key := range orderedKeys {
			fmt.Printf("    [%d] %s = %s\n", i+1, key, properties[key])
		}
		fmt.Println("\n  Edit: [number] to modify, [a] to add, [d number] to delete, Enter to keep")
	} else {
		fmt.Println("\n  Properties (for @FeignClient placeholders like ${xxx}):")
		fmt.Println("    (none)")
		fmt.Println("\n  Edit: [a] to add, Enter to skip")
	}

	for {
		fmt.Print("  > ")
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)

		if line == "" {
			break
		}

		if line == "a" {
			// Add new property
			fmt.Print("  Key: ")
			key, _ := reader.ReadString('\n')
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			fmt.Print("  Value: ")
			value, _ := reader.ReadString('\n')
			value = strings.TrimSpace(value)
			properties[key] = value
			orderedKeys = append(orderedKeys, key)
			fmt.Printf("  Added: %s = %s\n", key, value)
			continue
		}

		if strings.HasPrefix(line, "d ") || strings.HasPrefix(line, "d\t") {
			// Delete property
			indexStr := strings.TrimSpace(line[2:])
			index, err := strconv.Atoi(indexStr)
			if err != nil || index < 1 || index > len(orderedKeys) {
				fmt.Println("  Invalid number")
				continue
			}
			deletedKey := orderedKeys[index-1]
			delete(properties, deletedKey)
			orderedKeys = append(orderedKeys[:index-1], orderedKeys[index:]...)
			fmt.Printf("  Deleted: %s\n", deletedKey)
			continue
		}

		// Modify existing property by number
		index, err := strconv.Atoi(line)
		if err != nil || index < 1 || index > len(orderedKeys) {
			fmt.Println("  Invalid input. Use [number], [a], [d number], or Enter.")
			continue
		}
		key := orderedKeys[index-1]
		fmt.Printf("  %s = %s\n", key, properties[key])
		fmt.Print("  New value (Enter to keep): ")
		newValue, _ := reader.ReadString('\n')
		newValue = strings.TrimSpace(newValue)
		if newValue != "" {
			properties[key] = newValue
			fmt.Printf("  Updated: %s = %s\n", key, newValue)
		}
	}

	if len(properties) > 0 {
		fmt.Println("\n  Final properties:")
		for _, key := range orderedKeys {
			if value, ok := properties[key]; ok {
				fmt.Printf("    %s = %s\n", key, value)
			}
		}
	}

	return properties
}

// selectSetupCrossProjectIndex inherits global cross-project index config or lets user override.
func selectSetupCrossProjectIndex(reader *bufio.Reader) (backend, sqlitePath string) {
	globalConfig, _ := config.Load("")
	if globalConfig != nil && globalConfig.CrossProjectIndex.Backend != "" {
		display := globalConfig.CrossProjectIndex.Backend
		if globalConfig.CrossProjectIndex.SQLitePath != "" {
			display += " (" + globalConfig.CrossProjectIndex.SQLitePath + ")"
		}
		fmt.Printf("  Cross-project index: %s\n", display)
		if promptYesNo(reader, "Use same?", true) {
			return globalConfig.CrossProjectIndex.Backend, globalConfig.CrossProjectIndex.SQLitePath
		}
	}
	return selectCrossProjectIndex(reader)
}
