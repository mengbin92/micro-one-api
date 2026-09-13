package routingoutbox

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"micro-one-api/platform/database/testutil"
	"micro-one-api/platform/events"
	"testing"
)

func TestTransactionalDelivery(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := testutil.RoutingContextDB(t, driver)
			ctx := context.Background()
			fail := errors.New("publish interrupted")
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				require.NoError(t, Enqueue(tx, "identity", "user", 1, 2))
				return fail
			}), fail)
			var count int64
			require.NoError(t, db.Model(&record{}).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return Enqueue(tx, "identity", "user", 1, 2) }))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return Enqueue(tx, "channel", "group", 1, 2) }))
			require.ErrorIs(t, Dispatch(ctx, db, "identity", func(context.Context, string, any) error { return fail }), fail)
			require.NoError(t, db.Model(&record{}).Where("delivered_at = 0").Count(&count).Error)
			require.EqualValues(t, 2, count)
			calls := 0
			publish := func(_ context.Context, topic string, value any) error {
				require.Equal(t, Topic, topic)
				require.Equal(t, Change{Owner: "identity", Kind: "user", AggregateID: 1, Revision: 2}, value)
				calls++
				return nil
			}
			require.NoError(t, Dispatch(ctx, db, "identity", publish))
			require.NoError(t, Dispatch(ctx, db, "identity", publish))
			require.Equal(t, 1, calls)
			require.NoError(t, db.Model(&record{}).Where("owner = ? AND delivered_at = 0", "channel").Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestInvalidationReorderingAndFailure(t *testing.T) {
	fail := true
	var revisions []int64
	handler := Invalidator(func(_ context.Context, change Change) error {
		if fail {
			return errors.New("cache unavailable")
		}
		revisions = append(revisions, change.Revision)
		return nil
	})
	event := func(rev int64) events.Event {
		return events.Event{Topic: Topic, Payload: Change{Owner: "identity", Kind: "user", AggregateID: 1, Revision: rev}}
	}
	require.Error(t, handler(context.Background(), event(3)))
	fail = false
	for _, rev := range []int64{3, 2, 3, 4, 1} {
		require.NoError(t, handler(context.Background(), event(rev)))
	}
	require.Equal(t, []int64{3, 4}, revisions)
}
