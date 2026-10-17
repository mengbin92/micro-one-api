package integration

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/app/log/internal/biz"
	"micro-one-api/app/log/internal/data"
	"micro-one-api/app/log/internal/service"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/platform/grpc/xgrpc"
	"micro-one-api/platform/security/serviceidentity"
)

func TestTimingSummarySurvivesAuthenticatedGRPCIngestion(t *testing.T) {
	repo := data.NewMemoryRepositoryForTest()
	svc := service.NewLogService(biz.NewLogUsecase(repo))
	verifier, err := serviceidentity.NewVerifier(map[string]string{"relay": "relay-test-private", "admin": "admin-test-private"}, "")
	require.NoError(t, err)
	srv := grpc.NewServer(grpc.UnaryInterceptor(xgrpc.ServiceIdentityUnaryInterceptor(verifier)))
	logv1.RegisterLogServiceServer(srv, svc)
	lis := bufconn.Listen(1024 * 1024)
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	go func() { _ = srv.Serve(lis) }()
	conn, err := grpc.NewClient("passthrough:///timing-test", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := logv1.NewLogServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := &logv1.IngestLogRequest{Source: requesttrace.TimingSource, UserId: 7, RootRequestId: "root",
		RequestTiming: &logv1.RequestTimingSummary{
			Version: 1, ObservationId: uuid.NewString(), ObservationKind: "grpc", Protocol: "grpc",
			Endpoint: "/api.relay.v1.RelayService/Chat", ExecutionPath: "grpc", UserId: 7, RootRequestId: "root",
			StartedAtMs: 1000, Outcome: "failed", CollectionState: "not_recorded",
			Attempts: []*logv1.RequestTimingAttempt{{Number: 1, RequestId: "attempt", Outcome: "rejected"}},
		},
	}
	_, err = client.IngestLog(ctx, p)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	admin := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer admin-test-private")
	_, err = client.IngestLog(admin, p)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	relay := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer relay-test-private")
	first, err := client.IngestLog(relay, p)
	require.NoError(t, err)
	second, err := client.IngestLog(relay, p)
	require.NoError(t, err)
	require.Equal(t, first.Id, second.Id)
	stored, err := repo.Get(context.Background(), first.Id)
	require.NoError(t, err)
	require.Equal(t, p.RequestTiming.ObservationId, stored.RequestTiming.ObservationID)
	require.Len(t, stored.RequestTiming.Attempts, 1)
	require.Nil(t, stored.RequestTiming.Attempts[0].ReservationID)
	require.Nil(t, stored.RequestTiming.OutputDurationMS, "unary transport must not invent client write completion")
	require.Empty(t, stored.RequestTiming.OTelTraceID)
	// A system producer still cannot read back arbitrary logs through the RPC.
	_, err = client.GetLog(relay, &logv1.GetLogRequest{Id: first.Id})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}
