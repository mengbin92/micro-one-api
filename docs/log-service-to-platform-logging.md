# log-service 降级为 platform/logging 组件 — 具体实施方案

## 0. 前提确认

本方案假设 `log-service` 的职责是**日志采集/格式化/转发**（被动组件），不承担独立的业务查询、分析逻辑。

**动手前请先确认这个假设成立**，检查以下几点：

```bash
# 1. 是否有对外暴露的查询类 API（如按条件检索历史日志）
grep -r "func.*Query\|func.*Search\|func.*List" app/log/service/internal/service/ 2>/dev/null \
  || grep -r "func.*Query\|func.*Search\|func.*List" cmd/log-service/ internal/log-service/ 2>/dev/null

# 2. 是否有独立的存储依赖（ES、Kafka 消费者）
grep -rn "elasticsearch\|kafka" cmd/log-service/ internal/log-service/ 2>/dev/null

# 3. 是否有其他服务通过 gRPC/HTTP 调用它，而非仅仅"引用日志格式"
grep -rn "log.*Client\|LogServiceClient" app/*/internal/ cmd/*/internal/ 2>/dev/null
```

- 如果**只有格式化/输出封装**，没有对外 API、没有独立存储 → **确认降级**，继续本方案。
- 如果**存在查询 API 或独立存储依赖**（比如接了 ES 做日志检索平台）→ **不要降级**，保留为独立服务 `app/log/service`，本方案不适用。

---

## 1. 降级目标

把原来"作为一个独立部署单元运行的 log-service"，变成"一个所有服务都能直接 import 的 Go 库"：

```
迁移前：
  identity-service ──gRPC/HTTP──▶ log-service（独立进程）──▶ 落盘/ES

迁移后：
  identity-service ──import platform/logging──▶ 直接写日志（同进程内）
```

收益：少一次网络调用、少一个需要单独部署运维的进程；代价：所有服务需要重新编译发布（一次性成本）。

---

## 2. 目标目录结构

```
platform/
└── logging/
    ├── logging.go       # 核心 Logger 接口与实现
    ├── config.go        # 日志配置项（level、format、output）
    ├── kratos_adapter.go # 适配 kratos 自带的 log.Logger 接口
    ├── field.go         # 结构化字段辅助函数
    └── logging_test.go
```

---

## 3. 核心代码骨架

### 3.1 platform/logging/config.go

```go
package logging

// Config 日志配置，从各服务的 configs/config.yaml 里读取
type Config struct {
	Level      string `json:"level" yaml:"level"`             // debug/info/warn/error
	Format     string `json:"format" yaml:"format"`           // json/console
	Output     string `json:"output" yaml:"output"`           // stdout/file
	FilePath   string `json:"file_path" yaml:"file_path"`     // Output=file 时生效
	ServiceName string `json:"service_name" yaml:"service_name"` // 用于日志字段标识来源服务
}
```

### 3.2 platform/logging/logging.go

```go
package logging

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger 统一日志接口，业务代码只依赖这个接口，不直接依赖 zap
type Logger interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
	With(fields ...Field) Logger
}

type zapLogger struct {
	l *zap.Logger
}

// New 根据配置创建一个 Logger 实例，替代原来 log-service 的初始化逻辑
func New(cfg Config) (Logger, error) {
	level := parseLevel(cfg.Level)

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "ts"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	var encoder zapcore.Encoder
	if cfg.Format == "console" {
		encoder = zapcore.NewConsoleEncoder(encoderCfg)
	} else {
		encoder = zapcore.NewJSONEncoder(encoderCfg)
	}

	writer := zapcore.AddSync(os.Stdout)
	if cfg.Output == "file" && cfg.FilePath != "" {
		f, err := os.OpenFile(cfg.FilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return nil, err
		}
		writer = zapcore.AddSync(f)
	}

	core := zapcore.NewCore(encoder, writer, level)
	l := zap.New(core).With(zap.String("service", cfg.ServiceName))

	return &zapLogger{l: l}, nil
}

func (z *zapLogger) Debug(msg string, fields ...Field) { z.l.Debug(msg, toZapFields(fields)...) }
func (z *zapLogger) Info(msg string, fields ...Field)  { z.l.Info(msg, toZapFields(fields)...) }
func (z *zapLogger) Warn(msg string, fields ...Field)  { z.l.Warn(msg, toZapFields(fields)...) }
func (z *zapLogger) Error(msg string, fields ...Field) { z.l.Error(msg, toZapFields(fields)...) }

func (z *zapLogger) With(fields ...Field) Logger {
	return &zapLogger{l: z.l.With(toZapFields(fields)...)}
}

func parseLevel(s string) zapcore.Level {
	switch s {
	case "debug":
		return zapcore.DebugLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}
```

### 3.3 platform/logging/field.go

```go
package logging

import "go.uber.org/zap"

// Field 对上层屏蔽 zap 的具体类型，业务代码只 import logging 包
type Field = zap.Field

func String(key, val string) Field { return zap.String(key, val) }
func Int(key string, val int) Field { return zap.Int(key, val) }
func Err(err error) Field { return zap.Error(err) }
func Any(key string, val interface{}) Field { return zap.Any(key, val) }

func toZapFields(fields []Field) []zap.Field {
	return fields
}
```

### 3.4 platform/logging/kratos_adapter.go

kratos 框架自身依赖 `log.Logger` 接口（用于框架内部日志、中间件日志），需要提供一个适配器，让 `platform/logging.Logger` 也能喂给 kratos：

