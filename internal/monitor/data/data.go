package data

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"micro-one-api/internal/monitor/biz"
	"micro-one-api/internal/pkg/xdb"

	"gorm.io/gorm"
)

type Repository struct {
	db       *gorm.DB
	mu       sync.RWMutex
	checks   map[int64]*biz.HealthCheck
	checkSeq int64
	rules    map[int64]*biz.AlertRule
	ruleSeq  int64
}

type healthCheckModel struct {
	ID           int64  `gorm:"column:id;primaryKey;autoIncrement"`
	ServiceName  string `gorm:"column:service_name;index"`
	Status       string `gorm:"column:status"`
	ResponseTime int64  `gorm:"column:response_time"`
	CheckedAt    int64  `gorm:"column:checked_at;index"`
}

func (healthCheckModel) TableName() string { return "health_checks" }

type alertRuleModel struct {
	ID          int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Name        string `gorm:"column:name"`
	ServiceName string `gorm:"column:service_name"`
	Metric      string `gorm:"column:metric"`
	Threshold   float64 `gorm:"column:threshold"`
	Operator    string `gorm:"column:operator"`
	Duration    int    `gorm:"column:duration"`
	Enabled     bool   `gorm:"column:enabled"`
	CreatedAt   int64  `gorm:"column:created_at"`
}

func (alertRuleModel) TableName() string { return "alert_rules" }

func NewRepositoryFromEnv(driver string, dsn ...string) (*Repository, error) {
	var dbDSN string
	if len(dsn) > 0 && dsn[0] != "" {
		dbDSN = dsn[0]
	} else {
		dbDSN = os.Getenv("MONITOR_SQL_DSN")
		if dbDSN == "" {
			dbDSN = os.Getenv("SQL_DSN")
		}
	}
	if dbDSN == "" {
		return newMemoryRepository(), nil
	}
	db, err := xdb.Open(xdb.DatabaseConfig{Driver: xdb.NormalizeDriver(driver, dbDSN), DSN: dbDSN})
	if err != nil {
		return nil, err
	}
	return &Repository{db: db}, nil
}

func newMemoryRepository() *Repository {
	return &Repository{
		checks: map[int64]*biz.HealthCheck{
			1: {
				ID:           1,
				ServiceName:  "identity-service",
				Status:       biz.HealthStatusHealthy,
				ResponseTime: 12,
				CheckedAt:    time.Now(),
			},
		},
		checkSeq: 1,
		rules:    map[int64]*biz.AlertRule{},
		ruleSeq:  0,
	}
}

// Health check methods

func (r *Repository) SaveHealthCheck(ctx context.Context, check *biz.HealthCheck) error {
	if r.db != nil {
		return r.saveHealthCheckDB(ctx, check)
	}
	return r.saveHealthCheckMemory(check)
}

func (r *Repository) ListHealthChecks(ctx context.Context, serviceName string, page, pageSize int32) ([]*biz.HealthCheck, int64, error) {
	if r.db != nil {
		return r.listHealthChecksDB(ctx, serviceName, page, pageSize)
	}
	return r.listHealthChecksMemory(serviceName, page, pageSize)
}

func (r *Repository) GetLatestHealthCheck(ctx context.Context, serviceName string) (*biz.HealthCheck, error) {
	if r.db != nil {
		return r.getLatestHealthCheckDB(ctx, serviceName)
	}
	return r.getLatestHealthCheckMemory(serviceName)
}

// Alert rule methods

func (r *Repository) CreateAlertRule(ctx context.Context, rule *biz.AlertRule) error {
	if r.db != nil {
		return r.createAlertRuleDB(ctx, rule)
	}
	return r.createAlertRuleMemory(rule)
}

func (r *Repository) GetAlertRule(ctx context.Context, id int64) (*biz.AlertRule, error) {
	if r.db != nil {
		return r.getAlertRuleDB(ctx, id)
	}
	return r.getAlertRuleMemory(id)
}

