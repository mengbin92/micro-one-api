package biz

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/upstream/contract"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/platform/security/serviceidentity"
)

type contractProofOwnerRepo struct {
	*mockChannelRepo
	UpstreamContractRepo
	document contract.Document
	recorded int
}

func (r *contractProofOwnerRepo) GetContract(_ context.Context, scope contract.Scope, revision string) (contract.Snapshot, error) {
	if scope != r.document.Scope || revision != r.document.Revision() {
		return contract.Snapshot{}, ErrContractInvalid
	}
	return contract.Compile(r.document)
}
func (r *contractProofOwnerRepo) RecordContractResult(context.Context, ContractResult, time.Time) (bool, error) {
	r.recorded++
	return true, nil
}

func TestContractOwnerReparsesProofAgainstStoredDocument(t *testing.T) {
	doc := contract.Document{Scope: contract.Scope{Kind: "channel", ID: 11, Model: "deepseek-flash", Protocol: "openai_chat"}, Provider: "deepseek", AdapterBuild: contract.BuildID, ParserRevision: contract.DeepSeekProofParserID, UsageRevision: contract.DeepSeekProofUsageID, Inbound: []string{"openai_chat"}, FixtureEvidence: "official-pilot", BasisRevision: "official-schema"}
	const body = `{"usage":{"prompt_tokens":8,"completion_tokens":1,"total_tokens":9,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":8}}`
	repo := &contractProofOwnerRepo{mockChannelRepo: &mockChannelRepo{}, document: doc}
	uc := NewChannelUsecase(repo, nil)
	ctx := serviceidentity.WithRPCMethod(serviceidentity.WithPrincipal(context.Background(), serviceidentity.Principal{Name: "relay", Dedicated: true}), "/api.channel.v1.ChannelService/RecordContractResult")
	for _, tc := range []struct {
		name   string
		change func(*evidence.Record)
		valid  bool
	}{
		{"valid", func(*evidence.Record) {}, true},
		{"wrong_buckets", func(r *evidence.Record) { r.ContractProof.Observation.Buckets = &[5]int64{9000} }, false},
		{"old_profile", func(r *evidence.Record) { r.ContractProof.Observation.ParserRevision = evidence.ParserRevision }, false},
		{"wrong_snapshot", func(r *evidence.Record) { r.ParameterContract.Revision = strings.Repeat("0", 64) }, false},
		{"wrong_incident", func(r *evidence.Record) { r.ParameterContract.IncidentRevision++ }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proof := evidence.ParseDeepSeekCandidate([]byte(body))
			proof.ParserRevision, proof.ContractRevision = doc.ParserRevision, doc.UsageRevision
			r := &evidence.Record{Observation: evidence.Parse([]byte(body), "openai_chat"), ContractProof: &evidence.ContractProof{Observation: proof, Complete: true}, Complete: true, HTTPStatus: 200, ParameterContract: &contract.Trace{Scope: doc.Scope, Revision: doc.Revision(), Build: doc.AdapterBuild}}
			tc.change(r)
			before := repo.recorded
			accepted, err := uc.RecordContractResult(ctx, ContractResult{Scope: doc.Scope, Revision: doc.Revision(), AttemptID: tc.name, Record: r, CapabilityValid: true})
			if tc.valid {
				require.NoError(t, err)
				require.True(t, accepted)
				require.Equal(t, before+1, repo.recorded)
			} else {
				require.ErrorIs(t, err, ErrContractInvalid)
				require.Equal(t, before, repo.recorded, "a claimed verified status cannot bypass owner re-parse")
			}
		})
	}
}
