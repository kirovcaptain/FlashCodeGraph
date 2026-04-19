package cli

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:     "fcg",
	Short:   "FlashCodeGraph — code knowledge graph",
	Version: "0.1.0",
}

func init() {
	rootCmd.AddGroup(
		&cobra.Group{ID: "index", Title: "Indexing:"},
		&cobra.Group{ID: "query", Title: "Querying:"},
		&cobra.Group{ID: "manage", Title: "Management:"},
		&cobra.Group{ID: "agent", Title: "AI Agent:"},
	)
	rootCmd.CompletionOptions.HiddenDefaultCmd = true
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}
