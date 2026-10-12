package provider

type ExecutionCapacityError struct{ Err error }

func (e *ExecutionCapacityError) Error() string { return e.Err.Error() }
func (e *ExecutionCapacityError) Unwrap() error { return e.Err }

// ExecutionClaim validates the exact source/configuration used by a forward.
type ExecutionClaim struct {
	SourceKind            string
	SourceID              int64
	ConfigurationRevision string
	UpstreamModelID       string
	ProbeID               string
}

// ExecutionLease carries the owner's current data-plane recovery authority.
type ExecutionLease struct {
	ConfigurationRevision string
	ProbeID               string
}
