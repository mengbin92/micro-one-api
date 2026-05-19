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
	uc         *biz.BillingUsecase
	reconUc    *biz.ReconciliationUsecase
}

func NewBillingService(uc *biz.BillingUsecase, reconUc *biz.ReconciliationUsecase) *BillingService {
	return &BillingService{uc: uc, reconUc: reconUc}
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
		ReservationId:   reservation.ReservationID,
		ReservedAmount: reservation.Amount,
	}, nil
}

func (s *BillingService) CommitQuota(ctx context.Context, req *billingv1.CommitQuotaRequest) (*billingv1.CommitQuotaResponse, error) {
	committedAmount, refundAmount, err := s.uc.CommitQuota(ctx, req.ReservationId, req.ActualTokens, req.Success)
	if err != nil {
		return &billingv1.CommitQuotaResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}

	return &billingv1.CommitQuotaResponse{
		Success:        true,
		CommittedAmount: committedAmount,
		RefundAmount:   refundAmount,
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
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	ledgers, total, err := s.uc.ListLedgers(ctx, req.UserId, page, pageSize)
	if err != nil {
		return nil, err
	}

	entries := make([]*commonv1.LedgerEntry, len(ledgers))
	for i, ledger := range ledgers {
		entries[i] = &commonv1.LedgerEntry{
			Id:           fmt.Sprintf("%d", ledger.ID),
			UserId:       ledger.UserID,
			Amount:       ledger.Amount,
			BalanceAfter: ledger.BalanceAfter,
			Type:         ledger.Type,
			ReferenceId:  ledger.ReferenceID,
			Remark:       ledger.Remark,
			CreatedAt:    toProtoTimestamp(ledger.CreatedAt),
		}
	}

	return &billingv1.ListLedgerResponse{
		Entries: entries,
		Total:   total,
	}, nil
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
