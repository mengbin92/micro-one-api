package integration

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	logv1 "micro-one-api/api/log/v1"
	logbiz "micro-one-api/app/log/internal/biz"
	logservice "micro-one-api/app/log/internal/service"
)

// ========== Log Service Tests ==========

type testLogRepo struct {
	logs  []*logbiz.LogEntry
	idSeq int64
}

func (r *testLogRepo) Create(ctx context.Context, entry *logbiz.LogEntry) error {
	r.idSeq++
	entry.ID = r.idSeq
	r.logs = append(r.logs, entry)
	return nil
}

func (r *testLogRepo) Get(ctx context.Context, id int64) (*logbiz.LogEntry, error) {
	for _, l := range r.logs {
		if l.ID == id {
			return l, nil
		}
	}
	return nil, logbiz.ErrLogNotFound
}

func (r *testLogRepo) List(ctx context.Context, page, pageSize int32, level, source, keyword string) ([]*logbiz.LogEntry, int64, error) {
	var result []*logbiz.LogEntry
	for _, l := range r.logs {
		if (level == "" || l.Level == level) && (source == "" || l.Source == source) {
			result = append(result, l)
		}
	}
	return result, int64(len(result)), nil
}

func (r *testLogRepo) ListByUser(ctx context.Context, userID int64, page, pageSize int32, level, keyword string) ([]*logbiz.LogEntry, int64, error) {
	var result []*logbiz.LogEntry
	for _, l := range r.logs {
		if l.UserID == userID && (level == "" || l.Level == level) {
			result = append(result, l)
		}
	}
	return result, int64(len(result)), nil
}

func (r *testLogRepo) UsageByUser(ctx context.Context, userID int64, startTime, endTime time.Time) ([]*logbiz.UsageStat, error) {
	return nil, nil
}

func (r *testLogRepo) Delete(ctx context.Context, filter logbiz.DeleteLogsFilter) (int64, error) {
	return 0, nil
}

func (r *testLogRepo) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	return 0, nil
}

func setupLogService(t *testing.T, addr string) (func(), logv1.LogServiceClient) {
	repo := &testLogRepo{}
	uc := logbiz.NewLogUsecase(repo)
	svc := logservice.NewLogService(uc)

	server := grpc.NewServer()
	logv1.RegisterLogServiceServer(server, svc)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	go func() {
		if err := server.Serve(lis); err != nil {
			t.Logf("log server error: %v", err)
		}
	}()

	cleanup := func() {
		server.Stop()
		lis.Close()
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	return cleanup, logv1.NewLogServiceClient(conn)
}

func TestLogIntegration(t *testing.T) {
	cleanup, client := setupLogService(t, "127.0.0.1:19011")
	defer cleanup()

	ctx := context.Background()

	t.Run("IngestAndGet", func(t *testing.T) {
		ingestResp, err := client.IngestLog(ctx, &logv1.IngestLogRequest{
			Level:   "info",
			Message: "test log message",
			Source:  "test",
		})
		if err != nil {
			t.Fatalf("IngestLog failed: %v", err)
		}
		if ingestResp.Id == 0 {
			t.Fatal("expected non-zero log ID")
		}

		getResp, err := client.GetLog(ctx, &logv1.GetLogRequest{Id: ingestResp.Id})
		if err != nil {
			t.Fatalf("GetLog failed: %v", err)
		}
		if getResp.Message != "test log message" {
			t.Fatalf("expected message 'test log message', got '%s'", getResp.Message)
		}
	})

	t.Run("ListLogs", func(t *testing.T) {
		_, _ = client.IngestLog(ctx, &logv1.IngestLogRequest{Level: "error", Message: "error log", Source: "test"})
		_, _ = client.IngestLog(ctx, &logv1.IngestLogRequest{Level: "info", Message: "info log", Source: "test"})

		resp, err := client.ListLogs(ctx, &logv1.ListLogsRequest{
			Page:     1,
			PageSize: 10,
		})
		if err != nil {
			t.Fatalf("ListLogs failed: %v", err)
		}
		if resp.Total < 2 {
			t.Fatalf("expected at least 2 logs, got %d", resp.Total)
		}
	})
}
