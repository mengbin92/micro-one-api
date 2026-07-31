package biz

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	relayprovider "micro-one-api/domain/upstream/provider"
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

func (c testChannelClient) SelectChannelExcluding(ctx context.Context, group, model string, excluded map[int64]bool) (*Channel, error) {
	if excluded[1] {
		return nil, fmt.Errorf("no channel")
	}
	return c.SelectChannel(ctx, group, model, false)
}

func (testChannelClient) RecordSubscriptionAccountHealth(_ context.Context, _ int64, _ bool) error {
	return nil
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
	subscriptions         []*SubscriptionAccount
	subscriptionErr       error
	byID                  map[int64]*SubscriptionAccount
	getByIDErr            error
	getByIDCalls          []int64
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

func (c *recordingChannelClient) SelectChannelExcluding(ctx context.Context, group, model string, excluded map[int64]bool) (*Channel, error) {
	if excluded[1] {
		return nil, fmt.Errorf("no channel")
	}
	return c.SelectChannel(ctx, group, model, false)
}

func (c *recordingChannelClient) RecordSubscriptionAccountHealth(_ context.Context, _ int64, _ bool) error {
	return nil
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
	if len(c.subscriptions) > 0 {
		idx := len(c.subscriptionModels) - 1
		if idx >= len(c.subscriptions) {
			idx = len(c.subscriptions) - 1
		}
		return c.subscriptions[idx], nil
	}
	return c.subscription, nil
}

func (c *recordingChannelClient) GetSubscriptionAccountByID(_ context.Context, accountID int64) (*SubscriptionAccount, error) {
	c.getByIDCalls = append(c.getByIDCalls, accountID)
	if c.getByIDErr != nil {
		return nil, c.getByIDErr
	}
	if c.byID != nil {
		if a, ok := c.byID[accountID]; ok {
			return a, nil
		}
		return nil, nil
	}
	for _, a := range c.subscriptions {
		if a != nil && a.ID == accountID {
			return a, nil
		}
	}
	if c.subscription != nil && c.subscription.ID == accountID {
		return c.subscription, nil
	}
	return nil, nil
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

func (c testChannelClientError) SelectChannelExcluding(_ context.Context, _, _ string, _ map[int64]bool) (*Channel, error) {
	return nil, c.err
}

func (c testChannelClientError) RecordSubscriptionAccountHealth(_ context.Context, _ int64, _ bool) error {
	return nil
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
	mapper := NewModelMapperForTest(map[string]*ModelEntry{"gpt-4o": {ActualName: "gpt-4o-2024-08-06", Capabilities: []string{"function_call", "streaming"}}})
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
	mapper := NewModelMapperForTest(map[string]*ModelEntry{"gpt-5": {ActualName: "mimo-v2.5-pro", Capabilities: []string{"function_call", "streaming"}}})
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

func TestRelayUsecasePlan_SelectsDomesticSubscriptionPlatform(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		platform    string
		channelType int32
	}{
		{name: "zhipu", model: "GLM-5.2", platform: "zhipu", channelType: relayprovider.ChannelTypeZhipuPlan},
		{name: "minimax", model: "MiniMax-M2.5", platform: "minimax", channelType: relayprovider.ChannelTypeMinimaxPlan},
		{name: "kimi", model: "kimi-k2", platform: "kimi", channelType: relayprovider.ChannelTypeKimiOAuth},
		{name: "kimi k3", model: "k3", platform: "kimi", channelType: relayprovider.ChannelTypeKimiOAuth},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channelClient := &recordingChannelClient{
				failModels: map[string]error{tt.model: errors.New("no channel available")},
				subscription: &SubscriptionAccount{
					ID:       10,
					Name:     tt.name,
					Platform: tt.platform,
					Status:   1,
					Group:    "default",
					Models:   []string{tt.model},
				},
			}
			uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, nil, nil)

			plan, err := uc.Plan(context.Background(), RelayRequest{Token: "demo-token", Model: tt.model})
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}
			if plan.Channel == nil || plan.Channel.Type != tt.channelType {
				t.Fatalf("channel = %+v, want type %d", plan.Channel, tt.channelType)
			}
			if len(channelClient.subscriptionPlatforms) != 1 || channelClient.subscriptionPlatforms[0] != tt.platform {
				t.Fatalf("subscription selected platforms = %v, want [%s]", channelClient.subscriptionPlatforms, tt.platform)
			}
		})
	}
}

