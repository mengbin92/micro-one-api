package biz

import (
	"context"
	"testing"
	"time"
)

type mockChannelRepo struct {
	channels  map[int64]*Channel
	abilities map[string][]Ability
}

func (m *mockChannelRepo) FindByID(ctx context.Context, channelID int64) (*Channel, error) {
	channel, ok := m.channels[channelID]
	if !ok {
		return nil, ErrChannelNotFound
	}
	return channel, nil
}

func (m *mockChannelRepo) ListAbilitiesByGroupAndModel(ctx context.Context, group, model string) ([]Ability, error) {
	key := group + ":" + model
	abilities, ok := m.abilities[key]
	if !ok {
		return []Ability{}, nil
	}
	enabled := make([]Ability, 0, len(abilities))
	for _, ability := range abilities {
		if ability.Enabled {
			enabled = append(enabled, ability)
		}
	}
	return enabled, nil
}

func (m *mockChannelRepo) ListAvailableModels(ctx context.Context, group string) ([]string, error) {
	uniqueModels := make(map[string]bool)
	for key, abilities := range m.abilities {
		if len(key) > len(group)+1 && key[:len(group)+1] == group+":" {
			for _, ability := range abilities {
				if ability.Enabled {
					uniqueModels[ability.Model] = true
				}
			}
		}
	}
	models := make([]string, 0, len(uniqueModels))
	for model := range uniqueModels {
		models = append(models, model)
	}
	return models, nil
}

func (m *mockChannelRepo) ListChannels(ctx context.Context, page, pageSize int32, keyword, group string, status, chType int32) ([]*Channel, int64, error) {
	var result []*Channel
	for _, ch := range m.channels {
		result = append(result, ch)
	}
	return result, int64(len(result)), nil
}

func (m *mockChannelRepo) CreateChannel(ctx context.Context, channel *Channel) error {
	channel.ID = int64(len(m.channels) + 1)
	m.channels[channel.ID] = channel
	return nil
}

func (m *mockChannelRepo) UpdateChannel(ctx context.Context, channel *Channel) error {
	m.channels[channel.ID] = channel
	return nil
}

func (m *mockChannelRepo) RecordUsage(ctx context.Context, channelID int64, quota int64) error {
	if ch, ok := m.channels[channelID]; ok {
		ch.UsedQuota += quota
	}
	return nil
}

func (m *mockChannelRepo) RecordHealth(ctx context.Context, event ChannelHealthEvent, threshold int32, cooldown time.Duration) (*Channel, error) {
	ch, ok := m.channels[event.ChannelID]
	if !ok {
		return nil, ErrChannelNotFound
	}
	if event.Success {
		ch.HealthStatus = ChannelHealthHealthy
		ch.HealthLastError = ""
		ch.HealthConsecutiveFailures = 0
		ch.CircuitOpenedUntil = 0
	} else {
		ch.HealthLastError = event.Error
		ch.HealthConsecutiveFailures++
		if ch.HealthConsecutiveFailures >= threshold {
			ch.HealthStatus = ChannelHealthUnavailable
			ch.CircuitOpenedUntil = event.CheckedAt.Add(cooldown).Unix()
		} else {
			ch.HealthStatus = ChannelHealthDegraded
		}
	}
	return ch, nil
}

func (m *mockChannelRepo) DeleteChannel(ctx context.Context, channelID int64) error {
	delete(m.channels, channelID)
	return nil
}

func (m *mockChannelRepo) ChangeStatus(ctx context.Context, channelID int64, status int32) error {
	if ch, ok := m.channels[channelID]; ok {
		ch.Status = status
	}
	return nil
}

