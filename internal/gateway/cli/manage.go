package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kirovcaptain/FlashCodeGraph/internal/config"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/status"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
	"github.com/spf13/cobra"
)

func init() {
	// fcg overview
	overviewCmd := &cobra.Command{
		Use:   "overview",
		GroupID: "manage",
		Short: "Show project overview statistics",
		RunE:  runOverview,
	}
	rootCmd.AddCommand(overviewCmd)

	// fcg report
	reportCmd := &cobra.Command{
		Use:   "report",
		GroupID: "manage",
		Short: "Data quality report — duplicates, missing fields, route/query details",
		RunE:  runReport,
	}
	rootCmd.AddCommand(reportCmd)

	// fcg remove
	// fcg remove --id <number> --force
	removeCmd := &cobra.Command{
		Use:   "remove",
		GroupID: "manage",
		Short: "Remove project index data by ID (use 'fcg list' to see IDs)",
		RunE:  runRemove,
	}
	removeCmd.Flags().Bool("force", false, "Confirm deletion")
	removeCmd.Flags().Int("id", 0, "Project ID from 'fcg list'")
	removeCmd.Flags().Bool("graph", false, "Only delete graph data")
	removeCmd.Flags().Bool("cache", false, "Only delete cache and fingerprints")
	rootCmd.AddCommand(removeCmd)

	// fcg list
	listCmd := &cobra.Command{
		Use:   "list",
		GroupID: "manage",
		Short: "List all indexed projects",
		RunE:  runList,
	}
	rootCmd.AddCommand(listCmd)

	// fcg status
	statusCmd := &cobra.Command{
		Use:   "status",
		GroupID: "manage",
		Short: "Show index status for current project",
		RunE:  runStatus,
	}
	rootCmd.AddCommand(statusCmd)
}

func runOverview(cmd *cobra.Command, args []string) error {
	querier, store, err := createQuerier()
	if err != nil {
		return err
	}
	defer store.Close()

	stats, err := querier.Overview(context.Background())
	if err != nil {
		return err
	}

	fmt.Println("Project Overview:")
	fmt.Printf("  Nodes:  %d\n", stats.NodeCount)
	fmt.Printf("  Edges:  %d\n", stats.EdgeCount)
	fmt.Printf("  Files:  %d\n", stats.FileCount)
	if len(stats.NodesByKind) > 0 {
		fmt.Println("  By kind:")
		for kind, count := range stats.NodesByKind {
			fmt.Printf("    %-15s %d\n", kind, count)
		}
	}
	if len(stats.EdgesByKind) > 0 {
		fmt.Println("  By edge:")
		for kind, count := range stats.EdgesByKind {
			fmt.Printf("    %-15s %d\n", kind, count)
		}
	}
	if len(stats.FilesByLang) > 0 {
		fmt.Println("  By language:")
		for lang, count := range stats.FilesByLang {
			fmt.Printf("    %-15s %d\n", lang, count)
		}
	}
	return nil
}

func runRemove(cmd *cobra.Command, args []string) error {
	id, _ := cmd.Flags().GetInt("id")
	graphOnly, _ := cmd.Flags().GetBool("graph")
	cacheOnly, _ := cmd.Flags().GetBool("cache")

	if graphOnly && cacheOnly {
		return fmt.Errorf("--graph and --cache are mutually exclusive, use --id for full removal")
	}

	// --graph or --cache without --id: operate on current directory
	if id == 0 && (graphOnly || cacheOnly) {
		return runRemoveByPath(cmd, projectDir(), graphOnly, cacheOnly)
	}

	// Full removal requires --id
	if id == 0 {
		return fmt.Errorf("specify --id <number>. Use 'fcg list' to see project IDs")
	}

	fcgDir := config.GlobalDir()
	registry, err := storage.NewRegistry(fcgDir)
	if err != nil {
		return fmt.Errorf("load registry: %w", err)
	}

	entries := registry.List()
	if id < 1 || id > len(entries) {
		return fmt.Errorf("invalid ID %d. Use 'fcg list' to see valid IDs (1-%d)", id, len(entries))
	}
	entry := entries[id-1]

	force, _ := cmd.Flags().GetBool("force")
	if !force {
		fmt.Printf("⚠ This will delete all index data for project: %s (%s). Use --force to confirm.\n", entry.Name, entry.Path)
		return nil
	}

	absPath := entry.Path
	if !filepath.IsAbs(absPath) {
		absPath, _ = filepath.Abs(absPath)
	}

	// Remove graph data
	cfg, err := config.Load(entry.Path)
	if err != nil {
		cfg = config.DefaultConfig()
	}
	store, err := openGraphStore(cfg, entry.Path)
	if err != nil {
		fmt.Printf("⚠ Graph removal skipped: %v\n", err)
	} else {
		store.ClearAll(context.Background())
		store.Close()
		fmt.Println("✅ Graph data removed")
	}

	// Remove cache + fingerprints
	cacheDir := filepath.Join(absPath, ".fcg", "cache")
	os.RemoveAll(cacheDir)
	fingerprintDir := filepath.Join(absPath, ".fcg", "fingerprints")
	os.RemoveAll(fingerprintDir)
	fmt.Println("✅ Parse cache and fingerprints removed")

	// Remove registry entry
	registry.Unregister(entry.Path)
	fmt.Println("✅ Registry entry removed")

	return nil
}

