# v0.37.1 Relay 安全与流式终止修复

2026-10-29（北京时间）。修复来源为[第一轮审查 §4](round-one-code-review-2026-10-29.md#4-预存量观察项ocr-scan-命中非本轮引入登记待办)。本次为 PATCH：修正已有防护和错误处理，不新增 API、配置、依赖或数据库迁移。

## 1. 修复与红绿回归

| 级别 | 根因与修复 | 反馈环 |
| --- | --- | --- |
| P1 / 高 | SSRF 旁路只在 Relay main 提示，其他 provider 调用服务和直接校验路径静默。统一在 factory、HTTP/SSE 构造、URL 校验、dial、redirect 边界告警，每进程一次；日志未初始化不消耗告警机会。删除 main 重复提示 | `go test ./domain/upstream/provider -run TestProviderSSRFBypassWarning -count=1`：原实现 `warnings = 0, want 1`，修复后通过；子进程隔离七个入口与延迟日志初始化场景 |
| P2 / 中 | 八条非流式路径固定按成功上限读取。共享读取函数按调用方的成功条件选择上限：raw 的 2xx、typed Chat 的 200 使用 128MB，其余限制 1MB；保留 HTTP 状态和 Retry-After。既有流式错误读取仍为 1MB | `go test ./domain/upstream/provider -run 'TestProvider.*Body' -count=1`：429 错误与 typed 201 错误原均读入 2097152 字节，期望 1048576；修复后通过。raw 201 大响应仍完整返回 |
| P3 / 低 | typed Chat 先发 DONE 再结算，错误标记优先级错误、InvalidPayload 被忽略，失败出口缺少 SSE error。统一错误终止与结算顺序 | `go test ./internal/server -run 'TestChatStream|TestIncompleteStreamsNeverCommitOrReplay|TestClientCancellationReachesUpstreamAndFinalizes|TestServiceTier' -count=1`：六个原失败场景转绿，覆盖真实 provider 到 HTTP 的链路及正常用量、取消、写失败 |

SSRF 告警遵循既有 Warn 日志阈值；生产保持 `PROVIDER_DISABLE_SSRF_CHECK` 关闭，日志级别为 info。审计同类 `_DISABLE_` 配置：非测试 Go 的安全旁路只有该共享入口；`REGISTRATION_DISABLED` 用于禁止注册，不是关闭防护。

## 2. 流式结算契约

- 正常顺序为内容交付 → commit → DONE → usage log。日志 RPC 不延迟 DONE。
- 上游错误、解析拒绝、缺少完成标记：取消上游，释放预扣，发送一次脱敏 SSE error，不发 DONE。
- commit 失败或结果不确定：保留 reservation，不 release，不重试；发送脱敏错误并返回 PostForward/stream 中断标记，外层不再追加 JSON。
- 客户端取消或内容写失败：取消上游并释放预扣，不再尝试错误事件。DONE 写失败时内容已交付且结算已提交，保留结算和用量日志。
- 缺失或无效的档位 usage 继续按原契约释放预扣；不恢复估算收费。其他 raw/SSE 协议已透传终止帧后的策略不在本次变更范围。

## 3. 验证

| 状态 | 验证 | 结果 |
| --- | --- | --- |
| ✅ | `make test-unit`、`go build ./...`、`go vet ./...` | 通过 |
| ✅ | `make test-race`、provider 专项 race | 通过 |
| ✅ | format、architecture、migration、RBAC、IAM browser result、部署文档检查 | 通过；RBAC 915 条，更新对应 handler 源码摘要 |
| ✅ | Web lint、666 项单测、196 项 critical、生产构建 | 通过；critical 行覆盖 93.34%，分支覆盖 85.99% |
| ⏳ | 浏览器桌面/移动 | 最终结果待回填；不将中断的进程计为成功 |
| ⏳ | OCR high 与独立复核 | 独立复核修复 typed 201 边界；最终机器评审待回填 |
| ⏳ | 生产更新与验收 | 按下列顺序执行，结果待回填 |

发版前扫描得到八条既有 `shortcut:` / `ponytail:` 注释，无本次新增。它们涉及 Channel 多副本 fencing、IAM 管理扫描/写锁、既有证据 fixture、WS 有界历史、test-health 和旧账号查询协议；本次不扩大这些既有范围。

## 4. 生产升级与范围

预检确认十个业务容器健康、重启为零、SSRF 旁路均关闭。Identity 143、Billing 144 已应用，唯一缺失为既有 Log 145，三个档位观察列尚不存在。其余 owner 迁移与当前 manifest 一致。

1. 在服务器私有目录备份 Log 数据库，使用现有 `cmd/migrate` 只执行 Log 145；二次执行验证无操作，并使用真实运行账号验证新列读取及 SELECT/INSERT/UPDATE 权限。
2. 全部镜像在本机交叉构建 Linux/amd64；每次切换先保留旧镜像回滚标签和 Compose 备份。
3. 更新 Billing/Log owner，再更新 Channel、复用其镜像的账号 worker、Monitor、Notify、Relay 和 Admin 后端。provider 的所有生产调用服务均在范围内。
4. Web、Identity、Config 不在本次镜像更新范围；既有强制认证、档位计价、微信和证据监控开关保持现场状态。本次发布不代表这些功能完成外部验收或收费启用。
5. 核对镜像和源码摘要、健康、重启、SSRF 设置、日志分类计数、匿名拒绝和已认证只读接口。不上游付费探测、不创建支付订单。

部署证据完成后补入本节，再合入 develop/main 并发布 v0.37.1。
