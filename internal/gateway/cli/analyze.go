package cli

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/service"
	"github.com/kirovcaptain/FlashCodeGraph/internal/status"
	"github.com/spf13/cobra"
)

func init() {
	analyzeCmd := &cobra.Command{
		Use:     "analyze [scope]",
		GroupID: "query",
		Short:   "Analyze code graph: entry points and processes",
		Long:    "Scopes: entries, process. No scope = all.",
		Args:    cobra.MaximumNArgs(1),
		RunE:    runAnalyze,
	}
	analyzeCmd.Flags().Int("depth", 10, "Max trace depth for process analysis")
	analyzeCmd.Flags().Bool("force", false, "Re-analyze even if up-to-date")
	rootCmd.AddCommand(analyzeCmd)
	registerListEntries()
}

func runAnalyze(cmd *cobra.Command, args []string) error {
	scope := "all"
	if len(args) > 0 {
		scope = args[0]
	}
	maxDepth, _ := cmd.Flags().GetInt("depth")
	force, _ := cmd.Flags().GetBool("force")

	_, store, err := createQuerier()
	if err != nil {
		return err
	}
	defer store.Close()

	// Progress callback — same style as index
	var phaseStart time.Time
	var subStepStart time.Time
	var lastPhaseIndex int
	progressCallback := func(event model.ProgressEvent) {
		if event.PhaseIndex == 0 {
			return
		}
		if event.SubStep != "" {
			if event.Message == "" {
				subStepStart = time.Now()
			} else {
				dur := time.Since(subStepStart).Round(time.Millisecond)
				fmt.Printf("  ├─ %-22s %s (%s)\n", event.SubStep, event.Message, dur)
			}
		} else if event.Message != "" {
			dur := ""
			if lastPhaseIndex == event.PhaseIndex && !phaseStart.IsZero() {
				elapsed := time.Since(phaseStart).Round(time.Millisecond)
				dur = fmt.Sprintf(" (%s)", elapsed)
			}
			fmt.Printf("[%d/%d] %-22s %s%s\n", event.PhaseIndex, event.PhaseTotal, event.Phase, event.Message, dur)
		} else {
			phaseStart = time.Now()
			lastPhaseIndex = event.PhaseIndex
		}
	}

	analyzer := service.NewAnalyzer(store, progressCallback)
	entriesOnly := scope == "entries"
	analyzer.SetAnalyzeMode(entriesOnly)
	ctx := context.Background()

	if !force && !status.NeedsAnalyze(projectDir()) {
		fmt.Println("✓ Analysis is up-to-date (index unchanged since last analyze). Use --force to re-analyze.")
		return nil
	}

	start := time.Now()

	forest, err := analyzer.BuildCallForest(ctx)
	if err != nil {
		return err
	}

	if scope == "all" || scope == "entries" || scope == "process" {
		entries, err := analyzer.ClassifyRoots(ctx, forest)
		if err != nil {
			return err
		}

		if err := analyzer.WriteEntryPoints(ctx, entries); err != nil {
			return fmt.Errorf("write entry points: %w", err)
		}

		if scope == "all" || scope == "process" {
			layerMap := analyzer.BuildLayerMap(ctx)
			analyzer.ClearAnalysisData(ctx)
			analyzer.WriteProcesses(ctx, entries, forest, maxDepth, layerMap)
		}
	}

	fmt.Printf("\n✓ Analyzed in %s\n", time.Since(start).Round(time.Millisecond))
	status.MarkAnalyzed(projectDir())
	return nil
}

func registerListEntries() {
	listEntriesCmd := &cobra.Command{
		Use:     "list-entries [type]",
		GroupID: "query",
		Short:   "List entry points (run 'fcg analyze entries' first)",
		Long:    "Types: http_endpoint, cli_command, suspected_dead, unknown_entry",
		Args:    cobra.MaximumNArgs(1),
		RunE:    runListEntries,
	}
	rootCmd.AddCommand(listEntriesCmd)
}

func runListEntries(cmd *cobra.Command, args []string) error {
	filterType := ""
	if len(args) > 0 {
		filterType = args[0]
	}

	_, store, err := createQuerier()
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()

	// Read persisted entry_type from Function nodes (written by 'fcg analyze')
	funcs, err := store.QueryAllByKind(ctx, "Function", 0)
	if err != nil {
		return err
	}

	type entryInfo struct {
		QualifiedName, EntryType, RouteMethod, RoutePath string
	}
	var entries []entryInfo

	// Batch-load route info: Function → HANDLES → Route
	handlesEdges, _ := store.QueryAllEdges(ctx, "HANDLES", 0)
	routeNodes, _ := store.QueryAllByKind(ctx, "Route", 0)
	routeMap := make(map[string]*model.Node, len(routeNodes))
	for i := range routeNodes {
		routeMap[routeNodes[i].ID] = &routeNodes[i]
	}
	funcRoute := make(map[string][2]string) // funcID → [method, path]
	for _, edge := range handlesEdges {
		if rn := routeMap[edge.TargetID]; rn != nil {
			method, _ := rn.Properties["method"].(string)
			path, _ := rn.Properties["path_pattern"].(string)
			funcRoute[edge.SourceID] = [2]string{method, path}
		}
	}

	for _, fn := range funcs {
		entryType, _ := fn.Properties["entry_type"].(string)
		if entryType == "" {
			continue
		}
		if filterType != "" && entryType != filterType {
			continue
		}
		qn, _ := fn.Properties["qualified_name"].(string)
		info := entryInfo{
			QualifiedName: qn,
			EntryType:     entryType,
		}
		if route, ok := funcRoute[fn.ID]; ok {
			info.RouteMethod = route[0]
			info.RoutePath = route[1]
		}
		entries = append(entries, info)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].EntryType != entries[j].EntryType {
			return entries[i].EntryType < entries[j].EntryType
		}
		return entries[i].QualifiedName < entries[j].QualifiedName
	})

	for _, entry := range entries {
		if entry.RouteMethod != "" || entry.RoutePath != "" {
			fmt.Printf("  %-18s %-6s %-30s %s\n", entry.EntryType, entry.RouteMethod, entry.RoutePath, entry.QualifiedName)
		} else {
			fmt.Printf("  %-18s %s\n", entry.EntryType, entry.QualifiedName)
		}
	}
	return nil
}
