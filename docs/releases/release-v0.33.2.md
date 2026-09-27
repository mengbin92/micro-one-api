# Micro-One-API v0.33.2 发布：新用户默认角色修复与线上角色流程验收

> 2026-09-27 · 上一版：[v0.33.1](./release-v0.33.1.md)（2026-09-27）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.33.2)

v0.33.2 是 v0.33.1 之后的 **PATCH 用户角色修复版本**：新账号创建时统一赋予普通用户角色，修复管理台初始显示“访客”、提升再降级后才显示“用户”的问题。生产 identity-service 已更新，并通过临时账号完成注册和管理员角色操作验收。

**无公共 API/proto 变更、无数据库迁移、无前端资源更新**。受影响的运行时服务只有 `identity-service`；生产独立 Compose 同步补齐仓库模板已有的 `ADMIN_TOKEN` 透传。已有账号的角色不自动改写。

## 修复与变更

### 1. 新账号默认角色与管理员降级结果保持一致

**根因**：密码注册、后台创建和 OAuth 首次登录构造领域用户时遗漏 `Role`，Go 零值 `0` 被存储为访客。管理台正确显示存储值，而降级操作明确写入 `1`（普通用户），因此同一账号在提升、降级前后的角色不一致。邀请码注册复用密码注册入口，也受到影响。

**修复**：三个创建入口显式设置 `RoleCommonUser`；保留角色定义 `0=访客、1=用户、10=管理员、100=超级管理员` 及既有权限校验。补充新账号角色与持久化断言、提升/降级往返、注册后 HTTP 角色回读和 OAuth 已有账号角色保留测试。

**影响服务**：`identity-service`。管理页面无需重新构建，管理员提升仍为 `10`、降级仍为 `1`；已有访客、普通用户及管理员在 OAuth 再次登录时保持原角色。

### 2. 补齐生产管理令牌的角色操作配置

**根因**：生产独立维护的 Compose 未向 identity-service 传入 `ADMIN_TOKEN`，虽然仓库模板已经包含该项。使用 admin-api 管理令牌发起角色操作时，identity-service 无法独立确认系统操作员凭据并返回认证拒绝；该路径与用户会话凭据的角色操作不同。

**修复**：先备份生产 Compose，仅在 identity-service 的 environment 中增加既有 `ADMIN_TOKEN` 透传，核对解析值与运行中的 admin-api 一致后重建 identity-service。没有放宽服务令牌或操作员校验。

**影响服务**：生产 `identity-service` 的环境配置。通过管理令牌完成临时账号的提升、降级，并分别从管理用户查询接口回读角色 `10`、`1`；其他服务未重建。

### 3. 同步 Q3 基线与 O5 生产观察记录

**根因**：上一版发布后，性能对照基线及生产观察记录需要与已发布版本和新采样证据同步。

**变更**：Q3 基线更新为 v0.33.1；补充 O5 使用受限临时令牌执行的生产受控采样与权威 RPC 延迟记录，保留自然流量不足以形成缓存成本结论的边界。

**影响服务**：仅 CI 基线与运维文档，无新增运行时行为。受控采样记录见[下一阶段实施记录](../design/next-stage-plan-2026-09-22.md)。

## 兼容性说明

- 无公共 API/proto、数据库 schema、前端资源或角色数值定义变更。
- `ADMIN_TOKEN` 为既有配置；使用管理令牌调用角色接口的部署须同时向 admin-api 和 identity-service 传入相同值。用户会话操作员仍按原有身份与等级规则校验。
- 本次不批量更新历史 `role=0` 账号：存储值无法区分误创建账号与有意设置的访客。历史账号需核实后逐账号校正，不能把所有访客直接改成普通用户。

## 升级步骤

1. 在本机从本版本交叉构建 `linux/amd64` identity-service 镜像，使用 `app/identity/Dockerfile` 和 `SERVICE_PATH=./app/identity/cmd/identity`。生产主机不执行构建。
2. 备份当前运行镜像及生产 Compose；确认 identity-service 已透传与 admin-api 相同的 `ADMIN_TOKEN`。保留生产独立配置中的其他设置，不以仓库模板整份覆盖。
3. 上传并加载镜像，在生产 Compose 目录执行 `docker compose up -d --no-deps --no-build identity-service`。无需迁移数据库、重建其他服务或同步 `web/dist`。
4. 检查 identity-service `/healthz`、运行状态和重启计数。使用临时账号验证注册后角色为 `1`，管理端提升后为 `10`、降级后为 `1`，随后删除测试账号。

生产已完成以上步骤，运行镜像及脱敏验证结果见[上线证据](../runbooks/evidence/identity-role-v0.33.2-2026-09-27.json)。源码提交为 `9f9b4941`，后续发布提交只增加文档。

回滚镜像为 `docker-compose-identity-service:rollback-v0.33.2-20260927-214710`；配置备份为生产 Compose 目录下的 `docker-compose.yml.bak.identity-role-20260927-215023`。如需回滚，将旧镜像重新标记为 `docker-compose-identity-service:latest`，按需恢复该配置备份，再仅重建 identity-service。回退旧二进制会重新引入新用户角色缺陷；恢复旧配置也会恢复管理令牌路径的认证限制。

## 验证

- 修复前，注册、邀请码注册、后台创建和 OAuth 创建测试均得到角色 `0`，提升再降级后变为 `1`；修复后全部通过。
- `go test ./app/identity/internal/... -count=1` 与管理端角色操作、权限守卫回归测试通过。
- `make verify` 全部门禁通过：格式、Go 单测、关键包 race、分层、迁移治理、生成 API 类型、前端 lint、52 个测试文件 / 196 项测试、构建与字节预算。
- 生产 identity-service 健康检查为 `200`、状态为 `running`、重启计数为 `0`。受控账号经生产注册 RPC 创建，再通过 admin-api 用户查询及管理接口验证角色序列 `1 → 10 → 1`，最后删除账号并回查确认不存在。
- 生产探测采用注册 RPC，未触发公共 HTTP 注册入口的奖励发放；公共 HTTP 注册后角色由本地回归测试覆盖。OAuth 真实供应商登录未在线上重走，其创建与已有角色保留由单测覆盖。

## 完整变更日志

- `b25a0039` chore(benchmark): pin Q3 baseline to v0.33.1
- `2ae20588` docs: record O5 production controlled sampling with step-5-preview
- `9f9b4941` fix(identity): initialize new accounts with the common user role
- docs(release): v0.33.2
