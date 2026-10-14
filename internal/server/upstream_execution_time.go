package server

import "os"

// Rollout requires new evidence consumers before producing additional signed
// fields. Disabled preserves the exact P1 event encoding for rolling upgrades.
func upstreamExecutionTimingEnabled() bool {
	return os.Getenv("RELAY_UPSTREAM_EXECUTION_TIME_V1") == "true"
}
