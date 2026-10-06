package authzquery

import (
	"context"
	"time"

	"gorm.io/gorm"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/database/xdb"
)

// RunInTx refreshes identity decisions outside every replayable resource
// transaction attempt. The callback receives that attempt's fresh context;
// owner facts and expiry are checked with Require under row locks/CAS.
func RunInTx(ctx context.Context, db *gorm.DB, attempts int, write func(context.Context, *gorm.DB) error) error {
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 10 * time.Millisecond):
			}
		}
		current, refreshErr := authorization.Refresh(ctx)
		if refreshErr != nil {
			return refreshErr
		}
		err = db.WithContext(current).Transaction(func(tx *gorm.DB) error { return write(current, tx) })
		if err == nil || !xdb.IsSQLiteBusy(err) {
			return err
		}
	}
	return err
}
