package data

import (
	"context"
	"errors"
	"os"
	"time"

	"gorm.io/gorm"
	"micro-one-api/app/billing/internal/biz"
	billingdomain "micro-one-api/domain/billing"
	"micro-one-api/domain/execution"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/authzquery"
)

type executionIntentModel struct {
	Status           string `gorm:"size:32"`
	ID               string `gorm:"column:id;primaryKey;size:200"`
	ReservationID    string `gorm:"column:reservation_id;index;size:128"`
	Payload          string `gorm:"column:payload;type:text"`
	LeaseUntil       int64  `gorm:"column:lease_until"`
	EvidenceDeadline int64  `gorm:"column:evidence_deadline;index"`
}

func (executionIntentModel) TableName() string { return "billing_execution_intents" }

type executionEventModel struct {
	Category      string `gorm:"size:16"`
	SourceKind    string `gorm:"size:32"`
	SourceID      int64
	Model         string `gorm:"column:upstream_model_id;size:255"`
	Protocol      string `gorm:"size:64"`
	Purpose       string `gorm:"size:32"`
	EstimatedCost *int64
	CostBasis     string `gorm:"size:32"`
	ID            string `gorm:"column:id;primaryKey;size:255"`
	IntentID      string `gorm:"column:intent_id;index;size:200"`
	Digest        string `gorm:"column:digest;size:64"`
	Payload       string `gorm:"column:payload;type:text"`
	ReceivedAt    int64  `gorm:"column:received_at"`
}

