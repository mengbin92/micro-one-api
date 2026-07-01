package data

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	channelv1 "micro-one-api/api/channel/v1"
	relaycredential "micro-one-api/internal/relay/credential"
	relayquota "micro-one-api/internal/relay/quota"
)

// ChannelSubscriptionAccountStore adapts channel-service subscription-account
// RPCs to the relay credential and server resolver interfaces.
type ChannelSubscriptionAccountStore struct {
	client channelv1.ChannelServiceClient
}

type subscriptionAccountSecretClient interface {
	GetSubscriptionAccountWithSecrets(ctx context.Context, req *channelv1.GetSubscriptionAccountRequest) (*channelv1.GetSubscriptionAccountReply, error)
}

func NewChannelSubscriptionAccountStore(client channelv1.ChannelServiceClient) *ChannelSubscriptionAccountStore {
	return &ChannelSubscriptionAccountStore{client: client}
}

func (s *ChannelSubscriptionAccountStore) Lookup(ctx context.Context, accountID int64) (*relaycredential.AccountCredentials, error) {
	if s == nil || s.client == nil {
		return nil, relaycredential.ErrNotConfigured
	}
	reply, err := s.getSubscriptionAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	account := reply.GetAccount()
	if account == nil {
		return nil, relaycredential.ErrAccountNotFound
	}
	return &relaycredential.AccountCredentials{
		AccountID:    account.GetAccountId(),
		AccessToken:  account.GetAccessToken(),
		RefreshToken: account.GetRefreshToken(),
		ExpiresAt:    time.Unix(account.GetExpiresAt(), 0),
	}, nil
}

func (s *ChannelSubscriptionAccountStore) Store(ctx context.Context, accountID int64, creds *relaycredential.AccountCredentials) error {
	if s == nil || s.client == nil {
		return relaycredential.ErrNotConfigured
	}
	req := &channelv1.UpdateSubscriptionAccountRequest{Id: accountID}
	if creds != nil {
		req.AccessToken = creds.AccessToken
		req.RefreshToken = creds.RefreshToken
		req.ExpiresAt = creds.ExpiresAt.Unix()
		req.AccountId = creds.AccountID
	}
	reply, err := s.client.UpdateSubscriptionAccount(ctx, req)
	if err != nil {
		return err
	}
	if reply != nil && !reply.GetSuccess() {
		return relaycredential.ErrRefreshFailed
	}
	return nil
}

func (s *ChannelSubscriptionAccountStore) Resolve(ctx context.Context, channelID int64) (*relaycredential.SubscriptionAccountMetadata, error) {
	if s == nil || s.client == nil {
		return nil, relaycredential.ErrNotConfigured
	}
	reply, err := s.getSubscriptionAccount(ctx, channelID)
	if err != nil {
		return nil, err
	}
	account := reply.GetAccount()
	if account == nil {
		return nil, relaycredential.ErrAccountNotFound
	}
	return &relaycredential.SubscriptionAccountMetadata{
		ID:          account.GetId(),
		AccessToken: account.GetAccessToken(),
		AccountID:   account.GetAccountId(),
		Platform:    relaycredential.Platform(account.GetPlatform()),
		AccountType: account.GetAccountType(),
		Fingerprint: account.GetFingerprint(),
		GroupID:     account.GetGroup(),
	}, nil
}

func (s *ChannelSubscriptionAccountStore) PlatformOf(ctx context.Context, accountID int64) relaycredential.Platform {
	if s == nil || s.client == nil {
		return ""
	}
	reply, err := s.getSubscriptionAccount(ctx, accountID)
	if err != nil || reply.GetAccount() == nil {
		return ""
	}
	return relaycredential.Platform(reply.GetAccount().GetPlatform())
}

var (
	_ relaycredential.AccountLookup               = (*ChannelSubscriptionAccountStore)(nil)
	_ relaycredential.SubscriptionAccountResolver = (*ChannelSubscriptionAccountStore)(nil)
)

// ExpiringSoon implements credential.ExpiringScanner. It scans subscription
// accounts via the channel-service ListSubscriptionAccounts RPC and returns
// the IDs whose access token expires within the given duration. This is the
// production counterpart of the test-only NoopAccountLookup.ExpiringSoon and
// makes the background refresh task actually do work in a real deployment
// (without it, sweep() is a no-op and only request-time refresh covers
// correctness).
//
// The scan pages through all accounts (page size 200). It does not filter by
// status/platform: expired-but-stored tokens are still refreshed by the
// provider-level Refresh, which marks the account unschedulable on failure.
func (s *ChannelSubscriptionAccountStore) ExpiringSoon(ctx context.Context, within time.Duration) ([]int64, error) {
	if s == nil || s.client == nil {
		return nil, relaycredential.ErrNotConfigured
	}
	resp, err := s.client.ListOAuthRefreshCandidates(ctx, &channelv1.ListOAuthRefreshCandidatesRequest{
		WithinSeconds: int64(within.Seconds()),
	})
	if err != nil {
		return nil, err
	}
	return resp.GetAccountIds(), nil
}

// compile-time interface check.
var _ relaycredential.ExpiringScanner = (*ChannelSubscriptionAccountStore)(nil)

