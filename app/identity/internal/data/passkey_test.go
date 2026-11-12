package data

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"micro-one-api/app/identity/internal/biz"
)

func TestPasskeyRepoDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			f := newIAMA4Fixture(t, driver)
			_, err := f.uc.EnsureRootAdmin(f.ctx)
			require.NoError(t, err)
			user := f.register("passkey-user")
			otherUser := f.register("passkey-other")
			repo := NewPasskeyRepo(f.repo.(*iamRepo).data, []byte("01234567890123456789012345678901"))

			cred := biz.PasskeyCredential{
				ID:          []byte("cred-1"),
				PublicKey:   []byte("cose-public-key"),
				Attestation: "none",
				Transport:   []string{"usb", "nfc"},
				Name:        "YubiKey",
				SignCount:   7,
				CreatedAt:   time.Now().UnixMilli(),
			}
			f.setup(func(ctx context.Context, tx biz.IAMTx) error {
				return repo.Save(ctx, tx, user.ID, cred)
			})

			list := func(uid int64) []biz.PasskeyCredential {
				t.Helper()
				var listed []biz.PasskeyCredential
				require.NoError(t, f.runner.ReadIAMSnapshot(f.ctx, func(ctx context.Context, tx biz.IAMTx) error {
					var err error
					listed, err = repo.List(ctx, tx, uid)
					return err
				}))
				return listed
			}

			listed := list(user.ID)
			require.Len(t, listed, 1)
			require.Equal(t, cred.ID, listed[0].ID)
			require.Equal(t, cred.PublicKey, listed[0].PublicKey)
			require.Equal(t, cred.Attestation, listed[0].Attestation)
			require.Equal(t, cred.Transport, listed[0].Transport)
			require.Equal(t, cred.Name, listed[0].Name)
			require.Equal(t, cred.SignCount, listed[0].SignCount)

			// Public key is ciphertext at rest, not the raw COSE bytes.
			var raw string
			require.NoError(t, f.db.Raw("SELECT public_key FROM identity_passkey WHERE user_id = ?", user.ID).Scan(&raw).Error)
			require.NotContains(t, raw, "cose-public-key")

			// Other users never see the credential.
			require.Empty(t, list(otherUser.ID))

			// Save again updates last_used_at without duplicating.
			updated := listed[0]
			updated.LastUsedAt = time.Now().UnixMilli()
			f.setup(func(ctx context.Context, tx biz.IAMTx) error {
				return repo.Save(ctx, tx, user.ID, updated)
			})
			listed = list(user.ID)
			require.Len(t, listed, 1)
			require.Equal(t, updated.LastUsedAt, listed[0].LastUsedAt)

			// A credential_id owned by another account must not be overwritten
			// or re-bound (enrollment is client-supplied with no attestation
			// verification, so the repo rejects cross-user conflicts).
			foreign := cred
			foreign.PublicKey = []byte("attacker-key")
			f.setup(func(ctx context.Context, tx biz.IAMTx) error {
				require.ErrorIs(t, repo.Save(ctx, tx, otherUser.ID, foreign), biz.ErrMFAInvalid)
				return nil
			})
			listed = list(user.ID)
			require.Len(t, listed, 1)
			require.Equal(t, cred.PublicKey, listed[0].PublicKey)

			// Delete is scoped to the owning user and reports missing rows.
			f.setup(func(ctx context.Context, tx biz.IAMTx) error {
				require.ErrorIs(t, repo.Delete(ctx, tx, otherUser.ID, cred.ID), biz.ErrMFAInvalid)
				return nil
			})
			f.setup(func(ctx context.Context, tx biz.IAMTx) error {
				return repo.Delete(ctx, tx, user.ID, cred.ID)
			})
			require.Empty(t, list(user.ID))
		})
	}
}