func (executionEventModel) TableName() string { return "upstream_execution_evidence" }
func (r *reservationRepo) ExecutionAvailable(ctx context.Context) bool {
	db := r.data.db.WithContext(ctx)
	return db.Migrator().HasTable(&executionIntentModel{}) && db.Migrator().HasTable(&executionEventModel{}) && db.Migrator().HasTable(&settlementTaskModel{}) && db.Migrator().HasColumn(&executionEventModel{}, "EstimatedCost") && db.Migrator().HasColumn(&executionIntentModel{}, "Status")
}
func saveExecutionIntent(tx *gorm.DB, i execution.Intent) error {
	b, err := jsonx.Marshal(i)
	if err != nil {
		return err
	}
	return tx.Create(&executionIntentModel{ID: i.Key(), ReservationID: i.ReservationID, Status: "processing", Payload: string(b), LeaseUntil: i.LeaseUntil, EvidenceDeadline: i.EvidenceDeadline}).Error
}
func (r *reservationRepo) AcceptExecution(ctx context.Context, e execution.Event, digest string) (receipt execution.Receipt, err error) {
	b, d, err := e.Encode()
	if err != nil || d != digest {
		return receipt, biz.ErrSettlementReceiptInvalid
	}
	err = authzquery.RunInTx(ctx, r.data.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		var reservation *biz.Reservation
		if e.Intent.Purpose == "user" {
			var err error
			reservation, err = r.GetReservationInTx(ctx, &gormTx{db: tx}, e.Intent.ReservationID)
			if err != nil {
				return err
			}
		}
		var intent executionIntentModel
		q := tx.Where("id = ?", e.Intent.Key())
		if !isSQLite(dialectorName(tx)) {
			q = q.Clauses(forUpdateClause(dialectorName(tx)))
		}
		err := q.First(&intent).Error
		if errors.Is(err, gorm.ErrRecordNotFound) && e.Intent.Purpose != "user" && e.Category == "intent" {
			if os.Getenv("BILLING_EXECUTION_DELIVERY_V1") != "true" {
				return biz.ErrSettlementReceiptUnavailable
			}
			if err = saveExecutionIntent(tx, e.Intent); err != nil {
				return err
			}
			intent.Payload = string(mustIntentJSON(e.Intent))
		} else if err != nil {
			return biz.ErrSettlementReceiptInvalid
		}
		if intent.Payload != string(mustIntentJSON(e.Intent)) {
			return biz.ErrSettlementPayloadConflict
		}
		var prior executionEventModel
		if err = tx.Where("id = ?", e.ID).Limit(1).Find(&prior).Error; err != nil {
			return err
		}
		if prior.ID != "" && prior.Digest != digest {
			return biz.ErrSettlementPayloadConflict
		}
		if prior.ID == "" {
			row := executionEventModel{Category: e.Category, ID: e.ID, IntentID: e.Intent.Key(), Digest: digest, Payload: string(b), ReceivedAt: time.Now().Unix(), SourceKind: e.Intent.SourceKind, SourceID: e.Intent.SourceID, Model: e.Intent.Model, Purpose: e.Intent.Purpose, CostBasis: "unknown"}
			if e.Record != nil {
				row.Protocol = e.Record.Observation.Protocol
				row.EstimatedCost = biz.SupplierEstimate(e.Intent, e.Record)
				if row.EstimatedCost != nil {
					row.CostBasis = "tariff_estimate"
				}
			}
			if err = tx.Create(&row).Error; err != nil {
				return err
			}
		}
		if e.Category == "checkpoint" && reservation != nil && reservation.IsReserved() && time.Now().Unix() < e.Intent.EvidenceDeadline {
			until := time.Now().Add(5 * time.Minute).Unix()
			if until > e.Intent.EvidenceDeadline {
				until = e.Intent.EvidenceDeadline
			}
			if err := tx.Model(&executionIntentModel{}).Where("id = ?", e.Intent.Key()).Updates(map[string]any{"lease_until": until, "status": "processing"}).Error; err != nil {
				return err
			}
		}
		if e.Category == "terminal" {
			if err := tx.Model(&executionIntentModel{}).Where("id = ?", e.Intent.Key()).Update("status", "evidence_received").Error; err != nil {
				return err
			}
		}
		receipt = execution.Receipt{Received: true, Digest: digest, Status: "evidence_received"}
		if reservation != nil && !reservation.IsReserved() {
			receipt.Status = reservation.Status
		}
		if e.Intent.Purpose != "user" && e.Record != nil {
			var real executionEventModel
			if err := tx.Where("source_kind = ? AND source_id = ? AND upstream_model_id = ? AND protocol = ? AND purpose = ? AND category = ?", e.Intent.SourceKind, e.Intent.SourceID, e.Intent.Model, e.Record.Observation.Protocol, "user", "terminal").Order("received_at DESC").Limit(1).Find(&real).Error; err != nil {
				return err
			}
			if real.ID != "" {
				var event execution.Event
				if jsonx.Unmarshal([]byte(real.Payload), &event) != nil {
					return biz.ErrSettlementReceiptInvalid
				}
				if event.Record != nil {
					receipt.Comparison, _ = jsonx.Marshal(map[string]any{"event_id": real.ID, "digest": real.Digest, "received_at": real.ReceivedAt, "record": event.Record, "supplier_cost_basis": real.CostBasis, "supplier_estimated_cost": real.EstimatedCost, "verification_scope": "none"})
				}
			}
		}
		if e.Category != "final" {
			return nil
		}
		if reservation == nil {
			return biz.ErrSettlementReceiptInvalid
		}
		// Financially closed attempts still retain late procurement evidence above.
		if reservation.Status == biz.ReservationStatusReleased || reservation.Status == biz.ReservationStatusExpired {
			return nil
		}
		if reservation.Status != biz.ReservationStatusCommitted && time.Now().Unix() >= e.Intent.EvidenceDeadline {
			receipt.Status = "evidence_deadline_elapsed"
			return nil
		}
		var p billingdomain.SettlementPayload
		if jsonx.Unmarshal(e.Final, &p) != nil || p.ReservationID != e.Intent.ReservationID || !p.Success {
			return biz.ErrSettlementReceiptInvalid
		}
		var usage biz.LedgerUsage
		if jsonx.Unmarshal(p.Usage, &usage) != nil || usage.UsageContractVersion != 2 || biz.ValidateV2Envelope(usage.Envelope) != "" {
			return biz.ErrSettlementReceiptInvalid
		}
		if !biz.ValidateNativeFinal(e.Intent, e.Record, usage) {
			return biz.ErrSettlementReceiptInvalid
		}
		if usage.Envelope.ParseStatus == "verified" && (usage.Envelope.ParserRevision != e.Intent.ParserRevision || usage.Envelope.ContractRevision != e.Intent.ContractRevision) {
			return biz.ErrSettlementReceiptInvalid
		}
		accountID := int64(0)
		if e.Intent.SourceKind == "subscription" {
			accountID = e.Intent.SourceID
		}
		if p.SubscriptionAccountID != accountID || usage.SubscriptionAccountID != accountID {
			return biz.ErrSettlementReceiptInvalid
		}
		if usage.UpstreamCost != 0 || usage.Envelope.Canonical != nil && p.ActualTokens != usage.Envelope.Canonical.BillableTotal() || usage.Envelope.Canonical == nil && p.ActualTokens != 0 {
			return biz.ErrSettlementReceiptInvalid
		}
		if usage.SourceKind != e.Intent.SourceKind || usage.UpstreamModelID != e.Intent.Model {
			return biz.ErrSettlementReceiptInvalid
		}
		fd, err := p.Digest()
		if err != nil {
			return biz.ErrSettlementReceiptInvalid
		}
		task := &biz.SettleTask{ReservationID: p.ReservationID, RequestID: e.Intent.RequestID, ActualTokens: p.ActualTokens, Success: p.Success, SubscriptionAccountID: p.SubscriptionAccountID, Usage: usage, Timestamp: time.Unix(e.ObservedAt, 0), SettlementReceiptVersion: 1, SettlementEventID: p.ReservationID + "/final/0", SettlementPayloadDigest: fd}
		local := &Data{db: tx}
		fr, err := (&settlementTaskRepo{data: local}).AcceptTask(ctx, task)
		if err != nil {
			return err
		}
		receipt.SettlementAccepted, receipt.Settled, receipt.Status, receipt.Amount = fr.SettlementAccepted, fr.Settled, fr.Status, fr.CommittedAmount
		return nil
	})
	return receipt, err
}
func mustIntentJSON(i execution.Intent) []byte { b, _ := jsonx.Marshal(i); return b }

func (r *reservationRepo) StoredExecutionIntent(ctx context.Context, id string) (execution.Intent, error) {
	var row executionIntentModel
	if err := r.data.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return execution.Intent{}, err
	}
	var i execution.Intent
	if jsonx.Unmarshal([]byte(row.Payload), &i) != nil {
		return i, biz.ErrSettlementReceiptInvalid
	}
	return i, nil
}