```go
package logging

import (
	kratoslog "github.com/go-kratos/kratos/v2/log"
)

// KratosAdapter 把 platform/logging.Logger 适配成 kratos 框架要求的 log.Logger
type KratosAdapter struct {
	logger Logger
}

func NewKratosAdapter(l Logger) kratoslog.Logger {
	return &KratosAdapter{logger: l}
}

func (k *KratosAdapter) Log(level kratoslog.Level, keyvals ...interface{}) error {
	fields := make([]Field, 0, len(keyvals)/2)
	for i := 0; i+1 < len(keyvals); i += 2 {
		key, _ := keyvals[i].(string)
		fields = append(fields, Any(key, keyvals[i+1]))
	}

	switch level {
	case kratoslog.LevelDebug:
		k.logger.Debug("", fields...)
	case kratoslog.LevelWarn:
		k.logger.Warn("", fields...)
	case kratoslog.LevelError:
		k.logger.Error("", fields...)
	default:
		k.logger.Info("", fields...)
	}
	return nil
}
```

---

## 4. 各服务的接入方式

在每个服务的 `cmd/<service>/main.go`（或 `wire.go` 里的初始化逻辑）里，把原来"调用 log-service gRPC client"的代码替换为直接初始化：

```go
// 迁移前
logClient := logpb.NewLogServiceClient(conn)
logClient.Write(ctx, &logpb.WriteRequest{Level: "info", Msg: "..."})

// 迁移后
logger, err := logging.New(logging.Config{
	Level:       cfg.Log.Level,
	Format:      cfg.Log.Format,
	Output:      cfg.Log.Output,
	ServiceName: "identity-service",
})
if err != nil {
	panic(err)
}
logger.Info("service started", logging.String("addr", cfg.Server.Addr))

// 同时接入 kratos 框架日志
kratosLogger := logging.NewKratosAdapter(logger)
app := kratos.New(
	kratos.Logger(kratosLogger),
	// ...
)
```

各服务的 `configs/config.yaml` 增加日志配置段：

```yaml
log:
  level: info
  format: json
  output: stdout
  # output: file 时才需要下面这行
  # file_path: /var/log/identity-service.log
```

---

## 5. 迁移步骤清单

```bash
# 1. 创建 platform/logging 目录，落地第 3 节的代码
mkdir -p platform/logging
# ...（把上面代码骨架写入对应文件）

# 2. 补充单元测试
```

```go
// platform/logging/logging_test.go
package logging

import "testing"

func TestNew(t *testing.T) {
	l, err := New(Config{Level: "info", Format: "json", Output: "stdout", ServiceName: "test"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	l.Info("hello", String("key", "value"))
}
```

```bash
# 3. 跑通单元测试
go test ./platform/logging/...

# 4. 逐个服务接入（建议顺序：先接一个非核心服务打样，比如 config-service）
#    - 替换 main.go / wire.go 里的日志初始化逻辑（见第 4 节）
#    - 移除该服务里 import log-service client 的代码
#    - 更新该服务 configs/config.yaml，加入 log 配置段
#    - go build 确认编译通过
#    - 本地启动，确认日志正常输出（stdout 或落盘）
#    - 提交 PR

# 5. 重复步骤 4，接入剩余服务
```

按第一批～第四批的迁移顺序（config → monitor/notify → channel/billing → identity → admin/relay）依次接入，与整体大仓迁移节奏保持一致，避免额外开一条迁移线打乱原计划。

---

## 6. 下线 log-service

**所有服务接入完成、验证无误后**再执行下线，不要提前删除：

```bash
# 1. 确认没有任何服务还在调用 log-service 的 gRPC/HTTP 接口
grep -rn "LogServiceClient\|log-service" app/ deployments/ 2>/dev/null

# 2. 从部署配置中移除
git rm -r deployments/docker/log-service.Dockerfile 2>/dev/null
git rm -r deployments/k8s/log-service* 2>/dev/null
git rm -r deployments/helm/log-service 2>/dev/null

# 3. 从 CI 配置中移除对应的 build/deploy job

# 4. 删除原服务代码
git rm -r app/log/service 2>/dev/null || git rm -r cmd/log-service internal/log-service

# 5. 从服务发现/注册中心移除 log-service 的注册记录（如 Nacos/Consul/etcd 里的条目）

# 6. 提交 PR，说明"log-service 已降级为 platform/logging，全部服务已切换完毕"
```

---

## 7. 风险与回滚

| 风险点 | 应对措施 |
|---|---|
| 日志格式变化导致下游日志采集（ELK/Loki）解析失败 | 迁移前先对比新旧日志的 JSON 字段结构，保持 `service`、`level`、`ts`、`msg` 等关键字段名一致 |
| 某服务遗漏未接入，仍在调用已下线的 log-service | 第 6 节第 1 步的 grep 检查作为下线前的强制门禁，CI 里可以加一条自动检查 |
| 高并发场景下同步写日志影响服务性能 | `platform/logging` 内部使用 zap 的异步写入（`zapcore.NewMultiWriteSyncer` + 缓冲）或确认 zap 默认写入方式满足性能要求；必要时保留原有的日志采集管道（filebeat 读取 stdout/文件），而不是让业务代码同步阻塞式远程调用 |
| 需要日志检索能力（原来 log-service 可能间接提供） | 确认检索能力实际由日志平台（如 ELK/Loki）承担，而非 log-service 本身；如果 log-service 确实承担了检索职责，回到第 0 节重新评估是否应该降级 |

---

## 8. 工作量预估

| 阶段 | 内容 | 预估人日 |
|---|---|---|
| platform/logging 开发 + 单测 | 第 3 节代码落地 | 1 人日 |
| 逐服务接入（9 个服务） | 每服务约 0.5 人日 | 4-5 人日 |
| log-service 下线 | 第 6 节步骤 | 0.5 人日 |
| **合计** | | **约 5.5-6.5 人日** |

可以并入之前整体大仓迁移方案的"第一批"和穿插进行的收尾工作里，不需要单独排期。
