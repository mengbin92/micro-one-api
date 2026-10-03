package server

import (
	"google.golang.org/protobuf/types/known/timestamppb"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/platform/authz"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func handleCostReportExport(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := &billingv1.AggregateUsageRequest{GroupBy: strings.Split(r.URL.Query().Get("group_by"), ","), UserId: r.URL.Query().Get("user_id"), ChannelId: getQueryInt64(r, "channel_id", 0), SubscriptionAccountId: getQueryInt64(r, "subscription_account_id", 0), Model: r.URL.Query().Get("model"), Type: r.URL.Query().Get("type")}
	if len(q.GroupBy) == 1 && q.GroupBy[0] == "" {
		q.GroupBy = nil
	}
	if start := getQueryInt64(r, "start_time", 0); start > 0 {
		q.StartTime = timestamppb.New(time.Unix(start, 0))
	}
	if end := getQueryInt64(r, "end_time", 0); end > 0 {
		q.EndTime = timestamppb.New(time.Unix(end, 0))
	}
	out, err := svc.ExportCostReport(r.Context(), &billingv1.ExportCostReportRequest{Query: q, IncludeCosts: r.URL.Query().Get("include_costs") != "false"})
	if err != nil {
		authz.WriteHTTPError(w, err)
		return
	}
	writeBillingExport(w, out)
}
func handleAuthorizedRedemptionExport(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	out, err := svc.ExportRedeemCodes(r.Context())
	if err != nil {
		authz.WriteHTTPError(w, err)
		return
	}
	writeBillingExport(w, out)
}
func writeBillingExport(w http.ResponseWriter, out *billingv1.ExportArtifactReply) {
	w.Header().Set("Content-Type", out.ContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+out.FileName+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.Body)
}

func handleResetAccountBalance(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	part := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/admin/accounts/"), "/balance:reset")
	id, err := strconv.ParseInt(part, 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "invalid account id", nil))
		return
	}
	var input struct {
		TargetBalance   int64  `json:"target_balance"`
		ExpectedBalance *int64 `json:"expected_balance"`
		Reason          string `json:"reason"`
		RequestID       string `json:"request_id"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	if input.ExpectedBalance == nil {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "expected_balance is required", nil))
		return
	}
	out, err := svc.ResetAccountBalance(r.Context(), &billingv1.ResetAccountBalanceRequest{UserId: strconv.FormatInt(id, 10), TargetBalance: input.TargetBalance, ExpectedBalance: *input.ExpectedBalance, Reason: input.Reason, RequestId: input.RequestID})
	if err != nil {
		authz.WriteHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", out))
}
