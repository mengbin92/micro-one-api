# micro-one-api

基于 `one-api` 的微服务化重构方案仓库，当前主要沉淀：

1. 现有 `one-api` 项目的结构分析。
2. 微服务拆分与迁移路径设计。
3. 基于 `go-kratos` 的落地架构方案。
4. 第一阶段 Kratos 工程骨架、proto 契约与 gRPC 调用链落地。
5. 第二阶段 OpenAI 兼容 HTTP 网关与主链路完整实现。

## 文档

1. [One-API微服务改造方案](./docs/One-API微服务改造方案.md)
2. [One-API基于Kratos的微服务落地方案](./docs/One-API基于Kratos的微服务落地方案.md)
3. [第一阶段实施方案](./docs/第一阶段实施方案.md)
4. [第二阶段实施方案](./docs/第二阶段实施方案.md)

## 当前结构

当前仓库已按最新 `go-kratos/kratos-layout` 风格组织为：

1. `api/` - gRPC 和 HTTP API 定义
2. `cmd/` - 各服务的主程序入口
3. `configs/` - 配置文件
4. `internal/` - 各服务的内部实现
5. `third_party/` - 第三方 proto 文件
6. `test/` - 集成测试和 mock 服务

## 第二阶段完成状态

第二阶段已完成以下功能：

### 核心服务

1. **identity-service** - 用户鉴权服务
   - Token 验证（状态、过期时间、额度检查）
   - 用户状态验证
   - 模型白名单支持
   - 完整的错误映射到 gRPC/HTTP 状态码

2. **channel-service** - 渠道管理服务
   - 渠道选择与优先级排序
   - 同优先级随机负载均衡
   - 禁用渠道过滤
   - 可用模型列表查询

3. **relay-gateway** - OpenAI 兼容 HTTP 网关
   - `/v1/chat/completions` - 聊天补全接口（支持流式和非流式）
   - `/v1/models` - 模型列表接口
   - `/health` - 健康检查接口
   - Token 鉴权与模型权限校验
   - Provider 适配器支持多种上游
   - SSE 流式响应透传

### 测试覆盖

- **单元测试**: identity、channel、provider 完整测试覆盖
- **集成测试**: 端到端主链路验证
- **错误处理**: 完整的 gRPC 到 HTTP 错误码映射

## 快速启动

### 方式一：使用 Makefile（推荐）

```bash
# 启动所有服务
make run-all

# 停止所有服务
make stop-all

# 单独启动服务
make run-identity   # 启动 identity-service
make run-channel    # 启动 channel-service
make run-relay      # 启动 relay-gateway
```

### 方式二：手动启动

1. 启动 identity-service：
```bash
IDENTITY_GRPC_ADDR=127.0.0.1:9001 \
IDENTITY_SQL_DSN="" \
go run ./cmd/identity-service
```

2. 启动 channel-service：
```bash
CHANNEL_GRPC_ADDR=127.0.0.1:9002 \
CHANNEL_SQL_DSN="" \
go run ./cmd/channel-service
```

3. 启动 relay-gateway：
```bash
IDENTITY_GRPC_ENDPOINT=127.0.0.1:9001 \
CHANNEL_GRPC_ENDPOINT=127.0.0.1:9002 \
RELAY_HTTP_ADDR=:8080 \
RELAY_PROVIDER_TIMEOUT=30s \
go run ./cmd/relay-gateway
```

## 环境变量

### identity-service
- `IDENTITY_GRPC_ADDR` - gRPC 监听地址（默认: 127.0.0.1:9001）
- `IDENTITY_SQL_DSN` - 数据库连接字符串（测试环境可为空）
- `INITIAL_ADMIN_USERNAME` - 首次启动创建的管理员用户名（默认: `admin`）
- `INITIAL_ADMIN_EMAIL` - 首次启动创建的管理员邮箱（默认: `admin@example.com`）
- `INITIAL_ADMIN_PASSWORD` - 首次启动创建的管理员密码；**未设置时随机生成 16 字符并打印到 identity-service 日志一次**

> 仅在 `users` 表为空时才会创建,已有用户则跳过;请关注启动日志中的 `INITIAL ADMIN CREATED` 块以获取随机密码。
> 忘记密码可用离线工具 `go run ./cmd/admin-reset -username admin` 重置(读取 `ADMIN_RESET_DSN` / `IDENTITY_SQL_DSN` / `SQL_DSN`);
> 通过 `-role` 可设置角色(0=guest, 1=user, 10=admin, 100=root),创建新用户时不指定默认 100。

