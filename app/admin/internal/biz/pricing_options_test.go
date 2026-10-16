package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/authorization"
)

func TestBatchOptionsNeverFallsBackToPartialWrites(t *testing.T) {
	repo := &groupPricingRepo{}
	err := NewSystemOptionsUsecase(repo).SetMany(context.Background(), map[string]string{"ModelPrice": `{}`, "ModelRatio": `{}`, "CompletionRatio": `{}`}, nil)
	require.ErrorIs(t, err, authorization.ErrWriteStorageUnavailable)
	require.Zero(t, repo.writes)
}
