package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	billingv1 "micro-one-api/api/billing/v1"
	commonv1 "micro-one-api/api/common/v1"
	"micro-one-api/internal/billing/biz"

	"google.golang.org/protobuf/types/known/timestamppb"
)

type BillingService struct {
	billingv1.UnimplementedBillingServiceServer
	uc             *biz.BillingUsecase
	paymentUc      *biz.PaymentUsecase
	alipayVerifier biz.PaymentNotifyVerifier
	reconUc        *biz.ReconciliationUsecase
}

func NewBillingService(uc *biz.BillingUsecase, reconUc *biz.ReconciliationUsecase, paymentUc *biz.PaymentUsecase, alipayVerifier biz.PaymentNotifyVerifier) *BillingService {
	return &BillingService{uc: uc, reconUc: reconUc, paymentUc: paymentUc, alipayVerifier: alipayVerifier}
}

func (s *BillingService) ReserveQuota(ctx context.Context, req *billingv1.ReserveQuotaRequest) (*billingv1.ReserveQuotaResponse, error) {
	reservation, err := s.uc.ReserveQuota(ctx, req.UserId, req.RequestId, req.EstimatedTokens, req.Model, req.ChannelId)
	if err != nil {
		return &billingv1.ReserveQuotaResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}

	return &billingv1.ReserveQuotaResponse{
		Success:        true,
		ReservationId:  reservation.ReservationID,
		ReservedAmount: reservation.Amount,
	}, nil
}

func (s *BillingService) CommitQuota(ctx context.Context, req *billingv1.CommitQuotaRequest) (*billingv1.CommitQuotaResponse, error) {
	committedAmount, refundAmount, err := s.uc.CommitQuotaWithUsage(ctx, req.ReservationId, req.ActualTokens, req.Success, biz.LedgerUsage{
		TokenName:        req.TokenName,
		Endpoint:         req.Endpoint,
		PromptTokens:     req.PromptTokens,
		CompletionTokens: req.CompletionTokens,
		CacheReadTokens:  req.CacheReadTokens,
		ElapsedTime:      req.ElapsedTime,
		IsStream:         req.IsStream,
	})
	if err != nil {
		return &billingv1.CommitQuotaResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}

	return &billingv1.CommitQuotaResponse{
		Success:         true,
		CommittedAmount: committedAmount,
		RefundAmount:    refundAmount,
	}, nil
}

func (s *BillingService) ReleaseQuota(ctx context.Context, req *billingv1.ReleaseQuotaRequest) (*billingv1.ReleaseQuotaResponse, error) {
	err := s.uc.ReleaseQuota(ctx, req.ReservationId, req.Reason)
	if err != nil {
		return &billingv1.ReleaseQuotaResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}

	return &billingv1.ReleaseQuotaResponse{
		Success: true,
	}, nil
}

func (s *BillingService) GetAccountSnapshot(ctx context.Context, req *billingv1.GetAccountSnapshotRequest) (*billingv1.GetAccountSnapshotResponse, error) {
	account, err := s.uc.GetAccountSnapshot(ctx, req.UserId)
	if err != nil {
		return nil, err
	}

	return &billingv1.GetAccountSnapshotResponse{
		Snapshot: &commonv1.AccountSnapshot{
			UserId:       account.UserID,
			Quota:        account.Quota,
			UsedQuota:    account.UsedQuota,
			RequestCount: account.RequestCount,
			Group:        account.Group,
			GroupRatio:   account.GroupRatio(),
			FrozenQuota:  account.FrozenQuota,
		},
	}, nil
}

func (s *BillingService) BatchGetAccountSnapshots(ctx context.Context, req *billingv1.BatchGetAccountSnapshotsRequest) (*billingv1.BatchGetAccountSnapshotsResponse, error) {
	accounts, err := s.uc.BatchGetAccountSnapshots(ctx, req.GetUserIds())
	if err != nil {
		return nil, err
	}

	snapshots := make(map[string]*commonv1.AccountSnapshot, len(accounts))
	for userID, account := range accounts {
		snapshots[userID] = &commonv1.AccountSnapshot{
			UserId:       account.UserID,
			Quota:        account.Quota,
			UsedQuota:    account.UsedQuota,
			RequestCount: account.RequestCount,
			Group:        account.Group,
			GroupRatio:   account.GroupRatio(),
			FrozenQuota:  account.FrozenQuota,
		}
	}

	return &billingv1.BatchGetAccountSnapshotsResponse{
		Snapshots: snapshots,
	}, nil
}