func TestRelayUsecasePlan_CustomAliasUsesAbilityPlatform(t *testing.T) {
	const model = "company-reasoner"
	channelClient := &recordingChannelClient{
		failModels: map[string]error{model: errors.New("no channel available")},
		subscription: &SubscriptionAccount{
			ID: 11, Name: "zhipu-alias", Platform: "zhipu", Status: 1,
			Group: "default", Models: []string{model},
		},
	}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, nil, nil)

	plan, err := uc.Plan(context.Background(), RelayRequest{Token: "demo-token", Model: model})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Account == nil || plan.Account.Platform != "zhipu" {
		t.Fatalf("account = %+v, want zhipu", plan.Account)
	}
	if len(channelClient.subscriptionPlatforms) != 1 || channelClient.subscriptionPlatforms[0] != "" {
		t.Fatalf("subscription selected platforms = %q, want unfiltered lookup", channelClient.subscriptionPlatforms)
	}
}

func TestRelayUsecasePlan_SkipsRuntimeBlockedSubscriptionAccount(t *testing.T) {
	channelClient := &recordingChannelClient{
		failModels: map[string]error{"gpt-5": errors.New("no channel available")},
		subscriptions: []*SubscriptionAccount{
			{ID: 8, Name: "blocked", Platform: "codex", Status: 1, Group: "default", Models: []string{"gpt-5"}},
			{ID: 9, Name: "next", Platform: "codex", Status: 1, Group: "default", Models: []string{"gpt-5"}},
		},
	}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, nil, nil)
	blocker := NewMemoryRuntimeBlocker()
	uc.SetRuntimeBlocker(blocker)
	if err := blocker.Block(context.Background(), 8, time.Now().Add(time.Minute), "upstream 500"); err != nil {
		t.Fatalf("Block() error = %v", err)
	}

	plan, err := uc.Plan(context.Background(), RelayRequest{Token: "demo-token", Model: "gpt-5"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Account == nil || plan.Account.ID != 9 {
		t.Fatalf("selected account = %+v, want id 9", plan.Account)
	}
}

func TestRelayUsecasePlan_BalancesAPIKeyAndSubscriptionAtEqualPriority(t *testing.T) {
	channelClient := &recordingChannelClient{
		subscription: &SubscriptionAccount{
			ID: 8, Name: "codex-sub", Platform: "codex", Status: 1,
			Group: "default", Models: []string{"gpt-4o"}, Priority: 0,
		},
	}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, nil, nil)

	var channelCount, subscriptionCount int
	for i := 0; i < 6; i++ {
		plan, err := uc.Plan(context.Background(), RelayRequest{Token: "demo-token", Model: "gpt-4o"})
		if err != nil {
			t.Fatalf("Plan() error = %v", err)
		}
		if plan.Account != nil {
			subscriptionCount++
		} else {
			channelCount++
		}
	}
	if channelCount != 3 || subscriptionCount != 3 {
		t.Fatalf("route counts = channel:%d subscription:%d, want 3:3", channelCount, subscriptionCount)
	}
	if len(channelClient.subscriptionModels) != 6 {
		t.Fatalf("subscription selector calls = %d, want 6", len(channelClient.subscriptionModels))
	}
}

func TestRelayUsecasePlan_HigherPriorityWinsAcrossSourceTypes(t *testing.T) {
	channelClient := &recordingChannelClient{
		subscription: &SubscriptionAccount{
			ID: 8, Platform: "codex", Status: 1, Group: "default",
			Models: []string{"gpt-4o"}, Priority: 10,
		},
	}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, nil, nil)
	for i := 0; i < 4; i++ {
		plan, err := uc.Plan(context.Background(), RelayRequest{Token: "demo-token", Model: "gpt-4o"})
		if err != nil {
			t.Fatalf("Plan() error = %v", err)
		}
		if plan.Account == nil || plan.Account.ID != 8 {
			t.Fatalf("plan = %+v, want higher-priority subscription", plan)
		}
	}
}

// --- session -> subscription-account stickiness (docs #7) ---

type fakeSessionStore struct {
	bound     map[string]int64
	lookups   int
	refreshed int
}

func sessKey(group, hash string) string { return group + "|" + hash }

func (f *fakeSessionStore) LookupSessionChannel(_ context.Context, group, hash string) int64 {
	f.lookups++
	if f.bound == nil {
		return 0
	}
	return f.bound[sessKey(group, hash)]
}

func (f *fakeSessionStore) RefreshSessionTTL(_ context.Context, _, _ string, _ time.Duration) bool {
	f.refreshed++
	return true
}

