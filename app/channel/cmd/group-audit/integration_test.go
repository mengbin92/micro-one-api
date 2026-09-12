package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/app/channel/internal/data"
	"micro-one-api/pkg/jsonx"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

// Opt-in real-engine tests create and clean up uniquely named schemas. Never
// point these variables at production; the account needs CREATE/DROP on the
// disposable test database server. Default unit runs use SQLite only.
func TestAuditRealDatabase(t *testing.T) {
	for _, driver := range []string{"mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			dsn := os.Getenv("GROUP_AUDIT_TEST_" + strings.ToUpper(driver) + "_DSN")
			if dsn == "" {
				t.Skip("disposable database DSN not supplied")
			}
			sqlDriver := driver
			if driver == "postgres" {
				sqlDriver = "pgx"
			}
			admin, err := sql.Open(sqlDriver, dsn)
			require.NoError(t, err)
			defer admin.Close()
			schema := fmt.Sprintf("group_audit_test_%d", time.Now().UnixNano())
			if driver == "mysql" {
				_, err = admin.Exec("CREATE DATABASE " + schema + " CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci")
				require.NoError(t, err)
				defer admin.Exec("DROP DATABASE " + schema)
				cfg, parseErr := mysqldriver.ParseDSN(dsn)
				require.NoError(t, parseErr)
				cfg.DBName = schema
				dsn = cfg.FormatDSN()
			} else {
				_, err = admin.Exec("CREATE SCHEMA " + schema)
				require.NoError(t, err)
				defer admin.Exec("DROP SCHEMA " + schema + " CASCADE")
				parsed, parseErr := url.Parse(dsn)
				require.NoError(t, parseErr)
				require.Contains(t, []string{"postgres", "postgresql"}, parsed.Scheme, "integration test DSN must use URL format")
				query := parsed.Query()
				query.Set("search_path", schema)
				parsed.RawQuery = query.Encode()
				dsn = parsed.String()
			}
			db, err := sql.Open(sqlDriver, dsn)
			require.NoError(t, err)
			defer db.Close()
			for _, statement := range auditStatements() {
				if driver == "postgres" {
					statement = strings.ReplaceAll(statement, "`", `"`)
				}
				_, err := db.Exec(statement)
				require.NoError(t, err)
			}
			for _, statement := range []string{
				"INSERT INTO users VALUES (3, 'a_b'), (4, 'a%b'), (5, 'spaced')",
				"INSERT INTO channels VALUES (2, 1, 'axb,other', '', false, 0), (3, 1, 'default, spaced ', '', false, 0)",
			} {
				_, err := db.Exec(statement)
				require.NoError(t, err)
			}
			identitySchema, billingSchema := schema+"_identity", schema+"_billing"
			for owner, tables := range map[string][]string{
				identitySchema: {"users", "tokens"},
				billingSchema:  {"system_options", "subscription_groups", "subscription_plans", "user_subscriptions", "payment_orders"},
			} {
				if driver == "mysql" {
					_, err = db.Exec("CREATE DATABASE " + owner)
					require.NoError(t, err)
					defer admin.Exec("DROP DATABASE " + owner)
					for _, table := range tables {
						_, err = db.Exec("RENAME TABLE " + schema + "." + table + " TO " + owner + "." + table)
						require.NoError(t, err)
					}
				} else {
					_, err = db.Exec("CREATE SCHEMA " + owner)
					require.NoError(t, err)
					defer admin.Exec("DROP SCHEMA " + owner + " CASCADE")
					for _, table := range tables {
						_, err = db.Exec("ALTER TABLE " + schema + "." + table + " SET SCHEMA " + owner)
						require.NoError(t, err)
					}
				}
			}
			t.Setenv("GROUP_AUDIT_DSN", dsn)
			t.Setenv("AUDIT_TEST_BASE", `{"default":1,"vip":1,"svip":1}`)
			args := []string{"--driver=" + driver, "--model=legacy-chat", "--identity-schema=" + identitySchema, "--options-schema=" + billingSchema, "--billing-schema=" + billingSchema, "--base-ratios-env=AUDIT_TEST_BASE"}
			var output, again, diagnostics bytes.Buffer
			require.Equal(t, 1, run(args, &output, &diagnostics), diagnostics.String())
			require.Equal(t, 1, run(args, &again, &diagnostics), diagnostics.String())
			require.Equal(t, output.String(), again.String())
			var report biz.GroupAuditReport
			require.NoError(t, jsonx.Unmarshal(output.Bytes(), &report))
			require.Len(t, report.Orders, 1)
			require.Equal(t, "present", report.Orders[0].SnapshotState)
			require.NotContains(t, output.String(), "SECRET_SENTINEL")
			// MySQL's historical case-insensitive equality grants VIP the vip
			// mapping. The report must expose, not silently repair, this leak.
			foundCollationGrant := false
			for _, finding := range report.Issues {
				if finding.Code == "grant_without_exact_group_reference" && finding.Reference.Group == "VIP" {
					foundCollationGrant = true
				}
			}
			require.Equal(t, driver == "mysql", foundCollationGrant)
			for _, group := range []string{"a_b", "a%b"} {
				found := false
				for _, finding := range report.Issues {
					if finding.Code == "grant_without_exact_group_reference" && finding.Reference.Group == group {
						found = true
					}
				}
				require.True(t, found, "legacy LIKE must expose wildcard side effects for %q", group)
			}
			ctx := context.Background()
			tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
			require.NoError(t, err)
			defer tx.Rollback()
			_, _, inventoryRepo, err := data.NewGroupAuditRepositories(tx, driver, data.GroupAuditSchemas{Identity: identitySchema, Options: billingSchema, Billing: billingSchema})
			require.NoError(t, err)
			before, err := inventoryRepo.LoadGroupInventory(ctx)
			require.NoError(t, err)
			_, err = db.Exec("UPDATE " + billingSchema + ".subscription_plans SET for_sale = false WHERE id = 101")
			require.NoError(t, err)
			after, err := inventoryRepo.LoadGroupInventory(ctx)
			require.NoError(t, err)
			require.Equal(t, before, after, "cross-owner reads must share the same repeatable snapshot")
			_, err = tx.Exec("DELETE FROM " + identitySchema + ".tokens")
			require.Error(t, err, "audit transaction must reject writes")
		})
	}
}
