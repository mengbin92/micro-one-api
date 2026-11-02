package data

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
)

func backupDockerFixture(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	trace := filepath.Join(dir, "docker.calls")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$BACKUP_DOCKER_TRACE\"\n"+script), 0700))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BACKUP_DOCKER_TRACE", trace)
	return trace
}

func TestSnapshotRejectsUnreviewedRunningServicesBeforeStoreOperations(t *testing.T) {
	for _, tc := range []struct {
		name, rendered, active, wantError string
	}{
		{"missing_account_ops", `{"services":{"mysql":{},"redis":{}}}`, "mysql\nredis\nchannel-account-ops-worker", "running Compose service missing from backup topology"},
		{"missing_unknown_service", `{"services":{"mysql":{},"redis":{}}}`, "mysql\nredis\nunknown-writer", "running Compose service missing from backup topology"},
		{"configured_unknown_writer", `{"services":{"mysql":{},"redis":{},"unknown-writer":{"environment":{"SQL_DSN":"fixture"}}}}`, "mysql\nredis\nunknown-writer", "unreviewed Compose store writer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trace := backupDockerFixture(t, `case "$*" in
 *" config --format json") printf '%s' "$BACKUP_DOCKER_CONFIG" ;;
 *" ps --status running --services") printf '%s\n' "$BACKUP_DOCKER_SERVICES" ;;
 *) exit 97 ;;
esac
`)
			t.Setenv("BACKUP_DOCKER_CONFIG", tc.rendered)
			t.Setenv("BACKUP_DOCKER_SERVICES", tc.active)
			dir := t.TempDir()
			require.NoError(t, os.Chmod(dir, 0700))
			runner := &Runner{Config: Config{ComposeFile: "/fixture/compose.yml", Project: "fixture", StateDir: dir}}
			_, _, err := runner.Snapshot(context.Background(), "topology-fixture", dir)
			require.ErrorContains(t, err, tc.wantError)
			calls, err := os.ReadFile(trace)
			require.NoError(t, err)
			require.NotContains(t, string(calls), " exec ", "topology rejection must precede store operations")
			require.NotContains(t, string(calls), " stop ")
			_, err = os.Stat(runner.journalPath())
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestSnapshotIncludesProfiledAccountOpsWriterInInventory(t *testing.T) {
	trace := backupDockerFixture(t, `case "$*" in
 *"--profile * config --format json") printf '%s' '{"services":{"mysql":{},"redis":{},"channel-account-ops-worker":{"environment":{"SQL_DSN":"fixture"}}}}' ;;
 *" config --format json") printf '%s' '{"services":{"mysql":{},"redis":{}}}' ;;
 *" ps --status running --services") printf 'mysql\nredis\nchannel-account-ops-worker\n' ;;
 *" CONFIG GET dir") printf 'dir\n/data\n' ;;
 *" CONFIG GET dbfilename") printf 'dbfilename\ndump.rdb\n' ;;
 *" INFO cluster") printf 'cluster_enabled:0\n' ;;
 *" INFO replication") printf 'role:master\n' ;;
 *" ps -q channel-account-ops-worker") printf 'fixture-account-ops\n' ;;
 *) exit 97 ;;
esac
`)
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0700))
	runner := &Runner{Config: Config{ComposeFile: "/fixture/compose.yml", Project: "fixture", StateDir: dir}}
	_, _, err := runner.Snapshot(context.Background(), "profiled-writer-fixture", dir)
	// Stop the fixture at image inspection, before opening any database or
	// creating a resume journal. Images and stop commands use the same writer set.
	require.ErrorContains(t, err, "docker backup operation failed")
	calls, err := os.ReadFile(trace)
	require.NoError(t, err)
	require.Contains(t, string(calls), "--profile * config --format json")
	require.Contains(t, string(calls), "ps -q channel-account-ops-worker")
	require.Contains(t, string(calls), "inspect --format {{.Image}} fixture-account-ops")
	require.NotContains(t, string(calls), " stop ")
}

func TestRecoverResumesAccountOpsWriter(t *testing.T) {
	trace := backupDockerFixture(t, "exit 0\n")
	dir := t.TempDir()
	runner := &Runner{Config: Config{ComposeFile: "/fixture/compose.yml", Project: "fixture", StateDir: dir}}
	raw, err := jsonx.Marshal(journal{Project: "fixture", ComposeFile: "/fixture/compose.yml", Services: []string{"channel-account-ops-worker", "redis"}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(runner.journalPath(), raw, 0600))
	require.NoError(t, runner.Recover(context.Background()))
	calls, err := os.ReadFile(trace)
	require.NoError(t, err)
	require.Contains(t, string(calls), "start --wait --wait-timeout 120 channel-account-ops-worker redis")
	_, err = os.Stat(runner.journalPath())
	require.ErrorIs(t, err, os.ErrNotExist)
}
