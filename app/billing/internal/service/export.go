package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/domain/authorization"
	"strconv"
	"strings"
	"time"
)

// CSVText prevents spreadsheet formula evaluation in exported free text.
func CSVText(value string) string {
	if strings.ContainsAny(strings.TrimLeft(value, " \t\r\n")[:min(1, len(strings.TrimLeft(value, " \t\r\n")))], "=+-@") {
		return "'" + value
	}
	return value
}
func (s *BillingService) ExportCostReport(ctx context.Context, req *billingv1.ExportCostReportRequest) (*billingv1.ExportArtifactReply, error) {
	q := req.GetQuery()
	if q == nil {
		return nil, status.Error(codes.InvalidArgument, "report query is required")
	}
	filter := biz.UsageFilter{GroupBy: q.GetGroupBy(), UserID: q.GetUserId(), ChannelID: q.GetChannelId(), SubscriptionAccountID: q.GetSubscriptionAccountId(), Model: q.GetModel(), Type: q.GetType()}
	if len(filter.GroupBy) == 0 {
		filter.GroupBy = []string{biz.UsageDimUser, biz.UsageDimDay, biz.UsageDimModel}
	}
	if q.GetStartTime().IsValid() {
		filter.StartTime = q.GetStartTime().AsTime()
	}
	if q.GetEndTime().IsValid() {
		filter.EndTime = q.GetEndTime().AsTime()
	}
	rows, _, err := s.uc.ExportCostReport(ctx, filter, req.GetIncludeCosts())
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	header := []string{"user_id", "channel_id", "subscription_account_id", "model", "token_name", "type", "day", "hour", "quota", "count"}
	if req.GetIncludeCosts() {
		header = append(header, "upstream_cost", "gross_profit")
	}
	if err = writer.Write(header); err != nil {
		return nil, err
	}
	for _, row := range rows {
		record := []string{CSVText(row.UserID), fmt.Sprint(row.ChannelID), fmt.Sprint(row.SubscriptionAccountID), CSVText(row.Model), CSVText(row.TokenName), CSVText(row.Type), row.Day, row.Hour, fmt.Sprint(row.Quota), fmt.Sprint(row.Count)}
		if req.GetIncludeCosts() {
			if !row.CostFieldsVisible {
				return nil, authorization.ErrDenied
			}
			record = append(record, fmt.Sprint(row.UpstreamCost), fmt.Sprint(row.GrossProfit))
		}
		if err = writer.Write(record); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		return nil, err
	}
	return &billingv1.ExportArtifactReply{ContentType: "text/csv; charset=utf-8", FileName: "cost-report.csv", Body: buf.Bytes()}, nil
}
func (s *BillingService) ExportRedeemCodes(ctx context.Context, _ *billingv1.ExportRedeemCodesRequest) (*billingv1.ExportArtifactReply, error) {
	rows, err := s.uc.ExportRedeemCodes(ctx)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	if err = writer.Write([]string{"id", "code", "name", "amount", "count", "status", "created_at"}); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if err = writer.Write([]string{fmt.Sprint(row.ID), CSVText(row.Code), CSVText(row.Name), fmt.Sprint(row.Amount), fmt.Sprint(row.Count), fmt.Sprint(row.Status), strconv.FormatInt(row.CreatedAt.Unix(), 10)}); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		return nil, err
	}
	return &billingv1.ExportArtifactReply{ContentType: "text/csv; charset=utf-8", FileName: "redemptions.csv", Body: buf.Bytes()}, nil
}
func (s *BillingService) RunReconciliation(ctx context.Context, req *billingv1.RunReconciliationRequest) (*billingv1.GetReconciliationRunResponse, error) {
	if strings.TrimSpace(req.GetReason()) == "" {
		return nil, status.Error(codes.InvalidArgument, "reason is required")
	}
	ctx = authorization.WithWriteReason(ctx, req.GetReason())
	row, err := s.reconUc.RunReconciliation(ctx)
	if err != nil {
		return nil, err
	}
	out, err := reconciliationRunToProto(row)
	if err != nil {
		return nil, err
	}
	return &billingv1.GetReconciliationRunResponse{Run: out}, nil
}

func (s *BillingService) ResetAccountBalance(ctx context.Context, req *billingv1.ResetAccountBalanceRequest) (*billingv1.TopUpQuotaResponse, error) {
	balance, err := s.uc.ResetBalance(ctx, req.GetUserId(), req.GetTargetBalance(), req.GetExpectedBalance(), req.GetReason(), req.GetRequestId())
	if err != nil {
		return nil, err
	}
	return &billingv1.TopUpQuotaResponse{Success: true, NewBalance: balance}, nil
}

func (s *BillingService) ExportLedgerEntries(ctx context.Context, req *billingv1.ListLedgerRequest) (*billingv1.ExportArtifactReply, error) {
	orderBy, order, err := normalizeLedgerOrder(req.OrderBy)
	if err != nil {
		return nil, err
	}
	options := biz.LedgerListOptions{UserID: req.UserId, Page: req.Page, PageSize: req.PageSize, Type: req.Type, SubscriptionAccountID: req.SubscriptionAccountId, OrderBy: orderBy, Order: order}
	if req.StartTime.IsValid() {
		options.StartTime = req.StartTime.AsTime()
	}
	if req.EndTime.IsValid() {
		options.EndTime = req.EndTime.AsTime()
	}
	rows, err := s.uc.ExportLedgerEntries(ctx, options)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	writer.Write([]string{"id", "user_id", "type", "amount", "balance_after", "model", "prompt_tokens", "completion_tokens", "upstream_cost", "cost_fields_visible", "created_at", "reference_id"})
	for _, l := range rows {
		cost := ""
		if l.CostFieldsVisible {
			cost = strconv.FormatInt(l.UpstreamCost, 10)
		}
		writer.Write([]string{strconv.FormatUint(uint64(l.ID), 10), CSVText(l.UserID), CSVText(l.Type), strconv.FormatInt(l.Amount, 10), strconv.FormatInt(l.BalanceAfter, 10), CSVText(l.ModelName), strconv.FormatInt(l.PromptTokens, 10), strconv.FormatInt(l.CompletionTokens, 10), cost, strconv.FormatBool(l.CostFieldsVisible), l.CreatedAt.UTC().Format(time.RFC3339), CSVText(l.ReferenceID)})
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		return nil, err
	}
	return &billingv1.ExportArtifactReply{ContentType: "text/csv; charset=utf-8", FileName: "ledger.csv", Body: buf.Bytes()}, nil
}
