package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"micro-one-api/app/admin/internal/service"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	subscriptiondata "micro-one-api/domain/subscription/data"
)

// newPlanLifecycleTestServer wires an in-memory subscription repo so the plan
// for-sale lifecycle (create, list filter, toggle, off-shelf guard) can be
// exercised end-to-end without a database.
func newPlanLifecycleTestServer() http.Handler {
	adminSvc := service.NewAdminService(nil, nil, nil, nil)
	repo := subscriptiondata.NewMemoryRepositoryForTest()
	adminSvc.SetSubscriptionUsecases(
		subscriptionbiz.NewSubscriptionUsecase(repo, repo),
		subscriptionbiz.NewGroupUsecase(repo),
		subscriptionbiz.NewPlanUsecase(repo, repo),
	)
	return NewHTTPServer(":0", adminSvc, nil)
}

func TestPlanLifecycle_ForSaleFilterAndToggle(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	srv := newPlanLifecycleTestServer()

	// Create an enabled group so plan creation passes preparePlan.
	createGroup(t, srv, `{"name":"pro","display_name":"Pro","platform":"openai","status":1}`)

	// Create two plans; both start for_sale=true (Create forces it).
	createPlan(t, srv, `{"group_id":1,"name":"Monthly","price_quota":10,"validity_days":30,"for_sale":true}`)
	createPlan(t, srv, `{"group_id":1,"name":"Yearly","price_quota":100,"validity_days":365,"for_sale":true}`)

	// ?for_sale=true returns both on-shelf plans.
	onSale := listPlans(t, srv, "true")
	if len(onSale) != 2 {
		t.Fatalf("for_sale=true returned %d plans, want 2", len(onSale))
	}

	// Take Monthly off-shelf via the narrow toggle endpoint.
	togglePlanSale(t, srv, 1, false)

	// ?for_sale=true now returns only Yearly.
	onSale = listPlans(t, srv, "true")
	if len(onSale) != 1 || onSale[0].Name != "Yearly" {
		var names []string
		for _, p := range onSale {
			names = append(names, p.Name)
		}
		t.Fatalf("for_sale=true after toggle = %v, want [Yearly]", names)
	}

	// ?for_sale=false returns only Monthly.
	offSale := listPlans(t, srv, "false")
	if len(offSale) != 1 || offSale[0].Name != "Monthly" {
		t.Fatalf("for_sale=false after toggle returned %d plans, want 1 (Monthly)", len(offSale))
	}

	// No filter returns both (full lifecycle audit view).
	all := listPlans(t, srv, "")
	if len(all) != 2 {
		t.Fatalf("no filter returned %d plans, want 2", len(all))
	}

	// Re-shelf Monthly.
	togglePlanSale(t, srv, 1, true)
	onSale = listPlans(t, srv, "true")
	if len(onSale) != 2 {
		t.Fatalf("for_sale=true after re-shelf = %d, want 2", len(onSale))
	}
}

type planDTO struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	ForSale bool   `json:"for_sale"`
}

func createGroup(t *testing.T, srv http.Handler, body string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/subscription-groups", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("create group status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func createPlan(t *testing.T, srv http.Handler, body string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/subscription-plans", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("create plan status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func togglePlanSale(t *testing.T, srv http.Handler, id int64, forSale bool) {
	t.Helper()
	body := `{"for_sale":` + strconv.FormatBool(forSale) + `}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/subscription-plans/"+strconv.FormatInt(id, 10)+"/for-sale", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("toggle for-sale status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func listPlans(t *testing.T, srv http.Handler, forSale string) []planDTO {
	t.Helper()
	path := "/api/v1/admin/subscription-plans"
	if forSale != "" {
		path += "?for_sale=" + forSale
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list plans status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []planDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode plan list: %v body=%s", err, rec.Body.String())
	}
	return resp.Data
}
