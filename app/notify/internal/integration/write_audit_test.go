package integration

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	ownerv1 "micro-one-api/api/notify/v1"
	"micro-one-api/app/notify/internal/biz"
	"micro-one-api/app/notify/internal/data"
	"micro-one-api/app/notify/internal/service"
	"micro-one-api/platform/authz"
	"micro-one-api/platform/grpc/xgrpc"
	"micro-one-api/platform/security/serviceidentity"
)

func TestListWriteAuditsThroughRealInterceptors(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "audit.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.Exec(`CREATE TABLE resource_write_audits (
        event_id TEXT PRIMARY KEY, actor_user_id BIGINT NOT NULL,
        operation TEXT NOT NULL, resource_id TEXT NOT NULL,
        decision_versions TEXT NOT NULL, reason TEXT NOT NULL,
        occurred_at BIGINT NOT NULL, result TEXT NOT NULL
    )`).Error)
	require.NoError(t, db.Exec(`INSERT INTO resource_write_audits
        (event_id, actor_user_id, operation, resource_id, decision_versions, reason, occurred_at, result)
        VALUES ('old', 7, 'notify.notification_rule.update', '1', '[2]', 'ops', 100, 'success'),
               ('new', 7, 'notify.notification.test', '2', '[2]', 'ops', 300, 'success')`).Error)

	repo := data.NewRepositoryWithDB(db).(*data.Repository)
	svc := service.NewNotifyService(nil, "", "")
	svc.SetWriteAuditUsecase(biz.NewWriteAuditUsecase(data.NewWriteAuditRepo(repo)))
	verifier, err := serviceidentity.NewVerifier(map[string]string{
		"admin": "admin-test-private", "relay": "relay-test-private",
	}, "legacy")
	require.NoError(t, err)
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(
		xgrpc.ServiceIdentityUnaryInterceptor(verifier), authz.OperatorUnaryInterceptor(),
	))
	ownerv1.RegisterNotifyServiceServer(srv, svc)
	lis := bufconn.Listen(1024 * 1024)
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///audit-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := ownerv1.NewNotifyServiceClient(conn)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	adminCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer admin-test-private")
	reply, err := client.ListWriteAudits(adminCtx, &ownerv1.ListWriteAuditsRequest{PageSize: 10})
	require.NoError(t, err)
	require.EqualValues(t, 2, reply.Total)
	require.Len(t, reply.Items, 2)
	require.Equal(t, []string{"new", "old"}, []string{reply.Items[0].EventId, reply.Items[1].EventId})
	require.Len(t, reply.StoreId, 64)

	filtered, err := client.ListWriteAudits(adminCtx, &ownerv1.ListWriteAuditsRequest{
		Operation: "notify.notification_rule.update", PageSize: 10,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, filtered.Total)
	require.Len(t, filtered.Items, 1)
	require.Equal(t, "old", filtered.Items[0].EventId)
	require.Equal(t, reply.StoreId, filtered.StoreId)

	relayCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer relay-test-private")
	_, err = client.ListWriteAudits(relayCtx, &ownerv1.ListWriteAuditsRequest{PageSize: 10})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	operatorCtx := metadata.AppendToOutgoingContext(adminCtx, "x-operator-authorization", "Bearer operator-test")
	_, err = client.ListWriteAudits(operatorCtx, &ownerv1.ListWriteAuditsRequest{PageSize: 10})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}
