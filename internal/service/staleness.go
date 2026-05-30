package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kirovcaptain/FlashCodeGraph/internal/storage"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/branch"
)

// IndexStatus describes whether a project's index is up-to-date with its source files.
type IndexStatus struct {
	IsStale       bool      `json:"stale"`
	AddedFiles    int       `json:"added_files,omitempty"`
	ModifiedFiles int       `json:"modified_files,omitempty"`
	DeletedFiles  int       `json:"deleted_files,omitempty"`
	LastIndexedAt time.Time `json:"last_indexed_at,omitempty"`
	LastCommit    string    `json:"last_commit,omitempty"`
	CurrentCommit string    `json:"current_commit,omitempty"`
	Reason        string    `json:"reason"`
	Message       string    `json:"message,omitempty"`
}

// CheckIndexStatus detects whether the project index is stale compared to source files.
//
// Detection strategy:
//   - Not indexed: fingerprints not found → reason "not_indexed"
//   - Non-git project: no .git directory → reason "not_git_project" (cannot detect changes)
//   - Same branch (queryBranch == current git branch): git diff + stat fingerprints → file-level counts
//   - Cross branch (queryBranch != current git branch): compare commit SHA → reason "new_commits"
func CheckIndexStatus(ctx context.Context, fingerprintStore storage.FingerprintStore, repoPath string, queryBranch string) (*IndexStatus, error) {
	meta, err := fingerprintStore.LoadMeta(ctx, queryBranch)
	if err != nil {
		return nil, fmt.Errorf("check index status: %w", err)
	}
	if meta == nil {
		return &IndexStatus{
			IsStale: true,
			Reason:  "not_indexed",
			Message: "Project has not been indexed yet. Run index_repository first.",
		}, nil
	}

	lastIndexedAt := time.Unix(meta.LastIndexedAt, 0)

	// Check if this is a git project
	gitDir := filepath.Join(repoPath, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return &IndexStatus{
			IsStale:       false,
			Reason:        "not_git_project",
			LastIndexedAt: lastIndexedAt,
			Message:       "Non-git project, unable to detect changes. Re-index if source files have changed.",
		}, nil
	}

	currentBranch := branch.DetectBranch(repoPath)

	if queryBranch == currentBranch {
		return detectSameBranchStaleness(ctx, fingerprintStore, repoPath, queryBranch, meta)
	}
	return detectCrossBranchStaleness(repoPath, queryBranch, meta)
}

