package data

import (
	"context"
	"encoding/base64"
	"fmt"

	"gorm.io/gorm"
	appcrypto "micro-one-api/platform/security/crypto"
)

// CredentialMigrationRecord identifies a credential requiring operator
// attention. It intentionally contains no credential value.
type CredentialMigrationRecord struct {
	Table string `json:"table"`
	Field string `json:"field"`
	ID    int64  `json:"id"`
}

// CredentialMigrationSummary is the classification for one credential
// family. Empty values are not counted as credentials.
type CredentialMigrationSummary struct {
	Scanned            int `json:"scanned"`
	Encrypted          int `json:"encrypted"`
	SuspectedPlaintext int `json:"suspected_plaintext"`
	Indeterminate      int `json:"indeterminate"`
	Rewritten          int `json:"rewritten"`
}

// CredentialMigrationReport is safe to print in operator logs: it contains
// counts and row identifiers only, never credential contents.
type CredentialMigrationReport struct {
	DryRun               bool                        `json:"dry_run"`
	Channels             CredentialMigrationSummary  `json:"channels"`
	SubscriptionAccounts CredentialMigrationSummary  `json:"subscription_accounts"`
	SuspectedPlaintext   []CredentialMigrationRecord `json:"suspected_plaintext_records,omitempty"`
	Indeterminate        []CredentialMigrationRecord `json:"indeterminate_records,omitempty"`
}

type channelCredentialRow struct {
	ID  int64  `gorm:"column:id"`
	Key string `gorm:"column:key"`
}

func (channelCredentialRow) TableName() string { return "channels" }

type subscriptionCredentialRow struct {
	ID           int64   `gorm:"column:id"`
	AccessToken  *string `gorm:"column:access_token"`
	RefreshToken *string `gorm:"column:refresh_token"`
}

func (subscriptionCredentialRow) TableName() string { return "subscription_accounts" }

type credentialMigrationValue struct {
	record CredentialMigrationRecord
	value  string
}

// MigrateCredentials audits persisted channel and subscription credentials.
// Dry-run mode never writes. Apply mode encrypts only values classified as
// plaintext and updates all selected rows in one transaction, making reruns
// idempotent after a successful migration.
func (r *Repository) MigrateCredentials(ctx context.Context, dryRun bool) (CredentialMigrationReport, error) {
	report := CredentialMigrationReport{
		DryRun:             dryRun,
		SuspectedPlaintext: make([]CredentialMigrationRecord, 0),
		Indeterminate:      make([]CredentialMigrationRecord, 0),
	}
	if r.db == nil {
		return report, fmt.Errorf("credential migration requires a persistent channel repository")
	}

	var pending []credentialMigrationValue
	var channels []channelCredentialRow
	if err := r.db.WithContext(ctx).Select("id", "key").Find(&channels).Error; err != nil {
		return report, fmt.Errorf("scan channels credentials: %w", err)
	}
	for _, row := range channels {
		if row.Key == "" {
			continue
		}
		pending = classifyCredential(pending, &report, r.encKey, "channels", "key", row.ID, row.Key)
	}

	var accounts []subscriptionCredentialRow
	if err := r.db.WithContext(ctx).Select("id", "access_token", "refresh_token").Find(&accounts).Error; err != nil {
		return report, fmt.Errorf("scan subscription credentials: %w", err)
	}
	for _, row := range accounts {
		if row.AccessToken != nil && *row.AccessToken != "" {
			id := row.ID
			pending = classifyCredential(pending, &report, r.encKey, "subscription_accounts", "access_token", id, *row.AccessToken)
		}
		if row.RefreshToken != nil && *row.RefreshToken != "" {
			id := row.ID
			pending = classifyCredential(pending, &report, r.encKey, "subscription_accounts", "refresh_token", id, *row.RefreshToken)
		}
	}

	if dryRun || len(pending) == 0 {
		return report, nil
	}
	if err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, item := range pending {
			value, err := appcrypto.Encrypt(item.value, r.encKey)
			if err != nil {
				return fmt.Errorf("encrypt %s/%s/%d: %w", item.record.Table, item.record.Field, item.record.ID, err)
			}
			var result *gorm.DB
			switch item.record.Field {
			case "key":
				result = tx.Model(&channelCredentialRow{}).Where("id = ?", item.record.ID).Update("key", value)
			default:
				result = tx.Model(&subscriptionCredentialRow{}).Where("id = ?", item.record.ID).Update(item.record.Field, value)
			}
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("%s/%s/%d changed during migration", item.record.Table, item.record.Field, item.record.ID)
			}
		}
		return nil
	}); err != nil {
		return report, fmt.Errorf("migrate credentials: %w", err)
	}
	report.Channels.Rewritten += countRecords(pending, "channels")
	report.SubscriptionAccounts.Rewritten += countRecords(pending, "subscription_accounts")
	return report, nil
}

func classifyCredential(
	pending []credentialMigrationValue,
	report *CredentialMigrationReport,
	key []byte,
	table, field string,
	id int64,
	value string,
) []credentialMigrationValue {
	record := CredentialMigrationRecord{Table: table, Field: field, ID: id}
	summary := &report.Channels
	if table == "subscription_accounts" {
		summary = &report.SubscriptionAccounts
	}
	summary.Scanned++
	if _, err := appcrypto.Decrypt(value, key); err == nil {
		summary.Encrypted++
		return pending
	}
	if looksLikeCiphertext(value) {
		summary.Indeterminate++
		report.Indeterminate = append(report.Indeterminate, record)
		return pending
	}
	summary.SuspectedPlaintext++
	report.SuspectedPlaintext = append(report.SuspectedPlaintext, record)
	return append(pending, credentialMigrationValue{record: record, value: value})
}

func looksLikeCiphertext(value string) bool {
	decoded, err := base64.StdEncoding.DecodeString(value)
	return err == nil && len(decoded) >= 12+16
}

func countRecords(records []credentialMigrationValue, table string) int {
	count := 0
	for _, item := range records {
		if item.record.Table == table {
			count++
		}
	}
	return count
}
