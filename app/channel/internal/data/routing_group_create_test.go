package data

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"micro-one-api/app/channel/internal/biz"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestCreateRoutingGroupWithSingleConnection(t *testing.T) {
	db, gdb := routingGroupFixture(t)
	db.SetMaxOpenConns(1)
	uc := biz.NewRoutingGroupUsecase(NewRoutingGroupRepo(&Repository{db: gdb}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	detail, err := uc.Create(ctx, "single", "Single", "", "restricted")
	require.NoError(t, err)
	require.Equal(t, "single", detail.Group.Key)
}

func TestCreateRoutingGroupPersistsAndRejectsDuplicates(t *testing.T) {
	_, gdb := routingGroupFixture(t)
	repo := &routingGroupRepo{data: &Repository{db: gdb}}
	ctx := context.Background()

	created, err := repo.CreateRoutingGroup(ctx, &biz.RoutingGroup{Key: "vip", DisplayName: "VIP", Status: "disabled", AccessMode: "restricted", ModelAccessMode: "all_authorized", Revision: 1})
	require.NoError(t, err)
	require.NotZero(t, created.ID)
	require.Equal(t, "vip", created.Key)

	var row routingGroupModel
	require.NoError(t, gdb.Where(map[string]any{"key": "vip"}).First(&row).Error)
	require.Equal(t, int64(1), row.Revision)
	require.Equal(t, "disabled", row.Status)

	// The revision-1 invalidation is written in the same transaction and is
	// still undelivered.
	var pending int64
	require.NoError(t, gdb.Table("routing_change_outbox").
		Where("owner = ? AND kind = ? AND aggregate_id = ? AND revision = ? AND delivered_at = 0", "channel", "group", created.ID, 1).
		Count(&pending).Error)
	require.EqualValues(t, 1, pending)

	// A duplicate key is a typed conflict and leaves the projection untouched.
	_, err = repo.CreateRoutingGroup(ctx, &biz.RoutingGroup{Key: "vip", DisplayName: "VIP again", Status: "disabled", AccessMode: "restricted", ModelAccessMode: "all_authorized", Revision: 1})
	require.ErrorIs(t, err, biz.ErrRoutingGroupExists)
	var count int64
	require.NoError(t, gdb.Model(&routingGroupModel{}).Where(map[string]any{"key": "vip"}).Count(&count).Error)
	require.EqualValues(t, 1, count)

	// Keys stay byte-exact: case variants are distinct groups.
	other, err := repo.CreateRoutingGroup(ctx, &biz.RoutingGroup{Key: "VIP", DisplayName: "VIP upper", Status: "disabled", AccessMode: "restricted", ModelAccessMode: "all_authorized", Revision: 1})
	require.NoError(t, err)
	require.NotEqual(t, created.ID, other.ID)
}

func TestCreateRoutingGroupRequiresSchema(t *testing.T) {
	// A database without the 092/095 tables must fail closed rather than
	// creating a row the serving path cannot read.
	raw, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "empty.db")), &gorm.Config{})
	require.NoError(t, err)
	repo := &routingGroupRepo{data: &Repository{db: raw}}
	_, err = repo.CreateRoutingGroup(context.Background(), &biz.RoutingGroup{Key: "vip", Revision: 1})
	require.ErrorIs(t, err, biz.ErrRoutingGroupMigrationRequired)
}
