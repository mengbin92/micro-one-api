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
