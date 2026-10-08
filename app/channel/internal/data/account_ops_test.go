package data

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/platform/database/testutil"
)

func TestAccountRecoveryRejectsNewIncident(t *testing.T) {
	for _, memory := range []bool{false, true} {
		t.Run(fmt.Sprintf("memory=%v", memory), func(t *testing.T) {
			repo := setupChannelTestDB(t)
			if memory {
				repo = &Repository{subAccounts: make(map[int64]*biz.SubscriptionAccount)}
			}
			ctx := context.Background()
			a := &biz.SubscriptionAccount{Name: "recovery", Platform: "codex", Status: 1, Group: "default"}
			require.NoError(t, repo.CreateSubscriptionAccount(ctx, a))
			until := time.Unix(500, 0)
			require.NoError(t, repo.SetTempUnschedulable(ctx, a.ID, until, "upstream 429"))
			observed, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
			require.NoError(t, err)
			// Simulate the ABA case without depending on wall-clock timing:
			// same payload and timestamp, distinct incident versions.
			first := stampRecoveryMetadata(observed.Metadata, "upstream 429", 500, 100)
			second := stampRecoveryMetadata(first, "upstream 429", 500, 100)
			require.NotEqual(t, subscriptionAccountMetadataValue(first, "recovery_revision"), subscriptionAccountMetadataValue(second, "recovery_revision"))
			require.NoError(t, repo.SetTempUnschedulable(ctx, a.ID, until, "upstream 429"))
			newer, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
			require.NoError(t, err)
			cleared, err := repo.ClearRecoveryMarkers(ctx, observed.RecoveryState(), time.Unix(1000, 0))
			require.NoError(t, err)
			require.False(t, cleared)
			got, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
			require.NoError(t, err)
			require.Equal(t, newer.RecoveryState(), got.RecoveryState())
		})
	}
}

func TestRepeatedAccountErrorInvalidatesRecovery(t *testing.T) {
	for _, memory := range []bool{false, true} {
		t.Run(fmt.Sprintf("memory=%v", memory), func(t *testing.T) {
			repo := setupChannelTestDB(t)
			if memory {
				repo = &Repository{subAccounts: make(map[int64]*biz.SubscriptionAccount)}
			}
			ctx := context.Background()
			a := &biz.SubscriptionAccount{Name: "error", Platform: "codex", Status: 1, Group: "default"}
			require.NoError(t, repo.CreateSubscriptionAccount(ctx, a))
			require.NoError(t, repo.SetSubscriptionAccountError(ctx, a.ID, "upstream 429"))
			observed, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
			require.NoError(t, err)
			require.NoError(t, repo.SetSubscriptionAccountError(ctx, a.ID, "upstream 429"))
			cleared, err := repo.ClearRecoveryMarkers(ctx, observed.RecoveryState(), time.Now())
			require.NoError(t, err)
			require.False(t, cleared)
			got, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
			require.NoError(t, err)
			require.Equal(t, "upstream 429", got.LastError)
		})
	}
}

func TestAccountErrorAuthorizationNeverAutoRecovers(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	a := &biz.SubscriptionAccount{Name: "auth-error", Platform: "codex", Status: 1, Group: "default"}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, a))
	require.NoError(t, repo.SetSubscriptionAccountError(ctx, a.ID, "codex upstream 401 unauthorized"))
	observed, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
	require.NoError(t, err)
	cleared, err := repo.ClearRecoveryMarkers(ctx, observed.RecoveryState(), time.Now())
	require.NoError(t, err)
	require.False(t, cleared)
	got, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, observed.Metadata, got.Metadata)
}

func TestTemporaryFailureCannotDowngradeManualRecovery(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	a := &biz.SubscriptionAccount{Name: "manual-stays-manual", Platform: "codex", Status: 1, Group: "default"}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, a))
	require.NoError(t, repo.SetTempUnschedulable(ctx, a.ID, time.Unix(500, 0), "upstream 401 unauthorized"))
	require.NoError(t, repo.SetTempUnschedulable(ctx, a.ID, time.Unix(500, 0), "upstream 429"))
	observed, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
	require.NoError(t, err)
	cleared, err := repo.ClearRecoveryMarkers(ctx, observed.RecoveryState(), time.Unix(1000, 0))
	require.NoError(t, err)
	require.False(t, cleared, "a later transient error cannot substitute for manual recovery approval")
}

