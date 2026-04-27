package constants

// Call resolution confidence scores (assigned by resolver).
const (
	ConfidenceTypeExact  = 0.95 // receiver type known from TypeEnv → exact match
	ConfidenceArgCount   = 0.85 // unique function name + matching argument count
	ConfidenceSameFile   = 0.85 // unique function name within the same file
	ConfidenceNameUnique = 0.70 // globally unique function name
	ConfidenceTypeParent = 0.65 // matched via parent class in hierarchy
	ConfidenceBestGuess  = 0.25 // multiple candidates, pick first (low confidence)
	ConfidenceExternal   = 0.70 // external dependency via import
	ConfidenceImportPath = 1.0  // import path exact match
)

// Cross-service call confidence scores (assigned by matchConsumerToProvider).
const (
	ConfidenceViaRoute      = 0.9  // same-repo route match
	ConfidenceCrossService  = 0.85 // cross-repo route match via CrossProjectIndex
	ConfidencePendingRemote = 0.9  // PendingRemoteCall field-level match
)

// RemoteCall service confidence scores.
const (
	RemoteCallConfidenceLiteral    = 1.0 // service name from literal string or annotation
	RemoteCallConfidenceInferred   = 0.9 // service name from config or indirect inference
	RemoteCallConfidenceUnresolved = 0.0 // service name not resolved
)

// Confidence thresholds.
const (
	ConfidenceLowThreshold = 0.5  // below this is considered low confidence
	ConfidenceDefaultMin   = 0.70 // default min confidence for queries
)
