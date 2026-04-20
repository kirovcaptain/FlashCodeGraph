// Package branch detects Git branches and routes data directories.
package branch

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
)

// Manager handles branch detection and data directory routing.
type Manager struct {
	dataDir string // ~/.fcg/data/{project-hash}
}

// NewManager creates a BranchManager for a project.
func NewManager(dataDir string) *Manager {
	return &Manager{dataDir: dataDir}
}

// DetectBranch reads the current Git branch. Returns "default" if not a Git repo.
func DetectBranch(projectDir string) string {
	headPath := filepath.Join(projectDir, ".git", "HEAD")
	data, err := os.ReadFile(headPath)
	if err != nil {
		return "default"
	}
	line := strings.TrimSpace(string(data))
	// "ref: refs/heads/main" → "main"
	if strings.HasPrefix(line, "ref: refs/heads/") {
		return strings.TrimPrefix(line, "ref: refs/heads/")
	}
	// Detached HEAD — use short SHA
	if len(line) >= 8 {
		return line[:8]
	}
	return "default"
}

// BranchDir returns the data directory for a branch.
func (manager *Manager) BranchDir(branch string) string {
	return filepath.Join(manager.dataDir, branch)
}

// EnsureBranchDir creates the branch data directory if it doesn't exist.
func (manager *Manager) EnsureBranchDir(branch string) error {
	return os.MkdirAll(manager.BranchDir(branch), model.DirectoryPermission)
}

// FingerprintStore returns a FingerprintStore rooted at this project's data dir.
func (manager *Manager) FingerprintStore() storage.FingerprintStore {
	return storage.NewJSONFingerprintStore(manager.dataDir)
}


