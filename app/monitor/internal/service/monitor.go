package service

import (
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/authz"
	"net/http"
	"strconv"
	"strings"

	"micro-one-api/pkg/jsonx"

	monitorv1 "micro-one-api/api/monitor/v1"
	"micro-one-api/app/monitor/internal/biz"
	"micro-one-api/pkg/safecast"
)

// MonitorService is the transport layer entry for monitor-worker.
type MonitorService struct {
	ownerAuthorization *authz.Client
	monitorv1.UnimplementedMonitorServiceServer
	uc *biz.MonitorUsecase
}

func NewMonitorService(uc *biz.MonitorUsecase) *MonitorService {
	return &MonitorService{uc: uc}
}

// gRPC interface implementation

func (s *MonitorService) SaveHealthCheck(ctx context.Context, req *monitorv1.SaveHealthCheckRequest) (*monitorv1.SaveHealthCheckResponse, error) {
	if err := s.uc.RecordHealthCheck(ctx, req.ServiceName, req.Status, req.ResponseTime); err != nil {
		return nil, err
	}
	return &monitorv1.SaveHealthCheckResponse{Success: true}, nil
}

func (s *MonitorService) ListHealthChecks(ctx context.Context, req *monitorv1.ListHealthChecksRequest) (*monitorv1.ListHealthChecksResponse, error) {
	checks, total, err := s.uc.ListHealthChecks(ctx, req.ServiceName, req.Page, req.PageSize)
	if err != nil {
		return nil, err
	}
	items := make([]*monitorv1.HealthCheckItem, len(checks))
	for i, c := range checks {
		items[i] = &monitorv1.HealthCheckItem{
			Id:           c.ID,
			ServiceName:  c.ServiceName,
			Status:       c.Status,
			ResponseTime: c.ResponseTime,
			CheckedAt:    c.CheckedAt.Unix(),
		}
	}
	return &monitorv1.ListHealthChecksResponse{Items: items, Total: total}, nil
}

func (s *MonitorService) GetLatestHealthCheck(ctx context.Context, req *monitorv1.GetLatestHealthCheckRequest) (*monitorv1.GetLatestHealthCheckResponse, error) {
	c, err := s.uc.GetLatestHealth(ctx, req.ServiceName)
	if err != nil {
		return nil, err
	}
	return &monitorv1.GetLatestHealthCheckResponse{
		Check: &monitorv1.HealthCheckItem{
			Id:           c.ID,
			ServiceName:  c.ServiceName,
			Status:       c.Status,
			ResponseTime: c.ResponseTime,
			CheckedAt:    c.CheckedAt.Unix(),
		},
	}, nil
}

func (s *MonitorService) CreateAlertRule(ctx context.Context, req *monitorv1.CreateAlertRuleRequest) (*monitorv1.CreateAlertRuleResponse, error) {
	ctx = authorization.WithExpectedResourceRevision(ctx, req.ExpectedRevision)
	if req.Reason != "" {
		ctx = authorization.WithWriteReason(ctx, req.Reason)
	}
	rule := &biz.AlertRule{
		Name:        req.Name,
		ServiceName: req.ServiceName,
		Metric:      req.Metric,
		Threshold:   req.Threshold,
		Operator:    req.Operator,
		Duration:    int(req.Duration),
		Enabled:     req.Enabled,
	}
	if err := s.uc.CreateAlertRule(ctx, rule); err != nil {
		return nil, monitorMutationError(err)
	}
	item, err := alertRuleToProto(rule)
	if err != nil {
		return nil, monitorMutationError(err)
	}
	return &monitorv1.CreateAlertRuleResponse{Rule: item}, nil
}

