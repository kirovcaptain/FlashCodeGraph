package constants

// RemoteCall service confidence scores.
const (
	RemoteCallConfidenceLiteral    = 1.0 // service name from literal string or annotation
	RemoteCallConfidenceInferred   = 0.9 // service name from config or indirect inference
	RemoteCallConfidenceUnresolved = 0.0 // service name not resolved
)
