package credential

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRefreshTaskCoordinationDoesNotRewriteAccountStatus(t *testing.T) {
	for _, err := range []error{ErrRefreshBusy, ErrRefreshUncertain, ErrCoordinationUnavailable, ErrCredentialPersistencePending, ErrCredentialConflict} {
		t.Run(err.Error(), func(t *testing.T) {
			provider := &fakeRefreshProvider{errs: []error{err}}
			hook := &fakeRefreshHook{}
			task := NewRefreshTask(nil, nil, nil, RefreshTaskConfig{MaxRetries: 3, Hook: hook})
			require.ErrorIs(t, task.refreshAccount(context.Background(), provider, 1), err)
			require.Equal(t, 1, provider.calls)
			require.Empty(t, provider.invalidated)
			require.Empty(t, hook.success)
			require.Empty(t, hook.nonRetryable)
			require.Empty(t, hook.retryExhausted)
		})
	}
}
