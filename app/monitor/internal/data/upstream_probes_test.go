package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"micro-one-api/domain/execution"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/testutil"
	"path/filepath"
	"testing"
	"time"
)

func probeFixture(t *testing.T) (*Repository, execution.ProbeTarget) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "monitor.db")+"?_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&probeBudgetModel{}, &probeAttemptModel{}, &driftReportModel{}))
	sql, _ := db.DB()
	sql.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sql.Close() })
	return &Repository{db: db}, execution.ProbeTarget{ID: "probe", SourceKind: "channel", SourceID: 1, Model: "Exact", Protocol: "openai_chat", CallsPerDay: 2, InputTokensPerDay: 256, OutputTokensPerDay: 32, MaxOutputTokens: 16, UnknownCostCallsPerDay: 2}
}
func TestProbeBudgetIsPersistentAndSeparatesUnknown(t *testing.T) {
	r, target := probeFixture(t)
	ctx := context.Background()
	now := time.Now()
	ok, err := r.ReserveProbe(ctx, target, "first", now)
	require.NoError(t, err)
	require.True(t, ok)
	sameSource := target
	sameSource.ID = "another-model"
	ok, err = r.ReserveProbe(ctx, sameSource, "overlap", now)
	require.NoError(t, err)
	require.False(t, ok)
	recovered := &Repository{db: r.db}
	ok, err = recovered.ReserveProbe(ctx, target, "second", now.Add(2*time.Minute))
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = recovered.ReserveProbe(ctx, target, "third", now.Add(4*time.Minute))
	require.NoError(t, err)
	require.False(t, ok)
	var b probeBudgetModel
	require.NoError(t, r.db.First(&b).Error)
	require.EqualValues(t, 2, b.UnknownCostCalls)
	require.Zero(t, b.Cost)
}
func TestLateProbeCompletionDoesNotClearNewLease(t *testing.T) {
	r, target := probeFixture(t)
	now := time.Now()
	ctx := context.Background()
	ok, err := r.ReserveProbe(ctx, target, "old", now)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = r.ReserveProbe(ctx, target, "new", now.Add(2*time.Minute))
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, r.CompleteProbe(ctx, target, execution.ProbeResult{AttemptID: "old", TargetID: target.ID, ObservedAt: now.Unix(), Outcome: "probe_unavailable"}))
	var b probeBudgetModel
	require.NoError(t, r.db.First(&b).Error)
	require.Equal(t, now.Add(3*time.Minute).Unix(), b.LeaseUntil)
}
func TestProbeDriftReportsRejectUnvalidatedClientErrors(t *testing.T) {
	r, target := probeFixture(t)
	ctx := context.Background()
	now := time.Now()
	ok, err := r.ReserveProbe(ctx, target, "first", now)
	require.NoError(t, err)
	require.True(t, ok)
	raw, _ := jsonx.Marshal(evidence.Record{Complete: true, Observation: evidence.Observation{Status: "unsupported", Reason: "unknown_usage_field", Fingerprint: []string{"/new_field:number"}}})
	require.NoError(t, r.CompleteProbe(ctx, target, execution.ProbeResult{AttemptID: "first", TargetID: target.ID, ObservedAt: now.Unix(), Outcome: "contract_drift", RecordJSON: raw}))
	var count int64
	require.NoError(t, r.db.Model(&driftReportModel{}).Count(&count).Error)
	require.Zero(t, count)
	ok, err = r.ReserveProbe(ctx, target, "second", now.Add(2*time.Minute))
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, r.CompleteProbe(ctx, target, execution.ProbeResult{AttemptID: "second", TargetID: target.ID, ObservedAt: now.Unix(), Outcome: "contract_drift", RecordJSON: raw, ValidContractProbe: true}))
	require.NoError(t, r.db.Model(&driftReportModel{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestProbePermitIsSingleUseAndBoundToFrozenTarget(t *testing.T) {
	r, target := probeFixture(t)
	ctx := context.Background()
	ok, err := r.ReserveProbe(ctx, target, "permit", time.Now())
	require.NoError(t, err)
	require.True(t, ok)
	allowed, err := r.ClaimProbePermit(ctx, target.ID, "unreserved", target.Digest())
	require.NoError(t, err)
	require.False(t, allowed)
	altered := target
	altered.Model = "different"
	allowed, err = r.ClaimProbePermit(ctx, target.ID, "permit", altered.Digest())
	require.NoError(t, err)
	require.False(t, allowed)
	allowed, err = r.ClaimProbePermit(ctx, target.ID, "permit", target.Digest())
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, err = r.ClaimProbePermit(ctx, target.ID, "permit", target.Digest())
	require.NoError(t, err)
	require.False(t, allowed, "another relay instance cannot resend this attempt")
}

func TestProbePermitCannotStartNearLeaseExpiry(t *testing.T) {
	r, target := probeFixture(t)
	ctx := context.Background()
	ok, err := r.ReserveProbe(ctx, target, "expired-start", time.Now().Add(-40*time.Second))
	require.NoError(t, err)
	require.True(t, ok)
	allowed, err := r.ClaimProbePermit(ctx, target.ID, "expired-start", target.Digest())
	require.NoError(t, err)
	require.False(t, allowed, "must leave enough lease for the bounded supplier call")
}

func TestProbeBudgetAndAtomicPermitAcrossDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := testutil.RoutingContextDB(t, driver)
			r := &Repository{db: db}
			target := execution.ProbeTarget{ID: "dialect-probe", SourceKind: "channel", SourceID: 123, Model: "Exact", Protocol: "openai_chat", CallsPerDay: 1, InputTokensPerDay: 128, OutputTokensPerDay: 16, MaxOutputTokens: 16, UnknownCostCallsPerDay: 1}
			ctx := context.Background()
			require.True(t, r.ProbeStorageReady(ctx))
			ok, err := r.ReserveProbe(ctx, target, "single-global-attempt", time.Now())
			require.NoError(t, err)
			require.True(t, ok)
			claimed, err := r.ClaimProbePermit(ctx, target.ID, "single-global-attempt", target.Digest())
			require.NoError(t, err)
			require.True(t, claimed)
			claimed, err = r.ClaimProbePermit(ctx, target.ID, "single-global-attempt", target.Digest())
			require.NoError(t, err)
			require.False(t, claimed)
			require.NoError(t, r.CompleteProbe(ctx, target, execution.ProbeResult{TargetID: target.ID, AttemptID: "single-global-attempt", Outcome: "probe_unavailable", ObservedAt: time.Now().Unix()}))
			ok, err = r.ReserveProbe(ctx, target, "beyond-daily-limit", time.Now())
			require.NoError(t, err)
			require.False(t, ok)
		})
	}
}
