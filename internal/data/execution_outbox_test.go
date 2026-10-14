package data

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"io"
	"micro-one-api/domain/execution"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/internal/biz"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func outboxIntent() execution.Event {
	return execution.NewIntentEvent(execution.Intent{ReservationID: "res", RequestID: "attempt", RootRequestID: "root", AttemptNumber: 1, Purpose: "user", SourceKind: "channel", SourceID: 1, Model: "Exact", UsageContractVersion: 2, ParserRevision: evidence.ParserRevision, ContractRevision: evidence.ContractRevision, CostAlgorithmVersion: execution.AlgorithmVersion, Policy: execution.Policy, LeaseUntil: time.Now().Add(time.Minute).Unix(), EvidenceDeadline: time.Now().Add(time.Hour).Unix(), CostBasis: "unknown"}, time.Unix(1, 0))
}
func TestExecutionOutboxRestartConflictAndAck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "outbox.db")
	d, closeDB, err := NewExecutionData(path, nil)
	require.NoError(t, err)
	r := NewExecutionOutboxRepo(d, path)
	e := outboxIntent()
	ctx := context.Background()
	require.NoError(t, r.Admit(ctx, e))
	require.ErrorIs(t, r.Admit(ctx, e), biz.ErrExecutionAlreadyAdmitted)
	require.NoError(t, r.Put(ctx, e))
	changed := e
	changed.BuildRevision = "different"
	require.Error(t, r.Put(ctx, changed))
	closeDB()
	d, closeDB, err = NewExecutionData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	r = NewExecutionOutboxRepo(d, path)
	rows, err := r.Pending(ctx, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, e, rows[0].Event)
	require.ErrorIs(t, r.Admit(ctx, e), biz.ErrExecutionAlreadyAdmitted, "restart cannot retry upstream")
	require.NoError(t, r.Acknowledge(ctx, e.ID, rows[0].Digest))
	rows, err = r.Pending(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Error(t, r.Put(ctx, changed), "ACK tombstone retains the digest")
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	var mode string
	require.NoError(t, d.ExecutionDB.Raw("PRAGMA journal_mode").Scan(&mode).Error)
	require.Equal(t, "wal", mode)
	var sync int
	require.NoError(t, d.ExecutionDB.Raw("PRAGMA synchronous").Scan(&sync).Error)
	require.Equal(t, 2, sync)
}
func TestExecutionOutboxDiskFullRejectsAdmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	d, closeDB, err := NewExecutionData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	var pages int
	require.NoError(t, d.ExecutionDB.Raw("PRAGMA page_count").Scan(&pages).Error)
	require.NoError(t, d.ExecutionDB.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", pages)).Error)
	e := outboxIntent()
	e.Intent.Tariff = []byte(`{"padding":"` + strings.Repeat("x", 20000) + `"}`)
	e.Intent.CostBasis = "tariff_estimate"
	require.Error(t, NewExecutionOutboxRepo(d, path).Admit(context.Background(), e))
}

func TestExecutionOutboxProtectsTerminalAndFinalAfterAdmissionACKAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	d, closeDB, err := NewExecutionData(path, nil)
	require.NoError(t, err)
	r := NewExecutionOutboxRepo(d, path)
	ctx := context.Background()
	i := outboxIntent()
	require.NoError(t, r.Admit(ctx, i))
	_, digest, err := i.Encode()
	require.NoError(t, err)
	require.NoError(t, r.Acknowledge(ctx, i.ID, digest))
	closeDB()
	d, closeDB, err = NewExecutionData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	r = NewExecutionOutboxRepo(d, path)
	// Other already persisted events occupy all unreserved pending slots.
	rows := make([]executionOutboxModel, 9997)
	for n := range rows {
		rows[n] = executionOutboxModel{ID: fmt.Sprintf("fixture-%d", n), Status: "pending", CreatedAt: time.Now().Unix()}
	}
	require.NoError(t, d.ExecutionDB.CreateInBatches(rows, 100).Error)
	checkpoint := execution.Event{Version: 1, Intent: i.Intent, Category: "checkpoint", Sequence: 90, ObservedAt: time.Now().Unix(), Record: &evidence.Record{Execution: "checkpoint"}}
	checkpoint.ID = checkpoint.Key()
	require.Error(t, r.Put(ctx, checkpoint), "checkpoints cannot spend admitted completion capacity")
	header := checkpoint
	header.Sequence, header.Record = 1, &evidence.Record{Execution: "checkpoint", Observation: evidence.Observation{Reason: "response_headers_only"}}
	header.ID = header.Key()
	require.NoError(t, r.Put(ctx, header), "response headers have a separately protected checkpoint slot")
	require.NoError(t, r.Put(ctx, header), "duplicate headers must not consume another reservation")
	terminal := checkpoint
	terminal.Category, terminal.Sequence, terminal.Record = "terminal", 2, &evidence.Record{}
	terminal.ID = terminal.Key()
	require.NoError(t, r.Put(ctx, terminal), "terminal evidence has a protected slot")
	require.NoError(t, r.Put(ctx, terminal), "duplicate terminal must not consume another reservation")
	final := execution.Event{Version: 1, Intent: i.Intent, Category: "final", ObservedAt: time.Now().Unix(), Final: []byte(`{"success":true}`)}
	final.ID = final.Key()
	require.NoError(t, r.Put(ctx, final), "settlement has a separately protected slot")
	count, _, err := r.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(10000), count)
}

