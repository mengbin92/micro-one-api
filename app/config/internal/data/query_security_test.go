package data

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"micro-one-api/app/config/internal/biz"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestConfigQueriesBindUntrustedNamespaceAndKey(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "sqlite")
	r := &Repository{db: db}
	ctx := context.Background()
	namespace := "tenant' OR 1=1 --"
	key := "key'; DROP TABLE configs; --"
	entry := &biz.ConfigEntry{Namespace: namespace, Key: key, Value: "original", UpdatedAt: time.Now()}
	require.NoError(t, r.Set(ctx, entry))
	require.NoError(t, r.Set(ctx, &biz.ConfigEntry{Namespace: "other", Key: "safe", Value: "untouched", UpdatedAt: time.Now()}))
	queries := 0
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("assert_config_bindings", func(query *gorm.DB) {
		if !strings.Contains(query.Statement.SQL.String(), "configs") {
			return
		}
		queries++
		require.NotContains(t, query.Statement.SQL.String(), namespace)
		require.NotContains(t, query.Statement.SQL.String(), key)
		if strings.Contains(query.Statement.SQL.String(), "namespace") {
			require.Contains(t, query.Statement.Vars, namespace)
			require.Contains(t, query.Statement.Vars, key)
		}
	}))
	got, err := r.Get(ctx, namespace, key)
	require.NoError(t, err)
	require.Equal(t, "original", got.Value)
	entry.Value = "updated"
	require.NoError(t, r.Set(ctx, entry))
	got, err = r.Get(ctx, namespace, key)
	require.NoError(t, err)
	require.Equal(t, "updated", got.Value)
	require.NoError(t, r.Delete(ctx, namespace, key))
	_, err = r.Get(ctx, namespace, key)
	require.ErrorIs(t, err, biz.ErrConfigNotFound)
	require.NoError(t, db.Callback().Query().Remove("assert_config_bindings"))
	other, err := r.Get(ctx, "other", "safe")
	require.NoError(t, err)
	require.Equal(t, "untouched", other.Value)
	require.GreaterOrEqual(t, queries, 5)
}
