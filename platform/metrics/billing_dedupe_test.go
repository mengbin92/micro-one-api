package metrics

import (
	"github.com/prometheus/client_golang/prometheus/testutil"
	"testing"
)

func TestBillingDedupeConflictLabelsAreBounded(t *testing.T) {
	counter := BillingDedupeConflicts.WithLabelValues("other", "duplicate")
	before := testutil.ToFloat64(counter)
	RecordBillingDedupeConflict("request-123")
	RecordBillingDedupeConflict("user-456")
	if testutil.ToFloat64(counter) != before+2 {
		t.Fatal("unknown operations must collapse to other")
	}
}
