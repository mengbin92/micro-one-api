# Micro-One-API v0.7.1 发布公告

> 2026-07-14 · 上一版: [v0.7.0](./release-v0.7.0.md) (2026-07-12)

v0.7.1 是 v0.7.0 之后的 **PATCH** 版本,聚焦管理后台日志详情与分页修复、服务间路由与鉴权配置修复,以及代码安全扫描告警的收敛。覆盖 `v0.7.0..v0.7.1` 共 7 次提交(6 `fix:` + 1 `docs:`),无 `feat` 提交。

本版**不涉及数据库迁移**,**无破坏性 API 变更**。proto 变更为纯新增(`GetLedgerEntry` RPC + `username` 字段,向后兼容),不删除/不重编号。现有部署的升级路径为重建镜像并滚动重启,无需执行 SQL 迁移。

## 亮点

- **管理后台日志详情修复**:admin-api 的 `/api/log/{id}` 由代理 log-service 改为直接走 billing-service 的 `GetLedgerEntry`,彻底解决 log-service 403/404 导致日志详情打不开的问题,并补充 `username` 关联字段。
- **日志分页对齐**:`/api/log` 列表返回补齐 `total` 字段,修正前端分页控件无法显示总数的问题。
- **服务间路由修复**:log/notify/monitor/config/identity 多个服务把 gorilla/mux 的 `HandleFunc`(仅精确匹配)改为 `HandlePrefix`(前缀匹配),修复 `/v1/logs/{id}` 等子路径返回裸 404 的路由回归。
- **docker-compose 鉴权修复**:log-service 和 billing-service 补齐 `SERVICE_TOKEN` 环境变量,修复 ServiceAuth 中间件因 token 未设置而对所有 `/v1/logs` 请求返回 403 的问题。
- **安全扫描收敛**:移除幂等中间件中来自 HTTP 头的明文密钥日志(CodeQL CWE-312/315/359),升级 `golang.org/x/crypto` 至 v0.54.0,并抑制无修复版本且本仓不引用的 `GO-2026-5932`(openpgp)告警。

## 变更内容

### Added

- `api/billing/v1`:新增 `GetLedgerEntry` RPC 及 `GetLedgerEntryRequest` / `GetLedgerEntryResponse` 消息(向后兼容,不删除任何已有 RPC/字段)。注:此 RPC 为修复 admin 日志详情 bug 的内部支撑接口,非独立用户功能。
- `api/common/v1.LedgerEntry`:新增 `username` 字段(序号 24,从 `users` 表 LEFT JOIN 关联查询,读时计算不落表)。
- `app/billing/internal/biz`:`Ledger` DO 新增 `Username` 字段;`BillingUsecase.GetLedgerByID` 用例;`LedgerRepo.GetLedgerByID` 仓储接口;`ErrLedgerNotFound` 类型化错误。
- `app/billing/internal/data`:`ledgerRepo.GetLedgerByID` 实现(含 `users` LEFT JOIN);`ListLedgers` / `ListLedgersBySubscriptionAccount` 同步补充 `username` 关联;`ledgerModel.Username` 标记 `->:migration` 避免 AutoMigrate 误加列。
- `app/billing/internal/service`:`BillingService.GetLedgerEntry` 传输适配,映射 `ErrLedgerNotFound` → gRPC `NotFound`。
- `app/admin/internal/service`:`AdminService.GetLedgerEntry` 调用 billing-service 并组装管理端视图。
- `app/log/internal/server/http_getlog_route_test.go`:`/v1/logs/{id}` 路由可达性回归测试(覆盖 200 / JSON 404 / 401 三种路径)。

### Fixed

- **admin 日志详情 403/404**:`handleOneAPIGetLog` 从"代理 log-service HTTP"改为"调用 billing `GetLedgerEntry`",消除 log-service token 未配置或路由不匹配导致的故障;404 映射为 JSON `ledger entry not found`。
- **admin 日志分页**:`/api/log` 列表返回结构补齐 `total`,并修正 `page` 从 0 基改为 1 基的请求映射。
- **gorilla/mux 前缀路由**:log/notify/monitor/config/identity 服务把 `HandleFunc("/v1/.../")` 改为 `HandlePrefix("/v1/.../")`,使 `{id}`/`{provider}` 子路径能到达处理器。
- **docker-compose SERVICE_TOKEN**:log-service 与 billing-service 补齐 `SERVICE_TOKEN=${SERVICE_TOKEN:?...}`,修复 ServiceAuth 返回 403。
- **安全扫描告警**:`platform/middleware/idempotency.go` 移除来自 `Idempotency-Key` 头的明文日志字段(修复 CodeQL #229/#230);升级 `golang.org/x/crypto` v0.52.0 → v0.54.0(连带 x/net、x/sync、x/sys、x/text);`.trivyignore` 抑制无修复版本的 `GO-2026-5932`(本仓仅用 `bcrypt`,不引用 `crypto/openpgp`)。

### Changed

- `docs/` 目录重组:45 个平铺文档分类到 `releases/`(17)、`runbooks/`(8)、`design/`(15)、`migration/`(5),新增 `docs/README.md` 导航索引,同步更新全仓 33+ 处路径引用(README、release.yml、.gitleaksignore、代码注释、docker-compose、k6 脚本等)。
- `.github/workflows/release.yml`:发版说明校验路径从 `docs/release-*.md` 更新为 `docs/releases/release-*.md`。
- `go.mod`:`golang.org/x/crypto` v0.54.0、`x/sync` v0.22.0、`x/net` v0.56.0、`x/sys` v0.47.0、`x/text` v0.40.0。
- 管理后台前端内嵌资产随构建刷新。

## 数据库迁移

**无。** 本次发版不新增任何 MySQL/Postgres/SQLite 迁移。`LedgerEntry.username` 为读时关联查询字段(`->:migration`),不落表,无需 ALTER TABLE。

## 破坏性变更

**无。** proto 变更为纯新增 RPC 与字段(序号递增,不删除/不重编号),gRPC/HTTP 向后兼容;无数据库 schema 变更;无配置项删除。

## 升级步骤

v0.7.0(大仓结构)升级到 v0.7.1:

```bash
git fetch --tags
git checkout v0.7.1

# 重新生成 Wire 和 Proto(可选,提交中已包含生成产物)
make wire
make proto

# 构建
make build

# 测试
make test-unit
```

部署侧:重建各服务镜像并滚动重启即可,无需执行 SQL 迁移。如使用 docker-compose,请确认已通过 `.env` 设置 `SERVICE_TOKEN`(log-service/billing-service 现在强制要求该变量,缺失会导致 compose 启动失败)。

## 验证

本次发版前已在 `v0.7.0..HEAD`(main = 1b2c3df)执行:

```bash
go build ./...                          # 通过
GOCACHE=/tmp/wire-gocache go vet ./...  # 无告警
make wire                               # 9 个 wire_gen.go 重生成,工作树 clean
make api && make config                 # Proto 生成,工作树 clean
make api-check                          # OpenAPI 校验通过
make test-unit                          # 全部 PASS
make wire-check                         # wireinject 编译通过
./scripts/check-architecture.sh         # 架构边界 0 违规
gosec -exclude-generated ./...           # 0 issues
git status --porcelain                  # 生成文件新鲜度验证:clean
```

> 注:CI 在 GitHub Actions 上还会额外跑前端 `npm run lint && npm test && npm run build`、`make web-build` 生成文件新鲜度检查、Docker 多架构构建(amd64/arm64)和 Trivy/govulncheck 安全流水线。本地因沙箱网络限制无法访问 vuln.go.dev,已用 gosec 与 .trivyignore 覆盖。
