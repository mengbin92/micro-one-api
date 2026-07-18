package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	billingv1 "micro-one-api/api/billing/v1"
	commonv1 "micro-one-api/api/common/v1"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/pkg/safecast"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type BillingService struct {
	billingv1.UnimplementedBillingServiceServer
	uc        *biz.BillingUsecase
	asyncUc   *biz.AsyncBillingUsecase // optional; nil = synchronous path
	paymentUc *biz.PaymentUsecase
	alipayVerifier biz.PaymentNotifyVerifier
	reconUc  *biz.ReconciliationUsecase
	refundUc *biz.RefundUsecase
	reportUc *biz.SubscriptionReportUsecase
}

func NewBillingService(uc *biz.BillingUsecase, reconUc *biz.ReconciliationUsecase, paymentUc *biz.PaymentUsecase, alipayVerifier biz.PaymentNotifyVerifier) *BillingService {
	return &BillingService{uc: uc, reconUc: reconUc, paymentUc: paymentUc, alipayVerifier: alipayVerifier}
}

// SetRefundUsecase wires the refund coordinator. Optional; when unset the
// RefundPaymentOrder RPC returns not-configured.
func (s *BillingService) SetRefundUsecase(uc *biz.RefundUsecase) {
	if s == nil {
		return
	}
	s.refundUc = uc
}

// SetSubscriptionReportUsecase wires the operational report builder.
func (s *BillingService) SetSubscriptionReportUsecase(uc *biz.SubscriptionReportUsecase) {
	if s == nil {
		return
	}
	s.reportUc = uc
}

// SetAsyncBillingUsecase wires the async billing coordinator. When set, the
// CommitQuota RPC enqueues a settlement task and returns a provisional
// response immediately instead of blocking on the DB; the background worker
// runs the full CommitQuotaWithUsageAndSplit pipeline. When unset (nil),
// CommitQuota falls back to the original synchronous path.
func (s *BillingService) SetAsyncBillingUsecase(uc *biz.AsyncBillingUsecase) {
	if s == nil {
		return
	}
	s.asyncUc = uc
}

func (s *BillingService) ReserveQuota(ctx context.Context, req *billingv1.ReserveQuotaRequest) (*billingv1.ReserveQuotaResponse, error) {
	reservation, err := s.uc.ReserveQuota(ctx, req.UserId, req.RequestId, req.EstimatedTokens, req.Model, req.ChannelId, req.SubscriptionAccountId)
	if err != nil {
		return &billingv1.ReserveQuotaResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}
	// The relay layer and any debugging tools read the cost-dimension
	// split off the response. We forward the raw float USD as int64
	// nanodollars (USD * 1e9) so the wire type is a stable integer
	// regardless of float precision; the relay converts back to a
	// display USD when needed.
	resp := &billingv1.ReserveQuotaResponse{
		Success:            true,
		ReservationId:      reservation.ReservationID,
		ReservedAmount:     reservation.Amount,
		BalanceAmountQuota: reservation.BalanceAmountQuota,
		SubscriptionId:     reservation.SubscriptionID,
	}
	if reservation.SubscriptionAmountUSD > 0 {
		// Convert to nanodollars for stable int64 transport.
		resp.SubscriptionAmountUsd = int64(reservation.SubscriptionAmountUSD * 1e9)
	}
	return resp, nil
}

