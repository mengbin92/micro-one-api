package biz

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

type groupPricingRepo struct {
	raw    string
	err    error
	writes int
}

func (r *groupPricingRepo) Get(context.Context, string) (string, error) { return r.raw, r.err }
func (r *groupPricingRepo) Set(_ context.Context, _ string, value string) error {
	r.writes++
	r.raw = value
	return nil
}

func TestRoutingGroupRatiosCompatibility(t *testing.T) {
	ctx := context.Background()
	repo := &groupPricingRepo{raw: `{"vip":0.5,"team":0.8}`}
	uc := NewSystemOptionsUsecase(repo)
	items, err := uc.ListRoutingGroupRatios(ctx)
	require.NoError(t, err)
	require.Equal(t, []RoutingGroupRatio{{"default", 1}, {"team", 0.8}, {"vip", 0.5}}, items)
	require.Zero(t, repo.writes, "listing must not persist an invented registry")
	item, err := uc.UpsertRoutingGroupRatio(ctx, " team ", 0.7)
	require.NoError(t, err)
	require.Equal(t, &RoutingGroupRatio{"team", 0.7}, item)
	require.JSONEq(t, `{"vip":0.5,"team":0.7}`, repo.raw)
	_, err = uc.DeleteRoutingGroupRatio(ctx, "team")
	require.NoError(t, err)
	require.JSONEq(t, `{"default":1,"vip":0.5}`, repo.raw)
	_, err = uc.DeleteRoutingGroupRatio(ctx, "default")
	require.Error(t, err)
}

func TestRoutingGroupRatiosRejectInvalidWrites(t *testing.T) {
	for _, ratio := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		repo := &groupPricingRepo{}
		_, err := NewSystemOptionsUsecase(repo).UpsertRoutingGroupRatio(context.Background(), "vip", ratio)
		require.Error(t, err)
		require.Zero(t, repo.writes)
	}
	for _, name := range []string{"", "  ", "default,vip"} {
		repo := &groupPricingRepo{}
		_, err := NewSystemOptionsUsecase(repo).UpsertRoutingGroupRatio(context.Background(), name, 1)
		require.Error(t, err)
		require.Zero(t, repo.writes)
	}
}

func TestRoutingGroupRatiosReadFailureCannotOverwritePricing(t *testing.T) {
	readErr := errors.New("storage unavailable")
	repo := &groupPricingRepo{raw: `{"vip":0.5}`, err: readErr}
	uc := NewSystemOptionsUsecase(repo)
	_, err := uc.UpsertRoutingGroupRatio(context.Background(), "team", 0.8)
	require.ErrorIs(t, err, readErr)
	_, err = uc.DeleteRoutingGroupRatio(context.Background(), "vip")
	require.ErrorIs(t, err, readErr)
	require.Zero(t, repo.writes)
	require.Equal(t, `{"vip":0.5}`, repo.raw)
}

func TestRoutingGroupRatiosNullAndMalformedConfig(t *testing.T) {
	repo := &groupPricingRepo{raw: "null"}
	uc := NewSystemOptionsUsecase(repo)
	_, err := uc.UpsertRoutingGroupRatio(context.Background(), "team", 0.8)
	require.NoError(t, err)
	require.JSONEq(t, `{"team":0.8}`, repo.raw)
	repo.raw = "{"
	repo.writes = 0
	_, err = uc.DeleteRoutingGroupRatio(context.Background(), "team")
	require.Error(t, err)
	require.Zero(t, repo.writes)
}