func runRemoveByPath(cmd *cobra.Command, repoPath string, graphOnly, cacheOnly bool) error {
	force, _ := cmd.Flags().GetBool("force")
	absPath, _ := filepath.Abs(repoPath)

	if !force {
		fmt.Printf("⚠ This will delete data for project in: %s. Use --force to confirm.\n", absPath)
		return nil
	}

	if graphOnly {
		cfg, err := config.Load(repoPath)
		if err != nil {
			cfg = config.DefaultConfig()
		}
		store, err := openGraphStore(cfg, repoPath)
		if err != nil {
			fmt.Printf("⚠ Graph removal skipped: %v\n", err)
		} else {
			store.ClearAll(context.Background())
			store.Close()
			fmt.Println("✅ Graph data removed")
		}
	}

	if cacheOnly {
		cacheDir := filepath.Join(absPath, ".fcg", "cache")
		os.RemoveAll(cacheDir)
		fingerprintDir := filepath.Join(absPath, ".fcg", "fingerprints")
		os.RemoveAll(fingerprintDir)
		fmt.Println("✅ Parse cache and fingerprints removed")
	}

	return nil
}

func runReport(cmd *cobra.Command, args []string) error {
	querier, store, err := createQuerier()
	if err != nil {
		return err
	}
	defer store.Close()

	report, err := querier.Report(context.Background())
	if err != nil {
		return err
	}

	fmt.Println("=== Data Quality Report ===")
	fmt.Println()

	fmt.Println("Nodes:")
	for kind, count := range report.NodeCounts {
		if count > 0 {
			fmt.Printf("  %-15s %d\n", kind, count)
		}
	}

	if len(report.RouteDetails) > 0 {
		fmt.Printf("\nRoutes (%d):\n", len(report.RouteDetails))
		for _, r := range report.RouteDetails {
			fmt.Printf("  %-6s %-30s → %s\n", r.Method, r.PathPattern, r.Handler)
		}
	}

	if len(report.QueryDetails) > 0 {
		fmt.Printf("\nQueries (%d):\n", len(report.QueryDetails))
		for _, q := range report.QueryDetails {
			sql := q.SQLText
			if len(sql) > 60 {
				sql = sql[:60] + "..."
			}
			fmt.Printf("  %-8s tables=%-20s %s\n", q.QueryType, q.Tables, sql)
		}
	}

	fmt.Println()
	if len(report.Issues) == 0 {
		fmt.Println("✅ No data quality issues found")
	} else {
		for _, issue := range report.Issues {
			fmt.Printf("⚠ %s\n", issue)
		}
	}

	// Save reports
	repoPath := projectDir()
	absPath, _ := filepath.Abs(repoPath)
	fcgDir := filepath.Join(absPath, ".fcg")
	os.MkdirAll(fcgDir, 0o755)

	// JSON report
	jsonData, _ := json.Marshal(report)
	jsonPath := filepath.Join(fcgDir, "report.json")
	os.WriteFile(jsonPath, jsonData, 0o644)

	// Markdown report
	mdContent := generateReportMarkdown(report)
	mdPath := filepath.Join(fcgDir, "report.md")
	os.WriteFile(mdPath, []byte(mdContent), 0o644)

	fmt.Printf("\nReports saved to:\n  %s (raw data)\n  %s (readable)\n", jsonPath, mdPath)
	return nil
}

func runList(cmd *cobra.Command, args []string) error {
	fcgDir := config.GlobalDir()
	registry, err := storage.NewRegistry(fcgDir)
	if err != nil {
		return fmt.Errorf("load registry: %w", err)
	}

	entries := registry.List()
	if len(entries) == 0 {
		fmt.Println("No indexed projects. Run 'fcg index <path>' to index a project.")
		return nil
	}

	fmt.Printf("Indexed projects (%d):\n\n", len(entries))
	fmt.Printf("  %-4s %-20s %-10s %-12s %s\n", "ID", "NAME", "BRANCH", "DATABASE", "PATH")
	fmt.Printf("  %-4s %-20s %-10s %-12s %s\n", "--", "----", "------", "--------", "----")
	for i, e := range entries {
		branch := e.Branch
		if branch == "" {
			branch = "-"
		}
		fmt.Printf("  %-4d %-20s %-10s %-12s %s\n", i+1, e.Name, branch, e.Database, e.Path)
	}
	return nil
}