func TestFullAccountUpdateCannotOverwriteNewIncident(t *testing.T) {
	for _, memory := range []bool{false, true} {
		t.Run(fmt.Sprintf("memory=%v", memory), func(t *testing.T) {
			repo := setupChannelTestDB(t)
			if memory {
				repo = &Repository{subAccounts: make(map[int64]*biz.SubscriptionAccount)}
			}
			ctx := context.Background()
			a := &biz.SubscriptionAccount{Name: "rename", Platform: "codex", Status: 1, Group: "default"}
			require.NoError(t, repo.CreateSubscriptionAccount(ctx, a))
			edit, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
			require.NoError(t, err)
			require.NoError(t, repo.SetTempUnschedulable(ctx, a.ID, time.Unix(500, 0), "upstream 401 unauthorized"))
			edit.Name = "renamed"
			require.ErrorIs(t, repo.UpdateSubscriptionAccount(ctx, edit), biz.ErrAccountRecoveryStateChanged)
			got, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
			require.NoError(t, err)
			require.Equal(t, biz.RecoveryPolicyManual, subscriptionAccountMetadataValue(got.Metadata, "recovery_policy"))
			require.False(t, got.CanAutoRecoverAt(time.Unix(1000, 0)))
		})
	}
}

func TestTransientPausePreservesAdministrativeDisable(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	a := &biz.SubscriptionAccount{Name: "disabled", Platform: "codex", Status: biz.ChannelStatusDisabled, Group: "default", Models: []string{"gpt-5"}}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, a))
	require.NoError(t, repo.AutoPauseAccount(ctx, a.ID, "codex quota exhausted"))
	got, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
	require.NoError(t, err)
	require.EqualValues(t, biz.ChannelStatusDisabled, got.Status)
	for _, ability := range loadSubscriptionAbilities(t, repo, a.ID) {
		require.Zero(t, ability.Enabled)
	}
}

func TestAccountScanDoesNotExposeStoredSnapshotPointers(t *testing.T) {
	used := float64(100)
	repo := &Repository{subAccounts: map[int64]*biz.SubscriptionAccount{1: {ID: 1, PrimaryQuotaUsedPercent: &used}}}
	accounts, err := repo.ScanSubscriptionAccounts(context.Background(), biz.AccountScan{Limit: 1})
	require.NoError(t, err)
	*accounts[0].PrimaryQuotaUsedPercent = 0
	require.Equal(t, float64(100), *repo.subAccounts[1].PrimaryQuotaUsedPercent)
}

func TestQuotaResetRejectsChangedTimezone(t *testing.T) {
	repo := setupChannelTestDB(t)
	require.NoError(t, repo.db.AutoMigrate(&subscriptionAccountQuotaResetRunModel{}))
	ctx := context.Background()
	at := time.Date(2026, 10, 8, 1, 0, 0, 0, time.UTC)
	a := &biz.SubscriptionAccount{Name: "timezone-race", Platform: "codex", Status: 1, Group: "default", QuotaResetStrategy: "fixed", QuotaTimezone: "Asia/Shanghai"}
	a.QuotaDailyWindowStart = a.FixedQuotaWindowStart(at, "daily")
	a.QuotaDailyUsedUSD = 0.75
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, a))
	// A worker scanned the account while it still used UTC. A later timezone
	// change and charge already established the new local day's window.
	run := &biz.SubscriptionAccountQuotaResetRun{AccountID: a.ID, Scope: "daily", Strategy: "fixed", Timezone: "UTC", WindowStart: at.Truncate(24 * time.Hour).Unix(), ResetAt: at}
	require.ErrorIs(t, repo.RecordQuotaResetAndReset(ctx, run), biz.ErrQuotaResetRunStale)
	got, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, 0.75, got.QuotaDailyUsedUSD)
	var count int64
	require.NoError(t, repo.db.Model(&subscriptionAccountQuotaResetRunModel{}).Count(&count).Error)
	require.Zero(t, count, "stale reset must not consume a reset-run dedupe key")
}