func (s *BillingService) TopUpQuota(ctx context.Context, req *billingv1.TopUpQuotaRequest) (*billingv1.TopUpQuotaResponse, error) {
	newQuota, err := s.uc.TopUpQuota(ctx, req.UserId, req.OperatorId, req.Amount, req.Remark)
	if err != nil {
		return &billingv1.TopUpQuotaResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}

	return &billingv1.TopUpQuotaResponse{
		Success:  true,
		NewQuota: newQuota,
	}, nil
}

func (s *BillingService) CreateRedeemCode(ctx context.Context, req *billingv1.CreateRedeemCodeRequest) (*billingv1.CreateRedeemCodeResponse, error) {
	err := s.uc.CreateRedeemCode(ctx, req.Code, req.Name, req.Amount, req.Count, req.OperatorId)
	if err != nil {
		return &billingv1.CreateRedeemCodeResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}

	return &billingv1.CreateRedeemCodeResponse{
		Success: true,
	}, nil
}

func (s *BillingService) CreateRedeemCodesBatch(ctx context.Context, req *billingv1.CreateRedeemCodesBatchRequest) (*billingv1.CreateRedeemCodesBatchResponse, error) {
	codes, err := s.uc.CreateRedeemCodesBatch(ctx, req.Name, req.Amount, req.Count, req.BatchSize, req.OperatorId)
	if err != nil {
		return &billingv1.CreateRedeemCodesBatchResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}

	return &billingv1.CreateRedeemCodesBatchResponse{
		Success: true,
		Codes:   codes,
	}, nil
}

func (s *BillingService) GetRedeemCode(ctx context.Context, req *billingv1.GetRedeemCodeRequest) (*billingv1.GetRedeemCodeResponse, error) {
	code, err := s.uc.GetRedeemCode(ctx, req.Code)
	if err != nil {
		return &billingv1.GetRedeemCodeResponse{
			ErrorMessage: err.Error(),
		}, nil
	}

	return &billingv1.GetRedeemCodeResponse{
		RedeemCode: &commonv1.RedeemCode{
			Code:      code.Code,
			Name:      code.Name,
			Amount:    code.Amount,
			Count:     code.Count,
			Status:    code.Status,
			CreatedBy: code.CreatedBy,
			CreatedAt: toProtoTimestamp(code.CreatedAt),
			UpdatedAt: toProtoTimestamp(code.UpdatedAt),
		},
	}, nil
}

func (s *BillingService) ListRedeemCodes(ctx context.Context, req *billingv1.ListRedeemCodesRequest) (*billingv1.ListRedeemCodesResponse, error) {
	codes, total, err := s.uc.ListRedeemCodes(ctx, req.Page, req.PageSize)
	if err != nil {
		return nil, err
	}

	redeemCodes := make([]*commonv1.RedeemCode, len(codes))
	for i, code := range codes {
		redeemCodes[i] = &commonv1.RedeemCode{
			Code:      code.Code,
			Name:      code.Name,
			Amount:    code.Amount,
			Count:     code.Count,
			Status:    code.Status,
			CreatedBy: code.CreatedBy,
			CreatedAt: toProtoTimestamp(code.CreatedAt),
			UpdatedAt: toProtoTimestamp(code.UpdatedAt),
		}
	}

	return &billingv1.ListRedeemCodesResponse{
		Codes: redeemCodes,
		Total: total,
	}, nil
}

func (s *BillingService) SearchRedeemCodes(ctx context.Context, req *billingv1.SearchRedeemCodesRequest) (*billingv1.SearchRedeemCodesResponse, error) {
	codes, err := s.uc.SearchRedeemCodes(ctx, req.Keyword)
	if err != nil {
		return nil, err
	}

	redeemCodes := make([]*commonv1.RedeemCode, len(codes))
	for i, code := range codes {
		redeemCodes[i] = &commonv1.RedeemCode{
			Code:      code.Code,
			Name:      code.Name,
			Amount:    code.Amount,
			Count:     code.Count,
			Status:    code.Status,
			CreatedBy: code.CreatedBy,
			CreatedAt: toProtoTimestamp(code.CreatedAt),
			UpdatedAt: toProtoTimestamp(code.UpdatedAt),
		}
	}

	return &billingv1.SearchRedeemCodesResponse{
		Codes: redeemCodes,
	}, nil
}

func (s *BillingService) UpdateRedeemCode(ctx context.Context, req *billingv1.UpdateRedeemCodeRequest) (*billingv1.UpdateRedeemCodeResponse, error) {
	err := s.uc.UpdateRedeemCode(ctx, req.Code, req.Name, req.Amount, req.Status)
	if err != nil {
		return &billingv1.UpdateRedeemCodeResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}

	return &billingv1.UpdateRedeemCodeResponse{
		Success: true,
	}, nil
}

