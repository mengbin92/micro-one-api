package data

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/upstream/contract"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/authzquery"
)

type contractVersionModel struct {
	ID        string `gorm:"primaryKey;size:64"`
	ScopeKey  string `gorm:"size:64;index"`
	Document  string `gorm:"type:text"`
	CreatedAt int64
}

func (contractVersionModel) TableName() string { return "channel_upstream_contract_versions" }

type contractBindingModel struct {
	ApprovedPercent                                      int32
	PreviousPercent                                      int32
	ID                                                   string `gorm:"primaryKey;size:64"`
	SourceKind                                           string
	SourceID                                             int64
	Model                                                string `gorm:"column:upstream_model_id"`
	Protocol                                             string
	Current                                              string `gorm:"column:current_revision"`
	Previous                                             string `gorm:"column:previous_revision"`
	Revision, IncidentRevision                           int64
	Status                                               string
	GrayPercent                                          int32
	StartedAt, CompleteAttempts, Failures, CooldownUntil int64
	SwitchWindow, Switches                               int64
	AutoRevision                                         string
	AutoUntil                                            int64
}

func (contractBindingModel) TableName() string { return "channel_upstream_contract_bindings" }

type contractProbeModel struct {
	ID                                           string `gorm:"primaryKey;size:64"`
	AttemptID                                    string `gorm:"size:128;uniqueIndex"`
	ScopeKey, Revision, SourceRevision           string
	BindingRevision, IncidentRevision, ExpiresAt int64
	ResultDigest                                 string
	Passed                                       bool
	CompletedAt                                  int64
}

func (contractProbeModel) TableName() string { return "channel_upstream_contract_probes" }

type contractResultModel struct {
	ID        string `gorm:"primaryKey;size:64"`
	Digest    string
	CreatedAt int64
}

func (contractResultModel) TableName() string { return "channel_upstream_contract_results" }

type contractAuditModel struct {
	ID                                           string `gorm:"primaryKey;size:64"`
	ScopeKey, Action                             string
	Before                                       string `gorm:"column:before_revision"`
	After                                        string `gorm:"column:after_revision"`
	Reason                                       string
	ActorID                                      int64
	BindingRevision, IncidentRevision, CreatedAt int64
	EvidenceID                                   string
}

