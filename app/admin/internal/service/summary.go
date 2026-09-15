package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	adminv1 "micro-one-api/api/admin/v1"
	billingv1 "micro-one-api/api/billing/v1"
	commonv1 "micro-one-api/api/common/v1"
)

// Summary budgets include queueing and both the independent and enrichment
// phases. Upstream deadlines always win; cancellation is never detached.
const (
	summaryTimeout        = 8 * time.Second
	summarySectionTimeout = 2 * time.Second
	summaryConcurrency    = 4
)

// SummarySectionStatus distinguishes a successful empty result from a failed
// read. Reasons are a small stable vocabulary, never raw upstream errors.
type SummarySectionStatus struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// AdminSummaryData contains the transport results collected for the overview.
// Each field has one writer and is read only after all workers have joined.
type AdminSummaryData struct {
	Users                             *adminv1.AdminListUsersResponse
	Channels                          *adminv1.AdminListChannelsResponse
	Stats                             map[string]any
	SubscriptionAccounts              *adminv1.AdminListSubscriptionAccountsResponse
	ActiveUsers                       *adminv1.AdminListUsersResponse
	ActiveChannels                    *adminv1.AdminListChannelsResponse
	ActiveSubscriptionAccounts        *adminv1.AdminListSubscriptionAccountsResponse
	PaymentOrders                     *billingv1.ListPaymentOrdersResponse
	Reconciliation                    *ListReconciliationRunsResult
	Options                           []OneAPIOption
	TopModels                         []UsageAggregateView
	TopChannels                       []UsageAggregateView
	TopUsers                          []UsageAggregateView
	TopTokens                         []UsageAggregateView
	TopSubscriptionAccounts           []UsageAggregateView
	TopSubscriptionAccountQuotaEvents []SubscriptionAccountQuotaEventAggregateView
	RecentLogs                        []map[string]any
	RecentLogsTotal                   int64
	EnrichmentChannels                map[int64]*commonv1.ChannelSummary
	EnrichmentAccounts                map[int64]*commonv1.SubscriptionAccountSummary
	Sections                          map[string]SummarySectionStatus
}

type summaryTask struct {
	name string
	run  func(context.Context) error
}

func summaryFetch[T any](name string, dst *T, fetch func(context.Context) (T, error)) summaryTask {
	return summaryTask{name: name, run: func(ctx context.Context) error {
		value, err := fetch(ctx)
		if err == nil {
			*dst = value
		}
		return err
	}}
}

// runSummaryTasks bounds in-flight operations, including nested RPCs within
// each operation. A failed task never cancels healthy siblings. Queued tasks
// check cancellation before invoking a dependency; no detached goroutines.
func runSummaryTasks(ctx context.Context, timeout time.Duration, tasks []summaryTask) map[string]SummarySectionStatus {
	results := make([]SummarySectionStatus, len(tasks))
	jobs := make(chan int, len(tasks))
	for i := range tasks {
		jobs <- i
	}
	close(jobs)
	var wg sync.WaitGroup
	for range min(summaryConcurrency, len(tasks)) {
		wg.Go(func() {
			for i := range jobs {
				taskCtx, cancel := context.WithTimeout(ctx, timeout)
				err := taskCtx.Err()
				if err == nil {
					err = tasks[i].run(taskCtx)
				}
				if taskCtx.Err() != nil {
					err = taskCtx.Err()
				}
				results[i] = SummarySectionStatus{Available: err == nil}
				switch {
				case errors.Is(err, context.DeadlineExceeded), status.Code(err) == codes.DeadlineExceeded:
					results[i].Reason = "timeout"
				case errors.Is(err, context.Canceled), status.Code(err) == codes.Canceled:
					results[i].Reason = "canceled"
				case err != nil:
					results[i].Reason = "unavailable"
				}
				cancel()
			}
		})
	}
	wg.Wait()
	out := make(map[string]SummarySectionStatus, len(tasks))
	for i, task := range tasks {
		out[task.name] = results[i]
	}
	return out
}