func runStatus(cmd *cobra.Command, args []string) error {
	repoPath := projectDir()
	absPath, _ := filepath.Abs(repoPath)
	projectName := filepath.Base(absPath)

	fcgDir := config.GlobalDir()
	registry, err := storage.NewRegistry(fcgDir)
	if err != nil {
		return fmt.Errorf("load registry: %w", err)
	}

	entry := registry.FindByPath(absPath)
	if entry == nil {
		fmt.Printf("Project %q is not indexed.\n", projectName)
		fmt.Println("Run 'fcg index .' to index this project.")
		return nil
	}

	fmt.Printf("Project: %s\n", entry.Name)
	fmt.Printf("Path:    %s\n", entry.Path)
	fmt.Printf("DB:      %s\n", entry.Database)

	s := status.Read(absPath)
	if s.IndexTimestamp > 0 {
		fmt.Printf("Indexed: %s\n", time.Unix(s.IndexTimestamp, 0).Format("2006-01-02 15:04:05"))
	}
	if s.AnalyzeTimestamp > 0 {
		fmt.Printf("Analyzed: %s\n", time.Unix(s.AnalyzeTimestamp, 0).Format("2006-01-02 15:04:05"))
	} else {
		fmt.Println("Analyzed: not yet (run 'fcg analyze')")
	}

	querier, store, err := createQuerier()
	if err != nil {
		fmt.Println("Status:  indexed (cannot connect to database)")
		return nil
	}
	defer store.Close()

	stats, err := querier.Overview(context.Background())
	if err != nil {
		fmt.Println("Status:  indexed (cannot read stats)")
		return nil
	}

	fmt.Printf("Nodes:   %d\n", stats.NodeCount)
	fmt.Printf("Edges:   %d\n", stats.EdgeCount)
	if len(stats.NodesByKind) > 0 {
		fmt.Println("Symbols:")
		for kind, count := range stats.NodesByKind {
			if count > 0 {
				fmt.Printf("  %-15s %d\n", kind, count)
			}
		}
	}

	return nil
}

func generateReportMarkdown(r *model.GraphReport) string {
	var b strings.Builder
	b.WriteString("# FCG Data Quality Report\n\n")

	b.WriteString("## Node Summary\n\n")
	b.WriteString("| Kind | Count |\n|------|-------|\n")
	total := 0
	for kind, count := range r.NodeCounts {
		if count > 0 {
			fmt.Fprintf(&b, "| %s | %d |\n", kind, count)
			total += count
		}
	}
	fmt.Fprintf(&b, "| **Total** | **%d** |\n\n", total)

	if len(r.EdgeCounts) > 0 {
		b.WriteString("## Edge Summary\n\n")
		b.WriteString("| Relation | Count |\n|----------|-------|\n")
		edgeTotal := 0
		for kind, count := range r.EdgeCounts {
			if count > 0 {
				fmt.Fprintf(&b, "| %s | %d |\n", kind, count)
				edgeTotal += count
			}
		}
		fmt.Fprintf(&b, "| **Total** | **%d** |\n\n", edgeTotal)
	}

	if len(r.RouteDetails) > 0 {
		fmt.Fprintf(&b, "## Routes (%d)\n\n", len(r.RouteDetails))
		b.WriteString("| Method | Path | Handler |\n|--------|------|---------|\n")
		for _, rt := range r.RouteDetails {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", rt.Method, rt.PathPattern, rt.Handler)
		}
		b.WriteString("\n")
	}

	if len(r.QueryDetails) > 0 {
		fmt.Fprintf(&b, "## ORM Queries (%d)\n\n", len(r.QueryDetails))
		b.WriteString("| Type | Tables | Caller | SQL |\n|------|--------|--------|-----|\n")
		for _, q := range r.QueryDetails {
			sql := q.SQLText
			if len(sql) > 50 {
				sql = sql[:50] + "..."
			}
			sql = strings.ReplaceAll(sql, "|", "\\|")
			sql = strings.ReplaceAll(sql, "\n", " ")
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", q.QueryType, q.Tables, q.Caller, sql)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Data Quality\n\n")
	if len(r.Issues) == 0 {
		b.WriteString("✅ No issues found.\n\n")
	} else {
		for _, issue := range r.Issues {
			fmt.Fprintf(&b, "- ⚠ %s\n", issue)
		}
		b.WriteString("\n")
	}

	if len(r.DuplicateNodes) > 0 {
		fmt.Fprintf(&b, "### Duplicate Nodes (%d)\n\n", len(r.DuplicateNodes))
		for _, id := range r.DuplicateNodes {
			fmt.Fprintf(&b, "- `%s`\n", id)
		}
		b.WriteString("\n")
	}

	if len(r.MissingFilePath) > 0 {
		fmt.Fprintf(&b, "### Missing file_path (%d)\n\n", len(r.MissingFilePath))
		for _, id := range r.MissingFilePath {
			fmt.Fprintf(&b, "- `%s`\n", id)
		}
		b.WriteString("\n")
	}

	return b.String()
}
