package integration

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	configv1 "micro-one-api/api/config/v1"
	logv1 "micro-one-api/api/log/v1"
	monitorv1 "micro-one-api/api/monitor/v1"
	notifyv1 "micro-one-api/api/notify/v1"

	configbiz "micro-one-api/internal/config/biz"
	configservice "micro-one-api/internal/config/service"

	logbiz "micro-one-api/internal/log/biz"
	logservice "micro-one-api/internal/log/service"

	monitorbiz "micro-one-api/internal/monitor/biz"
	monitorservice "micro-one-api/internal/monitor/service"

	notifybiz "micro-one-api/internal/notify/biz"
	notifyservice "micro-one-api/internal/notify/service"
)

// ========== Config Service Tests ==========

type testConfigRepo struct {
	entries map[string]*configbiz.ConfigEntry
}

func (r *testConfigRepo) Get(ctx context.Context, namespace, key string) (*configbiz.ConfigEntry, error) {
	k := namespace + "/" + key
	entry, ok := r.entries[k]
	if !ok {
		return nil, configbiz.ErrConfigNotFound
	}
	return entry, nil
}

func (r *testConfigRepo) List(ctx context.Context, namespace string, page, pageSize int32) ([]*configbiz.ConfigEntry, int64, error) {
	var result []*configbiz.ConfigEntry
	for _, e := range r.entries {
		if e.Namespace == namespace {
			result = append(result, e)
		}
	}
	return result, int64(len(result)), nil
}

func (r *testConfigRepo) Set(ctx context.Context, entry *configbiz.ConfigEntry) error {
	k := entry.Namespace + "/" + entry.Key
	r.entries[k] = entry
	return nil
}

func (r *testConfigRepo) Delete(ctx context.Context, namespace, key string) error {
	k := namespace + "/" + key
	delete(r.entries, k)
	return nil
}

