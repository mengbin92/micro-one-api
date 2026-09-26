package routingtest

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	identityv1 "micro-one-api/api/identity/v1"
)

func TestRoutingAcceptance(t *testing.T) {
	if os.Getenv("ROUTING_E2E") != "1" {
		t.Skip("run make test-routing-e2e in its isolated Compose project")
	}
	s := newSuite(t)
	switch os.Getenv("ROUTING_PHASE") {
	case "legacy":
		s.legacy()
	case "stream-reliability":
		s.streamReliability()
	case "v2":
		for _, test := range []struct {
			name string
			run  func()
		}{{"fixed_and_revocation", s.fixed}, {"frozen_price_and_idempotency", s.frozenPrice}, {"ordered", s.ordered}, {"contracts_and_settlement", s.contracts}, {"sessions", s.sessions}, {"raw_contracts", s.rawContracts}} {
			if !t.Run(test.name, func(t *testing.T) { s.t = t; test.run() }) {
				return
			}
		}
	case "creation-rollback":
		s.creationRollback()
	case "legacy-redis-down":
		// O5a: legacy 缓存路径在 Redis 故障下的可用性与降级延迟。基线取
		// 故障前窗口（legacy 相位流量）；L1 30s 会吸收大部分鉴权查找，回源
		// 样本量小是预期行为而非缺陷。
		s.recordO5("legacy_baseline", 10)
		s.driveBurst(s.state.Legacy, "o5-legacy-down", 12)
		s.recordO5("legacy_outage", 2)
	case "legacy-redis-recovered":
		s.chat(s.state.Legacy, "o5-legacy-recovered", false)
		s.settled("o5-legacy-recovered")
		s.recordO5("legacy_recovered", 2)
	case "redis-down":
		s.access(s.state.Groups["p0-wallet"], "revoke", "fixture")
		require.Positive(t, s.scalar("SELECT COUNT(*) FROM routing_change_outbox WHERE delivered_at=0"))
		s.reject(s.state.Fixed, "routing-redis-down")
		s.observeRedisOutage()
		// O5a: V2 每请求回源路径在 Redis 故障下的可用性与降级延迟。用
		// Legacy token（fixed 相位已在 V2 下验证过它），Ordered 的可用组
		// 在本相位开头被撤权，不能用于故障窗口。
		s.recordO5("v2_baseline", 10)
		s.driveBurst(s.state.Legacy, "o5-v2-down", 12)
		s.waitDependencyRPCScraped(10)
		s.recordO5("v2_outage", 2)
	case "redis-recovered":
		require.Eventually(t, func() bool { return s.scalar("SELECT COUNT(*) FROM routing_change_outbox WHERE delivered_at=0") == 0 }, 20*time.Second, 200*time.Millisecond)
		s.reject(s.state.Fixed, "routing-redis-recovered-denied")
		s.access(s.state.Groups["p0-wallet"], "grant", "fixture")
		s.chat(s.state.Fixed, "routing-redis-recovered", false)
		s.settled("routing-redis-recovered")
		s.observeRedisRecovery()
		// O5a: 恢复后的回源延迟应回到基线量级。
		s.driveBurst(s.state.Legacy, "o5-v2-recovered", 12)
		s.waitDependencyRPCScraped(10)
		s.recordO5("v2_recovered", 2)
	case "missing-capability":
		s.reject(s.state.Fixed, "routing-no-billing-capability")
		s.reject(s.state.Ordered, "routing-no-ordered-capability")
		s.observeCapabilityRejection()
	default:
		t.Fatal("unknown acceptance phase")
	}
	s.t = t
	s.save()
}

