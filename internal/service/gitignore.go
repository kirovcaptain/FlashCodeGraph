package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// EnsureFcgIgnored checks if .fcg is git-ignored in the project, and if not,
// appends ".fcg/" to the project's .gitignore file.
// Returns true if .gitignore was modified, false if already ignored or not a git project.
func EnsureFcgIgnored(projectPath string) (bool, error) {
	// Skip non-git projects
	gitDir := filepath.Join(projectPath, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return false, nil
	}

	// Check if .gitignore already contains ".fcg" or ".fcg/"
	gitignorePath := filepath.Join(projectPath, ".gitignore")
	existingContent, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	for _, line := range strings.Split(string(existingContent), "\n") {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == ".fcg" || trimmedLine == ".fcg/" {
			return false, nil // already present
		}
	}

	// Also check via git check-ignore (covers .git/info/exclude and global gitignore)
	if isGitIgnored(projectPath, ".fcg") {
		return false, nil
	}

	// Append ".fcg/" to .gitignore
	file, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return false, err
	}
	defer file.Close()

	// Ensure we start on a new line
	if len(existingContent) > 0 && existingContent[len(existingContent)-1] != '\n' {
		file.WriteString("\n")
	}
	_, err = file.WriteString(".fcg/\n")
	if err != nil {
		return false, err
	}
	return true, nil
}

// IsFcgIgnored checks if .fcg is already git-ignored in the project.
func IsFcgIgnored(projectPath string) bool {
	// Quick text check first
	gitignorePath := filepath.Join(projectPath, ".gitignore")
	content, err := os.ReadFile(gitignorePath)
	if err == nil {
		for _, line := range strings.Split(string(content), "\n") {
			trimmedLine := strings.TrimSpace(line)
			if trimmedLine == ".fcg" || trimmedLine == ".fcg/" {
				return true
			}
		}
	}
	// Fallback to git check-ignore
	return isGitIgnored(projectPath, ".fcg")
}

// isGitIgnored uses git check-ignore to test if a path is ignored.
// Returns false on any error (fail-open).
func isGitIgnored(projectPath string, path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "check-ignore", "-q", path)
	command.Dir = projectPath
	return command.Run() == nil
}
