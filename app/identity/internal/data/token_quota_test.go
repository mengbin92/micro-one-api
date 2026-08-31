package data

import (
	"context"
	"sync"
	"testing"

	"micro-one-api/app/identity/internal/biz"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

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
	if err := db.AutoMigrate(&tokenModel{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	model := tokenModel{UserID: 7, Status: biz.TokenStatusEnabled, RemainQuota: 100}
	if err := db.Create(&model).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	repo := &Repository{db: db}

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