// LoadSummary performs independent reads first, then fetches display metadata
// for the returned rankings within the same request budget.
func (s *AdminService) LoadSummary(parent context.Context) *AdminSummaryData {
	ctx, cancel := context.WithTimeout(parent, summaryTimeout)
	defer cancel()
	out := &AdminSummaryData{}
	out.Sections = runSummaryTasks(ctx, summarySectionTimeout, []summaryTask{
		summaryFetch("users", &out.Users, func(ctx context.Context) (*adminv1.AdminListUsersResponse, error) {
			return s.ListUsers(ctx, &adminv1.AdminListUsersRequest{Page: 1, PageSize: 5})
		}),
		summaryFetch("channels", &out.Channels, func(ctx context.Context) (*adminv1.AdminListChannelsResponse, error) {
			return s.ListChannels(ctx, &adminv1.AdminListChannelsRequest{Page: 1, PageSize: 5})
		}),
		summaryFetch("usage_stats", &out.Stats, func(ctx context.Context) (map[string]any, error) {
			return s.GetLogStats(ctx, &adminv1.ListLogsRequest{Page: 1, PageSize: 1000})
		}),
		summaryFetch("subscription_accounts", &out.SubscriptionAccounts, func(ctx context.Context) (*adminv1.AdminListSubscriptionAccountsResponse, error) {
			return s.ListSubscriptionAccounts(ctx, &adminv1.AdminListSubscriptionAccountsRequest{Page: 1, PageSize: 5})
		}),
		summaryFetch("active_users", &out.ActiveUsers, func(ctx context.Context) (*adminv1.AdminListUsersResponse, error) {
			return s.ListUsers(ctx, &adminv1.AdminListUsersRequest{Page: 1, PageSize: 1, Status: 1})
		}),
		summaryFetch("active_channels", &out.ActiveChannels, func(ctx context.Context) (*adminv1.AdminListChannelsResponse, error) {
			return s.ListChannels(ctx, &adminv1.AdminListChannelsRequest{Page: 1, PageSize: 1, Status: 1})
		}),
		summaryFetch("active_subscription_accounts", &out.ActiveSubscriptionAccounts, func(ctx context.Context) (*adminv1.AdminListSubscriptionAccountsResponse, error) {
			return s.ListSubscriptionAccounts(ctx, &adminv1.AdminListSubscriptionAccountsRequest{Page: 1, PageSize: 1, Status: 1})
		}),
		summaryFetch("payment_orders", &out.PaymentOrders, func(ctx context.Context) (*billingv1.ListPaymentOrdersResponse, error) {
			return s.ListPaymentOrders(ctx, &billingv1.ListPaymentOrdersRequest{Page: 1, PageSize: 8, Status: "paid"})
		}),
		summaryFetch("reconciliation", &out.Reconciliation, func(ctx context.Context) (*ListReconciliationRunsResult, error) {
			return s.ListReconciliationRuns(ctx, 1, 1)
		}),
		summaryFetch("pricing_options", &out.Options, func(ctx context.Context) ([]OneAPIOption, error) { return s.summaryPricingOptions(ctx) }),
		summaryFetch("top_models", &out.TopModels, func(ctx context.Context) ([]UsageAggregateView, error) { return s.AggregateUsageTopN(ctx, "model", 5) }),
		summaryFetch("top_channels", &out.TopChannels, func(ctx context.Context) ([]UsageAggregateView, error) {
			return s.AggregateUsageTopN(ctx, "channel", 5)
		}),
		summaryFetch("top_users", &out.TopUsers, func(ctx context.Context) ([]UsageAggregateView, error) { return s.AggregateUsageTopN(ctx, "user", 5) }),
		summaryFetch("top_tokens", &out.TopTokens, func(ctx context.Context) ([]UsageAggregateView, error) { return s.AggregateUsageTopN(ctx, "token", 5) }),
		summaryFetch("top_subscription_accounts", &out.TopSubscriptionAccounts, func(ctx context.Context) ([]UsageAggregateView, error) {
			return s.AggregateUsageTopN(ctx, "subscription_account", 5)
		}),
		summaryFetch("top_subscription_account_quota_events", &out.TopSubscriptionAccountQuotaEvents, func(ctx context.Context) ([]SubscriptionAccountQuotaEventAggregateView, error) {
			return s.AggregateSubscriptionAccountQuotaEventsTopN(ctx, 5)
		}),
		{name: "recent_logs", run: func(ctx context.Context) error {
			var err error
			out.RecentLogs, out.RecentLogsTotal, err = s.ListLedgerEntries(ctx, &adminv1.ListLogsRequest{Page: 1, PageSize: 8})
			return err
		}},
	})
	channelIDs := make([]int64, 0, len(out.TopChannels))
	for _, item := range out.TopChannels {
		if item.ChannelID > 0 {
			channelIDs = append(channelIDs, item.ChannelID)
		}
	}
	accountIDs := make([]int64, 0, len(out.TopSubscriptionAccounts)+len(out.TopSubscriptionAccountQuotaEvents))
	for _, item := range out.TopSubscriptionAccounts {
		if item.SubscriptionAccountID > 0 {
			accountIDs = append(accountIDs, item.SubscriptionAccountID)
		}
	}
	for _, item := range out.TopSubscriptionAccountQuotaEvents {
		if item.SubscriptionAccountID > 0 {
			accountIDs = append(accountIDs, item.SubscriptionAccountID)
		}
	}
	enrichment := runSummaryTasks(ctx, summarySectionTimeout, []summaryTask{
		summaryFetch("channel_names", &out.EnrichmentChannels, func(ctx context.Context) (map[int64]*commonv1.ChannelSummary, error) {
			return s.FetchChannelSummariesByID(ctx, channelIDs)
		}),
		summaryFetch("subscription_account_names", &out.EnrichmentAccounts, func(ctx context.Context) (map[int64]*commonv1.SubscriptionAccountSummary, error) {
			return s.FetchSubscriptionAccountSummariesByID(ctx, accountIDs)
		}),
	})
	for key, value := range enrichment {
		out.Sections[key] = value
	}
	return out
}

// Read only the five public pricing options used by the overview. Reading all
// site options added dozens of serial config RPCs and silently used defaults
// on errors. Missing values still use the documented legacy/default values;
// failed reads must propagate so configuration outages remain visible.
func (s *AdminService) summaryPricingOptions(ctx context.Context) ([]OneAPIOption, error) {
	out := make([]OneAPIOption, 0, 5)
	for _, key := range []string{"ModelRatio", "CompletionRatio", "ModelPrice", "GroupRatio", "AmountPerUnit"} {
		value := oneAPIOptionDefaults[key]
		if s.systemOptsUc != nil {
			v, err := s.systemOptsUc.Get(ctx, key)
			if err != nil {
				return nil, err
			}
			if v == "" && oneAPIOptionLegacyAliases[key] != "" {
				v, err = s.systemOptsUc.Get(ctx, oneAPIOptionLegacyAliases[key])
				if err != nil {
					return nil, err
				}
			}
			if v != "" {
				value = v
			}
		}
		out = append(out, OneAPIOption{Key: key, Value: value})
	}
	return out, nil
}
