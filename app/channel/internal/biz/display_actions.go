package biz

import (
	"context"
	"micro-one-api/domain/authorization"
)

var channelDisplayActions = []struct{ point, operation string }{
	{"channel.channels.read", "channel.channel.read"},
	{"channel.channels.update", "channel.channel.update"},
	{"channel.channels.update", "channel.channel.enable"},
	{"channel.channels.update", "channel.channel.disable"},
	{"channel.channels.delete", "channel.channel.delete"},
	{"channel.channels.test", "channel.channel.test"},
	{"channel.channels.balance", "channel.channel.balance.refresh"},
	{"channel.channels.secret", "channel.channel.secret.rotate"},
}
var accountDisplayActions = []struct{ point, operation string }{
	{"channel.accounts.read", "channel.account.read"},
	{"channel.accounts.update", "channel.account.update"},
	{"channel.accounts.update", "channel.account.enable"},
	{"channel.accounts.update", "channel.account.disable"},
	{"channel.accounts.delete", "channel.account.delete"},
	{"channel.accounts.quota", "channel.account.quota.reset"},
	{"channel.accounts.credential", "channel.account.credential.update"},
	{"channel.accounts.oauth", "channel.account.oauth.bind"},
}

func (uc *ChannelUsecase) prepareDisplayActions(ctx context.Context, account bool) (context.Context, error) {
	entries := channelDisplayActions
	if account {
		entries = accountDisplayActions
	}
	for _, entry := range entries {
		var err error
		ctx, err = uc.authorizeOptional(ctx, entry.point, entry.operation)
		if err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}
func (uc *ChannelUsecase) displayActions(ctx context.Context, id int64, account bool) ([]string, error) {
	entries := channelDisplayActions
	if account {
		entries = accountDisplayActions
	}
	if _, iam := authorization.QueryScopeFromContext(ctx, entries[0].operation); !iam {
		return nil, nil
	}
	facts, err := uc.facts(ctx, id, account)
	if err != nil {
		return nil, err
	}
	actions := []string{}
	for _, entry := range entries {
		if authorization.Require(ctx, entry.operation, facts) == nil {
			actions = append(actions, entry.operation)
		}
	}
	return actions, nil
}
func (uc *ChannelUsecase) displayAccounts(ctx context.Context, rows []*SubscriptionAccount) ([]*SubscriptionAccount, error) {
	ctx, err := uc.prepareDisplayActions(ctx, true)
	if err != nil {
		return nil, err
	}
	rows = redactAccountMappings(ctx, rows)
	for i, row := range rows {
		if row == nil {
			continue
		}
		copy := *row
		copy.PermittedActions, err = uc.displayActions(ctx, row.ID, true)
		if err != nil {
			return nil, err
		}
		rows[i] = &copy
	}
	return rows, nil
}
