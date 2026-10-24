package data

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"io"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	authzt "micro-one-api/domain/authorization/testutil"
	"micro-one-api/domain/upstream/contract"
	"micro-one-api/domain/upstream/evidence"
	dbt "micro-one-api/platform/database/testutil"
	"net/http"
	"strings"
	"testing"
	"time"
)

func contractDoc(kind string, id int64) contract.Document {
	return contract.Document{Scope: contract.Scope{Kind: kind, ID: id, Model: "vendor/Exact", Protocol: "openai_chat"}, Provider: "fixture", AdapterBuild: contract.BuildID, ParserRevision: contract.ParserID, UsageRevision: contract.UsageID, Inbound: []string{"openai_chat"}, FixtureEvidence: "independent-matrix", BasisRevision: "official-schema-v1"}
}
func verifiedContractRecord() *evidence.Record {
	return &evidence.Record{Complete: true, HTTPStatus: 200, Observation: evidence.Parse([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`), "openai_chat")}
}

type contractProofTestTransport func(*http.Request) (*http.Response, error)

func (f contractProofTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDeepSeekIndependentProofPublishesAndQuarantinesCurrentSSE(t *testing.T) {
	db := dbt.RoutingContextDB(t, "sqlite")
	r := &Repository{db: db}
	ctx := authorization.WithWriteReason(context.Background(), "reviewed independent DeepSeek proof")
	now := time.Now().UTC()
	empty := ""
	require.NoError(t, db.Create(&channelModel{ModelMapping: &empty, SystemPrompt: &empty, Config: "{}", ID: 11, Name: "fixture", Type: 1, Status: 1, Key: "fixture-only", Models: "deepseek-flash"}).Error)
	doc := contractDoc("channel", 11)
	doc.Provider, doc.Scope.Model = "deepseek", "deepseek-flash"
	doc.ParserRevision, doc.UsageRevision = contract.DeepSeekProofParserID, contract.DeepSeekProofUsageID
	require.NoError(t, r.CreateContract(ctx, biz.UpstreamContract{Document: doc, Revision: doc.Revision()}, now))
	source, err := r.sourceContractRevision(db, doc.Scope)
	require.NoError(t, err)
	record := func(binding, incident int64, bad bool) *evidence.Record {
		usage := `{"prompt_tokens":8,"completion_tokens":1,"total_tokens":9,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":8}`
		if bad {
			usage = strings.ReplaceAll(usage, `"prompt_tokens":8`, `"prompt_tokens":"8"`)
		}
		snapshot, err := contract.Compile(doc)
		require.NoError(t, err)
		snapshot.BindingRevision, snapshot.IncidentRevision = binding, incident
		var observed evidence.Record
		call := contract.WithSelection(ctx, contract.Selection{Snapshots: []contract.Snapshot{snapshot}, Inbound: "openai_chat"})
		call = evidence.WithRecorder(call, func(r evidence.Record) { observed = r })
		client := &http.Client{Transport: contractProofTestTransport(func(*http.Request) (*http.Response, error) {
			body := "data: {\"object\":\"chat.completion.chunk\",\"usage\":null}\n\ndata: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":" + usage + "}\n\ndata: [DONE]\n\n"
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		request, err := http.NewRequestWithContext(call, http.MethodPost, "https://fixture.invalid/chat/completions", strings.NewReader(`{}`))
		require.NoError(t, err)
		response, err := evidence.Do(client, request, "openai_chat")
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		return &observed
	}
	lease, err := r.AcquireContractProbe(ctx, doc.Scope, doc.Revision(), "independent-first-proof", source, now)
	require.NoError(t, err)
	valid := record(lease.Snapshot.BindingRevision, lease.Snapshot.IncidentRevision, false)
	accepted, err := r.RecordContractResult(ctx, biz.ContractResult{Scope: doc.Scope, Revision: doc.Revision(), AttemptID: "independent-first-proof", LeaseID: lease.ID, Record: valid, CapabilityValid: true}, now)
	require.NoError(t, err)
	require.True(t, accepted)
	binding, err := r.TransitionContract(ctx, biz.ContractTransition{Scope: doc.Scope, Revision: doc.Revision(), Action: "publish", Reason: "independent raw usage proof passed", GrayPercent: 5}, now)
	require.NoError(t, err)
	require.Equal(t, "gray", binding.Status)
	valid = record(binding.Revision, binding.IncidentRevision, false)
	accepted, err = r.RecordContractResult(ctx, biz.ContractResult{Scope: doc.Scope, Revision: doc.Revision(), BindingRevision: binding.Revision, IncidentRevision: binding.IncidentRevision, AttemptID: "independent-user-proof", Record: valid, CapabilityValid: true}, now.Add(time.Second))
	require.NoError(t, err)
	require.True(t, accepted)
	_, actual, err := r.ListContracts(ctx, doc.Scope, 20)
	require.NoError(t, err)
	require.Equal(t, "gray", actual.Status, "old native unsupported/incomplete status must not quarantine a verified independent proof")
	require.EqualValues(t, 1, actual.CompleteAttempts)
	require.Equal(t, "unsupported", valid.Observation.Status)
	lease, err = r.AcquireContractProbe(ctx, doc.Scope, doc.Revision(), "independent-bad-current", source, now.Add(2*time.Second))
	require.NoError(t, err)
	bad := record(lease.Snapshot.BindingRevision, lease.Snapshot.IncidentRevision, true)
	require.False(t, bad.Complete, "the original native tracker still reports its metering fault as incomplete")
	require.True(t, bad.ContractProof.Complete, "the independent proof records that the same SSE reached DONE")
	accepted, err = r.RecordContractResult(ctx, biz.ContractResult{Scope: doc.Scope, Revision: doc.Revision(), BindingRevision: lease.Snapshot.BindingRevision, IncidentRevision: lease.Snapshot.IncidentRevision, AttemptID: "independent-bad-current", LeaseID: lease.ID, Record: bad, CapabilityValid: true}, now.Add(2*time.Second))
	require.NoError(t, err)
	require.True(t, accepted)
	_, actual, err = r.ListContracts(ctx, doc.Scope, 20)
	require.NoError(t, err)
	require.Equal(t, "quarantined", actual.Status, "a complete current SSE proof with invalid metering must isolate the contract even when native Complete is false")
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
			require.Equal(t, "active", b.Status, "a failed candidate probe is not evidence that the current contract failed")
			_, err = r.TransitionContract(ctx, biz.ContractTransition{Scope: d.Scope, Revision: rev2, Expected: uint64(b.Revision), Action: "publish", Reason: "candidate has no valid proof", GrayPercent: 1}, later.Add(3*time.Minute))
			require.ErrorIs(t, err, biz.ErrContractGate)
			// Only a bounded failed probe of the actual current revision may
			// quarantine its live traffic.
			ok, err = probe(rev, later.Add(4*time.Minute), bad)
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
	// A separately established current-revision incident is already isolated.
	// Failure of a rollback-target probe cannot restore it or move the incident.
	binding.Status = "quarantined"
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

func TestGrayRecoveryAndRollbackRequireNewObservationWindow(t *testing.T) {
	for _, action := range []string{"recover", "automatic_recover", "rollback"} {
		t.Run(action, func(t *testing.T) {
			db := dbt.RoutingContextDB(t, "sqlite")
			r := &Repository{db: db}
			ctx := context.Background()
			now := time.Now().UTC()
			empty := ""
			require.NoError(t, db.Create(&channelModel{ID: 1, Status: 1, Key: "fixture", ModelMapping: &empty, SystemPrompt: &empty, Config: "{}"}).Error)
			d := contractDoc("channel", 1)
			previous := d
			previous.BasisRevision = "prior-schema"
			for _, document := range []contract.Document{d, previous} {
				require.NoError(t, r.CreateContract(ctx, biz.UpstreamContract{Document: document, Revision: document.Revision()}, now))
			}
			b := contractBindingModel{ID: d.Scope.Key(), SourceKind: d.Scope.Kind, SourceID: 1, Model: d.Scope.Model, Protocol: d.Scope.Protocol, Current: d.Revision(), Previous: previous.Revision(), Revision: 3, IncidentRevision: 1, Status: "quarantined", ApprovedPercent: 5, PreviousPercent: 5, GrayPercent: 5, StartedAt: now.Add(-time.Hour).Unix(), CompleteAttempts: 100}
			if action == "automatic_recover" {
				b.AutoRevision, b.AutoUntil = d.Revision(), now.Add(time.Hour).Unix()
			}
			require.NoError(t, db.Save(&b).Error)
			target := d.Revision()
			if action == "rollback" {
				target = previous.Revision()
			}
			source, err := r.sourceContractRevision(db, d.Scope)
			require.NoError(t, err)
			lease, err := r.AcquireContractProbe(ctx, d.Scope, target, "new-proof", source, now)
			require.NoError(t, err)
			_, err = r.RecordContractResult(ctx, biz.ContractResult{Scope: d.Scope, Revision: target, BindingRevision: b.Revision, IncidentRevision: b.IncidentRevision, AttemptID: "new-proof", LeaseID: lease.ID, Record: verifiedContractRecord(), CapabilityValid: true}, now)
			require.NoError(t, err)
			if action != "automatic_recover" {
				_, err = r.TransitionContract(ctx, biz.ContractTransition{Scope: d.Scope, Revision: target, Expected: uint64(b.Revision), Action: action, Reason: "fresh independent proof"}, now)
				require.NoError(t, err)
			}
			_, restored, err := r.ListContracts(ctx, d.Scope, 20)
			require.NoError(t, err)
			require.Equal(t, "gray", restored.Status)
			require.Equal(t, target, restored.Current)
			require.Equal(t, now.Unix(), restored.StartedAt, "recovery and rollback cannot inherit a prior candidate's observation window")
			require.Zero(t, restored.CompleteAttempts, "only post-recovery attempts may promote this revision")
			require.Zero(t, restored.Failures)
		})
	}
}
