// request-timing-smoke validates a new log owner before enabling producers.
// Run inside relay-gateway so the existing dedicated service credential stays
// in its environment; no operator credentials or financial RPCs are used.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/pkg/jsonx"
	grpcauth "micro-one-api/platform/grpc"
	"micro-one-api/platform/security/serviceidentity"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	endpoint := flag.String("endpoint", "log-service:9006", "internal log owner endpoint")
	flag.Parse()
	token := serviceidentity.ClientToken()
	if token == "" {
		return fmt.Errorf("dedicated service credential is required")
	}
	conn, err := grpc.NewClient(*endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithPerRPCCredentials(grpcauth.NewInsecureTokenAuth(token)))
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := uuid.NewString()
	reservation := "timing-canary-" + id
	client := logv1.NewLogServiceClient(conn)
	req := &logv1.IngestLogRequest{Source: requesttrace.TimingSource, RootRequestId: "rollout-canary:" + id, DedupeKey: requesttrace.TimingDedupeKey(id), RequestTiming: &logv1.RequestTimingSummary{
		Version: 1, ObservationId: id, ObservationKind: "grpc", Protocol: "grpc", Endpoint: "/api.relay.v1.RelayService/ChatCompletion", ExecutionPath: "grpc", RootRequestId: "rollout-canary:" + id, StartedAtMs: time.Now().UnixMilli(), Outcome: "rejected", CollectionState: "not_recorded",
		Attempts: []*logv1.RequestTimingAttempt{{Number: 1, RequestId: "canary-unreserved", Outcome: "rejected"}, {Number: 2, RequestId: "canary-reserved", ReservationId: &reservation, Outcome: "rejected"}},
	}}
	first, err := client.IngestLog(ctx, req)
	if err != nil {
		return fmt.Errorf("intake failed: %w", err)
	}
	second, err := client.IngestLog(ctx, req)
	if err != nil || first.GetId() <= 0 || first.GetId() != second.GetId() {
		return fmt.Errorf("idempotent intake did not return the original row")
	}
	_, err = client.GetRequestTrace(ctx, &logv1.GetRequestTraceRequest{ObservationId: id})
	if status.Code(err) != codes.PermissionDenied {
		return fmt.Errorf("producer read boundary: got %s, want PermissionDenied", status.Code(err))
	}
	encoded, err := jsonx.Marshal(map[string]any{"observation_id": id, "log_id": first.Id, "attempts": 2, "dedupe": "passed", "producer_read": "denied"})
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}
