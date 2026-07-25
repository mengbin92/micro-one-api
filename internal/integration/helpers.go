package integration

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	channeltestutil "micro-one-api/app/channel/testutil"

	identitytestutil "micro-one-api/app/identity/testutil"
)

func init() {
	// Allow connections to localhost for testing (mock upstream servers)
	os.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
}

// setupInMemoryIdentityService starts an in-memory identity service for testing
func setupInMemoryIdentityService(t *testing.T, addr string) (func(), identityv1.IdentityServiceClient) {
	repo := &testIdentityRepo{
		tokens: map[string]*identitytestutil.Token{
			"test-token": {
				ID:             1,
				UserID:         1,
				Key:            "test-token",
				Status:         identitytestutil.TokenStatusEnabled,
				ExpiredAt:      time.Now().Add(time.Hour).Unix(),
				RemainQuota:    1000,
				UnlimitedQuota: false,
				Models:         []string{},
			},
			"restricted-token": {
				ID:             2,
				UserID:         2,
				Key:            "restricted-token",
				Status:         identitytestutil.TokenStatusEnabled,
				ExpiredAt:      time.Now().Add(time.Hour).Unix(),
				RemainQuota:    1000,
				UnlimitedQuota: false,
				Models:         []string{"gpt-4o-mini"},
			},
			"expired-token": {
				ID:             3,
				UserID:         3,
				Key:            "expired-token",
				Status:         identitytestutil.TokenStatusEnabled,
				ExpiredAt:      time.Now().Add(-time.Hour).Unix(),
				RemainQuota:    1000,
				UnlimitedQuota: false,
				Models:         []string{},
			},
			"disabled-token": {
				ID:             4,
				UserID:         4,
				Key:            "disabled-token",
				Status:         identitytestutil.TokenStatusDisabled,
				ExpiredAt:      time.Now().Add(time.Hour).Unix(),
				RemainQuota:    1000,
				UnlimitedQuota: false,
				Models:         []string{},
			},
		},
		users: map[int64]*identitytestutil.User{
			1: {
				ID:       1,
				Username: "test-user",
				Group:    "default",
				Status:   identitytestutil.UserStatusEnabled,
			},
			2: {
				ID:       2,
				Username: "restricted-user",
				Group:    "premium",
				Status:   identitytestutil.UserStatusEnabled,
			},
			3: {
				ID:       3,
				Username: "expired-user",
				Group:    "default",
				Status:   identitytestutil.UserStatusEnabled,
			},
			4: {
				ID:       4,
				Username: "disabled-user",
				Group:    "default",
				Status:   identitytestutil.UserStatusEnabled,
			},
			5: {
				ID:       5,
				Username: "disabled-user",
				Group:    "default",
				Status:   identitytestutil.UserStatusDisabled,
			},
		},
	}

	uc := identitytestutil.NewIdentityUsecase(repo)
	svc := identitytestutil.NewIdentityService(uc)

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
		channels: map[int64]*channeltestutil.Channel{
			1: {
				ID:       1,
				Type:     1,
				Name:     "mock-channel-1",
				Status:   channeltestutil.ChannelStatusEnabled,
				BaseURL:  "http://localhost:9999",
				Group:    "default",
				Models:   []string{"gpt-4o-mini", "gpt-4o"},
				Priority: 10,
				Key:      "mock-api-key-1",
			},
		},
		abilities: map[string][]channeltestutil.Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: true, Priority: 10},
			},
			"default:gpt-4o": {
				{Group: "default", Model: "gpt-4o", ChannelID: 1, Enabled: true, Priority: 10},
			},
		},
	}

	uc := channeltestutil.NewChannelUsecase(repo, nil)
	svc := channeltestutil.NewChannelService(uc)

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
	tokens          map[string]*identitytestutil.Token
	users           map[int64]*identitytestutil.User
	oauthIdentities map[string]*identitytestutil.OAuthIdentity
}

func (m *testIdentityRepo) FindTokenByKey(ctx context.Context, key string) (*identitytestutil.Token, error) {
	token, ok := m.tokens[key]
	if !ok {
		return nil, identitytestutil.ErrTokenNotFound
	}
	return token, nil
}

func (m *testIdentityRepo) FindUserByID(ctx context.Context, userID int64) (*identitytestutil.User, error) {
	user, ok := m.users[userID]
	if !ok {
		return nil, identitytestutil.ErrUserNotFound
	}
	return user, nil
}

