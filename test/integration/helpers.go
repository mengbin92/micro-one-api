package integration

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	channelbiz "micro-one-api/internal/channel/biz"
	channelservice "micro-one-api/internal/channel/service"
	identitybiz "micro-one-api/internal/identity/biz"
	identityservice "micro-one-api/internal/identity/service"
)

func init() {
	// Allow connections to localhost for testing (mock upstream servers)
	os.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
}

// setupInMemoryIdentityService starts an in-memory identity service for testing
func setupInMemoryIdentityService(t *testing.T, addr string) (func(), identityv1.IdentityServiceClient) {
	repo := &testIdentityRepo{
		tokens: map[string]*identitybiz.Token{
			"test-token": {
				ID:             1,
				UserID:         1,
				Key:            "test-token",
				Status:         identitybiz.TokenStatusEnabled,
				ExpiredAt:      time.Now().Add(time.Hour).Unix(),
				RemainQuota:    1000,
				UnlimitedQuota: false,
				Models:         []string{},
			},
			"restricted-token": {
				ID:             2,
				UserID:         2,
				Key:            "restricted-token",
				Status:         identitybiz.TokenStatusEnabled,
				ExpiredAt:      time.Now().Add(time.Hour).Unix(),
				RemainQuota:    1000,
				UnlimitedQuota: false,
				Models:         []string{"gpt-4o-mini"},
			},
			"expired-token": {
				ID:             3,
				UserID:         3,
				Key:            "expired-token",
				Status:         identitybiz.TokenStatusEnabled,
				ExpiredAt:      time.Now().Add(-time.Hour).Unix(),
				RemainQuota:    1000,
				UnlimitedQuota: false,
				Models:         []string{},
			},
			"disabled-token": {
				ID:             4,
				UserID:         4,
				Key:            "disabled-token",
				Status:         identitybiz.TokenStatusDisabled,
				ExpiredAt:      time.Now().Add(time.Hour).Unix(),
				RemainQuota:    1000,
				UnlimitedQuota: false,
				Models:         []string{},
			},
		},
		users: map[int64]*identitybiz.User{
			1: {
				ID:       1,
				Username: "test-user",
				Group:    "default",
				Status:   identitybiz.UserStatusEnabled,
			},
			2: {
				ID:       2,
				Username: "restricted-user",
				Group:    "premium",
				Status:   identitybiz.UserStatusEnabled,
			},
			3: {
				ID:       3,
				Username: "expired-user",
				Group:    "default",
				Status:   identitybiz.UserStatusEnabled,
			},
			4: {
				ID:       4,
				Username: "disabled-user",
				Group:    "default",
				Status:   identitybiz.UserStatusEnabled,
			},
			5: {
				ID:       5,
				Username: "disabled-user",
				Group:    "default",
				Status:   identitybiz.UserStatusDisabled,
			},
		},
	}

	uc := identitybiz.NewIdentityUsecase(repo)
	svc := identityservice.NewIdentityService(uc)

	server := grpc.NewServer()
	identityv1.RegisterIdentityServiceServer(server, svc)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	go func() {
		if err := server.Serve(lis); err != nil {
			t.Logf("identity server error: %v", err)
		}
	}()

	cleanup := func() {
		server.Stop()
		lis.Close()
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := identityv1.NewIdentityServiceClient(conn)

	return cleanup, client
}

// setupInMemoryChannelService starts an in-memory channel service for testing
func setupInMemoryChannelService(t *testing.T, addr string) (func(), channelv1.ChannelServiceClient) {
	repo := &testChannelRepo{
		channels: map[int64]*channelbiz.Channel{
			1: {
				ID:       1,
				Type:     1,
				Name:     "mock-channel-1",
				Status:   channelbiz.ChannelStatusEnabled,
				BaseURL:  "http://localhost:9999",
				Group:    "default",
				Models:   []string{"gpt-4o-mini", "gpt-4o"},
				Priority: 10,
				Key:      "mock-api-key-1",
			},
		},
		abilities: map[string][]channelbiz.Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: true, Priority: 10},
			},
			"default:gpt-4o": {
				{Group: "default", Model: "gpt-4o", ChannelID: 1, Enabled: true, Priority: 10},
			},
		},
	}

	uc := channelbiz.NewChannelUsecase(repo, nil)
	svc := channelservice.NewChannelService(uc)

	server := grpc.NewServer()
	channelv1.RegisterChannelServiceServer(server, svc)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	go func() {
		if err := server.Serve(lis); err != nil {
			t.Logf("channel server error: %v", err)
		}
	}()

	cleanup := func() {
		server.Stop()
		lis.Close()
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := channelv1.NewChannelServiceClient(conn)

	return cleanup, client
}

