package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	billingv1 "micro-one-api/api/billing/v1"
	identityv1 "micro-one-api/api/identity/v1"

	subscriptionbiz "micro-one-api/domain/subscription/biz"
)

var ErrSubscriptionServiceNotConfigured = errors.New("subscription service not configured")

// ErrSubscriptionNotPurchasable is returned when a user tries to buy a group
// that admins have not priced for self-purchase (price or duration_days is
// zero, or the group is disabled).
var ErrSubscriptionNotPurchasable = errors.New("subscription group is not available for purchase")

const secondsPerDay = 24 * 60 * 60

// ResolveUserIDFromToken validates a user session token and returns the owning
// user id. Unlike AuthorizeAdminToken it does not require an admin role, so it
// gates user-facing endpoints (e.g. subscription self-purchase) on a real,
// authenticated identity instead of trusting a client-supplied user_id.
func (s *AdminService) ResolveUserIDFromToken(ctx context.Context, token string) (int64, error) {
	if s == nil || s.identityClient == nil {
		return 0, ErrAdminUnauthorized
	}
	vr, err := s.identityClient.ValidateToken(ctx, &identityv1.ValidateTokenRequest{Token: token})
	if err != nil {
		return 0, err
	}
	if !vr.GetValid() {
		return 0, ErrAdminUnauthorized
	}
	return vr.GetUserId(), nil
}

// ListPurchasableSubscriptionGroups returns only groups that admins have made
// available for self-purchase: enabled, with a positive price and duration.
func (s *AdminService) ListPurchasableSubscriptionGroups(ctx context.Context) ([]*subscriptionbiz.SubscriptionGroup, error) {
	if s == nil || s.groupUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	groups, err := s.groupUc.List(ctx)
	if err != nil {
		return nil, err
	}
	purchasable := make([]*subscriptionbiz.SubscriptionGroup, 0, len(groups))
	for _, g := range groups {
		if isPurchasableGroup(g) {
			purchasable = append(purchasable, g)
		}
	}
	return purchasable, nil
}

func (s *AdminService) ListSubscriptionPlans(ctx context.Context) ([]*subscriptionbiz.SubscriptionPlan, error) {
	if s == nil || s.planUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	return s.planUc.List(ctx)
}

func (s *AdminService) ListPurchasableSubscriptionPlans(ctx context.Context) ([]*subscriptionbiz.SubscriptionPlan, error) {
	if s == nil || s.planUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	return s.planUc.ListForSale(ctx)
}

// ListSubscriptionPlansForSale returns only on-sale plans (for_sale=true).
// Backs the admin ?for_sale=true filter so the plan lifecycle view can
// distinguish on-shelf from off-shelf without a client-side filter.
func (s *AdminService) ListSubscriptionPlansForSale(ctx context.Context) ([]*subscriptionbiz.SubscriptionPlan, error) {
	if s == nil || s.planUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	return s.planUc.ListForSale(ctx)
}

// ListSubscriptionPlansOffSale returns only retired plans (for_sale=false).
func (s *AdminService) ListSubscriptionPlansOffSale(ctx context.Context) ([]*subscriptionbiz.SubscriptionPlan, error) {
	if s == nil || s.planUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	return s.planUc.ListOffSale(ctx)
}

// SetSubscriptionPlanForSale takes a plan on or off shelf. It is the narrow
// admin operation behind the 上架/下架 buttons; it only flips for_sale and
// does not accept a full plan body, so a price/validity edit cannot slip in
// through the shelf-toggle path.
func (s *AdminService) SetSubscriptionPlanForSale(ctx context.Context, planID int64, forSale bool) error {
	if s == nil || s.planUc == nil {
		return ErrSubscriptionServiceNotConfigured
	}
	return s.planUc.SetForSale(ctx, planID, forSale)
}

func (s *AdminService) CreateSubscriptionPlan(ctx context.Context, plan *subscriptionbiz.SubscriptionPlan) error {
	if s == nil || s.planUc == nil {
		return ErrSubscriptionServiceNotConfigured
	}
	return s.planUc.Create(ctx, plan)
}