func (m *testIdentityRepo) FindUserByUsername(ctx context.Context, username string) (*identitytestutil.User, error) {
	for _, u := range m.users {
		if u.Username == username {
			return u, nil
		}
	}
	return nil, identitytestutil.ErrUserNotFound
}

func (m *testIdentityRepo) FindUserByEmail(ctx context.Context, email string) (*identitytestutil.User, error) {
	for _, u := range m.users {
		if u.Email == email {
			return u, nil
		}
	}
	return nil, identitytestutil.ErrUserNotFound
}

func (m *testIdentityRepo) FindUserByAffCode(ctx context.Context, affCode string) (*identitytestutil.User, error) {
	for _, u := range m.users {
		if u.AffCode == affCode {
			return u, nil
		}
	}
	return nil, identitytestutil.ErrUserNotFound
}

func (m *testIdentityRepo) FindUserByOAuth(ctx context.Context, provider, oauthID string) (*identitytestutil.User, error) {
	for _, u := range m.users {
		if u.OAuthProvider == provider && u.OAuthID == oauthID {
			return u, nil
		}
	}
	return nil, identitytestutil.ErrOAuthUserNotFound
}

func (m *testIdentityRepo) FindOAuthIdentity(ctx context.Context, provider, providerID string) (*identitytestutil.OAuthIdentity, error) {
	identity, ok := m.oauthIdentities[provider+":"+providerID]
	if !ok {
		return nil, identitytestutil.ErrOAuthUserNotFound
	}
	return identity, nil
}

func (m *testIdentityRepo) FindOAuthIdentityByUserProvider(ctx context.Context, userID int64, provider string) (*identitytestutil.OAuthIdentity, error) {
	for _, identity := range m.oauthIdentities {
		if identity.UserID == userID && identity.Provider == provider {
			return identity, nil
		}
	}
	return nil, identitytestutil.ErrOAuthUserNotFound
}

func (m *testIdentityRepo) CreateOAuthIdentity(ctx context.Context, identity *identitytestutil.OAuthIdentity) error {
	if m.oauthIdentities == nil {
		m.oauthIdentities = map[string]*identitytestutil.OAuthIdentity{}
	}
	key := identity.Provider + ":" + identity.ProviderID
	if _, ok := m.oauthIdentities[key]; ok {
		return identitytestutil.ErrOAuthAlreadyBound
	}
	for _, existing := range m.oauthIdentities {
		if existing.UserID == identity.UserID && existing.Provider == identity.Provider {
			return identitytestutil.ErrOAuthAlreadyBound
		}
	}
	identity.ID = int64(len(m.oauthIdentities) + 1)
	m.oauthIdentities[key] = identity
	return nil
}

func (m *testIdentityRepo) CreateUser(ctx context.Context, user *identitytestutil.User) error {
	user.ID = int64(len(m.users) + 1)
	m.users[user.ID] = user
	return nil
}

func (m *testIdentityRepo) UpdateUser(ctx context.Context, user *identitytestutil.User) error {
	m.users[user.ID] = user
	return nil
}

func (m *testIdentityRepo) DeleteUser(ctx context.Context, userID int64) error {
	delete(m.users, userID)
	return nil
}

func (m *testIdentityRepo) IncreaseUserBalance(ctx context.Context, userID int64, amount int64) error {
	user, ok := m.users[userID]
	if !ok {
		return identitytestutil.ErrUserNotFound
	}
	user.Balance += amount
	return nil
}

func (m *testIdentityRepo) CreateToken(ctx context.Context, token *identitytestutil.Token) error {
	token.ID = int64(len(m.tokens) + 1)
	m.tokens[token.Key] = token
	return nil
}

func (m *testIdentityRepo) FindTokenByID(ctx context.Context, userID, tokenID int64) (*identitytestutil.Token, error) {
	for _, token := range m.tokens {
		if token.ID == tokenID && token.UserID == userID {
			return token, nil
		}
	}
	return nil, identitytestutil.ErrTokenNotFound
}

func (m *testIdentityRepo) ListTokens(ctx context.Context, userID int64, page, pageSize int32, keyword string) ([]*identitytestutil.Token, int64, error) {
	var result []*identitytestutil.Token
	for _, token := range m.tokens {
		if token.UserID == userID {
			result = append(result, token)
		}
	}
	return result, int64(len(result)), nil
}

