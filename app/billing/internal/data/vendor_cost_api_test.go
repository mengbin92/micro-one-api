package data

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/testutil"
)

func costQuery(provider string) biz.VendorCostQuery {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	return biz.VendorCostQuery{Scope: biz.VendorScope{Provider: provider, Account: "org-fixture", Project: "project-fixture", Currency: "USD", Unit: "currency", Start: start, End: start.Add(24 * time.Hour), Timezone: "UTC", Granularity: "account_period"}, CredentialBindingEvidence: "isolated-account-reviewed", CSVReportID: strings.Repeat("a", 64), CSVTotal: "0.123456789012345678"}
}
func costPageFixture(q biz.VendorCostQuery, amount any) []byte {
	b, _ := jsonx.Marshal(map[string]any{"data": []any{map[string]any{"start_time": q.Scope.Start.Unix(), "end_time": q.Scope.End.Unix(), "results": []any{map[string]any{"amount": map[string]any{"value": amount, "currency": "usd"}, "project_id": q.Scope.Project}}}}, "has_more": false, "next_page": nil})
	return b
}
func archivedPage(raw []byte) biz.VendorCostPage {
	return biz.VendorCostPage{Body: raw, Digest: costPageHash(raw)}
}

func TestCostAPIDecimalAndComparableScope(t *testing.T) {
	q := costQuery("openai")
	raw := costPageFixture(q, jsonx.Number(q.CSVTotal))
	e, err := ReplayVendorCostPages(q, []biz.VendorCostPage{archivedPage(raw)}, q.Scope.End)
	require.NoError(t, err)
	require.True(t, e.CSVMatched)
	require.Equal(t, q.CSVTotal, *e.Rows[0].Amount)
	require.Nil(t, e.Rows[0].Buckets)
	id := e.ID()
	e.FetchedAt = e.FetchedAt.Add(time.Hour)
	require.Equal(t, id, e.ID())
	q.CSVTotal = "0"
	e, err = ReplayVendorCostPages(q, []biz.VendorCostPage{archivedPage(raw)}, q.Scope.End)
	require.NoError(t, err)
	require.False(t, e.CSVMatched, "zero reference denominator must not hide a difference")
	q.CSVTotal = "0.000001"
	e, err = ReplayVendorCostPages(q, []biz.VendorCostPage{archivedPage(costPageFixture(q, jsonx.Number("1e-6")))}, q.Scope.End)
	require.NoError(t, err)
	require.True(t, e.CSVMatched)
	require.Equal(t, "0.000001", *e.Rows[0].Amount, "valid scientific JSON number must stay exact")
}

func TestAnthropicCentsAreConvertedExactly(t *testing.T) {
	q := costQuery("anthropic")
	q.CSVTotal = "1.2378912"
	raw, _ := jsonx.Marshal(map[string]any{"data": []any{map[string]any{"starting_at": q.Scope.Start.Format(time.RFC3339), "ending_at": q.Scope.End.Format(time.RFC3339), "results": []any{map[string]any{"amount": "123.78912", "currency": "USD", "workspace_id": q.Scope.Project}}}}, "has_more": false, "next_page": nil})
	e, err := ReplayVendorCostPages(q, []biz.VendorCostPage{archivedPage(raw)}, q.Scope.End)
	require.NoError(t, err)
	require.Equal(t, "1.2378912", *e.Rows[0].Amount)
	require.True(t, e.CSVMatched)
}

func TestAnthropicWorkspaceSelectionUsesDocumentedGroupingAndRetainsAllPages(t *testing.T) {
	q := costQuery("anthropic")
	q.CSVTotal = "1"
	raw, _ := jsonx.Marshal(map[string]any{"data": []any{map[string]any{"starting_at": q.Scope.Start.Format(time.RFC3339), "ending_at": q.Scope.End.Format(time.RFC3339), "results": []any{map[string]any{"amount": "100", "currency": "USD", "workspace_id": q.Scope.Project}, map[string]any{"amount": "999", "currency": "USD", "workspace_id": "another-workspace"}}}}, "has_more": false, "next_page": nil})
	client := &http.Client{Transport: costTransport(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "api.anthropic.com", req.URL.Host)
		require.Empty(t, req.URL.Query().Get("workspace_ids[]"))
		require.Equal(t, []string{"workspace_id", "description"}, req.URL.Query()["group_by[]"])
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})}
	e, err := NewVendorCostReader(&Data{vendorCostClient: client}, "fixture").FetchCosts(context.Background(), q)
	require.NoError(t, err)
	require.True(t, e.CSVMatched)
	require.Equal(t, "1", *e.Rows[0].Amount)
	require.Equal(t, raw, e.Pages[0].Body, "full organization page remains available for scope review")
}

func TestCostAPIFailsClosedForIncompleteOrAmbiguousEvidence(t *testing.T) {
	q := costQuery("openai")
	base := costPageFixture(q, jsonx.Number("0"))
	for name, raw := range map[string][]byte{
		"missing_presence": []byte(`{"data":[]}`),
		"duplicate_key":    []byte(`{"data":[],"has_more":true,"has_more":false}`),
		"missing_days":     []byte(`{"data":[],"has_more":false}`),
		"unfinished_pages": []byte(`{"data":[],"has_more":true,"next_page":"next"}`),
		"currency":         []byte(strings.ReplaceAll(string(base), "usd", "cny")),
		"project":          []byte(strings.ReplaceAll(string(base), q.Scope.Project, "other-project")),
		"unknown_amount":   costPageFixture(q, nil),
		"exponent_budget":  costPageFixture(q, jsonx.Number("1e-999")),
		"terminal_cursor":  []byte(strings.ReplaceAll(string(base), `"next_page":null`, `"next_page":"unexpected"`)),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ReplayVendorCostPages(q, []biz.VendorCostPage{archivedPage(raw)}, q.Scope.End)
			require.Error(t, err)
		})
	}
	page := archivedPage(base)
	page.Digest = strings.Repeat("0", 64)
	_, err := ReplayVendorCostPages(q, []biz.VendorCostPage{page}, q.Scope.End)
	require.Error(t, err)
	q.Scope.Model = "cannot-infer-model"
	_, err = ReplayVendorCostPages(q, []biz.VendorCostPage{archivedPage(base)}, q.Scope.End)
	require.Error(t, err)
}

