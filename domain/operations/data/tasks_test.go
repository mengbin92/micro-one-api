package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/operations/biz"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func taskPlan(now time.Time) biz.Plan {
	return biz.Plan{ID: "fixed-incident", Kind: "repair", Enabled: true, PayloadFile: "payload", PayloadDigest: strings.Repeat("a", 64), IntervalSeconds: 3600, ExpiresAt: now.Add(24 * time.Hour).Unix(), MaxRuns: 1}
}
func TestPaidClaimSurvivesCrashWithoutReplayOrBudgetRelease(t *testing.T) {
	now := time.Now().UTC()
	path := filepath.Join(t.TempDir(), "tasks.db")
	d, err := NewData(path)
	require.NoError(t, err)
	r := NewTaskRepo(d)
	p := taskPlan(now)
	claim, err := r.Claim(context.Background(), p, now, 1)
	require.NoError(t, err)
	require.NotNil(t, claim)
	require.NoError(t, d.Close())
	d, err = NewData(path)
	require.NoError(t, err)
	defer d.Close()
	r = NewTaskRepo(d)
	require.NoError(t, r.Expire(context.Background(), now.Add(3*time.Minute)))
	var status string
	require.NoError(t, d.db.QueryRow("SELECT status FROM operator_tasks").Scan(&status))
	require.Equal(t, "uncertain", status)
	again, err := r.Claim(context.Background(), p, now.Add(4*time.Minute), 1)
	require.NoError(t, err)
	require.Nil(t, again)
	p.ID = "second-incident"
	blocked, err := r.Claim(context.Background(), p, now.Add(4*time.Minute), 1)
	require.NoError(t, err)
	require.Nil(t, blocked, "daily budget includes uncertain calls")
	require.Error(t, r.Finish(context.Background(), *claim, biz.Receipt{Status: "succeeded"}, now.Add(4*time.Minute)))
}
func TestImmutablePlanAndConcurrentWorkersHaveOneClaim(t *testing.T) {
	now := time.Now().UTC()
	path := filepath.Join(t.TempDir(), "tasks.db")
	a, err := NewData(path)
	require.NoError(t, err)
	defer a.Close()
	b, err := NewData(path)
	require.NoError(t, err)
	defer b.Close()
	p := taskPlan(now)
	results := make(chan *biz.Claim, 2)
	errs := make(chan error, 2)
	for _, d := range []*Data{a, b} {
		go func(d *Data) { c, e := NewTaskRepo(d).Claim(context.Background(), p, now, 2); results <- c; errs <- e }(d)
	}
	count := 0
	for range 2 {
		require.NoError(t, <-errs)
		if <-results != nil {
			count++
		}
	}
	require.Equal(t, 1, count)
	p.PayloadDigest = strings.Repeat("b", 64)
	_, err = NewTaskRepo(a).Claim(context.Background(), p, now, 2)
	require.ErrorContains(t, err, "immutable")
}
