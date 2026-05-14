package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kirovcaptain/FlashCodeGraph/internal/config"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/branch"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/falkor"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/kuzu"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/ladybug"
)

// openGraphStore creates a GraphStore based on config and project path.
// For KùzuDB, it resolves the data directory from project path + branch.
// For FalkorDB, it connects to the configured address.
func openGraphStore(cfg *config.Config, repoPath string) (storage.GraphStore, error) {
	database, address, _ := storage.ResolveStorageAddress(cfg)

	// Parse buffer pool size from config for embedded databases
	var bufferPoolSize uint64
	if cfg.Storage.BufferPoolSize != "" {
		if parsed, err := config.ParseMemoryLimit(cfg.Storage.BufferPoolSize); err == nil && parsed > 0 {
			bufferPoolSize = uint64(parsed)
		}
	}

	switch database {
	case "falkordb":
		graphName := falkor.ResolveGraphName(cfg, repoPath)
		store, err := falkor.New(address, graphName)
		if err != nil {
			return nil, fmt.Errorf("open FalkorDB (%s): %w", address, err)
		}
		return store, nil

	case "kuzu":
		dbPath := cfg.Storage.KuzuPath
		if dbPath == "" && repoPath != "" {
			absPath, _ := filepath.Abs(repoPath)
			projectName := filepath.Base(absPath)
			branchName := cfg.Storage.Branch
			if branchName == "" {
				branchName = branch.DetectBranch(absPath)
			}
			dbPath = filepath.Join(cfg.ResolveDataDir(), projectName, branchName, "graph.kuzu")
			os.MkdirAll(filepath.Dir(dbPath), model.DirectoryPermission)
		}

		store, err := kuzu.New(dbPath, bufferPoolSize)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[warn] KùzuDB disk mode failed (%s): %v, falling back to in-memory\n", dbPath, err)
			store, err = kuzu.New("", bufferPoolSize)
			if err != nil {
				return nil, fmt.Errorf("open KùzuDB: %w", err)
			}
		}
		if err := store.Migrate(context.Background()); err != nil {
			store.Close()
			return nil, fmt.Errorf("migrate KùzuDB: %w", err)
		}
		return store, nil

	case "ladybug":
		dbPath := cfg.Storage.LadybugPath
		if dbPath == "" && repoPath != "" {
			absPath, _ := filepath.Abs(repoPath)
			projectName := filepath.Base(absPath)
			branchName := cfg.Storage.Branch
			if branchName == "" {
				branchName = branch.DetectBranch(absPath)
			}
			dbPath = filepath.Join(cfg.ResolveDataDir(), projectName, branchName, "graph.ladybug")
			os.MkdirAll(filepath.Dir(dbPath), model.DirectoryPermission)
		}

		lbStore, err := ladybug.New(dbPath, bufferPoolSize)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[warn] LadybugDB disk mode failed (%s): %v, falling back to in-memory\n", dbPath, err)
			lbStore, err = ladybug.New("", bufferPoolSize)
			if err != nil {
				return nil, fmt.Errorf("open LadybugDB: %w", err)
			}
		}
		if err := lbStore.Migrate(context.Background()); err != nil {
			lbStore.Close()
			return nil, fmt.Errorf("migrate LadybugDB: %w", err)
		}
		return lbStore, nil

	default:
		return nil, fmt.Errorf("unsupported database: %s", database)
	}
}
