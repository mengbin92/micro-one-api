package biz

import (
	"context"
	"errors"
	"testing"
)

type testIdentityClient struct{}

func (testIdentityClient) GetAuthSnapshot(_ context.Context, _ string) (*AuthSnapshot, error) {
	return &AuthSnapshot{
		UserID:        1,
		TokenID:       1,
		Group:         "default",
		AllowedModels: []string{"gpt-4o-mini"},
		UserEnabled:   true,
		TokenEnabled:  true,
	}, nil
}

type testChannelClient struct{}

func (testChannelClient) SelectChannel(_ context.Context, group, model string, _ bool) (*Channel, error) {
	return &Channel{
		ID:      1,
		Name:    group + ":" + model,
		BaseURL: "https://api.openai.com/v1",
	}, nil
}

type recordingChannelClient struct {
	models      []string
	failModels  map[string]error
	channelName string
}

func (c *recordingChannelClient) SelectChannel(_ context.Context, group, model string, _ bool) (*Channel, error) {
	c.models = append(c.models, model)
	if err := c.failModels[model]; err != nil {
		return nil, err
	}
	name := c.channelName
	if name == "" {
		name = group + ":" + model
	}
	return &Channel{
		ID:      1,
		Name:    name,
		BaseURL: "https://api.openai.com/v1",
	}, nil
}