// detectSameBranchStaleness performs file-level staleness detection when querying the current branch.
// Uses git diff to find working tree changes, then cross-references with stored fingerprints.
func detectSameBranchStaleness(ctx context.Context, fingerprintStore storage.FingerprintStore, repoPath string, queryBranch string, meta *storage.FingerprintMeta) (*IndexStatus, error) {
	lastIndexedAt := time.Unix(meta.LastIndexedAt, 0)

	// Fast path: compare commit SHA first — if HEAD hasn't moved, index is likely up-to-date
	currentCommit := readGitHeadCommit(repoPath)
	if meta.LastCommit != "" && currentCommit != "" && currentCommit == meta.LastCommit {
		// Same commit — check for uncommitted changes via git status (faster than stat loop)
		dirtyFiles := filterSourceFiles(listGitDirtyFiles(repoPath))
		if len(dirtyFiles) == 0 {
			return &IndexStatus{
				IsStale:       false,
				LastIndexedAt: lastIndexedAt,
				LastCommit:    meta.LastCommit,
			}, nil
		}
		// Has uncommitted changes — count them against fingerprints
		fingerprints, err := fingerprintStore.Load(ctx, queryBranch)
		if err != nil {
			return nil, fmt.Errorf("check index status: load fingerprints: %w", err)
		}
		addedCount := 0
		modifiedCount := 0
		for _, changedFile := range dirtyFiles {
			storedFingerprint, existsInFingerprints := fingerprints[changedFile]
			if !existsInFingerprints {
				addedCount++
				continue
			}
			// Compare current file stat with stored fingerprint
			fullPath := filepath.Join(repoPath, changedFile)
			fileInfo, statErr := os.Stat(fullPath)
			if statErr != nil {
				modifiedCount++ // file deleted or inaccessible
				continue
			}
			if fileInfo.ModTime().Unix() != storedFingerprint.ModTime || fileInfo.Size() != storedFingerprint.Size {
				modifiedCount++
			}
		}
		totalChanged := addedCount + modifiedCount
		return &IndexStatus{
			IsStale:       totalChanged > 0,
			AddedFiles:    addedCount,
			ModifiedFiles: modifiedCount,
			LastIndexedAt: lastIndexedAt,
			LastCommit:    meta.LastCommit,
			Reason:        "files_changed",
		}, nil
	}

	// Commit has moved or no LastCommit recorded — use git diff for speed
	if meta.LastCommit != "" && currentCommit != "" {
		diffFiles := filterSourceFiles(listGitDiffFiles(repoPath, meta.LastCommit))
		dirtyFiles := filterSourceFiles(listGitDirtyFiles(repoPath))
		allChanged := make(map[string]bool)
		for _, f := range diffFiles {
			allChanged[f] = true
		}
		for _, f := range dirtyFiles {
			allChanged[f] = true
		}
		if len(allChanged) == 0 {
			return &IndexStatus{
				IsStale:       false,
				LastIndexedAt: lastIndexedAt,
				LastCommit:    meta.LastCommit,
			}, nil
		}
		fingerprints, err := fingerprintStore.Load(ctx, queryBranch)
		if err != nil {
			return nil, fmt.Errorf("check index status: load fingerprints: %w", err)
		}
		addedCount := 0
		modifiedCount := 0
		for f := range allChanged {
			storedFingerprint, existsInFingerprints := fingerprints[f]
			if !existsInFingerprints {
				addedCount++
				continue
			}
			fullPath := filepath.Join(repoPath, f)
			fileInfo, statErr := os.Stat(fullPath)
			if statErr != nil {
				modifiedCount++
				continue
			}
			if fileInfo.ModTime().Unix() != storedFingerprint.ModTime || fileInfo.Size() != storedFingerprint.Size {
				modifiedCount++
			}
		}
		totalChanged := addedCount + modifiedCount
		return &IndexStatus{
			IsStale:       totalChanged > 0,
			AddedFiles:    addedCount,
			ModifiedFiles: modifiedCount,
			LastIndexedAt: lastIndexedAt,
			LastCommit:    meta.LastCommit,
			CurrentCommit: currentCommit,
			Reason:        "files_changed",
		}, nil
	}

	// No LastCommit — cannot use git diff, fall back to commit comparison
	return &IndexStatus{
		IsStale:       currentCommit != meta.LastCommit,
		LastIndexedAt: lastIndexedAt,
		LastCommit:    meta.LastCommit,
		CurrentCommit: currentCommit,
		Reason:        "new_commits",
		Message:       "Index may be outdated (no commit baseline recorded). Consider re-indexing.",
	}, nil
}

// detectCrossBranchStaleness checks if a non-current branch has new commits since last index.
// Cannot do file-level detection because disk files belong to the current branch.
func detectCrossBranchStaleness(repoPath string, queryBranch string, meta *storage.FingerprintMeta) (*IndexStatus, error) {
	lastIndexedAt := time.Unix(meta.LastIndexedAt, 0)

	latestCommit := getGitBranchCommit(repoPath, queryBranch)
	if latestCommit == "" {
		return &IndexStatus{
			IsStale:       false,
			LastIndexedAt: lastIndexedAt,
			LastCommit:    meta.LastCommit,
			Reason:        "",
			Message:       fmt.Sprintf("Cannot resolve commit for branch %q.", queryBranch),
		}, nil
	}

	if meta.LastCommit == "" || latestCommit != meta.LastCommit {
		return &IndexStatus{
			IsStale:       true,
			LastIndexedAt: lastIndexedAt,
			LastCommit:    meta.LastCommit,
			CurrentCommit: latestCommit,
			Reason:        "new_commits",
			Message:       fmt.Sprintf("Branch %q has new commits since last index.", queryBranch),
		}, nil
	}

	return &IndexStatus{
		IsStale:       false,
		LastIndexedAt: lastIndexedAt,
		LastCommit:    meta.LastCommit,
		Reason:        "",
	}, nil
}

