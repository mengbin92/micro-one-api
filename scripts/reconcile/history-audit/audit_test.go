package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
)

func fixture(t *testing.T) []ledger {
	t.Helper()
	raw, err := os.ReadFile("testdata/ledgers.json")
	require.NoError(t, err)
	var rows []ledger
	require.NoError(t, jsonx.Unmarshal(raw, &rows))
	return rows
}

func TestAuditEvidenceAndSplitSettlement(t *testing.T) {
	reports, err := audit(fixture(t))
	require.NoError(t, err)
	require.Len(t, reports, 5)
	require.Equal(t, "verified", reports[0].Classification)
	require.Len(t, reports[0].Ledgers, 2)
	require.EqualValues(t, 110, reports[0].ChargedCost)
	require.EqualValues(t, 94, *reports[0].CanonicalCost)
	require.EqualValues(t, -16, *reports[0].CanonicalDelta)
	require.EqualValues(t, 7, reports[0].Ledgers[0].SubscriptionID)
	require.Equal(t, "subscription", reports[0].Ledgers[0].CostSource)
	require.Equal(t, "balance", reports[0].Ledgers[1].CostSource)
	require.Equal(t, "verified", reports[1].Classification)
	require.EqualValues(t, 0, *reports[1].CanonicalDelta)
	require.Equal(t, "candidate", reports[2].Classification)
	require.EqualValues(t, 94, *reports[2].SubsetCost)
	require.EqualValues(t, 114, *reports[2].ExclusiveCost)
	require.Nil(t, reports[2].CanonicalCost)
	require.Equal(t, "candidate", reports[3].Classification)
	require.Nil(t, reports[3].SubsetDelta, "missing prices must remain unknown, not zero")
	require.Equal(t, "unknown", reports[4].Classification)
	require.Nil(t, reports[4].CanonicalDelta)
}

func TestAuditRejectsIncompleteOrConflictingEvidence(t *testing.T) {
	cases := []struct {
		name           string
		edit           func(*ledger)
		classification string
	}{
		{"partial window", func(l *ledger) { l.RequestLedgerCount = 2 }, "unknown"},
		{"negative tokens", func(l *ledger) { l.Evidence.Read = -1 }, "unknown"},
		{"overflow", func(l *ledger) { l.Evidence.Read = math.MaxInt64 }, "unknown"},
		{"canonical total", func(l *ledger) { l.Evidence.BillableTotal = 1 }, "unknown"},
		{"reported conflict", func(l *ledger) { l.Evidence.ReportedPrompt = 1 }, "unknown"},
		{"future contract", func(l *ledger) { l.Evidence.ContractVersion = 2 }, "unknown"},
		{"missing canonical", func(l *ledger) { l.Evidence.Canonical = 0 }, "unknown"},
		{"missing protocol", func(l *ledger) { l.Evidence.Protocol = "" }, "unknown"},
		{"unknown protocol", func(l *ledger) { l.Evidence.Protocol = "unknown_conversion" }, "unknown"},
		{"unknown shape", func(l *ledger) { l.Evidence.FieldShape = "unrecoverable" }, "unknown"},
		{"missing semantics", func(l *ledger) { l.Evidence.Semantics = "" }, "unknown"},
		{"hash mismatch", func(l *ledger) { l.Evidence.Snapshot.Input *= 2 }, "candidate"},
		{"missing price", func(l *ledger) { l.Evidence.Snapshot = nil }, "candidate"},
		{"source missing", func(l *ledger) { l.Evidence.AccountID = 0 }, "candidate"},
		{"estimated", func(l *ledger) { l.Evidence.ParseStatus = "estimated" }, "candidate"},
		{"positive consume", func(l *ledger) { l.Amount = 10 }, "unknown"},
		{"amount overflow", func(l *ledger) { l.Amount = math.MinInt64 }, "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := fixture(t)[2]
			tc.edit(&row)
			r, err := audit([]ledger{row})
			require.NoError(t, err)
			require.Equal(t, tc.classification, r[0].Classification)
			require.Nil(t, r[0].CanonicalDelta)
		})
	}
	rows := fixture(t)[:2]
	rows[1].Evidence.UpstreamModel = "another-source"
	r, err := audit(rows)
	require.NoError(t, err)
	require.Equal(t, "unknown", r[0].Classification)
	require.Nil(t, r[0].CanonicalDelta)
}

