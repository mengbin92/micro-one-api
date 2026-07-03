package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	billingv1 "micro-one-api/api/billing/v1"
	identityv1 "micro-one-api/api/identity/v1"
	subscriptionbiz "micro-one-api/internal/subscription/biz"
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

func isPurchasableGroup(g *subscriptionbiz.SubscriptionGroup) bool {
	return g != nil &&
		g.Status == subscriptionbiz.SubscriptionGroupStatusEnabled &&
		g.PriceQuota > 0 &&
		g.DurationDays > 0
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

	// Reject an existing active subscription before touching the wallet.
	if _, err := s.subscriptionUc.GetProgress(ctx, userID); err == nil {
		return nil, subscriptionbiz.ErrSubscriptionAlreadyAssigned
	} else if !errors.Is(err, subscriptionbiz.ErrSubscriptionNotFound) {
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

	now := time.Now().Unix()
	name := group.DisplayName
	if name == "" {
		name = group.Name
	}
	sub, err := s.subscriptionUc.Assign(ctx, &subscriptionbiz.AssignSubscriptionRequest{
		UserID:           userID,
		GroupID:          group.ID,
		SubscriptionName: name,
		StartsAt:         now,
		ExpiresAt:        now + int64(group.DurationDays)*secondsPerDay,
	})
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
func (s *AdminService) CreateSubscriptionPaymentOrder(ctx context.Context, userID, groupID int64, channel string, moneyCents int64, currency string) (subscription *subscriptionbiz.UserSubscription, paymentOrder *PaymentOrderInfo, err error) {
	if s == nil || s.subscriptionUc == nil || s.groupUc == nil {
		return nil, nil, ErrSubscriptionServiceNotConfigured
	}
	if s.billingClient == nil {
		return nil, nil, ErrSubscriptionServiceNotConfigured
	}
	if userID <= 0 {
		return nil, nil, fmt.Errorf("invalid user id")
	}

	group, err := s.groupUc.Get(ctx, groupID)
	if err != nil {
		return nil, nil, err
	}
	if !isPurchasableGroup(group) {
		return nil, nil, ErrSubscriptionNotPurchasable
	}

	// Reject an existing active subscription
	if _, err := s.subscriptionUc.GetProgress(ctx, userID); err == nil {
		return nil, nil, subscriptionbiz.ErrSubscriptionAlreadyAssigned
	} else if !errors.Is(err, subscriptionbiz.ErrSubscriptionNotFound) {
		return nil, nil, err
	}

	userIDStr := strconv.FormatInt(userID, 10)

	if moneyCents <= 0 {
		moneyCents = group.PriceQuota * 100
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
		AssetAmount: 1,
		MoneyCents:  moneyCents,
		Currency:    currency,
		GroupId:     groupID,
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

	// Check if subscription already exists
	if _, err := s.subscriptionUc.GetProgress(ctx, userID); err == nil {
		return nil, subscriptionbiz.ErrSubscriptionAlreadyAssigned
	} else if !errors.Is(err, subscriptionbiz.ErrSubscriptionNotFound) {
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

	// Assign subscription
	const secondsPerDay = 24 * 60 * 60
	now := time.Now().Unix()
	name := group.DisplayName
	if name == "" {
		name = group.Name
	}
	sub, err := s.subscriptionUc.Assign(ctx, &subscriptionbiz.AssignSubscriptionRequest{
		UserID:           userID,
		GroupID:          order.GroupId,
		SubscriptionName: name,
		StartsAt:         now,
		ExpiresAt:        now + int64(group.DurationDays)*secondsPerDay,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to assign subscription: %w", err)
	}
	return sub, nil
}
