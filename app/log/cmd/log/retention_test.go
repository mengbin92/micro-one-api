package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/log/internal/biz"
)

type retentionLogRepo struct {
	biz.LogRepo
	err error
}

func (r *retentionLogRepo) DeleteBefore(context.Context, time.Time) (int64, error) {
	return 0, r.err
}

type retentionObservationRepo struct {
	biz.UsageObservationRepo
	cleaned chan time.Time
}

func (r *retentionObservationRepo) DeleteObservationsBefore(_ context.Context, before time.Time) (int64, error) {
	r.cleaned <- before
	return 0, nil
}

func TestObservationRetentionIsIndependentOfOrdinaryLogs(t *testing.T) {
	for _, tc := range []struct {
		name string
		days int
		err  error
	}{
		{name: "ordinary log deletion fails", days: 30, err: errors.New("ordinary logs unavailable")},
		{name: "ordinary log retention disabled", days: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &retentionObservationRepo{cleaned: make(chan time.Time, 1)}
			started := time.Now()
			stop := startLogRetentionCleanup(biz.NewLogUsecase(&retentionLogRepo{err: tc.err}), biz.NewUsageObservationUsecase(repo), tc.days)
			defer stop()
			select {
			case before := <-repo.cleaned:
				require.WithinDuration(t, started.Add(-observationEvidenceRetention), before, time.Second)
			case <-time.After(time.Second):
				t.Fatal("the dedicated evidence retention must run independently of ordinary logs")
			}
		})
	}
}
