package routingoutbox

import (
	"context"
	"errors"
	"testing"
	"time"

	promtest "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"micro-one-api/platform/database/testutil"
	"micro-one-api/platform/metrics"
)

func TestOutboxMetricsFailureAndRecovery(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := testutil.RoutingContextDB(t, driver)
			ctx := context.Background()
			for _, owner := range []string{"identity", "channel", "subscription"} {
				t.Run(owner, func(t *testing.T) {
					require.NoError(t, Enqueue(db, owner, "user", 1, 1))
					require.NoError(t, db.Model(&record{}).Where("owner = ?", owner).Update("created_at", time.Now().Add(-2*time.Minute).Unix()).Error)
					metric := metrics.RoutingOutboxFailures.WithLabelValues(owner, "publish")
					before := promtest.ToFloat64(metric)
					failure := errors.New("Redis down")
					err := Dispatch(ctx, db, owner, func(context.Context, string, any) error { return failure })
					require.ErrorIs(t, err, failure)
					var detail *DeliveryError
					require.ErrorAs(t, err, &detail)
					require.Equal(t, owner, detail.Owner)
					require.Equal(t, "publish", detail.Operation)
					require.NotEmpty(t, detail.EventID)
					require.Equal(t, before+1, promtest.ToFloat64(metric))
					require.EqualValues(t, 1, promtest.ToFloat64(metrics.RoutingOutboxPending.WithLabelValues(owner)))
					require.GreaterOrEqual(t, promtest.ToFloat64(metrics.RoutingOutboxOldestAge.WithLabelValues(owner)), float64(120))
					require.Positive(t, promtest.ToFloat64(metrics.RoutingOutboxLastScan.WithLabelValues(owner)))
					require.NoError(t, Dispatch(ctx, db, owner, func(context.Context, string, any) error { return nil }))
					require.Zero(t, promtest.ToFloat64(metrics.RoutingOutboxPending.WithLabelValues(owner)))
					require.Zero(t, promtest.ToFloat64(metrics.RoutingOutboxOldestAge.WithLabelValues(owner)))
					success := promtest.ToFloat64(metrics.RoutingOutboxLastSuccess.WithLabelValues(owner))
					require.Positive(t, success)
					require.NoError(t, Dispatch(ctx, db, owner, func(context.Context, string, any) error { t.Fatal("empty queue must not publish"); return nil }))
					require.Equal(t, success, promtest.ToFloat64(metrics.RoutingOutboxLastSuccess.WithLabelValues(owner)), "empty poll is not a delivery")
				})
			}
		})
	}
}

func TestOutboxAcknowledgeAndScanFailureMetrics(t *testing.T) {
	db := testutil.RoutingContextDB(t, "sqlite")
	ctx := context.Background()
	require.NoError(t, Enqueue(db, "identity", "user", 1, 1))
	failure := errors.New("write unavailable")
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("reject_ack", func(tx *gorm.DB) { tx.AddError(failure) }))
	metric := metrics.RoutingOutboxFailures.WithLabelValues("identity", "acknowledge")
	before := promtest.ToFloat64(metric)
	success := promtest.ToFloat64(metrics.RoutingOutboxLastSuccess.WithLabelValues("identity"))
	require.ErrorIs(t, Dispatch(ctx, db, "identity", func(context.Context, string, any) error { return nil }), failure)
	require.Equal(t, before+1, promtest.ToFloat64(metric))
	require.Equal(t, success, promtest.ToFloat64(metrics.RoutingOutboxLastSuccess.WithLabelValues("identity")))
	require.NoError(t, db.Callback().Update().Remove("reject_ack"))
	require.NoError(t, db.Migrator().DropTable(&record{}))
	lastScan := promtest.ToFloat64(metrics.RoutingOutboxLastScan.WithLabelValues("identity"))
	metric = metrics.RoutingOutboxFailures.WithLabelValues("identity", "scan")
	before = promtest.ToFloat64(metric)
	require.Error(t, Dispatch(ctx, db, "identity", func(context.Context, string, any) error { t.Fatal("scan failed"); return nil }))
	require.Equal(t, before+1, promtest.ToFloat64(metric))
	require.Equal(t, lastScan, promtest.ToFloat64(metrics.RoutingOutboxLastScan.WithLabelValues("identity")))
	require.EqualValues(t, 1, promtest.ToFloat64(metrics.RoutingOutboxPending.WithLabelValues("identity")), "failed scan preserves last known pending count")
}

func TestWorkerWithoutRedisStillObservesPending(t *testing.T) {
	db := testutil.RoutingContextDB(t, "sqlite")
	require.NoError(t, Enqueue(db, "subscription", "subscription", 1, 1))
	reported := make(chan error, 1)
	stop := Start(db, nil, "subscription", func(err error) {
		select {
		case reported <- err:
		default:
		}
	})
	defer stop()
	select {
	case err := <-reported:
		var detail *DeliveryError
		require.ErrorAs(t, err, &detail)
		require.Equal(t, "publish", detail.Operation)
		require.EqualValues(t, 1, promtest.ToFloat64(metrics.RoutingOutboxPending.WithLabelValues("subscription")))
	case <-time.After(5 * time.Second):
		t.Fatal("missing Redis must not silently disable monitoring")
	}
}

func TestPendingMetricsCountBeyondDispatchBatchAndScopeOwner(t *testing.T) {
	db := testutil.RoutingContextDB(t, "sqlite")
	for id := int64(1); id <= 101; id++ {
		require.NoError(t, Enqueue(db, "channel", "group", id, 1))
	}
	require.NoError(t, Enqueue(db, "identity", "user", 1, 1))
	require.NoError(t, Dispatch(context.Background(), db, "channel", func(context.Context, string, any) error { return nil }))
	require.EqualValues(t, 1, promtest.ToFloat64(metrics.RoutingOutboxPending.WithLabelValues("channel")))
	var pending int64
	require.NoError(t, db.Model(&record{}).Where("owner = ? AND delivered_at = 0", "identity").Count(&pending).Error)
	require.EqualValues(t, 1, pending)
	require.NoError(t, Dispatch(context.Background(), db, "channel", func(context.Context, string, any) error { return nil }))
	require.Zero(t, promtest.ToFloat64(metrics.RoutingOutboxPending.WithLabelValues("channel")))
}
