package main

import (
	"bytes"
	"context"
	"database/sql"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/app/channel/internal/data"
	"micro-one-api/pkg/jsonx"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackfillCommandRehearsalApplyAndReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channel.db")
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	defer db.Close()
	for _, file := range []string{"../../internal/data/testdata/routing_group_legacy.sql", "../../../../migrations/sqlite/092_create_routing_groups.sql", "../../../../migrations/sqlite/095_create_routing_change_outbox.sql"} {
		raw, err := os.ReadFile(file)
		require.NoError(t, err)
		for _, s := range strings.Split(string(raw), ";") {
			if strings.TrimSpace(s) != "" {
				_, err = db.Exec(strings.ReplaceAll(s, "id BIGINT PRIMARY KEY", "id INTEGER PRIMARY KEY"))
				require.NoError(t, err)
			}
		}
	}
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	require.NoError(t, err)
	channels, routings, inventory, err := data.NewGroupAuditRepositories(tx, "sqlite3", data.GroupAuditSchemas{})
	require.NoError(t, err)
	uc := biz.NewChannelUsecase(channels, nil)
	uc.SetModelRoutingRepo(routings)
	report, err := biz.NewGroupAuditUsecase(inventory, uc).Run(ctx, []string{"legacy-chat", "unknown"}, map[string]float64{"default": 1, "vip": 1}, 1000)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	raw, err := jsonx.Marshal(report)
	require.NoError(t, err)
	baseline := filepath.Join(t.TempDir(), "report.json")
	require.NoError(t, os.WriteFile(baseline, raw, 0600))
	t.Setenv("GROUP_BACKFILL_DSN", path)
	args := []string{"--driver=sqlite3", "--report=" + baseline}
	for _, apply := range []bool{false, true, true} {
		flags := append([]string{}, args...)
		if apply {
			flags = append(flags, "--apply")
		}
		var out, diagnostics bytes.Buffer
		require.Equal(t, 0, run(flags, &out, &diagnostics), diagnostics.String())
		var result struct {
			Applied bool `json:"applied"`
		}
		require.NoError(t, jsonx.Unmarshal(out.Bytes(), &result))
		require.Equal(t, apply, result.Applied)
		var count int
		require.NoError(t, db.QueryRow("SELECT count(*) FROM routing_group_backfills").Scan(&count))
		if apply {
			require.Equal(t, 1, count)
		} else {
			require.Zero(t, count)
		}
	}
	require.NoError(t, os.WriteFile(baseline, append(raw, []byte(" {}")...), 0600))
	var out, diagnostics bytes.Buffer
	require.Equal(t, 2, run(args, &out, &diagnostics))
	require.Empty(t, out.String())
	require.Contains(t, diagnostics.String(), "invalid baseline JSON")
}
func TestBackfillCommandRejectsArgumentsWithoutLeakingDSN(t *testing.T) {
	t.Setenv("GROUP_BACKFILL_DSN", "SECRET_SENTINEL")
	for _, args := range [][]string{nil, {"--driver=unsupported", "--report=none"}, {"--report=none", "--timeout=-1s"}} {
		var out, diagnostics bytes.Buffer
		require.Equal(t, 2, run(args, &out, &diagnostics))
		require.NotContains(t, diagnostics.String(), "SECRET_SENTINEL")
	}
}
