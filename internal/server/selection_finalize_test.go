package server

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	relaycredential "micro-one-api/domain/upstream/credential"
	relaybiz "micro-one-api/internal/biz"
)

type selectionTestChannelClient struct {
	fallback *relaybiz.Channel
	models   []string
}

func (c *selectionTestChannelClient) SelectChannel(_ context.Context, _, model string, excludeFirstPriority bool) (*relaybiz.Channel, error) {
	c.models = append(c.models, model)
	if c.fallback == nil {
		return nil, errors.New("no channel")
	}
	return c.fallback, nil
}

func (c *selectionTestChannelClient) SelectChannelExcluding(_ context.Context, _, model string, excluded map[int64]bool) (*relaybiz.Channel, error) {
	c.models = append(c.models, model)
	if c.fallback == nil || excluded[c.fallback.ID] {
		return nil, errors.New("no channel")
	}
	return c.fallback, nil
}

func (*selectionTestChannelClient) RecordChannelHealth(context.Context, int64, bool, string, int64) error {
	return nil
}

func (*selectionTestChannelClient) RecordSubscriptionAccountHealth(context.Context, int64, bool) error {
	return nil
}

type selectionTestRecorder struct {
	events []relaybiz.SelectionEvent
}

func (r *selectionTestRecorder) RecordSelection(_ context.Context, event relaybiz.SelectionEvent) {
	r.events = append(r.events, event)
}

func TestMaybeFailoverChannel_SelectsDifferentSourceAfterRetryableFailure(t *testing.T) {
	initial := &relaybiz.Channel{ID: 7, SubscriptionAccountID: 7}
	fallback := &relaybiz.Channel{ID: 7, Name: "ordinary-channel"}
	channelClient := &selectionTestChannelClient{fallback: fallback}
	uc := relaybiz.NewRelayUsecase(nil, channelClient, nil, nil)
	server := &HTTPServer{relayUsecase: uc}
	plan := &relaybiz.RelayPlan{
		Auth:        &relaybiz.AuthSnapshot{Group: "default"},
		Channel:     initial,
		GlobalModel: "resolved-model",
	}
	next := initial

	excluded := map[relaybiz.RoutingSourceIdentity]bool{
		relaybiz.RoutingSourceIdentityForChannel(initial): true,
	}
	if !server.maybeFailoverChannel(context.Background(), plan, "client-model", initial, errors.New("dial tcp: connection refused"), excluded, &next) {
		t.Fatal("expected retryable transport failure to select fallback")
	}
	if next != fallback {
		t.Fatalf("selected channel = %+v, want fallback", next)
	}
	if len(channelClient.models) == 0 || channelClient.models[0] != "client-model" {
		t.Fatalf("fallback selected with models %v, want client-model first", channelClient.models)
	}
}

func TestFinalizeSelectionFromResult_UpdatesFinalSourceKind(t *testing.T) {
	recorder := &selectionTestRecorder{}
	uc := relaybiz.NewRelayUsecase(nil, nil, nil, nil)
	uc.SetSelectionRecorder(recorder)
	server := &HTTPServer{relayUsecase: uc}
	plan := &relaybiz.RelayPlan{SelectionEvent: &relaybiz.SelectionEvent{
		FinalKind:     relaybiz.UpstreamRouteSubscription.String(),
		FinalSourceID: 7,
	}}

	server.finalizeSelectionFromResult(plan, &relaybiz.ExecuteResult{
		Channel:  &relaybiz.Channel{ID: 7},
		Fallback: true,
	}, time.Millisecond)

	if len(recorder.events) != 1 {
		t.Fatalf("recorded events = %d, want 1", len(recorder.events))
	}
	got := recorder.events[0]
	if got.FinalKind != relaybiz.UpstreamRouteChannel.String() || got.FinalSourceID != 7 {
		t.Fatalf("final source = %s:%d, want channel:7", got.FinalKind, got.FinalSourceID)
	}
}

func TestBuildOpenAIWSUpstreamTarget_ResolvesSubscriptionCredential(t *testing.T) {
	resolver := relaycredential.NewNoopAccountResolver()
	resolver.SeedByChannel(23, &relaycredential.SubscriptionAccountMetadata{AccessToken: "resolved-token"})
	server := &HTTPServer{accountResolver: resolver}
	req, err := http.NewRequest(http.MethodGet, "http://relay.test/v1/responses", nil)
	if err != nil {
		t.Fatal(err)
	}

	url, headers, err := server.buildOpenAIWSUpstreamTarget(context.Background(), req, &relaybiz.Channel{
		ID:                    23,
		SubscriptionAccountID: 23,
		BaseURL:               "https://api.openai.com/v1",
	})
	if err != nil {
		t.Fatalf("buildOpenAIWSUpstreamTarget: %v", err)
	}
	if url != "wss://api.openai.com/v1/responses" {
		t.Fatalf("url = %q", url)
	}
	if got := headers.Get("Authorization"); got != "Bearer resolved-token" {
		t.Fatalf("authorization = %q", got)
	}
}

func TestUsageLogInputApplyChannelInputs_UsesFinalRoutingSource(t *testing.T) {
	in := usageLogInput{SubscriptionAccountID: 99, SourceKind: relaybiz.UpstreamSourceSubscription}
	in.applyChannelInputs(&relaybiz.Channel{ID: 7, UpstreamModelID: "upstream-final"})

	if in.SourceKind != relaybiz.UpstreamSourceChannel || in.SubscriptionAccountID != 0 {
		t.Fatalf("source = %s:%d, want channel:0", in.SourceKind, in.SubscriptionAccountID)
	}
	if in.UpstreamModelID != "upstream-final" {
		t.Fatalf("upstream model = %q", in.UpstreamModelID)
	}
}
