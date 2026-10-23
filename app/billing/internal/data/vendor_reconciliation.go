package data

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/execution"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/authzquery"
	"micro-one-api/platform/database/xdb"
)

type vendorReportModel struct {
	ID            string  `gorm:"primaryKey;size:64"`
	RootScopeHash *string `gorm:"size:64;uniqueIndex:uk_vendor_scope_root"`
	ScopeHash     string  `gorm:"size:64;uniqueIndex:uk_vendor_version,priority:1"`
	Version       string  `gorm:"size:128;uniqueIndex:uk_vendor_version,priority:2"`
	Supersedes    *string `gorm:"size:64;uniqueIndex:uk_vendor_correction"`
	Payload       string  `gorm:"type:text"`
	ImportedAt    int64
}

func (vendorReportModel) TableName() string { return "billing_vendor_reports" }

type vendorRunModel struct {
	ID        string `gorm:"primaryKey;size:64"`
	ReportID  string `gorm:"size:64;index"`
	Payload   string `gorm:"type:text"`
	CreatedAt int64
	Sequence  int64
}

func (vendorRunModel) TableName() string { return "billing_vendor_reconciliation_runs" }

type vendorReconciliationRepo struct{ data *Data }

// Audit commands use a dedicated owner DSN and initialize only the shared
// database client. They do not inherit service write credentials or Redis.
func NewVendorReconciliationData(driver, dsn, schema string) (*Data, error) {
	if dsn == "" {
		return nil, fmt.Errorf("vendor reconciliation DSN required")
	}
	db, err := xdb.Open(xdb.DatabaseConfig{Driver: driver, DSN: dsn, Schema: schema})
	if err != nil {
		return nil, err
	}
	return &Data{db: db}, nil
}

func NewVendorReconciliationRepo(d *Data) biz.VendorReconciliationRepo {
	return &vendorReconciliationRepo{d}
}

