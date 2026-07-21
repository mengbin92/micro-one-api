package service

import (
	"testing"
	"time"
)

func TestSortByResetAsc_NoResetFirst(t *testing.T) {
	r1 := int64(200)
	r2 := int64(100)
	entries := []zhipuEntry{
		{resetMs: &r1, percentage: 10},
		{resetMs: nil, percentage: 0},
		{resetMs: &r2, percentage: 50},
	}
	sortByResetAsc(entries)
	if entries[0].resetMs != nil {
		t.Fatalf("expected no-reset entry first, got %+v", entries)
	}
	if entries[1].resetMs == nil || *entries[1].resetMs != 100 {
		t.Fatalf("expected resetMs=100 second, got %+v", entries[1])
	}
	if entries[2].resetMs == nil || *entries[2].resetMs != 200 {
		t.Fatalf("expected resetMs=200 last, got %+v", entries[2])
	}
}

func TestParseZhipuTiers_ExplicitUnit(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	reset5h := now.Add(5 * time.Hour).UnixMilli()
	resetWeekly := now.Add(7 * 24 * time.Hour).UnixMilli()
	data := map[string]any{
		"limits": []any{
			map[string]any{"type": "TOKENS_LIMIT", "percentage": 42.0, "unit": 3.0, "nextResetTime": float64(reset5h)},
			map[string]any{"type": "TOKENS_LIMIT", "percentage": 80.0, "unit": 6.0, "nextResetTime": float64(resetWeekly)},
			map[string]any{"type": "OTHER_LIMIT", "percentage": 99.0, "unit": 3.0},
		},
	}
	snap := parseZhipuTiers(7, data, now)
	if snap.PrimaryUsedPercent == nil || *snap.PrimaryUsedPercent != 42.0 {
		t.Fatalf("primary used percent = %v, want 42", snap.PrimaryUsedPercent)
	}
	if snap.PrimaryWindowMinutes == nil || *snap.PrimaryWindowMinutes != 300 {
		t.Fatalf("primary window = %v, want 300", snap.PrimaryWindowMinutes)
	}
	if snap.PrimaryResetAfterSeconds == nil || *snap.PrimaryResetAfterSeconds != 5*3600 {
		t.Fatalf("primary reset after = %v, want %d", snap.PrimaryResetAfterSeconds, 5*3600)
	}
	if snap.SecondaryUsedPercent == nil || *snap.SecondaryUsedPercent != 80.0 {
		t.Fatalf("secondary used percent = %v, want 80", snap.SecondaryUsedPercent)
	}
	if snap.SecondaryWindowMinutes == nil || *snap.SecondaryWindowMinutes != 10080 {
		t.Fatalf("secondary window = %v, want 10080", snap.SecondaryWindowMinutes)
	}
}

// Fallback heuristic: entries without a unit field are classified by reset
// time — the entry without reset (or with the earliest reset) is the 5h
// window, the next one is weekly. This regresses the inverted insertion-sort
// bug where no-reset entries could never move ahead of entries with a reset.
func TestParseZhipuTiers_FallbackNoUnit(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	resetWeekly := now.Add(7 * 24 * time.Hour).UnixMilli()
	data := map[string]any{
		"limits": []any{
			// weekly first (has reset), 5h second (no reset) — sort must reorder.
			map[string]any{"type": "TOKENS_LIMIT", "percentage": 80.0, "nextResetTime": float64(resetWeekly)},
			map[string]any{"type": "TOKENS_LIMIT", "percentage": 42.0},
		},
	}
	snap := parseZhipuTiers(7, data, now)
	if snap.PrimaryUsedPercent == nil || *snap.PrimaryUsedPercent != 42.0 {
		t.Fatalf("primary used percent = %v, want 42 (no-reset entry)", snap.PrimaryUsedPercent)
	}
	if snap.PrimaryResetAfterSeconds != nil {
		t.Fatalf("primary reset after = %v, want nil", *snap.PrimaryResetAfterSeconds)
	}
	if snap.SecondaryUsedPercent == nil || *snap.SecondaryUsedPercent != 80.0 {
		t.Fatalf("secondary used percent = %v, want 80", snap.SecondaryUsedPercent)
	}
}

func TestParseMinimaxTiers_GeneralModelOnly(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	end5h := now.Add(5 * time.Hour).UnixMilli()
	endWeekly := now.Add(7 * 24 * time.Hour).UnixMilli()
	root := map[string]any{
		"model_remains": []any{
			map[string]any{"model_name": "video", "current_interval_remaining_percent": 99.0},
			map[string]any{
				"model_name":                          "general",
				"current_interval_remaining_percent":  60.0,
				"end_time":                            float64(end5h),
				"current_weekly_status":               1.0,
				"current_weekly_remaining_percent":    20.0,
				"weekly_end_time":                     float64(endWeekly),
			},
		},
	}
	snap := parseMinimaxTiers(9, root, now)
	if snap.PrimaryUsedPercent == nil || *snap.PrimaryUsedPercent != 40.0 {
		t.Fatalf("primary used percent = %v, want 40 (100-60)", snap.PrimaryUsedPercent)
	}
	if snap.PrimaryResetAfterSeconds == nil || *snap.PrimaryResetAfterSeconds != 5*3600 {
		t.Fatalf("primary reset after = %v, want %d", snap.PrimaryResetAfterSeconds, 5*3600)
	}
	if snap.SecondaryUsedPercent == nil || *snap.SecondaryUsedPercent != 80.0 {
		t.Fatalf("secondary used percent = %v, want 80 (100-20)", snap.SecondaryUsedPercent)
	}
}

func TestParseMinimaxTiers_WeeklyInactive(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	root := map[string]any{
		"model_remains": []any{
			map[string]any{
				"model_name":                         "general",
				"current_interval_remaining_percent": 60.0,
				"current_weekly_status":              0.0, // inactive → no secondary
				"current_weekly_remaining_percent":   20.0,
			},
		},
	}
	snap := parseMinimaxTiers(9, root, now)
	if snap.PrimaryUsedPercent == nil || *snap.PrimaryUsedPercent != 40.0 {
		t.Fatalf("primary used percent = %v, want 40", snap.PrimaryUsedPercent)
	}
	if snap.SecondaryUsedPercent != nil {
		t.Fatalf("secondary used percent = %v, want nil (weekly inactive)", *snap.SecondaryUsedPercent)
	}
}

func TestResetAfterFromISO(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

	future := now.Add(90 * time.Minute).Format(time.RFC3339)
	if got := resetAfterFromISO(&future, now); got == nil || *got != 5400 {
		t.Fatalf("future reset = %v, want 5400", got)
	}
	past := now.Add(-time.Minute).Format(time.RFC3339)
	if got := resetAfterFromISO(&past, now); got != nil {
		t.Fatalf("past reset = %v, want nil", *got)
	}
	bad := "not-a-date"
	if got := resetAfterFromISO(&bad, now); got != nil {
		t.Fatalf("invalid reset = %v, want nil", *got)
	}
	if got := resetAfterFromISO(nil, now); got != nil {
		t.Fatalf("nil reset = %v, want nil", *got)
	}
	empty := ""
	if got := resetAfterFromISO(&empty, now); got != nil {
		t.Fatalf("empty reset = %v, want nil", *got)
	}
}

func TestDecodeJSONObject(t *testing.T) {
	if _, err := decodeJSONObject([]byte(`{"a":1}`)); err != nil {
		t.Fatalf("valid object: %v", err)
	}
	if _, err := decodeJSONObject([]byte(`{invalid`)); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
