package data

import (
	"context"
	"math"
	"testing"
	"time"

	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	relayquota "micro-one-api/internal/quota"

	"google.golang.org/grpc"
)

type subscriptionAccountStoreClient struct {
	channelv1.ChannelServiceClient
	account         *commonv1.SubscriptionAccountInfo
	secretAccount   *commonv1.SubscriptionAccountInfo
	usedSecret      bool
	clearErrorID    int64
	clearTempID     int64
	setErrorReason  string
	setTempUntil    int64
	refreshWithin   int64
	refreshAccounts []int64
	snapshotReq     *channelv1.RecordAccountQuotaSnapshotRequest
	updateCalled    bool
}

func (c *subscriptionAccountStoreClient) RecordAccountQuotaSnapshot(_ context.Context, req *channelv1.RecordAccountQuotaSnapshotRequest, _ ...grpc.CallOption) (*channelv1.RecordAccountQuotaSnapshotResponse, error) {
	c.snapshotReq = req
	return &channelv1.RecordAccountQuotaSnapshotResponse{Success: true}, nil
}

func (c *subscriptionAccountStoreClient) UpdateSubscriptionAccount(_ context.Context, _ *channelv1.UpdateSubscriptionAccountRequest, _ ...grpc.CallOption) (*channelv1.UpdateSubscriptionAccountResponse, error) {
	c.updateCalled = true
	return &channelv1.UpdateSubscriptionAccountResponse{Success: true}, nil
}

func (c *subscriptionAccountStoreClient) GetSubscriptionAccount(context.Context, *channelv1.GetSubscriptionAccountRequest, ...grpc.CallOption) (*channelv1.GetSubscriptionAccountReply, error) {
	return &channelv1.GetSubscriptionAccountReply{Account: c.account}, nil
}

func (c *subscriptionAccountStoreClient) GetSubscriptionAccountWithSecrets(context.Context, *channelv1.GetSubscriptionAccountRequest, ...grpc.CallOption) (*channelv1.GetSubscriptionAccountReply, error) {
	c.usedSecret = true
	return &channelv1.GetSubscriptionAccountReply{Account: c.secretAccount}, nil
}

func (c *subscriptionAccountStoreClient) ListOAuthRefreshCandidates(_ context.Context, req *channelv1.ListOAuthRefreshCandidatesRequest, _ ...grpc.CallOption) (*channelv1.ListOAuthRefreshCandidatesResponse, error) {
	c.refreshWithin = req.GetWithinSeconds()
	return &channelv1.ListOAuthRefreshCandidatesResponse{AccountIds: c.refreshAccounts}, nil
}

func (c *subscriptionAccountStoreClient) ClearSubscriptionAccountError(_ context.Context, req *channelv1.ClearSubscriptionAccountErrorRequest, _ ...grpc.CallOption) (*channelv1.ClearSubscriptionAccountErrorResponse, error) {
	c.clearErrorID = req.GetAccountId()
	return &channelv1.ClearSubscriptionAccountErrorResponse{Success: true}, nil
}

func (c *subscriptionAccountStoreClient) SetSubscriptionAccountError(_ context.Context, req *channelv1.SetSubscriptionAccountErrorRequest, _ ...grpc.CallOption) (*channelv1.SetSubscriptionAccountErrorResponse, error) {
	c.setErrorReason = req.GetMessage()
	return &channelv1.SetSubscriptionAccountErrorResponse{Success: true}, nil
}

func (c *subscriptionAccountStoreClient) SetTempUnschedulable(_ context.Context, req *channelv1.SetTempUnschedulableRequest, _ ...grpc.CallOption) (*channelv1.SetTempUnschedulableResponse, error) {
	c.setTempUntil = req.GetUntil()
	c.setErrorReason = req.GetReason()
	return &channelv1.SetTempUnschedulableResponse{Success: true}, nil
}

func (c *subscriptionAccountStoreClient) ClearTempUnschedulable(_ context.Context, req *channelv1.ClearTempUnschedulableRequest, _ ...grpc.CallOption) (*channelv1.ClearTempUnschedulableResponse, error) {
	c.clearTempID = req.GetAccountId()
	return &channelv1.ClearTempUnschedulableResponse{Success: true}, nil
}

func TestChannelSubscriptionAccountStoreLookupUsesSecretClient(t *testing.T) {
	client := &subscriptionAccountStoreClient{
		account:       &commonv1.SubscriptionAccountInfo{AccessToken: "****", RefreshToken: "****"},
		secretAccount: &commonv1.SubscriptionAccountInfo{Id: 11, AccountId: "upstream", AccessToken: "access", RefreshToken: "refresh", ExpiresAt: 1710000000},
	}
	store := NewChannelSubscriptionAccountStore(client)

	got, err := store.Lookup(context.Background(), 11)
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !client.usedSecret {
		t.Fatal("Lookup() did not use secret-capable channel client")
	}
	if got.AccessToken != "access" || got.RefreshToken != "refresh" || got.AccountID != "upstream" {
		t.Fatalf("unexpected credentials: %+v", got)
	}
}

