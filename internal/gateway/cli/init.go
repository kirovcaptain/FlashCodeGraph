package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kirovcaptain/FlashCodeGraph/internal/config"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/crossindex"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/falkor"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:     "init",
		Short:   "Initialize global FCG configuration (~/.fcg/config.toml)",
		GroupID: "manage",
		RunE:    runInit,
	})
}

func runInit(cmd *cobra.Command, args []string) error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println()
	fmt.Println("🔧 FCG Global Setup")
	fmt.Println()

	// Check existing config
	globalPath := config.GlobalConfigPath()
	if _, err := os.Stat(globalPath); err == nil {
		fmt.Printf("  Config already exists: %s\n", globalPath)
		fmt.Print("  Overwrite? [y/N]: ")
		line, _ := reader.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(line)) != "y" {
			fmt.Println("  Aborted.")
			return nil
		}
		fmt.Println()
	}

	// Step 1: Detect environment and recommend
	recommended := config.DetectDefaultDatabase()
	fmt.Println("  Storage backend:")
	if recommended == "falkordb" {
		fmt.Println("    [1] FalkorDB  (recommended — WSL detected)")
		fmt.Println("    [2] KùzuDB   (embedded, no external service)")
	} else {
		fmt.Println("    [1] FalkorDB  (requires running FalkorDB/Redis)")
		fmt.Println("    [2] KùzuDB   (recommended — embedded, no setup)")
	}
	fmt.Printf("  Select [%s]: ", map[string]string{"falkordb": "1", "kuzu": "2"}[recommended])
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)

	var database, falkordbURI string
	switch line {
	case "1", "falkordb":
		database = "falkordb"
	case "2", "kuzu":
		database = "kuzu"
	case "":
		database = recommended
	default:
		database = recommended
	}

	// Step 2: FalkorDB connection
	if database == "falkordb" {
		falkordbURI = selectInitFalkorDBConnection(reader)
		// Test connection with retry
		for attempt := 0; attempt < 2; attempt++ {
			fmt.Print("  Testing connection... ")
			_, err := falkor.NewClient(falkordbURI)
			if err == nil {
				fmt.Println("✅ Connected")
				break
			}
			fmt.Printf("❌ %v\n", err)
			if attempt == 0 {
				fmt.Printf("  URI [%s]: ", falkordbURI)
				newURI, _ := reader.ReadString('\n')
				newURI = strings.TrimSpace(newURI)
				if newURI != "" {
					falkordbURI = newURI
				}
			} else {
				fmt.Println()
				fmt.Println("  ❌ Cannot connect to FalkorDB. Please ensure it is running and re-run `fcg init`.")
				return nil
			}
		}
	}

	// Step 3: Cross-project index backend
	crossBackend, crossSQLitePath := selectCrossProjectIndex(reader)

	// Step 4: Write config
	cfg := config.DefaultConfig()
	cfg.Storage.Database = database
	if falkordbURI != "" {
		cfg.Storage.FalkorDBURI = falkordbURI
	}
	cfg.CrossProjectIndex.Backend = crossBackend
	cfg.CrossProjectIndex.SQLitePath = crossSQLitePath

	if err := config.WriteConfig(globalPath, cfg); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	fmt.Println()
	fmt.Printf("  ✅ Config saved to %s\n", globalPath)
	fmt.Printf("  Storage: %s", database)
	if falkordbURI != "" {
		fmt.Printf(" (%s)", falkordbURI)
	}
	fmt.Println()
	crossDisplay := crossBackend
	if crossBackend == "sqlite" {
		p := crossSQLitePath
		if p == "" {
			p = crossindex.DefaultSQLitePath(config.GlobalDir())
		}
		crossDisplay += " (" + p + ")"
	}
	fmt.Printf("  Cross-project index: %s\n", crossDisplay)
	fmt.Println()
	fmt.Println("  Next: run `fcg index <project-path>` to index your first project.")
	fmt.Println()
	return nil
}

func selectInitFalkorDBConnection(reader *bufio.Reader) string {
	socketPath := storage.DefaultFalkorDBSocket()
	hasSocket := false
	if _, err := os.Stat(socketPath); err == nil {
		hasSocket = true
	}

	fmt.Println()
	fmt.Println("  FalkorDB connection:")
	fmt.Println("    [1] TCP localhost:6379")
	if hasSocket {
		fmt.Printf("    [2] Unix socket %s\n", socketPath)
	}
	fmt.Println("    [3] Custom address")

	defaultChoice := "1"
	if hasSocket {
		defaultChoice = "2"
	}
	fmt.Printf("  Select [%s]: ", defaultChoice)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		line = defaultChoice
	}

	switch line {
	case "1":
		return "localhost:6379"
	case "2":
		if hasSocket {
			return socketPath
		}
		return "localhost:6379"
	case "3":
		fmt.Print("  Address (host:port or /path/to/socket.sock): ")
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

// selectCrossProjectIndex lets user choose cross-project index backend.
func selectCrossProjectIndex(reader *bufio.Reader) (backend, sqlitePath string) {
	fmt.Println()
	fmt.Println("  Cross-project index backend:")
	fmt.Println("    [1] SQLite  (recommended — concurrent, incremental)")
	fmt.Println("    [2] JSON    (simple file)")
	fmt.Print("  Select [1]: ")
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)

	switch line {
	case "2", "json":
		return "json", ""
	default:
		backend = "sqlite"
	}

	defaultPath := crossindex.DefaultSQLitePath(config.GlobalDir())
	fmt.Printf("  SQLite path [%s]: ", defaultPath)
	line, _ = reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line != "" {
		sqlitePath = line
	}

	// Validate path writable
	targetPath := sqlitePath
	if targetPath == "" {
		targetPath = defaultPath
	}
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Printf("  ⚠️  Cannot create directory %s: %v\n", dir, err)
	} else {
		fmt.Println("  ✅ Path OK")
	}

	return backend, sqlitePath
}