func (m *testIdentityRepo) UpdateToken(ctx context.Context, token *identitytestutil.Token) error {
	for key, existing := range m.tokens {
		if existing.ID == token.ID && existing.UserID == token.UserID {
			if key != token.Key {
				delete(m.tokens, key)
			}
			m.tokens[token.Key] = token
			return nil
		}
	}
	return identitytestutil.ErrTokenNotFound
}

func (m *testIdentityRepo) DeleteToken(ctx context.Context, userID, tokenID int64) error {
	for key, token := range m.tokens {
		if token.ID == tokenID && token.UserID == userID {
			delete(m.tokens, key)
			return nil
		}
	}
	return identitytestutil.ErrTokenNotFound
}

func (m *testIdentityRepo) ListUsers(ctx context.Context, page, pageSize int32, keyword, group string, status int32) ([]*identitytestutil.User, int64, error) {
	var result []*identitytestutil.User
	for _, u := range m.users {
		result = append(result, u)
	}
	return result, int64(len(result)), nil
}

func (m *testIdentityRepo) CountUsers(ctx context.Context) (int64, error) {
	return int64(len(m.users)), nil
}

type testChannelRepo struct {
	channels  map[int64]*channeltestutil.Channel
	abilities map[string][]channeltestutil.Ability
}

func (m *testChannelRepo) FindByID(ctx context.Context, channelID int64) (*channeltestutil.Channel, error) {
	channel, ok := m.channels[channelID]
	if !ok {
		return nil, channeltestutil.ErrChannelNotFound
	}
	return channel, nil
}

func (m *testChannelRepo) ListAbilitiesByGroupAndModel(ctx context.Context, group, model string) ([]channeltestutil.Ability, error) {
	key := group + ":" + model
	abilities, ok := m.abilities[key]
	if !ok {
		return []channeltestutil.Ability{}, nil
	}
	enabled := make([]channeltestutil.Ability, 0, len(abilities))
	for _, ability := range abilities {
		if ability.Enabled {
			enabled = append(enabled, ability)
		}
	}
	return enabled, nil
}

func (m *testChannelRepo) ListUnrestrictedChannelsByGroup(ctx context.Context, group string) ([]*channeltestutil.Channel, error) {
	if m.channels == nil {
		return nil, nil
	}
	result := make([]*channeltestutil.Channel, 0)
	for _, ch := range m.channels {
		if ch.Status != channeltestutil.ChannelStatusEnabled || ch.RestrictModels {
			continue
		}
		for _, g := range strings.Split(ch.Group, ",") {
			if g == group {
				result = append(result, ch)
				break
			}
		}
	}
	return result, nil
}

func (m *testChannelRepo) FindSubscriptionAccountByID(ctx context.Context, accountID int64) (*channeltestutil.SubscriptionAccount, error) {
	return nil, channeltestutil.ErrSubscriptionAccountNotFound
}

func (m *testChannelRepo) ListSubscriptionAccountAbilities(ctx context.Context, group, model, platform string) ([]channeltestutil.SubscriptionAccountAbility, error) {
	return nil, nil
}

func (m *testChannelRepo) ListSubscriptionAccounts(ctx context.Context, page, pageSize int32, keyword, group string, status int32, platform string) ([]*channeltestutil.SubscriptionAccount, int64, error) {
	return nil, 0, nil
}

func (m *testChannelRepo) ListOAuthRefreshCandidates(ctx context.Context, within time.Duration) ([]int64, error) {
	return nil, nil
}

func (m *testChannelRepo) CreateSubscriptionAccount(ctx context.Context, account *channeltestutil.SubscriptionAccount) error {
	return nil
}

func (m *testChannelRepo) UpdateSubscriptionAccount(ctx context.Context, account *channeltestutil.SubscriptionAccount) error {
	return channeltestutil.ErrSubscriptionAccountNotFound
}

func (m *testChannelRepo) DeleteSubscriptionAccount(ctx context.Context, accountID int64) error {
	return nil
}

func (m *testChannelRepo) ChangeSubscriptionAccountStatus(ctx context.Context, accountID int64, status int32) error {
	return channeltestutil.ErrSubscriptionAccountNotFound
}

func (m *testChannelRepo) SetSubscriptionAccountError(ctx context.Context, accountID int64, message string) error {
	return channeltestutil.ErrSubscriptionAccountNotFound
}

func (m *testChannelRepo) SetTempUnschedulable(ctx context.Context, accountID int64, until time.Time, reason string) error {
	return channeltestutil.ErrSubscriptionAccountNotFound
}

