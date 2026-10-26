package data

import (
	"strings"

	"gorm.io/gorm"
	"micro-one-api/app/identity/internal/biz"
)

func emailConflicts(existing, proposed string) bool {
	return strings.TrimSpace(proposed) != "" && strings.EqualFold(strings.TrimSpace(existing), strings.TrimSpace(proposed))
}

// Call under the IAM policy write lock. Historical duplicate addresses remain
// readable for account administration, but cannot acquire new bindings or be
// selected arbitrarily for password recovery. No destructive schema rewrite.
func checkEmailBinding(db *gorm.DB, email string, excludeID int64) error {
	if strings.TrimSpace(email) == "" {
		return nil
	}
	var n int64
	if err := db.Table("users").Where("id <> ? AND LOWER(TRIM(email)) = ?", excludeID, strings.ToLower(strings.TrimSpace(email))).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return biz.ErrUserExists
	}
	return nil
}
