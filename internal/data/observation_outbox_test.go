package data

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/internal/biz"
	"micro-one-api/platform/metrics"
)

func observationEvent(id string) evidence.SendEvent {
	send := evidence.SendRef{ObservationID: "obs-" + id, SendID: "obs-" + id + "/1/send-" + id, RequestID: "req", RootRequestID: "root",
		AttemptNumber: 1, UserID: 7, Purpose: evidence.PurposeUser, SourceKind: "channel", SourceID: 4, Model: "exact",
		Protocol: "openai_chat", Endpoint: "/v1/chat/completions"}
	b := [5]int64{10, 0, 0, 0, 2}
	record := &evidence.Record{Observation: evidence.Observation{Protocol: "openai_chat", ParserRevision: evidence.ParserRevision, ContractRevision: evidence.ContractRevision, Status: "verified", Buckets: &b}, HTTPStatus: 200, Complete: true, Execution: "response_received"}
	return evidence.NewSendEvent(send, "terminal", 1, record, evidence.CaptureComplete, "", time.Unix(1700000000, 0))
}

func TestObservationOutboxRestartConflictAndAckTombstone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "observations.db")
	d, closeDB, err := NewObservationData(path, nil)
	require.NoError(t, err)
	repo, err := NewObservationOutboxRepo(d, path)
	require.NoError(t, err)
	ctx := context.Background()
	event := observationEvent("a")
	require.NoError(t, repo.Put(ctx, event))
	require.NoError(t, repo.Put(ctx, event), "same bytes under the same key are idempotent")

	changed := event
	changed.Record = &evidence.Record{Observation: evidence.Observation{Protocol: "openai_chat", Status: "invalid"}, HTTPStatus: 200, Complete: true}
	changed.ID = changed.Key()
	require.ErrorIs(t, repo.Put(ctx, changed), biz.ErrObservationReceipt, "different bytes must be rejected")

	rows, err := repo.Pending(ctx, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, event, rows[0].Event)
	require.NoError(t, repo.Acknowledge(ctx, event.ID, rows[0].Digest))
	rows, err = repo.Pending(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, rows)

	closeDB()
	d, closeDB, err = NewObservationData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	repo, err = NewObservationOutboxRepo(d, path)
	require.NoError(t, err)
	require.ErrorIs(t, repo.Put(ctx, changed), biz.ErrObservationReceipt, "ACK tombstone retains the original digest after restart")
	rows, err = repo.Pending(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, rows)

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	var mode string
	require.NoError(t, d.ObservationDB.Raw("PRAGMA journal_mode").Scan(&mode).Error)
	require.Equal(t, "wal", mode)
	var sync int
	require.NoError(t, d.ObservationDB.Raw("PRAGMA synchronous").Scan(&sync).Error)
	require.Equal(t, 2, sync)
}

func TestObservationOutboxStatsReportPendingAndBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observations.db")
	d, closeDB, err := NewObservationData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	repo, err := NewObservationOutboxRepo(d, path)
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, repo.Put(ctx, observationEvent("b")))
	stats, err := repo.Stats(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, stats.Pending)
	require.NotZero(t, stats.OldestUnix)
	require.Positive(t, stats.Bytes)
}

func TestObservationOutboxRestoresAckedAlertSignalsAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observations.db")
	d, closeDB, err := NewObservationData(path, nil)
	require.NoError(t, err)
	repo, err := NewObservationOutboxRepo(d, path)
	require.NoError(t, err)
	invalid, gap := observationEvent("restart-invalid"), observationEvent("restart-gap")
	stamp := time.Now().Add(-time.Minute).Truncate(time.Second)
	invalid.ObservedAt, gap.ObservedAt = stamp.UnixMilli(), stamp.UnixMilli()
	invalid.Record.Observation.Status = "invalid"
	gap.Record.Observation.Status, gap.Capture = "unobserved", evidence.CaptureLimited
	ctx := context.Background()
	for _, event := range []evidence.SendEvent{invalid, gap} {
		require.NoError(t, repo.Put(ctx, event))
		_, digest, err := event.Encode()
		require.NoError(t, err)
		require.NoError(t, repo.Acknowledge(ctx, event.ID, digest))
	}
	var payloadBytes int64
	require.NoError(t, d.ObservationDB.Model(&observationOutboxModel{}).Select("COALESCE(SUM(LENGTH(payload)),0)").Scan(&payloadBytes).Error)
	require.Zero(t, payloadBytes, "ACK must still discard payloads")
	closeDB()
	// Simulate the process restarting before any scrape observed the counters.
	anomaly := metrics.UsageObservationLastAnomaly.WithLabelValues("openai_chat", "channel", "invalid")
	limited := metrics.UsageObservationLastGap.WithLabelValues("limited")
	anomaly.Set(0)
	limited.Set(0)
	metrics.UsageObservationTerminal.DeleteLabelValues("openai_chat", "channel", "user", "complete", "invalid")
	metrics.UsageObservationTerminal.DeleteLabelValues("openai_chat", "channel", "user", "limited", "unobserved")
	t.Cleanup(func() { anomaly.Set(0); limited.Set(0); metrics.InitUsageObservationBaselines() })
	d, closeDB, err = NewObservationData(path, nil)
	require.NoError(t, err)
	t.Cleanup(closeDB)
	repo, err = NewObservationOutboxRepo(d, path)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		biz.NewObservationDelivery(repo, nil).Run(ctx, func(error) {})
	}()
	t.Cleanup(func() { cancel(); <-done })
	require.Eventually(t, func() bool {
		return testutil.ToFloat64(anomaly) >= float64(stamp.Unix()) && testutil.ToFloat64(limited) >= float64(stamp.Unix())
	}, time.Second, 5*time.Millisecond, "ACKed invalid/limited signals must survive a restart before the first scrape")
}

func TestObservationOutboxDeferHidesEventUntilRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observations.db")
	d, closeDB, err := NewObservationData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	repo, err := NewObservationOutboxRepo(d, path)
	require.NoError(t, err)
	ctx := context.Background()
	event := observationEvent("c")
	require.NoError(t, repo.Put(ctx, event))
	require.NoError(t, repo.Defer(ctx, event.ID, time.Now().Add(time.Minute)))
	rows, err := repo.Pending(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, rows, "deferred events must not be redelivered immediately")
	stats, err := repo.Stats(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, stats.Pending, "deferred evidence still counts as pending")
}

func TestObservationOutboxRejectsRelativePath(t *testing.T) {
	if _, _, err := NewObservationData("relative/observations.db", nil); err == nil {
		t.Fatal("a relative observation path must be rejected")
	}
}

// R09: capacity is judged from live pending payload, not the allocated file.
// A queue that was filled and fully acknowledged must accept new events again
// without an operator deleting the database or restarting the process.
func TestObservationOutboxRecoversAfterDrainedFullBurst(t *testing.T) {
	t.Setenv("RELAY_OBSERVATION_MAX_PENDING_EVENTS", "2")
	path := filepath.Join(t.TempDir(), "observations.db")
	d, closeDB, err := NewObservationData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	repo, err := NewObservationOutboxRepo(d, path)
	require.NoError(t, err)
	ctx := context.Background()

	first, second, third := observationEvent("drain-1"), observationEvent("drain-2"), observationEvent("drain-3")
	require.NoError(t, repo.Put(ctx, first))
	require.NoError(t, repo.Put(ctx, second))
	require.ErrorIs(t, repo.Put(ctx, third), biz.ErrObservationCapacity, "a full queue refuses new events")

	for _, event := range []evidence.SendEvent{first, second} {
		_, digest, err := event.Encode()
		require.NoError(t, err)
		require.NoError(t, repo.Acknowledge(ctx, event.ID, digest))
	}
	stats, err := repo.Stats(ctx)
	require.NoError(t, err)
	require.Zero(t, stats.Pending)
	require.Positive(t, stats.Bytes, "acknowledged rows keep their allocated pages; that must not block new writes")

	require.NoError(t, repo.Put(ctx, third), "a drained queue must accept a new event without manual maintenance")
	stats, err = repo.Stats(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, stats.Pending)
}

// R22: the observation queue must leave headroom on the shared filesystem for
// the financial outbox and the services, and fail degraded instead of filling
// the disk.
func TestObservationOutboxRespectsFilesystemHeadroom(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observations.db")
	d, closeDB, err := NewObservationData(path, nil)
	require.NoError(t, err)
	defer closeDB()

	t.Setenv("RELAY_OBSERVATION_MIN_FREE_BYTES", "9223372036854775807")
	guarded, err := NewObservationOutboxRepo(d, path)
	require.NoError(t, err)
	require.ErrorIs(t, guarded.Put(context.Background(), observationEvent("headroom")), biz.ErrObservationCapacity)

	t.Setenv("RELAY_OBSERVATION_MIN_FREE_BYTES", "0")
	unguarded, err := NewObservationOutboxRepo(d, path)
	require.NoError(t, err)
	require.NoError(t, unguarded.Put(context.Background(), observationEvent("headroom")))
}

func TestObservationOutboxLimitsUTF8PayloadBytes(t *testing.T) {
	first, second := observationEvent("utf8-first"), observationEvent("utf8-second")
	first.Send.Model, second.Send.Model = strings.Repeat("模", 70), strings.Repeat("模", 70)
	a, _, err := first.Encode()
	require.NoError(t, err)
	b, _, err := second.Encode()
	require.NoError(t, err)
	t.Setenv("RELAY_OBSERVATION_MAX_PENDING_BYTES", fmt.Sprint(len(a)+len(b)-1))
	path := filepath.Join(t.TempDir(), "observations.db")
	d, closeDB, err := NewObservationData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	repo, err := NewObservationOutboxRepo(d, path)
	require.NoError(t, err)
	require.NoError(t, repo.Put(context.Background(), first))
	require.ErrorIs(t, repo.Put(context.Background(), second), biz.ErrObservationCapacity)
}

func TestObservationOutboxRejectsUnavailablePageSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observations.db")
	d, closeDB, err := NewObservationData(path, nil)
	require.NoError(t, err)
	closeDB()
	_, err = NewObservationOutboxRepo(d, path)
	require.Error(t, err, "unknown page size must not silently report an invented allocation")
}

func TestObservationOutboxQuarantinesCorruptRowWithoutBlockingOthers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observations.db")
	d, closeDB, err := NewObservationData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	repo, err := NewObservationOutboxRepo(d, path)
	require.NoError(t, err)
	ctx := context.Background()
	bad, good := observationEvent("bad"), observationEvent("good")
	require.NoError(t, repo.Put(ctx, bad))
	require.NoError(t, repo.Put(ctx, good))
	require.NoError(t, d.ObservationDB.Model(&observationOutboxModel{}).Where("id = ?", bad.ID).Update("payload", "corrupt").Error)
	rows, err := repo.Pending(ctx, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, good.ID, rows[0].Event.ID)
	var retained observationOutboxModel
	require.NoError(t, d.ObservationDB.Where("id = ?", bad.ID).Take(&retained).Error)
	require.Equal(t, "quarantined", retained.Status)
	require.Equal(t, "corrupt", retained.Payload, "invalid bytes remain available for controlled investigation")
}

func TestObservationOutboxClassifiesSQLiteFullAsCapacity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observations.db")
	d, closeDB, err := NewObservationData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	repo, err := NewObservationOutboxRepo(d, path)
	require.NoError(t, err)
	var pages int64
	require.NoError(t, d.ObservationDB.Raw("PRAGMA page_count").Scan(&pages).Error)
	require.NoError(t, d.ObservationDB.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", pages)).Error)
	event := observationEvent("sqlite-full")
	event.Record.Observation.Usage = []byte(`{"dimension":"` + strings.Repeat("x", 30000) + `"}`)
	require.ErrorIs(t, repo.Put(context.Background(), event), biz.ErrObservationCapacity)
}

func TestObservationOutboxQuarantinesMismatchedOrNoncanonicalPayload(t *testing.T) {
	for _, changedID := range []bool{true, false} {
		t.Run(fmt.Sprint(changedID), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "observations.db")
			d, closeDB, err := NewObservationData(path, nil)
			require.NoError(t, err)
			defer closeDB()
			repo, err := NewObservationOutboxRepo(d, path)
			require.NoError(t, err)
			original := observationEvent("original")
			require.NoError(t, repo.Put(context.Background(), original))
			payload, _, err := original.Encode()
			require.NoError(t, err)
			if changedID {
				payload, _, err = observationEvent("different-id").Encode()
				require.NoError(t, err)
			} else {
				payload = append([]byte(" "), payload...)
			}
			digest := fmt.Sprintf("%x", sha256.Sum256(payload))
			require.NoError(t, d.ObservationDB.Model(&observationOutboxModel{}).Where("id = ?", original.ID).Updates(map[string]any{"payload": string(payload), "digest": digest}).Error)
			rows, err := repo.Pending(context.Background(), 10)
			require.NoError(t, err)
			require.Empty(t, rows, "an event must round-trip to the stored identity and digest used for ACK")
			var row observationOutboxModel
			require.NoError(t, d.ObservationDB.Where("id = ?", original.ID).Take(&row).Error)
			require.Equal(t, "quarantined", row.Status)
		})
	}
}

func TestObservationQuarantineDoesNotReviveAnExpiredUsageAnomaly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observations.db")
	d, closeDB, err := NewObservationData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	repo, err := NewObservationOutboxRepo(d, path)
	require.NoError(t, err)
	event := observationEvent("expired-anomaly")
	event.Record.Observation.Status = "invalid"
	event.ObservedAt = time.Now().Add(-20 * time.Minute).UnixMilli()
	require.NoError(t, repo.Put(context.Background(), event))
	require.NoError(t, d.ObservationDB.Model(&observationOutboxModel{}).Where("id = ?", event.ID).Update("payload", "corrupt").Error)
	_, err = repo.Pending(context.Background(), 10)
	require.NoError(t, err)
	stats, err := repo.Stats(context.Background())
	require.NoError(t, err)
	require.Len(t, stats.Signals, 1)
	require.Equal(t, "rejected", stats.Signals[0].Gap)
	require.Empty(t, stats.Signals[0].Status, "quarantine is a new capture gap, not a new invalid usage event")
}

func TestObservationOutboxMaintenanceHonorsCanceledContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observations.db")
	d, closeDB, err := NewObservationData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	repo, err := NewObservationOutboxRepo(d, path)
	require.NoError(t, err)
	db, err := d.ObservationDB.DB()
	require.NoError(t, err)
	conn, err := db.Conn(context.Background())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- repo.Put(ctx, observationEvent("canceled")) }()
	select {
	case err := <-done:
		_ = conn.Close()
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(250 * time.Millisecond):
		_ = conn.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("observation write did not stop after releasing the database")
		}
		t.Fatal("canceled observation write waited for an unbounded maintenance checkpoint")
	}
}
