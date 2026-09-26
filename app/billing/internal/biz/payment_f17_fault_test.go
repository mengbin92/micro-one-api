package biz

import (
	"context"
	"errors"
	"strings"
	"testing"

	subscriptionbiz "micro-one-api/domain/subscription/biz"
)

// F17 故障子项隔离验收（docs/runbooks/f17-fault-acceptance-2026-09-26.md）。
// 这组测试用内存 fake 注入四种生产故障：丢回调、查单暂时失败、重复签名
// 回调和发放失败。生产语义由被测代码自身保证（真实 repo 的事务回滚见
// payment_repo_test.go 的 replay 用例），这里验证 usecase 层在故障下的
// 收敛行为。

// flakyPaymentIssuer fails the first N issue attempts so tests can exercise
// the grant-failure rollback and replay-recovery path.
type flakyPaymentIssuer struct {
	delegate *countingPaymentIssuer
	failures int
	err      error
}

func (i *flakyPaymentIssuer) IssueBalance(ctx context.Context, order *PaymentOrder) error {
	if i.failures > 0 {
		i.failures--
		return i.err
	}
	return i.delegate.IssueBalance(ctx, order)
}

func (i *flakyPaymentIssuer) IssueBalanceInTx(ctx context.Context, tx subscriptionbiz.Tx, order *PaymentOrder) error {
	return i.IssueBalance(ctx, order)
}

func pendingAlipayBalanceOrder(tradeNo string) *PaymentOrder {
	return &PaymentOrder{
		TradeNo:          tradeNo,
		UserID:           "42",
		Channel:          PaymentChannelAlipay,
		AssetType:        PaymentAssetTypeBalance,
		AssetAmount:      1000000,
		MoneyCents:       1000,
		Status:           PaymentOrderStatusPending,
		AssetIssueStatus: PaymentAssetIssueStatusPending,
	}
}