func (s *suite) legacy() {
	ctx, c := s.conn("IDENTITY_GRPC_ENDPOINT")
	client := identityv1.NewIdentityServiceClient(c)
	user, err := client.Register(ctx, &identityv1.RegisterRequest{Username: "routing-e2e-user", Password: "routing-e2e-user-password", Email: "user@routing.test", Group: "default"})
	require.NoError(s.t, err)
	require.True(s.t, user.Success)
	login, err := client.Login(ctx, &identityv1.LoginRequest{Username: "routing-e2e-user", Password: "routing-e2e-user-password"})
	require.NoError(s.t, err)
	s.state.UserID = user.UserId
	s.state.Session = login.Token
	s.adminAPI("POST", "/api/channel", object{"name": "routing-legacy", "type": 1, "base_url": "http://mock-upstream:9999", "key": "sk-routing-fixture", "models": "gpt-3.5-turbo", "group": "default", "priority": 1, "weight": 1})
	s.adminAPI("POST", "/v1/topup", object{"user_id": fmt.Sprint(user.UserId), "amount": 50000000, "remark": "isolated routing acceptance"})
	v := s.api("POST", "/api/token", s.state.Session, object{"name": "routing-legacy", "models": []string{"gpt-3.5-turbo"}, "unlimited_quota": true}, "")
	s.state.Legacy = v["key"].(string)
	reliability := s.api("POST", "/api/token", s.state.Session, object{"name": "r-batch", "unlimited_quota": true}, "")
	s.state.Reliability = reliability["key"].(string)
	s.chat(s.state.Legacy, "routing-legacy", false)
	require.Nil(s.t, s.settled("routing-legacy"))
	require.Less(s.t, s.balance(), int64(50000000))
	quota := s.adminAPI("POST", "/api/v1/admin/subscription-groups", object{"name": "legacy-policy", "display_name": "legacy-policy", "rate_multiplier": 1, "daily_limit_usd": 1, "weekly_limit_usd": 7, "monthly_limit_usd": 30, "status": 1})
	plan := s.adminAPI("POST", "/api/v1/admin/subscription-plans", object{"group_id": quota["id"], "name": "legacy-plan", "price_quota": 1000, "validity_days": 30, "for_sale": true})
	sub := s.purchase(num(plan["id"]), "routing-legacy-purchase")
	s.state.LegacyPlan = num(plan["id"])
	require.Nil(s.t, sub["contract"])
	s.state.LegacySubscription = num(sub["id"])
	s.chat(s.state.Legacy, "routing-legacy-contract", false)
	require.Nil(s.t, s.settled("routing-legacy-contract"))
}
func (s *suite) fixed() {
	s.chat(s.state.Legacy, "routing-inherit-v2", false)
	require.EqualValues(s.t, 2, s.settled("routing-inherit-v2")["Version"])
	s.revoke(s.state.LegacySubscription)
	s.adminAPI("DELETE", fmt.Sprintf("/api/v1/admin/subscription-plans/%d", s.state.LegacyPlan), nil)
	g := s.group("p0-wallet", "wallet_only", true)
	s.access(g, "grant", "fixture")
	v := s.token("fixed", g, nil)
	s.state.Fixed = v["key"].(string)
	s.state.FixedID = num(v["id"])
	status, raw, err := send("GET", s.relay+"/v1/models", s.state.Fixed, nil, "")
	require.NoError(s.t, err)
	require.Equal(s.t, 200, status)
	require.Contains(s.t, string(raw), "gpt-3.5-turbo")
	s.chat(s.state.Fixed, "routing-fixed", true)
	snap := s.settled("routing-fixed")
	require.Equal(s.t, "p0-wallet", snap["GroupKey"])
	require.Equal(s.t, "wallet_only", snap["BillingMode"])
	primary := s.relay
	s.relay = os.Getenv("RELAY_PEER_HTTP_BASE")
	s.chat(s.state.Fixed, "routing-peer-warm", false)
	s.settled("routing-peer-warm")
	s.relay = primary
	s.access(g, "revoke", "fixture")
	s.replayInvalidations()
	s.reject(s.state.Fixed, "routing-revoked")
	s.relay = os.Getenv("RELAY_PEER_HTTP_BASE")
	s.reject(s.state.Fixed, "routing-peer-revoked")
	s.relay = primary
	s.access(g, "grant", "fixture")
	s.stateGroup(g, "disabled")
	s.reject(s.state.Fixed, "routing-disabled")
	s.stateGroup(g, "enabled")
}
func (s *suite) frozenPrice() {
	g := s.state.Groups["p0-wallet"]
	url := fmt.Sprintf("/api/v1/admin/routing-groups/%d/user-price/%d", g, s.state.UserID)
	s.adminAPI("PUT", url, object{"price_ratio": 2})
	after := s.scalar("SELECT COALESCE(MAX(id),0) FROM billing_reservations")
	done := make(chan error, 1)
	go func() {
		status, _, err := send("POST", s.relay+"/v1/chat/completions", s.state.Fixed, object{"model": "gpt-3.5-turbo", "messages": []any{object{"role": "user", "content": "freeze-price"}}, "max_tokens": 32}, "routing-frozen")
		if err == nil && status != 200 {
			err = fmt.Errorf("freeze request HTTP %d", status)
		}
		done <- err
	}()
	s.trackRequest("routing-frozen", after)
	require.EqualValues(s.t, 1, s.scalar("SELECT COUNT(*) FROM billing_reservations WHERE request_id = ? AND status = 'reserved'", s.state.Requests["routing-frozen"]))
	s.adminAPI("PUT", url, object{"price_ratio": 3})
	require.NoError(s.t, <-done)
	snap := s.settled("routing-frozen")
	require.EqualValues(s.t, 2, snap["Pricing"].(map[string]any)["GroupRatio"])
	s.chat(s.state.Fixed, "routing-new-price", false)
	snap = s.settled("routing-new-price")
	require.EqualValues(s.t, 3, snap["Pricing"].(map[string]any)["GroupRatio"])
	before := s.balance()
	s.chat(s.state.Fixed, "routing-new-price", false)
	s.settled("routing-new-price")
	require.Equal(s.t, before, s.balance(), "duplicate request must not debit twice")
	require.EqualValues(s.t, 1, s.scalar("SELECT COUNT(*) FROM billing_ledgers l JOIN billing_reservations r ON l.reference_id = r.reservation_id WHERE r.request_id = ? AND l.type = 'consume'", s.state.Requests["routing-new-price"]))
	s.adminAPI("DELETE", url, nil)
}
func (s *suite) ordered() {
	empty := s.group("p0-empty", "wallet_only", false)
	s.access(empty, "grant", "fixture")
	good := s.state.Groups["p0-wallet"]
	v := s.token("ordered", 0, []int64{empty, good})
	s.state.Ordered = v["key"].(string)
	s.chat(s.state.Ordered, "routing-ordered", false)
	require.Equal(s.t, "p0-wallet", s.settled("routing-ordered")["GroupKey"])
	s.access(empty, "revoke", "fixture")
	s.reject(s.state.Ordered, "routing-ordered-denied")
	s.access(empty, "grant", "fixture")
	noFunds := s.group("p0-no-subscription", "subscription_only", true)
	s.access(noFunds, "grant", "fixture")
	blocked := s.token("ordered", 0, []int64{noFunds, good})
	s.reject(blocked["key"].(string), "routing-ordered-settlement-denied")
	s.stateGroup(good, "disabled")
	s.reject(s.state.Ordered, "routing-ordered-exhausted")
	s.stateGroup(good, "enabled")
}
func (s *suite) creationRollback() {
	s.chat(s.state.Fixed, "routing-rollback-fixed", false)
	s.settled("routing-rollback-fixed")
	s.chat(s.state.Ordered, "routing-rollback-ordered", false)
	s.settled("routing-rollback-ordered")
	for _, mode := range []string{"fixed", "ordered"} {
		status, _, err := send("POST", s.admin+"/api/v1/routing-tokens", s.state.Session, object{"name": "must-not-create", "routing_mode": mode, "routing_group_id": s.state.Groups["p0-wallet"], "routing_group_ids": []int64{s.state.Groups["p0-wallet"]}}, "")
		require.NoError(s.t, err)
		require.GreaterOrEqual(s.t, status, 400)
	}
}