func (s *BillingService) DeleteRedeemCode(ctx context.Context, req *billingv1.DeleteRedeemCodeRequest) (*billingv1.DeleteRedeemCodeResponse, error) {
	err := s.uc.DeleteRedeemCode(ctx, req.Code)
	if err != nil {
		return &billingv1.DeleteRedeemCodeResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}

	return &billingv1.DeleteRedeemCodeResponse{
		Success: true,
	}, nil
}

func (s *BillingService) RedeemCode(ctx context.Context, req *billingv1.RedeemCodeRequest) (*billingv1.RedeemCodeResponse, error) {
	amount, newQuota, err := s.uc.RedeemCode(ctx, req.UserId, req.Code)
	if err != nil {
		return &billingv1.RedeemCodeResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}

	return &billingv1.RedeemCodeResponse{
		Success:  true,
		Amount:   amount,
		NewQuota: newQuota,
	}, nil
}

func (s *BillingService) ListLedger(ctx context.Context, req *billingv1.ListLedgerRequest) (*billingv1.ListLedgerResponse, error) {
	page := req.Page
	pageSize := req.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	// Allow up to 1000 for dashboard/statistics queries
	if pageSize > 1000 {
		pageSize = 1000
	}

	var ledgers []*biz.Ledger
	var total int64
	var err error

	// Parse time range
	var startTime, endTime time.Time
	if req.GetStartTime().IsValid() {
		startTime = req.GetStartTime().AsTime()
	}
	if req.GetEndTime().IsValid() {
		endTime = req.GetEndTime().AsTime()
	}

	// Use filtered query if type or time range is specified
	ledgerType := req.GetType()
	if ledgerType != "" || !startTime.IsZero() || !endTime.IsZero() {
		ledgers, total, err = s.uc.ListLedgersWithFilters(ctx, req.UserId, page, pageSize, ledgerType, startTime, endTime)
	} else {
		ledgers, total, err = s.uc.ListLedgers(ctx, req.UserId, page, pageSize)
	}
	if err != nil {
		return nil, err
	}

	entries := make([]*commonv1.LedgerEntry, len(ledgers))
	for i, ledger := range ledgers {
		entries[i] = &commonv1.LedgerEntry{
			Id:               fmt.Sprintf("%d", ledger.ID),
			UserId:           ledger.UserID,
			Amount:           ledger.Amount,
			BalanceAfter:     ledger.BalanceAfter,
			Type:             ledger.Type,
			ReferenceId:      ledger.ReferenceID,
			Remark:           ledger.Remark,
			CreatedAt:        toProtoTimestamp(ledger.CreatedAt),
			TokenName:        ledger.TokenName,
			ModelName:        ledger.ModelName,
			Quota:            ledger.Quota,
			PromptTokens:     ledger.PromptTokens,
			CompletionTokens: ledger.CompletionTokens,
			ChannelId:        ledger.ChannelID,
			ElapsedTime:      ledger.ElapsedTime,
			IsStream:         ledger.IsStream,
			Endpoint:         ledger.Endpoint,
		}
	}

	return &billingv1.ListLedgerResponse{
		Entries: entries,
		Total:   total,
	}, nil
}

func (s *BillingService) AggregateLedgerByDate(ctx context.Context, req *billingv1.AggregateLedgerByDateRequest) (*billingv1.AggregateLedgerByDateResponse, error) {
	ledgerType := req.GetType()
	if ledgerType == "" {
		ledgerType = "consume"
	}

	var startTime, endTime time.Time
	if req.GetStartTime().IsValid() {
		startTime = req.GetStartTime().AsTime()
	}
	if req.GetEndTime().IsValid() {
		endTime = req.GetEndTime().AsTime()
	}

	daily, models, err := s.uc.AggregateLedgerByDate(ctx, req.GetUserId(), ledgerType, startTime, endTime)
	if err != nil {
		return nil, err
	}

	dailyProto := make([]*billingv1.DailyUsage, len(daily))
	var totalQuota, totalPrompt, totalCompletion, totalCount int64
	for i, d := range daily {
		dailyProto[i] = &billingv1.DailyUsage{
			Date:             d.Date,
			Quota:            d.Quota,
			PromptTokens:     d.PromptTokens,
			CompletionTokens: d.CompletionTokens,
			Count:            d.Count,
			ElapsedTime:      d.ElapsedTime,
		}
		totalQuota += d.Quota
		totalPrompt += d.PromptTokens
		totalCompletion += d.CompletionTokens
		totalCount += d.Count
	}

	modelsProto := make([]*billingv1.ModelUsage, len(models))
	for i, m := range models {
		modelsProto[i] = &billingv1.ModelUsage{
			Model:  m.Model,
			Tokens: m.Tokens,
		}
	}

	return &billingv1.AggregateLedgerByDateResponse{
		Daily:                dailyProto,
		Models:               modelsProto,
		TotalQuota:           totalQuota,
		TotalPromptTokens:    totalPrompt,
		TotalCompletionTokens: totalCompletion,
		TotalCount:           totalCount,
	}, nil
}

