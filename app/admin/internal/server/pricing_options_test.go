package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	"micro-one-api/pkg/jsonx"
)

type pricingBatchStore struct {
	adminHTTPSystemOptionsStore
	batchCalls int
}

func (s *pricingBatchStore) SetMany(_ context.Context, values map[string]string, revisions map[string]int64) error {
	s.batchCalls++
	for key, value := range values {
		if revisions[key] != 1 {
			return biz.ErrSystemOptionConflict
		}
		s.values[key] = value
	}
	return nil
}

func TestPricingOptionsUsesOneOwnerBatch(t *testing.T) {
	store := &pricingBatchStore{adminHTTPSystemOptionsStore: adminHTTPSystemOptionsStore{values: map[string]string{}}}
	svc := service.NewAdminService(nil, nil, nil, biz.NewSystemOptionsUsecase(store))
	r := httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(`{"values":{"ModelPrice":"{}","ModelRatio":"{}","CompletionRatio":"{}"},"expected_revisions":{"ModelPrice":"1","ModelRatio":"1","CompletionRatio":"1"},"reason":"reviewed pricing"}`))
	w := httptest.NewRecorder()
	handleOneAPIOptions(w, r, svc)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, 1, store.batchCalls)
	require.Len(t, store.values, 3)
}

func TestServiceTierPricingUsesOneRevisionCheckedOwnerBatch(t *testing.T) {
	store := &pricingBatchStore{adminHTTPSystemOptionsStore: adminHTTPSystemOptionsStore{values: map[string]string{}}}
	svc := service.NewAdminService(nil, nil, nil, biz.NewSystemOptionsUsecase(store))
	body := `{"values":{"ServiceTierPricing":"{\"enabled\":false}"},"expected_revisions":{"ServiceTierPricing":"1"},"reason":"disable tier billing"}`
	w := httptest.NewRecorder()
	handleOneAPIOptions(w, httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(body)), svc)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, 1, store.batchCalls)
	require.Equal(t, `{"enabled":false}`, store.values["ServiceTierPricing"])
}

func TestServiceTierPricingBFFCannotBypassProcurementPermission(t *testing.T) {
	store := &pricingBatchStore{adminHTTPSystemOptionsStore: adminHTTPSystemOptionsStore{values: map[string]string{}}}
	uc := biz.NewSystemOptionsUsecase(store)
	svc := service.NewAdminService(nil, nil, nil, uc)
	uc.SetAuthorization(&authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{
		"system.option.update": authztest.All(), "system.option.pricing.update": authztest.All(), "billing.pricing.update": authztest.All(),
	}})
	body := `{"values":{"ServiceTierPricing":"{\"enabled\":false}"},"expected_revisions":{"ServiceTierPricing":"1"},"reason":"missing procurement permission"}`
	w := httptest.NewRecorder()
	handleOneAPIOptions(w, httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(body)).WithContext(authztest.Context()), svc)
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	require.Zero(t, store.batchCalls)
}

func TestServiceTierPricingBFFMarksScopedTariffsAsRestricted(t *testing.T) {
	store := &pricingBatchStore{adminHTTPSystemOptionsStore: adminHTTPSystemOptionsStore{values: map[string]string{"ServiceTierPricing": `{"enabled":true,"upstream_prices":{"secret":7}}`}}}
	uc := biz.NewSystemOptionsUsecase(store)
	svc := service.NewAdminService(nil, nil, nil, uc)
	uc.SetAuthorization(&authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{
		"system.option.read": authztest.All(), "billing.routing_policy.read": authztest.All(),
		"billing.pricing.read": authztest.Resources(71), "billing.upstream_cost.read": authztest.Resources(71),
	}})
	w := httptest.NewRecorder()
	handleOneAPIOptions(w, httptest.NewRequest(http.MethodGet, "/api/option/", nil).WithContext(authztest.Context()), svc)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var response struct {
		Data []service.OneAPIOption `json:"data"`
	}
	require.NoError(t, jsonx.Unmarshal(w.Body.Bytes(), &response))
	found := false
	for _, option := range response.Data {
		if option.Key == "ServiceTierPricing" {
			found = true
			require.True(t, option.Restricted)
			require.Empty(t, option.Value, "hidden configuration must not become a disabled placeholder")
		}
	}
	require.True(t, found)
}

func TestPricingBatchRequiresOwnerAuthorization(t *testing.T) {
	store := &pricingBatchStore{adminHTTPSystemOptionsStore: adminHTTPSystemOptionsStore{values: map[string]string{}}}
	uc := biz.NewSystemOptionsUsecase(store)
	svc := service.NewAdminService(nil, nil, nil, uc)
	uc.SetAuthorization(&authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{}})
	r := httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(`{"values":{"ModelPrice":"{}","ModelRatio":"{}","CompletionRatio":"{}"},"expected_revisions":{"ModelPrice":"1","ModelRatio":"1","CompletionRatio":"1"},"reason":"reviewed pricing"}`)).WithContext(authztest.Context())
	w := httptest.NewRecorder()
	handleOneAPIOptions(w, r, svc)
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	require.Zero(t, store.batchCalls)
}

func TestPricingBatchRejectsInvalidInputBeforeOwnerWrite(t *testing.T) {
	for _, change := range []struct{ key, value string }{
		{"ModelPrice", `null`}, {"ModelPrice", `[]`}, {"ModelPrice", `{"a":3}`},
		{"ModelPrice", `{"a":{"input_price":-1}}`}, {"ModelPrice", `{"a":{"input_price":null}}`},
		{"ModelRatio", `{"a":1e999}`}, {"ModelRatio", `{"a":null}`}, {"CompletionRatio", `{" ":1}`},
		{"unexpected", `{}`},
	} {
		t.Run(change.key+change.value, func(t *testing.T) {
			store := &pricingBatchStore{adminHTTPSystemOptionsStore: adminHTTPSystemOptionsStore{values: map[string]string{}}}
			svc := service.NewAdminService(nil, nil, nil, biz.NewSystemOptionsUsecase(store))
			values := map[string]string{"ModelPrice": `{}`, "ModelRatio": `{}`, "CompletionRatio": `{}`}
			values[change.key] = change.value
			body, err := jsonx.Marshal(map[string]any{"values": values, "expected_revisions": map[string]string{"ModelPrice": "1", "ModelRatio": "1", "CompletionRatio": "1"}})
			require.NoError(t, err)
			w := httptest.NewRecorder()
			handleOneAPIOptions(w, httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(string(body))), svc)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			require.Zero(t, store.batchCalls)
		})
	}
	for _, body := range []string{`{"values":null}`, `{"values":{}}`, `{"values":{"ModelPrice":"{}","ModelRatio":"{}","CompletionRatio":"{}"}}`, `{"values":{"ModelPrice":"{}"},"key":"Theme","value":"dark"}`} {
		store := &pricingBatchStore{adminHTTPSystemOptionsStore: adminHTTPSystemOptionsStore{values: map[string]string{}}}
		svc := service.NewAdminService(nil, nil, nil, biz.NewSystemOptionsUsecase(store))
		w := httptest.NewRecorder()
		handleOneAPIOptions(w, httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(body)), svc)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		require.Zero(t, store.batchCalls)
	}
}