func (r *vendorReconciliationRepo) LoadVendorAttempts(ctx context.Context, m biz.VendorManifest) ([]biz.VendorLocalAttempt, error) {
	if err := vendorReportAuth(ctx, "billing.vendor_report.import"); err != nil {
		return nil, err
	}
	if err := biz.ValidateVendorManifest(m); err != nil {
		return nil, err
	}
	db := r.data.db.WithContext(ctx)
	var out []biz.VendorLocalAttempt
	// Read every intent for the mapped sources, rather than filtering on inbox
	// received_at. This retains late arrivals and attempts without any response.
	// Bounded pagination fails closed instead of silently truncating coverage.
	sources := map[string]biz.VendorSourceMapping{}
	for _, v := range m.Mappings {
		sources[fmt.Sprintf("%s:%d", v.SourceKind, v.SourceID)] = v
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		var intents []executionIntentModel
		if err := tx.Order("id ASC").Limit(100001).Find(&intents).Error; err != nil {
			return err
		}
		if len(intents) > 100000 {
			return fmt.Errorf("intent scan exceeds limit; archive/partition the trial evidence before reconciliation")
		}
		batch := make([]vendorAttemptReader, 0, vendorIntentBatchSize)
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			if err := loadVendorEventBatch(tx, batch); err != nil {
				return err
			}
			for i := range batch {
				attempt, include, err := batch[i].finish(m)
				if err != nil {
					return err
				}
				if include {
					out = append(out, attempt)
				}
			}
			clear(batch)
			batch = batch[:0]
			return nil
		}
		for _, row := range intents {
			var intent execution.Intent
			if err := jsonx.Unmarshal([]byte(row.Payload), &intent); err != nil {
				return fmt.Errorf("corrupt execution intent %s", row.ID)
			}
			if _, ok := sources[fmt.Sprintf("%s:%d", intent.SourceKind, intent.SourceID)]; !ok {
				continue
			}
			batch = append(batch, newVendorAttemptReader(row, intent))
			if len(batch) == vendorIntentBatchSize {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		if err := flush(); err != nil {
			return err
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return out, err
}

func (r *vendorReconciliationRepo) StoreVendorReconciliation(ctx context.Context, report *biz.VendorReconciliation) error {
	if err := vendorReportAuth(ctx, "billing.vendor_report.import"); err != nil {
		return err
	}
	if report == nil {
		return fmt.Errorf("missing report")
	}
	if err := biz.ValidateVendorManifest(report.Manifest); err != nil {
		return err
	}
	if report.ID != biz.VendorReportID(report.Manifest, report.Rows) {
		return fmt.Errorf("report identity mismatch")
	}
	if err := biz.ValidateVendorRetrospectiveResult(report); err != nil {
		return err
	}
	if report.CostAPIEvidence != nil {
		e, err := r.ValidateCostEvidence(ctx, *report.CostAPIEvidence)
		if err != nil {
			return err
		}
		if !e.CSVMatched || biz.CanonicalVendorScope(e.Query.Scope) != biz.CanonicalVendorScope(report.Manifest.Scope) || report.Manifest.CostBasis != "provider_reported" || report.Manifest.EvidenceID != e.ID() || biz.VendorReportID(report.Manifest, e.Rows) != report.ID {
			return fmt.Errorf("API report identity or basis mismatch")
		}
	}
	return authzquery.RunInTx(ctx, r.data.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		scopeRaw, _ := jsonx.Marshal(biz.CanonicalVendorScope(report.Manifest.Scope))
		scopeHash := vendorHash(scopeRaw)
		var prior vendorReportModel
		if err := tx.Where("scope_hash = ? AND version = ?", scopeHash, report.Manifest.Version).Limit(1).Find(&prior).Error; err != nil {
			return err
		}
		if prior.ID != "" && prior.ID != report.ID {
			return fmt.Errorf("supplier report version already imported with different content")
		}
		if report.Manifest.Supersedes != "" {
			var old vendorReportModel
			if err := tx.Where("id = ?", report.Manifest.Supersedes).First(&old).Error; err != nil {
				return fmt.Errorf("missing correction predecessor")
			}
			if old.ScopeHash != scopeHash || old.ID == report.ID {
				return fmt.Errorf("correction scope mismatch")
			}
			var oldReport biz.VendorReconciliation
			if jsonx.Unmarshal([]byte(old.Payload), &oldReport) != nil {
				return fmt.Errorf("invalid prior report")
			}
			report.CorrectionDelta = vendorCorrection(oldReport.Rows, report.Rows)
		}
		raw, err := jsonx.Marshal(report)
		if err != nil {
			return err
		}
		if prior.ID == "" {
			// Empty predecessor must be nullable so unrelated initial reports
			// do not collide on the correction uniqueness gate.
			row := vendorReportModel{ID: report.ID, ScopeHash: scopeHash, Version: report.Manifest.Version, Payload: string(raw), ImportedAt: time.Now().Unix()}
			if report.Manifest.Supersedes != "" {
				row.Supersedes = &report.Manifest.Supersedes
			} else {
				row.RootScopeHash = &scopeHash
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
				return fmt.Errorf("report import conflict: %w", err)
			}
		}
		// Serialize replays on the immutable report row. Content hashes and
		// second-resolution timestamps do not define evidence arrival order.
		var accepted vendorReportModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", report.ID).Limit(1).Find(&accepted).Error; err != nil {
			return err
		}
		if accepted.ID == "" {
			return fmt.Errorf("supplier version/root/correction conflict: later report versions must supersede the current leaf")
		}
		// Replays with new local evidence append an immutable run, preserving
		// original imports. Identical replays are deduplicated by content SHA.
		run := vendorRunModel{ID: vendorHash(raw), ReportID: report.ID, Payload: string(raw), CreatedAt: time.Now().Unix()}
		if err := vendorReportAuth(ctx, "billing.vendor_report.import"); err != nil {
			return err
		}
		var existing vendorRunModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", run.ID).Limit(1).Find(&existing).Error; err != nil {
			return err
		}
		if existing.ID == "" {
			// A locking read is required on MySQL repeatable-read transactions:
			// an earlier version lookup may have established an older snapshot
			// while this transaction was waiting for the report-row lock.
			var last vendorRunModel
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("report_id = ?", report.ID).Order("sequence DESC").Limit(1).Find(&last).Error; err != nil {
				return err
			}
			run.Sequence = last.Sequence + 1
			if run.Sequence <= 0 {
				return fmt.Errorf("vendor replay sequence exhausted")
			}
			if err := tx.Create(&run).Error; err != nil {
				return err
			}
		}
		if err := authzquery.AppendWriteAudit(ctx, tx, "billing.vendor_report.import", 0); err != nil {
			return err
		}
		if _, ok := authorization.QueryScopeFromContext(ctx, "billing.vendor_report.attribute"); ok {
			return authzquery.AppendWriteAudit(ctx, tx, "billing.vendor_report.attribute", 0)
		}
		return nil
	})
}

