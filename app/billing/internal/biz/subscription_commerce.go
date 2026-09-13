package biz

import (
	"context"
	"fmt"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"strconv"
	"time"
)

type SubscriptionCommerceRequest struct {
	UserID             int64
	RequestID          string
	PlanID             int64
	FromSubscriptionID int64
	ChangePolicy       string
}
type SubscriptionCommerceResult struct {
	Subscription *subscriptionbiz.UserSubscription
	Change       *subscriptionbiz.ChangeResult
	Balance      int64
}
type SubscriptionCommerceRepo interface {
	Execute(context.Context, int64, string, string, func(context.Context, subscriptionbiz.Tx) (*SubscriptionCommerceResult, error)) (*SubscriptionCommerceResult, error)
}
type SubscriptionCommercePlanReader interface {
	GetPlanByIDInTx(context.Context, subscriptionbiz.Tx, int64) (*subscriptionbiz.SubscriptionPlan, error)
}
type SubscriptionCommerce struct {
	billing       *BillingUsecase
	subscriptions *subscriptionbiz.SubscriptionUsecase
	plans         SubscriptionCommercePlanReader
	repo          SubscriptionCommerceRepo
}

func NewSubscriptionCommerce(b *BillingUsecase, s *subscriptionbiz.SubscriptionUsecase, p SubscriptionCommercePlanReader, r SubscriptionCommerceRepo) *SubscriptionCommerce {
	return &SubscriptionCommerce{b, s, p, r}
}
func (uc *SubscriptionCommerce) Execute(ctx context.Context, req SubscriptionCommerceRequest) (*SubscriptionCommerceResult, error) {
	if !subscriptionbiz.EntitlementsEnabled() || req.UserID <= 0 || req.PlanID <= 0 || req.RequestID == "" || len(req.RequestID) > 128 {
		return nil, ErrRoutingContextInvalid
	}
	digest, err := snapshotDigest(req)
	if err != nil {
		return nil, err
	}
	return uc.repo.Execute(ctx, req.UserID, req.RequestID, digest, func(ctx context.Context, tx subscriptionbiz.Tx) (*SubscriptionCommerceResult, error) {
		plan, err := uc.plans.GetPlanByIDInTx(ctx, tx, req.PlanID)
		if err != nil {
			return nil, err
		}
		if plan == nil || !plan.ForSale || plan.PriceQuota <= 0 || plan.Group == nil || plan.Group.Status != subscriptionbiz.SubscriptionGroupStatusEnabled {
			return nil, subscriptionbiz.ErrSubscriptionPlanNotSaleable
		}
		if plan.Contract != nil {
			if plan.Contract.Validate() != nil {
				return nil, subscriptionbiz.ErrSubscriptionContractInvalid
			}
			for _, g := range plan.Contract.Coverage {
				if _, err := uc.billing.validateSubscriptionGroup(ctx, tx, g.GroupID); err != nil {
					return nil, err
				}
			}
		}
		now := time.Now().Unix()
		result := &SubscriptionCommerceResult{}
		charge := plan.PriceQuota
		if req.FromSubscriptionID > 0 {
			old, err := uc.subscriptions.GetInTx(ctx, tx, req.FromSubscriptionID)
			if err != nil {
				return nil, err
			}
			if old.UserID != req.UserID {
				return nil, ErrRoutingContextInvalid
			}
			change := subscriptionbiz.ChangeRequest{UserID: req.UserID, FromSubscriptionID: old.ID, ToPlanID: plan.ID, ToGroupID: plan.GroupID, NewPlanName: plan.Name, OldPriceQuota: old.PricePaid, NewPriceQuota: plan.PriceQuota, Policy: req.ChangePolicy, Contract: subscriptionbiz.CloneContract(plan.Contract), Now: now}
			result.Change, err = uc.subscriptions.ChangeSubscriptionInTx(ctx, tx, change)
			if err != nil {
				return nil, err
			}
			charge = 0
			if result.Change.Applied {
				charge = max(int64(0), plan.PriceQuota-old.PricePaid)
			}
			result.Subscription, err = uc.subscriptions.GetInTx(ctx, tx, old.ID)
			if err != nil {
				return nil, err
			}
		} else {
			result.Subscription, _, err = uc.subscriptions.AssignOrExtendInTx(ctx, tx, &subscriptionbiz.AssignSubscriptionRequest{UserID: req.UserID, GroupID: plan.GroupID, SubscriptionName: plan.Name, StartsAt: now, ExpiresAt: now + int64(plan.ValidityDays)*86400, Contract: subscriptionbiz.CloneContract(plan.Contract), SourceOrder: "wallet:" + req.RequestID, PricePaid: plan.PriceQuota, LegacyPurchase: plan.Contract == nil})
			if err != nil {
				return nil, err
			}
		}
		user := strconv.FormatInt(req.UserID, 10)
		if charge > 0 {
			result.Balance, err = uc.billing.accountRepo.UpdateBalanceInTx(ctx, tx, user, -charge, LedgerTypeSubscription)
			if err != nil {
				return nil, err
			}
		} else {
			account, err := uc.billing.accountRepo.GetAccountSnapshotInTx(ctx, tx, user)
			if err != nil {
				return nil, err
			}
			result.Balance = account.Balance
		}
		ledger := &Ledger{UserID: user, Amount: -charge, BalanceAfter: result.Balance, Type: LedgerTypeSubscription, ReferenceID: strconv.FormatInt(result.Subscription.ID, 10), Remark: fmt.Sprintf("subscription plan=%d purchase/change", plan.ID), LedgerDedupeKey: "subscription-commerce:" + user + ":" + req.RequestID}
		if err := uc.billing.ledgerRepo.CreateLedgerInTx(ctx, tx, ledger); err != nil {
			return nil, err
		}
		return result, nil
	})
}