func (s *ChannelSubscriptionAccountStore) OnRefreshSuccess(ctx context.Context, accountID int64) error {
	if s == nil || s.client == nil {
		return relaycredential.ErrNotConfigured
	}
	if resp, err := s.client.ClearSubscriptionAccountError(ctx, &channelv1.ClearSubscriptionAccountErrorRequest{AccountId: accountID}); err != nil {
		return err
	} else if resp != nil && !resp.GetSuccess() {
		return relaycredential.ErrRefreshFailed
	}
	if resp, err := s.client.ClearTempUnschedulable(ctx, &channelv1.ClearTempUnschedulableRequest{AccountId: accountID}); err != nil {
		return err
	} else if resp != nil && !resp.GetSuccess() {
		return relaycredential.ErrRefreshFailed
	}
	return nil
}

func (s *ChannelSubscriptionAccountStore) OnRefreshNonRetryable(ctx context.Context, accountID int64, reason string) error {
	if s == nil || s.client == nil {
		return relaycredential.ErrNotConfigured
	}
	resp, err := s.client.SetSubscriptionAccountError(ctx, &channelv1.SetSubscriptionAccountErrorRequest{
		AccountId: accountID,
		Message:   reason,
	})
	if err != nil {
		return err
	}
	if resp != nil && !resp.GetSuccess() {
		return relaycredential.ErrRefreshFailed
	}
	return nil
}

func (s *ChannelSubscriptionAccountStore) OnRefreshRetryExhausted(ctx context.Context, accountID int64, until time.Time, reason string) error {
	if s == nil || s.client == nil {
		return relaycredential.ErrNotConfigured
	}
	resp, err := s.client.SetTempUnschedulable(ctx, &channelv1.SetTempUnschedulableRequest{
		AccountId: accountID,
		Until:     until.Unix(),
		Reason:    reason,
	})
	if err != nil {
		return err
	}
	if resp != nil && !resp.GetSuccess() {
		return relaycredential.ErrRefreshFailed
	}
	return nil
}

func (s *ChannelSubscriptionAccountStore) RecordAccountQuotaSnapshot(ctx context.Context, accountID int64, snapshot *relayquota.CodexSnapshot) error {
	if s == nil || s.client == nil {
		return relaycredential.ErrNotConfigured
	}
	if snapshot == nil {
		return nil
	}
	values := make(map[string]any)
	if reply, err := s.getSubscriptionAccount(ctx, accountID); err == nil && reply.GetAccount() != nil && reply.GetAccount().GetMetadata() != "" {
		_ = json.Unmarshal([]byte(reply.GetAccount().GetMetadata()), &values)
	}
	values["quota_snapshot"] = map[string]any{
		"primary_used_percent":           snapshot.PrimaryUsedPercent,
		"primary_reset_after_seconds":    snapshot.PrimaryResetAfterSeconds,
		"primary_window_minutes":         snapshot.PrimaryWindowMinutes,
		"secondary_used_percent":         snapshot.SecondaryUsedPercent,
		"secondary_reset_after_seconds":  snapshot.SecondaryResetAfterSeconds,
		"secondary_window_minutes":       snapshot.SecondaryWindowMinutes,
		"primary_over_secondary_percent": snapshot.PrimaryOverSecondaryPercent,
		"updated_at":                     snapshot.UpdatedAt.Unix(),
	}
	metadata, err := json.Marshal(values)
	if err != nil {
		return err
	}
	reply, err := s.client.UpdateSubscriptionAccount(ctx, &channelv1.UpdateSubscriptionAccountRequest{
		Id:       accountID,
		Metadata: string(metadata),
	})
	if err != nil {
		return err
	}
	if reply != nil && !reply.GetSuccess() {
		return relaycredential.ErrRefreshFailed
	}
	return nil
}

func (s *ChannelSubscriptionAccountStore) AutoPauseAccount(ctx context.Context, accountID int64, reason string) error {
	if s == nil || s.client == nil {
		return relaycredential.ErrNotConfigured
	}
	if resp, err := s.client.SetSubscriptionAccountError(ctx, &channelv1.SetSubscriptionAccountErrorRequest{
		AccountId: accountID,
		Message:   reason,
	}); err != nil {
		return err
	} else if resp != nil && !resp.GetSuccess() {
		return relaycredential.ErrRefreshFailed
	}
	resp, err := s.client.ChangeSubscriptionAccountStatus(ctx, &channelv1.ChangeSubscriptionAccountStatusRequest{
		AccountId: accountID,
		Status:    2,
	})
	if err != nil {
		return err
	}
	if resp != nil && !resp.GetSuccess() {
		return fmt.Errorf("auto-pause subscription account: %s", resp.GetMessage())
	}
	return nil
}

func (s *ChannelSubscriptionAccountStore) getSubscriptionAccount(ctx context.Context, accountID int64) (*channelv1.GetSubscriptionAccountReply, error) {
	if secretClient, ok := s.client.(subscriptionAccountSecretClient); ok {
		return secretClient.GetSubscriptionAccountWithSecrets(ctx, &channelv1.GetSubscriptionAccountRequest{AccountId: accountID})
	}
	return s.client.GetSubscriptionAccount(ctx, &channelv1.GetSubscriptionAccountRequest{AccountId: accountID})
}

var _ relaycredential.RefreshHook = (*ChannelSubscriptionAccountStore)(nil)
