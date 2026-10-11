package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/internal/server/forwarder"
)

type slotLifecycleClient struct {
	orchestratorFailoverChannelClient
	mu        sync.Mutex
	events    []bool
	onAcquire func()
}

func TestProviderStreamCloseCancelsUpstream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	f := relayProviderStreamForwarder{forwarder: forwarder.NewStreamForwarder(relayprovider.NewProviderFactory(time.Second))}
	response, err := f.ForwardStream(ctx, &relaybiz.RelayPlan{Channel: &relaybiz.Channel{ID: 22, Type: 1, BaseURL: upstream.URL}}, relaybiz.ExecutorRequest{Endpoint: string(EndpointChatCompletions), Body: []byte(`{"model":"gpt-4o-mini","stream":true}`), Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- response.Stream.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("closing a stream waits for upstream data instead of cancelling the upstream")
	}
}

func (c *slotLifecycleClient) RecordChannelSlot(ctx context.Context, _ int64, _ string, acquired bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.events = append(c.events, acquired)
	c.mu.Unlock()
	return nil
}

func (c *slotLifecycleClient) RecordSubscriptionAccountSlot(ctx context.Context, id int64, slotID string, acquired bool) error {
	if acquired && c.onAcquire != nil {
		c.onAcquire()
	}
	return c.RecordChannelSlot(ctx, id, slotID, acquired)
}

func (c *slotLifecycleClient) reports() []bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]bool(nil), c.events...)
}

func TestChannelSlotReleaseSurvivesCancellationAndRepeats(t *testing.T) {
	client := &slotLifecycleClient{}
	uc := relaybiz.NewRelayUsecase(nil, client, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	release, err := uc.AcquireChannelSlot(ctx, &relaybiz.Channel{ID: 22})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	release()
	release()
	if got := client.reports(); !reflect.DeepEqual(got, []bool{true, false}) {
		t.Fatalf("slot reports = %v, want acquire then one release", got)
	}
}

func TestOrchestratorChannelSlotHeldUntilStreamFinishes(t *testing.T) {
	for _, closeEarly := range []bool{false, true} {
		t.Run(map[bool]string{false: "EOF", true: "close"}[closeEarly], func(t *testing.T) {
			client := &slotLifecycleClient{orchestratorFailoverChannelClient: orchestratorFailoverChannelClient{first: &relaybiz.Channel{ID: 22, Type: 1}}}
			uc := relaybiz.NewRelayUsecase(orchestratorIdentityClient{}, client, nil, nil)
			o := newRelayOrchestrator(uc, relayprovider.NewProviderFactory(time.Second), nil, nil)
			o.streamPort = &orchestratorFailoverStreamForwarder{}
			result, err := o.Execute(context.Background(), &RelayRequest{Token: "token", Model: "gpt-4o-mini", Endpoint: EndpointChatCompletions, IsStream: true, Body: strings.NewReader(`{"model":"gpt-4o-mini","stream":true}`)})
			if err != nil {
				t.Fatal(err)
			}
			defer result.Response.Close()
			if got := client.reports(); !reflect.DeepEqual(got, []bool{true}) {
				t.Fatalf("slot released before stream consumption: %v", got)
			}
			if !closeEarly {
				if _, err := io.ReadAll(result.Response); err != nil {
					t.Fatal(err)
				}
			}
			if err := result.Response.Close(); err != nil {
				t.Fatal(err)
			}
			if got := client.reports(); !reflect.DeepEqual(got, []bool{true, false}) {
				t.Fatalf("stream slot reports = %v", got)
			}
		})
	}
}

func TestSubscriptionRPMRejectionWaitsForSlotAcquireReport(t *testing.T) {
	started, unblock := make(chan struct{}), make(chan struct{})
	var once sync.Once
	allow := func() { once.Do(func() { close(unblock) }) }
	defer allow()
	client := &slotLifecycleClient{onAcquire: func() { close(started); <-unblock }}
	uc := relaybiz.NewRelayUsecase(nil, client, nil, nil)
	s := NewHTTPServer(nil, nil, nil, nil, uc)
	if !s.accountRPM.TryAcquire(context.Background(), 3, 1) {
		t.Fatal("could not fill account RPM")
	}
	plan := &relaybiz.RelayPlan{Channel: &relaybiz.Channel{ID: -3, SubscriptionAccountID: 3}, Account: &relaybiz.SubscriptionAccount{ID: 3, RPMLimit: 1}}
	done := make(chan error, 1)
	go func() {
		_, err := (httpRelayLifecycleHooks{s: s}).AcquireRelayAttempt(context.Background(), plan, relaybiz.ExecutorRequest{})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("acquire report not started")
	}
	select {
	case <-done:
		t.Fatal("RPM rejection returned before acquire report; release can overtake it")
	case <-time.After(50 * time.Millisecond):
	}
	allow()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected RPM rejection")
		}
	case <-time.After(time.Second):
		t.Fatal("RPM rejection did not finish")
	}
	if got := client.reports(); !reflect.DeepEqual(got, []bool{true, false}) {
		t.Fatalf("slot reports = %v, want ordered acquire/release", got)
	}
}
