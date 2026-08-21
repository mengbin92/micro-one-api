package biz

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComputeAbsorbablePure_DailyCapConsumed(t *testing.T) {
	cap := func(v float64) *float64 { return &v }
	r := ComputeAbsorbablePure(AbsorbableWindow{
		DailyUsageUSD: 4.5,
		DailyLimit:    cap(5.0),
	}, 1.0, 0.0)
	assert.InDelta(t, 0.5, r.AbsorbableUSD, 1e-9)
}

func TestComputeAbsorbablePure_WeeklyConstrains(t *testing.T) {
	cap := func(v float64) *float64 { return &v }
	r := ComputeAbsorbablePure(AbsorbableWindow{
		DailyUsageUSD:   0,
		WeeklyUsageUSD:  100,
		MonthlyUsageUSD: 0,
		DailyLimit:      cap(1000),
		WeeklyLimit:     cap(105),
		MonthlyLimit:    cap(1000),
	}, 1.0, 0.0)
	// 105 - 100 = 5 USD left this week.
	assert.InDelta(t, 5.0, r.AbsorbableUSD, 1e-9)
}

func TestComputeAbsorbablePure_Multiplier(t *testing.T) {
	cap := func(v float64) *float64 { return &v }
	r := ComputeAbsorbablePure(AbsorbableWindow{
		DailyUsageUSD: 0,
		DailyLimit:    cap(20),
	}, 0.5, 0.0)
	// 20 accounting / 0.5 = 40 original.
	assert.InDelta(t, 40.0, r.AbsorbableUSD, 1e-9)
}

func TestComputeAbsorbablePure_FrozenSubtraction(t *testing.T) {
	cap := func(v float64) *float64 { return &v }
	r := ComputeAbsorbablePure(AbsorbableWindow{
		DailyUsageUSD: 1.0,
		DailyLimit:    cap(10),
	}, 1.0, 3.0)
	assert.InDelta(t, 6.0, r.AbsorbableUSD, 1e-9)
}

func TestComputeAbsorbablePure_NoLimit(t *testing.T) {
	r := ComputeAbsorbablePure(AbsorbableWindow{}, 1.0, 0.0)
	assert.True(t, math.IsInf(r.AbsorbableUSD, 1), "uncapped subscription should have infinite absorbable quota")
}

func TestComputeAbsorbablePure_FullyConsumed(t *testing.T) {
	cap := func(v float64) *float64 { return &v }
	r := ComputeAbsorbablePure(AbsorbableWindow{
		DailyUsageUSD: 5.0,
		DailyLimit:    cap(5.0),
	}, 1.0, 0.0)
	assert.Equal(t, 0.0, r.AbsorbableUSD)
}
