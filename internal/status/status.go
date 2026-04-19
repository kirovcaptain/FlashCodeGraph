package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const statusFile = ".fcg/status.json"

// Status holds timestamps for index and analyze operations.
type Status struct {
	IndexTimestamp   int64 `json:"index_timestamp"`
	AnalyzeTimestamp int64 `json:"analyze_timestamp"`
}

func path(projectDir string) string {
	return filepath.Join(projectDir, statusFile)
}

// Read loads the status file. Returns zero Status if not found.
func Read(projectDir string) Status {
	data, err := os.ReadFile(path(projectDir))
	if err != nil {
		return Status{}
	}
	var s Status
	json.Unmarshal(data, &s)
	return s
}

// MarkIndexed updates the index timestamp.
func MarkIndexed(projectDir string) {
	s := Read(projectDir)
	s.IndexTimestamp = time.Now().Unix()
	write(projectDir, s)
}

// MarkAnalyzed updates the analyze timestamp.
func MarkAnalyzed(projectDir string) {
	s := Read(projectDir)
	s.AnalyzeTimestamp = time.Now().Unix()
	write(projectDir, s)
}

// NeedsAnalyze returns true if index is newer than last analyze.
func NeedsAnalyze(projectDir string) bool {
	s := Read(projectDir)
	if s.IndexTimestamp == 0 {
		return true
	}
	return s.IndexTimestamp > s.AnalyzeTimestamp
}

func write(projectDir string, s Status) {
	os.MkdirAll(filepath.Dir(path(projectDir)), 0755)
	data, _ := json.Marshal(s)
	os.WriteFile(path(projectDir), data, 0644)
}
