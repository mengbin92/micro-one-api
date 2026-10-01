package biz

import (
	"context"
	"errors"
	"micro-one-api/domain/authorization"
	"time"
)

var (
	ErrHealthCheckNotFound = errors.New("health check not found")
	ErrAlertRuleNotFound   = errors.New("alert rule not found")
	ErrInvalidAlertRule    = errors.New("invalid alert rule")
)

const (
	HealthStatusHealthy   = "healthy"
	HealthStatusUnhealthy = "unhealthy"
	HealthStatusUnknown   = "unknown"
)

// HealthCheck represents a service health check result.
type HealthCheck struct {
	ID           int64
	ServiceName  string
	Status       string
	ResponseTime int64 // milliseconds
	CheckedAt    time.Time
}

// AlertRule defines an alerting rule.
type AlertRule struct {
	ID          int64
	Name        string
	ServiceName string
	Metric      string
	Threshold   float64
	Operator    string // gt, lt, eq, gte, lte
	Duration    int    // seconds
	Enabled     bool
	CreatedAt   time.Time
}

// MonitorRepo is the repository interface for monitor persistence.
type MonitorRepo interface {
	// Health checks
	SaveHealthCheck(ctx context.Context, check *HealthCheck) error
	ListHealthChecks(ctx context.Context, serviceName string, page, pageSize int32) ([]*HealthCheck, int64, error)
	GetLatestHealthCheck(ctx context.Context, serviceName string) (*HealthCheck, error)

	// Alert rules
	CreateAlertRule(ctx context.Context, rule *AlertRule) error
	GetAlertRule(ctx context.Context, id int64) (*AlertRule, error)
	ListAlertRules(ctx context.Context, page, pageSize int32) ([]*AlertRule, int64, error)
	UpdateAlertRule(ctx context.Context, rule *AlertRule) error
	DeleteAlertRule(ctx context.Context, id int64) error
}

// MonitorUsecase implements business logic for monitor-worker.
type MonitorUsecase struct {
	authorization authorization.Resolver
	repo          MonitorRepo
}

func NewMonitorUsecase(repo MonitorRepo) *MonitorUsecase {
	return &MonitorUsecase{repo: repo}
}

func (uc *MonitorUsecase) RecordHealthCheck(ctx context.Context, serviceName, status string, responseTime int64) error {
	if err := uc.authorizeSystem(ctx, "/api.monitor.v1.MonitorService/SaveHealthCheck", "monitor.health.service", "monitor.health.service.read"); err != nil {
		return err
	}
	check := &HealthCheck{
		ServiceName:  serviceName,
		Status:       status,
		ResponseTime: responseTime,
		CheckedAt:    time.Now(),
	}
	return uc.repo.SaveHealthCheck(ctx, check)
}

func (uc *MonitorUsecase) GetLatestHealth(ctx context.Context, serviceName string) (*HealthCheck, error) {
	var err error
	ctx, err = uc.authorize(ctx, "monitor.health.service", "monitor.health.service.read")
	if err != nil {
		return nil, err
	}
	return uc.repo.GetLatestHealthCheck(ctx, serviceName)
}

func (uc *MonitorUsecase) ListHealthChecks(ctx context.Context, serviceName string, page, pageSize int32) ([]*HealthCheck, int64, error) {
	var err error
	ctx, err = uc.authorize(ctx, "monitor.health.service", "monitor.health.service.read")
	if err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return uc.repo.ListHealthChecks(ctx, serviceName, page, pageSize)
}

func (uc *MonitorUsecase) CreateAlertRule(ctx context.Context, rule *AlertRule) error {
	var err error
	ctx, err = uc.authorize(ctx, "monitor.alert_rules", "monitor.alert_rule.create")
	if err != nil {
		return err
	}
	if err := authorization.Require(ctx, "monitor.alert_rule.create", authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
		return err
	}
	if rule.Name == "" || rule.ServiceName == "" || rule.Metric == "" {
		return ErrInvalidAlertRule
	}
	rule.CreatedAt = time.Now()
	return uc.repo.CreateAlertRule(ctx, rule)
}

func (uc *MonitorUsecase) GetAlertRule(ctx context.Context, id int64) (*AlertRule, error) {
	var err error
	ctx, err = uc.authorize(ctx, "monitor.alert_rules", "monitor.alert_rule.read")
	if err != nil {
		return nil, err
	}
	if err := authorization.Require(ctx, "monitor.alert_rule.read", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: id}); err != nil {
		return nil, err
	}
	return uc.repo.GetAlertRule(ctx, id)
}

func (uc *MonitorUsecase) ListAlertRules(ctx context.Context, page, pageSize int32) ([]*AlertRule, int64, error) {
	var err error
	ctx, err = uc.authorize(ctx, "monitor.alert_rules", "monitor.alert_rule.list")
	if err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return uc.repo.ListAlertRules(ctx, page, pageSize)
}

func (uc *MonitorUsecase) UpdateAlertRule(ctx context.Context, rule *AlertRule) error {
	var err error
	ctx, err = uc.authorize(ctx, "monitor.alert_rules", "monitor.alert_rule.update")
	if err != nil {
		return err
	}
	if err := authorization.Require(ctx, "monitor.alert_rule.update", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: rule.ID}); err != nil {
		return err
	}
	return uc.repo.UpdateAlertRule(ctx, rule)
}

func (uc *MonitorUsecase) DeleteAlertRule(ctx context.Context, id int64) error {
	var err error
	ctx, err = uc.authorize(ctx, "monitor.alert_rules", "monitor.alert_rule.delete")
	if err != nil {
		return err
	}
	if err := authorization.Require(ctx, "monitor.alert_rule.delete", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: id}); err != nil {
		return err
	}
	return uc.repo.DeleteAlertRule(ctx, id)
}