func TestChannelUsecase_SelectChannel_SingleChannel(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {
				ID:       1,
				Type:     1,
				Name:     "test-channel",
				Status:   ChannelStatusEnabled,
				BaseURL:  "https://api.openai.com/v1",
				Group:    "default",
				Models:   []string{"gpt-4o-mini"},
				Priority: 10,
			},
		},
		abilities: map[string][]Ability{
			"default:gpt-4o-mini": {
				{
					Group:     "default",
					Model:     "gpt-4o-mini",
					ChannelID: 1,
					Enabled:   true,
					Priority:  10,
				},
			},
		},
	}

	uc := NewChannelUsecase(repo, nil)
	channel, err := uc.SelectChannel(context.Background(), "default", "gpt-4o-mini", false)
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	if channel.ID != 1 {
		t.Fatalf("unexpected channel ID: %d", channel.ID)
	}
}

func TestChannelUsecase_SelectChannel_NoAvailableChannels(t *testing.T) {
	repo := &mockChannelRepo{
		channels:  map[int64]*Channel{},
		abilities: map[string][]Ability{},
	}

	uc := NewChannelUsecase(repo, nil)
	_, err := uc.SelectChannel(context.Background(), "default", "gpt-4o-mini", false)
	if err != ErrChannelNotFound {
		t.Fatalf("expected ErrChannelNotFound, got: %v", err)
	}
}

func TestChannelUsecase_SelectChannel_PriorityOrdering(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {ID: 1, Name: "channel-1", Status: ChannelStatusEnabled, Priority: 10},
			2: {ID: 2, Name: "channel-2", Status: ChannelStatusEnabled, Priority: 20},
			3: {ID: 3, Name: "channel-3", Status: ChannelStatusEnabled, Priority: 5},
		},
		abilities: map[string][]Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: true, Priority: 10},
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 2, Enabled: true, Priority: 20},
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 3, Enabled: true, Priority: 5},
			},
		},
	}

	uc := NewChannelUsecase(repo, nil)
	channel, err := uc.SelectChannel(context.Background(), "default", "gpt-4o-mini", false)
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	if channel.ID != 2 {
		t.Fatalf("expected highest priority channel (ID=2), got ID=%d", channel.ID)
	}
}

func TestChannelUsecase_SelectChannel_SkipsOpenCircuit(t *testing.T) {
	now := time.Unix(1000, 0)
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {ID: 1, Name: "open-circuit", Status: ChannelStatusEnabled, Priority: 20, HealthStatus: ChannelHealthUnavailable, CircuitOpenedUntil: now.Add(time.Minute).Unix()},
			2: {ID: 2, Name: "healthy", Status: ChannelStatusEnabled, Priority: 10, HealthStatus: ChannelHealthHealthy},
		},
		abilities: map[string][]Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: true, Priority: 20},
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 2, Enabled: true, Priority: 10},
			},
		},
	}

	uc := NewChannelUsecase(repo, nil)
	uc.now = func() time.Time { return now }
	channel, err := uc.SelectChannel(context.Background(), "default", "gpt-4o-mini", false)
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	if channel.ID != 2 {
		t.Fatalf("expected healthy fallback channel, got ID=%d", channel.ID)
	}
}

func TestChannelUsecase_SelectChannel_AllowsHalfOpenAfterCooldown(t *testing.T) {
	now := time.Unix(1000, 0)
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {ID: 1, Name: "cooldown-ended", Status: ChannelStatusEnabled, Priority: 20, HealthStatus: ChannelHealthUnavailable, CircuitOpenedUntil: now.Add(-time.Second).Unix()},
			2: {ID: 2, Name: "healthy", Status: ChannelStatusEnabled, Priority: 10, HealthStatus: ChannelHealthHealthy},
		},
		abilities: map[string][]Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: true, Priority: 20},
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 2, Enabled: true, Priority: 10},
			},
		},
	}

	uc := NewChannelUsecase(repo, nil)
	uc.now = func() time.Time { return now }
	channel, err := uc.SelectChannel(context.Background(), "default", "gpt-4o-mini", false)
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	if channel.ID != 1 {
		t.Fatalf("expected half-open channel, got ID=%d", channel.ID)
	}
}

