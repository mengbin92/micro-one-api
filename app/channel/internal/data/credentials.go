package data

import (
	"context"
	"micro-one-api/app/channel/internal/biz"
)

// StoreSubscriptionCredentials updates only secrets, leaving routing/quotas untouched.
// Every full account update also advances this revision, fencing stale writers.
func (r *Repository) StoreSubscriptionCredentials(ctx context.Context, a *biz.SubscriptionAccount) error {
	if r.db == nil {
		r.lock.Lock()
		defer r.lock.Unlock()
		old, ok := r.subAccounts[a.ID]
		if !ok {
			return biz.ErrSubscriptionAccountNotFound
		}
		if old.CredentialRevision != a.CredentialRevision {
			if sameCredentialReplay(old, a) {
				a.CredentialRevision = old.CredentialRevision
				return nil
			}
			return biz.ErrCredentialConflict
		}
		cp := *old
		cp.AccessToken = a.AccessToken
		cp.RefreshToken = a.RefreshToken
		cp.ExpiresAt = a.ExpiresAt
		cp.AccountID = a.AccountID
		cp.CredentialRevision++
		r.subAccounts[a.ID] = &cp
		a.CredentialRevision = cp.CredentialRevision
		return nil
	}
	model, err := r.subscriptionAccountBizToModel(a)
	if err != nil {
		return err
	}
	result := r.db.WithContext(ctx).Model(&subscriptionAccountModel{}).Where("id = ? AND credential_revision = ?", a.ID, a.CredentialRevision).Updates(map[string]any{
		"access_token": model.AccessToken, "refresh_token": model.RefreshToken, "expires_at": a.ExpiresAt, "account_id": a.AccountID, "credential_revision": a.CredentialRevision + 1,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		a.CredentialRevision++
		return nil
	}
	old, err := r.FindSubscriptionAccountByID(ctx, a.ID)
	if err != nil {
		return err
	}
	// A lost RPC response is safe to retry, even with randomized encryption.
	if sameCredentialReplay(old, a) {
		a.CredentialRevision = old.CredentialRevision
		return nil
	}
	return biz.ErrCredentialConflict
}
func sameCredentialReplay(old, next *biz.SubscriptionAccount) bool {
	return old.CredentialRevision == next.CredentialRevision+1 && old.AccessToken == next.AccessToken && old.RefreshToken == next.RefreshToken && old.ExpiresAt == next.ExpiresAt && old.AccountID == next.AccountID
}
