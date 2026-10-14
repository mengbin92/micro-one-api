package data

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/domain/authorization"
	authzt "micro-one-api/domain/authorization/testutil"
	"micro-one-api/domain/execution"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/testutil"
)

func vendorRepoManifest() biz.VendorManifest {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	return biz.VendorManifest{Scope: biz.VendorScope{Provider: "supplier", Account: "ExactAccount", Currency: "USD", Unit: "currency", Start: start, End: end, Timezone: "Asia/Shanghai", Granularity: "account_period"}, Version: "Original", EvidenceID: "invoice", CostBasis: "invoice", Mode: "tokens", Complete: true, PagesComplete: true, VendorWatermark: end, LocalWatermark: end, CoverageEvidenceID: "coverage", AbsoluteTolerance: "0", RelativeTolerance: "0", Actor: "operator", Reason: "independent account trial", Mappings: []biz.VendorSourceMapping{{SourceKind: "channel", SourceID: 1, Provider: "supplier", Account: "ExactAccount", Start: start, End: end, Revision: "mapping-v1", EvidenceID: "account-key-proof"}}}
}

func TestVendorImmutableReportsAndCorrectionsAcrossDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := testutil.RoutingContextDB(t, driver)
			r := NewVendorReconciliationRepo(&Data{db: db})
			ctx := context.Background()
			m := vendorRepoManifest()
			value := "0.000000000000000001"
			rows := []biz.VendorBillRow{{Amount: &value}}
			report, err := biz.ReconcileVendor(m, rows, nil)
			require.NoError(t, err)
			require.NoError(t, r.StoreVendorReconciliation(ctx, &report))
			require.NoError(t, r.StoreVendorReconciliation(ctx, &report))
			var imports, runs int64
			require.NoError(t, db.Model(&vendorReportModel{}).Count(&imports).Error)
			require.EqualValues(t, 1, imports)
			require.NoError(t, db.Model(&vendorRunModel{}).Count(&runs).Error)
			require.EqualValues(t, 1, runs)
			// A local replay creates a run; the supplier original remains unchanged.
			original := report.ID
			m.LocalWatermark = m.Scope.Start
			replay, err := biz.ReconcileVendor(m, rows, nil)
			require.NoError(t, err)
			require.Equal(t, original, replay.ID)
			require.NoError(t, r.StoreVendorReconciliation(ctx, &replay))
			two := "0.000000000000000002"
			rows[0].Amount = &two
			conflict, err := biz.ReconcileVendor(m, rows, nil)
			require.NoError(t, err)
			require.Error(t, r.StoreVendorReconciliation(ctx, &conflict))
			m.Version = "Correction"
			withoutPredecessor, err := biz.ReconcileVendor(m, rows, nil)
			require.NoError(t, err)
			require.Error(t, r.StoreVendorReconciliation(ctx, &withoutPredecessor))
			m.Supersedes = original
			correction, err := biz.ReconcileVendor(m, rows, nil)
			require.NoError(t, err)
			require.NoError(t, r.StoreVendorReconciliation(ctx, &correction))
			require.Equal(t, "0.000000000000000001", *correction.CorrectionDelta)
			require.NoError(t, r.StoreVendorReconciliation(ctx, &correction))
			// Concurrent/forked correction chains cannot consume a predecessor twice.
			m.Version = "Fork"
			fork, err := biz.ReconcileVendor(m, rows, nil)
			require.NoError(t, err)
			require.Error(t, r.StoreVendorReconciliation(ctx, &fork))
			var persisted vendorReportModel
			require.NoError(t, db.Where("id = ?", original).First(&persisted).Error)
			var old biz.VendorReconciliation
			require.NoError(t, jsonx.Unmarshal([]byte(persisted.Payload), &old))
			require.Equal(t, value, *old.Rows[0].Amount)
			var ledgers int64
			require.NoError(t, db.Table("billing_ledgers").Count(&ledgers).Error)
			require.Zero(t, ledgers)
		})
	}
}

