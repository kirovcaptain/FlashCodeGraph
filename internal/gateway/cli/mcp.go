package cli

import (
	"fmt"

	"github.com/kirovcaptain/FlashCodeGraph/internal/config"
	fcgmcp "github.com/kirovcaptain/FlashCodeGraph/internal/gateway/mcp"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/falkor"
	"github.com/spf13/cobra"
)

func init() {
	mcpCmd := &cobra.Command{
		Use:     "mcp",
		GroupID: "agent",
		Short:   "MCP Server commands",
	}

	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start MCP Server (stdio transport)",
		RunE:  runMCPServe,
	}

	mcpCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(mcpCmd)
}

func runMCPServe(cmd *cobra.Command, args []string) error {
	cfg, _ := config.Load(projectDir())
	database, address, _ := storage.ResolveStorageAddress(cfg)

	fmt.Fprintf(cmd.ErrOrStderr(), "FCG MCP Server starting (db: %s)\n", storage.FormatStorageInfo(database, address))

	var storeFactory fcgmcp.StoreFactory
	switch database {
	case "falkordb":
		client, err := falkor.NewClient(address)
		if err != nil {
			return fmt.Errorf("connect FalkorDB (%s): %w", address, err)
		}
		defer client.Close()
		storeFactory = func(projectConfig *config.Config, projectPath string) (storage.GraphStore, error) {
			graphName := falkor.ResolveGraphName(projectConfig, projectPath)
			return falkor.NewWithClient(client, graphName), nil
		}
	default:
		storeFactory = func(projectConfig *config.Config, projectPath string) (storage.GraphStore, error) {
			return openGraphStore(projectConfig, projectPath)
		}
	}

	srv := fcgmcp.NewServer(storeFactory)
	return srv.ServeStdio()
}
