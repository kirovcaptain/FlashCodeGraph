package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/config"
	"github.com/spf13/cobra"
)

func init() {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage FCG configuration",
	}

	configCmd.AddCommand(
		&cobra.Command{
			Use:   "get <key>",
			Short: "Get a config value",
			Args:  cobra.ExactArgs(1),
			RunE:  configGet,
		},
		&cobra.Command{
			Use:   "set <key> <value>",
			Short: "Set a config value",
			Args:  cobra.ExactArgs(2),
			RunE:  configSet,
		},
		&cobra.Command{
			Use:   "list",
			Short: "List all config values",
			RunE:  configList,
		},
		&cobra.Command{
			Use:   "reset",
			Short: "Reset config to defaults",
			RunE:  configReset,
		},
		&cobra.Command{
			Use:   "edit",
			Short: "Open config in $EDITOR",
			RunE:  configEdit,
		},
	)

	rootCmd.AddCommand(configCmd)
}

func projectDir() string {
	dir, _ := os.Getwd()
	return dir
}

func configGet(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(projectDir())
	if err != nil {
		return err
	}
	val, err := config.Get(cfg, args[0])
	if err != nil {
		return err
	}
	fmt.Println(val)
	return nil
}

func configSet(cmd *cobra.Command, args []string) error {
	return config.Set(projectDir(), args[0], args[1])
}

func configList(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(projectDir())
	if err != nil {
		return err
	}
	keys := []string{
		"project.name", "project.type",
		"storage.database", "storage.neo4j_uri",
		"system.goroutines", "system.memory_limit", "system.log_level",
		"index.languages", "index.max_file_size",
		"embedding.enabled",
	}
	for _, key := range keys {
		val, _ := config.Get(cfg, key)
		fmt.Printf("%-25s = %s\n", key, val)
	}
	return nil
}

func configReset(cmd *cobra.Command, args []string) error {
	path := config.ProjectConfigPath(projectDir())
	cfg := config.DefaultConfig()
	if err := config.WriteConfig(path, cfg); err != nil {
		return err
	}
	fmt.Println("Config reset to defaults:", path)
	return nil
}

func configEdit(cmd *cobra.Command, args []string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	path := config.ProjectConfigPath(projectDir())
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("no config file found at %s, run 'fcg index .' first", path)
	}
	parts := strings.Fields(editor)
	c := exec.Command(parts[0], append(parts[1:], path)...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