func (s *MonitorService) GetAlertRule(ctx context.Context, req *monitorv1.GetAlertRuleRequest) (*monitorv1.GetAlertRuleResponse, error) {
	rule, err := s.uc.GetAlertRule(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	item, err := alertRuleToProto(rule)
	if err != nil {
		return nil, err
	}
	return &monitorv1.GetAlertRuleResponse{Rule: item}, nil
}

func (s *MonitorService) UpdateAlertRule(ctx context.Context, req *monitorv1.UpdateAlertRuleRequest) (*monitorv1.UpdateAlertRuleResponse, error) {
	ctx = authorization.WithExpectedResourceRevision(ctx, req.ExpectedRevision)
	if req.Reason != "" {
		ctx = authorization.WithWriteReason(ctx, req.Reason)
	}
	rule := &biz.AlertRule{
		ID:          req.Id,
		Name:        req.Name,
		ServiceName: req.ServiceName,
		Metric:      req.Metric,
		Threshold:   req.Threshold,
		Operator:    req.Operator,
		Duration:    int(req.Duration),
		Enabled:     req.Enabled,
	}
	if err := s.uc.UpdateAlertRule(ctx, rule); err != nil {
		return nil, monitorMutationError(err)
	}
	return &monitorv1.UpdateAlertRuleResponse{Success: true}, nil
}

func (s *MonitorService) DeleteAlertRule(ctx context.Context, req *monitorv1.DeleteAlertRuleRequest) (*monitorv1.DeleteAlertRuleResponse, error) {
	ctx = authorization.WithExpectedResourceRevision(ctx, req.ExpectedRevision)
	if req.Reason != "" {
		ctx = authorization.WithWriteReason(ctx, req.Reason)
	}
	if err := s.uc.DeleteAlertRule(ctx, req.Id); err != nil {
		return nil, monitorMutationError(err)
	}
	return &monitorv1.DeleteAlertRuleResponse{Success: true}, nil
}

func (s *MonitorService) ListAlertRules(ctx context.Context, req *monitorv1.ListAlertRulesRequest) (*monitorv1.ListAlertRulesResponse, error) {
	rules, total, err := s.uc.ListAlertRules(ctx, req.Page, req.PageSize)
	if err != nil {
		return nil, err
	}
	items := make([]*monitorv1.AlertRuleItem, len(rules))
	for i, r := range rules {
		item, err := alertRuleToProto(r)
		if err != nil {
			return nil, err
		}
		items[i] = item
	}
	return &monitorv1.ListAlertRulesResponse{Items: items, Total: total}, nil
}

func alertRuleToProto(rule *biz.AlertRule) (*monitorv1.AlertRuleItem, error) {
	duration, err := safecast.IntToInt32(rule.Duration)
	if err != nil {
		return nil, err
	}
	return &monitorv1.AlertRuleItem{
		Id:          rule.ID,
		Revision:    rule.Revision,
		Name:        rule.Name,
		ServiceName: rule.ServiceName,
		Metric:      rule.Metric,
		Threshold:   rule.Threshold,
		Operator:    rule.Operator,
		Duration:    duration,
		Enabled:     rule.Enabled,
		CreatedAt:   rule.CreatedAt.Unix(),
	}, nil
}

// HTTP handler implementations

func (s *MonitorService) HandleRecordHealthCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		ServiceName  string `json:"service_name"`
		Status       string `json:"status"`
		ResponseTime int64  `json:"response_time"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.ServiceName == "" {
		writeError(w, http.StatusBadRequest, "service_name is required")
		return
	}
	if err := s.uc.RecordHealthCheck(r.Context(), body.ServiceName, body.Status, body.ResponseTime); err != nil {
		writeMonitorError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

func (s *MonitorService) HandleListHealthChecks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	q := r.URL.Query()
	serviceName := q.Get("service_name")
	page, _ := strconv.ParseInt(q.Get("page"), 10, 32)
	pageSize, _ := strconv.ParseInt(q.Get("page_size"), 10, 32)
	checks, total, err := s.uc.ListHealthChecks(r.Context(), serviceName, int32(page), int32(pageSize))
	if err != nil {
		writeMonitorError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(checks))
	for _, c := range checks {
		items = append(items, map[string]any{
			"id": c.ID, "service_name": c.ServiceName, "status": c.Status,
			"response_time": c.ResponseTime, "checked_at": c.CheckedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *MonitorService) HandleListAlertRules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	q := r.URL.Query()
	page, _ := strconv.ParseInt(q.Get("page"), 10, 32)
	pageSize, _ := strconv.ParseInt(q.Get("page_size"), 10, 32)
	rules, total, err := s.uc.ListAlertRules(r.Context(), int32(page), int32(pageSize))
	if err != nil {
		writeMonitorError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		items = append(items, alertRuleToMap(rule))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *MonitorService) HandleCreateAlertRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Name             string  `json:"name"`
		ServiceName      string  `json:"service_name"`
		Metric           string  `json:"metric"`
		Threshold        float64 `json:"threshold"`
		Operator         string  `json:"operator"`
		Duration         int     `json:"duration"`
		Enabled          bool    `json:"enabled"`
		ExpectedRevision string  `json:"expected_revision"`
		Reason           string  `json:"reason"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rule := &biz.AlertRule{
		Name: body.Name, ServiceName: body.ServiceName, Metric: body.Metric,
		Threshold: body.Threshold, Operator: body.Operator, Duration: body.Duration, Enabled: body.Enabled,
	}
	ctx, err := monitorMutationContext(r.Context(), body.ExpectedRevision, body.Reason)
	if err != nil {
		writeMonitorError(w, err)
		return
	}
	if err := s.uc.CreateAlertRule(ctx, rule); err != nil {
		if err == biz.ErrInvalidAlertRule {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeMonitorError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, alertRuleToMap(rule))
}

func (s *MonitorService) HandleGetAlertRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := extractIDFromPath(r.URL.Path, "/v1/alert-rules/")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert rule id")
		return
	}
	rule, err := s.uc.GetAlertRule(r.Context(), id)
	if err != nil {
		writeMonitorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, alertRuleToMap(rule))
}

