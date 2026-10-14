package data

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/upstream/contract"
	"micro-one-api/pkg/jsonx"
	dbt "micro-one-api/platform/database/testutil"
)

func TestRepairPublishRequiresIndependentSignatureAndCurrentIncident(t *testing.T) {
	db := dbt.RoutingContextDB(t, "sqlite")
	r := &Repository{db: db}
	ctx := context.Background()
	now := time.Now().UTC()
	empty := ""
	require.NoError(t, db.Create(&channelModel{ModelMapping: &empty, SystemPrompt: &empty, Config: "{}", ID: 1, Name: "fixture", Type: 1, Status: 1, Key: "fixture-only", Models: "vendor/Exact"}).Error)
	d := contractDoc("channel", 1)
	source, err := r.sourceContractRevision(db, d.Scope)
	require.NoError(t, err)
	d.Repair = &contract.RepairReview{ProposalID: strings.Repeat("a", 64), SourceRevision: source, ReplayID: strings.Repeat("b", 64), CIEvidenceID: "independent-ci", Reviewer: "human", KeyID: "reviewer", ExpiresAt: now.Add(time.Hour).Unix()}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	keyJSON, _ := jsonx.Marshal(map[string]string{"reviewer": base64.StdEncoding.EncodeToString(pub)})
	t.Setenv("UPSTREAM_REPAIR_REVIEW_KEYS", string(keyJSON))
	storeAndProbe := func(d contract.Document, id string) {
		require.NoError(t, r.CreateContract(ctx, biz.UpstreamContract{Document: d, Revision: d.Revision()}, now))
		lease, err := r.AcquireContractProbe(ctx, d.Scope, d.Revision(), id, source, now)
		require.NoError(t, err)
		ok, err := r.RecordContractResult(ctx, biz.ContractResult{Scope: d.Scope, Revision: d.Revision(), BindingRevision: lease.Snapshot.BindingRevision, IncidentRevision: lease.Snapshot.IncidentRevision, AttemptID: id, LeaseID: lease.ID, Record: verifiedContractRecord(), CapabilityValid: true}, now)
		require.NoError(t, err)
		require.True(t, ok)
	}
	storeAndProbe(d, "unsigned-probe")
	transition := func(d contract.Document) (biz.ContractBinding, error) {
		return r.TransitionContract(ctx, biz.ContractTransition{Scope: d.Scope, Revision: d.Revision(), Action: "publish", GrayPercent: 5, Reason: "reviewed"}, now)
	}
	_, err = transition(d)
	require.ErrorIs(t, err, biz.ErrContractGate)
	stale := d
	review := *d.Repair
	stale.Repair = &review
	stale.Repair.IncidentRevision = 1
	stale, err = contract.SignRepairReview(stale, private)
	require.NoError(t, err)
	storeAndProbe(stale, "stale-incident-probe")
	_, err = transition(stale)
	require.ErrorIs(t, err, biz.ErrContractConflict)
	d, err = contract.SignRepairReview(d, private)
	require.NoError(t, err)
	storeAndProbe(d, "reviewed-probe")
	b, err := transition(d)
	require.NoError(t, err)
	require.Equal(t, "gray", b.Status)
	require.EqualValues(t, 5, b.GrayPercent)
	// A failed/stale repair did not modify the binding, usage gate or mappings.
	require.EqualValues(t, 1, b.Revision)
	var durable contractBindingModel
	require.NoError(t, db.Where("id = ?", d.Scope.Key()).First(&durable).Error)
	require.Equal(t, d.Repair.ExpiresAt, durable.AutoUntil, "omitted operator deadline must still enforce the signed review expiry")
	// Durable deadline is enforced during selection even if the coordinator
	// crashed. It revokes recovery and leaves old in-flight snapshots intact.
	require.NoError(t, db.Model(&contractBindingModel{}).Where("id = ?", d.Scope.Key()).Updates(map[string]any{"auto_revision": d.Revision(), "auto_until": time.Now().Add(-time.Second).Unix()}).Error)
	require.NoError(t, db.Model(&contractBindingModel{}).Where("id = ?", d.Scope.Key()).Updates(map[string]any{"started_at": now.Add(-31 * time.Minute).Unix(), "complete_attempts": 100}).Error)
	freshSource, err := r.sourceContractRevision(db, d.Scope)
	require.NoError(t, err)
	lease, err := r.AcquireContractProbe(ctx, d.Scope, d.Revision(), "expired-gray-proof", freshSource, now)
	require.NoError(t, err)
	ok, err := r.RecordContractResult(ctx, biz.ContractResult{Scope: d.Scope, Revision: d.Revision(), BindingRevision: lease.Snapshot.BindingRevision, IncidentRevision: lease.Snapshot.IncidentRevision, AttemptID: "expired-gray-proof", LeaseID: lease.ID, Record: verifiedContractRecord(), CapabilityValid: true}, now)
	require.NoError(t, err)
	require.True(t, ok)
	_, err = r.TransitionContract(ctx, biz.ContractTransition{Scope: d.Scope, Revision: d.Revision(), Expected: uint64(b.Revision), Action: "promote", Reason: "deadline reached despite valid fresh proof"}, now)
	require.ErrorIs(t, err, biz.ErrContractGate)
	_, blocked, revision, err := r.ResolveContracts(ctx, d.Scope.Kind, d.Scope.ID, d.Scope.Model, "new-attempt")
	require.NoError(t, err)
	require.Equal(t, []string{"openai_chat"}, blocked)
	_, stopped, err := r.ListContracts(ctx, d.Scope, 20)
	require.NoError(t, err)
	require.Equal(t, "quarantined", stopped.Status)
	require.EqualValues(t, 2, stopped.Revision)
	_, blocked2, revision2, err := r.ResolveContracts(ctx, d.Scope.Kind, d.Scope.ID, d.Scope.Model, "next-attempt")
	require.NoError(t, err)
	require.Equal(t, blocked, blocked2)
	require.Equal(t, revision, revision2, "selection fingerprint uses the stopped durable revision")
	// A fresh valid probe does not renew a revoked candidate's gray approval.
	at := now.Add(2 * time.Minute)
	lease, err = r.AcquireContractProbe(ctx, d.Scope, d.Revision(), "revoked-gray-proof", freshSource, at)
	require.NoError(t, err)
	_, err = r.RecordContractResult(ctx, biz.ContractResult{Scope: d.Scope, Revision: d.Revision(), BindingRevision: lease.Snapshot.BindingRevision, IncidentRevision: lease.Snapshot.IncidentRevision, AttemptID: "revoked-gray-proof", LeaseID: lease.ID, Record: verifiedContractRecord(), CapabilityValid: true}, at)
	require.NoError(t, err)
	_, err = r.TransitionContract(ctx, biz.ContractTransition{Scope: d.Scope, Revision: d.Revision(), Expected: uint64(stopped.Revision), Action: "recover", Reason: "fresh proof with revoked approval"}, at)
	require.ErrorIs(t, err, biz.ErrContractGate)
}