func TestRelayUsecasePlan(t *testing.T) {
	uc := NewRelayUsecase(testIdentityClient{}, testChannelClient{}, nil, nil)
	plan, err := uc.Plan(context.Background(), RelayRequest{
		Token: "demo-token",
		Model: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Auth.Group != "default" {
		t.Fatalf("unexpected auth group: %s", plan.Auth.Group)
	}
	if plan.Channel.Name != "default:gpt-4o-mini" {
		t.Fatalf("unexpected channel name: %s", plan.Channel.Name)
	}
	if plan.ResolvedModel != "gpt-4o-mini" {
		t.Fatalf("unexpected resolved model: %s", plan.ResolvedModel)
	}
}

type testIdentityClientError struct {
	err error
}

func (c testIdentityClientError) GetAuthSnapshot(_ context.Context, _ string) (*AuthSnapshot, error) {
	return nil, c.err
}

func TestRelayUsecasePlan_IdentityError(t *testing.T) {
	wantErr := errors.New("token not found")
	uc := NewRelayUsecase(testIdentityClientError{err: wantErr}, testChannelClient{}, nil, nil)
	_, err := uc.Plan(context.Background(), RelayRequest{Token: "bad-token", Model: "gpt-4o-mini"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != wantErr.Error() {
		t.Fatalf("expected error %v, got %v", wantErr, err)
	}
}

type testChannelClientError struct {
	err error
}

func (c testChannelClientError) SelectChannel(_ context.Context, _, _ string, _ bool) (*Channel, error) {
	return nil, c.err
}

func TestRelayUsecasePlan_ChannelError(t *testing.T) {
	wantErr := errors.New("no channel available")
	uc := NewRelayUsecase(testIdentityClient{}, testChannelClientError{err: wantErr}, nil, nil)
	_, err := uc.Plan(context.Background(), RelayRequest{Token: "demo-token", Model: "gpt-4o-mini"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != wantErr.Error() {
		t.Fatalf("expected error %v, got %v", wantErr, err)
	}
}

func TestRelayUsecasePlan_ModelNotAllowed(t *testing.T) {
	uc := NewRelayUsecase(testIdentityClient{}, testChannelClient{}, nil, nil)
	_, err := uc.Plan(context.Background(), RelayRequest{Token: "demo-token", Model: "gpt-4"})
	if err == nil {
		t.Fatal("expected error for disallowed model, got nil")
	}
}

func TestRelayUsecasePlan_WithModelMapping(t *testing.T) {
	mapper := &ModelMapper{
		models: map[string]*ModelEntry{
			"gpt-4o": {ActualName: "gpt-4o-2024-08-06", Capabilities: []string{"function_call", "streaming"}},
		},
	}
	// testIdentityClient allows "gpt-4o-mini" but we'll use a custom one that allows "gpt-4o"
	channelClient := &recordingChannelClient{}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, mapper, nil)
	plan, err := uc.Plan(context.Background(), RelayRequest{Token: "demo-token", Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.ResolvedModel != "gpt-4o-2024-08-06" {
		t.Fatalf("expected resolved model gpt-4o-2024-08-06, got %s", plan.ResolvedModel)
	}
	if len(channelClient.models) != 1 || channelClient.models[0] != "gpt-4o" {
		t.Fatalf("expected channel selection with client model gpt-4o, got %v", channelClient.models)
	}
}

func TestRelayUsecasePlan_SelectsResolvedModelWhenClientModelHasNoChannel(t *testing.T) {
	mapper := &ModelMapper{
		models: map[string]*ModelEntry{
			"gpt-5": {ActualName: "mimo-v2.5-pro", Capabilities: []string{"function_call", "streaming"}},
		},
	}
	channelClient := &recordingChannelClient{
		failModels:  map[string]error{"gpt-5": errors.New("no available channel")},
		channelName: "mimo-channel",
	}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, mapper, nil)
	plan, err := uc.Plan(context.Background(), RelayRequest{Token: "demo-token", Model: "gpt-5"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.ResolvedModel != "mimo-v2.5-pro" {
		t.Fatalf("resolved model = %q, want mimo-v2.5-pro", plan.ResolvedModel)
	}
	if plan.Channel.Name != "mimo-channel" {
		t.Fatalf("channel name = %q, want mimo-channel", plan.Channel.Name)
	}
	wantModels := []string{"gpt-5", "mimo-v2.5-pro"}
	if len(channelClient.models) != len(wantModels) {
		t.Fatalf("selected models = %v, want %v", channelClient.models, wantModels)
	}
	for i, want := range wantModels {
		if channelClient.models[i] != want {
			t.Fatalf("selected models = %v, want %v", channelClient.models, wantModels)
		}
	}
}

func TestRelayUsecase_ResolveModel(t *testing.T) {
	mapper := &ModelMapper{
		models: map[string]*ModelEntry{
			"gpt-4o": {ActualName: "gpt-4o-2024-08-06"},
		},
	}
	uc := NewRelayUsecase(testIdentityClient{}, testChannelClient{}, mapper, nil)
	if got := uc.ResolveModel("gpt-4o"); got != "gpt-4o-2024-08-06" {
		t.Fatalf("expected gpt-4o-2024-08-06, got %s", got)
	}
	if got := uc.ResolveModel("unknown"); got != "unknown" {
		t.Fatalf("expected unknown, got %s", got)
	}
}

func TestRelayUsecase_ResolveModel_NilMapper(t *testing.T) {
	uc := NewRelayUsecase(testIdentityClient{}, testChannelClient{}, nil, nil)
	if got := uc.ResolveModel("gpt-4o"); got != "gpt-4o" {
		t.Fatalf("expected gpt-4o, got %s", got)
	}
}

func TestRelayUsecase_HasCapability(t *testing.T) {
	mapper := &ModelMapper{
		models: map[string]*ModelEntry{
			"gpt-4o": {ActualName: "gpt-4o-2024-08-06", Capabilities: []string{"function_call", "streaming"}},
		},
	}
	uc := NewRelayUsecase(testIdentityClient{}, testChannelClient{}, mapper, nil)
	if !uc.HasCapability("gpt-4o", "streaming") {
		t.Fatal("expected streaming capability")
	}
	if uc.HasCapability("gpt-4o", "vision") {
		t.Fatal("unexpected vision capability")
	}
}

type testIdentityClientAllowAll struct{}

func (testIdentityClientAllowAll) GetAuthSnapshot(_ context.Context, _ string) (*AuthSnapshot, error) {
	return &AuthSnapshot{
		UserID:        1,
		TokenID:       1,
		Group:         "default",
		AllowedModels: []string{},
		UserEnabled:   true,
		TokenEnabled:  true,
	}, nil
}

func TestRelayUsecase_NewRetryExecutor(t *testing.T) {
	uc := NewRelayUsecase(testIdentityClient{}, testChannelClient{}, nil, nil)
	exec := uc.NewRetryExecutor()
	if exec == nil {
		t.Fatal("expected non-nil RetryExecutor")
	}
}