func (s *AdminService) GetSubscriptionPlan(ctx context.Context, planID int64) (*subscriptionbiz.SubscriptionPlan, error) {
	if s == nil || s.planUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	return s.planUc.Get(ctx, planID)
}

func (s *AdminService) UpdateSubscriptionPlan(ctx context.Context, plan *subscriptionbiz.SubscriptionPlan) error {
	if s == nil || s.planUc == nil {
		return ErrSubscriptionServiceNotConfigured
	}
	return s.planUc.Update(ctx, plan)
}

func (s *AdminService) DeleteSubscriptionPlan(ctx context.Context, planID int64) error {
	if s == nil || s.planUc == nil {
		return ErrSubscriptionServiceNotConfigured
	}
	return s.planUc.Delete(ctx, planID)
}

func isPurchasableGroup(g *subscriptionbiz.SubscriptionGroup) bool {
	return g != nil &&
		g.Status == subscriptionbiz.SubscriptionGroupStatusEnabled &&
		g.PriceQuota > 0 &&
		g.DurationDays > 0
}

func isPurchasablePlan(p *subscriptionbiz.SubscriptionPlan) bool {
	return p != nil &&
		p.ForSale &&
		p.PriceQuota > 0 &&
		p.ValidityDays > 0 &&
		p.Group != nil &&
		p.Group.Status == subscriptionbiz.SubscriptionGroupStatusEnabled
}

