package routingtest

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	billingv1 "micro-one-api/api/billing/v1"
)

func (s *suite) plan(name string, group int64, limit float64) int64 {
	quota := s.adminAPI("POST", "/api/v1/admin/subscription-groups", object{"name": name, "display_name": name, "rate_multiplier": 1, "daily_limit_usd": limit, "weekly_limit_usd": limit * 7, "monthly_limit_usd": limit * 30, "status": 1})
	plan := s.adminAPI("POST", "/api/v1/admin/subscription-plans", object{"group_id": quota["id"], "name": name, "price_quota": 1000, "validity_days": 30, "for_sale": true, "coverage": []any{object{"routing_group_id": group, "grants_access": true}}})
	require.NotNil(s.t, plan["contract"])
	return num(plan["id"])
}
func (s *suite) purchase(plan int64, id string) object {
	return s.api("POST", "/api/v1/subscriptions/purchase", s.state.Session, object{"plan_id": plan}, id)
}
func (s *suite) revoke(id int64) {
	s.adminAPI("POST", fmt.Sprintf("/api/v1/admin/subscriptions/%d/revoke", id), object{"reason": "isolated fixture lifecycle"})
}
func (s *suite) embedding(key, id string, allowed bool) {
	s.t.Helper()
	before, calls := s.balance(), s.calls()
	after := s.scalar("SELECT COALESCE(MAX(id),0) FROM billing_reservations")
	status, raw, err := send("POST", s.relay+"/v1/embeddings", key, object{"model": "text-embedding-3-small", "input": "bounded routing input"}, id)
	require.NoError(s.t, err)
	if allowed {
		require.Equal(s.t, 200, status, "embeddings: %s", raw)
		require.Contains(s.t, string(raw), "embedding")
		s.trackRequest(id, after)
		s.settled(id)
	} else {
		require.GreaterOrEqual(s.t, status, 400)
		require.Less(s.t, status, 600)
		require.Equal(s.t, calls, s.calls())
	}
	require.Equal(s.t, before, s.balance(), "subscription_only never debits the wallet")
}
func (s *suite) contracts() {
	root := s.t
	if !root.Run("subscription_only_purchase_renew_revoke", func(t *testing.T) {
		s.t = t
		g := s.group("p0-contract-only", "subscription_only", true)
		plan := s.plan("p0-contract-only", g, 1)
		before := s.balance()
		sub := s.purchase(plan, "routing-purchase")
		id := num(sub["id"])
		require.Equal(t, before-1000, s.balance())
		replay := s.purchase(plan, "routing-purchase")
		require.Equal(t, id, num(replay["id"]))
		require.Equal(t, before-1000, s.balance(), "purchase receipt must be idempotent")
		v := s.token("fixed", g, nil)
		key := v["key"].(string)
		before = s.balance()
		s.reject(key, "routing-subonly-unbounded-chat")
		s.embedding(key, "routing-subonly", true)
		snap := s.settled("routing-subonly")
		require.Equal(t, "subscription_only", snap["BillingMode"])
		require.Equal(t, before, s.balance())
		var usage float64
		require.NoError(t, s.db.QueryRow("SELECT daily_usage_usd FROM user_subscriptions WHERE id = ?", id).Scan(&usage))
		require.Positive(t, usage)
		renewed := s.purchase(plan, "routing-renew")
		require.Equal(t, id, num(renewed["id"]))
		require.Equal(t, num(sub["expires_at"])+30*86400, num(renewed["expires_at"]))
		var afterUsage float64
		require.NoError(t, s.db.QueryRow("SELECT daily_usage_usd FROM user_subscriptions WHERE id = ?", id).Scan(&afterUsage))
		require.InDelta(t, usage, afterUsage, 1e-10)
		// An independent admin source survives removal of subscription access, while payment qualification does not.
		s.access(g, "grant", "independent")
		s.revoke(id)
		facts := s.adminAPI("GET", fmt.Sprintf("/api/v1/admin/routing-access/%d", s.state.UserID), nil)
		require.NotEmpty(t, facts["grants"])
		s.embedding(key, "routing-contract-revoked", false)
		zero := s.plan("p0-zero-contract", g, 0)
		s.purchase(zero, "routing-zero-purchase")
		s.embedding(key, "routing-subonly-no-budget", false)
		var active int64
		require.NoError(t, s.db.QueryRow("SELECT id FROM user_subscriptions WHERE user_id = ? AND status = 'active'", s.state.UserID).Scan(&active))
		s.revoke(active)
	}) {
		s.t = root
		return
	}
	if !root.Run("subscription_first_split_and_expiry", func(t *testing.T) {
		s.t = t
		g := s.group("p0-contract-first", "subscription_first", true)
		cost := s.scalar("SELECT actual_cost FROM billing_reservations WHERE request_id = ?", s.state.Requests["routing-fixed"])
		require.Positive(t, cost)
		plan := s.plan("p0-partial", g, float64(cost)/500000/2)
		sub := s.purchase(plan, "routing-partial-purchase")
		id := num(sub["id"])
		key := s.token("fixed", g, nil)["key"].(string)
		before := s.balance()
		s.chat(key, "routing-split", false)
		snap := s.settled("routing-split")
		require.Equal(t, "subscription_first", snap["BillingMode"])
		var subscription, balance, total int64
		require.NoError(t, s.db.QueryRow("SELECT COALESCE(SUM(l.subscription_cost),0),COALESCE(SUM(l.balance_cost),0),COALESCE(SUM(l.amount),0) FROM billing_ledgers l JOIN billing_reservations r ON r.reservation_id=l.reference_id WHERE r.request_id = ? AND l.type='consume'", s.state.Requests["routing-split"]).Scan(&subscription, &balance, &total))
		require.Positive(t, subscription)
		require.Positive(t, balance)
		require.Equal(t, cost, subscription+balance)
		require.Equal(t, balance, before-s.balance())
		require.Equal(t, -cost, total, "consume ledger amount is a signed debit")
		// Expiry is a clock-boundary fixture, not a production write. Request-time enforcement must not wait for a scanner.
		_, err := s.db.Exec("UPDATE user_subscriptions SET expires_at = ? WHERE id = ?", time.Now().Unix()-1, id)
		require.NoError(t, err)
		s.reject(key, "routing-expired-grant")
		s.access(g, "grant", "independent")
		before = s.balance()
		s.chat(key, "routing-expired-wallet", false)
		s.settled("routing-expired-wallet")
		require.Equal(t, cost, before-s.balance())
		// Finish this clock-only fixture before creating an unrelated contract.
		s.revoke(id)
	}) {
		s.t = root
		return
	}
	root.Run("change_and_mock_payment_refund", func(t *testing.T) {
		s.t = t
		oldGroup := s.group("p0-contract-old", "subscription_first", true)
		newGroup := s.group("p0-contract-new", "subscription_first", true)
		oldPlan := s.plan("p0-change-old", oldGroup, 1)
		newPlan := s.plan("p0-change-new", newGroup, 1)
		sub := s.purchase(oldPlan, "routing-change-purchase")
		oldKey := s.token("fixed", oldGroup, nil)["key"].(string)
		s.access(oldGroup, "grant", "independent")
		changed := s.api("POST", "/api/v1/subscriptions/change", s.state.Session, object{"from_subscription_id": sub["id"], "to_plan_id": newPlan, "policy": "immediate"}, "routing-contract-change")
		require.Equal(t, true, changed["applied"])
		s.chat(oldKey, "routing-change-independent", false)
		s.settled("routing-change-independent")
		s.access(oldGroup, "revoke", "independent")
		s.reject(oldKey, "routing-change-old-denied")
		newKey := s.token("fixed", newGroup, nil)["key"].(string)
		s.chat(newKey, "routing-change-new", false)
		s.settled("routing-change-new")
		s.revoke(num(changed["subscription_id"]))
		s.reject(newKey, "routing-change-revoked")
		// Use billing's local mock payment channel; no real payment provider is called.
		ctx, conn := s.conn("BILLING_GRPC_ENDPOINT")
		client := billingv1.NewBillingServiceClient(conn)
		order, err := client.CreatePaymentOrder(ctx, &billingv1.CreatePaymentOrderRequest{UserId: fmt.Sprint(s.state.UserID), RequestId: "routing-mock-payment", Channel: "mock", AssetType: "subscription", AssetAmount: 30, MoneyCents: 100000, Currency: "CNY", PlanId: newPlan})
		require.NoError(t, err)
		require.True(t, order.Success, order.ErrorMessage)
		paid, err := client.MarkPaymentOrderPaid(ctx, &billingv1.MarkPaymentOrderPaidRequest{TradeNo: order.Order.TradeNo, ProviderTradeNo: "routing-mock-paid"})
		require.NoError(t, err)
		require.True(t, paid.Success, paid.ErrorMessage)
		s.chat(newKey, "routing-payment-grant", false)
		s.settled("routing-payment-grant")
		before := s.balance()
		refund, err := client.RefundPaymentOrder(ctx, &billingv1.RefundPaymentOrderRequest{TradeNo: order.Order.TradeNo, Policy: "revoke", Reason: "isolated fixture", OperatorId: "routing-e2e"})
		require.NoError(t, err)
		require.True(t, refund.Success, refund.ErrorMessage)
		s.reject(newKey, "routing-refund-revoked")
		require.EqualValues(t, 1000, refund.RefundedQuota)
		require.Equal(t, before+refund.RefundedQuota, s.balance())
		_, err = client.RefundPaymentOrder(ctx, &billingv1.RefundPaymentOrderRequest{TradeNo: order.Order.TradeNo, Policy: "revoke", Reason: "isolated fixture", OperatorId: "routing-e2e"})
		require.NoError(t, err)
		require.Equal(t, before+refund.RefundedQuota, s.balance(), "refund replay must not credit twice")
	})
	s.t = root
}
