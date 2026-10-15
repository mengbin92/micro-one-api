package provider

import "micro-one-api/domain/upstream/contract"

type ExecutionCapacityError struct{ Err error }

func (e *ExecutionCapacityError) Error() string { return e.Err.Error() }
func (e *ExecutionCapacityError) Unwrap() error { return e.Err }

// ExecutionClaim validates the exact source/configuration used by a forward.
type ExecutionClaim struct {
	RuntimeBuild          string
	AttemptID             string
	SourceKind            string
	SourceID              int64
	ConfigurationRevision string
	UpstreamModelID       string
	ProbeID               string
}

// ExecutionLease carries the owner's current data-plane recovery authority.
type ExecutionLease struct {
	Contracts             []contract.Snapshot
	BlockedProtocols      []string
	ExecutionRevision     string
	ConfigurationRevision string
	ProbeID               string
}
