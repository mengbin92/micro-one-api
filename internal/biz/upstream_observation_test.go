package biz

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"micro-one-api/domain/routing"
	"micro-one-api/domain/upstream/provider"
)

type observingChannelClient struct {
	mockChannelSelector
	observations []provider.Observation
}

func (c *observingChannelClient) RecordUpstreamObservation(_ context.Context, observation provider.Observation) error {
	c.observations = append(c.observations, observation)
	return nil
}

func (*observingChannelClient) SelectChannelExcluding(context.Context, string, string, map[int64]bool) (*Channel, error) {
	return nil, nil
}

func (*observingChannelClient) CanRoute(_ context.Context, _, model string, _ routing.Source) (routing.Permission, error) {
	return routing.Permission{Allowed: true, UpstreamModelID: model}, nil
}

func TestRetryExecutorReportsScopedUpstreamObservations(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		kind    string
		outcome provider.HealthOutcome
	}{
		{"success", nil, "", provider.HealthSuccess},
		{"capacity", &provider.UpstreamHTTPError{StatusCode: 429, RetryAfter: 10 * time.Minute}, "capacity", provider.HealthNeutral},
		{"overloaded", &provider.UpstreamHTTPError{StatusCode: 529}, "capacity", provider.HealthNeutral},
		{"credential", &provider.UpstreamHTTPError{StatusCode: 401}, "configuration", provider.HealthNeutral},
		{"proxy URL", &provider.UpstreamHTTPError{StatusCode: 404, Body: []byte("<html>nginx</html>")}, "configuration", provider.HealthNeutral},
		{"local URL", provider.InvalidConfiguration(errors.New("unsupported URL scheme")), "configuration", provider.HealthNeutral},
		{"model", &provider.UpstreamHTTPError{StatusCode: 500, Body: []byte(`{"message":"no healthy model"}`)}, "model", provider.HealthNeutral},
		{"availability", &provider.UpstreamHTTPError{StatusCode: 503}, "", provider.HealthFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &observingChannelClient{}
			ch := &Channel{ID: 7, UpstreamConfigurationRevision: "configuration-v2", HealthProbeID: "probe-7", ModelMapping: `{"gpt-5":"gpt-5-prod"}`}
			result := NewRetryExecutor(&RetryPolicy{MaxAttempts: 1}, client).ExecuteWithInitialChannel(context.Background(), "default", "gpt-5", ch, func(context.Context, *Channel) error { return tc.err })
			if !errors.Is(result.Err, tc.err) || len(client.observations) != 1 {
				t.Fatalf("result=%v observations=%+v", result.Err, client.observations)
			}
			got := client.observations[0]
			if got.SourceKind != UpstreamSourceChannel || got.SourceID != 7 || got.ConfigurationRevision != "configuration-v2" || got.ProbeID != "probe-7" || got.UpstreamModelID != "gpt-5-prod" || got.ID == "" || got.StartedAt.IsZero() {
				t.Fatalf("observation lost source/configuration identity: %+v", got)
			}
			if got.FailureKind != tc.kind || got.Outcome != tc.outcome {
				t.Fatalf("kind/outcome=%s/%v, want %s/%v", got.FailureKind, got.Outcome, tc.kind, tc.outcome)
			}
			if tc.name == "capacity" && got.RetryAfter != 5*time.Minute || tc.name == "overloaded" && got.RetryAfter != 30*time.Second {
				t.Fatalf("capacity cooldown=%v", got.RetryAfter)
			}
			if len(client.healthEvents) != 0 {
				t.Fatalf("new observations must not also feed legacy bool health: %+v", client.healthEvents)
			}
		})
	}
}

func TestRetryExecutorIgnoresPreForwardDependencyErrors(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("billing unavailable")}} {
		client := &observingChannelClient{}
		calls := 0
		result := NewRetryExecutor(&RetryPolicy{MaxAttempts: 3}, client).ExecuteWithInitialChannel(context.Background(), "default", "gpt-5", &Channel{ID: 7, UpstreamConfigurationRevision: "v2"}, func(context.Context, *Channel) error {
			calls++
			return provider.BeforeForward(cause)
		})
		if !errors.Is(result.Err, cause) || calls != 1 || len(client.observations) != 0 || len(client.healthEvents) != 0 || len(client.modelHealth) != 0 {
			t.Fatalf("preforward result=%v calls=%d observations=%+v health=%+v models=%+v", result.Err, calls, client.observations, client.healthEvents, client.modelHealth)
		}
	}
}

func TestRetryBudgetSharedAcrossRequestExecutors(t *testing.T) {
	client := &observingChannelClient{}
	uc := NewRelayUsecase(nil, client, nil, &RetryPolicy{MaxAttempts: 3, RetryableStatus: map[int]bool{503: true}})
	ch := &Channel{ID: 7, UpstreamConfigurationRevision: "v2"}
	calls := 0
	for range 30 {
		result := uc.NewRetryExecutor().ExecuteWithInitialChannel(context.Background(), "default", "gpt-5", ch, func(context.Context, *Channel) error {
			calls++
			return &provider.UpstreamHTTPError{StatusCode: 503}
		})
		if result.Err == nil {
			t.Fatal("failed upstream unexpectedly succeeded")
		}
	}
	if retries := calls - 30; retries != 12 {
		t.Fatalf("retries=%d, want 12 shared bootstrap/primary credits; per-request budgets amplify outages", retries)
	}
}

func TestDeferredStreamHealthDoesNotTreatHeadersAsSuccess(t *testing.T) {
	client := &observingChannelClient{}
	ch := &Channel{ID: 7, UpstreamConfigurationRevision: "v2"}
	result := NewRetryExecutor(&RetryPolicy{MaxAttempts: 1}, client).WithDeferredStreamHealth().ExecuteWithInitialChannel(context.Background(), "default", "gpt-5", ch, func(context.Context, *Channel) error { return nil })
	if result.Err != nil || len(client.observations) != 0 || len(client.healthEvents) != 0 || len(client.modelHealth) != 0 {
		t.Fatalf("stream headers result=%v observations=%+v health=%+v models=%+v", result.Err, client.observations, client.healthEvents, client.modelHealth)
	}
}