func (r *Repository) ListAlertRules(ctx context.Context, page, pageSize int32) ([]*biz.AlertRule, int64, error) {
	if r.db != nil {
		return r.listAlertRulesDB(ctx, page, pageSize)
	}
	return r.listAlertRulesMemory(page, pageSize)
}

func (r *Repository) UpdateAlertRule(ctx context.Context, rule *biz.AlertRule) error {
	if r.db != nil {
		return r.updateAlertRuleDB(ctx, rule)
	}
	return r.updateAlertRuleMemory(rule)
}

func (r *Repository) DeleteAlertRule(ctx context.Context, id int64) error {
	if r.db != nil {
		return r.deleteAlertRuleDB(ctx, id)
	}
	return r.deleteAlertRuleMemory(id)
}

// DB implementations - health checks

func (r *Repository) saveHealthCheckDB(ctx context.Context, check *biz.HealthCheck) error {
	m := healthCheckModel{
		ServiceName:  check.ServiceName,
		Status:       check.Status,
		ResponseTime: check.ResponseTime,
		CheckedAt:    check.CheckedAt.Unix(),
	}
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return err
	}
	check.ID = m.ID
	return nil
}

func (r *Repository) listHealthChecksDB(ctx context.Context, serviceName string, page, pageSize int32) ([]*biz.HealthCheck, int64, error) {
	query := r.db.WithContext(ctx).Model(&healthCheckModel{})
	if serviceName != "" {
		query = query.Where("service_name = ?", serviceName)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	var models []healthCheckModel
	if err := query.Offset(int(offset)).Limit(int(pageSize)).Order("id DESC").Find(&models).Error; err != nil {
		return nil, 0, err
	}
	checks := make([]*biz.HealthCheck, len(models))
	for i, m := range models {
		checks[i] = &biz.HealthCheck{
			ID:           m.ID,
			ServiceName:  m.ServiceName,
			Status:       m.Status,
			ResponseTime: m.ResponseTime,
			CheckedAt:    time.Unix(m.CheckedAt, 0),
		}
	}
	return checks, total, nil
}

func (r *Repository) getLatestHealthCheckDB(ctx context.Context, serviceName string) (*biz.HealthCheck, error) {
	var m healthCheckModel
	if err := r.db.WithContext(ctx).Where("service_name = ?", serviceName).Order("id DESC").First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrHealthCheckNotFound
		}
		return nil, err
	}
	return &biz.HealthCheck{
		ID:           m.ID,
		ServiceName:  m.ServiceName,
		Status:       m.Status,
		ResponseTime: m.ResponseTime,
		CheckedAt:    time.Unix(m.CheckedAt, 0),
	}, nil
}

// DB implementations - alert rules

func (r *Repository) createAlertRuleDB(ctx context.Context, rule *biz.AlertRule) error {
	m := alertRuleModel{
		Name:        rule.Name,
		ServiceName: rule.ServiceName,
		Metric:      rule.Metric,
		Threshold:   rule.Threshold,
		Operator:    rule.Operator,
		Duration:    rule.Duration,
		Enabled:     rule.Enabled,
		CreatedAt:   rule.CreatedAt.Unix(),
	}
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return err
	}
	rule.ID = m.ID
	return nil
}

func (r *Repository) getAlertRuleDB(ctx context.Context, id int64) (*biz.AlertRule, error) {
	var m alertRuleModel
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrAlertRuleNotFound
		}
		return nil, err
	}
	return &biz.AlertRule{
		ID:          m.ID,
		Name:        m.Name,
		ServiceName: m.ServiceName,
		Metric:      m.Metric,
		Threshold:   m.Threshold,
		Operator:    m.Operator,
		Duration:    m.Duration,
		Enabled:     m.Enabled,
		CreatedAt:   time.Unix(m.CreatedAt, 0),
	}, nil
}