// listGitDirtyFiles returns relative paths of uncommitted changes in the working tree
// (tracked modifications + untracked new files).
func listGitDirtyFiles(repoPath string) []string {
	output := runGitCommand(repoPath, "status", "--porcelain")
	seen := make(map[string]bool)
	var result []string
	for _, line := range strings.Split(output, "\n") {
		if len(line) < 4 {
			continue
		}
		// porcelain format: XY filename (first 3 chars are status + space)
		filePath := strings.TrimSpace(line[3:])
		// Handle renamed files: "R  old -> new"
		if arrowIndex := strings.Index(filePath, " -> "); arrowIndex >= 0 {
			filePath = filePath[arrowIndex+4:]
		}
		if filePath != "" && !seen[filePath] {
			seen[filePath] = true
			result = append(result, filePath)
		}
	}
	return result
}

// listGitDiffFiles returns relative paths of files changed between two commits.
func listGitDiffFiles(repoPath string, fromCommit string) []string {
	output := runGitCommand(repoPath, "diff", "--name-only", fromCommit, "HEAD")
	var result []string
	for _, line := range strings.Split(output, "\n") {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine != "" {
			result = append(result, trimmedLine)
		}
	}
	return result
}

// getGitBranchCommit returns the latest commit SHA for a given branch name.
// Reads directly from .git/refs/heads/ to avoid slow git commands on cross-filesystem mounts.
// Returns empty string on any error.
func getGitBranchCommit(repoPath string, branchName string) string {
	// Try reading from packed-refs or refs/heads directly
	refPath := filepath.Join(repoPath, ".git", "refs", "heads", branchName)
	commitContent, err := os.ReadFile(refPath)
	if err == nil {
		return strings.TrimSpace(string(commitContent))
	}

	// Fallback: git rev-parse (handles packed refs)
	output := runGitCommand(repoPath, "rev-parse", branchName)
	return strings.TrimSpace(output)
}

// runGitCommand executes a git command in the given directory and returns stdout.
// Returns empty string on any error or if the command takes longer than 2 seconds.
func runGitCommand(repoPath string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = repoPath
	outputBytes, err := command.Output()
	if err != nil {
		return ""
	}
	return string(outputBytes)
}

// FormatStalenessWarning produces a human-readable warning string from an IndexStatus.
// Returns empty string if the index is not stale.
func FormatStalenessWarning(status *IndexStatus) string {
	if status == nil || !status.IsStale {
		return ""
	}
	switch status.Reason {
	case "not_indexed":
		return "Index not found. Run index_repository to build the index."
	case "new_commits":
		return fmt.Sprintf("Index outdated: branch has new commits (indexed: %.7s, current: %.7s). Consider re-indexing.",
			status.LastCommit, status.CurrentCommit)
	case "files_changed":
		parts := []string{}
		if status.AddedFiles > 0 {
			parts = append(parts, fmt.Sprintf("%d added", status.AddedFiles))
		}
		if status.ModifiedFiles > 0 {
			parts = append(parts, fmt.Sprintf("%d modified", status.ModifiedFiles))
		}
		if status.DeletedFiles > 0 {
			parts = append(parts, fmt.Sprintf("%d deleted", status.DeletedFiles))
		}
		return fmt.Sprintf("Index outdated: %s since last index. Consider re-indexing.",
			strings.Join(parts, ", "))
	default:
		return ""
	}
}

// FilterSourceFiles filters git-reported changed files to only include source code files
// that FCG would index (by file extension).
func filterSourceFiles(files []string) []string {
	sourceExtensions := map[string]bool{
		".java": true, ".go": true, ".py": true,
		".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	}
	var sourceFiles []string
	for _, filePath := range files {
		extension := strings.ToLower(filepath.Ext(filePath))
		if sourceExtensions[extension] {
			sourceFiles = append(sourceFiles, filePath)
		}
	}
	return sourceFiles
}

// CreateFingerprintStore creates a FingerprintStore for the given project path.
// This is a lightweight operation that does not connect to any database.
func CreateFingerprintStore(repoPath string) storage.FingerprintStore {
	fcgDir := filepath.Join(repoPath, ".fcg", "fingerprints")
	branchManager := branch.NewManager(fcgDir)
	return branchManager.FingerprintStore()
}

// CheckProjectStaleness is a convenience function that creates a FingerprintStore
// and checks index staleness in one call.
func CheckProjectStaleness(ctx context.Context, repoPath string, queryBranch string) (*IndexStatus, error) {
	if queryBranch == "" {
		queryBranch = branch.DetectBranch(repoPath)
	}
	fingerprintStore := CreateFingerprintStore(repoPath)
	return CheckIndexStatus(ctx, fingerprintStore, repoPath, queryBranch)
}
