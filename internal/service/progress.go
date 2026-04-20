package service

import "github.com/kirovcaptain/FlashCodeGraph/internal/model"

// PhaseID identifies an indexing phase.
type PhaseID int

const (
	PhaseProjectDetection PhaseID = iota
	PhaseFileScan
	PhaseIncremental
	PhaseParsing
	PhaseWriting
	PhaseResolving
	PhaseComplete

	// Analyze phases
	PhaseCallForest
	PhaseLoadMetadata
	PhaseClassifyRoots
	PhaseTraceProcesses
	PhasePersist
	PhaseAnalyzeComplete
)

type indexPhase struct {
	ID    PhaseID
	Index int
	Label string
}

var fullPhaseList = []indexPhase{
	{PhaseProjectDetection, 1, "Project detection..."},
	{PhaseFileScan, 2, "File scan..."},
	{PhaseParsing, 3, "Parsing..."},
	{PhaseWriting, 4, "Writing..."},
	{PhaseResolving, 5, "Resolving..."},
	{PhaseComplete, 6, "Complete"},
}

var incrementalPhaseList = []indexPhase{
	{PhaseProjectDetection, 1, "Project detection..."},
	{PhaseFileScan, 2, "File scan..."},
	{PhaseIncremental, 3, "Detecting changes..."},
	{PhaseParsing, 4, "Parsing..."},
	{PhaseWriting, 5, "Writing..."},
	{PhaseResolving, 6, "Resolving..."},
	{PhaseComplete, 7, "Complete"},
}

var analyzePhaseList = []indexPhase{
	{PhaseCallForest, 1, "Call forest..."},
	{PhaseLoadMetadata, 2, "Loading metadata..."},
	{PhaseClassifyRoots, 3, "Classifying roots..."},
	{PhaseTraceProcesses, 4, "Tracing processes..."},
	{PhasePersist, 5, "Persisting..."},
}

var analyzeEntriesOnlyPhaseList = []indexPhase{
	{PhaseCallForest, 1, "Call forest..."},
	{PhaseLoadMetadata, 2, "Loading metadata..."},
	{PhaseClassifyRoots, 3, "Classifying roots..."},
}

// ProgressManager manages indexing phase progress reporting.
type ProgressManager struct {
	callback model.ProgressCallback
	phases   map[PhaseID]indexPhase
	total    int
}

// NewProgressManager creates a ProgressManager with the given callback.
func NewProgressManager(callback model.ProgressCallback) *ProgressManager {
	return &ProgressManager{callback: callback}
}

// SetMode configures phases for full or incremental indexing.
func (pm *ProgressManager) SetMode(list []indexPhase) {
	pm.phases = make(map[PhaseID]indexPhase, len(list))
	pm.total = len(list)
	for _, p := range list {
		pm.phases[p.ID] = p
	}
}

// Emit sends a progress event for the given phase.
func (pm *ProgressManager) Emit(id PhaseID, current, total int, detail string) {
	if pm == nil || pm.callback == nil || pm.phases == nil {
		return
	}
	phase := pm.phases[id]
	pm.callback(model.ProgressEvent{
		Phase:      phase.Label,
		PhaseIndex: phase.Index,
		PhaseTotal: pm.total,
		Current:    current,
		Total:      total,
		Message:    detail,
	})
}

// SubStepID identifies a sub-step within a phase.
type SubStepID string

const (
	// Index — Parsing phase
	SubParseDefFiles SubStepID = "parse def files"

	// Index — Writing phase
	SubCleanGraph       SubStepID = "clean graph"
	SubCleanChanged     SubStepID = "clean changed"
	SubFindAffected     SubStepID = "find affected"
	SubLoadSymbols      SubStepID = "load symbols"
	SubStructuralNodes  SubStepID = "structural nodes"
	SubSymbolNodes      SubStepID = "symbol nodes"
	SubContainsEdges    SubStepID = "contains edges"
	SubRouteNodes       SubStepID = "route nodes"
	SubQueryNodes       SubStepID = "query nodes"
	SubAnnotationNodes  SubStepID = "annotation nodes"
	SubRemoteCallEdges  SubStepID = "remote call edges"
	SubSaveFingerprints SubStepID = "save fingerprints"

	// Index — Resolving phase
	SubImportEdges           SubStepID = "import edges"
	SubInferLocal            SubStepID = "infer local"
	SubInferMultiReturn      SubStepID = "infer multi-return"
	SubResolveFixpoint       SubStepID = "resolve fixpoint"
	SubResolveCalls          SubStepID = "resolve calls"
	SubResolveHeritage       SubStepID = "resolve heritage"
	SubDetectOverridesAndDispatches       SubStepID = "detect overrides"
	SubCrossFilePropagation  SubStepID = "cross-file propagation"
	SubExternalNodes         SubStepID = "external nodes"
	SubRelationEdges         SubStepID = "relation edges"
	SubUnresolvedHints       SubStepID = "unresolved hints"
	SubDebugDump             SubStepID = "debug dump"

	// Analyze — LoadMetadata phase
	SubRouteHandlers    SubStepID = "route handlers"
	SubAnnotations      SubStepID = "annotations"
	SubImplementations  SubStepID = "implementations"
)

// EmitSub sends a sub-step progress event within a phase.
func (pm *ProgressManager) EmitSub(id PhaseID, subStep SubStepID, detail string) {
	if pm == nil || pm.callback == nil || pm.phases == nil {
		return
	}
	phase := pm.phases[id]
	pm.callback(model.ProgressEvent{
		Phase:      phase.Label,
		PhaseIndex: phase.Index,
		PhaseTotal: pm.total,
		Message:    detail,
		SubStep:    string(subStep),
	})
}

// AnalyzePhaseList returns the phase list for full analyze.
func AnalyzePhaseList() []indexPhase { return analyzePhaseList }

// AnalyzeEntriesOnlyPhaseList returns the phase list for entries-only analyze.
func AnalyzeEntriesOnlyPhaseList() []indexPhase { return analyzeEntriesOnlyPhaseList }
