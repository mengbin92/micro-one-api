package biz

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/routing"
	"strings"
	"testing"
)

// createFake captures the DO handed to the data layer and echoes it back as if
// it had been inserted.
type createFake struct {
	created *RoutingGroup
	calls   int
	err     error
}

func (f *createFake) ListRoutingGroups(context.Context, RoutingGroupListOptions) ([]*RoutingGroup, error) {
	return nil, nil
}
func (f *createFake) GetRoutingGroup(_ context.Context, id int64) (*RoutingGroupDetail, error) {
	return &RoutingGroupDetail{Group: f.created, Resources: []routing.GroupResource{}}, nil
}
func (f *createFake) CreateRoutingGroup(_ context.Context, group *RoutingGroup) (*RoutingGroup, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	stored := *group
	stored.ID = 7
	f.created = &stored
	return &stored, nil
}

func TestRoutingGroupCreateValidationAndDefaults(t *testing.T) {
	ctx := context.Background()
	f := &createFake{}
	uc := NewRoutingGroupUsecase(f)

	// The owner decides status, revision and model scope; the caller supplies
	// key, label and access mode only.
	detail, err := uc.Create(ctx, "vip", "  VIP 客户  ", "付费客户", "public")
	require.NoError(t, err)
	require.Equal(t, int64(7), detail.Group.ID)
	require.Equal(t, "disabled", f.created.Status)
	require.Equal(t, int64(1), f.created.Revision)
	require.Equal(t, "all_authorized", f.created.ModelAccessMode)
	require.Equal(t, "VIP 客户", f.created.DisplayName)
	require.Equal(t, "public", f.created.AccessMode)

	// An empty display name falls back to the key; an empty mode to restricted.
	_, err = uc.Create(ctx, "pro", "", "", "")
	require.NoError(t, err)
	require.Equal(t, "pro", f.created.DisplayName)
	require.Equal(t, "restricted", f.created.AccessMode)

	validCalls := f.calls
	for _, bad := range []struct{ key, display, access string }{
		{"", "x", ""},                          // empty key
		{" vip", "x", ""},                      // leading whitespace
		{"vip ", "x", ""},                      // trailing whitespace
		{"a,b", "x", ""},                       // CSV separator
		{"vip\n", "x", ""},                     // control character
		{strings.Repeat("k", 1025), "x", ""},   // wider than the column
		{"vip", "x", "everyone"},               // unknown access mode
		{"vip", strings.Repeat("n", 4097), ""}, // unbounded label
	} {
		_, err = uc.Create(ctx, bad.key, bad.display, "", bad.access)
		require.ErrorIs(t, err, ErrRoutingGroupInvalid, "key=%q access=%q", bad.key, bad.access)
	}
	require.Equal(t, validCalls, f.calls)

	// A storage without the create capability fails closed instead of silently
	// claiming success.
	_, err = NewRoutingGroupUsecase(&readOnlyGroupFake{}).Create(ctx, "vip", "", "", "")
	require.ErrorIs(t, err, ErrRoutingGroupStorage)
}

// readOnlyGroupFake implements the read repo only, so the create capability is
// absent — exactly the shape of a data layer that predates explicit creation.
type readOnlyGroupFake struct{}

func (readOnlyGroupFake) ListRoutingGroups(context.Context, RoutingGroupListOptions) ([]*RoutingGroup, error) {
	return nil, nil
}
func (readOnlyGroupFake) GetRoutingGroup(context.Context, int64) (*RoutingGroupDetail, error) {
	return nil, ErrRoutingGroupNotFound
}

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