func (s *MonitorService) HandleUpdateAlertRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := extractIDFromPath(r.URL.Path, "/v1/alert-rules/")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert rule id")
		return
	}
	var body struct {
		Name             string  `json:"name"`
		ServiceName      string  `json:"service_name"`
		Metric           string  `json:"metric"`
		Threshold        float64 `json:"threshold"`
		Operator         string  `json:"operator"`
		Duration         int     `json:"duration"`
		Enabled          *bool   `json:"enabled"`
		ExpectedRevision string  `json:"expected_revision"`
		Reason           string  `json:"reason"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	enabled := false
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	rule := &biz.AlertRule{
		ID: id, Name: body.Name, ServiceName: body.ServiceName, Metric: body.Metric,
		Threshold: body.Threshold, Operator: body.Operator, Duration: body.Duration, Enabled: enabled,
	}
	ctx, err := monitorMutationContext(r.Context(), body.ExpectedRevision, body.Reason)
	if err != nil {
		writeMonitorError(w, err)
		return
	}
	if err := s.uc.UpdateAlertRule(ctx, rule); err != nil {
		writeMonitorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *MonitorService) HandleDeleteAlertRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := extractIDFromPath(r.URL.Path, "/v1/alert-rules/")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert rule id")
		return
	}
	ctx, err := monitorMutationContext(r.Context(), r.URL.Query().Get("expected_revision"), r.URL.Query().Get("reason"))
	if err != nil {
		writeMonitorError(w, err)
		return
	}
	if err := s.uc.DeleteAlertRule(ctx, id); err != nil {
		writeMonitorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func extractIDFromPath(path, prefix string) (int64, error) {
	idStr := strings.TrimPrefix(path, prefix)
	idStr = strings.TrimRight(idStr, "/")
	return strconv.ParseInt(idStr, 10, 64)
}

func alertRuleToMap(rule *biz.AlertRule) map[string]any {
	return map[string]any{
		"id": rule.ID, "name": rule.Name, "service_name": rule.ServiceName,
		"metric": rule.Metric, "threshold": rule.Threshold, "operator": rule.Operator,
		"duration": rule.Duration, "enabled": rule.Enabled, "created_at": rule.CreatedAt, "revision": strconv.FormatUint(rule.Revision, 10),
	}
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = jsonx.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = jsonx.NewEncoder(w).Encode(map[string]any{"error": message})
}

func (s *MonitorService) SetAuthorization(r authorization.Resolver) {
	s.uc.SetAuthorization(r)
	s.ownerAuthorization, _ = r.(*authz.Client)
}
func (s *MonitorService) OwnerAuthorizationClient() *authz.Client { return s.ownerAuthorization }

// Preserve domain HTTP errors while retaining owner authorization statuses.
func writeMonitorError(w http.ResponseWriter, err error) {
	err = monitorMutationError(err)
	switch err {
	case biz.ErrAlertRuleNotFound:
		writeError(w, http.StatusNotFound, err.Error())
	case biz.ErrInvalidAlertRule:
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		authz.WriteHTTPError(w, err)
	}
}

func (s *MonitorService) HandleLatestHealthCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name := r.URL.Query().Get("service_name")
	if name == "" {
		writeError(w, 400, "service_name is required")
		return
	}
	check, err := s.uc.GetLatestHealth(r.Context(), name)
	if err != nil {
		writeMonitorError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": check.ID, "service_name": check.ServiceName, "status": check.Status, "response_time": check.ResponseTime, "checked_at": check.CheckedAt.Unix()})
}

func monitorMutationContext(ctx context.Context, expected, reason string) (context.Context, error) {
	if expected == "" {
		expected = "0"
	}
	revision, err := strconv.ParseUint(expected, 10, 64)
	if err != nil {
		return ctx, status.Error(codes.InvalidArgument, "invalid expected_revision")
	}
	ctx = authorization.WithExpectedResourceRevision(ctx, revision)
	if reason != "" {
		ctx = authorization.WithWriteReason(ctx, reason)
	}
	return ctx, nil
}
func monitorMutationError(err error) error {
	switch {
	case errors.Is(err, biz.ErrAlertRuleRevisionConflict):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, biz.ErrAlertRuleMutationRequired):
		return status.Error(codes.InvalidArgument, err.Error())
	}
	return err
}
