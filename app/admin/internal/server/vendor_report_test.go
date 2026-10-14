package server

import (
	"github.com/stretchr/testify/require"
	v "micro-one-api/api/billing/v1"
	"micro-one-api/pkg/jsonx"
	"testing"
)

func TestVendorReportHTTPPreservesWeeklyOwnerProjection(t *testing.T) {
	r := &v.ListVendorReportsReply{DailySummaryJson: []byte(`[]`), WeeklyReviewJson: []byte(`{"pending":1,"unknown_amounts":2}`)}
	b, err := jsonx.Marshal(vendorReportsHTTPReply(r, nil))
	require.NoError(t, err)
	require.Contains(t, string(b), `"weekly_review":{"pending":1,"unknown_amounts":2}`)
}
