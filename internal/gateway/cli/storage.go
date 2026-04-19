package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/liuymcn/flash-code-graph/internal/config"
	"github.com/liuymcn/flash-code-graph/internal/model"
	"github.com/liuymcn/flash-code-graph/internal/storage"
	"github.com/liuymcn/flash-code-graph/internal/storage/branch"
	"github.com/liuymcn/flash-code-graph/internal/storage/falkor"
	"github.com/liuymcn/flash-code-graph/internal/storage/kuzu"
)

// openGraphStore creates a GraphStore based on config and project path.
// For KùzuDB, it resolves the data directory from project path + branch.
// For FalkorDB, it connects to the configured address.
func openGraphStore(cfg *config.Config, repoPath string) (storage.GraphStore, error) {
	database, address, graphName := storage.ResolveStorageAddress(cfg)

	switch database {
	case "falkordb":
		// Per-project + per-branch graph isolation
		absPath, _ := filepath.Abs(repoPath)
		if absPath != "" {
			projectName := filepath.Base(absPath)
			branchName := cfg.Storage.Branch
			if branchName == "" {
				branchName = branch.DetectBranch(absPath)
			}
			graphName = graphName + "_" + projectName + "_" + branchName
			// Sanitize to match FalkorDB internal graph name normalization
			graphName = strings.NewReplacer("-", "_", "/", "_").Replace(graphName)
		}
		store, err := falkor.New(address, graphName)
		if err != nil {
			return nil, fmt.Errorf("open FalkorDB (%s): %w", address, err)
		}
		return store, nil

	case "kuzu":
		dbPath := cfg.Storage.KuzuPath
		if dbPath == "" && repoPath != "" {
			// Auto-resolve: ~/.fcg/data/{project-hash}/{branch}/graph.kuzu
			absPath, _ := filepath.Abs(repoPath)
			fcgDir := config.GlobalDir()
			dataDir := storage.DataDir(fcgDir, absPath)
			branchName := cfg.Storage.Branch
			if branchName == "" {
				branchName = branch.DetectBranch(absPath)
			}
			dbPath = filepath.Join(dataDir, branchName, "graph.kuzu")
			os.MkdirAll(filepath.Dir(dbPath), model.DirectoryPermission)
		}

		store, err := kuzu.New(dbPath)
		if err != nil {
			// Fallback to in-memory if disk mode fails (e.g. WSL)
			store, err = kuzu.New("")
			if err != nil {
				return nil, fmt.Errorf("open KùzuDB: %w", err)
			}
		}
		if err := store.Migrate(context.Background()); err != nil {
			store.Close()
			return nil, fmt.Errorf("migrate KùzuDB: %w", err)
		}
		return store, nil

	default:
		return nil, fmt.Errorf("unsupported database: %s", database)
	}
}