// F17-1 丢回调：异步 notify 丢失后，查单任务通过 alipay.trade.query 收敛
// 订单并恰好发放一次。
func TestPaymentReconcileLostNotifyConverges(t *testing.T) {
	repo := &memoryPaymentRepo{order: pendingAlipayBalanceOrder("PAY-LOST-1")}
	issuer := &countingPaymentIssuer{}
	uc := NewPaymentUsecase(repo, &statusPaymentProvider{status: &PaymentProviderStatus{
		ProviderTradeNo: "ALI-LOST-1",
		TradeStatus:     "TRADE_SUCCESS",
		Paid:            true,
	}}, issuer)

	report, err := uc.ReconcilePendingOrders(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 1 || report.Paid != 1 || report.QueryFailures != 0 {
		t.Fatalf("report = %+v", report)
	}
	if repo.order.Status != PaymentOrderStatusPaid {
		t.Fatalf("status = %q", repo.order.Status)
	}
	if repo.order.ProviderTradeNo != "ALI-LOST-1" {
		t.Fatalf("provider_trade_no = %q", repo.order.ProviderTradeNo)
	}
	if issuer.issued != 1 {
		t.Fatalf("issued = %d", issuer.issued)
	}
}

// F17-2 查单暂时失败：本轮查询失败计入 QueryFailures、订单保持 pending
// 且不发放；下一轮（1 分钟间隔由任务层控制，这里直接再调一轮）自然重试
// 并成功收敛。生产语义是固定间隔无限重试，无退避也无死信。
func TestPaymentReconcileQueryFailureStaysPendingAndRetriesNextRound(t *testing.T) {
	repo := &memoryPaymentRepo{order: pendingAlipayBalanceOrder("PAY-QUERY-1")}
	issuer := &countingPaymentIssuer{}
	provider := &statusPaymentProvider{err: errors.New("alipay trade.query 503")}
	uc := NewPaymentUsecase(repo, provider, issuer)

	report, err := uc.ReconcilePendingOrders(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 1 || report.QueryFailures != 1 || report.Paid != 0 {
		t.Fatalf("report = %+v", report)
	}
	if repo.order.Status != PaymentOrderStatusPending {
		t.Fatalf("status = %q", repo.order.Status)
	}
	if issuer.issued != 0 {
		t.Fatalf("issued = %d before recovery", issuer.issued)
	}

	provider.err = nil
	provider.status = &PaymentProviderStatus{
		ProviderTradeNo: "ALI-QUERY-1",
		TradeStatus:     "TRADE_SUCCESS",
		Paid:            true,
	}
	report, err = uc.ReconcilePendingOrders(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 1 || report.Paid != 1 || report.QueryFailures != 0 {
		t.Fatalf("report after recovery = %+v", report)
	}
	if repo.order.Status != PaymentOrderStatusPaid {
		t.Fatalf("status = %q", repo.order.Status)
	}
	if issuer.issued != 1 {
		t.Fatalf("issued = %d after recovery", issuer.issued)
	}
}

// F17-3 重复签名回调：同一笔已支付通知重放时，状态短路使发放回调至多
// 执行一次（第二道闸门是账本 dedupe claim，见 ledger_repo 测试）。
func TestPaymentDuplicatePaidNotificationIssuesOnce(t *testing.T) {
	repo := &memoryPaymentRepo{order: pendingAlipayBalanceOrder("PAY-DUP-1")}
	issuer := &countingPaymentIssuer{}
	uc := NewPaymentUsecase(repo, &statusPaymentProvider{}, issuer)

	order, err := uc.MarkOrderPaid(context.Background(), "PAY-DUP-1", "ALI-DUP-1")
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != PaymentOrderStatusPaid {
		t.Fatalf("status = %q", order.Status)
	}

	// 支付宝重发同一笔签名回调；重复投递不得再次发放。
	replayed, err := uc.MarkOrderPaid(context.Background(), "PAY-DUP-1", "ALI-DUP-1")
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Status != PaymentOrderStatusPaid {
		t.Fatalf("replayed status = %q", replayed.Status)
	}
	if issuer.issued != 1 {
		t.Fatalf("issued = %d", issuer.issued)
	}
}

// F17-4a 余额发放失败：钱包写入失败使整个事务回滚，订单保持 pending；
// 重放收敛后恰好发放一次。
func TestPaymentBalanceGrantFailureRollsBackAndRecovers(t *testing.T) {
	repo := &memoryPaymentRepo{order: pendingAlipayBalanceOrder("PAY-GRANT-1")}
	issuer := &flakyPaymentIssuer{
		delegate: &countingPaymentIssuer{},
		failures: 1,
		err:      errors.New("wallet store unavailable"),
	}
	uc := NewPaymentUsecase(repo, &statusPaymentProvider{}, issuer)

	if _, err := uc.MarkOrderPaid(context.Background(), "PAY-GRANT-1", "ALI-GRANT-1"); err == nil {
		t.Fatal("expected grant failure to surface")
	}
	if repo.order.Status != PaymentOrderStatusPending {
		t.Fatalf("status after failed grant = %q", repo.order.Status)
	}
	if repo.order.AssetIssueStatus != PaymentAssetIssueStatusPending {
		t.Fatalf("asset_issue_status = %q", repo.order.AssetIssueStatus)
	}

	order, err := uc.MarkOrderPaid(context.Background(), "PAY-GRANT-1", "ALI-GRANT-1")
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != PaymentOrderStatusPaid {
		t.Fatalf("status after recovery = %q", order.Status)
	}
	if issuer.delegate.issued != 1 {
		t.Fatalf("issued = %d after recovery", issuer.delegate.issued)
	}
}

// F17-4b 订阅发放失败：assigner 失败同样整事务回滚保持 pending，重放后
// 发放成功。
func TestPaymentSubscriptionGrantFailureRollsBackAndRecovers(t *testing.T) {
	repo := &memoryPaymentRepo{order: &PaymentOrder{
		TradeNo:          "PAY-SUB-GRANT-1",
		UserID:           "42",
		Channel:          PaymentChannelAlipay,
		AssetType:        PaymentAssetTypeSubscription,
		AssetAmount:      1,
		GroupID:          9,
		Status:           PaymentOrderStatusPending,
		AssetIssueStatus: PaymentAssetIssueStatusPending,
	}}
	issuer := &countingPaymentIssuer{}
	assigner := &countingSubscriptionAssigner{err: errors.New("subscription assign failed")}
	uc := NewPaymentUsecaseWithAssigner(repo, &statusPaymentProvider{}, issuer, assigner)

	_, err := uc.MarkOrderPaid(context.Background(), "PAY-SUB-GRANT-1", "ALI-SUB-1")
	if err == nil {
		t.Fatal("expected subscription grant failure to surface")
	}
	if !strings.Contains(err.Error(), "assign subscription after payment") {
		t.Fatalf("err = %v", err)
	}
	if repo.order.Status != PaymentOrderStatusPending {
		t.Fatalf("status after failed grant = %q", repo.order.Status)
	}
	if assigner.assigned != 1 {
		t.Fatalf("assigned = %d", assigner.assigned)
	}

	assigner.err = nil
	order, err := uc.MarkOrderPaid(context.Background(), "PAY-SUB-GRANT-1", "ALI-SUB-1")
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != PaymentOrderStatusPaid {
		t.Fatalf("status after recovery = %q", order.Status)
	}
	if order.AssetIssueStatus != PaymentAssetIssueStatusIssued {
		t.Fatalf("asset_issue_status = %q", order.AssetIssueStatus)
	}
	if assigner.assigned != 2 {
		t.Fatalf("assigned = %d after recovery", assigner.assigned)
	}
}
