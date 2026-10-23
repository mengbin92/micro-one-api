package data

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/domain/execution"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/testutil"
)

// Observe actual database/sql operations at the real repository boundary. The
// underlying scratch database and transactions still execute every statement.
type vendorAttemptBatchProbe struct {
	*sql.DB
	options []sql.TxOptions
	queries []string
	writes  []string
}

type vendorAttemptBatchTx struct {
	*sql.Tx
	probe *vendorAttemptBatchProbe
}

func (p *vendorAttemptBatchProbe) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	if opts != nil {
		p.options = append(p.options, *opts)
	}
	tx, err := p.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &vendorAttemptBatchTx{Tx: tx, probe: p}, nil
}

func (tx *vendorAttemptBatchTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	tx.probe.queries = append(tx.probe.queries, query)
	return tx.Tx.QueryContext(ctx, query, args...)
}

func (tx *vendorAttemptBatchTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	tx.probe.queries = append(tx.probe.queries, query)
	return tx.Tx.QueryRowContext(ctx, query, args...)
}

func (tx *vendorAttemptBatchTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	tx.probe.writes = append(tx.probe.writes, query)
	return tx.Tx.ExecContext(ctx, query, args...)
}

func vendorAttemptBatchFixture(t *testing.T) (*gorm.DB, biz.VendorReconciliationRepo, *vendorAttemptBatchProbe, biz.VendorManifest) {
	t.Helper()
	// SQLite remains the ordinary unit gate. Real dialect runs opt in with
	// VENDOR_ATTEMPT_BATCH_TEST_DRIVER and the testutil GROUP_CONTEXT_TEST_*_DSN
	// variables, which accept only disposable local servers and create a schema.
	driver := os.Getenv("VENDOR_ATTEMPT_BATCH_TEST_DRIVER")
	if driver == "" {
		driver = "sqlite"
	}
	require.Contains(t, []string{"sqlite", "mysql", "postgres"}, driver)
	db := testutil.RoutingContextDB(t, driver)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	probe := &vendorAttemptBatchProbe{DB: sqlDB}
	var dialect gorm.Dialector = sqlite.New(sqlite.Config{Conn: probe})
	if driver == "mysql" {
		dialect = mysql.New(mysql.Config{Conn: probe})
	} else if driver == "postgres" {
		dialect = postgres.New(postgres.Config{Conn: probe})
	}
	observed, err := gorm.Open(dialect, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	return db, NewVendorReconciliationRepo(&Data{db: observed}), probe, vendorRepoManifest()
}

func vendorAttemptBatchIntent(n int, sourceKind string, sourceID int64, m biz.VendorManifest) execution.Intent {
	return execution.Freeze(execution.Intent{RequestID: fmt.Sprintf("batch-attempt-%04d", n), RootRequestID: fmt.Sprintf("batch-root-%04d", n), AttemptNumber: 1, Purpose: "health_probe", SourceKind: sourceKind, SourceID: sourceID, Model: "vendor/Exact", Policy: execution.Policy, CostBasis: "unknown", LeaseUntil: m.Scope.End.Unix(), EvidenceDeadline: m.Scope.End.Add(time.Hour).Unix()})
}

func vendorAttemptBatchEvent(t *testing.T, i execution.Intent, sequence int64, sent time.Time, received time.Time) executionEventModel {
	t.Helper()
	record := &evidence.Record{Complete: true, HTTPStatus: 200, Observation: evidence.Parse([]byte(`{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`), "openai_chat")}
	if !sent.IsZero() {
		record.StartedAtUnixNano = sent.UnixNano()
	}
	ev := execution.Event{Version: execution.Version, Intent: i, Category: "terminal", Sequence: sequence, ObservedAt: received.Unix(), Record: record, BuildRevision: "batch-regression-fixture"}
	ev.ID = ev.Key()
	payload, digest, err := ev.Encode()
	require.NoError(t, err, "fixtures must satisfy the original execution contract")
	return executionEventModel{ID: ev.ID, IntentID: i.Key(), Category: ev.Category, SourceKind: i.SourceKind, SourceID: i.SourceID, Model: i.Model, Purpose: i.Purpose, Protocol: record.Observation.Protocol, Payload: string(payload), Digest: digest, ReceivedAt: received.Unix(), CostBasis: "unknown"}
}

func vendorAttemptBatchAssertReadOnly(t *testing.T, probe *vendorAttemptBatchProbe) {
	t.Helper()
	require.Len(t, probe.options, 1, "one consistent snapshot must cover intents and all event batches")
	require.True(t, probe.options[0].ReadOnly)
	require.Equal(t, sql.LevelRepeatableRead, probe.options[0].Isolation)
	require.Empty(t, probe.writes, "loading supplier evidence must not write any storage state")
}

func TestVendorAttemptBatchQueryBoundPreservesUnknownTimeAndSourceIsolation(t *testing.T) {
	db, repo, probe, m := vendorAttemptBatchFixture(t)
	const count = 64
	late := m.Scope.End.Add(24 * time.Hour)
	var events []executionEventModel
	var unknownID, withoutResponseID, outsideID string
	for n := 0; n < count; n++ {
		i := vendorAttemptBatchIntent(n, "channel", 1, m)
		require.NoError(t, saveExecutionIntent(db, i))
		sent := m.Scope.Start.Add(time.Hour)
		switch n {
		case 0:
			sent, unknownID = time.Time{}, i.Key()
		case 1:
			withoutResponseID = i.Key()
			continue
		case count - 1:
			sent, outsideID = m.Scope.Start.Add(-time.Second), i.Key()
		}
		events = append(events, vendorAttemptBatchEvent(t, i, 1, sent, late))
	}
	// Equal numeric IDs in separate source kinds must never be conflated.
	for n, source := range []struct {
		kind string
		id   int64
	}{{"channel", 2}, {"subscription", 1}} {
		i := vendorAttemptBatchIntent(count+n, source.kind, source.id, m)
		require.NoError(t, saveExecutionIntent(db, i))
		event := vendorAttemptBatchEvent(t, i, 1, m.Scope.Start.Add(time.Hour), late)
		event.Digest = "foreign-source-tampering"
		events = append(events, event)
	}
	require.NoError(t, db.CreateInBatches(&events, 100).Error)
	attempts, err := repo.LoadVendorAttempts(context.Background(), m)
	require.NoError(t, err, "unmapped source evidence is outside this report's coverage")
	require.Len(t, attempts, count-1, "only an evidenced send outside the window may be excluded")
	byID := map[string]biz.VendorLocalAttempt{}
	for _, attempt := range attempts {
		require.Equal(t, "channel", attempt.SourceKind)
		require.EqualValues(t, 1, attempt.SourceID)
		byID[attempt.ID] = attempt
	}
	require.Contains(t, byID, unknownID)
	require.True(t, byID[unknownID].ExecutedAt.IsZero(), "a late inbox timestamp must not become a send timestamp")
	require.Len(t, byID[unknownID].EvidenceIDs, 1)
	require.Contains(t, byID, withoutResponseID)
	require.True(t, byID[withoutResponseID].ExecutedAt.IsZero())
	require.Empty(t, byID[withoutResponseID].EvidenceIDs)
	require.NotContains(t, byID, outsideID)
	reconciled, err := biz.ReconcileVendor(m, nil, attempts)
	require.NoError(t, err)
	require.Equal(t, 2, reconciled.UnknownTimeAttempts)
	vendorAttemptBatchAssertReadOnly(t, probe)
	var eventQueries int
	for _, query := range probe.queries {
		if strings.Contains(query, "upstream_execution_evidence") {
			eventQueries++
		}
	}
	t.Logf("%d mapped intents: %d database queries, %d event queries", count, len(probe.queries), eventQueries)
	require.LessOrEqual(t, len(probe.queries), 6, "batch event loading must have a fixed query bound for this scope; one event query per intent causes the owner RPC deadline regression")
}

func TestVendorAttemptBatchRejectsTamperedDigestAndWrongIntent(t *testing.T) {
	for _, mismatch := range []string{"digest", "intent_identity"} {
		t.Run(mismatch, func(t *testing.T) {
			db, repo, probe, m := vendorAttemptBatchFixture(t)
			i := vendorAttemptBatchIntent(0, "channel", 1, m)
			require.NoError(t, saveExecutionIntent(db, i))
			eventIntent := i
			if mismatch == "intent_identity" {
				eventIntent.RequestID = "another-legal-intent"
			}
			event := vendorAttemptBatchEvent(t, eventIntent, 1, m.Scope.Start.Add(time.Hour), m.Scope.End.Add(time.Hour))
			event.IntentID = i.Key()
			if mismatch == "digest" {
				event.Digest = "tampered"
			}
			require.NoError(t, db.Create(&event).Error)
			_, err := repo.LoadVendorAttempts(context.Background(), m)
			require.ErrorContains(t, err, "execution evidence digest mismatch")
			vendorAttemptBatchAssertReadOnly(t, probe)
		})
	}
}

func TestVendorAttemptBatchEnforcesEventLimitPerIntent(t *testing.T) {
	for _, scenario := range []struct {
		name             string
		intents, events  int
		wantLimitFailure bool
	}{{"multiple_valid_attempts_over_1000_total", 2, 600, false}, {"one_attempt_over_1000", 1, 1001, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			db, repo, probe, m := vendorAttemptBatchFixture(t)
			var events []executionEventModel
			for n := 0; n < scenario.intents; n++ {
				i := vendorAttemptBatchIntent(n, "channel", 1, m)
				require.NoError(t, saveExecutionIntent(db, i))
				for sequence := 0; sequence < scenario.events; sequence++ {
					events = append(events, vendorAttemptBatchEvent(t, i, int64(sequence), m.Scope.Start.Add(time.Hour), m.Scope.End.Add(time.Hour)))
				}
			}
			require.NoError(t, db.CreateInBatches(&events, 100).Error)
			attempts, err := repo.LoadVendorAttempts(context.Background(), m)
			if scenario.wantLimitFailure {
				require.ErrorContains(t, err, "attempt event limit exceeded")
			} else {
				require.NoError(t, err, "the safety limit applies to each attempt, not all report events")
				require.Len(t, attempts, scenario.intents)
				for _, attempt := range attempts {
					require.Len(t, attempt.EvidenceIDs, scenario.events)
				}
			}
			vendorAttemptBatchAssertReadOnly(t, probe)
		})
	}
}

