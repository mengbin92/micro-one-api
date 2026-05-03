package service

import (
	"context"
	"net/http"
	"strconv"

	"github.com/bytedance/sonic"

	monitorv1 "micro-one-api/api/monitor/v1"
	"micro-one-api/internal/monitor/biz"
)

// MonitorService is the transport layer entry for monitor-worker.
type MonitorService struct {
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
		return nil, err
	}
	return &monitorv1.CreateAlertRuleResponse{
		Rule: &monitorv1.AlertRuleItem{
			Id:          rule.ID,
			Name:        rule.Name,
			ServiceName: rule.ServiceName,
			Metric:      rule.Metric,
			Threshold:   rule.Threshold,
			Operator:    rule.Operator,
			Duration:    int32(rule.Duration),
			Enabled:     rule.Enabled,
			CreatedAt:   rule.CreatedAt.Unix(),
		},
	}, nil
}

func (s *MonitorService) ListAlertRules(ctx context.Context, req *monitorv1.ListAlertRulesRequest) (*monitorv1.ListAlertRulesResponse, error) {
	rules, total, err := s.uc.ListAlertRules(ctx, req.Page, req.PageSize)
	if err != nil {
		return nil, err
	}
	items := make([]*monitorv1.AlertRuleItem, len(rules))
	for i, r := range rules {
		items[i] = &monitorv1.AlertRuleItem{
			Id:          r.ID,
			Name:        r.Name,
			ServiceName: r.ServiceName,
			Metric:      r.Metric,
			Threshold:   r.Threshold,
			Operator:    r.Operator,
			Duration:    int32(r.Duration),
			Enabled:     r.Enabled,
			CreatedAt:   r.CreatedAt.Unix(),
		}
	}
	return &monitorv1.ListAlertRulesResponse{Items: items, Total: total}, nil
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
	if err := sonic.ConfigStd.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.ServiceName == "" {
		writeError(w, http.StatusBadRequest, "service_name is required")
		return
	}
	if err := s.uc.RecordHealthCheck(r.Context(), body.ServiceName, body.Status, body.ResponseTime); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]interface{}, 0, len(checks))
	for _, c := range checks {
		items = append(items, map[string]interface{}{
			"id": c.ID, "service_name": c.ServiceName, "status": c.Status,
			"response_time": c.ResponseTime, "checked_at": c.CheckedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": items, "total": total})
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]interface{}, 0, len(rules))
	for _, rule := range rules {
		items = append(items, alertRuleToMap(rule))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": items, "total": total})
}

func (s *MonitorService) HandleCreateAlertRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Name        string  `json:"name"`
		ServiceName string  `json:"service_name"`
		Metric      string  `json:"metric"`
		Threshold   float64 `json:"threshold"`
		Operator    string  `json:"operator"`
		Duration    int     `json:"duration"`
		Enabled     bool    `json:"enabled"`
	}
	if err := sonic.ConfigStd.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rule := &biz.AlertRule{
		Name: body.Name, ServiceName: body.ServiceName, Metric: body.Metric,
		Threshold: body.Threshold, Operator: body.Operator, Duration: body.Duration, Enabled: body.Enabled,
	}
	if err := s.uc.CreateAlertRule(r.Context(), rule); err != nil {
		if err == biz.ErrInvalidAlertRule {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, alertRuleToMap(rule))
}

func alertRuleToMap(rule *biz.AlertRule) map[string]interface{} {
	return map[string]interface{}{
		"id": rule.ID, "name": rule.Name, "service_name": rule.ServiceName,
		"metric": rule.Metric, "threshold": rule.Threshold, "operator": rule.Operator,
		"duration": rule.Duration, "enabled": rule.Enabled, "created_at": rule.CreatedAt,
	}
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	sonic.ConfigStd.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	sonic.ConfigStd.NewEncoder(w).Encode(map[string]interface{}{"error": message})
}
