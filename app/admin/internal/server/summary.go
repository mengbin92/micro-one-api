package server

import "micro-one-api/app/admin/internal/service"

// Keep successful empty/zero values intact, but encode failed reads as null.
// Existing response keys remain stable; sections explains every unavailable
// component, including optional display-name enrichment.
func applySummaryAvailability(data map[string]any, sections map[string]service.SummarySectionStatus) {
	fields := map[string][]string{
		"users":                                 {"recent_users"},
		"channels":                              {"channels"},
		"subscription_accounts":                 {"subscription_accounts"},
		"recent_logs":                           {"recent_logs"},
		"payment_orders":                        {"payment_orders", "payment_summary"},
		"usage_stats":                           {"usage_stats", "cost_analysis"},
		"top_models":                            {"top_models"},
		"top_channels":                          {"top_channels"},
		"top_users":                             {"top_users"},
		"top_tokens":                            {"top_tokens"},
		"top_subscription_accounts":             {"top_subscription_accounts", "top_subscription_account_quota_events"},
		"top_subscription_account_quota_events": {"top_subscription_accounts", "top_subscription_account_quota_events"},
		"reconciliation":                        {"latest_reconciliation"},
		"pricing_options":                       {"pricing_options"},
	}
	totalFields := map[string][]string{
		"users":                        {"users"},
		"active_users":                 {"active_users"},
		"channels":                     {"channels", "configured_models", "channel_balance", "stale_balance_channels"},
		"active_channels":              {"active_channels"},
		"subscription_accounts":        {"subscription_accounts"},
		"active_subscription_accounts": {"active_subscription_accounts"},
		"recent_logs":                  {"log_count"},
		"usage_stats":                  {"request_count", "quota_used", "upstream_cost", "gross_profit"},
	}
	partial := false
	totals := data["totals"].(map[string]any)
	for section, state := range sections {
		if state.Available {
			continue
		}
		partial = true
		for _, field := range fields[section] {
			data[field] = nil
		}
		for _, field := range totalFields[section] {
			totals[field] = nil
		}
	}
	data["partial"] = partial
	data["sections"] = sections
	// Preserve alerts from healthy sources, but never infer overall health
	// from an empty list when any of its sources failed.
	data["alerts_complete"] = sections["channels"].Available && sections["top_channels"].Available && sections["reconciliation"].Available && sections["subscription_accounts"].Available
}