func (s *BillingService) CommitQuota(ctx context.Context, req *billingv1.CommitQuotaRequest) (*billingv1.CommitQuotaResponse, error) {
	usage := biz.LedgerUsage{
		TokenName:             req.TokenName,
		Endpoint:              req.Endpoint,
		PromptTokens:          req.PromptTokens,
		CompletionTokens:      req.CompletionTokens,
		CacheReadTokens:       req.CacheReadTokens,
		UpstreamCost:          firstPositiveInt64(req.UpstreamCost, req.ActualCost),
		ElapsedTime:           req.ElapsedTime,
		IsStream:              req.IsStream,
		SubscriptionAccountID: req.SubscriptionAccountId,
	}

	// Async path: enqueue the settlement task and return a provisional
	// success response immediately. The background worker runs the full
	// CommitQuotaWithUsageAndSplit pipeline (reservation state machine,
	// wallet settlement, ledger, subscription usage). A nil asyncUc falls
	// back to the synchronous path below. When the caller reports a failed
	// request (Success=false) we never enqueue: a release must be
	// synchronous so the caller observes the released reservation and does
	// not proceed against a frozen amount.
	if s.asyncUc != nil && req.Success {
		// The task carries the original reservation id as a stable
		// correlation id so async settlement logs and metrics can be tied back to
		// the relay's request span. When the reservation id is missing we fall
		// back to a synthetic id so the worker never operates without context.
		requestID := req.ReservationId
		if requestID == "" {
			requestID = fmt.Sprintf("async-%d", time.Now().UnixNano())
		}
		s.asyncUc.Settle(ctx, &biz.SettleTask{
			RequestID:             requestID,
			ReservationID:         req.ReservationId,
			ActualTokens:          req.ActualTokens,
			Success:               true,
			Usage:                 usage,
			Timestamp:             time.Now(),
		})
		return &billingv1.CommitQuotaResponse{
			Success:         true,
			CommittedAmount: 0, // provisional; authoritative amount written by the worker
			AsyncEnqueued:   true,
		}, nil
	}

	committedAmount, refundAmount, result, err := s.uc.CommitQuotaWithUsageAndSplit(ctx, req.ReservationId, req.ActualTokens, req.Success, usage)
	if err != nil {
		return &billingv1.CommitQuotaResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}

	return &billingv1.CommitQuotaResponse{
		Success:          true,
		CommittedAmount:  committedAmount,
		RefundAmount:     refundAmount,
		SubscriptionCost: result.SubscriptionCost,
		BalanceCost:      result.BalanceCost,
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
			Balance:      account.Balance,
			UsedAmount:   account.UsedAmount,
			RequestCount: account.RequestCount,
			Group:        account.Group,
			GroupRatio:   account.GroupRatio(),
			FrozenAmount: account.FrozenAmount,
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
			Balance:      account.Balance,
			UsedAmount:   account.UsedAmount,
			RequestCount: account.RequestCount,
			Group:        account.Group,
			GroupRatio:   account.GroupRatio(),
			FrozenAmount: account.FrozenAmount,
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
		Success:    true,
		NewBalance: newQuota,
	}, nil
}

func (s *BillingService) PurchaseSubscription(ctx context.Context, req *billingv1.PurchaseSubscriptionRequest) (*billingv1.PurchaseSubscriptionResponse, error) {
	newQuota, err := s.uc.PurchaseSubscription(ctx, req.UserId, req.PriceAmount, req.GroupId, req.Remark)
	if err != nil {
		return &billingv1.PurchaseSubscriptionResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}

	return &billingv1.PurchaseSubscriptionResponse{
		Success:    true,
		NewBalance: newQuota,
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
	codes, err := s.uc.CreateRedeemCodesBatch(ctx, req.Name, req.Amount, req.Count, req.BatchSize)
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
		Success:    true,
		Amount:     amount,
		NewBalance: newQuota,
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
	if req.GetSubscriptionAccountId() != 0 {
		ledgers, total, err = s.uc.ListLedgersBySubscriptionAccount(ctx, req.GetSubscriptionAccountId(), page, pageSize)
	} else if ledgerType != "" || !startTime.IsZero() || !endTime.IsZero() {
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
			Id:                    fmt.Sprintf("%d", ledger.ID),
			UserId:                ledger.UserID,
			Amount:                ledger.Amount,
			BalanceAfter:          ledger.BalanceAfter,
			Type:                  ledger.Type,
			ReferenceId:           ledger.ReferenceID,
			Remark:                ledger.Remark,
			CreatedAt:             toProtoTimestamp(ledger.CreatedAt),
			TokenName:             ledger.TokenName,
			ModelName:             ledger.ModelName,
			Quota:                 ledger.Quota,
			PromptTokens:          ledger.PromptTokens,
			CompletionTokens:      ledger.CompletionTokens,
			CacheReadTokens:       ledger.CacheReadTokens,
			ChannelId:             ledger.ChannelID,
			SubscriptionAccountId: ledger.SubscriptionAccountID,
			ElapsedTime:           ledger.ElapsedTime,
			IsStream:              ledger.IsStream,
			Endpoint:              ledger.Endpoint,
			CostSource:            ledger.CostSource,
			SubscriptionCost:      ledger.SubscriptionCost,
			BalanceCost:           ledger.BalanceCost,
			LedgerDedupeKey:       ledger.LedgerDedupeKey,
			Username:              ledger.Username,
		}
	}

	return &billingv1.ListLedgerResponse{
		Entries: entries,
		Total:   total,
	}, nil
}