func TestRepairPublicationRejectsExpiredAutomaticDeadline(t *testing.T) {
	db := dbt.RoutingContextDB(t, "sqlite")
	r := &Repository{db: db}
	ctx := context.Background()
	now := time.Now().UTC()
	empty := ""
	require.NoError(t, db.Create(&channelModel{ModelMapping: &empty, SystemPrompt: &empty, Config: "{}", ID: 1, Status: 1, Key: "fixture-only"}).Error)
	d := contractDoc("channel", 1)
	source, err := r.sourceContractRevision(db, d.Scope)
	require.NoError(t, err)
	d.Repair = &contract.RepairReview{ProposalID: strings.Repeat("a", 64), SourceRevision: source, ReplayID: strings.Repeat("b", 64), CIEvidenceID: "independent-ci", Reviewer: "human", KeyID: "reviewer", ExpiresAt: now.Add(time.Hour).Unix()}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	keyJSON, _ := jsonx.Marshal(map[string]string{"reviewer": base64.StdEncoding.EncodeToString(pub)})
	t.Setenv("UPSTREAM_REPAIR_REVIEW_KEYS", string(keyJSON))
	d, err = contract.SignRepairReview(d, private)
	require.NoError(t, err)
	require.NoError(t, r.CreateContract(ctx, biz.UpstreamContract{Document: d, Revision: d.Revision()}, now))
	lease, err := r.AcquireContractProbe(ctx, d.Scope, d.Revision(), "reviewed-probe", source, now)
	require.NoError(t, err)
	_, err = r.RecordContractResult(ctx, biz.ContractResult{Scope: d.Scope, Revision: d.Revision(), AttemptID: "reviewed-probe", LeaseID: lease.ID, Record: verifiedContractRecord(), CapabilityValid: true}, now)
	require.NoError(t, err)
	_, err = r.TransitionContract(ctx, biz.ContractTransition{Scope: d.Scope, Revision: d.Revision(), Action: "publish", Reason: "reviewed", GrayPercent: 1, AutomaticUntil: now.Add(-time.Second).Unix()}, now)
	require.ErrorIs(t, err, biz.ErrContractGate, "a deadline already passed must not enter gray")
}

func TestFailedRepairGrayRevokesRecoveryAndDoesNotDoubleCountIncident(t *testing.T) {
	db := dbt.RoutingContextDB(t, "sqlite")
	r := &Repository{db: db}
	ctx := context.Background()
	now := time.Now().UTC()
	d := contractDoc("channel", 1)
	d.Repair = &contract.RepairReview{ProposalID: strings.Repeat("a", 64)}
	require.NoError(t, r.CreateContract(ctx, biz.UpstreamContract{Document: d, Revision: d.Revision()}, now))
	require.NoError(t, db.Model(&contractBindingModel{}).Where("id = ?", d.Scope.Key()).Updates(map[string]any{"current_revision": d.Revision(), "status": "gray", "gray_percent": 1, "approved_percent": 1, "auto_revision": d.Revision(), "auto_until": now.Add(time.Hour).Unix()}).Error)
	record := verifiedContractRecord()
	record.Observation.Status = "unsupported"
	ok, err := r.RecordContractResult(ctx, biz.ContractResult{Scope: d.Scope, Revision: d.Revision(), AttemptID: "failed-gray-attempt", Record: record, CapabilityValid: true}, now)
	require.NoError(t, err)
	require.True(t, ok)
	var binding contractBindingModel
	require.NoError(t, db.Where("id = ?", d.Scope.Key()).First(&binding).Error)
	require.Equal(t, "quarantined", binding.Status)
	require.EqualValues(t, 1, binding.IncidentRevision)
	require.Zero(t, binding.AutoUntil)
	require.EqualValues(t, 1, binding.Failures)
}
