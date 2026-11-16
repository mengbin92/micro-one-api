package integration

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/app/channel/internal/data"
	"micro-one-api/app/channel/internal/service"
	"micro-one-api/platform/grpc/xgrpc"
	"micro-one-api/platform/security/serviceidentity"
)

// TestListWriteAuditsThroughRealInterceptor drives the channel owner's real
// service → biz → data stack behind the real service-identity interceptor —
// the exact chain the admin audit console reads through. It pins the
// cross-owner audit fix: the admin system identity passes and rows come back
// sorted; a relay identity or a forwarded operator credential is denied.
func TestListWriteAuditsThroughRealInterceptor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channel.db")
	seed, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, seed.Exec(`CREATE TABLE resource_write_audits (
		event_id VARCHAR(64) PRIMARY KEY,
		actor_user_id BIGINT NOT NULL,
		operation VARCHAR(160) NOT NULL,
		resource_id VARCHAR(128) NOT NULL,
		decision_versions TEXT NOT NULL,
		reason TEXT NOT NULL,
		occurred_at BIGINT NOT NULL,
		result VARCHAR(16) NOT NULL
	)`).Error)
	require.NoError(t, seed.Exec(`INSERT INTO resource_write_audits
		(event_id, actor_user_id, operation, resource_id, decision_versions, reason, occurred_at, result)
		VALUES ('owner-ev-1', 7, 'channel.channel.update', '1', '[2]', 'ops', 300, 'success'),
		       ('owner-ev-2', 7, 'channel.model_mapping.create', '12', '[2]', 'ops', 100, 'success')`).Error)
	sqlDB, err := seed.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	t.Setenv("CHANNEL_ENCRYPTION_KEY", "01234567890123456789012345678901")
	repo, err := data.NewRepositoryFromEnv("sqlite3", path)
	require.NoError(t, err)
	svc := service.NewChannelService(nil)
	svc.SetWriteAuditUsecase(biz.NewWriteAuditUsecase(data.NewWriteAuditRepo(repo)))

	verifier, err := serviceidentity.NewVerifier(map[string]string{"admin": "admin-test-private", "relay": "relay-test-private"}, "legacy")
	require.NoError(t, err)
	srv := grpc.NewServer(grpc.UnaryInterceptor(xgrpc.ServiceIdentityUnaryInterceptor(verifier)))
	channelv1.RegisterChannelServiceServer(srv, svc)
	lis := bufconn.Listen(1024 * 1024)
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	go func() { _ = srv.Serve(lis) }()

	dial := func(t *testing.T, token string) channelv1.ChannelServiceClient {
		conn, err := grpc.NewClient("passthrough:///audit-test",
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
			grpc.WithPerRPCCredentials(staticTokenAuth{token: token}))
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return channelv1.NewChannelServiceClient(conn)
	}

	admin := dial(t, "admin-test-private")
	resp, err := admin.ListWriteAudits(context.Background(), &channelv1.ListWriteAuditsRequest{PageSize: 10})
	require.NoError(t, err)
	require.EqualValues(t, 2, resp.Total)
	require.Equal(t, []string{"owner-ev-1", "owner-ev-2"}, []string{resp.Items[0].EventId, resp.Items[1].EventId},
		"occurred_at DESC ordering")
	require.Equal(t, "channel.channel.update", resp.Items[0].Operation)
	require.Equal(t, "1", resp.Items[0].ResourceId)

	// Operation prefix filter crosses the whole chain.
	resp, err = admin.ListWriteAudits(context.Background(), &channelv1.ListWriteAuditsRequest{Operation: "channel.model_mapping", PageSize: 10})
	require.NoError(t, err)
	require.EqualValues(t, 1, resp.Total)
	require.Equal(t, "owner-ev-2", resp.Items[0].EventId)

	// A non-admin dedicated identity is rejected by the registry.
	relay := dial(t, "relay-test-private")
	_, err = relay.ListWriteAudits(context.Background(), &channelv1.ListWriteAuditsRequest{PageSize: 10})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

type staticTokenAuth struct {
	token string
}

func (a staticTokenAuth) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + a.token}, nil
}

func (a staticTokenAuth) RequireTransportSecurity() bool { return false }
