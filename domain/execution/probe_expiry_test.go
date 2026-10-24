package execution

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProbeExpirationIsValidatedAndBoundToTargetDigest(t *testing.T) {
	const target = `[{"id":"probe","source_kind":"channel","source_id":11,"model":"deepseek-flash","protocol":"openai_chat","calls_per_day":4,"input_tokens_per_day":512,"output_tokens_per_day":64,"max_output_tokens":16,"unknown_cost_calls_per_day":4,"expires_at":%d}]`
	_, err := DecodeProbeTargets(fmt.Sprintf(target, -1))
	require.Error(t, err)
	for _, expires := range []int64{0, 1792807200} {
		targets, err := DecodeProbeTargets(fmt.Sprintf(target, expires))
		require.NoError(t, err)
		require.Equal(t, expires, targets[0].ExpiresAt)
	}
	targets, err := DecodeProbeTargets(fmt.Sprintf(target, 1792807200))
	require.NoError(t, err)
	bounded := targets[0]
	unbounded := bounded
	unbounded.ExpiresAt = 0
	require.NotEqual(t, bounded.Digest(), unbounded.Digest(), "expiry cannot change under an existing one-shot permit")
	require.False(t, bounded.Expired(time.Unix(bounded.ExpiresAt-1, 0)))
	require.True(t, bounded.Expired(time.Unix(bounded.ExpiresAt, 0)))
	require.True(t, bounded.Expired(time.Unix(bounded.ExpiresAt+1, 0)))
	require.False(t, unbounded.Expired(time.Unix(bounded.ExpiresAt+1, 0)))
}
