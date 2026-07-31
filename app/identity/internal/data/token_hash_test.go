package data

import (
	"context"
	"testing"

	"micro-one-api/app/identity/internal/biz"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newTokenHashTestRepo spins up an in-memory SQLite DB with the tokenModel
// schema (including the L6 key_hash column) for testing the hashing path.
func newTokenHashTestRepo(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&tokenModel{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return &Repository{db: db}
}

// TestTokenHash_KeyNeverStoredAsPlaintext proves the L6 security property:
// after CreateToken, the DB row's `key` column holds only a short display
// prefix, never the full plaintext, while key_hash holds the HMAC. A raw DB
// dump therefore cannot authenticate as the user.
func TestTokenHash_KeyNeverStoredAsPlaintext(t *testing.T) {
	repo := newTokenHashTestRepo(t)
	plaintext := "sk-secret-plaintext-key-1234567890"
	token := &biz.Token{
		UserID:         1,
		Name:           "work",
		Key:            plaintext,
		KeyHash:        biz.HashTokenKey(plaintext),
		Status:         biz.TokenStatusEnabled,
		UnlimitedQuota: true,
	}
	if err := repo.CreateToken(context.Background(), token); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	// Inspect the raw stored row (as the DB driver sees it, not via toBiz).
	var row tokenModel
	if err := repo.db.First(&row, token.ID).Error; err != nil {
		t.Fatalf("read row: %v", err)
	}
	if row.Key == plaintext {
		t.Fatalf("L6 VIOLATION: full plaintext key stored in `key` column: %q", row.Key)
	}
	if row.Key != biz.TokenDisplayPrefix(plaintext) {
		t.Fatalf("stored key = %q, want display prefix %q", row.Key, biz.TokenDisplayPrefix(plaintext))
	}
	if row.KeyHash == "" {
		t.Fatal("key_hash column is empty")
	}
	if row.KeyHash == plaintext {
		t.Fatal("L6 VIOLATION: key_hash equals plaintext")
	}
	if row.KeyHash != biz.HashTokenKey(plaintext) {
		t.Fatalf("key_hash = %q, want %q", row.KeyHash, biz.HashTokenKey(plaintext))
	}
}

// TestTokenHash_FindTokenByKeyUsesHash proves ValidateToken's hot path looks
// up by the hash, not the plaintext key: a token created with plaintext P is
// found by FindTokenByKey(P) because the incoming key is hashed before lookup.
func TestTokenHash_FindTokenByKeyUsesHash(t *testing.T) {
	repo := newTokenHashTestRepo(t)
	plaintext := "sk-findme-abcdef-1234567890"
	token := &biz.Token{
		UserID: 7, Name: "find", Key: plaintext, KeyHash: biz.HashTokenKey(plaintext),
		Status: biz.TokenStatusEnabled, UnlimitedQuota: true,
	}
	if err := repo.CreateToken(context.Background(), token); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	got, err := repo.FindTokenByKey(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("FindTokenByKey(plaintext): %v", err)
	}
	if got.ID != token.ID {
		t.Fatalf("returned token ID = %d, want %d", got.ID, token.ID)
	}

	// A wrong plaintext must not match even if it shares a prefix.
	if _, err := repo.FindTokenByKey(context.Background(), "sk-findme-wrong"); err != biz.ErrTokenNotFound {
		t.Fatalf("FindTokenByKey(wrong key) err = %v, want ErrTokenNotFound", err)
	}
}

// TestTokenHash_TwoKeysProduceDistinctHashes guards against a degenerate hash
// (constant/collapsing) that would let one key authenticate as another.
func TestTokenHash_TwoKeysProduceDistinctHashes(t *testing.T) {
	a, b := biz.HashTokenKey("key-one-1234567890"), biz.HashTokenKey("key-two-1234567890")
	if a == b {
		t.Fatalf("distinct keys hashed to the same value: %q", a)
	}
}

// TestTokenHash_ListTokensDropsKeySearch proves the L6 keyword filter no longer
// matches the (now-hashed) key column — a search by a known plaintext must not
// surface the token; only name matches.
func TestTokenHash_ListTokensDropsKeySearch(t *testing.T) {
	repo := newTokenHashTestRepo(t)
	plaintext := "sk-searchable-key-1234567890"
	token := &biz.Token{
		UserID: 1, Name: "my-token", Key: plaintext, KeyHash: biz.HashTokenKey(plaintext),
		Status: biz.TokenStatusEnabled, UnlimitedQuota: true,
	}
	if err := repo.CreateToken(context.Background(), token); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	// Search by the plaintext key must NOT find the token (key is hashed now).
	got, total, err := repo.ListTokens(context.Background(), 1, 1, 10, plaintext)
	if err != nil {
		t.Fatalf("ListTokens(plaintext): %v", err)
	}
	if total != 0 || len(got) != 0 {
		t.Fatalf("L6 VIOLATION: keyword search matched the hashed key column (total=%d)", total)
	}

	// Search by name still works.
	got, total, err = repo.ListTokens(context.Background(), 1, 1, 10, "my-token")
	if err != nil {
		t.Fatalf("ListTokens(name): %v", err)
	}
	if total != 1 || len(got) != 1 {
		t.Fatalf("name search: total=%d, want 1", total)
	}
}

// TestTokenHash_BackfillHashesPlaintextRows proves the startup migration: rows
// that predate L6 (full plaintext in `key`, empty key_hash) get hashed in place
// and their key column truncated to a prefix. Idempotent on re-run.
func TestTokenHash_BackfillHashesPlaintextRows(t *testing.T) {
	repo := newTokenHashTestRepo(t)
	plaintext := "sk-legacy-plaintext-key-99"
	// Seed a pre-L6 row directly: full plaintext key, empty key_hash.
	row := tokenModel{
		UserID: 1, Name: "legacy", Key: plaintext, KeyHash: "",
		Status: biz.TokenStatusEnabled, UnlimitedQuota: true,
	}
	if err := repo.db.Create(&row).Error; err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	repo.BackfillTokenHashes(context.Background())

	var got tokenModel
	if err := repo.db.First(&got, row.ID).Error; err != nil {
		t.Fatalf("read after backfill: %v", err)
	}
	if got.KeyHash != biz.HashTokenKey(plaintext) {
		t.Fatalf("after backfill key_hash = %q, want %q", got.KeyHash, biz.HashTokenKey(plaintext))
	}
	if got.Key == plaintext {
		t.Fatalf("L6 VIOLATION: backfill left full plaintext in key column")
	}
	if got.Key != biz.TokenDisplayPrefix(plaintext) {
		t.Fatalf("after backfill key = %q, want prefix %q", got.Key, biz.TokenDisplayPrefix(plaintext))
	}

	// Idempotent: re-running finds zero pending rows and leaves the row alone.
	repo.BackfillTokenHashes(context.Background())
	var got2 tokenModel
	_ = repo.db.First(&got2, row.ID).Error
	if got2.KeyHash != got.KeyHash {
		t.Fatal("backfill not idempotent: hash changed on second run")
	}
}