func TestVendorAttemptBatchCrossPagePreservesCacheEstimateAndUnknownTime(t *testing.T) {
	db, repo, probe, m := vendorAttemptBatchFixture(t)
	m.RequestIDKind = "response"
	const perIntent = 600
	var events []executionEventModel
	var unknownID string
	for n := 0; n < 2; n++ {
		i := vendorAttemptBatchIntent(n, "channel", 1, m)
		i.CostBasis = "tariff_estimate"
		i.Tariff = jsonx.RawMessage(`{"input_price":0.01,"output_price":0.02,"cache_read_price":0.001}`)
		require.NoError(t, saveExecutionIntent(db, i))
		for sequence := 0; sequence < perIntent; sequence++ {
			sent := m.Scope.Start.Add(time.Hour)
			if n == 1 {
				sent, unknownID = time.Time{}, i.Key()
			}
			row := vendorAttemptBatchEvent(t, i, int64(sequence), sent, m.Scope.End.Add(time.Hour))
			var ev execution.Event
			require.NoError(t, jsonx.Unmarshal([]byte(row.Payload), &ev))
			ev.Record.Observation = evidence.Parse([]byte(`{"usage":{"prompt_tokens":18,"completion_tokens":3,"total_tokens":21,"prompt_tokens_details":{"cached_tokens":11}}}`), "openai_chat")
			ev.Record.Observation.ProviderRequestID = fmt.Sprintf("native-response-%d", sequence)
			payload, digest, err := ev.Encode()
			require.NoError(t, err)
			row.Payload, row.Digest = string(payload), digest
			row.EstimatedCost = biz.SupplierEstimate(i, ev.Record)
			require.NotNil(t, row.EstimatedCost)
			require.EqualValues(t, 1410, *row.EstimatedCost)
			row.CostBasis = "tariff_estimate"
			events = append(events, row)
		}
	}
	require.NoError(t, db.CreateInBatches(&events, 100).Error)
	attempts, err := repo.LoadVendorAttempts(context.Background(), m)
	require.NoError(t, err)
	require.Len(t, attempts, 2)
	for _, attempt := range attempts {
		require.Len(t, attempt.EvidenceIDs, perIntent, "all pages must contribute evidence before selecting the final observation")
		require.Equal(t, &[5]int64{7, 11, 0, 0, 3}, attempt.Buckets)
		require.NotNil(t, attempt.EstimatedAmount)
		require.Equal(t, "0.1410", *attempt.EstimatedAmount)
		require.Equal(t, "USD", attempt.Currency)
		require.Equal(t, "currency", attempt.Unit)
		require.Equal(t, "native-response-599", attempt.ResponseID, "selection uses observation time/sequence, not lexicographic event ID order")
		require.Equal(t, attempt.ResponseID, attempt.RequestID)
		require.Equal(t, attempt.ID == unknownID, attempt.ExecutedAt.IsZero())
	}
	reconciled, err := biz.ReconcileVendor(m, nil, attempts)
	require.NoError(t, err)
	require.Equal(t, 1, reconciled.UnknownTimeAttempts)
	vendorAttemptBatchAssertReadOnly(t, probe)
	require.LessOrEqual(t, len(probe.queries), 6)
}