func (s *BillingService) CreatePaymentOrder(ctx context.Context, req *billingv1.CreatePaymentOrderRequest) (*billingv1.PaymentOrderResponse, error) {
	if s.paymentUc == nil {
		return &billingv1.PaymentOrderResponse{Success: false, ErrorMessage: "payment service is not configured"}, nil
	}
	order, err := s.paymentUc.CreateOrder(ctx, biz.CreatePaymentOrderRequest{
		UserID:      req.GetUserId(),
		Channel:     req.GetChannel(),
		AssetType:   req.GetAssetType(),
		AssetAmount: req.GetAssetAmount(),
		MoneyCents:  req.GetMoneyCents(),
		Currency:    req.GetCurrency(),
	})
	if err != nil {
		return &billingv1.PaymentOrderResponse{Success: false, ErrorMessage: err.Error()}, nil
	}
	return &billingv1.PaymentOrderResponse{Success: true, Order: toProtoPaymentOrder(order)}, nil
}

func (s *BillingService) GetPaymentOrderByTradeNo(ctx context.Context, req *billingv1.GetPaymentOrderByTradeNoRequest) (*billingv1.PaymentOrderResponse, error) {
	if s.paymentUc == nil {
		return &billingv1.PaymentOrderResponse{Success: false, ErrorMessage: "payment service is not configured"}, nil
	}
	order, err := s.paymentUc.GetOrderByTradeNo(ctx, req.GetTradeNo())
	if err != nil {
		return &billingv1.PaymentOrderResponse{Success: false, ErrorMessage: err.Error()}, nil
	}
	if order == nil {
		return &billingv1.PaymentOrderResponse{Success: false, ErrorMessage: "payment order not found"}, nil
	}
	return &billingv1.PaymentOrderResponse{Success: true, Order: toProtoPaymentOrder(order)}, nil
}

func (s *BillingService) MarkPaymentOrderPaid(ctx context.Context, req *billingv1.MarkPaymentOrderPaidRequest) (*billingv1.PaymentOrderResponse, error) {
	if s.paymentUc == nil {
		return &billingv1.PaymentOrderResponse{Success: false, ErrorMessage: "payment service is not configured"}, nil
	}
	order, err := s.paymentUc.MarkOrderPaid(ctx, req.GetTradeNo(), req.GetProviderTradeNo())
	if err != nil {
		return &billingv1.PaymentOrderResponse{Success: false, ErrorMessage: err.Error()}, nil
	}
	return &billingv1.PaymentOrderResponse{Success: true, Order: toProtoPaymentOrder(order)}, nil
}

func (s *BillingService) ListPaymentOrders(ctx context.Context, req *billingv1.ListPaymentOrdersRequest) (*billingv1.ListPaymentOrdersResponse, error) {
	if s.paymentUc == nil {
		return &billingv1.ListPaymentOrdersResponse{}, nil
	}
	orders, total, err := s.paymentUc.ListOrders(ctx, biz.ListPaymentOrdersRequest{
		Page:      req.GetPage(),
		PageSize:  req.GetPageSize(),
		UserID:    req.GetUserId(),
		Status:    req.GetStatus(),
		Channel:   req.GetChannel(),
		TradeNo:   req.GetTradeNo(),
		StartTime: req.GetStartTime(),
		EndTime:   req.GetEndTime(),
	})
	if err != nil {
		return nil, err
	}
	resp := &billingv1.ListPaymentOrdersResponse{Total: total}
	for _, order := range orders {
		resp.Orders = append(resp.Orders, toProtoPaymentOrder(order))
	}
	return resp, nil
}

func (s *BillingService) HandleAlipayNotify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeNotifyResponse(w, false)
		return
	}
	if s.alipayVerifier == nil {
		writeNotifyResponse(w, false)
		return
	}
	notify, err := s.alipayVerifier.VerifyNotify(r.Context(), parseFormValues(r))
	if err != nil {
		writeNotifyResponse(w, false)
		return
	}
	if !notify.Success {
		writeNotifyResponse(w, true)
		return
	}
	if s.paymentUc == nil {
		writeNotifyResponse(w, false)
		return
	}
	if _, err := s.paymentUc.MarkOrderPaid(r.Context(), notify.TradeNo, notify.ProviderTradeNo); err != nil {
		writeNotifyResponse(w, false)
		return
	}
	writeNotifyResponse(w, true)
}