func (contractAuditModel) TableName() string { return "channel_upstream_contract_audits" }
func contractBindingDO(m contractBindingModel) biz.ContractBinding {
	return biz.ContractBinding{Scope: contract.Scope{Kind: m.SourceKind, ID: m.SourceID, Model: m.Model, Protocol: m.Protocol}, Current: m.Current, Previous: m.Previous, Revision: m.Revision, IncidentRevision: m.IncidentRevision, Status: m.Status, GrayPercent: m.GrayPercent, StartedAt: m.StartedAt, CompleteAttempts: m.CompleteAttempts, Failures: m.Failures, CooldownUntil: m.CooldownUntil}
}
func (r *Repository) contractDB(ctx context.Context) (*gorm.DB, error) {
	if r.db == nil || !r.db.Migrator().HasTable(&contractBindingModel{}) {
		return nil, biz.ErrContractUnavailable
	}
	return r.db.WithContext(ctx), nil
}
func checkContractAuth(ctx context.Context, op string) error {
	if _, ok := authorization.QueryScopeFromContext(ctx, op); ok {
		return authorization.Require(ctx, op, authorization.ObjectFacts{Context: authorization.Platform()})
	}
	return nil
}
func contractAudit(ctx context.Context, tx *gorm.DB, b contractBindingModel, action, before, reason, evidenceID string, now time.Time) error {
	op := "channel.upstream_contract.publish"
	if action == "create" {
		op = "channel.upstream_contract.create"
	}
	if action == "quarantine" {
		op = "channel.upstream_contract.quarantine"
	}
	actor := int64(0)
	if q, ok := authorization.QueryScopeFromContext(ctx, op); ok {
		actor = q.ActorID
	}
	row := contractAuditModel{ID: uuid.NewString(), ScopeKey: b.ID, Action: action, Before: before, After: b.Current, Reason: reason, ActorID: actor, BindingRevision: b.Revision, IncidentRevision: b.IncidentRevision, CreatedAt: now.Unix(), EvidenceID: evidenceID}
	if err := tx.Create(&row).Error; err != nil {
		return err
	}
	return authzquery.AppendWriteAudit(ctx, tx, op, b.SourceID)
}
func contractVersion(tx *gorm.DB, scope contract.Scope, revision string) (contract.Snapshot, error) {
	var v contractVersionModel
	if tx.Where("id = ? AND scope_key = ?", revision, scope.Key()).First(&v).Error != nil {
		return contract.Snapshot{}, biz.ErrContractInvalid
	}
	var d contract.Document
	if jsonx.Unmarshal([]byte(v.Document), &d) != nil {
		return contract.Snapshot{}, biz.ErrContractInvalid
	}
	s, err := contract.Compile(d)
	if err != nil || s.Revision != revision {
		return s, biz.ErrContractInvalid
	}
	return s, nil
}
func seedBinding(tx *gorm.DB, s contract.Scope) (contractBindingModel, error) {
	seed := contractBindingModel{ID: s.Key(), SourceKind: s.Kind, SourceID: s.ID, Model: s.Model, Protocol: s.Protocol, Status: "candidate"}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return seed, err
	}
	var b contractBindingModel
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", seed.ID).First(&b).Error
	return b, err
}
func (r *Repository) CreateContract(ctx context.Context, c biz.UpstreamContract, now time.Time) error {
	db, err := r.contractDB(ctx)
	if err != nil {
		return err
	}
	snap, err := contract.Compile(c.Document)
	if err != nil || snap.Revision != c.Revision {
		return biz.ErrContractInvalid
	}
	raw, _ := jsonx.Marshal(c.Document)
	return authzquery.RunInTx(ctx, db, 3, func(ctx context.Context, tx *gorm.DB) error {
		if err := checkContractAuth(ctx, "channel.upstream_contract.create"); err != nil {
			return err
		}
		var old contractVersionModel
		if err := tx.Where("id = ?", c.Revision).Limit(1).Find(&old).Error; err != nil {
			return err
		}
		if old.ID != "" {
			if old.Document != string(raw) {
				return biz.ErrContractConflict
			}
			return nil
		}
		if err := tx.Create(&contractVersionModel{ID: c.Revision, ScopeKey: c.Document.Scope.Key(), Document: string(raw), CreatedAt: now.Unix()}).Error; err != nil {
			return err
		}
		b, err := seedBinding(tx, c.Document.Scope)
		if err != nil {
			return err
		}
		return contractAudit(ctx, tx, b, "create", "", authorization.WriteReason(ctx), c.Document.FixtureEvidence, now)
	})
}
func (r *Repository) ListContracts(ctx context.Context, s contract.Scope, limit int) ([]contract.Snapshot, biz.ContractBinding, error) {
	db, err := r.contractDB(ctx)
	if err != nil {
		return nil, biz.ContractBinding{}, err
	}
	if err := checkContractAuth(ctx, "channel.upstream_contract.read"); err != nil {
		return nil, biz.ContractBinding{}, err
	}
	var rows []contractVersionModel
	if err := db.Where("scope_key = ?", s.Key()).Order("created_at DESC, id ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, biz.ContractBinding{}, err
	}
	var b contractBindingModel
	if err := db.Where("id = ?", s.Key()).Limit(1).Find(&b).Error; err != nil {
		return nil, biz.ContractBinding{}, err
	}
	var out []contract.Snapshot
	for _, v := range rows {
		snap, err := contractVersion(db, s, v.ID)
		if err != nil {
			return nil, biz.ContractBinding{}, err
		}
		snap.BindingRevision, snap.IncidentRevision = b.Revision, b.IncidentRevision
		out = append(out, snap)
	}
	return out, contractBindingDO(b), nil
}
func freshProbe(tx *gorm.DB, b contractBindingModel, revision string, now time.Time, source string) bool {
	var n int64
	err := tx.Model(&contractProbeModel{}).Where("scope_key = ? AND revision = ? AND incident_revision = ? AND binding_revision = ? AND source_revision = ? AND passed = ? AND completed_at >= ?", b.ID, revision, b.IncidentRevision, b.Revision, source, true, now.Add(-5*time.Minute).Unix()).Count(&n).Error
	return err == nil && n > 0
}
func syncContractMappings(tx *gorm.DB, b contractBindingModel) error {
	table, idColumn := "model_channel_mapping", "channel_id"
	if b.SourceKind == "subscription" {
		table, idColumn = "model_subscription_mapping", "subscription_account_id"
	}
	var rows []struct {
		ID              int64
		ContractRefs    string
		UpstreamModelID string
		ModelID         string
	}
	if err := tx.Table(table+" AS mapping").Select("mapping.id,mapping.contract_refs,mapping.upstream_model_id,models.model_id").Joins("JOIN models ON models.id = mapping.model_id").Where("mapping."+idColumn+" = ?", b.SourceID).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		exact := row.UpstreamModelID
		if exact == "" {
			exact = row.ModelID
		}
		if exact != b.Model {
			continue
		}
		refs := map[string]string{}
		if row.ContractRefs != "" && jsonx.Unmarshal([]byte(row.ContractRefs), &refs) != nil {
			return biz.ErrContractInvalid
		}
		if refs == nil {
			refs = map[string]string{}
		}
		refs[b.Protocol] = b.Current
		raw, _ := jsonx.Marshal(refs)
		if err := tx.Table(table).Where("id = ?", row.ID).Update("contract_refs", string(raw)).Error; err != nil {
			return err
		}
	}
	return nil
}
func resolveSemanticForContract(tx *gorm.DB, b contractBindingModel, now time.Time) error {
	return tx.Model(&usageSemanticSourceBlockModel{}).Where("source_kind = ? AND source_id = ? AND upstream_model_id = ? AND adapter_protocol = ?", b.SourceKind, b.SourceID, b.Model, b.Protocol).Updates(map[string]any{"status": biz.UsageSemanticBlockStatusResolved, "blocked_until": nil, "consecutive_ambiguous": 0, "updated_at": now, "revision": gorm.Expr("revision + 1")}).Error
}
func (r *Repository) TransitionContract(ctx context.Context, t biz.ContractTransition, now time.Time) (out biz.ContractBinding, err error) {
	db, err := r.contractDB(ctx)
	if err != nil {
		return out, err
	}
	op := "channel.upstream_contract.publish"
	if t.Action == "quarantine" {
		op = "channel.upstream_contract.quarantine"
	}
	err = authzquery.RunInTx(ctx, db, 3, func(ctx context.Context, tx *gorm.DB) error {
		if err := checkContractAuth(ctx, op); err != nil {
			return err
		}
		b, err := seedBinding(tx, t.Scope)
		if err != nil {
			return err
		}
		if b.Revision < 0 || uint64(b.Revision) != t.Expected {
			return biz.ErrContractConflict
		}
		before := b.Current
		if t.Action == "quarantine" {
			b.Status = "quarantined"
			b.IncidentRevision++
			b.CooldownUntil = now.Add(time.Minute).Unix()
			if b.Current != "" {
				snapshot, err := contractVersion(tx, t.Scope, b.Current)
				if err != nil {
					return err
				}
				if snapshot.Document.Repair != nil {
					b.AutoUntil = 0
				}
			}
		} else {
			snapshot, err := contractVersion(tx, t.Scope, t.Revision)
			if err != nil {
				return err
			}
			source, err := r.sourceContractRevision(tx, t.Scope)
			if err != nil {
				return err
			}
			if t.Action == "publish" && snapshot.Document.Repair != nil {
				var keys map[string]string
				if jsonx.Unmarshal([]byte(os.Getenv("UPSTREAM_REPAIR_REVIEW_KEYS")), &keys) != nil || contract.VerifyRepairReview(snapshot.Document, keys, now) != nil {
					return biz.ErrContractGate
				}
				review := snapshot.Document.Repair
				if t.AutomaticUntil > review.ExpiresAt {
					return biz.ErrContractGate
				}
				if t.AutomaticUntil == 0 {
					// The signed review bounds gray authority even when a manual
					// operator omits a stricter coordinator deadline.
					t.AutomaticUntil = review.ExpiresAt
				}
				if t.AutomaticUntil <= now.Unix() {
					return biz.ErrContractGate
				}
				if review.BaseRevision != b.Current || review.BindingRevision != b.Revision || review.IncidentRevision != b.IncidentRevision || review.SourceRevision != source {
					return biz.ErrContractConflict
				}
			}
			if !freshProbe(tx, b, t.Revision, now, source) {
				return biz.ErrContractGate
			}
			switch t.Action {
			case "publish":
				if t.GrayPercent < 1 || t.GrayPercent > 5 {
					return biz.ErrContractGate
				}
				b.AutoRevision = t.Revision
				b.AutoUntil = t.AutomaticUntil
				b.PreviousPercent = b.ApprovedPercent
				b.ApprovedPercent = t.GrayPercent
				b.Previous = b.Current
				b.Current = t.Revision
				b.Status = "gray"
				b.GrayPercent = t.GrayPercent
				b.StartedAt = now.Unix()
				b.CompleteAttempts = 0
				b.Failures = 0
			case "promote":
				if snapshot.Document.Repair != nil && b.AutoUntil > 0 && now.Unix() >= b.AutoUntil {
					return biz.ErrContractGate
				}
				if b.Status != "gray" || t.Revision != b.Current || now.Unix()-b.StartedAt < 1800 || b.CompleteAttempts < 100 || b.Failures != 0 {
					return biz.ErrContractGate
				}
				b.Status = "active"
				b.ApprovedPercent = 100
				b.GrayPercent = 100
			case "rollback":
				if t.Revision != b.Previous || b.Previous == "" {
					return biz.ErrContractGate
				}
				if now.Unix()-b.SwitchWindow >= 3600 {
					b.SwitchWindow = now.Unix()
					b.Switches = 0
				}
				if b.Switches >= 3 {
					return biz.ErrContractGate
				}
				b.Switches++
				b.Current, b.Previous = b.Previous, b.Current
				b.ApprovedPercent, b.PreviousPercent = b.PreviousPercent, b.ApprovedPercent
				if b.ApprovedPercent <= 0 {
					return biz.ErrContractGate
				}
				b.Status = "active"
				if b.ApprovedPercent < 100 {
					b.Status = "gray"
					resetGrayObservation(&b, now)
					b.AutoRevision, b.AutoUntil = b.Current, 0
					if snapshot.Document.Repair != nil {
						if snapshot.Document.Repair.ExpiresAt <= now.Unix() {
							return biz.ErrContractGate
						}
						b.AutoUntil = snapshot.Document.Repair.ExpiresAt
					}
				}
				b.GrayPercent = b.ApprovedPercent
			case "recover":
				if b.Status != "quarantined" || b.Current != t.Revision {
					return biz.ErrContractGate
				}
				b.Status = "active"
				if b.ApprovedPercent < 100 {
					if snapshot.Document.Repair != nil && (b.AutoRevision != b.Current || b.AutoUntil <= now.Unix() || snapshot.Document.Repair.ExpiresAt <= now.Unix()) {
						return biz.ErrContractGate
					}
					b.Status = "gray"
					resetGrayObservation(&b, now)
				}
				b.GrayPercent = b.ApprovedPercent
			default:
				return biz.ErrContractInvalid
			}
			if err := resolveSemanticForContract(tx, b, now); err != nil {
				return err
			}
		}
		b.Revision++
		if err := tx.Save(&b).Error; err != nil {
			return err
		}
		if err := syncContractMappings(tx, b); err != nil {
			return err
		}
		out = contractBindingDO(b)
		return contractAudit(ctx, tx, b, t.Action, before, t.Reason, t.Revision, now)
	})
	return out, err
}
func (r *Repository) ResolveContracts(ctx context.Context, kind string, id int64, model, attempt string) (out []contract.Snapshot, blocked []string, revision string, err error) {
	db, err := r.contractDB(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	var rows []contractBindingModel
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("source_kind = ? AND source_id = ? AND upstream_model_id = ?", kind, id, model).Order("protocol ASC").Find(&rows).Error; err != nil {
			return err
		}
		for i := range rows {
			b := rows[i]
			// P3 gray authority is bounded in durable owner state. Coordinator
			// loss cannot leave a reviewed candidate running past its deadline.
			if b.Status == "gray" && (b.AutoRevision != b.Current || time.Now().Unix() >= b.AutoUntil) {
				// Re-read under the same owner lock used for publication before
				// writing; a stale selector must not overwrite a concurrent CAS.
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", b.ID).First(&b).Error; err != nil {
					return err
				}
				snap, err := contractVersion(tx, contract.Scope{Kind: b.SourceKind, ID: b.SourceID, Model: b.Model, Protocol: b.Protocol}, b.Current)
				if err != nil {
					return err
				}
				if snap.Document.Repair != nil && b.Status == "gray" && (b.AutoRevision != b.Current || time.Now().Unix() >= b.AutoUntil) {
					if err := expireRepairGray(ctx, tx, &b, time.Now()); err != nil {
						return err
					}
				}
				rows[i] = b
			}
			if b.Status == "quarantined" {
				blocked = append(blocked, b.Protocol)
				continue
			}
			target := b.Current
			if b.Status == "gray" {
				if attempt == "" {
					return biz.ErrContractInvalid
				}
				hash := sha256.Sum256([]byte(attempt + b.ID))
				if int32(hash[0])*100/256 >= b.GrayPercent {
					target = b.Previous
					if target == "" {
						blocked = append(blocked, b.Protocol)
						continue
					}
				}
			}
			if target == "" {
				continue
			}
			scope := contract.Scope{Kind: kind, ID: id, Model: model, Protocol: b.Protocol}
			s, err := contractVersion(tx, scope, target)
			if err != nil {
				return err
			}
			s.BindingRevision, s.IncidentRevision = b.Revision, b.IncidentRevision
			out = append(out, s)
		}
		return nil
	})
	var versions []string
	for _, b := range rows {
		versions = append(versions, fmt.Sprintf("%s:%s:%s:%d:%d:%s:%d:%s", b.ID, b.Current, b.Previous, b.Revision, b.IncidentRevision, b.Status, b.GrayPercent, contract.BuildID))
	}
	raw, _ := jsonx.Marshal(versions)
	revision = fmt.Sprintf("%x", sha256.Sum256(raw))
	return
}
func (r *Repository) AcquireContractProbe(ctx context.Context, s contract.Scope, revision, attempt, source string, now time.Time) (out biz.ContractProbeLease, err error) {
	db, err := r.contractDB(ctx)
	if err != nil {
		return out, err
	}
	err = authzquery.RunInTx(ctx, db, 3, func(ctx context.Context, tx *gorm.DB) error {
		snap, err := contractVersion(tx, s, revision)
		if err != nil {
			return err
		}
		b, err := seedBinding(tx, s)
		if err != nil {
			return err
		}
		if b.Status == "quarantined" && b.CooldownUntil > now.Unix() {
			return biz.ErrContractGate
		}
		var pending int64
		if err := tx.Model(&contractProbeModel{}).Where("scope_key = ? AND expires_at > ? AND completed_at = ?", s.Key(), now.Unix(), 0).Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return biz.ErrContractGate
		}
		row := contractProbeModel{ID: uuid.NewString(), AttemptID: attempt, ScopeKey: s.Key(), Revision: revision, SourceRevision: source, BindingRevision: b.Revision, IncidentRevision: b.IncidentRevision, ExpiresAt: now.Add(time.Minute).Unix()}
		if err := tx.Create(&row).Error; err != nil {
			return biz.ErrContractConflict
		}
		snap.BindingRevision, snap.IncidentRevision = b.Revision, b.IncidentRevision
		out = biz.ContractProbeLease{ID: row.ID, Snapshot: snap, SourceRevision: source}
		return nil
	})
	return
}
func (r *Repository) RecordContractResult(ctx context.Context, result biz.ContractResult, now time.Time) (accepted bool, err error) {
	db, err := r.contractDB(ctx)
	if err != nil {
		return false, err
	}
	raw, _ := jsonx.Marshal(result)
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(result.Scope.Key()+result.Revision+result.AttemptID+result.LeaseID)))
	err = authzquery.RunInTx(ctx, db, 3, func(ctx context.Context, tx *gorm.DB) error {
		b, err := seedBinding(tx, result.Scope)
		if err != nil {
			return err
		}
		var old contractResultModel
		if err := tx.Where("id = ?", key).Limit(1).Find(&old).Error; err != nil {
			return err
		}
		if old.ID != "" {
			if old.Digest != digest {
				return biz.ErrContractConflict
			}
			accepted = true
			return nil
		}
		if b.Revision != result.BindingRevision || b.IncidentRevision != result.IncidentRevision {
			return nil
		}
		native := result.Record
		pass := result.CapabilityValid && native.Complete && (native.HTTPStatus == 0 || native.HTTPStatus >= 200 && native.HTTPStatus < 300) && native.Observation.Status == "verified"
		if result.LeaseID != "" {
			var lease contractProbeModel
			if tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", result.LeaseID).First(&lease).Error != nil {
				return biz.ErrContractGate
			}
			if lease.ScopeKey != b.ID || lease.Revision != result.Revision || lease.AttemptID != result.AttemptID || lease.BindingRevision != b.Revision || lease.IncidentRevision != b.IncidentRevision || lease.CompletedAt != 0 || lease.ExpiresAt < now.Unix() {
				return nil
			}
			// Configuration changes revoke old probe authority, including credentials.
			current, err := r.sourceContractRevision(tx, result.Scope)
			if err != nil {
				return err
			}
			if current != lease.SourceRevision {
				return nil
			}
			lease.ResultDigest = digest
			lease.Passed = pass
			lease.CompletedAt = now.Unix()
			if err := tx.Save(&lease).Error; err != nil {
				return err
			}
			if pass && b.Status == "quarantined" && b.Current == result.Revision && b.AutoRevision == result.Revision && now.Unix() < b.AutoUntil {
				if now.Unix()-b.SwitchWindow >= 3600 {
					b.SwitchWindow = now.Unix()
					b.Switches = 0
				}
				if b.Switches < 3 {
					before := b.Current
					b.Switches++
					b.Status = "active"
					if b.ApprovedPercent < 100 {
						b.Status = "gray"
						resetGrayObservation(&b, now)
					}
					b.GrayPercent = b.ApprovedPercent
					b.Revision++
					if err := tx.Save(&b).Error; err != nil {
						return err
					}
					if err := resolveSemanticForContract(tx, b, now); err != nil {
						return err
					}
					if err := contractAudit(ctx, tx, b, "automatic_recover", before, "exact previously approved revision and fresh bounded proof", result.AttemptID, now); err != nil {
						return err
					}
				}
			}
			if !pass && b.Current == result.Revision && (native.Complete || native.HTTPStatus == 400 || native.HTTPStatus == 415 || native.HTTPStatus == 422) {
				before := b.Current
				b.Status = "quarantined"
				b.IncidentRevision++
				b.Revision++
				b.CooldownUntil = now.Add(time.Minute).Unix()
				if err := tx.Save(&b).Error; err != nil {
					return err
				}
				if err := contractAudit(ctx, tx, b, "probe_quarantine", before, "valid bounded contract probe failed", result.AttemptID, now); err != nil {
					return err
				}
			}
		} else {
			if result.Revision != b.Current {
				return nil
			}
			if pass {
				b.CompleteAttempts++
			} else {
				b.Failures++
				if b.Status == "gray" && b.AutoRevision == b.Current && b.AutoUntil > 0 {
					snap, err := contractVersion(tx, result.Scope, b.Current)
					if err != nil {
						return err
					}
					if snap.Document.Repair != nil {
						if err := expireRepairGray(ctx, tx, &b, now); err != nil {
							return err
						}
					}
				}
			}
			if b.Status != "quarantined" && native.Complete && (native.HTTPStatus == 0 || native.HTTPStatus >= 200 && native.HTTPStatus < 300) && native.Observation.Status != "verified" {
				before := b.Current
				b.Status = "quarantined"
				b.IncidentRevision++
				b.Revision++
				b.CooldownUntil = now.Add(time.Minute).Unix()
				if err := contractAudit(ctx, tx, b, "usage_quarantine", before, "native usage integrity redline", result.AttemptID, now); err != nil {
					return err
				}
			}
			if err := tx.Save(&b).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(&contractResultModel{ID: key, Digest: digest, CreatedAt: now.Unix()}).Error; err != nil {
			return err
		}
		accepted = true
		return nil
	})
	return
}