func TestVendorAttemptBatchCrossPageEvidenceStillFailsClosed(t *testing.T) {
	for _, scenario := range []struct{ name, wantError string }{
		{"digest", "execution evidence digest mismatch"},
		{"conflicting_send_time", "multiple upstream sends share one intent"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			db, repo, probe, m := vendorAttemptBatchFixture(t)
			var events []executionEventModel
			for n := 0; n < 2; n++ {
				i := vendorAttemptBatchIntent(n, "channel", 1, m)
				require.NoError(t, saveExecutionIntent(db, i))
				for sequence := 0; sequence < 600; sequence++ {
					events = append(events, vendorAttemptBatchEvent(t, i, int64(sequence), m.Scope.Start.Add(time.Hour), m.Scope.End.Add(time.Hour)))
				}
			}
			sort.Slice(events, func(i, j int) bool {
				if events[i].IntentID != events[j].IntentID {
					return events[i].IntentID < events[j].IntentID
				}
				return events[i].ID < events[j].ID
			})
			// This event lies beyond a 1001-row page, within an otherwise valid
			// attempt. Neither pagination nor last-observation selection may hide it.
			last := &events[len(events)-1]
			if scenario.name == "digest" {
				last.Digest = "tampered-after-first-page"
			} else {
				var ev execution.Event
				require.NoError(t, jsonx.Unmarshal([]byte(last.Payload), &ev))
				ev.Record.StartedAtUnixNano += int64(time.Second)
				payload, digest, err := ev.Encode()
				require.NoError(t, err)
				last.Payload, last.Digest = string(payload), digest
			}
			require.NoError(t, db.CreateInBatches(&events, 100).Error)
			_, err := repo.LoadVendorAttempts(context.Background(), m)
			require.ErrorContains(t, err, scenario.wantError)
			vendorAttemptBatchAssertReadOnly(t, probe)
		})
	}
}