type testIdentityRepo struct {
	tokens          map[string]*identitybiz.Token
	users           map[int64]*identitybiz.User
	oauthIdentities map[string]*identitybiz.OAuthIdentity
}

func (m *testIdentityRepo) FindTokenByKey(ctx context.Context, key string) (*identitybiz.Token, error) {
	token, ok := m.tokens[key]
	if !ok {
		return nil, identitybiz.ErrTokenNotFound
	}
	return token, nil
}

func (m *testIdentityRepo) FindUserByID(ctx context.Context, userID int64) (*identitybiz.User, error) {
	user, ok := m.users[userID]
	if !ok {
		return nil, identitybiz.ErrUserNotFound
	}
	return user, nil
}

func (m *testIdentityRepo) FindUserByUsername(ctx context.Context, username string) (*identitybiz.User, error) {
	for _, u := range m.users {
		if u.Username == username {
			return u, nil
		}
	}
	return nil, identitybiz.ErrUserNotFound
}

func (m *testIdentityRepo) FindUserByEmail(ctx context.Context, email string) (*identitybiz.User, error) {
	for _, u := range m.users {
		if u.Email == email {
			return u, nil
		}
	}
	return nil, identitybiz.ErrUserNotFound
}

func (m *testIdentityRepo) FindUserByAffCode(ctx context.Context, affCode string) (*identitybiz.User, error) {
	for _, u := range m.users {
		if u.AffCode == affCode {
			return u, nil
		}
	}
	return nil, identitybiz.ErrUserNotFound
}

func (m *testIdentityRepo) FindUserByOAuth(ctx context.Context, provider, oauthID string) (*identitybiz.User, error) {
	for _, u := range m.users {
		if u.OAuthProvider == provider && u.OAuthID == oauthID {
			return u, nil
		}
	}
	return nil, identitybiz.ErrOAuthUserNotFound
}

func (m *testIdentityRepo) FindOAuthIdentity(ctx context.Context, provider, providerID string) (*identitybiz.OAuthIdentity, error) {
	identity, ok := m.oauthIdentities[provider+":"+providerID]
	if !ok {
		return nil, identitybiz.ErrOAuthUserNotFound
	}
	return identity, nil
}

func (m *testIdentityRepo) FindOAuthIdentityByUserProvider(ctx context.Context, userID int64, provider string) (*identitybiz.OAuthIdentity, error) {
	for _, identity := range m.oauthIdentities {
		if identity.UserID == userID && identity.Provider == provider {
			return identity, nil
		}
	}
	return nil, identitybiz.ErrOAuthUserNotFound
}

func (m *testIdentityRepo) CreateOAuthIdentity(ctx context.Context, identity *identitybiz.OAuthIdentity) error {
	if m.oauthIdentities == nil {
		m.oauthIdentities = map[string]*identitybiz.OAuthIdentity{}
	}
	key := identity.Provider + ":" + identity.ProviderID
	if _, ok := m.oauthIdentities[key]; ok {
		return identitybiz.ErrOAuthAlreadyBound
	}
	for _, existing := range m.oauthIdentities {
		if existing.UserID == identity.UserID && existing.Provider == identity.Provider {
			return identitybiz.ErrOAuthAlreadyBound
		}
	}
	identity.ID = int64(len(m.oauthIdentities) + 1)
	m.oauthIdentities[key] = identity
	return nil
}

func (m *testIdentityRepo) CreateUser(ctx context.Context, user *identitybiz.User) error {
	user.ID = int64(len(m.users) + 1)
	m.users[user.ID] = user
	return nil
}

func (m *testIdentityRepo) UpdateUser(ctx context.Context, user *identitybiz.User) error {
	m.users[user.ID] = user
	return nil
}

func (m *testIdentityRepo) DeleteUser(ctx context.Context, userID int64) error {
	delete(m.users, userID)
	return nil
}

func (m *testIdentityRepo) IncreaseUserQuota(ctx context.Context, userID int64, amount int64) error {
	user, ok := m.users[userID]
	if !ok {
		return identitybiz.ErrUserNotFound
	}
	user.Quota += amount
	return nil
}

func (m *testIdentityRepo) CreateToken(ctx context.Context, token *identitybiz.Token) error {
	token.ID = int64(len(m.tokens) + 1)
	m.tokens[token.Key] = token
	return nil
}

func (m *testIdentityRepo) FindTokenByID(ctx context.Context, userID, tokenID int64) (*identitybiz.Token, error) {
	for _, token := range m.tokens {
		if token.ID == tokenID && token.UserID == userID {
			return token, nil
		}
	}
	return nil, identitybiz.ErrTokenNotFound
}