func (r *Repository) listAlertRulesDB(ctx context.Context, page, pageSize int32) ([]*biz.AlertRule, int64, error) {
	query := r.db.WithContext(ctx).Model(&alertRuleModel{})
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	var models []alertRuleModel
	if err := query.Offset(int(offset)).Limit(int(pageSize)).Order("id DESC").Find(&models).Error; err != nil {
		return nil, 0, err
	}
	rules := make([]*biz.AlertRule, len(models))
	for i, m := range models {
		rules[i] = &biz.AlertRule{
			ID:          m.ID,
			Name:        m.Name,
			ServiceName: m.ServiceName,
			Metric:      m.Metric,
			Threshold:   m.Threshold,
			Operator:    m.Operator,
			Duration:    m.Duration,
			Enabled:     m.Enabled,
			CreatedAt:   time.Unix(m.CreatedAt, 0),
		}
	}
	return rules, total, nil
}

func (r *Repository) updateAlertRuleDB(ctx context.Context, rule *biz.AlertRule) error {
	return r.db.WithContext(ctx).Model(&alertRuleModel{}).Where("id = ?", rule.ID).Updates(map[string]interface{}{
		"name":         rule.Name,
		"service_name": rule.ServiceName,
		"metric":       rule.Metric,
		"threshold":    rule.Threshold,
		"operator":     rule.Operator,
		"duration":     rule.Duration,
		"enabled":      rule.Enabled,
	}).Error
}

func (r *Repository) deleteAlertRuleDB(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&alertRuleModel{}).Error
}

// Memory implementations - health checks

func (r *Repository) saveHealthCheckMemory(check *biz.HealthCheck) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checkSeq++
	check.ID = r.checkSeq
	r.checks[check.ID] = check
	return nil
}

func (r *Repository) listHealthChecksMemory(serviceName string, page, pageSize int32) ([]*biz.HealthCheck, int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var all []*biz.HealthCheck
	for _, check := range r.checks {
		if serviceName != "" && check.ServiceName != serviceName {
			continue
		}
		cloned := *check
		all = append(all, &cloned)
	}
	total := int64(len(all))
	start := int((page - 1) * pageSize)
	if start >= len(all) {
		return nil, total, nil
	}
	end := start + int(pageSize)
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], total, nil
}

func (r *Repository) getLatestHealthCheckMemory(serviceName string) (*biz.HealthCheck, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var latest *biz.HealthCheck
	for _, check := range r.checks {
		if serviceName != "" && check.ServiceName != serviceName {
			continue
		}
		if latest == nil || check.ID > latest.ID {
			latest = check
		}
	}
	if latest == nil {
		return nil, biz.ErrHealthCheckNotFound
	}
	cloned := *latest
	return &cloned, nil
}

// Memory implementations - alert rules

func (r *Repository) createAlertRuleMemory(rule *biz.AlertRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ruleSeq++
	rule.ID = r.ruleSeq
	r.rules[rule.ID] = rule
	return nil
}

func (r *Repository) getAlertRuleMemory(id int64) (*biz.AlertRule, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rule, ok := r.rules[id]
	if !ok {
		return nil, biz.ErrAlertRuleNotFound
	}
	cloned := *rule
	return &cloned, nil
}

func (r *Repository) listAlertRulesMemory(page, pageSize int32) ([]*biz.AlertRule, int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var all []*biz.AlertRule
	for _, rule := range r.rules {
		cloned := *rule
		all = append(all, &cloned)
	}
	total := int64(len(all))
	start := int((page - 1) * pageSize)
	if start >= len(all) {
		return nil, total, nil
	}
	end := start + int(pageSize)
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], total, nil
}

func (r *Repository) updateAlertRuleMemory(rule *biz.AlertRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.rules[rule.ID]; !ok {
		return biz.ErrAlertRuleNotFound
	}
	r.rules[rule.ID] = rule
	return nil
}

func (r *Repository) deleteAlertRuleMemory(id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.rules[id]; !ok {
		return biz.ErrAlertRuleNotFound
	}
	delete(r.rules, id)
	return nil
}