func (m *testChannelRepo) ClearTempUnschedulable(ctx context.Context, accountID int64) error {
	return channeltestutil.ErrSubscriptionAccountNotFound
}

func (m *testChannelRepo) RecordAccountQuotaSnapshot(ctx context.Context, snapshot *channeltestutil.AccountQuotaSnapshot) error {
	return channeltestutil.ErrSubscriptionAccountNotFound
}

func (m *testChannelRepo) GetAccountQuotaSnapshot(ctx context.Context, accountID int64) (*channeltestutil.AccountQuotaSnapshot, error) {
	return nil, channeltestutil.ErrSubscriptionAccountNotFound
}

func (m *testChannelRepo) RecordSubscriptionAccountQuotaUsage(ctx context.Context, usage channeltestutil.SubscriptionAccountQuotaUsage) error {
	return channeltestutil.ErrSubscriptionAccountNotFound
}

func (m *testChannelRepo) AggregateSubscriptionAccountQuotaEvents(ctx context.Context, filter channeltestutil.SubscriptionAccountQuotaEventFilter) ([]*channeltestutil.SubscriptionAccountQuotaEventAggregate, error) {
	return []*channeltestutil.SubscriptionAccountQuotaEventAggregate{}, nil
}

func (m *testChannelRepo) ResetSubscriptionAccountQuota(ctx context.Context, accountID int64, scope string) error {
	return channeltestutil.ErrSubscriptionAccountNotFound
}

func (m *testChannelRepo) AutoPauseAccount(ctx context.Context, accountID int64, reason string) error {
	return channeltestutil.ErrSubscriptionAccountNotFound
}

func (m *testChannelRepo) ClearRecoveryMetadata(ctx context.Context, accountID int64) error {
	return channeltestutil.ErrSubscriptionAccountNotFound
}

func (m *testChannelRepo) RecordQuotaResetRun(ctx context.Context, run *channeltestutil.SubscriptionAccountQuotaResetRun) error {
	return channeltestutil.ErrQuotaResetRunDuplicate
}

func (m *testChannelRepo) StampQuotaAlertMetadata(ctx context.Context, accountID int64, kind string, alertAt int64) error {
	return nil
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

func (m *testChannelRepo) ListChannels(ctx context.Context, page, pageSize int32, keyword, group string, status, chType int32) ([]*channeltestutil.Channel, int64, error) {
	var result []*channeltestutil.Channel
	for _, ch := range m.channels {
		result = append(result, ch)
	}
	return result, int64(len(result)), nil
}

func (m *testChannelRepo) CreateChannel(ctx context.Context, channel *channeltestutil.Channel) error {
	channel.ID = int64(len(m.channels) + 1)
	m.channels[channel.ID] = channel
	return nil
}

func (m *testChannelRepo) UpdateChannel(ctx context.Context, channel *channeltestutil.Channel) error {
	m.channels[channel.ID] = channel
	return nil
}

func (m *testChannelRepo) RecordUsage(ctx context.Context, channelID int64, quota int64) error {
	if ch, ok := m.channels[channelID]; ok {
		ch.UsedQuota += quota
	}
	return nil
}

func (m *testChannelRepo) RecordHealth(ctx context.Context, event channeltestutil.ChannelHealthEvent, threshold int32, cooldown time.Duration) (*channeltestutil.Channel, error) {
	ch, ok := m.channels[event.ChannelID]
	if !ok {
		return nil, channeltestutil.ErrChannelNotFound
	}
	if event.Success {
		ch.HealthStatus = channeltestutil.ChannelHealthHealthy
		ch.HealthLastError = ""
		ch.HealthConsecutiveFailures = 0
		ch.CircuitOpenedUntil = 0
	} else {
		ch.HealthLastError = event.Error
		ch.HealthConsecutiveFailures++
		if ch.HealthConsecutiveFailures >= threshold {
			ch.HealthStatus = channeltestutil.ChannelHealthUnavailable
			ch.CircuitOpenedUntil = event.CheckedAt.Add(cooldown).Unix()
		} else {
			ch.HealthStatus = channeltestutil.ChannelHealthDegraded
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

func (m *testChannelRepo) ClearRecoveryMarkers(ctx context.Context, accountID int64, clearTemp, clearError, clearMeta bool) error {
	return channeltestutil.ErrSubscriptionAccountNotFound
}
