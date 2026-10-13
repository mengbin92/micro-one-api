package data

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/execution"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/internal/biz"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func outboxIntent() execution.Event {
	return execution.NewIntentEvent(execution.Intent{ReservationID: "res", RequestID: "attempt", RootRequestID: "root", AttemptNumber: 1, Purpose: "user", SourceKind: "channel", SourceID: 1, Model: "Exact", UsageContractVersion: 2, ParserRevision: evidence.ParserRevision, ContractRevision: evidence.ContractRevision, CostAlgorithmVersion: execution.AlgorithmVersion, Policy: execution.Policy, LeaseUntil: 10, EvidenceDeadline: 20, CostBasis: "unknown"}, time.Unix(1, 0))
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
