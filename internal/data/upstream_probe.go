package data

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"micro-one-api/domain/execution"
	"micro-one-api/domain/upstream/contract"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/domain/upstream/provider"
	"micro-one-api/internal/adaptor"
	"micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
)

type upstreamProbeRepo struct {
	channels biz.ProbeSourceClient
	delivery *biz.ExecutionDelivery
	factory  *provider.ProviderFactory
}

func NewUpstreamProbeRepo(c biz.ProbeSourceClient, d *biz.ExecutionDelivery, f *provider.ProviderFactory) biz.UpstreamProbeRepo {
	return &upstreamProbeRepo{c, d, f}
}
func (r *upstreamProbeRepo) Probe(ctx context.Context, t execution.ProbeTarget, attemptID string) (result execution.ProbeResult, err error) {
	if t.Inventory && len(t.ManualModelIDs) > 0 {
		now := time.Now().Unix()
		err = r.channels.RecordProbeInventory(ctx, t.SourceKind, t.SourceID, t.ManualModelIDs, attemptID, now, true)
		return execution.ProbeResult{AttemptID: attemptID, TargetID: t.ID, Inventory: t.ManualModelIDs, ObservedAt: now, Outcome: "inventory_manual"}, err
	}
	result = execution.ProbeResult{AttemptID: attemptID, TargetID: t.ID, ObservedAt: time.Now().Unix(), Outcome: "transport_failure"}
	var typ int32
	var base, key, version string
	var account *adaptor.AccountRef
	source, err := r.channels.GetProbeSource(ctx, t.SourceKind, t.SourceID)
	if err != nil {
		return result, err
	}
	if source.Channel != nil {
		c := source.Channel
		typ, base, key, version = c.Type, c.BaseURL, c.Key, c.Config.APIVersion
	} else if source.Account != nil {
		c := source.Account
		base, key = c.BaseURL, c.AccessToken
		account = &adaptor.AccountRef{ID: c.ID, Platform: c.Platform, AccountType: c.AccountType, GroupID: c.Group, AccessToken: c.AccessToken, AccountID: c.AccountID, Fingerprint: c.Fingerprint}
		switch c.Platform {
		case "claude":
			typ = provider.ChannelTypeClaudeOAuth
		case "codex":
			typ = provider.ChannelTypeCodexOAuth
		case "anthropic":
			typ = provider.ChannelTypeAnthropic
		case "gemini":
			typ = provider.ChannelTypeGemini
		case "openai":
			typ = provider.ChannelTypeOpenAI
		default:
			return result, fmt.Errorf("subscription probe requires configured adaptor")
		}
	} else {
		return result, fmt.Errorf("probe source unavailable")
	}
	var contractLease contract.ProbeLease
	var contractClient biz.ContractProbeClient
	scope := contract.Scope{Kind: t.SourceKind, ID: t.SourceID, Model: t.Model, Protocol: t.Protocol}
	if t.ContractRevision != "" {
		var ok bool
		contractClient, ok = r.channels.(biz.ContractProbeClient)
		if !ok {
			return result, fmt.Errorf("contract probe owner unavailable")
		}
		contractLease, err = contractClient.AcquireContractProbe(ctx, scope, t.ContractRevision, attemptID)
		if err != nil {
			return result, err
		}
		if source.Revision == "" || source.Revision != contractLease.SourceRevision {
			return result, fmt.Errorf("probe source changed before send")
		}
		ctx = contract.WithSelection(ctx, contract.Selection{Snapshots: []contract.Snapshot{contractLease.Snapshot}, Inbound: t.Protocol})
	}
	now := time.Now()
	purpose := "canary"
	if t.Inventory {
		purpose = "health_probe"
	}
	i := execution.Intent{RequestID: attemptID, RootRequestID: attemptID, AttemptNumber: 1, Purpose: purpose, SourceKind: t.SourceKind, SourceID: t.SourceID, Model: t.Model, Policy: execution.Policy, LeaseUntil: now.Add(time.Minute).Unix(), EvidenceDeadline: now.Add(24 * time.Hour).Unix(), CostBasis: "unknown"}
	if t.Inventory {
		i.Model = ""
	}
	i = execution.Freeze(i)
	// Both durable stores must confirm the independent intent before any supplier call.
	e := execution.NewIntentEvent(i, now)
	if err = r.delivery.Admit(ctx, e); err != nil {
		return result, err
	}
	if _, err = r.delivery.Deliver(ctx, e); err != nil {
		return result, err
	}
	var mu sync.Mutex
	var native *evidence.Record
	var persistedErr error
	var terminal *execution.Event
	var capabilityErr error
	seq := int64(0)
	ctx = evidence.WithDurableRecorder(ctx, func() error { return nil }, func(record evidence.Record) error {
		mu.Lock()
		defer mu.Unlock()
		seq++
		native = &record
		ev := execution.Event{Version: 1, Intent: i, Category: "terminal", Sequence: seq, ObservedAt: time.Now().Unix(), Record: &record, BuildRevision: evidence.BuildRevision}
		if ev.Record != nil && ev.Record.Execution == "checkpoint" {
			ev.Category = "checkpoint"
		}
		ev.ID = ev.Key()
		detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		terminal = &ev
		persistedErr = r.delivery.Put(detached, ev)
		return persistedErr
	})
	method, path, body := http.MethodPost, "/chat/completions", map[string]any{"model": t.Model, "messages": []any{map[string]any{"role": "user", "content": "Reply OK."}}, "max_tokens": t.MaxOutputTokens, "stream": t.Stream}
	if t.Inventory {
		method, path, body = http.MethodGet, "/models", nil
	} else {
		switch t.Protocol {
		case "responses":
			path = "/responses"
			body = map[string]any{"model": t.Model, "input": "Reply OK.", "max_output_tokens": t.MaxOutputTokens, "stream": t.Stream}
		case "anthropic_messages":
			path = "/messages"
		case "gemini":
			path = "/models/" + t.Model + ":generateContent"
			if t.Stream {
				path = "/models/" + t.Model + ":streamGenerateContent"
			}
			body = map[string]any{"contents": []any{map[string]any{"parts": []any{map[string]any{"text": "Reply OK."}}}}, "generationConfig": map[string]any{"maxOutputTokens": t.MaxOutputTokens}}
		}
	}
	if t.Capability == "tools" && !t.Inventory {
		execution.AddProbeTool(body, t.Protocol)
	}
	if !t.Inventory && typ == provider.ChannelTypeDeepSeek {
		body["thinking"] = map[string]any{"type": "disabled"}
		if t.Stream {
			body["stream_options"] = map[string]any{"include_usage": true}
		}
	}
	var raw []byte
	if body != nil {
		raw, err = jsonx.Marshal(body)
		if err != nil {
			return result, err
		}
	}
	req := &provider.RawRequest{Method: method, Path: path, Header: http.Header{"Content-Type": {"application/json"}}, Body: raw}

	if t.Inventory {
		p, createErr := r.factory.CreateProviderWithConfig(typ, base, key, provider.ProviderConfig{APIVersion: version})
		if createErr != nil {
			return result, createErr
		}
		resp, callErr := p.Forward(ctx, req)
		err = callErr
		if err == nil {
			result.Inventory, err = probeModelIDs(resp.Body)
		}
	} else {
		ad, ok := adaptor.GetAdaptor(typ)
		if !ok {
			return result, fmt.Errorf("probe adaptor unavailable")
		}
		format := adaptor.Format(t.Protocol)
		if t.Protocol == "openai_chat" {
			format = adaptor.FormatOpenAIChatCompletions
		}
		rc := &adaptor.RelayContext{InboundFormat: format, ResolvedModel: t.Model, ClientModel: t.Model, Channel: &biz.Channel{ID: t.SourceID, Type: typ, BaseURL: base, Key: key, Config: biz.ChannelConfig{APIVersion: version}}, Account: account, IsStream: t.Stream, RequestID: attemptID, RawBody: raw, InboundHeader: req.Header}
		ad.Init(rc)
		upstreamFormat, converted, convertErr := ad.ConvertRequest(rc, format, raw)
		if convertErr != nil {
			return result, convertErr
		}
		request, buildErr := ad.BuildUpstreamRequest(ctx, rc, upstreamFormat, converted)
		if buildErr != nil {
			return result, buildErr
		}
		if validateErr := provider.ValidateBaseURLForChannel(typ, request.URL.String()); validateErr != nil {
			return result, validateErr
		}
		if typ == provider.ChannelTypeOllama {
			request = provider.WithLocalNetworkAccess(request)
		}
		client := provider.NewHTTPClient(30 * time.Second)
		if t.Stream {
			client = provider.NewStreamHTTPClient(30 * time.Second)
		}
		protocol := string(upstreamFormat)
		if upstreamFormat == adaptor.FormatOpenAIChatCompletions {
			protocol = "openai_chat"
		}
		if contractClient != nil && protocol != scope.Protocol {
			return result, fmt.Errorf("candidate upstream protocol differs from the admitted probe scope")
		}
		response, callErr := evidence.Do(client, request, protocol)
		err = callErr
		if err == nil {
			output, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20))
			err = readErr
			capabilityErr = execution.ValidateProbeOutput(output, protocol, t.Stream, t.Capability == "tools")
			closeErr := response.Body.Close()
			if err == nil {
				err = closeErr
			}
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				err = &provider.UpstreamHTTPError{StatusCode: response.StatusCode}
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if persistedErr != nil {
		return result, persistedErr
	}
	if terminal != nil {
		detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		receipt, deliveryErr := r.delivery.Deliver(detached, *terminal)
		cancel()
		if deliveryErr != nil {
			return result, deliveryErr
		}
		result.RealTraffic = receipt.Comparison
	}
	if native != nil {
		result.RecordJSON, _ = jsonx.Marshal(native)
		if contractClient != nil {
			detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			reportErr := contractClient.ReportContractProbe(detached, scope, contractLease, attemptID, result.RecordJSON, capabilityErr == nil && err == nil)
			cancel()
			if reportErr != nil {
				return result, reportErr
			}
		}
	}
	if err != nil {
		result.Outcome = "transport_failure"
		if biz.UpstreamStatus(err) >= 400 {
			result.Outcome = "upstream_rejected"
		}
		if !t.Inventory && biz.UpstreamStatus(err) == 400 {
			result.ValidContractProbe = true
			result.Outcome = "contract_probe_rejected"
		}
		return result, nil
	}
	if t.Inventory {
		err = r.channels.RecordProbeInventory(ctx, t.SourceKind, t.SourceID, result.Inventory, attemptID, result.ObservedAt, false)
		if err != nil {
			return result, err
		}
		result.Outcome = "inventory_observed"
		return result, nil
	}
	result.ValidContractProbe = true
	result.Outcome = "contract_drift"
	verified := native != nil && native.Complete && native.Observation.Status == "verified"
	if native != nil && contractClient != nil {
		verified = evidence.ContractVerified(native, contractLease.Snapshot.Document)
	}
	if verified && native.Stream == t.Stream && capabilityErr == nil {
		result.Outcome = "contract_verified"
	}
	return result, nil
}
func probeModelIDs(raw []byte) ([]string, error) {
	var p struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if len(raw) > 1<<20 || jsonx.Unmarshal(raw, &p) != nil {
		return nil, fmt.Errorf("invalid inventory response")
	}
	if p.Data == nil && p.Models == nil {
		return nil, fmt.Errorf("inventory list absent")
	}
	set := map[string]bool{}
	for _, m := range p.Data {
		if m.ID == "" || len(m.ID) > 255 {
			return nil, fmt.Errorf("invalid exact model ID")
		}
		set[m.ID] = true
	}
	for _, m := range p.Models {
		if m.Name == "" || len(m.Name) > 255 {
			return nil, fmt.Errorf("invalid exact model ID")
		}
		set[m.Name] = true
	}
	if len(set) > 10000 {
		return nil, fmt.Errorf("inventory exceeds limit")
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}