func TestRelayUsecasePlan_StickyHit_SelectsBoundAccount(t *testing.T) {
	acct := &SubscriptionAccount{ID: 9, Name: "claude-sub", Platform: "claude", Status: 1, Group: "default", Models: []string{"claude-sonnet-4-20250514"}, AccessToken: "tok"}
	channelClient := &recordingChannelClient{
		failModels: map[string]error{"claude-sonnet-4-20250514": errors.New("no channel available")},
		byID:       map[int64]*SubscriptionAccount{9: acct},
	}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, nil, nil)
	store := &fakeSessionStore{bound: map[string]int64{sessKey("default", "sess-1"): 9}}
	uc.SetSessionAccountStore(store, time.Hour, true)

	plan, err := uc.Plan(context.Background(), RelayRequest{Token: "demo-token", Model: "claude-sonnet-4-20250514", SessionHash: "sess-1"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Account == nil || plan.Account.ID != 9 || plan.Account.AccessToken != "tok" {
		t.Fatalf("account = %+v, want id 9 with token", plan.Account)
	}
	if plan.Channel == nil || plan.Channel.ID != 9 || plan.Channel.Type != relayprovider.ChannelTypeClaudeOAuth {
		t.Fatalf("channel = %+v", plan.Channel)
	}
	if len(channelClient.subscriptionModels) != 0 {
		t.Fatalf("normal selection must not run on sticky hit: %v", channelClient.subscriptionModels)
	}
	if len(channelClient.getByIDCalls) != 1 || channelClient.getByIDCalls[0] != 9 {
		t.Fatalf("getByID calls = %v, want [9]", channelClient.getByIDCalls)
	}
	if store.refreshed != 1 {
		t.Fatalf("refresh count = %d, want 1", store.refreshed)
	}
}

func TestRelayUsecasePlan_StickyHit_CustomDomesticAlias(t *testing.T) {
	const model = "company-reasoner"
	acct := &SubscriptionAccount{
		ID: 12, Name: "zhipu-alias", Platform: "zhipu", Status: 1,
		Group: "default", Models: []string{model}, AccessToken: "tok",
	}
	channelClient := &recordingChannelClient{
		failModels: map[string]error{model: errors.New("no channel available")},
		byID:       map[int64]*SubscriptionAccount{acct.ID: acct},
	}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, nil, nil)
	store := &fakeSessionStore{bound: map[string]int64{sessKey("default", "domestic-alias"): acct.ID}}
	uc.SetSessionAccountStore(store, time.Hour, true)

	plan, err := uc.Plan(context.Background(), RelayRequest{Token: "demo-token", Model: model, SessionHash: "domestic-alias"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Account == nil || plan.Account.ID != acct.ID {
		t.Fatalf("account = %+v, want sticky id %d", plan.Account, acct.ID)
	}
	if len(channelClient.subscriptionModels) != 0 {
		t.Fatalf("normal selection must not run on sticky hit: %v", channelClient.subscriptionModels)
	}
}

func TestRelayUsecasePlan_StickyMiss_FallsBackToNormal(t *testing.T) {
	channelClient := &recordingChannelClient{
		failModels:   map[string]error{"claude-sonnet-4-20250514": errors.New("no channel available")},
		subscription: &SubscriptionAccount{ID: 5, Platform: "claude", Status: 1, Group: "default", Models: []string{"claude-sonnet-4-20250514"}},
	}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, nil, nil)
	store := &fakeSessionStore{} // no bindings
	uc.SetSessionAccountStore(store, time.Hour, true)

	plan, err := uc.Plan(context.Background(), RelayRequest{Token: "demo-token", Model: "claude-sonnet-4-20250514", SessionHash: "sess-x"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Account == nil || plan.Account.ID != 5 {
		t.Fatalf("account = %+v, want id 5 (normal)", plan.Account)
	}
	if store.lookups != 1 {
		t.Fatalf("lookups = %d, want 1", store.lookups)
	}
	if len(channelClient.getByIDCalls) != 0 {
		t.Fatalf("byID must not be called on miss: %v", channelClient.getByIDCalls)
	}
}

func TestRelayUsecasePlan_StickyInvalid_GroupMismatch(t *testing.T) {
	sticky := &SubscriptionAccount{ID: 9, Platform: "claude", Status: 1, Group: "other", Models: []string{"claude-sonnet-4-20250514"}}
	normal := &SubscriptionAccount{ID: 5, Platform: "claude", Status: 1, Group: "default", Models: []string{"claude-sonnet-4-20250514"}}
	channelClient := &recordingChannelClient{
		failModels:   map[string]error{"claude-sonnet-4-20250514": errors.New("no channel available")},
		byID:         map[int64]*SubscriptionAccount{9: sticky},
		subscription: normal,
	}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, nil, nil)
	store := &fakeSessionStore{bound: map[string]int64{sessKey("default", "s"): 9}}
	uc.SetSessionAccountStore(store, time.Hour, true)

	plan, err := uc.Plan(context.Background(), RelayRequest{Token: "t", Model: "claude-sonnet-4-20250514", SessionHash: "s"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Account == nil || plan.Account.ID != 5 {
		t.Fatalf("account = %+v, want normal id 5 (cross-group binding must not leak)", plan.Account)
	}
}

func TestRelayUsecasePlan_StickyInvalid_ModelSwitch(t *testing.T) {
	sticky := &SubscriptionAccount{ID: 9, Platform: "claude", Status: 1, Group: "default", Models: []string{"claude-sonnet-4-20250514"}}
	normal := &SubscriptionAccount{ID: 5, Platform: "codex", Status: 1, Group: "default", Models: []string{"gpt-5"}}
	channelClient := &recordingChannelClient{
		failModels:   map[string]error{"gpt-5": errors.New("no channel available")},
		byID:         map[int64]*SubscriptionAccount{9: sticky},
		subscription: normal,
	}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, nil, nil)
	store := &fakeSessionStore{bound: map[string]int64{sessKey("default", "s"): 9}}
	uc.SetSessionAccountStore(store, time.Hour, true)

	// Session bound to a claude account, but this turn asks for a gpt model:
	// platform no longer matches, so stickiness must be skipped.
	plan, err := uc.Plan(context.Background(), RelayRequest{Token: "t", Model: "gpt-5", SessionHash: "s"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Account == nil || plan.Account.ID != 5 {
		t.Fatalf("account = %+v, want id 5 (normal codex)", plan.Account)
	}
}

func TestRelayUsecasePlan_StickyInvalid_RuntimeBlocked(t *testing.T) {
	sticky := &SubscriptionAccount{ID: 9, Platform: "claude", Status: 1, Group: "default", Models: []string{"claude-sonnet-4-20250514"}}
	normal := &SubscriptionAccount{ID: 5, Platform: "claude", Status: 1, Group: "default", Models: []string{"claude-sonnet-4-20250514"}}
	channelClient := &recordingChannelClient{
		failModels:   map[string]error{"claude-sonnet-4-20250514": errors.New("no channel available")},
		byID:         map[int64]*SubscriptionAccount{9: sticky},
		subscription: normal,
	}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, nil, nil)
	blocker := NewMemoryRuntimeBlocker()
	uc.SetRuntimeBlocker(blocker)
	if err := blocker.Block(context.Background(), 9, time.Now().Add(time.Minute), "upstream 500"); err != nil {
		t.Fatalf("Block() error = %v", err)
	}
	store := &fakeSessionStore{bound: map[string]int64{sessKey("default", "s"): 9}}
	uc.SetSessionAccountStore(store, time.Hour, true)

	plan, err := uc.Plan(context.Background(), RelayRequest{Token: "t", Model: "claude-sonnet-4-20250514", SessionHash: "s"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Account == nil || plan.Account.ID != 5 {
		t.Fatalf("account = %+v, want id 5 (blocked sticky account skipped)", plan.Account)
	}
}

func TestRelayUsecasePlan_StickyDisabled_NoLookup(t *testing.T) {
	channelClient := &recordingChannelClient{
		failModels:   map[string]error{"claude-sonnet-4-20250514": errors.New("no channel available")},
		subscription: &SubscriptionAccount{ID: 5, Platform: "claude", Status: 1, Group: "default", Models: []string{"claude-sonnet-4-20250514"}},
		byID:         map[int64]*SubscriptionAccount{9: {ID: 9, Platform: "claude", Status: 1, Group: "default", Models: []string{"claude-sonnet-4-20250514"}}},
	}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, nil, nil)
	store := &fakeSessionStore{bound: map[string]int64{sessKey("default", "s"): 9}}
	uc.SetSessionAccountStore(store, time.Hour, false) // disabled

	plan, err := uc.Plan(context.Background(), RelayRequest{Token: "t", Model: "claude-sonnet-4-20250514", SessionHash: "s"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Account == nil || plan.Account.ID != 5 {
		t.Fatalf("account = %+v, want normal id 5", plan.Account)
	}
	if store.lookups != 0 {
		t.Fatalf("lookups = %d, want 0 when disabled", store.lookups)
	}
}

func TestRelayPlan_BaseModel(t *testing.T) {
	tests := []struct {
		name string
		plan *RelayPlan
		want string
	}{
		{name: "global model wins", plan: &RelayPlan{GlobalModel: " global ", ResolvedModel: "mapped"}, want: "global"},
		{name: "legacy plan falls back to resolved", plan: &RelayPlan{ResolvedModel: " mapped "}, want: "mapped"},
		{name: "nil plan", plan: nil, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.plan.BaseModel(); got != tt.want {
				t.Fatalf("BaseModel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRelayUsecase_ResolveModel(t *testing.T) {
	mapper := NewModelMapperForTest(map[string]*ModelEntry{"gpt-4o": {ActualName: "gpt-4o-2024-08-06"}})
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
	mapper := NewModelMapperForTest(map[string]*ModelEntry{"gpt-4o": {ActualName: "gpt-4o-2024-08-06", Capabilities: []string{"function_call", "streaming"}}})
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

// ---------------------------------------------------------------------------
// P1 (#4) — wildcard keys in per-account / per-channel model mapping.
// ---------------------------------------------------------------------------

func TestApplyPerAccountModelMapping_WildcardPattern(t *testing.T) {
	mapping := `{"claude-*":"claude-upstream","gpt-4o":"gpt-4o-2024-08-06"}`
	if got := applyPerAccountModelMapping(mapping, "claude-sonnet-4"); got != "claude-upstream" {
		t.Errorf("claude-sonnet-4 = %s, want claude-upstream", got)
	}
	if got := applyPerAccountModelMapping(mapping, "claude-3-5-sonnet"); got != "claude-upstream" {
		t.Errorf("claude-3-5-sonnet = %s, want claude-upstream", got)
	}
	// Exact match still works.
	if got := applyPerAccountModelMapping(mapping, "gpt-4o"); got != "gpt-4o-2024-08-06" {
		t.Errorf("gpt-4o = %s, want gpt-4o-2024-08-06", got)
	}
	// Non-matching passthrough.
	if got := applyPerAccountModelMapping(mapping, "llama-3"); got != "llama-3" {
		t.Errorf("llama-3 = %s, want passthrough", got)
	}
}

func TestApplyPerAccountModelMapping_CatchAll(t *testing.T) {
	mapping := `{"claude-*":"claude-family","*":"default-upstream"}`
	if got := applyPerAccountModelMapping(mapping, "claude-sonnet-4"); got != "claude-family" {
		t.Errorf("claude-sonnet-4 = %s, want claude-family", got)
	}
	if got := applyPerAccountModelMapping(mapping, "gpt-4o"); got != "default-upstream" {
		t.Errorf("gpt-4o = %s, want default-upstream", got)
	}
}

func TestApplyPerAccountModelMapping_ExactBeatsWildcard(t *testing.T) {
	mapping := `{"claude-*":"family","claude-sonnet-4":"exact-sonnet"}`
	if got := applyPerAccountModelMapping(mapping, "claude-sonnet-4"); got != "exact-sonnet" {
		t.Errorf("exact must win: claude-sonnet-4 = %s, want exact-sonnet", got)
	}
	if got := applyPerAccountModelMapping(mapping, "claude-opus-4"); got != "family" {
		t.Errorf("claude-opus-4 = %s, want family", got)
	}
}

func TestResolveChannelModel_PreservesSelectedUpstreamCase(t *testing.T) {
	tests := []struct {
		name    string
		channel *Channel
		model   string
		want    string
	}{
		{
			name:    "configured model spelling",
			channel: &Channel{Models: []string{"GLM-5.2"}},
			model:   "glm-5.2",
			want:    "GLM-5.2",
		},
		{
			name:    "explicit mapping remains authoritative",
			channel: &Channel{Models: []string{"GLM-5.2"}, ModelMapping: `{"glm-5.2":"vendor/glm-5.2"}`},
			model:   "glm-5.2",
			want:    "vendor/glm-5.2",
		},
		{
			name:    "wildcard is not an upstream identifier",
			channel: &Channel{Models: []string{"glm-*"}},
			model:   "glm-5.2",
			want:    "glm-5.2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveChannelModel(tt.channel, tt.model); got != tt.want {
				t.Fatalf("ResolveChannelModel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRelayUsecasePlan_UsesSelectedChannelModelCase(t *testing.T) {
	channelClient := &caseSensitiveModelChannelClient{}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, nil, nil)

	plan, err := uc.Plan(context.Background(), RelayRequest{Token: "tok", Model: "glm-5.2"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.ResolvedModel != "GLM-5.2" {
		t.Fatalf("ResolvedModel = %q, want selected upstream spelling %q", plan.ResolvedModel, "GLM-5.2")
	}
}

type caseSensitiveModelChannelClient struct{}

func (*caseSensitiveModelChannelClient) SelectChannel(context.Context, string, string, bool) (*Channel, error) {
	return &Channel{ID: 1, Models: []string{"GLM-5.2"}}, nil
}

func (*caseSensitiveModelChannelClient) SelectChannelExcluding(_ context.Context, _, _ string, excluded map[int64]bool) (*Channel, error) {
	if excluded[1] {
		return nil, fmt.Errorf("no channel")
	}
	return &Channel{ID: 1, Models: []string{"GLM-5.2"}}, nil
}

func (*caseSensitiveModelChannelClient) RecordChannelHealth(context.Context, int64, bool, string, int64) error {
	return nil
}

func (*caseSensitiveModelChannelClient) RecordSubscriptionAccountHealth(context.Context, int64, bool) error {
	return nil
}

// TestSelectSubscriptionFailover_AppliesFailoverAccountModelMapping proves the
// 🔴#7 fix: when failover selects a different account (B), the returned
// ResolvedModel must be recomputed against B's model mapping, NOT carried
// over from A's mapping. Pre-fix the failover plan returned the caller's
// resolvedModel verbatim, so B received A's mapped model upstream.
func TestSelectSubscriptionFailover_AppliesFailoverAccountModelMapping(t *testing.T) {
	channelClient := &recordingChannelClient{
		failModels: map[string]error{"gpt-5": errors.New("no channel available")},
		subscriptions: []*SubscriptionAccount{
			// Account A (failed, excluded).
			{ID: 1, Name: "A", Platform: "codex", Status: 1, Group: "default",
				Models: []string{"gpt-5"}, ModelMapping: `{"gpt-5":"a-mapped"}`},
			// Account B (failover target).
			{ID: 2, Name: "B", Platform: "codex", Status: 1, Group: "default",
				Models: []string{"gpt-5"}, ModelMapping: `{"gpt-5":"b-mapped"}`},
		},
	}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, nil, nil)

	plan, err := uc.SelectSubscriptionFailover(
		context.Background(), "default", "gpt-5", "gpt-5",
		map[int64]bool{1: true}, // account A failed
	)
	if err != nil {
		t.Fatalf("SelectSubscriptionFailover() error = %v", err)
	}
	if plan.Account == nil || plan.Account.ID != 2 {
		t.Fatalf("failover must select account 2 (B), got %+v", plan.Account)
	}
	if plan.ResolvedModel != "b-mapped" {
		t.Fatalf("failover ResolvedModel must use B's mapping (b-mapped), got %q", plan.ResolvedModel)
	}
	// P1 review #4: GlobalModel must be the pre-channel-mapping name so the
	// server-layer failover closure can recompute against a different
	// channel/account without stacking A's mapping onto B's lookup.
	if plan.GlobalModel != "gpt-5" {
		t.Fatalf("failover GlobalModel must be the globally-resolved name (gpt-5), got %q", plan.GlobalModel)
	}
}

// TestRelayUsecase_Plan_StampsGlobalModel (P1 review #4): Plan() must set
// GlobalModel to the globally-resolved model on every return path so server-
// layer retry closures can recompute the upstream model per-channel instead
// of stacking mappings (feeding plan.ResolvedModel, which already carries the
// first channel's mapping, into ApplyChannelModelMapping on retry).
func TestRelayUsecase_Plan_StampsGlobalModel(t *testing.T) {
	channelClient := &recordingChannelClient{
		failModels: map[string]error{"gpt-5": errors.New("no channel available")},
		subscription: &SubscriptionAccount{
			ID: 1, Name: "ch", Platform: "codex", Status: 1, Group: "default",
			Models: []string{"gpt-5"}, ModelMapping: `{"gpt-5":"ch-a-mapped"}`,
		},
	}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, channelClient, nil, nil)

	plan, err := uc.Plan(context.Background(), RelayRequest{Token: "tok", Model: "gpt-5"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.GlobalModel != "gpt-5" {
		t.Fatalf("Plan() GlobalModel must be the globally-resolved model (gpt-5), got %q", plan.GlobalModel)
	}
	if plan.ResolvedModel != "ch-a-mapped" {
		t.Fatalf("Plan() ResolvedModel must apply the channel's mapping, got %q", plan.ResolvedModel)
	}
}

type fallbackRoutingClient struct {
	channel             *Channel
	channelErr          error
	subscription        *SubscriptionAccount
	subscriptionErr     error
	channelModels       []string
	channelExcludeFirst []bool
	channelExcludedSeen []map[int64]bool
}

func (c *fallbackRoutingClient) SelectChannel(_ context.Context, _ string, model string, excludeFirstPriority bool) (*Channel, error) {
	c.channelModels = append(c.channelModels, model)
	c.channelExcludeFirst = append(c.channelExcludeFirst, excludeFirstPriority)
	return c.channel, c.channelErr
}

func (c *fallbackRoutingClient) SelectChannelExcluding(_ context.Context, _ string, model string, excluded map[int64]bool) (*Channel, error) {
	c.channelModels = append(c.channelModels, model)
	c.channelExcludedSeen = append(c.channelExcludedSeen, excluded)
	if c.channel != nil && excluded[c.channel.ID] {
		return nil, errors.New("channel excluded")
	}
	return c.channel, c.channelErr
}

func (*fallbackRoutingClient) RecordChannelHealth(context.Context, int64, bool, string, int64) error {
	return nil
}

func (*fallbackRoutingClient) RecordSubscriptionAccountHealth(context.Context, int64, bool) error {
	return nil
}

func (c *fallbackRoutingClient) SelectSubscriptionAccount(context.Context, string, string, string, bool) (*SubscriptionAccount, error) {
	return c.subscription, c.subscriptionErr
}

func (c *fallbackRoutingClient) GetSubscriptionAccountByID(_ context.Context, accountID int64) (*SubscriptionAccount, error) {
	if c.subscription != nil && c.subscription.ID == accountID {
		return c.subscription, nil
	}
	return nil, nil
}

func TestSelectFallbackRoutingSourceCrossesSourceNamespaces(t *testing.T) {
	t.Run("failed channel can fall back to subscription with same id", func(t *testing.T) {
		client := &fallbackRoutingClient{
			channel: &Channel{ID: 7, Priority: 10},
			subscription: &SubscriptionAccount{
				ID: 7, Name: "subscription", Platform: "codex", Status: 1, Group: "default",
				Models: []string{"gpt-4o"}, Priority: 10,
			},
		}
		uc := NewRelayUsecase(nil, client, nil, nil)
		got, err := uc.SelectFallbackRoutingSource(context.Background(), "default", "gpt-4o", "gpt-4o", map[RoutingSourceIdentity]bool{
			{Kind: UpstreamRouteChannel, ID: 7}: true,
		})
		if err != nil {
			t.Fatalf("SelectFallbackRoutingSource() error = %v", err)
		}
		if identity := RoutingSourceIdentityForChannel(got); identity.Kind != UpstreamRouteSubscription || identity.ID != 7 {
			t.Fatalf("selected source = %s:%d, want subscription:7", identity.Kind.String(), identity.ID)
		}
	})

	t.Run("failed subscription does not exclude ordinary channel with same id", func(t *testing.T) {
		client := &fallbackRoutingClient{
			channel: &Channel{ID: 7, Priority: 10},
			subscription: &SubscriptionAccount{
				ID: 7, Name: "subscription", Platform: "codex", Status: 1, Group: "default",
				Models: []string{"gpt-4o"}, Priority: 10,
			},
		}
		uc := NewRelayUsecase(nil, client, nil, nil)
		got, err := uc.SelectFallbackRoutingSource(context.Background(), "default", "gpt-4o", "gpt-4o", map[RoutingSourceIdentity]bool{
			{Kind: UpstreamRouteSubscription, ID: 7}: true,
		})
		if err != nil {
			t.Fatalf("SelectFallbackRoutingSource() error = %v", err)
		}
		if identity := RoutingSourceIdentityForChannel(got); identity.Kind != UpstreamRouteChannel || identity.ID != 7 {
			t.Fatalf("selected source = %s:%d, want channel:7", identity.Kind.String(), identity.ID)
		}
	})

	t.Run("excluded subscription is not selected again", func(t *testing.T) {
		client := &fallbackRoutingClient{
			channelErr: errors.New("no ordinary channel"),
			subscription: &SubscriptionAccount{
				ID: 7, Name: "subscription", Platform: "codex", Status: 1, Group: "default",
				Models: []string{"gpt-4o"}, Priority: 10,
			},
		}
		uc := NewRelayUsecase(nil, client, nil, nil)
		got, err := uc.SelectFallbackRoutingSource(context.Background(), "default", "gpt-4o", "gpt-4o", map[RoutingSourceIdentity]bool{
			{Kind: UpstreamRouteSubscription, ID: 7}: true,
		})
		if err == nil || got != nil {
			t.Fatalf("SelectFallbackRoutingSource() = %+v, %v; want no excluded source", got, err)
		}
	})
}

// TestSelectFallbackRoutingSource_PassesExcludedChannelsToSelection is the
// regression test for the v0.11.0 failover bug: the request-scoped failed
// channel set must reach selection as a filter (not a post-hoc nil-out), so a
// re-returned failed channel is rejected inside selection and lower tiers stay
// reachable.
func TestSelectFallbackRoutingSource_PassesExcludedChannelsToSelection(t *testing.T) {
	client := &fallbackRoutingClient{
		channel: &Channel{ID: 5, Priority: 10},
	}
	uc := NewRelayUsecase(nil, client, nil, nil)
	_, err := uc.SelectFallbackRoutingSource(context.Background(), "default", "gpt-4o", "gpt-4o", map[RoutingSourceIdentity]bool{
		{Kind: UpstreamRouteChannel, ID: 5}: true,
	})
	if err == nil {
		t.Fatal("expected error when the only channel is excluded")
	}
	if len(client.channelExcludedSeen) == 0 {
		t.Fatal("expected selection to go through SelectChannelExcluding")
	}
	if !client.channelExcludedSeen[0][5] {
		t.Fatalf("excluded set passed to selection = %#v, want channel 5 excluded", client.channelExcludedSeen[0])
	}
}

func TestSelectFallbackRoutingSourceAppliesCrossSourcePriorityAndWeight(t *testing.T) {
	t.Run("higher priority wins", func(t *testing.T) {
		client := &fallbackRoutingClient{
			channel: &Channel{ID: 1, Priority: 10, Weight: 100},
			subscription: &SubscriptionAccount{
				ID: 2, Name: "subscription", Platform: "codex", Status: 1, Group: "default",
				Models: []string{"gpt-4o"}, Priority: 20, Weight: 1,
			},
		}
		uc := NewRelayUsecase(nil, client, nil, nil)
		got, err := uc.SelectFallbackRoutingSource(context.Background(), "default", "gpt-4o", "gpt-4o", nil)
		if err != nil {
			t.Fatalf("SelectFallbackRoutingSource() error = %v", err)
		}
		if identity := RoutingSourceIdentityForChannel(got); identity.Kind != UpstreamRouteSubscription || identity.ID != 2 {
			t.Fatalf("selected source = %s:%d, want subscription:2", identity.Kind.String(), identity.ID)
		}
	})

	t.Run("same tier uses configured weights", func(t *testing.T) {
		client := &fallbackRoutingClient{
			channel: &Channel{ID: 1, Priority: 10, Weight: 3},
			subscription: &SubscriptionAccount{
				ID: 2, Name: "subscription", Platform: "codex", Status: 1, Group: "default",
				Models: []string{"gpt-4o"}, Priority: 10, Weight: 1,
			},
		}
		uc := NewRelayUsecase(nil, client, nil, nil)
		counts := map[UpstreamRouteKind]int{}
		for i := 0; i < 40; i++ {
			got, err := uc.SelectFallbackRoutingSource(context.Background(), "default", "gpt-4o", "gpt-4o", nil)
			if err != nil {
				t.Fatalf("iteration %d: SelectFallbackRoutingSource() error = %v", i, err)
			}
			counts[RoutingSourceIdentityForChannel(got).Kind]++
		}
		if counts[UpstreamRouteChannel] != 30 || counts[UpstreamRouteSubscription] != 10 {
			t.Fatalf("weighted counts = %#v, want channel:30 subscription:10", counts)
		}
	})
}

// TestApplyPerAccountModelMapping_MostSpecificWildcard (🟡#3): when both
// "claude-*" and "claude-sonnet-*" match "claude-sonnet-4", the more
// specific mapping wins, deterministically across repeated calls.
func TestApplyPerAccountModelMapping_MostSpecificWildcard(t *testing.T) {
	mapping := `{"claude-*":"claude-family","claude-sonnet-*":"claude-sonnet-family"}`
	for i := 0; i < 16; i++ {
		if got := applyPerAccountModelMapping(mapping, "claude-sonnet-4"); got != "claude-sonnet-family" {
			t.Fatalf("iter %d: claude-sonnet-4 = %s, want claude-sonnet-family", i, got)
		}
		if got := applyPerAccountModelMapping(mapping, "claude-opus-4"); got != "claude-family" {
			t.Fatalf("iter %d: claude-opus-4 = %s, want claude-family", i, got)
		}
	}
}
