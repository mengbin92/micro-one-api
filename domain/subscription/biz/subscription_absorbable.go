package biz

import "math"

// AbsorbableWindow captures the rolled state of a subscription's rolling
// window plus the matching group limits. It is the input to the
// ComputeAbsorbablePure function.
type AbsorbableWindow struct {
	DailyStart   int64
	WeeklyStart  int64
	MonthlyStart int64

	DailyUsageUSD   float64
	WeeklyUsageUSD  float64
	MonthlyUsageUSD float64

	DailyLimit   *float64
	WeeklyLimit  *float64
	MonthlyLimit *float64

	FrozenDailyAccountingUSD   float64
	FrozenWeeklyAccountingUSD  float64
	FrozenMonthlyAccountingUSD float64
}

// AbsorbableResult is the output of ComputeAbsorbablePure. The values are
// in the original (un-multiplied) USD because the caller is responsible
// for converting reservation.SubscriptionAmountUSD into quota via
// usdToQuotaFloor.
type AbsorbableResult struct {
	// AbsorbableUSD is the maximum cost (in original USD) the next
	// request can absorb against the subscription without exceeding
	// any window's limit.
	AbsorbableUSD float64
	// DailyRemainingAccounting, WeeklyRemainingAccounting and
	// MonthlyRemainingAccounting are the per-window remaining quota
	// in accounting USD (limit - usage - frozen). The minimum of the
	// three divided by RateMultiplier is what becomes AbsorbableUSD.
	DailyRemainingAccounting   float64
	WeeklyRemainingAccounting  float64
	MonthlyRemainingAccounting float64
	// Multiplier is the rate multiplier that was applied; reported
	// back so callers can avoid recomputing it.
	Multiplier float64
}

// ComputeAbsorbablePure calculates the maximum USD (original, pre-
// multiplier) the next request can absorb against the subscription. It
// is a pure function: the caller is responsible for passing in the
// rolled subscription state (via RollUsageWindowsPure) and the
// aggregated frozen USD (sum of active reservation
// SubscriptionAmountUSD times the multiplier) so the calculation can
// happen inside the locked transaction without I/O.
//
// Unit conversion rules (see docs/design/subscription-priority-deduction-design.md
// 5.1):
//
//   - Limit/Usage/Frozen are all converted to the *accounting* USD
//     space (original USD * RateMultiplier) before subtraction so the
//     comparison is unit-consistent.
//   - The remaining amount is then divided by RateMultiplier to
//     return the answer in original USD so the caller can store
//     reservation.SubscriptionAmountUSD without any further
//     conversion.
func ComputeAbsorbablePure(window AbsorbableWindow, rateMultiplier float64, frozenAccountingUSD float64) AbsorbableResult {
	multiplier := rateMultiplier
	if multiplier <= 0 {
		multiplier = 1.0
	}
	frozenDaily := frozenAccountingUSD
	frozenWeekly := frozenAccountingUSD
	frozenMonthly := frozenAccountingUSD
	if window.FrozenDailyAccountingUSD != 0 || window.FrozenWeeklyAccountingUSD != 0 || window.FrozenMonthlyAccountingUSD != 0 {
		frozenDaily = window.FrozenDailyAccountingUSD
		frozenWeekly = window.FrozenWeeklyAccountingUSD
		frozenMonthly = window.FrozenMonthlyAccountingUSD
	}
	remainingAccounting := func(limit *float64, usage float64, frozen float64) float64 {
		if limit == nil {
			// nil limit means "no cap"; use +Inf so this dimension
			// never constrains the result.
			return math.Inf(1)
		}
		return *limit - usage - frozen
	}
	dailyRem := remainingAccounting(window.DailyLimit, window.DailyUsageUSD, frozenDaily)
	weeklyRem := remainingAccounting(window.WeeklyLimit, window.WeeklyUsageUSD, frozenWeekly)
	monthlyRem := remainingAccounting(window.MonthlyLimit, window.MonthlyUsageUSD, frozenMonthly)
	if dailyRem < 0 {
		dailyRem = 0
	}
	if weeklyRem < 0 {
		weeklyRem = 0
	}
	if monthlyRem < 0 {
		monthlyRem = 0
	}
	// The strictest of the three dimensions governs. Inf is preserved
	// for "no cap" so min(unlimited, X) == X.
	minRem := dailyRem
	if weeklyRem < minRem {
		minRem = weeklyRem
	}
	if monthlyRem < minRem {
		minRem = monthlyRem
	}
	absorbable := 0.0
	if !math.IsInf(minRem, 1) {
		absorbable = minRem / multiplier
		if absorbable < 0 {
			absorbable = 0
		}
	}
	return AbsorbableResult{
		AbsorbableUSD:              absorbable,
		DailyRemainingAccounting:   dailyRem,
		WeeklyRemainingAccounting:  weeklyRem,
		MonthlyRemainingAccounting: monthlyRem,
		Multiplier:                 multiplier,
	}
}

// RollUsageWindowsPure is a thin wrapper around RollUsageWindows for use
// from the billing domain. The intent is to make it explicit that the
// billing side never writes back to the subscription row from this
// function (the write happens through RecordUsageForSubscriptionInTx).
func RollUsageWindowsPure(subscription *UserSubscription, now int64) *UserSubscription {
	return RollUsageWindows(subscription, now)
}