func vendorHash(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }

func vendorCorrection(before, after []biz.VendorBillRow) *string {
	total := func(rows []biz.VendorBillRow) (*big.Rat, error) {
		if len(rows) == 0 {
			return nil, errors.New("unknown")
		}
		sum := new(big.Rat)
		for _, r := range rows {
			if r.Amount == nil {
				return nil, errors.New("unknown")
			}
			n, ok := new(big.Rat).SetString(*r.Amount)
			if !ok {
				return nil, errors.New("invalid")
			}
			sum.Add(sum, n)
		}
		return sum, nil
	}
	a, err := total(before)
	if err != nil {
		return nil
	}
	b, err := total(after)
	if err != nil {
		return nil
	}
	s := new(big.Rat).Sub(b, a).FloatString(18)
	return &s
}

func (r *vendorReconciliationRepo) ListVendorReconciliations(ctx context.Context, start, end time.Time, limit int) ([]biz.VendorReconciliation, error) {
	return r.listVendorReports(ctx, start, end, limit, false)
}

func (r *vendorReconciliationRepo) ListVendorMonitoringReports(ctx context.Context, limit int) ([]biz.VendorReconciliation, error) {
	return r.listVendorReports(ctx, time.Time{}, time.Time{}, limit, true)
}

func (r *vendorReconciliationRepo) listVendorReports(ctx context.Context, start, end time.Time, limit int, all bool) ([]biz.VendorReconciliation, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("bounded vendor report limit required")
	}
	if err := vendorReportAuth(ctx, "billing.vendor_report.read"); err != nil {
		return nil, err
	}
	// Latest immutable run for each import; corrected predecessors are excluded
	// from current coverage, but remain in the historical import/run tables.
	var rows []vendorRunModel
	q := r.data.db.WithContext(ctx).Table("billing_vendor_reconciliation_runs AS r").Select("r.*").Joins("JOIN billing_vendor_reports AS p ON p.id = r.report_id").Where("NOT EXISTS (SELECT 1 FROM billing_vendor_reports newer WHERE newer.supersedes = p.id)").Where("NOT EXISTS (SELECT 1 FROM billing_vendor_reconciliation_runs x WHERE x.report_id = r.report_id AND x.sequence > r.sequence)").Order("r.created_at DESC, r.id ASC").Limit(limit + 1)
	if !all {
		q = q.Where("r.created_at >= ? AND r.created_at < ?", start.Unix(), end.Unix())
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) > limit {
		return nil, fmt.Errorf("report export exceeds limit; narrow the fixed window")
	}
	var out []biz.VendorReconciliation
	for _, row := range rows {
		var report biz.VendorReconciliation
		if jsonx.Unmarshal([]byte(row.Payload), &report) != nil {
			return nil, fmt.Errorf("invalid saved vendor report")
		}
		out = append(out, report)
	}
	return out, nil
}
func vendorReportAuth(ctx context.Context, op string) error {
	if _, ok := authorization.QueryScopeFromContext(ctx, op); ok {
		return authorization.Require(ctx, op, authorization.ObjectFacts{Context: authorization.Platform()})
	}
	return nil
}
