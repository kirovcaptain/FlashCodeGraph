package model

// IndexResult holds the outcome of an indexing run.
type IndexResult struct {
	FilesScanned       int            `json:"files_scanned"`
	FilesProcessed     int            `json:"files_processed"`
	FilesSkipped       int            `json:"files_skipped"`
	SymbolsCreated     int            `json:"symbols_created"`
	RelationsCreated   int            `json:"relations_created"`
	CommunitiesFound   int            `json:"communities_found"`
	EntryPointsFound   int            `json:"entry_points_found"`
	ProcessesFound     int            `json:"processes_found"`
	DurationMs         int64          `json:"duration_ms"`
	SymbolsByKind      map[string]int `json:"symbols_by_kind"`
	RelationsByKind    map[string]int `json:"relations_by_kind"`
	FilesByLanguage    map[string]int `json:"files_by_language"`
	EntriesByType      map[string]int `json:"entries_by_type"`
	Errors             []IndexError   `json:"errors,omitempty"`
	SkippedFiles       []SkippedFile  `json:"skipped_files,omitempty"`
	LowConfidenceCount int            `json:"low_confidence_count"`
	AnnotationCount    int            `json:"annotation_count"`
}

// IndexError records a non-fatal indexing error.
type IndexError struct {
	FilePath string `json:"file_path"`
	Phase    string `json:"phase"`
	Message  string `json:"message"`
}

// SkippedFile records why a file was skipped.
type SkippedFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason"` // exceeds_max_size, unsupported_language, permission_denied
	Detail string `json:"detail"`
}

// ProgressEvent is emitted during indexing for progress display.
type ProgressEvent struct {
	Phase      string `json:"phase"`
	PhaseIndex int    `json:"phase_index"` // 1-7
	PhaseTotal int    `json:"phase_total"` // 7
	Current    int    `json:"current"`
	Total      int    `json:"total"`
	Message    string `json:"message"`
	SubStep    string `json:"sub_step,omitempty"`
}

// ProgressCallback receives progress updates during indexing.
type ProgressCallback func(event ProgressEvent)
