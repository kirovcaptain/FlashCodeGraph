package cli

import (
	"fmt"

	"github.com/liuymcn/flash-code-graph/internal/config"
	fcgmcp "github.com/liuymcn/flash-code-graph/internal/gateway/mcp"
	"github.com/liuymcn/flash-code-graph/internal/storage"
	"github.com/spf13/cobra"
)

func init() {
	mcpCmd := &cobra.Command{
		Use:   "mcp",
		GroupID: "agent",
		Short: "MCP Server commands",
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

	// Factory for creating stores for any project path
	storeFactory := func(projectConfig *config.Config, projectPath string) (storage.GraphStore, error) {
		return openGraphStore(projectConfig, projectPath)
	}

	database, address, _ := storage.ResolveStorageAddress(cfg)
	fmt.Fprintf(cmd.ErrOrStderr(), "FCG MCP Server starting (db: %s)\n", storage.FormatStorageInfo(database, address))

	srv := fcgmcp.NewServer(storeFactory)
	return srv.ServeStdio()
}
