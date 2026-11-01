package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	billingv1 "micro-one-api/api/billing/v1"
	billingtest "micro-one-api/app/billing/testutil"
	channeltest "micro-one-api/app/channel/testutil"
	identitytest "micro-one-api/app/identity/testutil"
	d "micro-one-api/domain/backup/data"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/migrate"
	"micro-one-api/platform/database/xdb"
	appcrypto "micro-one-api/platform/security/crypto"
	"micro-one-api/platform/security/totp"
)

func backupSecret(t *testing.T) string {
	t.Helper()
	var raw [16]byte
	_, err := rand.Read(raw[:])
	require.NoError(t, err)
	return hex.EncodeToString(raw[:])
}

// Opt-in: creates and destroys only two unique private Compose projects. It
// never reads .env or any production credential, volume or container name.
func TestManagedBackupRealEmptyRestore(t *testing.T) {
	if os.Getenv("BACKUP_E2E") != "true" {
		t.Skip("set BACKUP_E2E=true for private MySQL/Redis/S3 empty-environment restore")
	}
	ctx := context.Background()
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	scratch := t.TempDir()
	source := "backup-src-" + backupSecret(t)[:8]
	target := "backup-dst-" + backupSecret(t)[:8]
	password := backupSecret(t)
	redisPassword := backupSecret(t)
	ak, sk := backupSecret(t), backupSecret(t)
	s3Image := os.Getenv("BACKUP_TEST_S3_IMAGE")
	if s3Image == "" {
		s3Image = "rustfs/rustfs@sha256:ed9210d6411d77ffceb70d94b964a3cb7d225983368bc25bebe01e995293dcc2"
	}
	composeFile := filepath.Join(scratch, "compose.json")
	compose := map[string]any{"services": map[string]any{
		"mysql":         map[string]any{"image": "mysql:8.4", "environment": map[string]string{"MYSQL_ROOT_PASSWORD": password, "MYSQL_DATABASE": "oneapi"}, "ports": []string{"127.0.0.1::3306"}, "volumes": []string{"mysql-data:/var/lib/mysql"}, "healthcheck": map[string]any{"test": []string{"CMD-SHELL", `MYSQL_PWD="$$MYSQL_ROOT_PASSWORD" mysqladmin ping -h 127.0.0.1 -uroot --silent`}, "interval": "2s", "timeout": "3s", "retries": 60}},
		"redis":         map[string]any{"image": "redis:8.4", "command": []string{"redis-server", "--requirepass", redisPassword}, "ports": []string{"127.0.0.1::6379"}, "volumes": []string{"redis-data:/data"}},
		"object-store":  map[string]any{"image": s3Image, "user": "0:0", "command": []string{"/data"}, "environment": map[string]string{"RUSTFS_ACCESS_KEY": ak, "RUSTFS_SECRET_KEY": sk, "RUSTFS_CONSOLE_ENABLE": "false", "RUSTFS_ADDRESS": ":9000", "RUSTFS_OBS_LOGGER_LEVEL": "error"}, "ports": []string{"127.0.0.1::9000"}, "volumes": []string{"s3-data:/data"}},
		"admin-api":     map[string]any{"image": "redis:8.4", "entrypoint": []string{"sh", "-c", "trap 'exit 0' TERM; while :; do sleep 1 & wait $!; done"}},
		"notify-worker": map[string]any{"image": "redis:8.4", "entrypoint": []string{"sh", "-c", "sleep 3600"}},
	}, "volumes": map[string]any{"mysql-data": map[string]any{}, "redis-data": map[string]any{}, "s3-data": map[string]any{}}}
	raw, err := jsonx.Marshal(compose)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(composeFile, raw, 0600))
	call := func(project string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "docker", append([]string{"compose", "-f", composeFile, "-p", project}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "compose %v: %s", args, string(out))
		return strings.TrimSpace(string(out))
	}
	for _, project := range []string{source, target} {
		project := project
		t.Cleanup(func() {
			_ = exec.Command("docker", "compose", "-f", composeFile, "-p", project, "down", "-v", "--remove-orphans").Run()
		})
	}
	call(source, "up", "-d", "--wait", "--wait-timeout", "150", "mysql", "redis", "object-store", "admin-api")
	call(target, "up", "-d", "--wait", "--wait-timeout", "150", "mysql", "redis")
	sourceAddr := call(source, "port", "mysql", "3306")
	targetAddr := call(target, "port", "mysql", "3306")
	sourceDSN := fmt.Sprintf("root:%s@tcp(%s)/oneapi?parseTime=true&multiStatements=true", password, sourceAddr)
	targetDSN := fmt.Sprintf("root:%s@tcp(%s)/oneapi?parseTime=true&multiStatements=true", password, targetAddr)
	open := func(dsn string) *gorm.DB {
		db, err := xdb.Open(xdb.DatabaseConfig{Driver: "mysql", DSN: dsn})
		require.NoError(t, err)
		pool, err := db.DB()
		require.NoError(t, err)
		t.Cleanup(func() { pool.Close() })
		return db
	}
	sourceDB := open(sourceDSN)
	pool, err := sourceDB.DB()
	require.NoError(t, err)
	migrator := migrate.NewWithDriver(pool, filepath.Join(root, "migrations"), "mysql")
	_, err = migrator.Apply(ctx)
	require.NoError(t, err)
	again, err := migrator.Apply(ctx)
	require.NoError(t, err)
	require.Empty(t, again)
	t.Setenv("JWT_SECRET_KEY", backupSecret(t))
	t.Setenv("TOKEN_HASH_KEY", backupSecret(t))
	t.Setenv("IDENTITY_ROUTING_V2", "false")
	t.Setenv("IDENTITY_STEP_UP_ENABLED", "false")
	t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "false")
	t.Setenv("BILLING_EXECUTION_DELIVERY_V1", "false")
	t.Setenv("BILLING_CANONICAL_USAGE_V1", "false")
	rdb := redis.NewClient(&redis.Options{Addr: call(source, "port", "redis", "6379"), Password: redisPassword})
	defer rdb.Close()
	require.NoError(t, rdb.Set(ctx, "backup:recovery-probe", "same-snapshot", time.Hour).Err())
	mfaKey := []byte(backupSecret(t))
	identity := identitytest.NewBackupSecurity(sourceDB, rdb, mfaKey)
	user, err := identity.Register(ctx, "backup-user", "password-backup-123", "backup@example.test", "default")
	require.NoError(t, err)
	token, err := identity.CreateAccessToken(ctx, user.ID, "backup-token", []string{"gpt-test"}, 0)
	require.NoError(t, err)
	require.NoError(t, sourceDB.Table("users").Where("id = ?", user.ID).Update("balance", 1000000).Error)
	billing := billingtest.NewServiceTierBilling(sourceDB)
	reserved, err := billing.ReserveQuota(ctx, &billingv1.ReserveQuotaRequest{UserId: fmt.Sprint(user.ID), RequestId: "backup-pending-settlement", EstimatedTokens: 100, Model: "gpt-test", ChannelId: "1"})
	require.NoError(t, err)
	require.True(t, reserved.Success, reserved.ErrorMessage)
	require.Positive(t, reserved.ReservedAmount, "recovery exercises a real positive pre-deduction")
	wallet, err := billing.GetAccountSnapshot(ctx, &billingv1.GetAccountSnapshotRequest{UserId: fmt.Sprint(user.ID)})
	require.NoError(t, err)
	_, session, err := identity.Login(ctx, "backup-user", "password-backup-123", "")
	require.NoError(t, err)
	require.NoError(t, identity.VerifyStepUp(ctx, session, "password-backup-123", ""))
	secret, _, _, err := identity.ManageMFA(ctx, session, "setup", "")
	require.NoError(t, err)
	code, err := totp.Code(secret, time.Now().Unix()/30-1, 6)
	require.NoError(t, err)
	_, _, _, err = identity.ManageMFA(ctx, session, "confirm", code)
	require.NoError(t, err)
	channelKey := "fixture-channel-secret"
	encrypted, err := appcrypto.Encrypt(channelKey, []byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	require.NoError(t, sourceDB.Table("channels").Create(map[string]any{"id": 1, "type": 1, "name": "backup-channel", "key": encrypted, "group": "default", "models": "gpt-test", "status": 1}).Error)
	require.NoError(t, sourceDB.Exec("INSERT INTO abilities (`group`,model,channel_id,enabled,priority) VALUES ('default','gpt-test',1,1,1)").Error)
	t.Setenv("BACKUP_FIXTURE_CONTROL", sourceDSN)
	t.Setenv("BACKUP_FIXTURE_SNAPSHOT", sourceDSN)
	t.Setenv("BACKUP_FIXTURE_REDIS", redisPassword)
	t.Setenv("BACKUP_FIXTURE_ACCESS", ak)
	t.Setenv("BACKUP_FIXTURE_SECRET", sk)
	endpoint := call(source, "port", "object-store", "9000")
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(ak, sk, ""), Secure: false})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return client.MakeBucket(ctx, "private-backups", minio.MakeBucketOptions{}) == nil }, 90*time.Second, time.Second, "private object store readiness")
	key, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	state := filepath.Join(scratch, "state")
	require.NoError(t, os.Mkdir(state, 0700))
	envFile := filepath.Join(scratch, "recovery.env")
	require.NoError(t, os.WriteFile(envFile, []byte("CHANNEL_ENCRYPTION_KEY=0123456789abcdef0123456789abcdef\nIDENTITY_MFA_ENCRYPTION_KEY="+string(mfaKey)+"\nJWT_SECRET_KEY="+os.Getenv("JWT_SECRET_KEY")+"\nTOKEN_HASH_KEY="+os.Getenv("TOKEN_HASH_KEY")+"\n"), 0600))
	payloadPath := filepath.Join(scratch, "multipart-fixture.bin")
	payload, err := os.OpenFile(payloadPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = io.CopyN(payload, rand.Reader, 20<<20)
	require.NoError(t, err)
	require.NoError(t, payload.Close())
	config := d.Config{ComposeFile: composeFile, Project: source, StateDir: state, ControlDSNEnv: "BACKUP_FIXTURE_CONTROL", SnapshotDSNEnv: "BACKUP_FIXTURE_SNAPSHOT", RedisPasswordEnv: "BACKUP_FIXTURE_REDIS", Schemas: []string{"oneapi"}, Files: map[string]string{"recovery.env": envFile, "multipart-fixture.bin": payloadPath}, KeyReferences: []string{"JWT_SECRET_KEY", "CHANNEL_ENCRYPTION_KEY", "IDENTITY_MFA_ENCRYPTION_KEY", "TOKEN_HASH_KEY"}, SourceRevision: "isolated-test-current-tree", Recipient: key.Recipient().String(), TimeoutMinutes: 5, S3: d.S3Config{Endpoint: "http://" + endpoint, Bucket: "private-backups", Prefix: "b3", AccessKeyEnv: "BACKUP_FIXTURE_ACCESS", SecretKeyEnv: "BACKUP_FIXTURE_SECRET"}}
	require.NoError(t, config.Validate())
	runner := &d.Runner{Config: config}
	unlockHost, err := runner.LockHostState()
	require.NoError(t, err)
	defer unlockHost()
	store, err := d.NewObjectStore(config.S3)
	require.NoError(t, err)
	require.NoError(t, store.Preflight(ctx))
	repo := d.NewRepo(&d.Data{DB: sourceDB})
	j, err := repo.Enqueue(ctx, "backup-real-empty-restore", "fixture-admin", "isolated empty restore", time.Now())
	require.NoError(t, err)
	// The global server connection lock rejects another process/connection.
	first := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, e := repo.Lead(ctx, func(context.Context) error { close(first); <-release; return nil })
		done <- e
	}()
	<-first
	acquired, err := repo.Lead(ctx, func(context.Context) error { return fmt.Errorf("duplicate leader executed") })
	require.NoError(t, err)
	require.False(t, acquired)
	close(release)
	require.NoError(t, <-done)
	backupStart := time.Now()
	acquired, err = repo.Lead(ctx, func(work context.Context) error { return d.Tick(work, repo, runner, store) })
	require.NoError(t, err)
	require.True(t, acquired)
	backupSeconds := time.Since(backupStart).Seconds()
	jobs, err := repo.List(ctx, 50)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	receipt := jobs[0]
	require.Equal(t, j.ID, receipt.ID)
	require.Equal(t, "succeeded", receipt.Status)
	require.NotEmpty(t, receipt.SHA256)
	require.Positive(t, receipt.Bytes)
	externalReceipt, err := store.Receipt(ctx, receipt.ID)
	require.NoError(t, err)
	require.Equal(t, receipt.SHA256, externalReceipt.SHA256)
	require.Error(t, store.PublishReceipt(ctx, externalReceipt), "external receipts are immutable")
	catalog, err := store.Catalog(ctx)
	require.NoError(t, err)
	require.Len(t, catalog, 1)
	require.Equal(t, "admin-api\nmysql\nobject-store\nredis", call(source, "ps", "--status", "running", "--services"), "resume original service set only")
	response, err := http.Get("http://" + endpoint + "/private-backups/" + receipt.ObjectKey)
	require.NoError(t, err)
	response.Body.Close()
	require.Contains(t, []int{401, 403}, response.StatusCode, "backup object must not be public")
	recoveryStart := time.Now()
	artifact := filepath.Join(scratch, "download.age")
	require.NoError(t, store.Get(ctx, receipt.ObjectKey, artifact, receipt.SHA256))
	_, err = store.Put(ctx, receipt.ObjectKey, artifact, receipt.SHA256)
	require.Error(t, err, "existing encrypted artifacts are immutable")
	// Wrong receipt never opens either target store.
	targetConfig := config
	targetConfig.Project = target
	targetConfig.StateDir = filepath.Join(scratch, "target-state")
	t.Setenv("BACKUP_FIXTURE_TARGET", targetDSN)
	targetConfig.SnapshotDSNEnv = "BACKUP_FIXTURE_TARGET"
	restorer := &d.Runner{Config: targetConfig}
	_, err = restorer.Restore(ctx, artifact, key.String(), strings.Repeat("0", 64))
	require.Error(t, err)
	restoreStart := time.Now()
	manifest, err := restorer.Restore(ctx, artifact, key.String(), receipt.SHA256)
	require.NoError(t, err)
	restoreSeconds := time.Since(restoreStart).Seconds()
	require.Less(t, restoreSeconds, 3600.0)
	require.Equal(t, source, manifest.Project)
	require.Equal(t, "mysql\nredis", call(target, "ps", "--status", "running", "--services"))
	restored := open(targetDSN)
	targetRedis := redis.NewClient(&redis.Options{Addr: call(target, "port", "redis", "6379"), Password: redisPassword})
	defer targetRedis.Close()
	value, err := targetRedis.Get(ctx, "backup:recovery-probe").Result()
	require.NoError(t, err)
	require.Equal(t, "same-snapshot", value)
	configBytes, err := os.ReadFile(filepath.Join(targetConfig.StateDir, "recovered-"+manifest.JobID, "config", "recovery.env"))
	require.NoError(t, err)
	recoveredKeys := map[string]string{}
	for _, line := range strings.Split(string(configBytes), "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			recoveredKeys[parts[0]] = parts[1]
		}
	}
	for _, name := range []string{"JWT_SECRET_KEY", "TOKEN_HASH_KEY"} {
		require.NotEmpty(t, recoveredKeys[name])
		t.Setenv(name, recoveredKeys[name])
	}
	require.NotEmpty(t, recoveredKeys["IDENTITY_MFA_ENCRYPTION_KEY"])
	require.NotEmpty(t, recoveredKeys["CHANNEL_ENCRYPTION_KEY"])
	recoveredIdentity := identitytest.NewBackupSecurity(restored, targetRedis, []byte(recoveredKeys["IDENTITY_MFA_ENCRYPTION_KEY"]))
	valid, err := recoveredIdentity.ValidateToken(ctx, token.Key, "127.0.0.1")
	require.NoError(t, err)
	require.Equal(t, token.ID, valid.ID)
	_, _, err = recoveredIdentity.Login(ctx, "backup-user", "password-backup-123", "")
	var challenge *identitytest.MFARequired
	require.ErrorAs(t, err, &challenge)
	code, err = totp.Code(secret, time.Now().Unix()/30, 6)
	require.NoError(t, err)
	_, newSession, err := recoveredIdentity.CompleteMFALogin(ctx, challenge.Challenge, code)
	require.NoError(t, err)
	require.NotEmpty(t, newSession)
	selected, err := channeltest.NewBackupRecoveryService(restored, []byte(recoveredKeys["CHANNEL_ENCRYPTION_KEY"])).GetChannelModel(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, channelKey, selected.Key)
	recoveredBilling := billingtest.NewServiceTierBilling(restored)
	gotWallet, err := recoveredBilling.GetAccountSnapshot(ctx, &billingv1.GetAccountSnapshotRequest{UserId: fmt.Sprint(user.ID)})
	require.NoError(t, err)
	require.Equal(t, wallet.Snapshot, gotWallet.Snapshot)
	replay, err := recoveredBilling.ReserveQuota(ctx, &billingv1.ReserveQuotaRequest{UserId: fmt.Sprint(user.ID), RequestId: "backup-pending-settlement", EstimatedTokens: 100, Model: "gpt-test", ChannelId: "1"})
	require.NoError(t, err)
	require.Equal(t, reserved.ReservationId, replay.ReservationId)
	released, err := recoveredBilling.ReleaseQuota(ctx, &billingv1.ReleaseQuotaRequest{ReservationId: reserved.ReservationId, Reason: "isolated restore acceptance"})
	require.NoError(t, err)
	require.True(t, released.Success)
	againRelease, err := recoveredBilling.ReleaseQuota(ctx, &billingv1.ReleaseQuotaRequest{ReservationId: reserved.ReservationId, Reason: "duplicate recovery"})
	require.NoError(t, err)
	require.True(t, againRelease.Success)
	finalWallet, err := recoveredBilling.GetAccountSnapshot(ctx, &billingv1.GetAccountSnapshotRequest{UserId: fmt.Sprint(user.ID)})
	require.NoError(t, err)
	require.EqualValues(t, 1000000, finalWallet.Snapshot.Balance)
	_, err = restorer.Restore(ctx, artifact, key.String(), receipt.SHA256)
	require.ErrorContains(t, err, "empty MySQL", "nonempty target refused")
	recoveryVerifiedSeconds := time.Since(recoveryStart).Seconds()
	require.Less(t, recoveryVerifiedSeconds, 3600.0)
	// Simulate power loss after the durable restart journal was written. Even
	// unavailable remote storage must not keep the original services stopped.
	_, err = repo.Enqueue(ctx, "backup-interrupted-recovery", "fixture-admin", "interruption recovery", time.Now())
	require.NoError(t, err)
	interrupted, err := repo.Claim(ctx, time.Now())
	require.NoError(t, err)
	require.NotNil(t, interrupted)
	journal, _ := jsonx.Marshal(map[string]any{"project": source, "compose_file": composeFile, "services": []string{"admin-api", "redis"}})
	require.NoError(t, os.WriteFile(filepath.Join(state, "resume.json"), journal, 0600))
	call(source, "stop", "--timeout", "10", "admin-api", "redis")
	unavailable := config.S3
	unavailable.Bucket = "unconfigured-bucket"
	badStore, err := d.NewObjectStore(unavailable)
	require.NoError(t, err)
	_, err = repo.Lead(ctx, func(work context.Context) error { return d.Tick(work, repo, runner, badStore) })
	require.ErrorContains(t, err, "storage_preflight_failed")
	require.Equal(t, "admin-api\nmysql\nobject-store\nredis", call(source, "ps", "--status", "running", "--services"))
	jobs, err = repo.List(ctx, 50)
	require.NoError(t, err)
	for _, job := range jobs {
		if job.ID == interrupted.ID {
			require.Equal(t, "uncertain", job.Status)
		}
	}
	// A snapshot failure after stopping writers follows the same resume path.
	deniedUser := "backup_denied"
	require.NoError(t, sourceDB.Exec("CREATE USER '"+deniedUser+"'@'%' IDENTIFIED BY '"+password+"'").Error)
	require.NoError(t, sourceDB.Exec("GRANT SELECT, SHOW DATABASES ON *.* TO '"+deniedUser+"'@'%'").Error)
	deniedDSN := fmt.Sprintf("%s:%s@tcp(%s)/oneapi?parseTime=true", deniedUser, password, sourceAddr)
	t.Setenv("BACKUP_FIXTURE_DENIED", deniedDSN)
	failedConfig := config
	failedConfig.SnapshotDSNEnv = "BACKUP_FIXTURE_DENIED"
	_, err = repo.Enqueue(ctx, "backup-lock-failure-recovery", "fixture-admin", "snapshot failure recovery", time.Now())
	require.NoError(t, err)
	_, err = repo.Lead(ctx, func(work context.Context) error { return d.Tick(work, repo, &d.Runner{Config: failedConfig}, store) })
	require.ErrorContains(t, err, "snapshot_failed")
	require.Equal(t, "admin-api\nmysql\nobject-store\nredis", call(source, "ps", "--status", "running", "--services"))
	evidence := map[string]any{"date": "2026-10-30", "scope": "isolated MySQL8.4 Redis8.4 S3 empty restore", "production_changed": false, "backup_seconds": backupSeconds, "restore_seconds": restoreSeconds, "recovery_verified_seconds": recoveryVerifiedSeconds, "snapshot_at": manifest.SnapshotAt, "artifact_bytes": receipt.Bytes, "artifact_sha256": receipt.SHA256, "images": manifest.Images, "checks": []string{"full_migrations_repeat", "connection_leader_exclusion", "exact_writer_resume", "private_s3_encrypted_roundtrip", "wrong_receipt_rejected", "empty_restore", "redis_value", "original_api_token", "mfa_secret_decryption_login", "channel_key_decryption", "wallet_exact", "pending_reservation_idempotency", "duplicate_release_once", "nonempty_restore_refused", "external_s3_recovery_catalog", "multipart_encrypted_roundtrip", "conditional_immutable_objects", "interruption_journal_resume_before_storage", "snapshot_lock_failure_resumes_writers"}}
	if path := os.Getenv("BACKUP_EVIDENCE_PATH"); path != "" {
		raw, err := jsonx.MarshalIndent(evidence, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, append(raw, '\n'), 0644))
	}
	t.Logf("backup %.3fs, restore %.3fs, encrypted bytes %d", backupSeconds, restoreSeconds, receipt.Bytes)
}