func TestChannelUsecase_SelectChannel_SamePriorityRandom(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {ID: 1, Name: "channel-1", Status: ChannelStatusEnabled, Priority: 10},
			2: {ID: 2, Name: "channel-2", Status: ChannelStatusEnabled, Priority: 10},
		},
		abilities: map[string][]Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: true, Priority: 10},
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 2, Enabled: true, Priority: 10},
			},
		},
	}

	uc := NewChannelUsecase(repo, nil)
	results := make(map[int64]int)

	for i := 0; i < 50; i++ {
		channel, err := uc.SelectChannel(context.Background(), "default", "gpt-4o-mini", false)
		if err != nil {
			t.Fatalf("SelectChannel() error = %v", err)
		}
		results[channel.ID]++
	}

	if len(results) != 2 {
		t.Fatalf("expected both channels to be selected, got: %v", results)
	}
	if results[1] == 0 || results[2] == 0 {
		t.Fatalf("expected both channels to be selected multiple times, got: %v", results)
	}
}

func TestChannelUsecase_SelectChannel_ExcludeFirstPriority(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {ID: 1, Name: "channel-1", Status: ChannelStatusEnabled, Priority: 10},
			2: {ID: 2, Name: "channel-2", Status: ChannelStatusEnabled, Priority: 10},
			3: {ID: 3, Name: "channel-3", Status: ChannelStatusEnabled, Priority: 5},
		},
		abilities: map[string][]Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: true, Priority: 10},
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 2, Enabled: true, Priority: 10},
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 3, Enabled: true, Priority: 5},
			},
		},
	}

	uc := NewChannelUsecase(repo, nil)

	channel, err := uc.SelectChannel(context.Background(), "default", "gpt-4o-mini", true)
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	if channel.ID == 1 || channel.ID == 2 {
		t.Fatalf("expected lower priority channel, got ID=%d", channel.ID)
	}
	if channel.ID != 3 {
		t.Fatalf("expected channel-3 (ID=3), got ID=%d", channel.ID)
	}
}

func TestChannelUsecase_SelectChannel_FilterDisabled(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {ID: 1, Name: "channel-1", Status: ChannelStatusEnabled, Priority: 10},
			2: {ID: 2, Name: "channel-2", Status: ChannelStatusEnabled, Priority: 20},
		},
		abilities: map[string][]Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: true, Priority: 10},
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 2, Enabled: false, Priority: 20},
			},
		},
	}

	uc := NewChannelUsecase(repo, nil)
	channel, err := uc.SelectChannel(context.Background(), "default", "gpt-4o-mini", false)
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	if channel.ID != 1 {
		t.Fatalf("expected enabled channel (ID=1), got ID=%d", channel.ID)
	}
}

func TestChannelUsecase_SelectChannel_AllDisabled(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {ID: 1, Name: "channel-1", Status: ChannelStatusEnabled, Priority: 10},
		},
		abilities: map[string][]Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: false, Priority: 10},
			},
		},
	}

	uc := NewChannelUsecase(repo, nil)
	_, err := uc.SelectChannel(context.Background(), "default", "gpt-4o-mini", false)
	if err != ErrChannelNotFound {
		t.Fatalf("expected ErrChannelNotFound, got: %v", err)
	}
}

func TestChannelUsecase_GetChannel(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {
				ID:       1,
				Type:     1,
				Name:     "test-channel",
				Status:   ChannelStatusEnabled,
				BaseURL:  "https://api.openai.com/v1",
				Group:    "default",
				Models:   []string{"gpt-4o-mini"},
				Priority: 10,
				Key:      "sk-test",
			},
		},
		abilities: map[string][]Ability{},
	}

	uc := NewChannelUsecase(repo, nil)
	channel, err := uc.GetChannel(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetChannel() error = %v", err)
	}
	if channel.ID != 1 {
		t.Fatalf("unexpected channel ID: %d", channel.ID)
	}
	if channel.Name != "test-channel" {
		t.Fatalf("unexpected channel name: %s", channel.Name)
	}
	if channel.Key != "sk-test" {
		t.Fatalf("unexpected channel key: %s", channel.Key)
	}
}

