package data

import (
	"fmt"
	"math/big"
	"time"

	"gorm.io/gorm"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/domain/execution"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/pkg/jsonx"
)

const (
	vendorIntentBatchSize  = 256
	vendorEventPageSize    = 1001
	vendorAttemptMaxEvents = 1000
)

// A batch retains only one page of raw events and the selected terminal per
// intent. Unknown send times remain evidence; inbox time never selects a period.
type vendorAttemptReader struct {
	intent        execution.Intent
	row           executionIntentModel
	attempt       biz.VendorLocalAttempt
	eventCount    int
	selected      *execution.Event
	estimatedCost *int64
	headerID      string
}

func newVendorAttemptReader(row executionIntentModel, intent execution.Intent) vendorAttemptReader {
	return vendorAttemptReader{row: row, intent: intent, attempt: biz.VendorLocalAttempt{
		ID: row.ID, RootRequestID: intent.RootRequestID, ParserRevision: intent.ParserRevision,
		ContractRevision: intent.ContractRevision, TariffSnapshotID: intent.TariffSnapshotID,
		CostAlgorithmVersion: intent.CostAlgorithmVersion, SettlementPolicy: intent.Policy,
		ReservationID: intent.ReservationID, SourceKind: intent.SourceKind,
		SourceID: intent.SourceID, Model: intent.Model, Purpose: intent.Purpose,
	}}
}

func loadVendorEventBatch(tx *gorm.DB, batch []vendorAttemptReader) error {
	keys := make([]string, 0, len(batch))
	readers := make(map[string]*vendorAttemptReader, len(batch))
	for i := range batch {
		keys = append(keys, batch[i].row.ID)
		readers[batch[i].row.ID] = &batch[i]
	}
	lastIntent, lastEvent := "", ""
	for {
		query := tx.Where("intent_id IN ?", keys).Order("intent_id ASC, id ASC").Limit(vendorEventPageSize)
		if lastIntent != "" {
			query = query.Where("intent_id > ? OR (intent_id = ? AND id > ?)", lastIntent, lastIntent, lastEvent)
		}
		var page []executionEventModel
		if err := query.Find(&page).Error; err != nil {
			return err
		}
		for i := range page {
			reader := readers[page[i].IntentID]
			if reader == nil {
				return fmt.Errorf("execution evidence has no selected intent")
			}
			if err := reader.accept(page[i]); err != nil {
				return err
			}
		}
		if len(page) < vendorEventPageSize {
			return nil
		}
		last := page[len(page)-1]
		lastIntent, lastEvent = last.IntentID, last.ID
	}
}

func (r *vendorAttemptReader) accept(row executionEventModel) error {
	r.eventCount++
	if r.eventCount > vendorAttemptMaxEvents {
		return fmt.Errorf("attempt event limit exceeded")
	}
	var event execution.Event
	if jsonx.Unmarshal([]byte(row.Payload), &event) != nil {
		return fmt.Errorf("corrupt execution event %s", row.ID)
	}
	_, digest, err := event.Encode()
	if err != nil || digest != row.Digest || event.Intent.Key() != r.row.ID || string(mustIntentJSON(event.Intent)) != r.row.Payload {
		return fmt.Errorf("execution evidence digest mismatch %s", row.ID)
	}
	r.attempt.EvidenceIDs = append(r.attempt.EvidenceIDs, row.ID)
	if event.Category == "checkpoint" && event.Record != nil && event.Record.Observation.Reason == "response_headers_only" {
		r.headerID = event.Record.Observation.ProviderRequestID
	}
	if event.Record != nil && event.Record.StartedAtUnixNano > 0 {
		sent := time.Unix(0, event.Record.StartedAtUnixNano).UTC()
		if !r.attempt.ExecutedAt.IsZero() && !r.attempt.ExecutedAt.Equal(sent) {
			return fmt.Errorf("multiple upstream sends share one intent %s", r.row.ID)
		}
		r.attempt.ExecutedAt = sent
	}
	if event.Record != nil && (event.Category == "terminal" || event.Category == "final") && (r.selected == nil || event.ObservedAt > r.selected.ObservedAt || event.ObservedAt == r.selected.ObservedAt && event.Sequence > r.selected.Sequence) {
		r.selected = &event
		r.estimatedCost = nil
		if row.EstimatedCost != nil {
			value := *row.EstimatedCost
			r.estimatedCost = &value
		}
	}
	return nil
}

func (r *vendorAttemptReader) finish(m biz.VendorManifest) (biz.VendorLocalAttempt, bool, error) {
	a := r.attempt
	if !a.ExecutedAt.IsZero() && (a.ExecutedAt.Before(m.Scope.Start) || !a.ExecutedAt.Before(m.Scope.End)) {
		return a, false, nil
	}
	selected := r.selected
	if selected == nil {
		return a, true, nil
	}
	a.BuildRevision = selected.BuildRevision
	a.ResponseID = selected.Record.Observation.ProviderRequestID
	a.RequestID = r.headerID
	if m.RequestIDKind == "response" {
		a.RequestID = a.ResponseID
	}
	a.Complete = selected.Record.Complete
	if selected.Record.Observation.Status == "verified" {
		field := "usage"
		if selected.Record.Observation.Protocol == "gemini" {
			field = "usageMetadata"
		}
		body, _ := jsonx.Marshal(map[string]any{field: selected.Record.Observation.Usage})
		parsed := evidence.Parse(body, selected.Record.Observation.Protocol)
		if parsed.Status != "verified" || parsed.Buckets == nil || selected.Record.Observation.Buckets == nil || *parsed.Buckets != *selected.Record.Observation.Buckets {
			return a, false, fmt.Errorf("native usage proof mismatch %s", selected.ID)
		}
		a.Buckets = parsed.Buckets
	}
	// Preserve the frozen USD/AmountScale estimate, including its currency.
	if r.estimatedCost != nil {
		computed := biz.SupplierEstimate(r.intent, selected.Record)
		if computed == nil || *computed != *r.estimatedCost {
			return a, false, fmt.Errorf("frozen tariff estimate mismatch %s", selected.ID)
		}
		value := new(big.Rat).SetFrac64(*r.estimatedCost, 10000).FloatString(4)
		a.EstimatedAmount, a.Currency, a.Unit = &value, "USD", "currency"
	}
	return a, true, nil
}
