package data

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	dbt "micro-one-api/platform/database/testutil"
)

func TestFirstContractGrayPreservesUnboundBaselineAndQuarantine(t *testing.T) {
	db := dbt.RoutingContextDB(t, "sqlite")
	r := &Repository{db: db}
	ctx := authorization.WithWriteReason(context.Background(), "first-contract bounded trial")
	now := time.Now()
	d := contractDoc("channel", 11)
	require.NoError(t, r.CreateContract(ctx, biz.UpstreamContract{Document: d, Revision: d.Revision()}, now))
	var binding contractBindingModel
	require.NoError(t, db.Where("id = ?", d.Scope.Key()).First(&binding).Error)
	binding.Current, binding.Status, binding.GrayPercent, binding.Revision = d.Revision(), "gray", 5, 1
	require.NoError(t, db.Save(&binding).Error)
	selected, baseline := 0, 0
	for i := 0; i < 256; i++ {
		snapshots, blocked, _, err := r.ResolveContracts(ctx, "channel", 11, d.Scope.Model, fmt.Sprintf("attempt-%d", i))
		require.NoError(t, err)
		require.Empty(t, blocked, "first publication must not block requests outside the approved gray percentage")
		if len(snapshots) == 0 {
			baseline++
		} else {
			selected++
			require.Len(t, snapshots, 1)
			require.Equal(t, d.Revision(), snapshots[0].Revision)
		}
	}
	require.Positive(t, selected)
	require.Greater(t, baseline, selected)
	binding.Status = "quarantined"
	require.NoError(t, db.Save(&binding).Error)
	_, blocked, _, err := r.ResolveContracts(ctx, "channel", 11, d.Scope.Model, "not-in-gray")
	require.NoError(t, err)
	require.Equal(t, []string{"openai_chat"}, blocked, "quarantine must never fall back to the unbound baseline")
}