func setupConfigService(t *testing.T, addr string) (func(), configv1.ConfigServiceClient) {
	repo := &testConfigRepo{entries: make(map[string]*configbiz.ConfigEntry)}
	uc := configbiz.NewConfigUsecase(repo, nil)
	svc := configservice.NewConfigService(uc)

	server := grpc.NewServer()
	configv1.RegisterConfigServiceServer(server, svc)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	go func() {
		if err := server.Serve(lis); err != nil {
			t.Logf("config server error: %v", err)
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

	return cleanup, configv1.NewConfigServiceClient(conn)
}

func TestConfigIntegration(t *testing.T) {
	cleanup, client := setupConfigService(t, "127.0.0.1:19010")
	defer cleanup()

	ctx := context.Background()

	t.Run("SetAndGet", func(t *testing.T) {
		_, err := client.SetConfig(ctx, &configv1.SetConfigRequest{
			Namespace: "test",
			Key:       "model_ratio",
			Value:     "1.5",
			Comment:   "test config",
		})
		if err != nil {
			t.Fatalf("SetConfig failed: %v", err)
		}

		resp, err := client.GetConfig(ctx, &configv1.GetConfigRequest{
			Namespace: "test",
			Key:       "model_ratio",
		})
		if err != nil {
			t.Fatalf("GetConfig failed: %v", err)
		}
		if resp.Value != "1.5" {
			t.Fatalf("expected value '1.5', got '%s'", resp.Value)
		}
	})

	t.Run("ListConfigs", func(t *testing.T) {
		_, err := client.SetConfig(ctx, &configv1.SetConfigRequest{
			Namespace: "test",
			Key:       "group_ratio",
			Value:     "0.8",
		})
		if err != nil {
			t.Fatalf("SetConfig failed: %v", err)
		}

		resp, err := client.ListConfigs(ctx, &configv1.ListConfigsRequest{
			Namespace: "test",
			Page:      1,
			PageSize:  10,
		})
		if err != nil {
			t.Fatalf("ListConfigs failed: %v", err)
		}
		if resp.Total < 2 {
			t.Fatalf("expected at least 2 configs, got %d", resp.Total)
		}
	})

	t.Run("DeleteConfig", func(t *testing.T) {
		_, err := client.DeleteConfig(ctx, &configv1.DeleteConfigRequest{
			Namespace: "test",
			Key:       "model_ratio",
		})
		if err != nil {
			t.Fatalf("DeleteConfig failed: %v", err)
		}

		_, err = client.GetConfig(ctx, &configv1.GetConfigRequest{
			Namespace: "test",
			Key:       "model_ratio",
		})
		if err == nil {
			t.Fatal("expected error for deleted config, got nil")
		}
	})
}

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

// ========== Monitor Service Tests ==========

type testMonitorRepo struct {
	checks []*monitorbiz.HealthCheck
	rules  []*monitorbiz.AlertRule
	idSeq  int64
}

func (r *testMonitorRepo) SaveHealthCheck(ctx context.Context, check *monitorbiz.HealthCheck) error {
	r.idSeq++
	check.ID = r.idSeq
	r.checks = append(r.checks, check)
	return nil
}

func (r *testMonitorRepo) ListHealthChecks(ctx context.Context, serviceName string, page, pageSize int32) ([]*monitorbiz.HealthCheck, int64, error) {
	var result []*monitorbiz.HealthCheck
	for _, c := range r.checks {
		if serviceName == "" || c.ServiceName == serviceName {
			result = append(result, c)
		}
	}
	return result, int64(len(result)), nil
}

func (r *testMonitorRepo) GetLatestHealthCheck(ctx context.Context, serviceName string) (*monitorbiz.HealthCheck, error) {
	for i := len(r.checks) - 1; i >= 0; i-- {
		if r.checks[i].ServiceName == serviceName {
			return r.checks[i], nil
		}
	}
	return nil, monitorbiz.ErrHealthCheckNotFound
}

func (r *testMonitorRepo) CreateAlertRule(ctx context.Context, rule *monitorbiz.AlertRule) error {
	r.idSeq++
	rule.ID = r.idSeq
	r.rules = append(r.rules, rule)
	return nil
}

func (r *testMonitorRepo) GetAlertRule(ctx context.Context, id int64) (*monitorbiz.AlertRule, error) {
	for _, rule := range r.rules {
		if rule.ID == id {
			return rule, nil
		}
	}
	return nil, monitorbiz.ErrAlertRuleNotFound
}

func (r *testMonitorRepo) ListAlertRules(ctx context.Context, page, pageSize int32) ([]*monitorbiz.AlertRule, int64, error) {
	return r.rules, int64(len(r.rules)), nil
}

func (r *testMonitorRepo) UpdateAlertRule(ctx context.Context, rule *monitorbiz.AlertRule) error {
	for i, existing := range r.rules {
		if existing.ID == rule.ID {
			r.rules[i] = rule
			return nil
		}
	}
	return monitorbiz.ErrAlertRuleNotFound
}

func (r *testMonitorRepo) DeleteAlertRule(ctx context.Context, id int64) error {
	for i, rule := range r.rules {
		if rule.ID == id {
			r.rules = append(r.rules[:i], r.rules[i+1:]...)
			return nil
		}
	}
	return monitorbiz.ErrAlertRuleNotFound
}

func setupMonitorService(t *testing.T, addr string) (func(), monitorv1.MonitorServiceClient) {
	repo := &testMonitorRepo{}
	uc := monitorbiz.NewMonitorUsecase(repo)
	svc := monitorservice.NewMonitorService(uc)

	server := grpc.NewServer()
	monitorv1.RegisterMonitorServiceServer(server, svc)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	go func() {
		if err := server.Serve(lis); err != nil {
			t.Logf("monitor server error: %v", err)
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

	return cleanup, monitorv1.NewMonitorServiceClient(conn)
}

func TestMonitorIntegration(t *testing.T) {
	cleanup, client := setupMonitorService(t, "127.0.0.1:19012")
	defer cleanup()

	ctx := context.Background()

	t.Run("SaveAndListHealthChecks", func(t *testing.T) {
		_, err := client.SaveHealthCheck(ctx, &monitorv1.SaveHealthCheckRequest{
			ServiceName:  "relay-gateway",
			Status:       "healthy",
			ResponseTime: 50,
		})
		if err != nil {
			t.Fatalf("SaveHealthCheck failed: %v", err)
		}

		resp, err := client.ListHealthChecks(ctx, &monitorv1.ListHealthChecksRequest{
			ServiceName: "relay-gateway",
			Page:        1,
			PageSize:    10,
		})
		if err != nil {
			t.Fatalf("ListHealthChecks failed: %v", err)
		}
		if resp.Total < 1 {
			t.Fatalf("expected at least 1 health check, got %d", resp.Total)
		}
	})

	t.Run("CreateAndListAlertRules", func(t *testing.T) {
		_, err := client.CreateAlertRule(ctx, &monitorv1.CreateAlertRuleRequest{
			Name:        "high-latency",
			ServiceName: "relay-gateway",
			Metric:      "response_time",
			Threshold:   1000,
			Operator:    ">",
			Duration:    60,
			Enabled:     true,
		})
		if err != nil {
			t.Fatalf("CreateAlertRule failed: %v", err)
		}

		resp, err := client.ListAlertRules(ctx, &monitorv1.ListAlertRulesRequest{
			Page:     1,
			PageSize: 10,
		})
		if err != nil {
			t.Fatalf("ListAlertRules failed: %v", err)
		}
		if resp.Total < 1 {
			t.Fatalf("expected at least 1 alert rule, got %d", resp.Total)
		}
	})
}

// ========== Notify Service Tests ==========

type testNotifyRepo struct {
	notifications []*notifybiz.Notification
	idSeq         int64
}

func (r *testNotifyRepo) Create(ctx context.Context, n *notifybiz.Notification) error {
	r.idSeq++
	n.ID = r.idSeq
	n.Status = "pending"
	r.notifications = append(r.notifications, n)
	return nil
}

func (r *testNotifyRepo) Get(ctx context.Context, id int64) (*notifybiz.Notification, error) {
	for _, n := range r.notifications {
		if n.ID == id {
			return n, nil
		}
	}
	return nil, notifybiz.ErrNotificationNotFound
}

func (r *testNotifyRepo) List(ctx context.Context, page, pageSize int32, notifyType, status string) ([]*notifybiz.Notification, int64, error) {
	var result []*notifybiz.Notification
	for _, n := range r.notifications {
		if (notifyType == "" || n.Type == notifyType) && (status == "" || n.Status == status) {
			result = append(result, n)
		}
	}
	return result, int64(len(result)), nil
}

func (r *testNotifyRepo) UpdateStatus(ctx context.Context, id int64, status string) error {
	for _, n := range r.notifications {
		if n.ID == id {
			n.Status = status
			return nil
		}
	}
	return notifybiz.ErrNotificationNotFound
}

func (r *testNotifyRepo) ListPending(ctx context.Context, limit int32, maxRetry int) ([]*notifybiz.Notification, error) {
	var result []*notifybiz.Notification
	for _, n := range r.notifications {
		if n.Status == notifybiz.NotifyStatusPending && n.RetryCount < maxRetry {
			result = append(result, n)
			if int32(len(result)) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (r *testNotifyRepo) MarkFailed(ctx context.Context, id int64) error {
	for _, n := range r.notifications {
		if n.ID == id {
			n.Status = notifybiz.NotifyStatusFailed
			return nil
		}
	}
	return notifybiz.ErrNotificationNotFound
}

func (r *testNotifyRepo) RecordFailure(ctx context.Context, id int64, maxRetry int) error {
	for _, n := range r.notifications {
		if n.ID == id {
			n.RetryCount++
			if n.RetryCount >= maxRetry {
				n.Status = notifybiz.NotifyStatusFailed
			}
			return nil
		}
	}
	return notifybiz.ErrNotificationNotFound
}

func setupNotifyService(t *testing.T, addr string) (func(), notifyv1.NotifyServiceClient) {
	repo := &testNotifyRepo{}
	uc := notifybiz.NewNotifyUsecase(repo)
	svc := notifyservice.NewNotifyService(uc)

	server := grpc.NewServer()
	notifyv1.RegisterNotifyServiceServer(server, svc)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	go func() {
		if err := server.Serve(lis); err != nil {
			t.Logf("notify server error: %v", err)
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

	return cleanup, notifyv1.NewNotifyServiceClient(conn)
}

func TestNotifyIntegration(t *testing.T) {
	cleanup, client := setupNotifyService(t, "127.0.0.1:19013")
	defer cleanup()

	ctx := context.Background()

	t.Run("CreateAndGet", func(t *testing.T) {
		createResp, err := client.CreateNotification(ctx, &notifyv1.CreateNotificationRequest{
			Type:      "email",
			Recipient: "test@example.com",
			Subject:   "Test Notification",
			Content:   "This is a test notification",
		})
		if err != nil {
			t.Fatalf("CreateNotification failed: %v", err)
		}
		if createResp.Notification.Id == 0 {
			t.Fatal("expected non-zero notification ID")
		}

		getResp, err := client.GetNotification(ctx, &notifyv1.GetNotificationRequest{
			Id: createResp.Notification.Id,
		})
		if err != nil {
			t.Fatalf("GetNotification failed: %v", err)
		}
		if getResp.Notification.Subject != "Test Notification" {
			t.Fatalf("expected subject 'Test Notification', got '%s'", getResp.Notification.Subject)
		}
	})

	t.Run("ListAndFilter", func(t *testing.T) {
		_, _ = client.CreateNotification(ctx, &notifyv1.CreateNotificationRequest{
			Type: "sms", Recipient: "+1234567890", Subject: "SMS Test", Content: "SMS content",
		})

		resp, err := client.ListNotifications(ctx, &notifyv1.ListNotificationsRequest{
			Page:     1,
			PageSize: 10,
			Type:     "email",
		})
		if err != nil {
			t.Fatalf("ListNotifications failed: %v", err)
		}
		if resp.Total < 1 {
			t.Fatalf("expected at least 1 email notification, got %d", resp.Total)
		}
	})

	t.Run("UpdateStatus", func(t *testing.T) {
		createResp, _ := client.CreateNotification(ctx, &notifyv1.CreateNotificationRequest{
			Type: "webhook", Recipient: "http://example.com", Subject: "Webhook", Content: "{}",
		})

		_, err := client.UpdateNotificationStatus(ctx, &notifyv1.UpdateNotificationStatusRequest{
			Id:     createResp.Notification.Id,
			Status: "sent",
		})
		if err != nil {
			t.Fatalf("UpdateNotificationStatus failed: %v", err)
		}

		getResp, _ := client.GetNotification(ctx, &notifyv1.GetNotificationRequest{
			Id: createResp.Notification.Id,
		})
		if getResp.Notification.Status != "sent" {
			t.Fatalf("expected status 'sent', got '%s'", getResp.Notification.Status)
		}
	})
}