func TestVendorAttemptEvidenceUsesSendTimeAndDedupesTerminalFinal(t *testing.T) {
	db := testutil.RoutingContextDB(t, "sqlite")
	r := NewVendorReconciliationRepo(&Data{db: db})
	m := vendorRepoManifest()
	ctx := context.Background()
	i := execution.Freeze(execution.Intent{ReservationID: "res", RequestID: "attempt", RootRequestID: "root", AttemptNumber: 1, Purpose: "user", SourceKind: "channel", SourceID: 1, Model: "vendor/Exact", Policy: execution.Policy, CostBasis: "tariff_estimate", Tariff: jsonx.RawMessage(`{"input_price":0,"output_price":0}`), LeaseUntil: m.Scope.End.Unix(), EvidenceDeadline: m.Scope.End.Add(time.Hour).Unix()})
	require.NoError(t, saveExecutionIntent(db, i))
	startedAt := m.Scope.Start.Add(time.Hour).UnixNano()
	record := &evidence.Record{StartedAtUnixNano: startedAt, Complete: true, HTTPStatus: 200, Observation: evidence.Parse([]byte(`{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`), "openai_chat")}
	record.Observation.ProviderRequestID = "supplier-req"
	zero := int64(0)
	for _, category := range []string{"checkpoint", "terminal", "final"} {
		ev := execution.Event{Version: 1, Intent: i, Category: category, Sequence: 1, ObservedAt: startedAt/int64(time.Second) + 1, Record: record}
		if category == "final" {
			ev.Sequence = 0
			ev.Final = jsonx.RawMessage(`{"reservation_id":"res","success":true}`)
		}
		if category == "checkpoint" {
			copy := *record
			copy.Observation.Reason = "response_headers_only"
			copy.Observation.ProviderRequestID = "header-req"
			ev.Record = &copy
		}
		ev.ID = ev.Key()
		payload, digest, err := ev.Encode()
		require.NoError(t, err)
		require.NoError(t, db.Create(&executionEventModel{ID: ev.ID, IntentID: i.Key(), Category: category, Payload: string(payload), Digest: digest, ReceivedAt: m.Scope.End.Add(24 * time.Hour).Unix(), EstimatedCost: &zero}).Error)
	}
	local, err := r.LoadVendorAttempts(ctx, m)
	require.NoError(t, err)
	require.Len(t, local, 1)
	require.Equal(t, "0.0000", *local[0].EstimatedAmount)
	require.Equal(t, "header-req", local[0].RequestID)
	require.Equal(t, "supplier-req", local[0].ResponseID)
	require.Equal(t, time.Unix(0, startedAt).UTC(), local[0].ExecutedAt)
	require.Len(t, local[0].EvidenceIDs, 3)
	require.NotNil(t, local[0].Buckets)
	// Inbox reception dates are deliberately outside the fixed period.
	rows := []biz.VendorBillRow{{Amount: local[0].EstimatedAmount, Buckets: local[0].Buckets}}
	out, err := biz.ReconcileVendor(m, rows, local)
	require.NoError(t, err)
	require.Equal(t, "matched", out.Status)
	require.Len(t, out.Comparisons[0].Attempts, 1)
	require.NoError(t, db.Model(&executionEventModel{}).Where("category = ?", "terminal").Update("digest", "tampered").Error)
	_, err = r.LoadVendorAttempts(ctx, m)
	require.ErrorContains(t, err, "digest mismatch")
}

func TestVendorReportIAMReadImportAreIndependentAndDailyKeepsCurrentLeaf(t *testing.T) {
	db := testutil.RoutingContextDB(t, "sqlite")
	r := NewVendorReconciliationRepo(&Data{db: db})
	m := vendorRepoManifest()
	value := "0"
	rows := []biz.VendorBillRow{{Amount: &value}}
	report, err := biz.ReconcileVendor(m, rows, nil)
	require.NoError(t, err)
	denied := authorization.WithQueryScope(context.Background(), "billing.vendor_report.import", authzt.Resources(1))
	require.Error(t, r.StoreVendorReconciliation(denied, &report))
	require.NoError(t, r.StoreVendorReconciliation(context.Background(), &report))
	m.Version = "next"
	m.Supersedes = report.ID
	correction, err := biz.ReconcileVendor(m, rows, nil)
	require.NoError(t, err)
	require.NoError(t, r.StoreVendorReconciliation(context.Background(), &correction))
	current, err := r.ListVendorReconciliations(context.Background(), time.Now().Add(-time.Hour), time.Now().Add(time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, current, 1)
	require.Equal(t, correction.ID, current[0].ID)
	readDenied := authorization.WithQueryScope(context.Background(), "billing.vendor_report.read", authzt.Resources(1))
	_, err = r.ListVendorReconciliations(readDenied, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), 10)
	require.Error(t, err)
}