func TestExecutionOutboxACKRetentionStartsWhenReceived(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	d, closeDB, err := NewExecutionData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	r := NewExecutionOutboxRepo(d, path)
	ctx := context.Background()
	i := outboxIntent()
	require.NoError(t, r.Admit(ctx, i))
	require.NoError(t, d.ExecutionDB.Model(&executionOutboxModel{}).Where("id = ?", i.ID).Update("created_at", time.Now().Add(-8*24*time.Hour).Unix()).Error)
	_, digest, err := i.Encode()
	require.NoError(t, err)
	require.NoError(t, r.Acknowledge(ctx, i.ID, digest))
	other := i
	other.Intent.ReservationID = "other"
	other.ID = other.Key()
	require.NoError(t, r.Admit(ctx, other))
	require.ErrorIs(t, r.Admit(ctx, i), biz.ErrExecutionAlreadyAdmitted, "a just-received old event keeps its seven-day tombstone")
}

func TestExecutionOutboxRejectsUnknownOrMismatchedACK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	d, closeDB, err := NewExecutionData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	r := NewExecutionOutboxRepo(d, path)
	i := outboxIntent()
	require.NoError(t, r.Admit(context.Background(), i))
	require.ErrorIs(t, r.Acknowledge(context.Background(), i.ID, "different"), biz.ErrExecutionReceipt)
	require.ErrorIs(t, r.Acknowledge(context.Background(), "unknown", "different"), biz.ErrExecutionReceipt)
}

func TestExecutionOutboxReservesCompletionDiskIncludingWAL(t *testing.T) {
	dir := t.TempDir()
	d, closeDB, err := NewExecutionData(filepath.Join(dir, "actual.db"), nil)
	require.NoError(t, err)
	defer closeDB()
	// The capacity fixture isolates file-size accounting without corrupting the
	// live database by padding its WAL. The repo accounts for every sidecar.
	path := filepath.Join(dir, "budget.db")
	r := NewExecutionOutboxRepo(d, path).(*executionOutboxRepo)
	f, err := os.Create(path + "-wal")
	require.NoError(t, err)
	require.NoError(t, f.Truncate(outboxMaxBytes-4*r.diskReserve+1))
	require.NoError(t, f.Close())
	i := outboxIntent()
	require.Error(t, r.Admit(context.Background(), i), "intent + headers + terminal + final must fit including WAL growth")
	require.NoError(t, os.Truncate(path+"-wal", outboxMaxBytes-4*r.diskReserve))
	require.NoError(t, r.Admit(context.Background(), i))
	var saved executionOutboxModel
	require.NoError(t, d.ExecutionDB.Where("id = ?", i.ID).Take(&saved).Error)
	require.Equal(t, int64(1), saved.HeaderReserved)
	require.Equal(t, int64(1), saved.TerminalReserved)
	require.Equal(t, int64(1), saved.FinalReserved)
}

func TestExecutionOutboxExpiresCapacityWithoutDiscardingLateEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	d, closeDB, err := NewExecutionData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	r := NewExecutionOutboxRepo(d, path)
	ctx := context.Background()
	i := outboxIntent()
	i.Intent.ReservationID, i.Intent.Purpose = "", "canary"
	i.ID = i.Key()
	require.NoError(t, r.Admit(ctx, i))
	var saved executionOutboxModel
	require.NoError(t, d.ExecutionDB.Where("id = ?", i.ID).Take(&saved).Error)
	require.Equal(t, int64(1), saved.HeaderReserved)
	require.Equal(t, int64(1), saved.TerminalReserved)
	require.Zero(t, saved.FinalReserved, "internal attempts have no user settlement to reserve")
	require.NoError(t, d.ExecutionDB.Model(&executionOutboxModel{}).Where("id = ?", i.ID).Update("evidence_deadline", time.Now().Add(-time.Second).Unix()).Error)
	terminal := execution.Event{Version: 1, Intent: i.Intent, Category: "terminal", Sequence: 1, ObservedAt: time.Now().Unix(), Record: &evidence.Record{}}
	terminal.ID = terminal.Key()
	require.NoError(t, r.Put(ctx, terminal), "late supplier evidence remains admissible within unreserved capacity")
	require.NoError(t, d.ExecutionDB.Where("id = ?", i.ID).Take(&saved).Error)
	require.Zero(t, saved.HeaderReserved)
	require.Zero(t, saved.TerminalReserved)
	require.ErrorIs(t, r.Admit(ctx, i), biz.ErrExecutionAlreadyAdmitted, "expiry never authorizes an upstream replay")
	changed := terminal
	changed.Sequence++
	changed.Intent.Model = "changed"
	changed.ID = changed.Key()
	require.Error(t, r.Put(ctx, changed), "a completion cannot spend another frozen intent's capacity")
}