#### 用户角色

`users.role` 字段(migration 025 加入)决定权限,数值越大越高:

| 数值 | 角色 | 含义 |
|---|---|---|
| 0   | `RoleGuestUser`  | 访客 |
| 1   | `RoleCommonUser` | 默认普通用户(注册即此角色) |
| 10  | `RoleAdminUser`  | 管理员(通过 `user.IsAdmin()` 判定) |
| 100 | `RoleRootUser`   | 首启 bootstrap 创建的超级管理员 |

代码内务必通过 `user.IsAdmin()` / `user.IsRoot()` 判定,**不要**再用 `user.Username == "admin"`。

##### 调用 promote/demote 时的操作者约束

`/api/user/manage` 的 `promote` / `demote` 动作会触发 identity-service 的 `SetUserRole`。
当请求头 `X-Operator-User-Id: <user_id>` 存在时,会按下列规则强校验:

1. 操作者必须存在,且角色 ≥ `RoleAdminUser`
2. 操作者不能修改自己的角色
3. 操作者必须**严格**高于目标用户的当前角色(同级 admin 之间无法互相降级)
4. 新角色必须**严格**低于操作者角色(admin 不能把别人提升到 admin)
5. root 用户的角色不可被任何人修改;`SetRole` 也不会授予 root 角色

未带 `X-Operator-User-Id` 时按"系统调用"处理,仅执行 root 保护与角色合法性检查——
之所以保留这条豁免,是因为当前 admin-api 由共享 `ADMIN_TOKEN` 鉴权,没有具体 operator 身份。
后续若切换为每用户 session 鉴权,可在 admin/server 入口处把它升级为必填。

### channel-service
- `CHANNEL_GRPC_ADDR` - gRPC 监听地址（默认: 127.0.0.1:9002）
- `CHANNEL_SQL_DSN` - 数据库连接字符串（测试环境可为空）

### relay-gateway
- `IDENTITY_GRPC_ENDPOINT` - identity-service gRPC 地址
- `CHANNEL_GRPC_ENDPOINT` - channel-service gRPC 地址
- `RELAY_HTTP_ADDR` - HTTP 监听地址（默认: :8080）
- `RELAY_PROVIDER_TIMEOUT` - 上游请求超时时间（默认: 30s）

## API 测试

### 1. 健康检查
```bash
curl http://localhost:8080/health
```

### 2. 获取模型列表
```bash
curl -H "Authorization: Bearer test-token" \
  http://localhost:8080/v1/models
```

### 3. 聊天补全
```bash
# 非流式响应
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer test-token" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [
      {"role": "user", "content": "Hello, world!"}
    ]
  }'

# 流式响应
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer test-token" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [
      {"role": "user", "content": "Hello, streaming!"}
    ],
    "stream": true
  }'
```

## 测试

```bash
# 运行所有测试
make test

# 运行集成测试
make dev-test-integration

# 运行单个服务测试
make dev-test-identity
make dev-test-channel
make dev-test-provider
```

## 第二阶段验收标准

第二阶段完成标准已全部满足：

- [x] `identity-service` 真实鉴权可用
- [x] `channel-service` 真实渠道选择可用
- [x] `relay-gateway` OpenAI 兼容 HTTP 主链路可用
- [x] `/v1/models` 和 `/v1/chat/completions` 可本地验收
- [x] 有 mock upstream 集成验证
- [x] 主要 usecase 有单元测试覆盖
- [x] billing 只作为明确接口边界存在，没有被绕过或散落实现

## 已知限制

1. **账务系统**: 仅预留 billing 接口，完整扣费逻辑在第三阶段实现
2. **Provider 适配**: 当前仅支持 OpenAI-compatible 适配器，其他 provider 待后续支持
3. **数据库**: 当前使用内存数据源，真实数据库连接待配置

## 后续计划

第三阶段将重点关注：
1. 完整账务扣费逻辑
2. 更多 Provider 适配器（如 Anthropic、Gemini、Azure 等）
3. 后台管理 API 迁移
4. 真实数据库集成

## 鸣谢

感谢 [one-api](https://github.com/songquanpeng/one-api) 项目提供的原始架构与实现基础，本仓库中的分析与改造方案均基于该项目展开。

感谢 [go-kratos/kratos](https://github.com/go-kratos/kratos) 项目提供的微服务框架设计与工程实践参考。