type costTransport func(*http.Request) (*http.Response, error)

func (f costTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCostAPIUsesFixedDestinationAndNoCredentialRedirect(t *testing.T) {
	q := costQuery("openai")
	calls := 0
	client := &http.Client{Transport: costTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "api.openai.com", r.URL.Host)
		require.Equal(t, "/v1/organization/costs", r.URL.Path)
		require.Equal(t, "org-fixture", r.Header.Get("OpenAI-Organization"))
		require.Equal(t, "Bearer fixture-secret-never-log", r.Header.Get("Authorization"))
		require.Equal(t, q.Scope.Project, r.URL.Query().Get("project_ids"))
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://evil.invalid"}}, Body: io.NopCloser(strings.NewReader("fixture-secret-never-log"))}, nil
	})}
	r := NewVendorCostReader(&Data{vendorCostClient: client}, "fixture-secret-never-log")
	_, err := r.FetchCosts(context.Background(), q)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "fixture-secret")
	require.Equal(t, 1, calls)
}

func TestCostAPIPaginationAndCursorBudget(t *testing.T) {
	q := costQuery("openai")
	q.Scope.End = q.Scope.End.Add(24 * time.Hour)
	q.CSVTotal = "0"
	calls := 0
	client := &http.Client{Transport: costTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		day := q
		day.Scope.Start = q.Scope.Start.Add(time.Duration(calls-1) * 24 * time.Hour)
		day.Scope.End = day.Scope.Start.Add(24 * time.Hour)
		raw := string(costPageFixture(day, jsonx.Number("0")))
		if calls == 1 {
			raw = strings.ReplaceAll(raw, `"has_more":false`, `"has_more":true`)
			raw = strings.ReplaceAll(raw, `"next_page":null`, `"next_page":"page-2"`)
		} else {
			require.Equal(t, "page-2", r.URL.Query().Get("page"))
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw))}, nil
	})}
	e, err := NewVendorCostReader(&Data{vendorCostClient: client}, "fixture").FetchCosts(context.Background(), q)
	require.NoError(t, err)
	require.Len(t, e.Pages, 2)
	require.True(t, e.CSVMatched)
	client.Transport = costTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[],"has_more":true,"next_page":"same"}`))}, nil
	})
	_, err = NewVendorCostReader(&Data{vendorCostClient: client}, "fixture").FetchCosts(context.Background(), q)
	require.ErrorContains(t, err, "repeated")
}

func TestAPIImportRequiresStoredCSVAndPreservesOriginalCorrectionChain(t *testing.T) {
	db := testutil.RoutingContextDB(t, "sqlite")
	r := &vendorReconciliationRepo{data: &Data{db: db}}
	ctx := context.Background()
	q := costQuery("openai")
	q.CSVTotal = "0"
	m := vendorRepoManifest()
	m.Scope = q.Scope
	m.Mappings[0].Provider, m.Mappings[0].Account, m.Mappings[0].Project = q.Scope.Provider, q.Scope.Account, q.Scope.Project
	zero := "0"
	rows := []biz.VendorBillRow{{Amount: &zero}}
	csv, err := biz.ReconcileVendor(m, rows, nil)
	require.NoError(t, err)
	q.CSVReportID = csv.ID
	e, err := ReplayVendorCostPages(q, []biz.VendorCostPage{archivedPage(costPageFixture(q, jsonx.Number("0")))}, q.Scope.End)
	require.NoError(t, err)
	_, err = r.ValidateCostEvidence(ctx, e)
	require.Error(t, err, "JSON claim cannot replace independent stored CSV")
	require.NoError(t, r.StoreVendorReconciliation(ctx, &csv))
	_, err = r.ValidateCostEvidence(ctx, e)
	require.NoError(t, err)
	m.Version = "API-1"
	m.EvidenceID = e.ID()
	m.CostBasis = "provider_reported"
	m.Supersedes = csv.ID
	api, err := biz.ReconcileVendor(m, e.Rows, nil)
	require.NoError(t, err)
	api.CostAPIEvidence = &e
	require.NoError(t, r.StoreVendorReconciliation(ctx, &api))
	require.NoError(t, r.StoreVendorReconciliation(ctx, &api))
	require.Equal(t, "0.000000000000000000", *api.CorrectionDelta)
	api.CostAPIEvidence.Pages[0].Digest = "changed"
	require.Error(t, r.StoreVendorReconciliation(ctx, &api))
	var original vendorReportModel
	require.NoError(t, db.Where("id = ?", csv.ID).First(&original).Error)
	var saved biz.VendorReconciliation
	require.NoError(t, jsonx.Unmarshal([]byte(original.Payload), &saved))
	require.Nil(t, saved.CostAPIEvidence)
	require.Equal(t, "invoice", saved.CostBasis)
	var ledgers int64
	require.NoError(t, db.Table("billing_ledgers").Count(&ledgers).Error)
	require.Zero(t, ledgers)
}
