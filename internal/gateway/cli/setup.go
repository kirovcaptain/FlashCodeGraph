package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
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

	// Step 2: Detect storage
	database := detectStorage()
	fmt.Printf("  Storage: %s\n\n", database)

	// Step 3: Select modules to ignore
	var ignore []string
	if projectInfo != nil && len(projectInfo.SubModules) > 0 {
		ignore = selectIgnoreModules(reader, projectInfo.SubModules)
	} else {
		ignore = selectIgnoreDirectories(reader, absPath)
	}

	// Step 4: Exclude tests
	excludeTests := promptYesNo(reader, "Exclude test files?", true)

	// Step 5: Generate config
	fmt.Println()
	configPath := config.ProjectConfigPath(absPath)
	cfg := buildSetupConfig(projectName, projectType, database, ignore, excludeTests)
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

	// Step 7: Index now?
	if promptYesNo(reader, "Run fcg index now?", true) {
		fmt.Println()
		return runIndex(cmd, []string{projectPath})
	}
	return nil
}

func detectStorage() string {
	socketPath := storage.DefaultFalkorDBSocket()
	if _, err := os.Stat(socketPath); err == nil {
		return "falkordb"
	}
	// TODO: try TCP localhost:6379
	return "kuzu"
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

func buildSetupConfig(projectName, projectType, database string, ignore []string, excludeTests bool) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[project]\nname = %q\ntype = %q\n\n", projectName, projectType))
	sb.WriteString(fmt.Sprintf("[storage]\ndatabase = %q\n\n", database))
	sb.WriteString("[index]\n")
	if len(ignore) > 0 {
		sb.WriteString(fmt.Sprintf("ignore = [%s]\n", formatStringSlice(ignore)))
	}
	if excludeTests {
		sb.WriteString("exclude_tests = true\n")
	}
	return sb.String()
}

func formatStringSlice(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = fmt.Sprintf("%q", item)
	}
	return strings.Join(quoted, ", ")
}
