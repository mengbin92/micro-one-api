package biz

import (
	"context"
	"errors"
	"testing"

	relayprovider "micro-one-api/internal/relay/provider"
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

func (testChannelClient) RecordChannelHealth(_ context.Context, _ int64, _ bool, _ string, _ int64) error {
	return nil
}

type recordingChannelClient struct {
	models                []string
	failModels            map[string]error
	channelName           string
	subscriptionModels    []string
	subscriptionPlatforms []string
	subscription          *SubscriptionAccount
	subscriptionErr       error
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

func (c *recordingChannelClient) RecordChannelHealth(_ context.Context, _ int64, _ bool, _ string, _ int64) error {
	return nil
}

func (c *recordingChannelClient) SelectSubscriptionAccount(_ context.Context, group, model, platform string, _ bool) (*SubscriptionAccount, error) {
	c.subscriptionModels = append(c.subscriptionModels, model)
	c.subscriptionPlatforms = append(c.subscriptionPlatforms, platform)
	if c.subscriptionErr != nil {
		return nil, c.subscriptionErr
	}
	return c.subscription, nil
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

func (c testChannelClientError) RecordChannelHealth(_ context.Context, _ int64, _ bool, _ string, _ int64) error {
	return nil
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

func TestRelayUsecasePlan_SelectsSubscriptionAccountWhenNoAPIKeyChannel(t *testing.T) {
	channelClient := &recordingChannelClient{
		failModels: map[string]error{"gpt-5": errors.New("no channel available")},
		subscription: &SubscriptionAccount{
			ID:          8,
			Name:        "codex-sub",
			Platform:    "codex",
			AccountType: "oauth",
			Status:      1,
			BaseURL:     "https://chatgpt.example/backend-api/codex",
			Group:       "default",
			Models:      []string{"gpt-5"},
			Priority:    20,
			AccessToken: "access-token",
			AccountID:   "chatgpt-account",
		},
	}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, nil, nil)

	plan, err := uc.Plan(context.Background(), RelayRequest{Token: "demo-token", Model: "gpt-5"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Channel == nil || plan.Channel.Type != relayprovider.ChannelTypeCodexOAuth || plan.Channel.ID != 8 || plan.Channel.Key != "" {
		t.Fatalf("unexpected subscription channel projection: %+v", plan.Channel)
	}
	// The access token lives on the first-class Account, NOT on Channel.Key.
	if plan.Account == nil || plan.Account.ID != 8 || plan.Account.AccessToken != "access-token" || plan.Account.AccountID != "chatgpt-account" {
		t.Fatalf("unexpected subscription account: %+v", plan.Account)
	}
	if len(channelClient.subscriptionModels) != 1 || channelClient.subscriptionModels[0] != "gpt-5" {
		t.Fatalf("subscription selected models = %v", channelClient.subscriptionModels)
	}
	if len(channelClient.subscriptionPlatforms) != 1 || channelClient.subscriptionPlatforms[0] != "codex" {
		t.Fatalf("subscription selected platforms = %v", channelClient.subscriptionPlatforms)
	}
}

func TestRelayUsecasePlan_SelectsClaudeSubscriptionWithPlatformFilter(t *testing.T) {
	channelClient := &recordingChannelClient{
		failModels: map[string]error{"claude-sonnet-4-20250514": errors.New("no channel available")},
		subscription: &SubscriptionAccount{
			ID:       9,
			Name:     "claude-sub",
			Platform: "claude",
			Status:   1,
			Group:    "default",
			Models:   []string{"claude-sonnet-4-20250514"},
		},
	}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, nil, nil)

	plan, err := uc.Plan(context.Background(), RelayRequest{Token: "demo-token", Model: "claude-sonnet-4-20250514"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Channel == nil || plan.Channel.Type != relayprovider.ChannelTypeClaudeOAuth {
		t.Fatalf("unexpected subscription channel projection: %+v", plan.Channel)
	}
	if len(channelClient.subscriptionPlatforms) != 1 || channelClient.subscriptionPlatforms[0] != "claude" {
		t.Fatalf("subscription selected platforms = %v", channelClient.subscriptionPlatforms)
	}
}

func TestRelayUsecasePlan_APIKeyChannelWinsOverSubscriptionAccount(t *testing.T) {
	channelClient := &recordingChannelClient{
		subscription: &SubscriptionAccount{ID: 8, Platform: "codex"},
	}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, nil, nil)

	plan, err := uc.Plan(context.Background(), RelayRequest{Token: "demo-token", Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Channel == nil || plan.Channel.Name != "default:gpt-4o" {
		t.Fatalf("unexpected channel: %+v", plan.Channel)
	}
	if len(channelClient.subscriptionModels) != 0 {
		t.Fatalf("subscription selector should not be called, got %v", channelClient.subscriptionModels)
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
