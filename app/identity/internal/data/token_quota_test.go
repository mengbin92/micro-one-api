package data

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/identity/internal/biz"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type tokenEditQuotaRepo struct {
	*Repository
	amount int64
}

func (r *tokenEditQuotaRepo) FindTokenByID(ctx context.Context, userID, tokenID int64) (*biz.Token, error) {
	token, err := r.Repository.FindTokenByID(ctx, userID, tokenID)
	if err == nil && r.amount > 0 {
		amount := r.amount
		r.amount = 0
		_, err = r.Repository.ConsumeTokenQuota(ctx, userID, tokenID, amount)
	}
	return token, err
}

func TestTokenEditPreservesConcurrentUsage(t *testing.T) {
	for _, storage := range []string{"memory", "sqlite"} {
		for _, tt := range []struct {
			name                     string
			amount, quota, remaining int64
			status                   int32
		}{
			{"rename", 20, -1, 80, biz.TokenStatusEnabled},
			{"exhausted", 100, -1, 0, biz.TokenStatusExhausted},
			{"explicit quota", 20, 250, 250, biz.TokenStatusEnabled},
		} {
			t.Run(storage+"/"+tt.name, func(t *testing.T) {
				repo := NewMemoryRepositoryForTest()
				if storage == "sqlite" {
					repo = newTokenHashTestRepo(t)
				}
				ctx := context.Background()
				token := &biz.Token{UserID: 7, Name: "before", Key: "quota-edit-key", KeyHash: biz.HashTokenKey("quota-edit-key"), Status: biz.TokenStatusEnabled, RemainQuota: 100, AccessedAt: 10}
				require.NoError(t, repo.CreateToken(ctx, token))
				uc := biz.NewIdentityUsecase(&tokenEditQuotaRepo{Repository: repo, amount: tt.amount}, nil)
				_, err := uc.UpdateAccessTokenWithOptions(ctx, 7, token.ID, biz.UpdateAccessTokenOptions{Name: "after", RemainQuota: tt.quota})
				require.NoError(t, err)
				got, err := repo.FindTokenByID(ctx, 7, token.ID)
				require.NoError(t, err)
				require.Equal(t, "after", got.Name)
				require.EqualValues(t, tt.amount, got.UsedQuota, "editing token settings must not roll back accumulated usage")
				require.EqualValues(t, tt.remaining, got.RemainQuota, "omitted quota must preserve concurrent consumption")
				require.Equal(t, tt.status, got.Status, "omitted status must preserve concurrent exhaustion")
			})
		}
	}
}

func TestConsumeTokenQuotaDBAtomicAndUserScoped(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:token-quota?mode=memory&cache=shared&_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.Exec(`CREATE TABLE tokens (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER, name TEXT, key TEXT, key_hash TEXT, status INTEGER, created_time INTEGER, accessed_time INTEGER, expired_time INTEGER, remain_quota INTEGER, unlimited_quota INTEGER, used_quota INTEGER, models TEXT, subnet TEXT, created_at INTEGER, routing_mode TEXT, routing_group_id INTEGER, routing_revision INTEGER)`).Error; err != nil {
		t.Fatalf("create tokens: %v", err)
	}
	model := tokenModel{UserID: 7, Status: biz.TokenStatusEnabled, RemainQuota: 100}
	if err := db.Create(&model).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	repo := &Repository{Data: &Data{db: db}}

	if _, err := repo.ConsumeTokenQuota(context.Background(), 8, model.ID, 10); err == nil {
		t.Fatal("different user was allowed to consume token quota")
	}

	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 10 {
		wg.Go(func() {
			_, err := repo.ConsumeTokenQuota(context.Background(), 7, model.ID, 10)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("consume quota: %v", err)
		}
	}
	var got tokenModel
	if err := db.First(&got, model.ID).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if got.RemainQuota != 0 || got.UsedQuota != 100 || got.Status != biz.TokenStatusExhausted {
		t.Fatalf("token state = remain:%d used:%d status:%d", got.RemainQuota, got.UsedQuota, got.Status)
	}
}

func TestConsumeTokenQuotaWithDedupeChargesReservationOnce(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:token-quota-dedupe?mode=memory&cache=shared&_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec(`CREATE TABLE tokens (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER, name TEXT, key TEXT, key_hash TEXT, status INTEGER, created_time INTEGER, accessed_time INTEGER, expired_time INTEGER, remain_quota INTEGER, unlimited_quota INTEGER, used_quota INTEGER, models TEXT, subnet TEXT, created_at INTEGER, routing_mode TEXT, routing_group_id INTEGER, routing_revision INTEGER)`).Error; err != nil {
		t.Fatalf("create tokens: %v", err)
	}
	if err := db.Exec(`CREATE TABLE identity_token_quota_dedupe (id INTEGER PRIMARY KEY AUTOINCREMENT, reservation_id TEXT NOT NULL, user_id INTEGER NOT NULL, token_id INTEGER NOT NULL, amount INTEGER NOT NULL, remaining INTEGER NOT NULL, created_at INTEGER NOT NULL, UNIQUE (reservation_id, user_id, token_id))`).Error; err != nil {
		t.Fatalf("create dedupe: %v", err)
	}
	model := tokenModel{UserID: 7, Status: biz.TokenStatusEnabled, RemainQuota: 100}
	if err := db.Create(&model).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	repo := &Repository{Data: &Data{db: db}}
	remaining, applied, err := repo.ConsumeTokenQuotaWithDedupe(context.Background(), 7, model.ID, 10, "reservation-1")
	if err != nil || !applied || remaining != 90 {
		t.Fatalf("first consume = remaining:%d applied:%v err:%v", remaining, applied, err)
	}
	remaining, applied, err = repo.ConsumeTokenQuotaWithDedupe(context.Background(), 7, model.ID, 10, "reservation-1")
	if err != nil || applied || remaining != 90 {
		t.Fatalf("replay consume = remaining:%d applied:%v err:%v", remaining, applied, err)
	}
}
