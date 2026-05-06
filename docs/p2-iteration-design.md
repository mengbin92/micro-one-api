# P2 迭代设计方案

> 基于 `gap-analysis-and-fix-plan.md` 中标记的 P2 待办事项，制定详细实施方案。
> 最后更新：2026/05/06 — 已与实际实现对齐

## 1. 迭代范围

| # | 任务 | 优先级 | 状态 | 依赖 |
|---|------|--------|------|------|
| 1 | 链路追踪 Jaeger 集成 | P2-High | ✅ 已完成 | 无 |
| 2 | 对账任务定时调度 | P2-Medium | ✅ 已完成 | 无 |
| 3 | 二期服务集成测试 | P2-Medium | ✅ 已完成 | 无 |

## 2. 任务 1：链路追踪 Jaeger 集成

### 2.1 实现方案

**技术选型**：OpenTelemetry SDK + OTLP HTTP Exporter（Jaeger 兼容）

**架构**：
```
Service → OTel SDK → OTLP HTTP → Jaeger Collector → Jaeger UI
```

**实现文件**：`internal/pkg/xtrace/trace.go`

**核心结构**：
```go
type Config struct {
    Enabled    bool    `yaml:"enabled"`
    Endpoint   string  `yaml:"endpoint"`    // e.g. "http://jaeger:4318/v1/traces"
    Service    string  `yaml:"service"`     // service name
    SampleRate float64 `yaml:"sample_rate"` // 0.0 - 1.0
}

func InitTracer(cfg Config) (func(), error)
```

**实现要点**：
- 使用 `otlptracehttp` exporter（HTTP 协议，非 gRPC）
- `TraceIDRatioBased` 采样器，支持可配采样率
- 默认采样率 1.0（全量采集）
- 返回 shutdown 函数，应用退出时调用
- 保留原有 `TraceIDHeader` 中间件（X-Trace-ID）

**集成方式**：各服务 main.go 中调用
```go
shutdown, err := xtrace.InitTracer(xtrace.Config{
    Endpoint: cfg.Trace.Endpoint,
    Service:  "identity-service",
    Enabled:  cfg.Trace.Enabled,
})
defer shutdown()
```

**配置示例**（添加到各服务 configs/*.yaml）：
```yaml
trace:
  enabled: false           # 开发环境可关闭
  endpoint: "http://jaeger:4318/v1/traces"
  service: "identity-service"
  sample_rate: 1.0
```

**Docker Compose 扩展**（可选，按需添加）：
```yaml
jaeger:
  image: jaegertracing/all-in-one:latest
  ports:
    - "16686:16686"  # UI
    - "4318:4318"    # OTLP HTTP
  environment:
    COLLECTOR_OTLP_ENABLED: true
```

### 2.2 验证标准

- [ ] Jaeger UI 可访问 (http://localhost:16686)
- [ ] 请求链路可在 Jaeger 中查看
- [ ] 跨服务调用链路可追踪（relay → identity → channel → billing）

## 3. 任务 2：对账任务定时调度

### 3.1 实现方案

**技术选型**：使用 `time.Ticker` 实现定时调度（轻量，无需额外依赖）

**架构**：
```
billing-service main.go
  └── ReconciliationJob (time.Ticker)
        └── ReconciliationUsecase.RunReconciliation()
```

**实现文件**：`internal/billing/biz/reconciliation_job.go`

**核心结构**：
```go
type ReconciliationJob struct {
    uc            *ReconciliationUsecase
    checkInterval time.Duration
    stopChan      chan struct{}
}

func NewReconciliationJob(uc *ReconciliationUsecase, checkInterval time.Duration) *ReconciliationJob
func (j *ReconciliationJob) Start(ctx context.Context)
func (j *ReconciliationJob) Stop()
```

**实现要点**：
- 启动时立即执行一次对账
- 之后按 `checkInterval` 周期执行（默认 1 小时）
- 支持 context 取消和 stopChan 双重停止机制
- 日志输出：run_at、expired_cleaned、total_accounts、inconsistencies

**集成方式**：`cmd/billing-service/wire_gen.go` 中启动
```go
ctx, cancel := context.WithCancel(context.Background())
reconJob := biz.NewReconciliationJob(reconUc, 1*time.Hour)
go reconJob.Start(ctx)
// shutdown 时调用 reconJob.Stop() 和 cancel()
```

**选择 time.Ticker 而非 robfig/cron 的原因**：
- 项目已有 `CleanupJob` 使用相同模式，保持一致
- 对账场景只需固定间隔，不需要 cron 表达式的灵活性
- 减少外部依赖

### 3.2 验证标准

- [ ] 对账任务在 billing-service 启动时立即执行一次
- [ ] 之后每小时自动执行
- [ ] 过期 reservation 被正确清理
- [ ] 账户不一致被检测并记录

## 4. 任务 3：二期服务集成测试

### 4.1 实现方案

**实现文件**：`test/integration/phase2_test.go`（单文件，包含所有测试）

**测试范围**：

| 服务 | 测试场景 | 测试函数 |
|------|---------|---------|
| config-service | Set+Get、List、Delete | `TestConfigIntegration` |
| log-service | Ingest+Get、List | `TestLogIntegration` |
| monitor-service | Save+ListHealthChecks、Create+ListAlertRules | `TestMonitorIntegration` |
| notify-service | Create+Get、List+Filter、UpdateStatus | `TestNotifyIntegration` |

**测试架构**：
- 每个服务使用内存 test repo（无外部依赖）
- 每个服务独立 gRPC server，监听不同端口（19010-19013）
- 测试完成后通过 cleanup 函数关闭 server

**test repo 实现**：
- `testConfigRepo` — 实现 `configbiz.ConfigRepo` 接口
- `testLogRepo` — 实现 `logbiz.LogRepo` 接口
- `testMonitorRepo` — 实现 `monitorbiz.MonitorRepo` 接口
- `testNotifyRepo` — 实现 `notifybiz.NotifyRepo` 接口

### 4.2 验证标准

- [x] 4 个服务各有至少 2 个测试场景
- [x] 测试可独立运行，无数据污染
- [x] `go test ./test/integration/...` 全部通过

## 5. 相关提交

| 提交 | 内容 |
|------|------|
| `06fa443` | feat: implement P2 iteration - tracing, reconciliation job, integration tests |
| `5a9478c` | docs: update gap analysis with P2 completion status |
| `3f78762` | docs: update design doc with P2 iteration completion status |
