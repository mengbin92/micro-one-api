package data

import (
	"context"
	"crypto/sha256"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/domain/execution"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/authzquery"
	"time"
)

type probeCoverageModel struct {
	ID          string `gorm:"primaryKey;size:64"`
	Status      string `gorm:"size:64"`
	ScheduledAt int64
	NextDue     int64
}

func (probeCoverageModel) TableName() string { return "monitor_probe_coverage" }

type probeBudgetModel struct {
	LeaseOwner                                                           string `gorm:"size:128"`
	ID                                                                   string `gorm:"primaryKey;size:128"`
	Calls, InputTokens, OutputTokens, Cost, UnknownCostCalls, LeaseUntil int64
	Paused                                                               bool
}

func (probeBudgetModel) TableName() string { return "monitor_probe_budgets" }

type probeAttemptModel struct {
	PermitClaimed bool
	TargetDigest  string `gorm:"size:64"`
	ID            string `gorm:"primaryKey;size:128"`
	SourceDay     string `gorm:"index;size:128"`
	TargetID      string `gorm:"size:64"`
	Result        string `gorm:"type:text"`
	CreatedAt     int64
}

func (probeAttemptModel) TableName() string { return "monitor_probe_attempts" }

type driftReportModel struct {
	ID                  string `gorm:"primaryKey;size:64"`
	FirstSeen, LastSeen int64
	Occurrences         int64
	Report              string `gorm:"type:text"`
}