func TestChannelUsecase_GetChannel_NotFound(t *testing.T) {
	repo := &mockChannelRepo{
		channels:  map[int64]*Channel{},
		abilities: map[string][]Ability{},
	}

	uc := NewChannelUsecase(repo, nil)
	_, err := uc.GetChannel(context.Background(), 999)
	if err != ErrChannelNotFound {
		t.Fatalf("expected ErrChannelNotFound, got: %v", err)
	}
}

func TestChannelUsecase_ListAvailableModels(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {ID: 1, Name: "channel-1", Status: ChannelStatusEnabled},
			2: {ID: 2, Name: "channel-2", Status: ChannelStatusEnabled},
		},
		abilities: map[string][]Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: true, Priority: 10},
			},
			"default:gpt-4o": {
				{Group: "default", Model: "gpt-4o", ChannelID: 1, Enabled: true, Priority: 10},
				{Group: "default", Model: "gpt-4o", ChannelID: 2, Enabled: true, Priority: 10},
			},
			"premium:gpt-4o": {
				{Group: "premium", Model: "gpt-4o", ChannelID: 1, Enabled: true, Priority: 10},
			},
		},
	}

	uc := NewChannelUsecase(repo, nil)
	models, err := uc.ListAvailableModels(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListAvailableModels() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got: %d", len(models))
	}

	modelSet := make(map[string]bool)
	for _, model := range models {
		modelSet[model] = true
	}
	if !modelSet["gpt-4o-mini"] || !modelSet["gpt-4o"] {
		t.Fatalf("expected gpt-4o-mini and gpt-4o, got: %v", models)
	}
}

func TestChannelUsecase_ListAvailableModels_NoChannels(t *testing.T) {
	repo := &mockChannelRepo{
		channels:  map[int64]*Channel{},
		abilities: map[string][]Ability{},
	}

	uc := NewChannelUsecase(repo, nil)
	models, err := uc.ListAvailableModels(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListAvailableModels() error = %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("expected no models, got: %v", models)
	}
}

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"empty string", "", []string{}},
		{"single item", "gpt-4o-mini", []string{"gpt-4o-mini"}},
		{"multiple items", "gpt-4o-mini,gpt-4o", []string{"gpt-4o-mini", "gpt-4o"}},
		{"items with spaces", "gpt-4o-mini, gpt-4o, gpt-3.5-turbo", []string{"gpt-4o-mini", "gpt-4o", "gpt-3.5-turbo"}},
		{"items with extra spaces", "  gpt-4o-mini  ,  gpt-4o  ", []string{"gpt-4o-mini", "gpt-4o"}},
		{"empty items", "gpt-4o-mini,,gpt-4o", []string{"gpt-4o-mini", "gpt-4o"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SplitCSV(tt.input)
			if !equalStringSlices(result, tt.expected) {
				t.Fatalf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestDecodeChannelConfig(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected ChannelConfig
	}{
		{"empty string", "", ChannelConfig{}},
		{"valid json", `{"APIVersion":"v1","Region":"us-east-1"}`, ChannelConfig{APIVersion: "v1", Region: "us-east-1"}},
		{"invalid json", `{invalid json}`, ChannelConfig{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DecodeChannelConfig(tt.input)
			if result != tt.expected {
				t.Fatalf("expected %+v, got %+v", tt.expected, result)
			}
		})
	}
}

func TestChannel_ModelsCSV(t *testing.T) {
	tests := []struct {
		name     string
		models   []string
		expected string
	}{
		{"empty", []string{}, ""},
		{"single model", []string{"gpt-4o-mini"}, "gpt-4o-mini"},
		{"multiple models", []string{"gpt-4o-mini", "gpt-4o"}, "gpt-4o-mini,gpt-4o"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &Channel{Models: tt.models}
			result := channel.ModelsCSV()
			if result != tt.expected {
				t.Fatalf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
