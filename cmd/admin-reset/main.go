// admin-reset is a small CLI that resets (or creates) an admin user's
// password directly in the database. Useful when the initial random
// password printed by identity-service was lost, or when an admin locked
// themselves out.
//
// Usage:
//
//	admin-reset -username admin -password 'new-secret'
//	admin-reset -username admin -generated-password-file ./admin-password.txt
//
// DSN is read from ADMIN_RESET_DSN, falling back to IDENTITY_SQL_DSN, then
// SQL_DSN. Password is hashed with bcrypt before update.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"micro-one-api/internal/pkg/xdb"
)

const defaultGroup = "default"

const defaultDisplayName = "Administrator"

const userStatusEnabled = 1

func main() {
	var (
		username = flag.String("username", "admin", "username to reset (created if missing)")
		password = flag.String("password", "", "new password; if empty, a random 16-char hex password is generated")
		passFile = flag.String("generated-password-file", "", "file to write the generated password when -password is empty; created with mode 0600")
		email    = flag.String("email", "", "email to set when creating a new user (ignored on reset unless -role is also set)")
		quota    = flag.Int64("quota", 1_000_000, "quota to set when creating a new user (ignored on reset)")
		role     = flag.Int("role", -1, "role to set (0=guest, 1=user, 10=admin, 100=root); negative means leave unchanged on reset, default to 100 on create")
	)
	flag.Parse()

	uname := strings.TrimSpace(*username)
	if uname == "" {
		fmt.Fprintln(os.Stderr, "error: -username is required")
		os.Exit(2)
	}

	dsn := pickDSN()
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "error: ADMIN_RESET_DSN, IDENTITY_SQL_DSN, or SQL_DSN must be set")
		os.Exit(2)
	}

	plain := *password
	outputFile := strings.TrimSpace(*passFile)
	generated := false
	if plain == "" {
		if outputFile == "" {
			fmt.Fprintln(os.Stderr, "error: -password or -generated-password-file is required")
			os.Exit(2)
		}
		buf := make([]byte, 8)
		if _, err := rand.Read(buf); err != nil {
			fmt.Fprintf(os.Stderr, "error: generate password: %v\n", err)
			os.Exit(1)
		}
		plain = hex.EncodeToString(buf)
		generated = true
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: hash password: %v\n", err)
		os.Exit(1)
	}

	db, err := xdb.OpenMySQL(dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open DB: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	created, err := upsertPassword(ctx, db, uname, string(hash), *email, *quota, *role)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	verb := "updated password for"
	if created {
		verb = "created admin"
	}
	if generated {
		if err := writeGeneratedPasswordFile(outputFile, plain); err != nil {
			fmt.Fprintf(os.Stderr, "error: write generated password file: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%s %q\n", verb, uname)
		fmt.Println("generated password written to private file")
		return
	}
	fmt.Printf("%s %q (password from -password flag)\n", verb, uname)
}

func writeGeneratedPasswordFile(path, password string) error {
	cleanPath, err := validateGeneratedPasswordFilePath(path)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(cleanPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304,G703 -- CLI caller selects this absolute one-time password output file; path is cleaned, must not be a directory, and O_EXCL/0600 prevent overwrite and broad reads.
	if err != nil {
		return err
	}
	if _, err := file.WriteString(password + "\n"); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func validateGeneratedPasswordFilePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("password file path is required")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("password file path must be absolute")
	}
	cleanPath := filepath.Clean(path)
	if info, err := os.Stat(cleanPath); err == nil && info.IsDir() {
		return "", fmt.Errorf("password file path must not be a directory")
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return cleanPath, nil
}

func pickDSN() string {
	if v := os.Getenv("ADMIN_RESET_DSN"); v != "" {
		return v
	}
	if v := os.Getenv("IDENTITY_SQL_DSN"); v != "" {
		return v
	}
	return os.Getenv("SQL_DSN")
}

// upsertPassword updates an existing user's password_hash or creates a new
// enabled admin row if none exists. When role >= 0 the role column is also
// updated/set. Returns created=true when a new row was inserted.
func upsertPassword(ctx context.Context, db *gorm.DB, username, hash, email string, quota int64, role int) (bool, error) {
	var count int64
	if err := db.WithContext(ctx).Table("users").Where("username = ?", username).Count(&count).Error; err != nil {
		return false, fmt.Errorf("query user: %w", err)
	}
	if count > 0 {
		updates := map[string]any{"password_hash": hash}
		if role >= 0 {
			updates["role"] = role
		}
		res := db.WithContext(ctx).Table("users").Where("username = ?", username).Updates(updates)
		if res.Error != nil {
			return false, fmt.Errorf("update password: %w", res.Error)
		}
		return false, nil
	}

	if email == "" {
		email = username + "@example.com"
	}
	newRole := role
	if newRole < 0 {
		newRole = 100 // root, matches biz.RoleRootUser
	}
	row := map[string]any{
		"username":      username,
		"display_name":  defaultDisplayName,
		"email":         email,
		"group":         defaultGroup,
		"status":        userStatusEnabled,
		"role":          newRole,
		"password_hash": hash,
		"quota":         quota,
	}
	if err := db.WithContext(ctx).Table("users").Create(row).Error; err != nil {
		return false, fmt.Errorf("create user: %w", err)
	}
	return true, nil
}