func TestExecutionOutboxUpgradesEarlierSchemaAndPendingIntent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	legacy, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, legacy.Exec(`CREATE TABLE execution_outbox (id TEXT PRIMARY KEY, digest TEXT, payload TEXT, status TEXT, next_retry INTEGER, created_at INTEGER)`).Error)
	i := outboxIntent()
	raw, digest, err := i.Encode()
	require.NoError(t, err)
	require.NoError(t, legacy.Exec(`INSERT INTO execution_outbox VALUES (?, ?, ?, 'pending', 0, ?)`, i.ID, digest, string(raw), time.Now().Unix()).Error)
	require.NoError(t, legacy.Exec(`INSERT INTO execution_outbox VALUES ('old-ack', 'retained', '', 'acknowledged', 0, ?)`, time.Now().Add(-8*24*time.Hour).Unix()).Error)
	sqlDB, err := legacy.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	d, closeDB, err := NewExecutionData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	var saved executionOutboxModel
	require.NoError(t, d.ExecutionDB.Where("id = ?", i.ID).Take(&saved).Error)
	require.Equal(t, int64(1), saved.HeaderReserved)
	require.Equal(t, int64(1), saved.TerminalReserved)
	require.Equal(t, int64(1), saved.FinalReserved)
	require.NotEmpty(t, saved.IntentDigest)
	var old executionOutboxModel
	require.NoError(t, d.ExecutionDB.Where("id = ?", "old-ack").Take(&old).Error)
	require.GreaterOrEqual(t, old.AcknowledgedAt, time.Now().Add(-time.Minute).Unix())
}

func TestExecutionOutboxFailedCompletionWriteKeepsReservationForRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	d, closeDB, err := NewExecutionData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	r := NewExecutionOutboxRepo(d, path)
	ctx := context.Background()
	i := outboxIntent()
	require.NoError(t, r.Admit(ctx, i))
	var pages int
	require.NoError(t, d.ExecutionDB.Raw("PRAGMA page_count").Scan(&pages).Error)
	require.NoError(t, d.ExecutionDB.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", pages)).Error)
	final := execution.Event{Version: 1, Intent: i.Intent, Category: "final", ObservedAt: time.Now().Unix(), Final: []byte(`{"padding":"` + strings.Repeat("x", execution.MaxEventBytes-2048) + `"}`)}
	final.ID = final.Key()
	raw, _, err := final.Encode()
	require.NoError(t, err)
	require.Greater(t, len(raw), execution.MaxEventBytes-2048, "exercise an almost maximum-size legal event")
	require.Error(t, r.Put(ctx, final))
	var saved executionOutboxModel
	require.NoError(t, d.ExecutionDB.Where("id = ?", i.ID).Take(&saved).Error)
	require.Equal(t, int64(1), saved.FinalReserved, "failed insertion rolls back reservation consumption")
	require.NoError(t, d.ExecutionDB.Exec("PRAGMA max_page_count=32768").Error)
	require.NoError(t, r.Put(ctx, final))
	require.NoError(t, d.ExecutionDB.Where("id = ?", i.ID).Take(&saved).Error)
	require.Zero(t, saved.FinalReserved)
}

