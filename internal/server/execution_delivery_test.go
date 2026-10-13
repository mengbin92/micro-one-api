package server

import (
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/upstream/evidence"
	"testing"
)

func TestNativeV2PreservesZeroAndUnprovenStates(t *testing.T) {
	zero := evidence.Parse([]byte(`{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`), "openai_chat")
	u := nativeV2Envelope(&evidence.Record{Observation: zero, Complete: true, HTTPStatus: 200})
	require.Equal(t, "verified", string(u.ParseStatus))
	require.NotNil(t, u.Canonical)
	require.Zero(t, u.BillableTotal())
	for _, status := range []string{"missing", "unsupported", "invalid"} {
		u = nativeV2Envelope(&evidence.Record{Observation: evidence.Observation{Status: status, Reason: "fixture"}, Complete: true})
		require.Equal(t, status, string(u.ParseStatus))
		require.Nil(t, u.Canonical)
	}
	u = nativeV2Envelope(&evidence.Record{Observation: zero, Complete: false})
	require.Equal(t, "missing", string(u.ParseStatus))
	require.Nil(t, u.Canonical)
}
