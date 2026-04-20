package storage

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kirovcaptain/FlashCodeGraph/internal/config"
)

// DefaultFalkorDBSocket returns the default FalkorDB Lite socket path.
func DefaultFalkorDBSocket() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".fcg", "falkordb.sock")
}

// DefaultFalkorDBAddress is the default FalkorDB TCP address.
const DefaultFalkorDBAddress = "localhost:6379"

// ResolveStorageAddress returns the connection address for the configured storage backend.
func ResolveStorageAddress(cfg *config.Config) (database string, address string, graphName string) {
	database = cfg.Storage.Database

	switch database {
	case "falkordb":
		address = cfg.Storage.FalkorDBURI
		if address == "" {
			socketPath := DefaultFalkorDBSocket()
			if _, err := os.Stat(socketPath); err == nil {
				address = socketPath
			} else {
				address = DefaultFalkorDBAddress
			}
		}
		graphName = cfg.Storage.FalkorDBGraph
		if graphName == "" {
			graphName = "fcg"
		}
	case "neo4j":
		address = cfg.Storage.Neo4jURI
	default:
		// kuzu — address is the data directory path
		database = "kuzu"
		address = ""
	}

	return database, address, graphName
}

// FormatStorageInfo returns a human-readable description of the storage backend.
func FormatStorageInfo(database, address string) string {
	switch database {
	case "falkordb":
		return fmt.Sprintf("FalkorDB (%s)", address)
	case "neo4j":
		return fmt.Sprintf("Neo4j (%s)", address)
	default:
		return "KùzuDB (in-memory)"
	}
}
