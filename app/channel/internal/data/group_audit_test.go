package data

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestGroupAuditRejectsUnsafeSchemaOverridesBeforeOpeningDatabase(t *testing.T) {
	for _, schema := range []string{"a.b", "a`", "a; SELECT 1", "a b", "a\n", "-identity"} {
		_, _, _, err := NewGroupAuditRepositories(nil, "mysql", GroupAuditSchemas{Identity: schema})
		require.Error(t, err)
		_, _, _, err = NewGroupAuditRepositories(nil, "mysql", GroupAuditSchemas{Options: schema})
		require.Error(t, err)
	}
	_, _, _, err := NewGroupAuditRepositories(nil, "sqlite3", GroupAuditSchemas{Identity: "identity"})
	require.Error(t, err, "SQLite audit must not silently use a different attached schema")
}

func TestGroupAuditMySQLQuotesCrossSchemaReadTables(t *testing.T) {
	// DryRun renders the production dialect without connecting to a database.
	db, err := gorm.Open(mysql.New(mysql.Config{SkipInitializeWithVersion: true}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	require.NoError(t, err)
	var users []struct {
		ID    int64
		Group string
	}
	query := db.Table("oneapi_identity.users").Select("id", "group").Find(&users)
	require.NoError(t, query.Error)
	require.Equal(t, "SELECT `id`,`group` FROM `oneapi_identity`.`users`", query.Statement.SQL.String())
	var options []struct{ OptionValue string }
	query = db.Table("oneapi_admin.system_options").Select("option_value").Where("option_key = ?", "GroupRatio").Find(&options)
	require.NoError(t, query.Error)
	require.Equal(t, "SELECT `option_value` FROM `oneapi_admin`.`system_options` WHERE option_key = ?", query.Statement.SQL.String())
	require.Equal(t, []any{"GroupRatio"}, query.Statement.Vars)
}
