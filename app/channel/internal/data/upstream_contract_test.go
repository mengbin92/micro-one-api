package data

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	authzt "micro-one-api/domain/authorization/testutil"
	"micro-one-api/domain/upstream/contract"
	"micro-one-api/domain/upstream/evidence"
	dbt "micro-one-api/platform/database/testutil"
	"testing"
	"time"
)

func contractDoc(kind string, id int64) contract.Document {
	return contract.Document{Scope: contract.Scope{Kind: kind, ID: id, Model: "vendor/Exact", Protocol: "openai_chat"}, Provider: "fixture", AdapterBuild: contract.BuildID, ParserRevision: contract.ParserID, UsageRevision: contract.UsageID, Inbound: []string{"openai_chat"}, FixtureEvidence: "independent-matrix", BasisRevision: "official-schema-v1"}
}
func verifiedContractRecord() *evidence.Record {
	return &evidence.Record{Complete: true, HTTPStatus: 200, Observation: evidence.Parse([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`), "openai_chat")}
}
func TestContractCASGrayIncidentAndRollbackDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbt.RoutingContextDB(t, driver)
			r := &Repository{db: db}
			ctx := authorization.WithWriteReason(context.Background(), "reviewed fixture")
			now := time.Now().UTC()
			empty := ""
			require.NoError(t, db.Create(&channelModel{ModelMapping: &empty, SystemPrompt: &empty, Config: "{}", ID: 1, Name: "fixture", Type: 1, Status: 1, Key: "fixture-only", Models: "vendor/Exact"}).Error)
			d := contractDoc("channel", 1)
			rev := d.Revision()
			require.NoError(t, r.CreateContract(ctx, biz.UpstreamContract{Document: d, Revision: rev}, now))
			rows, b, err := r.ListContracts(ctx, d.Scope, 20)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Zero(t, b.Revision)
			_, err = r.TransitionContract(ctx, biz.ContractTransition{Scope: d.Scope, Revision: rev, Action: "publish", Reason: "no proof", GrayPercent: 5}, now)
			require.ErrorIs(t, err, biz.ErrContractGate)
			source, err := r.sourceContractRevision(db, d.Scope)
			require.NoError(t, err)
			probe := func(revision string, at time.Time, record *evidence.Record) (bool, error) {
				lease, err := r.AcquireContractProbe(ctx, d.Scope, revision, fmt.Sprint(at.UnixNano()), source, at)
				if err != nil {
					return false, err
				}
				return r.RecordContractResult(ctx, biz.ContractResult{Scope: d.Scope, Revision: revision, BindingRevision: lease.Snapshot.BindingRevision, IncidentRevision: lease.Snapshot.IncidentRevision, AttemptID: fmt.Sprint(at.UnixNano()), LeaseID: lease.ID, Record: record, CapabilityValid: true}, at)
			}
			ok, err := probe(rev, now, verifiedContractRecord())
			require.NoError(t, err)
			require.True(t, ok)
			b, err = r.TransitionContract(ctx, biz.ContractTransition{Scope: d.Scope, Revision: rev, Action: "publish", Reason: "proof passed", GrayPercent: 5, AutomaticUntil: now.Add(time.Hour).Unix()}, now)
			require.NoError(t, err)
			require.Equal(t, "gray", b.Status)
			_, err = r.TransitionContract(ctx, biz.ContractTransition{Scope: d.Scope, Revision: rev, Action: "publish", Reason: "stale", GrayPercent: 5}, now)
			require.ErrorIs(t, err, biz.ErrContractConflict)
			for n := 0; n < 100; n++ {
				ok, err = r.RecordContractResult(ctx, biz.ContractResult{Scope: d.Scope, Revision: rev, BindingRevision: b.Revision, IncidentRevision: b.IncidentRevision, AttemptID: fmt.Sprint("user-", n), Record: verifiedContractRecord(), CapabilityValid: true}, now.Add(time.Minute))
				require.NoError(t, err)
				require.True(t, ok)
			}
			_, err = r.TransitionContract(ctx, biz.ContractTransition{Scope: d.Scope, Revision: rev, Expected: uint64(b.Revision), Action: "promote", Reason: "too early"}, now.Add(time.Minute))
			require.ErrorIs(t, err, biz.ErrContractGate)
			later := now.Add(31 * time.Minute)
			ok, err = probe(rev, later, verifiedContractRecord())
			require.NoError(t, err)
			require.True(t, ok)
			b, err = r.TransitionContract(ctx, biz.ContractTransition{Scope: d.Scope, Revision: rev, Expected: uint64(b.Revision), Action: "promote", Reason: "100 complete attempts and fixed window"}, later)
			require.NoError(t, err)
			require.Equal(t, "active", b.Status)
			oldRevision := b.Revision
			b, err = r.TransitionContract(ctx, biz.ContractTransition{Scope: d.Scope, Expected: uint64(b.Revision), Action: "quarantine", Reason: "incident"}, later)
			require.NoError(t, err)
			_, blocked, _, err := r.ResolveContracts(ctx, "channel", 1, d.Scope.Model, "request")
			require.NoError(t, err)
			require.Equal(t, []string{"openai_chat"}, blocked)
			ok, err = r.RecordContractResult(ctx, biz.ContractResult{Scope: d.Scope, Revision: rev, BindingRevision: oldRevision, AttemptID: "old-verified", Record: verifiedContractRecord(), CapabilityValid: true}, later.Add(10*time.Minute))
			require.NoError(t, err)
			require.False(t, ok)
			_, err = r.TransitionContract(ctx, biz.ContractTransition{Scope: d.Scope, Revision: rev, Expected: uint64(b.Revision), Action: "recover", Reason: "TTL expired only"}, later.Add(10*time.Minute))
			require.ErrorIs(t, err, biz.ErrContractGate)
			ok, err = probe(rev, later.Add(2*time.Minute), verifiedContractRecord())
			require.NoError(t, err)
			require.True(t, ok)
			_, b, err = r.ListContracts(ctx, d.Scope, 20)
			require.NoError(t, err)
			require.Equal(t, "active", b.Status, "only exact previously approved recovery may be automatic")
			// Failed candidate HTTP 200 with missing usage cannot produce a publish proof.
			newer := d
			newer.BasisRevision = "schema-v2"
			rev2 := newer.Revision()
			require.NoError(t, r.CreateContract(ctx, biz.UpstreamContract{Document: newer, Revision: rev2}, later))
			bad := &evidence.Record{Complete: true, HTTPStatus: 200, Observation: evidence.Parse([]byte(`{"ok":true}`), "openai_chat")}
			ok, err = probe(rev2, later.Add(3*time.Minute), bad)
			require.NoError(t, err)
			require.True(t, ok)
			_, b, err = r.ListContracts(ctx, d.Scope, 20)
			require.NoError(t, err)
			require.Equal(t, "quarantined", b.Status)
			// Same DB and new repository cannot bypass persistent incident by waiting.
			restarted := &Repository{db: db}
			_, blocked, _, err = restarted.ResolveContracts(ctx, "channel", 1, d.Scope.Model, "new-request")
			require.NoError(t, err)
			require.Len(t, blocked, 1)
		})
	}
}
func TestContractPermissionsAndPreciseSubscriptionNamespace(t *testing.T) {
	db := dbt.RoutingContextDB(t, "sqlite")
	r := &Repository{db: db}
	d := contractDoc("subscription", 1)
	ctx := authorization.WithQueryScope(context.Background(), "channel.upstream_contract.create", authzt.Resources(1))
	require.Error(t, r.CreateContract(ctx, biz.UpstreamContract{Document: d, Revision: d.Revision()}, time.Now()), "resource grants cannot widen a platform-only publication permission")
}

func TestProtocolIncidentTTLAndMappingReferenceArePersistent(t *testing.T) {
	db := dbt.RoutingContextDB(t, "sqlite")
	r := &Repository{db: db}
	ctx := authorization.WithWriteReason(context.Background(), "independent fixture")
	now := time.Now()
	empty := ""
	require.NoError(t, db.Create(&channelModel{ID: 1, Status: 1, Models: "vendor/Exact", Key: "fixture", ModelMapping: &empty, SystemPrompt: &empty, Config: "{}"}).Error)
	require.NoError(t, db.Create(&modelModel{ID: 1, ModelID: "public", Status: 1, Capabilities: "[]", InputModalities: "[]", OutputModalities: "[]", Tags: "[]"}).Error)
	require.NoError(t, db.Create(&modelChannelMappingModel{ID: 1, ChannelID: 1, ModelPK: 1, Enabled: true, UpstreamModelID: "vendor/Exact"}).Error)
	d := contractDoc("channel", 1)
	require.NoError(t, r.CreateContract(ctx, biz.UpstreamContract{Document: d, Revision: d.Revision()}, now))
	source, err := r.sourceContractRevision(db, d.Scope)
	require.NoError(t, err)
	lease, err := r.AcquireContractProbe(ctx, d.Scope, d.Revision(), "proof", source, now)
	require.NoError(t, err)
	_, err = r.RecordContractResult(ctx, biz.ContractResult{Scope: d.Scope, Revision: d.Revision(), AttemptID: "proof", LeaseID: lease.ID, Record: verifiedContractRecord(), CapabilityValid: true}, now)
	require.NoError(t, err)
	b, err := r.TransitionContract(ctx, biz.ContractTransition{Scope: d.Scope, Revision: d.Revision(), Action: "publish", Reason: "validated", GrayPercent: 5}, now)
	require.NoError(t, err)
	var mapping modelChannelMappingModel
	require.NoError(t, db.First(&mapping).Error)
	require.Contains(t, mapping.ContractRefs, d.Revision())
	_, err = r.UpsertUsageSemanticVerdict(ctx, biz.UsageSemanticVerdict{SourceKind: "channel", SourceID: 1, UpstreamModelID: d.Scope.Model, AdapterProtocol: d.Scope.Protocol, ParseStatus: "invalid"}, time.Minute, time.Minute, 1, now)
	require.NoError(t, err)
	_, err = r.UpsertUsageSemanticVerdict(ctx, biz.UsageSemanticVerdict{SourceKind: "channel", SourceID: 1, UpstreamModelID: d.Scope.Model, AdapterProtocol: d.Scope.Protocol, ParseStatus: "verified"}, time.Minute, time.Minute, 1, now.Add(2*time.Minute))
	require.NoError(t, err)
	blocks, err := r.ListBlockedUsageSemanticBlocks(ctx, now.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	ok, err := r.ResolveUsageSemanticBlock(ctx, "channel", 1, d.Scope.Model, d.Scope.Protocol, now.Add(time.Hour))
	require.ErrorIs(t, err, biz.ErrContractGate)
	require.False(t, ok)
	require.Positive(t, b.Revision)
}

func TestRollbackRequiresFreshProofAndRemovedOldProtocolStaysQuarantined(t *testing.T) {
	db := dbt.RoutingContextDB(t, "sqlite")
	r := &Repository{db: db}
	ctx := authorization.WithWriteReason(context.Background(), "reviewed rollback")
	now := time.Now()
	empty := ""
	require.NoError(t, db.Create(&channelModel{ID: 1, Status: 1, Key: "fixture", ModelMapping: &empty, SystemPrompt: &empty, Config: "{}"}).Error)
	old := contractDoc("channel", 1)
	current := old
	current.BasisRevision = "v2"
	for _, d := range []contract.Document{old, current} {
		require.NoError(t, r.CreateContract(ctx, biz.UpstreamContract{Document: d, Revision: d.Revision()}, now))
	}
	var binding contractBindingModel
	require.NoError(t, db.Where("id = ?", old.Scope.Key()).First(&binding).Error)
	binding.Current = current.Revision()
	binding.Previous = old.Revision()
	binding.Status = "active"
	binding.ApprovedPercent = 100
	binding.PreviousPercent = 100
	binding.Revision = 7
	binding.IncidentRevision = 2
	require.NoError(t, db.Save(&binding).Error)
	_, err := r.TransitionContract(ctx, biz.ContractTransition{Scope: old.Scope, Revision: old.Revision(), Expected: 7, Action: "rollback", Reason: "unvalidated rollback"}, now)
	require.ErrorIs(t, err, biz.ErrContractGate)
	source, err := r.sourceContractRevision(db, old.Scope)
	require.NoError(t, err)
	lease, err := r.AcquireContractProbe(ctx, old.Scope, old.Revision(), "old-removed", source, now)
	require.NoError(t, err)
	record := &evidence.Record{HTTPStatus: 400, Complete: true, Observation: evidence.Parse([]byte(`{}`), "openai_chat")}
	_, err = r.RecordContractResult(ctx, biz.ContractResult{Scope: old.Scope, Revision: old.Revision(), BindingRevision: 7, IncidentRevision: 2, AttemptID: "old-removed", LeaseID: lease.ID, Record: record, CapabilityValid: false}, now)
	require.NoError(t, err)
	_, b, err := r.ListContracts(ctx, old.Scope, 20)
	require.NoError(t, err)
	require.Equal(t, "quarantined", b.Status)
	_, err = r.TransitionContract(ctx, biz.ContractTransition{Scope: old.Scope, Revision: old.Revision(), Expected: uint64(b.Revision), Action: "rollback", Reason: "old endpoint removed"}, now.Add(time.Hour))
	require.ErrorIs(t, err, biz.ErrContractGate)
	at := now.Add(2 * time.Minute)
	lease, err = r.AcquireContractProbe(ctx, old.Scope, old.Revision(), "old-fixed", source, at)
	require.NoError(t, err)
	_, err = r.RecordContractResult(ctx, biz.ContractResult{Scope: old.Scope, Revision: old.Revision(), BindingRevision: b.Revision, IncidentRevision: b.IncidentRevision, AttemptID: "old-fixed", LeaseID: lease.ID, Record: verifiedContractRecord(), CapabilityValid: true}, at)
	require.NoError(t, err)
	b, err = r.TransitionContract(ctx, biz.ContractTransition{Scope: old.Scope, Revision: old.Revision(), Expected: uint64(b.Revision), Action: "rollback", Reason: "fresh old contract proof"}, at)
	require.NoError(t, err)
	require.Equal(t, old.Revision(), b.Current)
	require.Equal(t, "active", b.Status)
}

func TestAutomaticRecoveryNeverPromotesUnreleasedGray(t *testing.T) {
	db := dbt.RoutingContextDB(t, "sqlite")
	r := &Repository{db: db}
	ctx := context.Background()
	now := time.Now()
	empty := ""
	require.NoError(t, db.Create(&channelModel{ID: 1, Status: 1, Key: "fixture", ModelMapping: &empty, SystemPrompt: &empty, Config: "{}"}).Error)
	d := contractDoc("channel", 1)
	require.NoError(t, r.CreateContract(ctx, biz.UpstreamContract{Document: d, Revision: d.Revision()}, now))
	var b contractBindingModel
	require.NoError(t, db.Where("id = ?", d.Scope.Key()).First(&b).Error)
	b.Current = d.Revision()
	b.Status = "quarantined"
	b.ApprovedPercent = 5
	b.GrayPercent = 5
	b.Revision = 3
	b.IncidentRevision = 1
	b.AutoRevision = d.Revision()
	b.AutoUntil = now.Add(time.Hour).Unix()
	require.NoError(t, db.Save(&b).Error)
	source, err := r.sourceContractRevision(db, d.Scope)
	require.NoError(t, err)
	lease, err := r.AcquireContractProbe(ctx, d.Scope, d.Revision(), "fresh-gray", source, now)
	require.NoError(t, err)
	_, err = r.RecordContractResult(ctx, biz.ContractResult{Scope: d.Scope, Revision: d.Revision(), BindingRevision: 3, IncidentRevision: 1, LeaseID: lease.ID, AttemptID: "fresh-gray", Record: verifiedContractRecord(), CapabilityValid: true}, now)
	require.NoError(t, err)
	_, out, err := r.ListContracts(ctx, d.Scope, 20)
	require.NoError(t, err)
	require.Equal(t, "gray", out.Status)
	require.EqualValues(t, 5, out.GrayPercent)
}
