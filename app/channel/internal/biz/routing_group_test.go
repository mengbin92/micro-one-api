package biz

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

type backfillFake struct{ calls int }

func (f *backfillFake) ApplyRoutingGroupBackfill(context.Context, *GroupAuditReport) (*RoutingGroupBackfillResult, error) {
	f.calls++
	return &RoutingGroupBackfillResult{}, nil
}
func TestRoutingGroupBackfillRejectsIncompleteBaseline(t *testing.T) {
	f := &backfillFake{}
	uc := NewRoutingGroupBackfillUsecase(f)
	base := func() *GroupAuditReport {
		return &GroupAuditReport{Version: 2, Groups: []string{"default"}, Models: []string{"model"}, Migration: GroupAuditMigration{ReadyForBackfill: true, Groups: []GroupAuditMigrationGroup{{LegacyKey: "default", InitialStatus: "enabled", AccessMode: "restricted"}}}}
	}
	for _, mutate := range []func(*GroupAuditReport){func(r *GroupAuditReport) { r.Version = 1 }, func(r *GroupAuditReport) { r.Migration.ReadyForBackfill = false }, func(r *GroupAuditReport) { r.Issues = []GroupAuditIssue{{Blocking: true}} }, func(r *GroupAuditReport) { r.Migration.Groups[0].AccessMode = "public" }, func(r *GroupAuditReport) { r.Groups = []string{"other"} }, func(r *GroupAuditReport) { r.Models = nil }} {
		report := base()
		mutate(report)
		_, err := uc.Apply(context.Background(), report)
		require.Error(t, err)
	}
	require.Zero(t, f.calls)
	_, err := uc.Apply(context.Background(), base())
	require.NoError(t, err)
	require.Equal(t, 1, f.calls)
}