func (m *testIdentityRepo) ListTokens(ctx context.Context, userID int64, page, pageSize int32, keyword string) ([]*identitybiz.Token, int64, error) {
	var result []*identitybiz.Token
	for _, token := range m.tokens {
		if token.UserID == userID {
			result = append(result, token)
		}
	}
	return result, int64(len(result)), nil
}

func (m *testIdentityRepo) UpdateToken(ctx context.Context, token *identitybiz.Token) error {
	for key, existing := range m.tokens {
		if existing.ID == token.ID && existing.UserID == token.UserID {
			if key != token.Key {
				delete(m.tokens, key)
			}
			m.tokens[token.Key] = token
			return nil
		}
	}
	return identitybiz.ErrTokenNotFound
}

func (m *testIdentityRepo) DeleteToken(ctx context.Context, userID, tokenID int64) error {
	for key, token := range m.tokens {
		if token.ID == tokenID && token.UserID == userID {
			delete(m.tokens, key)
			return nil
		}
	}
	return identitybiz.ErrTokenNotFound
}

func (m *testIdentityRepo) ListUsers(ctx context.Context, page, pageSize int32, keyword, group string, status int32) ([]*identitybiz.User, int64, error) {
	var result []*identitybiz.User
	for _, u := range m.users {
		result = append(result, u)
	}
	return result, int64(len(result)), nil
}

func (m *testIdentityRepo) CountUsers(ctx context.Context) (int64, error) {
	return int64(len(m.users)), nil
}

type testChannelRepo struct {
	channels  map[int64]*channelbiz.Channel
	abilities map[string][]channelbiz.Ability
}

func (m *testChannelRepo) FindByID(ctx context.Context, channelID int64) (*channelbiz.Channel, error) {
	channel, ok := m.channels[channelID]
	if !ok {
		return nil, channelbiz.ErrChannelNotFound
	}
	return channel, nil
}

func (m *testChannelRepo) ListAbilitiesByGroupAndModel(ctx context.Context, group, model string) ([]channelbiz.Ability, error) {
	key := group + ":" + model
	abilities, ok := m.abilities[key]
	if !ok {
		return []channelbiz.Ability{}, nil
	}
	enabled := make([]channelbiz.Ability, 0, len(abilities))
	for _, ability := range abilities {
		if ability.Enabled {
			enabled = append(enabled, ability)
		}
	}
	return enabled, nil
}

func (m *testChannelRepo) ListAvailableModels(ctx context.Context, group string) ([]string, error) {
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

func (m *testChannelRepo) ListChannels(ctx context.Context, page, pageSize int32, keyword, group string, status, chType int32) ([]*channelbiz.Channel, int64, error) {
	var result []*channelbiz.Channel
	for _, ch := range m.channels {
		result = append(result, ch)
	}
	return result, int64(len(result)), nil
}

func (m *testChannelRepo) CreateChannel(ctx context.Context, channel *channelbiz.Channel) error {
	channel.ID = int64(len(m.channels) + 1)
	m.channels[channel.ID] = channel
	return nil
}

func (m *testChannelRepo) UpdateChannel(ctx context.Context, channel *channelbiz.Channel) error {
	m.channels[channel.ID] = channel
	return nil
}

func (m *testChannelRepo) RecordUsage(ctx context.Context, channelID int64, quota int64) error {
	if ch, ok := m.channels[channelID]; ok {
		ch.UsedQuota += quota
	}
	return nil
}

func (m *testChannelRepo) RecordHealth(ctx context.Context, event channelbiz.ChannelHealthEvent, threshold int32, cooldown time.Duration) (*channelbiz.Channel, error) {
	ch, ok := m.channels[event.ChannelID]
	if !ok {
		return nil, channelbiz.ErrChannelNotFound
	}
	if event.Success {
		ch.HealthStatus = channelbiz.ChannelHealthHealthy
		ch.HealthLastError = ""
		ch.HealthConsecutiveFailures = 0
		ch.CircuitOpenedUntil = 0
	} else {
		ch.HealthLastError = event.Error
		ch.HealthConsecutiveFailures++
		if ch.HealthConsecutiveFailures >= threshold {
			ch.HealthStatus = channelbiz.ChannelHealthUnavailable
			ch.CircuitOpenedUntil = event.CheckedAt.Add(cooldown).Unix()
		} else {
			ch.HealthStatus = channelbiz.ChannelHealthDegraded
		}
	}
	return ch, nil
}

func (m *testChannelRepo) DeleteChannel(ctx context.Context, channelID int64) error {
	delete(m.channels, channelID)
	return nil
}

func (m *testChannelRepo) ChangeStatus(ctx context.Context, channelID int64, status int32) error {
	if ch, ok := m.channels[channelID]; ok {
		ch.Status = status
	}
	return nil
}
