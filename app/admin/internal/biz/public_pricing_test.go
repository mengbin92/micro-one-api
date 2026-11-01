package biz

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublicServiceTierPricingPropagatesReadAndValidationFailures(t *testing.T) {
	readErr := errors.New("storage unavailable")
	repo := &groupPricingRepo{err: readErr}
	uc := NewSystemOptionsUsecase(repo)
	view, err := uc.PublicServiceTierPricing(context.Background())
	require.ErrorIs(t, err, readErr)
	require.Nil(t, view)
	repo.err = nil
	for _, raw := range []string{"", `{"enabled":false}`, `{"enabled":true}`, "{"} {
		repo.raw = raw
		view, err = uc.PublicServiceTierPricing(context.Background())
		if raw == "" || raw == `{"enabled":false}` {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
		require.Nil(t, view)
	}
	require.Zero(t, repo.writes)
}
