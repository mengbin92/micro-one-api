package data

import (
	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/authorization"
	"testing"
	"time"
)

func TestManagedBackupStepUpIsSessionBoundInLegacyAndIAM(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			f := newIAMA4Fixture(t, driver)
			user := f.register("backup-security")
			srv := miniredis.RunT(t)
			uc := securityReplica(t, f, srv)
			t.Setenv("IDENTITY_STEP_UP_ENABLED", "true")
			raw := f.token(user.ID, "backup-writer", 0, time.Now().Add(time.Hour))
			other := f.token(user.ID, "backup-other-session", 0, time.Now().Add(time.Hour))
			request := authorization.ResourceRequest{ExecutionPoint: "admin.backups", Operation: "system.option.security.update"}
			f.mode("legacy", "idle")
			_, err := uc.GetResourceAuthorization(f.ctx, raw, request)
			require.ErrorIs(t, err, biz.ErrStepUpRequired)
			require.NoError(t, uc.VerifyStepUp(f.ctx, raw, "password123", ""))
			_, err = uc.GetResourceAuthorization(f.ctx, raw, request)
			require.NoError(t, err)
			_, err = uc.GetResourceAuthorization(f.ctx, other, request)
			require.ErrorIs(t, err, biz.ErrStepUpRequired)
			srv.FastForward(5 * time.Minute)
			_, err = uc.GetResourceAuthorization(f.ctx, raw, request)
			require.ErrorIs(t, err, biz.ErrStepUpRequired)
			// Read-only receipts do not require a write proof.
			request.Operation = "system.option.read"
			_, err = uc.GetResourceAuthorization(f.ctx, raw, request)
			require.NoError(t, err)
			f.mode("iam", "complete")
			request.Operation = "system.option.security.update"
			_, err = uc.GetResourceAuthorization(f.ctx, raw, request)
			require.ErrorIs(t, err, biz.ErrStepUpRequired)
		})
	}
}