func TestProtocolEvidence(t *testing.T) {
	row := fixture(t)[2]
	row.Evidence.FieldShape = "provider:anthropic_messages"
	r, err := audit([]ledger{row})
	require.NoError(t, err)
	require.Equal(t, "verified", r[0].Classification)
	row.Evidence.FieldShape = "input_tokens+cache_read_input_tokens+details.cached_tokens"
	r, err = audit([]ledger{row})
	require.NoError(t, err)
	require.Equal(t, "unknown", r[0].Classification)
}

func TestFloatRoundingAndCreationMode(t *testing.T) {
	// 25 * 0.000002 * 10000 is one ULP below 0.5, as in the known
	// DECIMAL-vs-float64 audit discrepancy. This input bucket rounds to 0.
	s := &snapshot{Input: 0.000002, Output: 0.001, Ratio: 1, Mode: "charge"}
	require.EqualValues(t, 10, priceBuckets(s, [5]int64{25, 0, 0, 0, 1}))
	s.Write5m, s.Write1h = 0.001, 0.002
	require.EqualValues(t, 70, priceBuckets(s, [5]int64{25, 0, 2, 2, 1}))
	s.Mode = "observe"
	require.EqualValues(t, 10, priceBuckets(s, [5]int64{25, 0, 2, 2, 1}))
	require.EqualValues(t, 1, priceBuckets(s, [5]int64{}))
	s.Input = math.MaxFloat64
	require.EqualValues(t, math.MaxInt64, priceBuckets(s, [5]int64{100, 0, 0, 0, 1}))
}

func TestSnapshotHashUsesFrozenPayload(t *testing.T) {
	e := fixture(t)[2].Evidence
	require.True(t, validSnapshot(e))
	e.Snapshot.Ratio = math.NaN()
	require.False(t, validSnapshot(e))
	e.Snapshot.Ratio = 1
	e.Snapshot.Version = 2
	raw, err := jsonx.Marshal(e.Snapshot)
	require.NoError(t, err)
	e.PricingHash = fmt.Sprintf("%x", sha256.Sum256(raw))
	require.False(t, validSnapshot(e), "future hash formats need explicit support")
}

func TestDeterministicJSONAndCSV(t *testing.T) {
	rows := fixture(t)
	a, err := audit(rows)
	require.NoError(t, err)
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	b, err := audit(rows)
	require.NoError(t, err)
	for _, format := range []string{"json", "csv"} {
		var first, second bytes.Buffer
		require.NoError(t, writeReport(&first, format, a))
		require.NoError(t, writeReport(&second, format, b))
		require.Equal(t, first.String(), second.String())
		if format == "csv" {
			lines, err := csv.NewReader(&first).ReadAll()
			require.NoError(t, err)
			require.Len(t, lines, 6)
			var split []ledger
			require.NoError(t, jsonx.Unmarshal([]byte(lines[1][10]), &split))
			require.Len(t, split, 2)
		}
	}
	_, err = audit(append(rows, rows[0]))
	require.ErrorContains(t, err, "duplicate ledger")
}

func TestCommandOfflineAndValidation(t *testing.T) {
	t.Setenv("HISTORY_AUDIT_DSN", "invalid-user:secret@tcp(invalid-host:1)/db")
	var out, stderr bytes.Buffer
	require.NoError(t, run([]string{"-input", "testdata/ledgers.json"}, &out, &stderr))
	require.NotContains(t, out.String(), "secret")
	for _, args := range [][]string{
		{}, {"-format", "bad"}, {"-input", "testdata/ledgers.json", "-start", "2026-08-01T00:00:00Z"},
		{"-start", "2026-08-02T00:00:00Z", "-end", "2026-08-01T00:00:00Z"},
		{"-input", "testdata/ledgers.json", "-timeout", "0s"}, {"-apply"},
	} {
		out.Reset()
		require.Error(t, run(args, &out, &stderr))
		require.Empty(t, out.String())
	}
}
