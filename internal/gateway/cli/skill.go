package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// SkillsFS is set by main.go with the embedded skills filesystem.
var SkillsFS fs.FS

type platformConfig struct {
	Version   string                   `json:"version"`
	Platforms map[string]platformEntry `json:"platforms"`
	MCPServer mcpServerConfig          `json:"mcp_server"`
}

type platformEntry struct {
	SkillFile     string  `json:"skill_file"`
	InstallPath   string  `json:"install_path"`
	VersionFile   string  `json:"version_file"`
	MCPConfigPath *string `json:"mcp_config_path"`
	MCPConfigKey  *string `json:"mcp_config_key"`
}

type mcpServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

func init() {
	skillCmd := &cobra.Command{
		Use:     "skill",
		GroupID: "agent",
		Short:   "Manage FCG skills for AI coding assistants",
	}

	installCmd := &cobra.Command{
		Use:   "install [platform]",
		Short: "Install FCG skill and MCP config for a platform",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runSkillInstall,
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List available platforms",
		RunE:  runSkillList,
	}

	skillCmd.AddCommand(installCmd, listCmd)
	rootCmd.AddCommand(skillCmd)
}

func loadPlatformConfig() (*platformConfig, error) {
	data, err := fs.ReadFile(SkillsFS, "platforms.json")
	if err != nil {
		return nil, fmt.Errorf("read platforms.json: %w", err)
	}
	var cfg platformConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse platforms.json: %w", err)
	}
	return &cfg, nil
}

// platformNames returns sorted platform names for stable display order.
func platformNames(cfg *platformConfig) []string {
	// Fixed order for display
	order := []string{"kiro", "claude", "copilot"}
	var names []string
	for _, name := range order {
		if _, ok := cfg.Platforms[name]; ok {
			names = append(names, name)
		}
	}
	// Append any platforms not in the fixed order
	for name := range cfg.Platforms {
		found := false
		for _, o := range order {
			if name == o {
				found = true
				break
			}
		}
		if !found {
			names = append(names, name)
		}
	}
	return names
}

func runSkillList(cmd *cobra.Command, args []string) error {
	cfg, err := loadPlatformConfig()
	if err != nil {
		return err
	}
	fmt.Println("Available platforms:")
	for _, name := range platformNames(cfg) {
		entry := cfg.Platforms[name]
		fmt.Printf("  %-12s → ~/%s\n", name, entry.InstallPath)
	}
	return nil
}

func runSkillInstall(cmd *cobra.Command, args []string) error {
	cfg, err := loadPlatformConfig()
	if err != nil {
		return err
	}

	var platform string
	if len(args) > 0 {
		platform = args[0]
	} else {
		platform = promptPlatformSelection(cfg)
		if platform == "" {
			return nil
		}
	}

	entry, ok := cfg.Platforms[platform]
	if !ok {
		return fmt.Errorf("unknown platform %q, run 'fcg skill list' to see available platforms", platform)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("detect home dir: %w", err)
	}

	fcgBinary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("detect fcg binary path: %w", err)
	}
	fcgBinary, _ = filepath.EvalSymlinks(fcgBinary)

	fmt.Println()

	// Step 1: Install skill file
	skillContent, err := fs.ReadFile(SkillsFS, entry.SkillFile)
	if err != nil {
		return fmt.Errorf("read skill file %s: %w", entry.SkillFile, err)
	}
	skillDst := filepath.Join(home, entry.InstallPath)
	if err := os.MkdirAll(filepath.Dir(skillDst), 0755); err != nil {
		return fmt.Errorf("create skill dir: %w", err)
	}
	if err := os.WriteFile(skillDst, skillContent, 0644); err != nil {
		return fmt.Errorf("write skill: %w", err)
	}
	fmt.Printf("  ✅ skill installed → %s\n", skillDst)

	// Step 2: Write version file
	versionDst := filepath.Join(home, entry.VersionFile)
	if err := os.WriteFile(versionDst, []byte(cfg.Version), 0644); err != nil {
		return fmt.Errorf("write version: %w", err)
	}

	// Step 3: Configure MCP server
	if entry.MCPConfigPath != nil && entry.MCPConfigKey != nil {
		mcpPath := filepath.Join(home, *entry.MCPConfigPath)
		if err := installMCPConfig(mcpPath, *entry.MCPConfigKey, fcgBinary, cfg.MCPServer); err != nil {
			fmt.Printf("  ⚠ MCP config skipped: %v\n", err)
		} else {
			fmt.Printf("  ✅ MCP server configured → %s\n", mcpPath)
		}
	}

	fmt.Printf("\n  version: %s\n", cfg.Version)
	fmt.Println("\nDone. FCG skill and MCP server are ready.")
	return nil
}

func promptPlatformSelection(cfg *platformConfig) string {
	names := platformNames(cfg)

	fmt.Println("\n🔧 FCG Skill Installer")
	fmt.Println()
	fmt.Println("Available platforms:")
	for i, name := range names {
		entry := cfg.Platforms[name]
		fmt.Printf("  [%d] %-12s → ~/%s\n", i+1, name, entry.InstallPath)
	}
	fmt.Println("  [q] quit")
	fmt.Print("\nSelect platform: ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return ""
	}
	input := strings.TrimSpace(scanner.Text())
	if input == "q" || input == "" {
		return ""
	}
	index, err := strconv.Atoi(input)
	if err != nil || index < 1 || index > len(names) {
		fmt.Println("Invalid selection.")
		return ""
	}
	return names[index-1]
}

func installMCPConfig(mcpPath, configKey, fcgBinary string, serverCfg mcpServerConfig) error {
	var root map[string]any
	data, err := os.ReadFile(mcpPath)
	if err == nil {
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parse %s: %w", mcpPath, err)
		}
	} else {
		root = make(map[string]any)
	}

	servers, ok := root[configKey].(map[string]any)
	if !ok {
		servers = make(map[string]any)
	}

	// Only create fcg entry if it doesn't exist; if it exists, only update command path
	existing, exists := servers["fcg"]
	if exists {
		if m, ok := existing.(map[string]any); ok {
			m["command"] = fcgBinary
			servers["fcg"] = m
		}
	} else {
		servers["fcg"] = map[string]any{
			"command":  fcgBinary,
			"args":     serverCfg.Args,
			"env":      serverCfg.Env,
			"disabled": false,
		}
	}
	root[configKey] = servers

	if err := os.MkdirAll(filepath.Dir(mcpPath), 0755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(mcpPath, out, 0644)
}
