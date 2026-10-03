package biz

import (
	"context"
	"fmt"
	"math"
	"micro-one-api/domain/authorization"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"strings"
)

// ResetBalance is an explicit compare-and-set adjustment. The wallet, delta
// ledger and owner audit commit together; a stale balance never silently wins.
func (uc *BillingUsecase) ResetBalance(ctx context.Context, user string, target, expected int64, reason, request string) (int64, error) {
	if target < 0 || request == "" || len(request) > 128 || strings.TrimSpace(reason) == "" {
		return 0, ErrRoutingContextInvalid
	}
	var err error
	ctx, err = uc.authorizeAccount(ctx, "billing.accounts.adjust", "billing.account.balance.reset", user)
	if err != nil {
		return 0, err
	}
	ctx = authorization.WithWriteReason(ctx, reason)
	runner := uc.resolveRunner()
	if runner == nil {
		return 0, ErrRequestSnapshotUnavailable
	}
	var result int64
	err = runner.RunInTx(ctx, func(ctx context.Context, tx subscriptionbiz.Tx) error {
		account, err := uc.accountRepo.GetAccountSnapshotInTx(ctx, tx, user)
		if err != nil {
			return err
		}
		facts, err := accountFacts(user)
		if err != nil {
			return err
		}
		if err = authorization.Require(ctx, "billing.account.balance.reset", facts); err != nil {
			return err
		}
		if account.Balance != expected {
			return ErrRoutingContextConflict
		}
		if account.Balance < 0 && target > math.MaxInt64+account.Balance {
			return ErrRoutingContextInvalid
		}
		delta := target - account.Balance
		if _, err = uc.accountRepo.UpdateBalanceInTx(ctx, tx, user, delta, LedgerTypeRecharge); err != nil {
			return err
		}
		actor := int64(0)
		if q, iam := authorization.QueryScopeFromContext(ctx, "billing.account.balance.reset"); iam {
			actor = q.ActorID
		}
		ledger := &Ledger{UserID: user, Amount: delta, BalanceAfter: target, Type: LedgerTypeRecharge, Remark: fmt.Sprintf("balance reset actor=%d reason=%s", actor, reason), LedgerDedupeKey: ledgerDedupeKeyFor("balance_reset", user, request)}
		if err = uc.ledgerRepo.CreateLedgerInTx(ctx, tx, ledger); err != nil {
			if isDuplicateKeyError(err) {
				return ErrDuplicateRequest
			}
			return err
		}
		result = target
		return nil
	})
	return result, err
}