func (s *BillingService) GetLedgerEntry(ctx context.Context, req *billingv1.GetLedgerEntryRequest) (*billingv1.GetLedgerEntryResponse, error) {
	ledger, err := s.uc.GetLedgerByID(ctx, req.GetId())
	if err != nil {
		if errors.Is(err, biz.ErrLedgerNotFound) {
			return nil, status.Errorf(codes.NotFound, "ledger entry not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get ledger entry: %v", err)
	}

	return &billingv1.GetLedgerEntryResponse{
		Entry: &commonv1.LedgerEntry{
			Id:                    fmt.Sprintf("%d", ledger.ID),
			UserId:                ledger.UserID,
			Amount:                ledger.Amount,
			BalanceAfter:          ledger.BalanceAfter,
			Type:                  ledger.Type,
			ReferenceId:           ledger.ReferenceID,
			Remark:                ledger.Remark,
			CreatedAt:             toProtoTimestamp(ledger.CreatedAt),
			TokenName:             ledger.TokenName,
			ModelName:             ledger.ModelName,
			Quota:                 ledger.Quota,
			PromptTokens:          ledger.PromptTokens,
			CompletionTokens:      ledger.CompletionTokens,
			CacheReadTokens:       ledger.CacheReadTokens,
			ChannelId:             ledger.ChannelID,
			SubscriptionAccountId: ledger.SubscriptionAccountID,
			ElapsedTime:           ledger.ElapsedTime,
			IsStream:              ledger.IsStream,
			Endpoint:              ledger.Endpoint,
			CostSource:            ledger.CostSource,
			SubscriptionCost:      ledger.SubscriptionCost,
			BalanceCost:           ledger.BalanceCost,
			LedgerDedupeKey:       ledger.LedgerDedupeKey,
			Username:              ledger.Username,
		},
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
	var totalQuota, totalPrompt, totalCompletion, totalCacheRead, totalCount int64
	for i, d := range daily {
		dailyProto[i] = &billingv1.DailyUsage{
			Date:             d.Date,
			Quota:            d.Quota,
			PromptTokens:     d.PromptTokens,
			CompletionTokens: d.CompletionTokens,
			CacheReadTokens:  d.CacheReadTokens,
			Count:            d.Count,
			ElapsedTime:      d.ElapsedTime,
		}
		totalQuota += d.Quota
		totalPrompt += d.PromptTokens
		totalCompletion += d.CompletionTokens
		totalCacheRead += d.CacheReadTokens
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
		Daily:                 dailyProto,
		Models:                modelsProto,
		TotalQuota:            totalQuota,
		TotalPromptTokens:     totalPrompt,
		TotalCompletionTokens: totalCompletion,
		TotalCacheReadTokens:  totalCacheRead,
		TotalCount:            totalCount,
	}, nil
}

func (s *BillingService) AggregateUsage(ctx context.Context, req *billingv1.AggregateUsageRequest) (*billingv1.AggregateUsageResponse, error) {
	filter := biz.UsageFilter{
		GroupBy:               req.GetGroupBy(),
		UserID:                req.GetUserId(),
		ChannelID:             req.GetChannelId(),
		SubscriptionAccountID: req.GetSubscriptionAccountId(),
		Model:                 req.GetModel(),
		Type:                  req.GetType(),
		Limit:                 int(req.GetLimit()),
	}
	if req.GetStartTime().IsValid() {
		filter.StartTime = req.GetStartTime().AsTime()
	}
	if req.GetEndTime().IsValid() {
		filter.EndTime = req.GetEndTime().AsTime()
	}

	buckets, totals, err := s.uc.AggregateUsage(ctx, filter)
	if err != nil {
		return nil, err
	}

	bucketsProto := make([]*billingv1.UsageBucket, len(buckets))
	for i, b := range buckets {
		bucketsProto[i] = &billingv1.UsageBucket{
			UserId:                b.UserID,
			ChannelId:             b.ChannelID,
			SubscriptionAccountId: b.SubscriptionAccountID,
			Model:                 b.Model,
			TokenName:             b.TokenName,
			Type:                  b.Type,
			Day:                   b.Day,
			Hour:                  b.Hour,
			Quota:                 b.Quota,
			UpstreamCost:          b.UpstreamCost,
			GrossProfit:           b.GrossProfit,
			PromptTokens:          b.PromptTokens,
			CompletionTokens:      b.CompletionTokens,
			CacheReadTokens:       b.CacheReadTokens,
			Count:                 b.Count,
			ElapsedTime:           b.ElapsedTime,
		}
	}

	if totals == nil {
		totals = &biz.UsageTotals{}
	}
	return &billingv1.AggregateUsageResponse{
		Buckets: bucketsProto,
		Totals: &billingv1.UsageTotals{
			Quota:            totals.Quota,
			UpstreamCost:     totals.UpstreamCost,
			GrossProfit:      totals.GrossProfit,
			PromptTokens:     totals.PromptTokens,
			CompletionTokens: totals.CompletionTokens,
			CacheReadTokens:  totals.CacheReadTokens,
			Count:            totals.Count,
			ElapsedTime:      totals.ElapsedTime,
		},
	}, nil
}

func firstPositiveInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
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
		GroupID:     req.GetGroupId(),
		PlanID:      req.GetPlanId(),
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

func (s *BillingService) RefundPaymentOrder(ctx context.Context, req *billingv1.RefundPaymentOrderRequest) (*billingv1.RefundPaymentOrderResponse, error) {
	if s.refundUc == nil {
		return &billingv1.RefundPaymentOrderResponse{Success: false, ErrorMessage: "refund service is not configured"}, nil
	}
	res, err := s.refundUc.RefundSubscriptionOrder(ctx, biz.RefundRequest{
		TradeNo:  req.GetTradeNo(),
		Reason:   req.GetReason(),
		Policy:   biz.RefundPolicy(req.GetPolicy()),
		Operator: req.GetOperatorId(),
	})
	if err != nil {
		return &billingv1.RefundPaymentOrderResponse{Success: false, ErrorMessage: err.Error()}, nil
	}
	return &billingv1.RefundPaymentOrderResponse{
		Success:            true,
		RefundedQuota:      res.RefundedQuota,
		BalanceAfter:       res.BalanceAfter,
		SubscriptionId:     res.SubscriptionID,
		SubscriptionAction: res.SubscriptionAct,
		LedgerDedupeKey:    res.LedgerDedupeKey,
	}, nil
}

func (s *BillingService) SubscriptionOperationReport(ctx context.Context, req *billingv1.SubscriptionOperationReportRequest) (*billingv1.SubscriptionOperationReportResponse, error) {
	if s.reportUc == nil {
		return &billingv1.SubscriptionOperationReportResponse{Success: false, ErrorMessage: "report service is not configured"}, nil
	}
	var startTime, endTime time.Time
	if req.GetStartTime() > 0 {
		startTime = time.Unix(req.GetStartTime(), 0)
	}
	if req.GetEndTime() > 0 {
		endTime = time.Unix(req.GetEndTime(), 0)
	}
	report, err := s.reportUc.BuildReport(ctx, startTime, endTime, req.GetPlanId(), req.GetGroupId(), req.GetUserId())
	if err != nil {
		return &billingv1.SubscriptionOperationReportResponse{Success: false, ErrorMessage: err.Error()}, nil
	}
	resp := &billingv1.SubscriptionOperationReportResponse{
		Success:            true,
		TotalRevenueQuota:  report.TotalRevenueQuota,
		TotalRefundedQuota: report.TotalRefundedQuota,
	}
	for _, r := range report.Rows {
		resp.Rows = append(resp.Rows, &billingv1.PlanOperationRow{
			PlanId:                 r.PlanID,
			PlanName:               r.PlanName,
			GroupId:                r.GroupID,
			NewPurchaseCount:       r.NewPurchaseCount,
			RenewalCount:           r.RenewalCount,
			RefundCount:            r.RefundCount,
			RevenueQuota:           r.RevenueQuota,
			RefundedQuota:          r.RefundedQuota,
			ActiveSubscriptions:    r.ActiveSubscriptions,
			ExpiredSubscriptions:   r.ExpiredSubscriptions,
			RevokedSubscriptions:   r.RevokedSubscriptions,
			SubscriptionUsageQuota: r.SubscriptionUsageQuota,
			BalanceFallbackQuota:   r.BalanceFallbackQuota,
		})
	}
	return resp, nil
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
		GroupId:          order.GroupID,
		PlanId:           order.PlanID,
		PlanSnapshot:     order.PlanSnapshot,
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
		Nanos:   int32(t.Nanosecond()), // #nosec G115 -- Nanosecond returns [0, 999999999].
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
		item, err := reconciliationRunToProto(run)
		if err != nil {
			return nil, err
		}
		resp.Runs = append(resp.Runs, item)
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
	item, err := reconciliationRunToProto(run)
	if err != nil {
		return nil, err
	}
	return &billingv1.GetReconciliationRunResponse{Run: item}, nil
}

func reconciliationRunToProto(run *biz.ReconciliationResult) (*billingv1.ReconciliationRun, error) {
	if run == nil {
		return nil, nil
	}
	expiredCleaned, err := safecast.IntToInt32(run.ExpiredCleaned)
	if err != nil {
		return nil, err
	}
	totalAccounts, err := safecast.IntToInt32(run.TotalAccounts)
	if err != nil {
		return nil, err
	}
	totalChannels, err := safecast.IntToInt32(run.TotalChannels)
	if err != nil {
		return nil, err
	}
	totalReservations, err := safecast.IntToInt32(run.TotalReservations)
	if err != nil {
		return nil, err
	}
	discrepancyCount, err := safecast.IntToInt32(run.DiscrepancyCount())
	if err != nil {
		return nil, err
	}
	out := &billingv1.ReconciliationRun{
		RunId:             run.RunID,
		RunAt:             run.RunAt.Unix(),
		ExpiredCleaned:    expiredCleaned,
		TotalAccounts:     totalAccounts,
		TotalChannels:     totalChannels,
		TotalReservations: totalReservations,
		DiscrepancyCount:  discrepancyCount,
	}
	for _, d := range run.AccountInconsistencies {
		out.Discrepancies = append(out.Discrepancies, &billingv1.ReconciliationDiscrepancy{
			Type:            biz.ReconciliationDiscrepancyTypeAccount,
			UserId:          d.UserID,
			ExpectedQuota:   d.ExpectedQuota,
			ActualQuota:     d.ActualQuota,
			LedgerNetAmount: d.LedgerNetAmount,
			FrozenQuota:     d.FrozenQuota,
		})
	}
	for _, d := range run.ChannelInconsistencies {
		out.Discrepancies = append(out.Discrepancies, &billingv1.ReconciliationDiscrepancy{
			Type:              biz.ReconciliationDiscrepancyTypeChannel,
			ChannelId:         d.ChannelID,
			ExpectedUsedQuota: d.ExpectedUsedQuota,
			ActualUsedQuota:   d.ActualUsedQuota,
			LedgerQuota:       d.LedgerQuota,
			UpstreamCost:      d.UpstreamCost,
			Difference:        d.Difference,
		})
	}
	for _, d := range run.LogInconsistencies {
		out.Discrepancies = append(out.Discrepancies, &billingv1.ReconciliationDiscrepancy{
			Type:        biz.ReconciliationDiscrepancyTypeLog,
			LedgerCount: d.LedgerCount,
			LogCount:    d.LogCount,
			LedgerQuota: d.LedgerQuota,
			LogQuota:    d.LogQuota,
			CountDiff:   d.CountDiff,
			QuotaDiff:   d.QuotaDiff,
		})
	}
	return out, nil
}