func TestChannelSubscriptionAccountStoreRefreshHookRPCs(t *testing.T) {
	client := &subscriptionAccountStoreClient{refreshAccounts: []int64{1, 2}}
	store := NewChannelSubscriptionAccountStore(client)

	ids, err := store.ExpiringSoon(context.Background(), 12*time.Hour)
	if err != nil {
		t.Fatalf("ExpiringSoon() error = %v", err)
	}
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("unexpected refresh candidates: %#v", ids)
	}
	if client.refreshWithin != int64((12 * time.Hour).Seconds()) {
		t.Fatalf("within seconds = %d", client.refreshWithin)
	}

	if err := store.OnRefreshSuccess(context.Background(), 3); err != nil {
		t.Fatalf("OnRefreshSuccess() error = %v", err)
	}
	if client.clearErrorID != 3 || client.clearTempID != 3 {
		t.Fatalf("success hook did not clear error/temp state: %+v", client)
	}

	if err := store.OnRefreshNonRetryable(context.Background(), 4, "invalid_grant"); err != nil {
		t.Fatalf("OnRefreshNonRetryable() error = %v", err)
	}
	if client.setErrorReason != "invalid_grant" {
		t.Fatalf("non-retryable reason = %q", client.setErrorReason)
	}

	until := time.Unix(1710001234, 0)
	if err := store.OnRefreshRetryExhausted(context.Background(), 5, until, "timeout"); err != nil {
		t.Fatalf("OnRefreshRetryExhausted() error = %v", err)
	}
	if client.setTempUntil != until.Unix() || client.setErrorReason != "timeout" {
		t.Fatalf("retry-exhausted hook not forwarded: until=%d reason=%q", client.setTempUntil, client.setErrorReason)
	}
}

// TestChannelSubscriptionAccountStoreRecordQuotaSnapshotUsesRPC verifies the
// snapshot is persisted via the dedicated (server-side atomic) RPC and NOT the
// racy metadata read-modify-write through UpdateSubscriptionAccount.
func TestChannelSubscriptionAccountStoreRecordQuotaSnapshotUsesRPC(t *testing.T) {
	client := &subscriptionAccountStoreClient{}
	store := NewChannelSubscriptionAccountStore(client)

	primaryUsed := 87.5
	primaryWindow := 300
	updatedAt := time.Unix(1710002000, 0)
	err := store.RecordAccountQuotaSnapshot(context.Background(), 42, &relayquota.CodexSnapshot{
		PrimaryUsedPercent:   &primaryUsed,
		PrimaryWindowMinutes: &primaryWindow,
		UpdatedAt:            updatedAt,
	})
	if err != nil {
		t.Fatalf("RecordAccountQuotaSnapshot() error = %v", err)
	}
	if client.updateCalled {
		t.Fatal("snapshot must not round-trip the account metadata blob")
	}
	if client.snapshotReq == nil {
		t.Fatal("expected RecordAccountQuotaSnapshot RPC to be called")
	}
	if client.snapshotReq.GetAccountId() != 42 {
		t.Fatalf("account id = %d, want 42", client.snapshotReq.GetAccountId())
	}
	if client.snapshotReq.PrimaryUsedPercent == nil || client.snapshotReq.GetPrimaryUsedPercent() != 87.5 {
		t.Fatalf("primary used percent = %v", client.snapshotReq.PrimaryUsedPercent)
	}
	if client.snapshotReq.PrimaryWindowMinutes == nil || client.snapshotReq.GetPrimaryWindowMinutes() != 300 {
		t.Fatalf("primary window minutes = %v", client.snapshotReq.PrimaryWindowMinutes)
	}
	if client.snapshotReq.GetUpdatedAt() != updatedAt.Unix() {
		t.Fatalf("updated_at = %d, want %d", client.snapshotReq.GetUpdatedAt(), updatedAt.Unix())
	}
	// Windows the upstream didn't report must stay absent, not zero-valued.
	if client.snapshotReq.SecondaryUsedPercent != nil {
		t.Fatal("secondary used percent should be absent")
	}
}

func TestChannelSubscriptionAccountStoreRecordQuotaSnapshotRejectsOverflow(t *testing.T) {
	client := &subscriptionAccountStoreClient{}
	store := NewChannelSubscriptionAccountStore(client)

	overflow := int(math.MaxInt32) + 1
	err := store.RecordAccountQuotaSnapshot(context.Background(), 42, &relayquota.CodexSnapshot{
		PrimaryWindowMinutes: &overflow,
		UpdatedAt:            time.Unix(1710002000, 0),
	})
	if err == nil {
		t.Fatal("expected overflow error")
	}
	if client.snapshotReq != nil {
		t.Fatal("overflow snapshot must not be sent to RPC")
	}
}
