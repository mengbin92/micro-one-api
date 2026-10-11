package server

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"micro-one-api/pkg/jsonx"
)

func TestSubscriptionCatalogPriceCurrencyAndWalletQuote(t *testing.T) {
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "2")
	for _, price := range []int64{100, math.MaxInt64} {
		group := &subscriptionbiz.SubscriptionGroup{ID: 7, PriceQuota: price}
		plan := &subscriptionbiz.SubscriptionPlan{ID: 8, PriceQuota: price, Group: group}
		for _, catalog := range []any{group, plan} {
			raw, err := jsonx.Marshal(normalizeSubscriptionResponse(catalog))
			require.NoError(t, err)
			var reply struct {
				PriceQuota        int64  `json:"price_quota"`
				PriceCurrency     string `json:"price_currency"`
				WalletPriceAmount *int64 `json:"wallet_price_amount"`
				WalletCurrency    string `json:"wallet_currency"`
			}
			require.NoError(t, jsonx.Unmarshal(raw, &reply))
			require.Equal(t, price, reply.PriceQuota, "catalog preserves the nominal CNY price")
			require.Equal(t, "CNY", reply.PriceCurrency)
			require.Equal(t, "USD", reply.WalletCurrency)
			if price == 100 {
				require.NotNil(t, reply.WalletPriceAmount)
				require.EqualValues(t, 2_000_000, *reply.WalletPriceAmount, "quote uses the server's recharge multiplier")
			} else {
				require.Nil(t, reply.WalletPriceAmount, "overflow must never produce a guessed wallet quote")
			}
		}
	}
}

func TestSubscriptionPaymentUsesConfiguredCNYPrice(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "price-admin")
	for _, tt := range []struct {
		name, extra  string
		price        int64
		plan, v2, ok bool
	}{
		{name: "default price", price: 100, ok: true},
		{name: "matching quoted price", price: 100, extra: `,"money_cents":10000,"currency":"CNY"`, ok: true},
		{name: "underpriced group", price: 100, extra: `,"money_cents":1`},
		{name: "underpriced plan", price: 100, extra: `,"money_cents":1`, plan: true},
		{name: "negative price override", price: 100, extra: `,"money_cents":-1`},
		{name: "wrong currency", price: 100, extra: `,"currency":"USD"`},
		{name: "V2 wrong currency", price: 100, extra: `,"currency":"USD"`, plan: true, v2: true},
		{name: "overflow with client override", price: math.MaxInt64/100 + 1, extra: `,"money_cents":1`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
			billing := &adminHTTPBillingClient{}
			srv := newAdminHTTPSubscriptionPaymentTestServer(&adminHTTPIdentityClient{validateValid: true, validateUserID: 42}, billing)
			create := httptest.NewRequest(http.MethodPost, "/api/v1/admin/subscription-groups", strings.NewReader(fmt.Sprintf(`{"name":"price-policy","status":1,"price_quota":%d,"duration_days":30}`, tt.price)))
			create.Header.Set("Authorization", "Bearer price-admin")
			created := httptest.NewRecorder()
			srv.ServeHTTP(created, create)
			require.Contains(t, created.Body.String(), `"success":true`)
			idField := `"group_id":1`
			if tt.plan {
				create = httptest.NewRequest(http.MethodPost, "/api/v1/admin/subscription-plans", strings.NewReader(fmt.Sprintf(`{"group_id":1,"name":"price-plan","price_quota":%d,"validity_days":30,"for_sale":true}`, tt.price)))
				create.Header.Set("Authorization", "Bearer price-admin")
				created = httptest.NewRecorder()
				srv.ServeHTTP(created, create)
				require.Contains(t, created.Body.String(), `"success":true`)
				idField = `"plan_id":1`
			}
			if tt.v2 {
				t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "true")
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions/purchase/payment", strings.NewReader("{"+idField+tt.extra+"}"))
			req.Header.Set("Authorization", "Bearer price-buyer")
			req.Header.Set("Idempotency-Key", "price-authority")
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			require.Equal(t, 200, rec.Code, rec.Body.String())
			var reply struct {
				Success bool `json:"success"`
			}
			require.NoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &reply))
			require.Equal(t, tt.ok, reply.Success, rec.Body.String())
			if tt.ok {
				require.NotNil(t, billing.paymentCreateReq)
				require.EqualValues(t, 10_000, billing.paymentCreateReq.MoneyCents)
				require.Equal(t, "CNY", billing.paymentCreateReq.Currency)
			} else {
				require.Nil(t, billing.paymentCreateReq, "invalid price must be rejected before a provider order is created")
			}
		})
	}
}
