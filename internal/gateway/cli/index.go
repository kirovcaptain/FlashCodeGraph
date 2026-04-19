package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/liuymcn/flash-code-graph/internal/config"
	"github.com/liuymcn/flash-code-graph/internal/model"
	"github.com/liuymcn/flash-code-graph/internal/service"
	"github.com/liuymcn/flash-code-graph/internal/status"
	"github.com/liuymcn/flash-code-graph/internal/storage"
	"github.com/liuymcn/flash-code-graph/internal/storage/branch"
	"github.com/liuymcn/flash-code-graph/internal/storage/lock"
	"github.com/spf13/cobra"
)

var (
	indexForce  bool
	indexBranch string
	indexDebug  bool
)

func init() {
	indexCmd := &cobra.Command{
		Use:   "index [path]",
		GroupID: "index",
		Short: "Index a code repository",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runIndex,
	}
	indexCmd.Flags().BoolVar(&indexForce, "force", false, "Force full re-index")
	indexCmd.Flags().StringVar(&indexBranch, "branch", "", "Branch name (auto-detected if not set)")
	indexCmd.Flags().BoolVar(&indexDebug, "debug", false, "Dump debug data to .fcg/debug/")
	rootCmd.AddCommand(indexCmd)
}

func runIndex(cmd *cobra.Command, args []string) error {
	repoPath := "."
	if len(args) > 0 {
		repoPath = args[0]
	}
	repoPath, _ = filepath.Abs(repoPath)

	// Auto-init if needed
	if err := service.AutoInit(repoPath); err != nil {
		fmt.Printf("⚠ Auto-init warning: %v\n", err)
	}

	// Ensure .fcg/ is in .gitignore on first index
	fcgLocalDir := filepath.Join(repoPath, ".fcg")
	if _, err := os.Stat(fcgLocalDir); os.IsNotExist(err) {
		if modified, _ := service.EnsureFcgIgnored(repoPath); modified {
			fmt.Fprintf(os.Stderr, "ℹ️  Added '.fcg/' to .gitignore\n")
		}
	}

	// Load config
	cfg, err := config.Load(repoPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Detect branch
	branchName := indexBranch
	if branchName == "" {
		branchName = branch.DetectBranch(repoPath)
	}

	// Setup storage
	fcgDir := config.GlobalDir()
	dataDir := filepath.Join(repoPath, ".fcg", "fingerprints")
	branchManager := branch.NewManager(dataDir)
	branchManager.EnsureBranchDir(branchName)

	store, err := openGraphStore(cfg, repoPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	ctx := context.Background()

	fingerprintStore := branchManager.FingerprintStore()
	indexLock := lock.NewNoopLock()

	// Create indexer with optional debug dump
	var dump service.DumpManager
	if indexDebug {
		dump = service.NewFileDumpManager(repoPath)
	}
	indexer := service.NewIndexer(store, fingerprintStore, indexLock, cfg, dump)

	database, address, _ := storage.ResolveStorageAddress(cfg)
	fmt.Printf("Indexing: %s (branch: %s, db: %s)\n", repoPath, branchName, storage.FormatStorageInfo(database, address))

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
			// Main phase complete — show duration for phases without sub-steps
			dur := ""
			if lastPhaseIndex == event.PhaseIndex && !phaseStart.IsZero() {
				elapsed := time.Since(phaseStart).Round(time.Millisecond)
				dur = fmt.Sprintf(" (%s)", elapsed)
			}
			fmt.Printf("[%d/%d] %-20s %s%s\n", event.PhaseIndex, event.PhaseTotal, event.Phase, event.Message, dur)
		} else {
			// Phase start — record time, don't print (completion line will show)
			phaseStart = time.Now()
			lastPhaseIndex = event.PhaseIndex
		}
	}

	result, err := indexer.Index(ctx, repoPath, branchName, indexForce, progressCallback)
	if err != nil {
		return fmt.Errorf("indexing failed: %w", err)
	}

	// Register project after successful indexing
	registry, _ := storage.NewRegistry(fcgDir)
	if registry != nil {
		registry.Register(cfg.Project.Name, repoPath, cfg.Storage.Database, branchName)
	}

	// Mark index timestamp
	status.MarkIndexed(repoPath)

	// Print summary
	duration := time.Duration(result.DurationMs) * time.Millisecond
	fmt.Printf("\n✓ Indexed in %s\n", duration.Round(time.Millisecond))
	fmt.Printf("  Files:     %d processed, %d skipped\n", result.FilesProcessed, result.FilesSkipped)
	fmt.Printf("  Symbols:   %d", result.SymbolsCreated)
	if len(result.SymbolsByKind) > 0 {
		fmt.Printf(" (")
		first := true
		for kind, count := range result.SymbolsByKind {
			if !first {
				fmt.Printf(", ")
			}
			fmt.Printf("%s %d", kind, count)
			first = false
		}
		fmt.Printf(")")
	}
	fmt.Println()
	fmt.Printf("  Relations: %d", result.RelationsCreated)
	if len(result.RelationsByKind) > 0 {
		fmt.Printf(" (")
		first := true
		for kind, count := range result.RelationsByKind {
			if !first {
				fmt.Printf(", ")
			}
			fmt.Printf("%s %d", kind, count)
			first = false
		}
		fmt.Printf(")")
	}
	fmt.Println()

	if result.AnnotationCount > 0 {
		fmt.Printf("  Annotations: %d\n", result.AnnotationCount)
	}

	if len(result.Errors) > 0 {
		fmt.Printf("  ⚠ %d parse errors\n", len(result.Errors))
	}
	if result.LowConfidenceCount > 0 {
		fmt.Printf("  ⚠ %d low confidence calls (< 0.5)\n", result.LowConfidenceCount)
	}

	return nil
}
