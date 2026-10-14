package contractdto

import (
	"fmt"
	v "micro-one-api/api/channel/v1"
	"micro-one-api/domain/upstream/contract"
)

func Scope(p *v.ContractScope) contract.Scope {
	return contract.Scope{Kind: p.GetSourceKind(), ID: p.GetSourceId(), Model: p.GetUpstreamModelId(), Protocol: p.GetProtocol()}
}
func ScopeProto(s contract.Scope) *v.ContractScope {
	return &v.ContractScope{SourceKind: s.Kind, SourceId: s.ID, UpstreamModelId: s.Model, Protocol: s.Protocol}
}
func Document(p *v.UpstreamContractDocument) contract.Document {
	d := contract.Document{Scope: Scope(p.GetScope()), Provider: p.GetProvider(), AdapterBuild: p.GetAdapterBuild(), ParserRevision: p.GetParserRevision(), UsageRevision: p.GetUsageRevision(), APIVersion: p.GetApiVersion(), Endpoint: p.GetEndpoint(), Inbound: append([]string(nil), p.GetInboundProtocols()...), FixtureEvidence: p.GetFixtureEvidenceId(), BasisRevision: p.GetBasisRevision()}
	for _, set := range p.GetRuleSets() {
		s := contract.RuleSet{Level: set.GetLevel()}
		for _, r := range set.GetRules() {
			s.Rules = append(s.Rules, contract.Rule{ID: r.GetId(), Kind: r.GetKind(), Path: r.GetPath(), Target: r.GetTarget(), Value: append([]byte(nil), r.GetValueJson()...), Type: r.GetType(), Min: r.Min, Max: r.Max, Replaces: r.GetReplaces(), Evidence: r.GetEvidenceId()})
		}
		d.Sets = append(d.Sets, s)
	}
	return d
}
func DocumentProto(d contract.Document) *v.UpstreamContractDocument {
	p := &v.UpstreamContractDocument{Scope: ScopeProto(d.Scope), Provider: d.Provider, AdapterBuild: d.AdapterBuild, ParserRevision: d.ParserRevision, UsageRevision: d.UsageRevision, ApiVersion: d.APIVersion, Endpoint: d.Endpoint, InboundProtocols: d.Inbound, FixtureEvidenceId: d.FixtureEvidence, BasisRevision: d.BasisRevision}
	for _, set := range d.Sets {
		s := &v.ContractRuleSet{Level: set.Level}
		for _, r := range set.Rules {
			s.Rules = append(s.Rules, &v.ContractRule{Id: r.ID, Kind: r.Kind, Path: r.Path, Target: r.Target, ValueJson: r.Value, Type: r.Type, Min: r.Min, Max: r.Max, Replaces: r.Replaces, EvidenceId: r.Evidence})
		}
		p.RuleSets = append(p.RuleSets, s)
	}
	return p
}
func Snapshot(p *v.UpstreamContractSnapshot) (contract.Snapshot, error) {
	s, err := contract.Compile(Document(p.GetDocument()))
	if err != nil {
		return s, err
	}
	s.BindingRevision = p.GetBindingRevision()
	s.IncidentRevision = p.GetIncidentRevision()
	if s.Revision != p.GetRevision() {
		return s, fmt.Errorf("contract snapshot digest mismatch")
	}
	return s, nil
}
func SnapshotProto(s contract.Snapshot) *v.UpstreamContractSnapshot {
	return &v.UpstreamContractSnapshot{Document: DocumentProto(s.Document), Revision: s.Revision, BindingRevision: s.BindingRevision, IncidentRevision: s.IncidentRevision}
}
