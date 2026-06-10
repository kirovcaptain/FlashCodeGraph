package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"time"

	"github.com/kirovcaptain/FlashCodeGraph/internal/config"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/service"
	"github.com/kirovcaptain/FlashCodeGraph/internal/status"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/branch"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/crossindex"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/lock"
	"github.com/spf13/cobra"
)

var (
	indexForce   bool
	indexBranch  string
	indexDebug   bool
	indexProfile bool
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
	indexCmd.Flags().BoolVar(&indexProfile, "profile", false, "Write CPU profile to .fcg/cpu.prof")
	rootCmd.AddCommand(indexCmd)
}

func runIndex(cmd *cobra.Command, args []string) error {
	repoPath := "."
	if len(args) > 0 {
		repoPath = args[0]
	}
	repoPath, _ = filepath.Abs(repoPath)

	// CPU profiling — output to .fcg/profile/ directory
	var profileStartTime time.Time
	var phaseTimelineFile *os.File
	if indexProfile {
		profileDir := filepath.Join(repoPath, ".fcg", "profile")
		os.MkdirAll(profileDir, 0o755)

		// CPU profile
		cpuProfilePath := filepath.Join(profileDir, "cpu.prof")
		cpuProfileFile, err := os.Create(cpuProfilePath)
		if err != nil {
			return fmt.Errorf("create profile file: %w", err)
		}
		defer cpuProfileFile.Close()
		if err := pprof.StartCPUProfile(cpuProfileFile); err != nil {
			return fmt.Errorf("start cpu profile: %w", err)
		}
		defer pprof.StopCPUProfile()

		profileStartTime = time.Now()

		// Memory trace — sample heap stats every second to CSV
		memTracePath := filepath.Join(profileDir, "mem_trace.csv")
		memTraceFile, err := os.Create(memTracePath)
		if err != nil {
			return fmt.Errorf("create mem trace file: %w", err)
		}
		fmt.Fprintln(memTraceFile, "elapsed_sec,heap_inuse_mb,heap_sys_mb,num_gc")
		traceStopChannel := make(chan struct{})
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					var memStats runtime.MemStats
					runtime.ReadMemStats(&memStats)
					fmt.Fprintf(memTraceFile, "%d,%d,%d,%d\n",
						int(time.Since(profileStartTime).Seconds()),
						memStats.HeapInuse/1024/1024,
						memStats.HeapSys/1024/1024,
						memStats.NumGC)
				case <-traceStopChannel:
					return
				}
			}
		}()
		defer func() {
			close(traceStopChannel)
			memTraceFile.Close()
		}()

		// Phase timeline
		phaseTimelinePath := filepath.Join(profileDir, "phase_timeline.csv")
		phaseTimelineFile, err = os.Create(phaseTimelinePath)
		if err != nil {
			return fmt.Errorf("create phase timeline file: %w", err)
		}
		fmt.Fprintln(phaseTimelineFile, "elapsed_sec,event,name,detail")
		defer phaseTimelineFile.Close()

		fmt.Printf("Profile output: %s\n", profileDir)
	}

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
	// Create cross-project index
	crossIndex, err := crossindex.New(cfg.CrossProjectIndex.Backend, cfg.CrossProjectIndex.SQLitePath, config.GlobalDir())
	if err != nil {
		return fmt.Errorf("load cross-project index: %w", err)
	}

	indexer := service.NewIndexer(store, fingerprintStore, indexLock, cfg, dump, crossIndex)

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
				if phaseTimelineFile != nil {
					elapsed := int(time.Since(profileStartTime).Seconds())
					fmt.Fprintf(phaseTimelineFile, "%d,substep_start,%s,\n", elapsed, event.SubStep)
				}
			} else {
				duration := time.Since(subStepStart).Round(time.Millisecond)
				if indexProfile {
					var memStats runtime.MemStats
					runtime.ReadMemStats(&memStats)
					fmt.Printf("  ├─ %-22s %s (%s) [Heap: %dMB, GC: %d]\n",
						event.SubStep, event.Message, duration,
						memStats.HeapInuse/1024/1024, memStats.NumGC)
					if phaseTimelineFile != nil {
						elapsed := int(time.Since(profileStartTime).Seconds())
						fmt.Fprintf(phaseTimelineFile, "%d,substep_end,%s,%s\n", elapsed, event.SubStep, event.Message)
					}
				} else {
					fmt.Printf("  ├─ %-22s %s (%s)\n", event.SubStep, event.Message, duration)
				}
			}
		} else if event.Message != "" {
			// Main phase complete — show duration for phases without sub-steps
			dur := ""
			if lastPhaseIndex == event.PhaseIndex && !phaseStart.IsZero() {
				elapsed := time.Since(phaseStart).Round(time.Millisecond)
				dur = fmt.Sprintf(" (%s)", elapsed)
			}
			fmt.Printf("[%d/%d] %-20s %s%s\n", event.PhaseIndex, event.PhaseTotal, event.Phase, event.Message, dur)
			if phaseTimelineFile != nil {
				elapsed := int(time.Since(profileStartTime).Seconds())
				fmt.Fprintf(phaseTimelineFile, "%d,phase_end,%s,%s\n", elapsed, event.Phase, event.Message)
			}
		} else {
			// Phase start — record time, don't print (completion line will show)
			phaseStart = time.Now()
			lastPhaseIndex = event.PhaseIndex
			if phaseTimelineFile != nil {
				elapsed := int(time.Since(profileStartTime).Seconds())
				fmt.Fprintf(phaseTimelineFile, "%d,phase_start,%s,\n", elapsed, event.Phase)
			}
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