func TestAccountOpsAcrossDialects(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := testutil.RoutingContextDB(t, dialect)
			repo := &Repository{db: db}
			ctx := context.Background()
			at := time.Now().UTC()
			create := func(name string) *biz.SubscriptionAccount {
				a := &biz.SubscriptionAccount{Name: name, Platform: "codex", Group: "default", Models: []string{"gpt-5"}, Status: 1}
				require.NoError(t, repo.CreateSubscriptionAccount(ctx, a))
				return a
			}
			t.Run("concurrent recovery", func(t *testing.T) {
				a := create("concurrent-recovery")
				require.NoError(t, repo.SetTempUnschedulable(ctx, a.ID, at.Add(-time.Minute), "upstream 429"))
				observed, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
				require.NoError(t, err)
				if dialect == "sqlite" {
					sqlDB, err := db.DB()
					require.NoError(t, err)
					sqlDB.SetMaxOpenConns(1)
				}
				var wg sync.WaitGroup
				type result struct {
					cleared bool
					err     error
				}
				results := make(chan result, 2)
				for range 2 {
					wg.Go(func() {
						cleared, err := repo.ClearRecoveryMarkers(ctx, observed.RecoveryState(), at)
						results <- result{cleared, err}
					})
				}
				wg.Wait()
				close(results)
				success := 0
				for result := range results {
					require.NoError(t, result.err)
					if result.cleared {
						success++
					}
				}
				require.Equal(t, 1, success)
				got, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
				require.NoError(t, err)
				require.Zero(t, got.RateLimitedUntil)
				require.Empty(t, got.Metadata)
			})
			t.Run("authorization and administrative disable", func(t *testing.T) {
				a := create("auth-policy")
				require.NoError(t, repo.SetSubscriptionAccountError(ctx, a.ID, "codex upstream 401 unauthorized"))
				observed, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
				require.NoError(t, err)
				cleared, err := repo.ClearRecoveryMarkers(ctx, observed.RecoveryState(), at)
				require.NoError(t, err)
				require.False(t, cleared)
				require.NoError(t, repo.SetTempUnschedulable(ctx, a.ID, at.Add(-time.Minute), "upstream 429"))
				observed, err = repo.FindSubscriptionAccountByID(ctx, a.ID)
				require.NoError(t, err)
				cleared, err = repo.ClearRecoveryMarkers(ctx, observed.RecoveryState(), at)
				require.NoError(t, err)
				require.False(t, cleared, "a transient failure cannot downgrade manual recovery")
				require.NoError(t, repo.ChangeSubscriptionAccountStatus(ctx, a.ID, biz.ChannelStatusDisabled))
				require.NoError(t, repo.AutoPauseAccount(ctx, a.ID, "codex quota exhausted"))
				got, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
				require.NoError(t, err)
				require.EqualValues(t, biz.ChannelStatusDisabled, got.Status)
			})
			t.Run("full update fences new incident", func(t *testing.T) {
				a := create("full-update-race")
				edit, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
				require.NoError(t, err)
				require.NoError(t, repo.SetTempUnschedulable(ctx, a.ID, at.Add(-time.Minute), "upstream 401 unauthorized"))
				edit.Name = "renamed"
				require.ErrorIs(t, repo.UpdateSubscriptionAccount(ctx, edit), biz.ErrAccountRecoveryStateChanged)
				edit, err = repo.FindSubscriptionAccountByID(ctx, a.ID)
				require.NoError(t, err)
				edit.Name = "renamed"
				require.NoError(t, repo.UpdateSubscriptionAccount(ctx, edit))
				got, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
				require.NoError(t, err)
				require.Equal(t, "renamed", got.Name)
				require.Equal(t, biz.RecoveryPolicyManual, subscriptionAccountMetadataValue(got.Metadata, "recovery_policy"))
			})
			t.Run("stale reset configuration", func(t *testing.T) {
				a := create("stale-reset")
				clock := time.Date(2026, 10, 8, 1, 0, 0, 0, time.UTC)
				a.QuotaResetStrategy, a.QuotaTimezone = "fixed", "Asia/Shanghai"
				start := a.FixedQuotaWindowStart(clock, "daily")
				require.NoError(t, db.Model(&subscriptionAccountModel{}).Where("id = ?", a.ID).Updates(map[string]any{
					"quota_reset_strategy": "fixed", "quota_timezone": "Asia/Shanghai", "quota_daily_window_start": start, "quota_daily_used_usd": 0.75,
				}).Error)
				run := &biz.SubscriptionAccountQuotaResetRun{AccountID: a.ID, Scope: "daily", Strategy: "fixed", Timezone: "UTC", WindowStart: clock.Truncate(24 * time.Hour).Unix(), ResetAt: clock}
				require.ErrorIs(t, repo.RecordQuotaResetAndReset(ctx, run), biz.ErrQuotaResetRunStale)
				got, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
				require.NoError(t, err)
				require.Equal(t, 0.75, got.QuotaDailyUsedUSD)
				// A new scan of the current configuration can still record its run.
				run.Timezone, run.WindowStart = "Asia/Shanghai", start
				require.NoError(t, repo.RecordQuotaResetAndReset(ctx, run))
				// Keep this fixture outside the later fixed-only pagination test.
				require.NoError(t, db.Model(&subscriptionAccountModel{}).Where("id = ?", a.ID).Update("quota_reset_strategy", "rolling").Error)
			})
			t.Run("new local exhaustion", func(t *testing.T) {
				a := create("local-exhaustion")
				require.NoError(t, repo.SetTempUnschedulable(ctx, a.ID, at.Add(-time.Minute), "upstream 429"))
				observed, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
				require.NoError(t, err)
				// Quota changed after scanning, without modifying recovery metadata.
				require.NoError(t, db.Model(&subscriptionAccountModel{}).Where("id = ?", a.ID).Updates(map[string]any{"quota_limit_usd": 1, "quota_used_usd": 1}).Error)
				cleared, err := repo.ClearRecoveryMarkers(ctx, observed.RecoveryState(), at)
				require.NoError(t, err)
				require.False(t, cleared)
				got, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
				require.NoError(t, err)
				require.Equal(t, observed.Metadata, got.Metadata)
			})
			t.Run("new snapshot exhaustion", func(t *testing.T) {
				a := create("snapshot-exhaustion")
				require.NoError(t, repo.AutoPauseAccount(ctx, a.ID, "codex snapshot exhausted"))
				observed, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
				require.NoError(t, err)
				primary, secondary := float64(10), float64(100)
				require.NoError(t, repo.RecordAccountQuotaSnapshot(ctx, &biz.AccountQuotaSnapshot{AccountID: a.ID, PrimaryUsedPercent: &primary, SecondaryUsedPercent: &secondary, UpdatedAt: at}))
				cleared, err := repo.ClearRecoveryMarkers(ctx, observed.RecoveryState(), at)
				require.NoError(t, err)
				require.False(t, cleared)
			})
			t.Run("auto waits for upstream quota", func(t *testing.T) {
				a := create("auto-snapshot-exhaustion")
				require.NoError(t, repo.SetTempUnschedulable(ctx, a.ID, at.Add(-time.Minute), "upstream 429"))
				observed, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
				require.NoError(t, err)
				used := float64(100)
				require.NoError(t, repo.RecordAccountQuotaSnapshot(ctx, &biz.AccountQuotaSnapshot{AccountID: a.ID, SecondaryUsedPercent: &used, UpdatedAt: at}))
				cleared, err := repo.ClearRecoveryMarkers(ctx, observed.RecoveryState(), at)
				require.NoError(t, err)
				require.False(t, cleared)
			})
			t.Run("concurrent alert and new incident", func(t *testing.T) {
				a := create("alert-and-incident")
				var wg sync.WaitGroup
				results := make(chan error, 2)
				wg.Go(func() { results <- repo.SetTempUnschedulable(ctx, a.ID, at.Add(time.Minute), "upstream 429") })
				wg.Go(func() { results <- repo.StampQuotaAlertMetadata(ctx, a.ID, "near_exhausted", at.Unix()) })
				wg.Wait()
				close(results)
				for err := range results {
					require.NoError(t, err)
				}
				got, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
				require.NoError(t, err)
				require.Equal(t, "upstream 429", subscriptionAccountMetadataValue(got.Metadata, "unschedulable_reason"))
				require.NotEmpty(t, subscriptionAccountMetadataValue(got.Metadata, "recovery_revision"))
				require.Equal(t, "near_exhausted", subscriptionAccountMetadataValue(got.Metadata, "last_quota_alert_kind"))
			})
			t.Run("legacy markers and unrelated metadata", func(t *testing.T) {
				a := create("legacy-recovery")
				metadata := `{"recovery_policy":"auto","unschedulable_reason":"429","last_error":"429","provider":"keep"}`
				require.NoError(t, db.Model(&subscriptionAccountModel{}).Where("id = ?", a.ID).Updates(map[string]any{"rate_limited_until": at.Add(-time.Minute).Unix(), "metadata": metadata}).Error)
				observed, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
				require.NoError(t, err)
				cleared, err := repo.ClearRecoveryMarkers(ctx, observed.RecoveryState(), at)
				require.NoError(t, err)
				require.True(t, cleared)
				got, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
				require.NoError(t, err)
				require.JSONEq(t, `{"provider":"keep"}`, got.Metadata)
				cleared, err = repo.ClearRecoveryMarkers(ctx, observed.RecoveryState(), at)
				require.NoError(t, err)
				require.False(t, cleared)
			})
			t.Run("shard pagination", func(t *testing.T) {
				var expected []int64
				for i := range 11 {
					a := create(fmt.Sprintf("shard-%d", i))
					require.NoError(t, db.Model(&subscriptionAccountModel{}).Where("id = ?", a.ID).Update("quota_reset_strategy", "fixed").Error)
					expected = append(expected, a.ID)
				}
				seen := make(map[int64]bool)
				for index := range int64(3) {
					scan := biz.AccountScan{Shard: biz.AccountScanShard{Index: index, Count: 3}, Limit: 2, FixedOnly: true}
					for {
						accounts, err := repo.ScanSubscriptionAccounts(ctx, scan)
						require.NoError(t, err)
						for _, a := range accounts {
							require.Greater(t, a.ID, scan.AfterID)
							require.Equal(t, index, a.ID%3)
							require.False(t, seen[a.ID], "account scanned twice")
							seen[a.ID] = true
							scan.AfterID = a.ID
						}
						if len(accounts) < int(scan.Limit) {
							break
						}
						// Removing an earlier account from a status-filtered scan
						// must not shift the cursor past an unvisited account.
						require.NoError(t, repo.ChangeSubscriptionAccountStatus(ctx, scan.AfterID, biz.ChannelStatusDisabled))
						scan.Status = biz.ChannelStatusEnabled
					}
				}
				require.Len(t, seen, len(expected))
				for _, id := range expected {
					require.True(t, seen[id])
				}
			})
		})
	}
}

func TestAccountRecoveryRollsBackAbilityFailure(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	a := &biz.SubscriptionAccount{Name: "rollback", Platform: "codex", Status: 1, Group: "default", Models: []string{"gpt-5"}}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, a))
	require.NoError(t, repo.SetTempUnschedulable(ctx, a.ID, time.Unix(500, 0), "upstream 429"))
	observed, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
	require.NoError(t, err)
	require.NoError(t, repo.db.Callback().Update().Before("gorm:update").Register("test:recovery:ability", func(tx *gorm.DB) {
		if tx.Statement.Table == "subscription_account_abilities" {
			tx.AddError(fmt.Errorf("injected ability failure"))
		}
	}))
	cleared, err := repo.ClearRecoveryMarkers(ctx, observed.RecoveryState(), time.Unix(1000, 0))
	require.ErrorContains(t, err, "injected ability failure")
	require.False(t, cleared)
	require.NoError(t, repo.db.Callback().Update().Remove("test:recovery:ability"))
	got, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, observed.RecoveryState(), got.RecoveryState())
	cleared, err = repo.ClearRecoveryMarkers(ctx, observed.RecoveryState(), time.Unix(1000, 0))
	require.NoError(t, err)
	require.True(t, cleared)
}