func toProtoPaymentOrder(order *biz.PaymentOrder) *billingv1.PaymentOrder {
	if order == nil {
		return nil
	}
	return &billingv1.PaymentOrder{
		Id:               order.ID,
		UserId:           order.UserID,
		TradeNo:          order.TradeNo,
		Channel:          order.Channel,
		AssetType:        order.AssetType,
		AssetAmount:      order.AssetAmount,
		MoneyCents:       order.MoneyCents,
		Currency:         order.Currency,
		Status:           order.Status,
		ProviderTradeNo:  order.ProviderTradeNo,
		ProviderPayload:  order.ProviderPayload,
		PayUrl:           order.PayURL,
		AssetIssueStatus: order.AssetIssueStatus,
		PaidAt:           toProtoTimestampPtr(order.PaidAt),
		CreatedAt:        toProtoTimestamp(order.CreatedAt),
		UpdatedAt:        toProtoTimestamp(order.UpdatedAt),
	}
}

func toProtoTimestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return &timestamppb.Timestamp{
		Seconds: t.Unix(),
		Nanos:   int32(t.Nanosecond()),
	}
}

func toProtoTimestampPtr(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return toProtoTimestamp(*t)
}

func parseFormValues(r *http.Request) map[string]string {
	if err := r.ParseForm(); err != nil {
		return parseQueryValues(r)
	}
	params := make(map[string]string, len(r.Form))
	for key, values := range r.Form {
		if len(values) > 0 {
			params[key] = values[0]
		}
	}
	for key, values := range r.URL.Query() {
		if _, ok := params[key]; !ok && len(values) > 0 {
			params[key] = values[0]
		}
	}
	return params
}

func parseQueryValues(r *http.Request) map[string]string {
	params := map[string]string{}
	for key, values := range r.URL.Query() {
		if len(values) > 0 {
			params[key] = values[0]
		}
	}
	return params
}

func writeNotifyResponse(w http.ResponseWriter, ok bool) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if ok {
		_, _ = w.Write([]byte("success"))
		return
	}
	_, _ = w.Write([]byte("fail"))
}

// HandleReconciliation triggers a billing reconciliation and returns the result as JSON.
func (s *BillingService) HandleReconciliation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	result, err := s.reconUc.RunReconciliation(r.Context())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result)
}

func (s *BillingService) ListReconciliationRuns(ctx context.Context, req *billingv1.ListReconciliationRunsRequest) (*billingv1.ListReconciliationRunsResponse, error) {
	runs, total, err := s.reconUc.ListReconciliationRuns(ctx, req.GetPage(), req.GetPageSize())
	if err != nil {
		return nil, err
	}
	resp := &billingv1.ListReconciliationRunsResponse{Total: total}
	for _, run := range runs {
		resp.Runs = append(resp.Runs, reconciliationRunToProto(run))
	}
	return resp, nil
}

func (s *BillingService) GetReconciliationRun(ctx context.Context, req *billingv1.GetReconciliationRunRequest) (*billingv1.GetReconciliationRunResponse, error) {
	run, err := s.reconUc.GetReconciliationRun(ctx, req.GetRunId())
	if err != nil {
		return nil, err
	}
	if run == nil {
		return &billingv1.GetReconciliationRunResponse{}, nil
	}
	return &billingv1.GetReconciliationRunResponse{Run: reconciliationRunToProto(run)}, nil
}

func reconciliationRunToProto(run *biz.ReconciliationResult) *billingv1.ReconciliationRun {
	if run == nil {
		return nil
	}
	out := &billingv1.ReconciliationRun{
		RunId:             run.RunID,
		RunAt:             run.RunAt.Unix(),
		ExpiredCleaned:    int32(run.ExpiredCleaned),
		TotalAccounts:     int32(run.TotalAccounts),
		TotalReservations: int32(run.TotalReservations),
		DiscrepancyCount:  int32(len(run.AccountInconsistencies)),
	}
	for _, d := range run.AccountInconsistencies {
		out.Discrepancies = append(out.Discrepancies, &billingv1.ReconciliationDiscrepancy{
			UserId:          d.UserID,
			ExpectedQuota:   d.ExpectedQuota,
			ActualQuota:     d.ActualQuota,
			LedgerNetAmount: d.LedgerNetAmount,
			FrozenQuota:     d.FrozenQuota,
		})
	}
	return out
}
