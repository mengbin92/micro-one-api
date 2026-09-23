package biz

import (
	"context"
	"github.com/go-kratos/kratos/v3/errors"
	channelv1 "micro-one-api/api/channel/v1"
)

var ErrCredentialConflict = errors.Conflict(channelv1.CredentialErrorReason_CREDENTIAL_REVISION_CONFLICT.String(), "credential revision conflict; reload authorization")

func (uc *ChannelUsecase) StoreSubscriptionCredentials(ctx context.Context, account *SubscriptionAccount) error {
	return uc.repo.StoreSubscriptionCredentials(ctx, account)
}