func TestVendorAttemptBatchAcrossIntentFlushesPreservesOrderAndUnknownTime(t *testing.T) {
	db, repo, probe, m := vendorAttemptBatchFixture(t)
	const count = 513 // Exercise two complete 256-intent batches and a tail.
	var events []executionEventModel
	var expectedIDs []string
	unknown := map[string]bool{}
	for n := 0; n < count; n++ {
		i := vendorAttemptBatchIntent(n, "channel", 1, m)
		require.NoError(t, saveExecutionIntent(db, i))
		expectedIDs = append(expectedIDs, i.Key())
		if n == 256 {
			unknown[i.Key()] = true
			continue // First intent of the second batch has no response at all.
		}
		sent := m.Scope.Start.Add(time.Hour)
		if n == 255 || n == 512 {
			sent = time.Time{}
			unknown[i.Key()] = true
		}
		events = append(events, vendorAttemptBatchEvent(t, i, 1, sent, m.Scope.End.Add(time.Hour)))
	}
	require.NoError(t, db.CreateInBatches(&events, 100).Error)
	attempts, err := repo.LoadVendorAttempts(context.Background(), m)
	require.NoError(t, err)
	require.Len(t, attempts, count)
	var actualIDs []string
	for _, attempt := range attempts {
		actualIDs = append(actualIDs, attempt.ID)
		require.Equal(t, unknown[attempt.ID], attempt.ExecutedAt.IsZero(), attempt.ID)
	}
	require.Equal(t, expectedIDs, actualIDs, "flushing a batch must preserve the intent ordering and every tail attempt")
	reconciled, err := biz.ReconcileVendor(m, nil, attempts)
	require.NoError(t, err)
	require.Equal(t, 3, reconciled.UnknownTimeAttempts)
	vendorAttemptBatchAssertReadOnly(t, probe)
	t.Logf("%d mapped intents across batch boundaries: %d database queries", count, len(probe.queries))
	require.LessOrEqual(t, len(probe.queries), 6, "batch-boundary and tail loads must remain bounded")
}
