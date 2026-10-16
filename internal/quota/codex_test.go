package quota

import (
	"testing"
	"time"
)

func TestParseCodexSnapshot(t *testing.T) {
	now := time.Unix(123, 0)
	body := []byte(`{
		"usage": {
			"primary": {"used_percent": 96.5, "reset_after_seconds": 120, "window_minutes": 300},
			"secondary": {"usedPercent": 12.5, "resetAfterSeconds": 3600, "windowMinutes": 10080},
			"primary_over_secondary_percent": 18.2
		}
	}`)
	snapshot, ok := ParseCodexSnapshot(body, now)
	if !ok {
		t.Fatal("snapshot not parsed")
	}
	if snapshot.PrimaryUsedPercent == nil || *snapshot.PrimaryUsedPercent != 96.5 {
		t.Fatalf("primary used = %v", snapshot.PrimaryUsedPercent)
	}
	if snapshot.SecondaryWindowMinutes == nil || *snapshot.SecondaryWindowMinutes != 10080 {
		t.Fatalf("secondary window = %v", snapshot.SecondaryWindowMinutes)
	}
	if snapshot.PrimaryOverSecondaryPercent == nil || *snapshot.PrimaryOverSecondaryPercent != 18.2 {
		t.Fatalf("ratio = %v", snapshot.PrimaryOverSecondaryPercent)
	}
	if !snapshot.UpdatedAt.Equal(now) {
		t.Fatalf("updated_at = %s", snapshot.UpdatedAt)
	}
}

func TestShouldAutoPause(t *testing.T) {
	primary := 95.1
	if !ShouldAutoPause(&CodexSnapshot{PrimaryUsedPercent: &primary}, 95, 100) {
		t.Fatal("expected primary threshold to auto-pause")
	}
	secondary := 99.9
	if ShouldAutoPause(&CodexSnapshot{SecondaryUsedPercent: &secondary}, 95, 100) {
		t.Fatal("did not expect secondary below threshold to auto-pause")
	}
}

func TestPartialCodexSnapshotKeepsWindowIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, body         string
		primary, secondary bool
		pause              bool
	}{
		{"primary only", `{"primary":{"used_percent":96,"window_minutes":300}}`, true, false, true},
		{"secondary below threshold", `{"secondary":{"used_percent":96,"window_minutes":10080}}`, false, true, false},
		{"secondary exhausted", `{"secondary":{"used_percent":100,"window_minutes":10080}}`, false, true, true},
		{"unlabelled windows", `[{"used_percent":20},{"used_percent":30}]`, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, ok := ParseCodexSnapshot([]byte(tc.body), time.Unix(123, 0))
			if !ok {
				t.Fatal("snapshot not parsed")
			}
			if (snapshot.PrimaryUsedPercent != nil) != tc.primary || (snapshot.SecondaryUsedPercent != nil) != tc.secondary {
				t.Fatalf("window identity changed: primary=%v secondary=%v", snapshot.PrimaryUsedPercent, snapshot.SecondaryUsedPercent)
			}
			if pause := ShouldAutoPause(snapshot, 95, 100); pause != tc.pause {
				t.Fatalf("auto-pause=%v, want %v", pause, tc.pause)
			}
		})
	}
}