func resetGrayObservation(b *contractBindingModel, now time.Time) {
	b.StartedAt = now.Unix()
	b.CompleteAttempts = 0
	b.Failures = 0
}

func expireRepairGray(ctx context.Context, tx *gorm.DB, b *contractBindingModel, now time.Time) error {
	before := b.Current
	b.Status = "quarantined"
	b.IncidentRevision++
	b.Revision++
	b.CooldownUntil = now.Add(time.Minute).Unix()
	// Revoke automatic recovery of the failed candidate; only an explicit
	// new review or the still-valid previous contract may restore traffic.
	b.AutoUntil = 0
	if err := tx.Save(b).Error; err != nil {
		return err
	}
	return contractAudit(ctx, tx, *b, "repair_gray_stop", before, "reviewed repair gray deadline or failure boundary", b.Current, now)
}
func (r *Repository) sourceContractRevision(tx *gorm.DB, s contract.Scope) (string, error) {
	ctx := context.Background()
	copy := Repository{db: tx, encKey: r.encKey}
	if s.Kind == "channel" {
		c, err := copy.FindByID(ctx, s.ID)
		if err != nil {
			return "", err
		}
		if c == nil {
			return "", biz.ErrContractInvalid
		}
		return c.UpstreamRevision(), nil
	}
	c, err := copy.FindSubscriptionAccountByID(ctx, s.ID)
	if err != nil {
		return "", err
	}
	if c == nil {
		return "", biz.ErrContractInvalid
	}
	return c.UpstreamRevision(), nil
}

// An incident remains quarantined after its cooldown. TTL admits only a
// revision-bound canary; ordinary resolve cannot bypass fresh contract proof.
func contractSemanticResolveAllowed(r *Repository, tx *gorm.DB, s contract.Scope, now time.Time) bool {
	if !tx.Migrator().HasTable(&contractBindingModel{}) {
		return true
	}
	var b contractBindingModel
	err := tx.Where("id = ?", s.Key()).First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true
	}
	if err != nil {
		return false
	}
	if b.Current == "" {
		return true
	}
	source, err := r.sourceContractRevision(tx, s)
	return err == nil && freshProbe(tx, b, b.Current, now, source)
}

var _ biz.UpstreamContractRepo = (*Repository)(nil)

func (r *Repository) RequiresContractRuntime(ctx context.Context, kind string, id int64, model string) (bool, error) {
	if r.db == nil || !r.db.Migrator().HasTable(&contractBindingModel{}) {
		return false, nil
	}
	var n int64
	err := r.db.WithContext(ctx).Model(&contractBindingModel{}).Where("source_kind = ? AND source_id = ? AND upstream_model_id = ? AND current_revision <> ?", kind, id, model, "").Count(&n).Error
	return n > 0, err
}