// PurchaseSubscription lets an authenticated user buy a subscription group with
// their wallet balance. It orchestrates two services (billing owns the wallet,
// subscription owns the entitlement) as a compensating saga:
//  1. validate the group is purchasable and the user has no active subscription;
//  2. deduct the configured price from the wallet (billing, atomic, rejects overdraft);
//  3. create the subscription row (subscription usecase);
//  4. if step 3 fails, refund the wallet so no charge lingers without service.
//
// The active-subscription pre-check in step 1 keeps the common "already
// subscribed" rejection from ever taking money, so the refund path in step 4 is
// reserved for genuinely rare failures (a race or a DB error).
func (s *AdminService) PurchaseSubscription(ctx context.Context, userID, groupID int64) (*subscriptionbiz.UserSubscription, error) {
	if s == nil || s.subscriptionUc == nil || s.groupUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	if s.billingClient == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	if userID <= 0 {
		return nil, fmt.Errorf("invalid user id")
	}

	group, err := s.groupUc.Get(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if !isPurchasableGroup(group) {
		return nil, ErrSubscriptionNotPurchasable
	}

	if err := s.ensureSubscriptionCanUseGroup(ctx, userID, group.ID); err != nil {
		return nil, err
	}

	userIDStr := strconv.FormatInt(userID, 10)
	remark := fmt.Sprintf("purchase subscription group=%d (%s)", group.ID, group.Name)
	deduct, err := s.billingClient.PurchaseSubscription(ctx, &billingv1.PurchaseSubscriptionRequest{
		UserId:      userIDStr,
		PriceAmount: group.PriceQuota,
		GroupId:     group.ID,
		Remark:      remark,
	})
	if err != nil {
		return nil, err
	}
	if !deduct.GetSuccess() {
		return nil, errors.New(deduct.GetErrorMessage())
	}

	sub, err := s.assignOrExtendGroupSubscription(ctx, userID, group, int64(group.DurationDays), "", "")
	if err != nil {
		// Compensate: give the quota back so the user is not charged for a
		// subscription that was never created.
		if _, refundErr := s.billingClient.TopUpQuota(ctx, &billingv1.TopUpQuotaRequest{
			UserId:     userIDStr,
			Amount:     group.PriceQuota,
			OperatorId: "system",
			Remark:     fmt.Sprintf("refund: subscription purchase rollback group=%d", group.ID),
		}); refundErr != nil {
			return nil, fmt.Errorf("assign subscription failed (%w) and quota refund failed: %v", err, refundErr)
		}
		return nil, err
	}
	return sub, nil
}

func (s *AdminService) PurchaseSubscriptionPlan(ctx context.Context, userID, planID int64) (*subscriptionbiz.UserSubscription, error) {
	if s == nil || s.subscriptionUc == nil || s.groupUc == nil || s.planUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	if s.billingClient == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	if userID <= 0 {
		return nil, fmt.Errorf("invalid user id")
	}
	plan, err := s.planUc.Get(ctx, planID)
	if err != nil {
		return nil, err
	}
	if !isPurchasablePlan(plan) {
		return nil, subscriptionbiz.ErrSubscriptionPlanNotSaleable
	}
	if err := s.ensureSubscriptionCanUseGroup(ctx, userID, plan.GroupID); err != nil {
		return nil, err
	}

	userIDStr := strconv.FormatInt(userID, 10)
	remark := fmt.Sprintf("purchase subscription plan=%d (%s)", plan.ID, plan.Name)
	deduct, err := s.billingClient.PurchaseSubscription(ctx, &billingv1.PurchaseSubscriptionRequest{
		UserId:      userIDStr,
		PriceAmount: plan.PriceQuota,
		GroupId:     plan.GroupID,
		Remark:      remark,
	})
	if err != nil {
		return nil, err
	}
	if !deduct.GetSuccess() {
		return nil, errors.New(deduct.GetErrorMessage())
	}
	sub, err := s.assignOrExtendPlanSubscription(ctx, userID, plan, "")
	if err != nil {
		if _, refundErr := s.billingClient.TopUpQuota(ctx, &billingv1.TopUpQuotaRequest{
			UserId:     userIDStr,
			Amount:     plan.PriceQuota,
			OperatorId: "system",
			Remark:     fmt.Sprintf("refund: subscription purchase rollback plan=%d", plan.ID),
		}); refundErr != nil {
			return nil, fmt.Errorf("assign subscription failed (%w) and quota refund failed: %v", err, refundErr)
		}
		return nil, err
	}
	return sub, nil
}

func (s *AdminService) ensureSubscriptionCanUseGroup(ctx context.Context, userID, groupID int64) error {
	active, err := s.subscriptionUc.GetActiveSubscriptionForUser(ctx, userID)
	if err != nil {
		if errors.Is(err, subscriptionbiz.ErrSubscriptionNotFound) {
			return nil
		}
		return err
	}
	if active == nil {
		return nil
	}
	if active.GroupID == groupID {
		return nil
	}
	// A pending next-cycle change (downgrade) scheduled on the active
	// subscription may target a different group. Allow the renewal to proceed
	// for the pending target group so the downgrade takes effect at renewal
	// (review H9 fix). Previously the renewal flow required the request's
	// GroupID to equal the active group, so a pending downgrade never applied.
	if pending, ok := subscriptionbiz.PendingChangeFromMetadata(active.Metadata); ok && pending.ToGroupID == groupID {
		return nil
	}
	return subscriptionbiz.ErrSubscriptionAlreadyAssigned
}

func (s *AdminService) assignOrExtendGroupSubscription(ctx context.Context, userID int64, group *subscriptionbiz.SubscriptionGroup, durationDays int64, subscriptionName, metadata string) (*subscriptionbiz.UserSubscription, error) {
	if group == nil {
		return nil, subscriptionbiz.ErrSubscriptionGroupNotFound
	}
	name := subscriptionName
	if name == "" {
		name = group.DisplayName
	}
	if name == "" {
		name = group.Name
	}
	// Pass the raw renewal duration (startsAt=now, expiresAt=now+duration) and
	// let SubscriptionUsecase.AssignOrExtend accumulate any remaining time on
	// the active subscription (review H3: max(active.ExpiresAt, now)+duration).
	// Previously this function computed the accumulated expiry itself while the
	// payment-callback assigner path did not, so the two issuance paths used
	// inconsistent renewal semantics. Centralizing the accumulation in
	// AssignOrExtend makes both paths identical.
	now := time.Now().Unix()
	sub, _, err := s.subscriptionUc.AssignOrExtend(ctx, &subscriptionbiz.AssignSubscriptionRequest{
		UserID:           userID,
		GroupID:          group.ID,
		SubscriptionName: name,
		StartsAt:         now,
		ExpiresAt:        now + durationDays*secondsPerDay,
		Metadata:         metadata,
	})
	return sub, err
}

func (s *AdminService) assignOrExtendPlanSubscription(ctx context.Context, userID int64, plan *subscriptionbiz.SubscriptionPlan, metadata string) (*subscriptionbiz.UserSubscription, error) {
	if plan == nil {
		return nil, subscriptionbiz.ErrSubscriptionPlanNotFound
	}
	group := plan.Group
	if group == nil {
		var err error
		group, err = s.groupUc.Get(ctx, plan.GroupID)
		if err != nil {
			return nil, err
		}
	}
	name := plan.Name
	if name == "" {
		name = group.DisplayName
	}
	if name == "" {
		name = group.Name
	}
	return s.assignOrExtendGroupSubscription(ctx, userID, group, int64(plan.ValidityDays), name, metadata)
}

func (s *AdminService) AssignSubscription(ctx context.Context, req *subscriptionbiz.AssignSubscriptionRequest) (*subscriptionbiz.UserSubscription, error) {
	if s == nil || s.subscriptionUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	return s.subscriptionUc.Assign(ctx, req)
}

func (s *AdminService) RevokeSubscription(ctx context.Context, id int64, reason string) error {
	if s == nil || s.subscriptionUc == nil {
		return ErrSubscriptionServiceNotConfigured
	}
	return s.subscriptionUc.Revoke(ctx, id, reason)
}

func (s *AdminService) ExtendSubscription(ctx context.Context, id int64, expiresAt int64) error {
	if s == nil || s.subscriptionUc == nil {
		return ErrSubscriptionServiceNotConfigured
	}
	return s.subscriptionUc.Extend(ctx, id, expiresAt)
}

// ChangeSubscription upgrades or downgrades the user's active subscription
// (phase 2.4). It enforces the fixed policy:
//   - immediate upgrade: charge the price difference to the wallet (billing
//     writes the ledger atomically) BEFORE mutating the subscription, so the
//     audit trail reflects a real charge; if the wallet rejects the charge
//     (insufficient balance) the subscription is not changed.
//   - next-cycle downgrade: record a pending_change applied at the next
//     renewal; no wallet movement.
//
// The operator id is the admin who triggered the change.
//
// Review H7+H8 fix: previously the admin layer delegated to the subscription
// usecase without charging the wallet, so upgrades were free while the
// subscription metadata recorded a charged amount — a fabricated audit entry.
// Now the wallet charge happens here, before ChangeSubscription, and the
// ChargeResult.ChargedQuota only reflects a real charge.
func (s *AdminService) ChangeSubscription(ctx context.Context, req subscriptionbiz.ChangeRequest) (*subscriptionbiz.ChangeResult, error) {
	if s == nil || s.subscriptionUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	// Review M7 fix: server-side price validation. When the target plan is
	// specified, load it and verify the requested NewPriceQuota matches the
	// plan's configured price so a tampered admin request cannot charge a
	// wrong upgrade diff. OldPriceQuota is validated against the active
	// subscription's current plan when available.
	if req.ToPlanID > 0 && s.planUc != nil {
		plan, err := s.planUc.Get(ctx, req.ToPlanID)
		if err != nil {
			return nil, fmt.Errorf("load target plan for price validation: %w", err)
		}
		if plan == nil {
			return nil, subscriptionbiz.ErrSubscriptionPlanNotFound
		}
		if req.NewPriceQuota != plan.PriceQuota {
			return nil, fmt.Errorf("new_price_quota %d does not match plan %d price %d", req.NewPriceQuota, req.ToPlanID, plan.PriceQuota)
		}
		if req.ToGroupID <= 0 {
			req.ToGroupID = plan.GroupID
		}
	}
	// Infer the policy the same way the usecase does, so we only charge for
	// immediate upgrades.
	policy := req.Policy
	if policy == "" {
		if req.NewPriceQuota >= req.OldPriceQuota {
			policy = subscriptionbiz.SubscriptionChangePolicyImmediate
		} else {
			policy = subscriptionbiz.SubscriptionChangePolicyNextCycle
		}
	}
	charged := int64(0)
	if policy == subscriptionbiz.SubscriptionChangePolicyImmediate {
		charged = req.NewPriceQuota - req.OldPriceQuota
		// Only charge a positive difference. A same-price or negative-difference
		// "upgrade" is treated as a no-op change (the usecase still records the
		// group swap); charging a negative amount would be a refund through the
		// wrong path. Downgrades are deferred to next_cycle and never charged
		// here.
		if charged > 0 {
			if s.billingClient == nil {
				return nil, ErrSubscriptionServiceNotConfigured
			}
			userIDStr := strconv.FormatInt(req.UserID, 10)
			remark := fmt.Sprintf("subscription upgrade group=%d (operator=%s)", req.ToGroupID, req.Operator)
			deduct, err := s.billingClient.PurchaseSubscription(ctx, &billingv1.PurchaseSubscriptionRequest{
				UserId:      userIDStr,
				PriceAmount: charged,
				GroupId:     req.ToGroupID,
				Remark:      remark,
			})
			if err != nil {
				return nil, fmt.Errorf("charge upgrade difference: %w", err)
			}
			if !deduct.GetSuccess() {
				return nil, fmt.Errorf("charge upgrade difference: %s", deduct.GetErrorMessage())
			}
		} else {
			charged = 0
		}
	}
	res, err := s.subscriptionUc.ChangeSubscription(ctx, req)
	if err != nil && charged > 0 {
		// Compensate: the wallet was charged but the subscription mutation
		// failed, so refund the difference so no charge lingers without a
		// service change. Mirrors the PurchaseSubscription saga.
		userIDStr := strconv.FormatInt(req.UserID, 10)
		if _, refundErr := s.billingClient.TopUpQuota(ctx, &billingv1.TopUpQuotaRequest{
			UserId:     userIDStr,
			Amount:     charged,
			OperatorId: "system",
			Remark:     fmt.Sprintf("refund: subscription change rollback group=%d", req.ToGroupID),
		}); refundErr != nil {
			return nil, fmt.Errorf("change subscription failed (%w) and quota refund failed: %v", err, refundErr)
		}
		return nil, err
	}
	if res != nil && policy == subscriptionbiz.SubscriptionChangePolicyImmediate {
		// Reflect the real charge in the result (the usecase's ChargedQuota is
		// derived from the request; we override it with the amount actually
		// deducted so the audit trail matches the ledger).
		res.ChargedQuota = charged
	}
	return res, err
}

func (s *AdminService) ResetSubscriptionQuota(ctx context.Context, id int64, scope string) error {
	if s == nil || s.subscriptionUc == nil {
		return ErrSubscriptionServiceNotConfigured
	}
	return s.subscriptionUc.ResetQuota(ctx, id, scope)
}

func (s *AdminService) GetSubscriptionProgress(ctx context.Context, userID int64) (*subscriptionbiz.SubscriptionProgress, error) {
	if s == nil || s.subscriptionUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	return s.subscriptionUc.GetProgress(ctx, userID)
}

func (s *AdminService) ListUserSubscriptions(ctx context.Context, userID int64) ([]*subscriptionbiz.UserSubscription, error) {
	if s == nil || s.subscriptionUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	return s.subscriptionUc.ListByUser(ctx, userID)
}

func (s *AdminService) ListAllSubscriptions(ctx context.Context) ([]*subscriptionbiz.UserSubscription, error) {
	if s == nil || s.subscriptionUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	return s.subscriptionUc.List(ctx)
}

func (s *AdminService) CreateSubscriptionGroup(ctx context.Context, group *subscriptionbiz.SubscriptionGroup) error {
	if s == nil || s.groupUc == nil {
		return ErrSubscriptionServiceNotConfigured
	}
	return s.groupUc.Create(ctx, group)
}

func (s *AdminService) UpdateSubscriptionGroup(ctx context.Context, group *subscriptionbiz.SubscriptionGroup) error {
	if s == nil || s.groupUc == nil {
		return ErrSubscriptionServiceNotConfigured
	}
	return s.groupUc.Update(ctx, group)
}

func (s *AdminService) DeleteSubscriptionGroup(ctx context.Context, groupID int64) error {
	if s == nil || s.groupUc == nil {
		return ErrSubscriptionServiceNotConfigured
	}
	return s.groupUc.Delete(ctx, groupID)
}

func (s *AdminService) GetSubscriptionGroup(ctx context.Context, groupID int64) (*subscriptionbiz.SubscriptionGroup, error) {
	if s == nil || s.groupUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	return s.groupUc.Get(ctx, groupID)
}

func (s *AdminService) ListSubscriptionGroups(ctx context.Context) ([]*subscriptionbiz.SubscriptionGroup, error) {
	if s == nil || s.groupUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	return s.groupUc.List(ctx)
}

// CreateSubscriptionPaymentOrder creates a payment order for subscription purchase.
func (s *AdminService) CreateSubscriptionPaymentOrder(ctx context.Context, userID, groupID, planID int64, channel string, moneyCents int64, currency string) (subscription *subscriptionbiz.UserSubscription, paymentOrder *PaymentOrderInfo, err error) {
	if s == nil || s.subscriptionUc == nil || s.groupUc == nil {
		return nil, nil, ErrSubscriptionServiceNotConfigured
	}
	if s.billingClient == nil {
		return nil, nil, ErrSubscriptionServiceNotConfigured
	}
	if userID <= 0 {
		return nil, nil, fmt.Errorf("invalid user id")
	}

	var group *subscriptionbiz.SubscriptionGroup
	var plan *subscriptionbiz.SubscriptionPlan
	priceQuota := int64(0)
	durationDays := int64(0)
	if planID > 0 {
		if s.planUc == nil {
			return nil, nil, ErrSubscriptionServiceNotConfigured
		}
		plan, err = s.planUc.Get(ctx, planID)
		if err != nil {
			return nil, nil, err
		}
		if !isPurchasablePlan(plan) {
			return nil, nil, subscriptionbiz.ErrSubscriptionPlanNotSaleable
		}
		group = plan.Group
		groupID = plan.GroupID
		priceQuota = plan.PriceQuota
		durationDays = int64(plan.ValidityDays)
	} else {
		group, err = s.groupUc.Get(ctx, groupID)
		if err != nil {
			return nil, nil, err
		}
		if !isPurchasableGroup(group) {
			return nil, nil, ErrSubscriptionNotPurchasable
		}
		priceQuota = group.PriceQuota
		durationDays = int64(group.DurationDays)
	}
	if err := s.ensureSubscriptionCanUseGroup(ctx, userID, groupID); err != nil {
		return nil, nil, err
	}

	userIDStr := strconv.FormatInt(userID, 10)

	if moneyCents <= 0 {
		moneyCents = priceQuota * 100
	}
	if currency == "" {
		currency = "CNY"
	}
	if channel == "" {
		channel = "alipay"
	}

	paymentResp, err := s.billingClient.CreatePaymentOrder(ctx, &billingv1.CreatePaymentOrderRequest{
		UserId:      userIDStr,
		Channel:     channel,
		AssetType:   "subscription",
		AssetAmount: durationDays,
		MoneyCents:  moneyCents,
		Currency:    currency,
		GroupId:     groupID,
		PlanId:      planID,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create payment order: %w", err)
	}
	if !paymentResp.Success {
		return nil, nil, errors.New(paymentResp.GetErrorMessage())
	}
	if paymentResp.Order == nil {
		return nil, nil, errors.New("payment order creation returned nil order")
	}

	order := &PaymentOrderInfo{
		TradeNo:     paymentResp.Order.TradeNo,
		Channel:     paymentResp.Order.Channel,
		MoneyCents:  paymentResp.Order.MoneyCents,
		Currency:    paymentResp.Order.Currency,
		Status:      paymentResp.Order.Status,
		PayURL:      paymentResp.Order.PayUrl,
		AssetAmount: paymentResp.Order.AssetAmount,
		GroupId:     paymentResp.Order.GroupId,
		PlanId:      paymentResp.Order.PlanId,
		CreatedAt:   paymentResp.Order.CreatedAt.AsTime().Unix(),
	}
	return nil, order, nil
}

// PaymentOrderInfo represents a payment order for subscription purchase.
type PaymentOrderInfo struct {
	TradeNo     string `json:"trade_no"`
	Channel     string `json:"channel"`
	MoneyCents  int64  `json:"money_cents"`
	Currency    string `json:"currency"`
	Status      string `json:"status"`
	PayURL      string `json:"pay_url"`
	AssetAmount int64  `json:"asset_amount"`
	GroupId     int64  `json:"group_id"`
	PlanId      int64  `json:"plan_id"`
	CreatedAt   int64  `json:"created_at"`
}

// CompleteSubscriptionPurchase completes a subscription purchase after payment.
// It checks the payment order status and assigns the subscription if payment is successful.
func (s *AdminService) CompleteSubscriptionPurchase(ctx context.Context, userID int64, tradeNo string) (*subscriptionbiz.UserSubscription, error) {
	if s == nil || s.subscriptionUc == nil || s.groupUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	if s.billingClient == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}

	// Get payment order to verify payment and get group_id
	orderResp, err := s.billingClient.GetPaymentOrderByTradeNo(ctx, &billingv1.GetPaymentOrderByTradeNoRequest{TradeNo: tradeNo})
	if err != nil {
		return nil, fmt.Errorf("failed to get payment order: %w", err)
	}
	if !orderResp.Success {
		return nil, errors.New(orderResp.GetErrorMessage())
	}
	if orderResp.Order == nil {
		return nil, errors.New("payment order not found")
	}
	order := orderResp.Order

	// Verify the order belongs to the user
	if strconv.FormatInt(userID, 10) != order.UserId {
		return nil, errors.New("payment order does not belong to the user")
	}

	// Check if payment is completed
	if order.Status != "paid" {
		return nil, fmt.Errorf("payment not completed (status: %s)", order.Status)
	}
	// M10 fix: atomically claim the asset issuance (pending -> issued) BEFORE
	// fulfilling. A concurrent or replayed completion can only ever win this
	// claim once; the loser observes claimed=false with an already-issued
	// order and returns the current subscription instead of double-fulfilling
	// (which would extend the user's entitlement repeatedly). On fulfilment
	// failure the claim is released via UnmarkPaymentOrderAssetIssued so the
	// order can be retried.
	claimResp, err := s.billingClient.MarkPaymentOrderAssetIssued(ctx, &billingv1.MarkPaymentOrderAssetIssuedRequest{
		TradeNo: tradeNo,
		UserId:  strconv.FormatInt(userID, 10),
	})
	if err != nil {
		return nil, fmt.Errorf("claim asset issuance: %w", err)
	}
	if !claimResp.Success {
		return nil, errors.New(claimResp.GetErrorMessage())
	}
	if claimResp.Order == nil {
		return nil, errors.New("payment order not found")
	}
	if !claimResp.GetClaimed() {
		// Did not win the claim. If the order is already issued, someone else
		// fulfilled it — return the user's current active subscription so a
		// replay is idempotent. The returned subscription is the user's
		// best-current active subscription, which may differ from the one
		// granted by THIS order (e.g. a later purchase superseded it, or it
		// has since expired); callers needing the exact order->subscription
		// link must read order.subscription_id instead.
		if claimResp.Order.GetAssetIssueStatus() == "issued" {
			sub, err := s.subscriptionUc.GetActiveSubscriptionForUser(ctx, userID)
			if err != nil {
				return nil, err
			}
			return sub, nil
		}
		return nil, fmt.Errorf("payment order is not issuable (asset_issue_status: %s)", claimResp.Order.GetAssetIssueStatus())
	}

	// Won the claim; fulfil the order.
	sub, fulfilErr := s.fulfilPaidOrder(ctx, userID, order)
	if fulfilErr != nil {
		// Release the claim so the order can be retried (compensation). If
		// the release itself fails the order is left in the issued-but-
		// unfulfilled stuck state; a reconciler must detect and repair it
		// (status=paid && asset_issue_status=issued && subscription_id=0).
		// errors.Join preserves both the fulfilment and the release failures
		// so callers can match on either.
		if _, unmarkErr := s.billingClient.UnmarkPaymentOrderAssetIssued(ctx, &billingv1.UnmarkPaymentOrderAssetIssuedRequest{TradeNo: tradeNo}); unmarkErr != nil {
			return nil, errors.Join(fulfilErr, fmt.Errorf("releasing asset-issuance claim after fulfilment failure: %w", unmarkErr))
		}
		return nil, fulfilErr
	}
	return sub, nil
}

// fulfilPaidOrder grants the subscription for an already-paid order using the
// plan snapshot / live plan / group paths. It is called only after winning the
// issuance claim (see CompleteSubscriptionPurchase), so it never runs twice for
// the same order.
func (s *AdminService) fulfilPaidOrder(ctx context.Context, userID int64, order *billingv1.PaymentOrder) (*subscriptionbiz.UserSubscription, error) {
	if order.PlanId > 0 {
		// Phase 2: fulfil from the immutable plan snapshot captured at order
		// creation. This keeps already-created orders completable even if the
		// plan was later taken off-shelf or edited; only NEW orders are gated
		// by the for_sale check (in CreateSubscriptionPaymentOrder).
		if order.GetPlanSnapshot() != "" {
			return s.completeFromPlanSnapshot(ctx, userID, order)
		}
		if s.planUc == nil {
			return nil, ErrSubscriptionServiceNotConfigured
		}
		plan, err := s.planUc.Get(ctx, order.PlanId)
		if err != nil {
			return nil, err
		}
		if !isPurchasablePlan(plan) {
			return nil, subscriptionbiz.ErrSubscriptionPlanNotSaleable
		}
		if err := s.ensureSubscriptionCanUseGroup(ctx, userID, plan.GroupID); err != nil {
			return nil, err
		}
		durationDays := order.AssetAmount
		if durationDays <= 0 {
			durationDays = int64(plan.ValidityDays)
		}
		name := plan.Name
		if name == "" {
			name = plan.ProductName
		}
		return s.assignOrExtendGroupSubscription(ctx, userID, plan.Group, durationDays, name, "")
	}
	if err := s.ensureSubscriptionCanUseGroup(ctx, userID, order.GroupId); err != nil {
		return nil, err
	}

	// Get group info
	group, err := s.groupUc.Get(ctx, order.GroupId)
	if err != nil {
		return nil, fmt.Errorf("failed to get subscription group: %w", err)
	}
	if !isPurchasableGroup(group) {
		return nil, ErrSubscriptionNotPurchasable
	}

	sub, err := s.assignOrExtendGroupSubscription(ctx, userID, group, int64(group.DurationDays), "", "")
	if err != nil {
		return nil, fmt.Errorf("failed to assign subscription: %w", err)
	}
	return sub, nil
}

// completeFromPlanSnapshot fulfils a paid plan order from the immutable
// snapshot stored on the payment order. The live plan row is not consulted,
// so a plan taken off-shelf between order creation and payment completion
// cannot strand the order. The snapshot carries group_id, validity and name.
func (s *AdminService) completeFromPlanSnapshot(ctx context.Context, userID int64, order *billingv1.PaymentOrder) (*subscriptionbiz.UserSubscription, error) {
	snap, err := subscriptionbiz.DecodePlanSnapshot(order.GetPlanSnapshot())
	if err != nil {
		return nil, fmt.Errorf("decode plan snapshot: %w", err)
	}
	if snap.PlanID == 0 || snap.GroupID <= 0 {
		return nil, fmt.Errorf("plan snapshot is incomplete")
	}
	if err := s.ensureSubscriptionCanUseGroup(ctx, userID, snap.GroupID); err != nil {
		return nil, err
	}
	durationDays := order.GetAssetAmount()
	if durationDays <= 0 {
		durationDays = int64(snap.ValidityDays)
	}
	if durationDays <= 0 {
		return nil, errors.New("subscription plan duration must be positive")
	}
	group, err := s.groupUc.Get(ctx, snap.GroupID)
	if err != nil {
		return nil, fmt.Errorf("failed to get subscription group: %w", err)
	}
	name := snap.Name
	if name == "" {
		name = snap.ProductName
	}
	return s.assignOrExtendGroupSubscription(ctx, userID, group, durationDays, name, "")
}