func (driftReportModel) TableName() string { return "upstream_drift_reports" }
func (r *Repository) ReserveProbe(ctx context.Context, t execution.ProbeTarget, id string, now time.Time) (ok bool, err error) {
	if t.Expired(now) {
		return false, nil
	}
	if r.db == nil || !r.db.Migrator().HasTable(&probeBudgetModel{}) || !r.db.Migrator().HasTable(&probeAttemptModel{}) || !r.db.Migrator().HasTable(&driftReportModel{}) {
		return false, fmt.Errorf("persistent probe budget/report schema unavailable")
	}
	day := fmt.Sprintf("%s:%d:%s", t.SourceKind, t.SourceID, now.UTC().Format("2006-01-02"))
	cost, unknown := int64(0), int64(1)
	if t.CostUpperBound != nil {
		cost, unknown = *t.CostUpperBound, 0
	}
	err = authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		var paused int64
		if err := tx.Model(&probeBudgetModel{}).Where("id LIKE ? AND paused = ?", fmt.Sprintf("%s:%d:%%", t.SourceKind, t.SourceID), true).Count(&paused).Error; err != nil {
			return err
		}
		if paused > 0 {
			return nil
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&probeBudgetModel{ID: day}).Error; err != nil {
			return err
		}
		var b probeBudgetModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", day).First(&b).Error; err != nil {
			return err
		}
		if b.Paused || b.LeaseUntil > now.Unix() || b.Calls >= t.CallsPerDay || b.InputTokens > t.InputTokensPerDay-128 || b.OutputTokens > t.OutputTokensPerDay-t.MaxOutputTokens || b.Cost > t.CostPerDay-cost || b.UnknownCostCalls > t.UnknownCostCallsPerDay-unknown {
			return nil
		}
		// Reserve the entire trusted bound, including failed calls; unknown exposure has its own counter.
		result := tx.Model(&probeBudgetModel{}).Where("id = ? AND calls = ? AND lease_until = ?", day, b.Calls, b.LeaseUntil).Updates(map[string]any{"calls": b.Calls + 1, "input_tokens": b.InputTokens + 128, "output_tokens": b.OutputTokens + t.MaxOutputTokens, "cost": b.Cost + cost, "unknown_cost_calls": b.UnknownCostCalls + unknown, "lease_until": now.Add(time.Minute).Unix(), "lease_owner": id})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return nil
		}
		if err := tx.Create(&probeAttemptModel{ID: id, SourceDay: day, TargetID: t.ID, TargetDigest: t.Digest(), Result: "{}", CreatedAt: now.Unix()}).Error; err != nil {
			return err
		}
		ok = true
		return nil
	})
	return ok, err
}
func (r *Repository) CompleteProbe(ctx context.Context, t execution.ProbeTarget, result execution.ProbeResult) error {
	if result.TargetID != t.ID || result.AttemptID == "" {
		return fmt.Errorf("probe identity mismatch")
	}
	raw, err := jsonx.Marshal(result)
	if err != nil {
		return err
	}
	if len(raw) > execution.MaxEventBytes {
		return fmt.Errorf("probe result exceeds limit")
	}
	return authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		var attempt probeAttemptModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND target_id = ?", result.AttemptID, t.ID).First(&attempt).Error; err != nil {
			return err
		}
		if attempt.TargetDigest != t.Digest() {
			return fmt.Errorf("probe target changed after reservation")
		}
		if attempt.Result != "{}" {
			if attempt.Result != string(raw) {
				return fmt.Errorf("probe result conflict")
			}
			return nil
		}
		var record evidence.Record
		if len(result.RecordJSON) > 0 && jsonx.Unmarshal(result.RecordJSON, &record) != nil {
			return fmt.Errorf("invalid native probe result")
		}
		observations := []evidence.Observation{record.Observation}
		if record.ContractProof != nil {
			observations = append(observations, record.ContractProof.Observation)
		}
		overrun := false
		for _, observation := range observations {
			b := observation.Buckets
			if b == nil {
				continue
			}
			// Either independently observed bucket set can revoke a breached
			// bound. This does not verify a proof or derive any supplier cost.
			overrun = overrun || b[4] < 0 || b[4] > t.MaxOutputTokens
			input := int64(0)
			for _, amount := range b[:4] {
				// A corrupt or overflow-sized bucket cannot wrap the total
				// back below the reserved input bound.
				if amount < 0 || amount > 128-input {
					overrun = true
					break
				}
				input += amount
			}
		}
		if overrun {
			// Revocation applies to the source even when this result is
			// late; releasing a lease is a separate owner-bound action.
			if err := tx.Model(&probeBudgetModel{}).Where("id = ?", attempt.SourceDay).Update("paused", true).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&probeBudgetModel{}).Where("id = ? AND lease_owner = ?", attempt.SourceDay, result.AttemptID).Update("lease_until", int64(0)).Error; err != nil {
			return err
		}
		if err := tx.Model(&attempt).Update("result", string(raw)).Error; err != nil {
			return err
		}
		if !result.ValidContractProbe || result.Outcome != "contract_drift" && result.Outcome != "contract_probe_rejected" {
			return nil
		}
		fingerprint := fmt.Sprintf("%s:%d:%s:%s:%s:%v", t.SourceKind, t.SourceID, t.Model, t.Protocol, record.Observation.ContractRevision, record.Observation.Fingerprint)
		id := fmt.Sprintf("%x", sha256.Sum256([]byte(fingerprint)))
		var real struct {
			Record *evidence.Record `json:"record"`
		}
		if len(result.RealTraffic) > 0 && jsonx.Unmarshal(result.RealTraffic, &real) != nil {
			return fmt.Errorf("invalid real-traffic comparison")
		}
		before := []string{}
		if real.Record != nil {
			before = real.Record.Observation.Fingerprint
		}
		fields := execution.CompareFields(before, record.Observation.Fingerprint)
		report := map[string]any{"field_diff": fields, "real_traffic_comparison": result.RealTraffic, "incident_id": id, "source_kind": t.SourceKind, "source_id": t.SourceID, "upstream_model_id": t.Model, "protocol": t.Protocol, "parser_revision": record.Observation.ParserRevision, "contract_revision": record.Observation.ContractRevision, "fingerprint": record.Observation.Fingerprint, "present_paths": record.Observation.Present, "reason": record.Observation.Reason, "evidence_attempt_id": result.AttemptID, "sample": record.Observation.Usage, "valid_contract_probe": true, "traffic_destination": "existing_authorized_routes", "executed_rules": []string{}, "rollback_actions": []string{}, "affected_billing_scope": "internal_probe_only", "canary_cost_basis": "unknown", "reserved_cost_upper_bound": t.CostUpperBound, "outcome": "report_only", "probe_outcome": result.Outcome, "required_capability": t.Capability}
		payload, _ := jsonx.Marshal(report)
		seed := driftReportModel{ID: id, FirstSeen: result.ObservedAt, LastSeen: result.ObservedAt, Occurrences: 1, Report: string(payload)}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.Assignments(map[string]any{"last_seen": result.ObservedAt, "occurrences": gorm.Expr("upstream_drift_reports.occurrences + 1"), "report": string(payload)})}).Create(&seed).Error
	})
}