func TestExecutionOutboxFullCapacityPreservesRealHTTPHeadersTerminalAndSettlement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	d, closeDB, err := NewExecutionData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	r := NewExecutionOutboxRepo(d, path)
	ctx := context.Background()
	i := outboxIntent()
	require.NoError(t, r.Admit(ctx, i))
	_, digest, err := i.Encode()
	require.NoError(t, err)
	require.NoError(t, r.Acknowledge(ctx, i.ID, digest))
	rows := make([]executionOutboxModel, 9997)
	for n := range rows {
		rows[n] = executionOutboxModel{ID: fmt.Sprintf("fixture-%d", n), Status: "pending", CreatedAt: time.Now().Unix()}
	}
	require.NoError(t, d.ExecutionDB.CreateInBatches(rows, 100).Error)
	// Optional usage checkpoints must leave room for the mandatory post-send
	// response-headers write performed by evidence.Do before it wraps the body.
	optional := execution.Event{Version: 1, Intent: i.Intent, Category: "checkpoint", Sequence: 90, ObservedAt: time.Now().Unix(), Record: &evidence.Record{Execution: "checkpoint"}}
	optional.ID = optional.Key()
	checkpointErr := r.Put(ctx, optional)
	sequence := int64(0)
	var records []evidence.Record
	ctx = evidence.WithDurableRecorder(ctx, nil, func(record evidence.Record) error {
		sequence++
		e := execution.Event{Version: 1, Intent: i.Intent, Category: "terminal", Sequence: sequence, ObservedAt: time.Now().Unix(), Record: &record}
		if record.Execution == "checkpoint" {
			e.Category = "checkpoint"
		}
		e.ID = e.Key()
		if err := r.Put(ctx, e); err != nil {
			return err
		}
		records = append(records, record)
		return nil
	})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}}`))
	}))
	defer upstream.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.URL+"/chat/completions", strings.NewReader(`{"model":"Exact"}`))
	require.NoError(t, err)
	resp, err := evidence.Do(upstream.Client(), req, "openai_chat")
	require.NoError(t, err, "a completed upstream response must retain its mandatory headers at full capacity")
	require.Error(t, checkpointErr, "an optional checkpoint cannot consume the response-headers reservation")
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Len(t, records, 2)
	require.Equal(t, "response_headers_only", records[0].Observation.Reason)
	require.Equal(t, "verified", records[1].Observation.Status)
	require.True(t, records[1].Complete)
	final := execution.Event{Version: 1, Intent: i.Intent, Category: "final", ObservedAt: time.Now().Unix(), Final: []byte(`{"success":true}`)}
	final.ID = final.Key()
	require.NoError(t, r.Put(ctx, final))
	count, _, err := r.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(10000), count)
}

func legacyOutboxWithIntents(t *testing.T, count int) (string, execution.Event) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "outbox.db")
	legacy, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, legacy.Exec(`CREATE TABLE execution_outbox (id TEXT PRIMARY KEY, digest TEXT, payload TEXT, status TEXT, next_retry INTEGER, created_at INTEGER)`).Error)
	first := outboxIntent()
	rows := make([]map[string]any, count)
	for n := range rows {
		i := first
		i.Intent.ReservationID = fmt.Sprintf("legacy-res-%d", n)
		i.ID = i.Key()
		if n == 0 {
			first = i
		}
		raw, digest, err := i.Encode()
		require.NoError(t, err)
		rows[n] = map[string]any{"id": i.ID, "digest": digest, "payload": string(raw), "status": "pending", "next_retry": 0, "created_at": time.Now().Unix()}
	}
	require.NoError(t, legacy.Table("execution_outbox").CreateInBatches(rows, 100).Error)
	sqlDB, err := legacy.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	return path, first
}

func TestExecutionOutboxLegacyOversubscriptionKeepsCompletionLiveAndRejectsNewAdmission(t *testing.T) {
	path, i := legacyOutboxWithIntents(t, 500)
	d, closeDB, err := NewExecutionData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	r := NewExecutionOutboxRepo(d, path)
	ctx := context.Background()
	newIntent := outboxIntent()
	require.Error(t, r.Admit(ctx, newIntent), "a legacy backlog cannot authorize new promises exceeding disk capacity")
	terminal := execution.Event{Version: 1, Intent: i.Intent, Category: "terminal", Sequence: 1, ObservedAt: time.Now().Unix(), Record: &evidence.Record{}}
	terminal.ID = terminal.Key()
	require.NoError(t, r.Put(ctx, terminal), "unfulfillable legacy future reservations cannot deadlock already completed evidence")
}

func TestExecutionOutboxExpiredLegacyReservationsAreClearedWithoutRollbackDeadlock(t *testing.T) {
	path, _ := legacyOutboxWithIntents(t, 500)
	d, closeDB, err := NewExecutionData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	r := NewExecutionOutboxRepo(d, path)
	require.NoError(t, d.ExecutionDB.Model(&executionOutboxModel{}).Where("evidence_deadline > 0").Update("evidence_deadline", time.Now().Add(-time.Second).Unix()).Error)
	require.NoError(t, r.Admit(context.Background(), outboxIntent()), "expired cleanup must commit progress before checking the next admission budget")
	var oldReserved int64
	require.NoError(t, d.ExecutionDB.Model(&executionOutboxModel{}).Where("evidence_deadline <= ?", time.Now().Unix()).Select("COALESCE(SUM(header_reserved + terminal_reserved + final_reserved),0)").Scan(&oldReserved).Error)
	require.Zero(t, oldReserved)
}
