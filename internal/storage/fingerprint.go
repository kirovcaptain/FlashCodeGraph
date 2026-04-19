package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/liuymcn/flash-code-graph/internal/model"
)

// JSONFingerprintStore implements FingerprintStore using JSON files.
type JSONFingerprintStore struct {
	dataDir string // ~/.fcg/data/{project-hash}
}

// NewJSONFingerprintStore creates a fingerprint store rooted at dataDir.
func NewJSONFingerprintStore(dataDir string) *JSONFingerprintStore {
	return &JSONFingerprintStore{dataDir: dataDir}
}

type fingerprintFile struct {
	Version       int                          `json:"version"`
	LastIndexedAt int64                        `json:"last_indexed_at"`
	LastCommit    string                       `json:"last_commit"`
	Files         map[string]model.Fingerprint `json:"files"`
}

func (store *JSONFingerprintStore) path(branch string) string {
	return filepath.Join(store.dataDir, branch, "fingerprints.json")
}

// Load reads fingerprints for a branch. Returns empty map if file doesn't exist.
func (store *JSONFingerprintStore) Load(_ context.Context, branch string) (map[string]model.Fingerprint, error) {
	data, err := os.ReadFile(store.path(branch))
	if os.IsNotExist(err) {
		return make(map[string]model.Fingerprint), nil
	}
	if err != nil {
		return nil, fmt.Errorf("fingerprint: load %s: %w", branch, err)
	}
	var f fingerprintFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("fingerprint: parse %s: %w", branch, err)
	}
	if f.Files == nil {
		f.Files = make(map[string]model.Fingerprint)
	}
	return f.Files, nil
}

// Save writes fingerprints and metadata for a branch.
// If meta is non-nil, LastIndexedAt and LastCommit are persisted alongside the fingerprints.
func (store *JSONFingerprintStore) Save(_ context.Context, branch string, fps map[string]model.Fingerprint, meta *FingerprintMeta) error {
	filePath := store.path(branch)
	if err := os.MkdirAll(filepath.Dir(filePath), model.DirectoryPermission); err != nil {
		return fmt.Errorf("fingerprint: mkdir %s: %w", branch, err)
	}
	file := fingerprintFile{
		Version: 1,
		Files:   fps,
	}
	if meta != nil {
		file.LastIndexedAt = meta.LastIndexedAt
		file.LastCommit = meta.LastCommit
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("fingerprint: marshal %s: %w", branch, err)
	}
	return os.WriteFile(filePath, data, model.FilePermission)
}

// LoadMeta reads only the metadata (LastIndexedAt, LastCommit) for a branch.
// Returns nil with no error if the fingerprint file does not exist.
func (store *JSONFingerprintStore) LoadMeta(_ context.Context, branch string) (*FingerprintMeta, error) {
	data, err := os.ReadFile(store.path(branch))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fingerprint: load meta %s: %w", branch, err)
	}
	var file fingerprintFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("fingerprint: parse meta %s: %w", branch, err)
	}
	return &FingerprintMeta{
		LastIndexedAt: file.LastIndexedAt,
		LastCommit:    file.LastCommit,
	}, nil
}