func (r *Repository) RecordProbeCoverage(ctx context.Context, id, status string, at, next int64) error {
	if r.db == nil {
		return fmt.Errorf("coverage persistence unavailable")
	}
	row := probeCoverageModel{ID: id, Status: status, ScheduledAt: at, NextDue: next}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"status", "scheduled_at", "next_due"})}).Create(&row).Error
}

func (r *Repository) ProbeStorageReady(ctx context.Context) bool {
	return r.db != nil && r.db.WithContext(ctx).Migrator().HasTable(&probeBudgetModel{}) && r.db.Migrator().HasTable(&probeAttemptModel{}) && r.db.Migrator().HasTable(&driftReportModel{}) && r.db.Migrator().HasTable(&probeCoverageModel{}) && r.db.Migrator().HasColumn(&probeBudgetModel{}, "LeaseOwner") && r.db.Migrator().HasColumn(&probeAttemptModel{}, "PermitClaimed")
}
func (r *Repository) ExportUpstreamReport(ctx context.Context) (map[string]any, error) {
	if !r.ProbeStorageReady(ctx) {
		return nil, fmt.Errorf("persistent P1 report schema unavailable")
	}
	var incidents []driftReportModel
	var coverage []probeCoverageModel
	var budgets []probeBudgetModel
	if err := r.db.WithContext(ctx).Order("last_seen DESC").Limit(1000).Find(&incidents).Error; err != nil {
		return nil, err
	}
	if err := r.db.WithContext(ctx).Order("id ASC").Limit(1000).Find(&coverage).Error; err != nil {
		return nil, err
	}
	if err := r.db.WithContext(ctx).Order("id DESC").Limit(1000).Find(&budgets).Error; err != nil {
		return nil, err
	}
	reports := make([]map[string]any, 0, len(incidents))
	for _, row := range incidents {
		reports = append(reports, map[string]any{"incident_id": row.ID, "first_seen": row.FirstSeen, "last_seen": row.LastSeen, "occurrences": row.Occurrences, "report": jsonx.RawMessage(row.Report)})
	}
	return map[string]any{"exported_at": time.Now().Unix(), "limit_per_section": 1000, "incidents": reports, "coverage": coverage, "reserved_budgets": budgets, "verification_scope": "none", "mode": "P1_report_only"}, nil
}

func (r *Repository) ClaimProbePermit(ctx context.Context, target, attempt, digest string) (bool, error) {
	if r.db == nil {
		return false, fmt.Errorf("persistent budget permit unavailable")
	}
	result := r.db.WithContext(ctx).Model(&probeAttemptModel{}).Where("id = ? AND target_id = ? AND target_digest = ? AND result = ? AND permit_claimed = ?", attempt, target, digest, "{}", false).Where("EXISTS (SELECT 1 FROM monitor_probe_budgets b WHERE b.id = monitor_probe_attempts.source_day AND b.lease_owner = ? AND b.lease_until >= ? AND b.paused = ?)", attempt, time.Now().Add(35*time.Second).Unix(), false).Update("permit_claimed", true)
	return result.RowsAffected == 1, result.Error
}
