# micro-one-api → Kratos 大仓模式迁移方案（最终版）

> 整合原则：采纳"platform/ 基础设施分层"和"按路径触发 CI"这两个真实改进；`client/` SDK 层列为**可选的后续阶段**，不在首次迁移中引入；补齐可执行的迁移命令、风险预案和工作量估算；对"服务间禁止互相 import internal"给出具体的 CI 强制机制，而不只是停留在文档规定。

---

## 0. 背景与目标

**现状**：单仓库、单 `go.mod`、多服务并存，服务按 `cmd/<service>` + `internal/<service>` 组织。

**目标**：改造为 kratos 社区约定的 `app/<domain>/<type>` 大仓结构，统一 API 目录，抽出公共基础设施层，建立能规模化维护的 CI 策略。

**范围控制**：本次只做**结构化 + 基础设施统一 + CI 策略**三件事。SDK 客户端封装（`client/`）作为可选的第二阶段，视团队实际痛点决定是否引入，不在本轮铺开，避免过度设计。

---

## 1. 现状盘点

| 服务 | 当前路径 | 归属类型 | 是否有独立数据库 | 是否被其他服务依赖 |
|---|---|---|---|---|
| relay-gateway | cmd/relay-gateway | interface | 否 | 是（对外入口） |
| admin-api | cmd/admin-api | admin | 是 | 否 |
| identity-service | cmd/identity-service | service | 是 | 是（核心，被多方调用） |
| channel-service | cmd/channel-service | service | 是 | 是 |
| billing-service | cmd/billing-service | service | 是 | 是 |
| config-service | cmd/config-service | service | 是 | 是 |
| log-service | cmd/log-service | 待评估（见下） | 否/写ES | 是 |
| monitor-worker | cmd/monitor-worker | job | 否 | 否（消费者） |
| notify-worker | cmd/notify-worker | job | 否 | 否（消费者） |

**log-service 归属判断**：若只负责日志采集/格式化，建议降级为 `platform/logging` 里的公共能力，不单独维护一个服务；若负责日志查询、分析、对接 ES/Kafka 等独立业务逻辑，则保留为 `app/log/service`。迁移前需团队确认。

> 迁移前请对照实际仓库核实以上分类和依赖关系，"是否被其他服务依赖"直接决定迁移顺序。

**命名冲突提醒**：`config-service`（业务服务，管理配置数据）与后面新增的 `platform/config`（应用启动时加载本地配置/接 Nacos 等的工具库）是两个不同概念，迁移文档和团队沟通中要明确区分，避免开发时误用。

---

## 2. 目标目录结构

```
micro-one-api/
├── api/                          # 全局统一 proto 定义与生成代码
│   ├── common/
│   │   ├── errors.proto
│   │   └── pagination.proto
│   ├── identity/service/v1/
│   ├── channel/service/v1/
│   ├── billing/service/v1/
│   ├── config/service/v1/
│   ├── relay/interface/v1/
│   └── admin/admin/v1/
│
├── app/                          # 所有服务，按 领域/类型 组织
│   ├── relay/interface/
│   │   ├── cmd/relay-interface/main.go
│   │   ├── configs/config.yaml
│   │   └── internal/{conf,biz,data,service,server}
│   ├── admin/admin/
│   ├── identity/service/
│   ├── channel/service/
│   ├── billing/service/
│   ├── config/service/
│   ├── monitor/job/
│   └── notify/job/
│
├── platform/                     # 基础设施统一层（新增）
│   ├── database/                 # MySQL 等连接封装
│   ├── cache/                    # Redis 封装
│   ├── registry/                 # 服务发现
│   ├── config/                   # 配置加载（Nacos/本地文件等）
│   ├── logging/                  # 日志组件（含原 log-service 降级内容，如适用）
│   ├── tracing/                  # 链路追踪
│   ├── messaging/                # MQ 封装
│   └── security/                 # JWT / 加解密等
│
├── pkg/                          # 纯工具函数（不放基础设施）
│   ├── utils/
│   ├── validator/
│   ├── xerror/
│   └── timeutil/
│
├── third_party/                  # google/api 等 proto 依赖
├── migrations/                   # 数据库迁移脚本，全局统一
├── deployments/
│   ├── docker/
│   │   ├── identity.Dockerfile
│   │   ├── relay.Dockerfile
│   │   └── ...
│   ├── helm/
│   └── k8s/
├── scripts/                      # 统一脚本（新增，替代分散在 Makefile 里的命令）
│   ├── proto.sh
│   ├── wire.sh
│   ├── lint.sh
│   └── migrate.sh
├── test/
├── docs/
├── go.mod                        # 唯一根 go.mod
├── go.sum
├── Makefile
└── README.md
```

**`platform/` vs `pkg/` 的边界**（明确写入团队规范，避免退化成大杂烩）：
- `platform/`：任何涉及外部资源连接、协议封装、跨服务基础能力的代码（DB、Redis、MQ、Registry、Trace、Config 加载、JWT）
- `pkg/`：不依赖外部资源、无状态的纯函数工具（字符串处理、校验、错误码定义、时间处理）

---

## 3. 迁移原则

1. **一次只迁一个服务**，每完成一个服务的迁移就跑通编译、单测、Docker 构建，再迁下一个。
2. **保留旧目录直到新服务本地验证通过**，通过独立分支隔离，不在 main 上边迁边删。
3. **对外接口（gRPC/HTTP 地址、端口、服务发现注册名）保持不变**，迁移只是工程结构调整。
4. **每个服务迁移单独建 PR**，附上第 7 节 checklist 勾选记录。
5. **优先迁移被依赖最少的服务打样**，把踩坑经验固化成脚本，再迁移核心服务。
6. **`platform/` 只允许被 `app/` 依赖，不能反向依赖 `app/`**，避免循环依赖。
7. **`client/` SDK 层本轮不引入**：只有当团队实际观察到"多个服务重复手写 retry/熔断/超时逻辑"这类具体痛点时，再作为独立的第二阶段迭代加入，避免一开始就为不存在的规模问题预先设计。

---

## 4. 迁移顺序

按依赖关系和风险从低到高：

```
第一批（无依赖方，风险最低，用于打样）：
  config-service → monitor-worker → notify-worker

第二批（被 1-2 个服务依赖）：
  channel-service → billing-service → log-service（含降级判断）

第三批（核心服务，被多方依赖）：
  identity-service

第四批（对外入口 + 管理后台，依赖前面所有服务）：
  admin-api → relay-gateway

穿插进行（不依赖具体服务迁移顺序，可提前做）：
  platform/ 基础设施抽离（建议在第一批服务打样时同步启动）
```

---

## 5. 详细迁移步骤（以 config-service 打样为例）

### 5.1 建立分支

```bash
git checkout -b refactor/monorepo-config-service
```

### 5.2 统一 proto 到全局 api/

```bash
mkdir -p api/config/service/v1
git mv cmd/config-service/proto/*.proto api/config/service/v1/ 2>/dev/null \
  || git mv internal/config-service/proto/*.proto api/config/service/v1/
```

### 5.3 用官方脚手架生成新骨架

```bash
kratos new app/config/service --nomod
```
`--nomod` 确保复用仓库根目录的唯一 go.mod。

### 5.4 生成 proto 代码

```bash
kratos proto client api/config/service/v1/config.proto
kratos proto server api/config/service/v1/config.proto -t app/config/service/internal/service
```

### 5.5 清理脚手架自带示例

```bash
rm -f app/config/service/internal/service/greeter.go
rm -f api/config/service/v1/greeter.proto 2>/dev/null
```

### 5.6 迁移业务代码

| 旧位置 | 新位置 |
|---|---|
| `internal/config-service/handler/*.go` | `app/config/service/internal/service/*.go` |
| `internal/config-service/logic/*.go` | `app/config/service/internal/biz/*.go` |
| `internal/config-service/repository/*.go` | `app/config/service/internal/data/*.go` |
| `internal/config-service/config/*.go` | `app/config/service/internal/conf/*.go`（改用 kratos 的 proto-based conf） |
| `cmd/config-service/main.go` | `app/config/service/cmd/config-service/main.go` |

重新生成 wire：

```bash
cd app/config/service/cmd/config-service
wire
```

### 5.7 抽离基础设施到 platform/（首个服务迁移时同步建立）

检查该服务里的 DB 连接、Redis 客户端初始化等代码，抽到对应目录：

```bash
mkdir -p platform/{database,cache,logging,config,security}
# 把原来分散在各服务里的连接初始化代码合并进 platform/database/mysql.go 等
```

服务代码里改为：

```go
import "yourmodule/platform/database"
import "yourmodule/platform/cache"

db := database.NewMySQL(cfg)
rdb := cache.NewRedis(cfg)
```

纯工具函数（不涉及外部资源）下沉到 `pkg/`：

```go
import "yourmodule/pkg/xerror"
import "yourmodule/pkg/timeutil"
```

### 5.8 更新 go.mod

```bash
go mod tidy
go build ./app/config/service/...
```

### 5.9 更新 Makefile 与 scripts/

改为参数化调用，避免每个服务一个 target：

```makefile
build:
	go build -o bin/$(SERVICE) ./app/$(DOMAIN)/$(TYPE)/cmd/$(SERVICE)

run:
	go run ./app/$(DOMAIN)/$(TYPE)/cmd/$(SERVICE)

proto:
	./scripts/proto.sh $(DOMAIN) $(TYPE)
```

使用示例：

```bash
make build SERVICE=config-service DOMAIN=config TYPE=service
make run SERVICE=config-service DOMAIN=config TYPE=service
```

`scripts/proto.sh` 示例：

```bash
#!/bin/bash
# scripts/proto.sh <domain> <type>
DOMAIN=$1
TYPE=$2
PROTO_DIR="api/${DOMAIN}/${TYPE}/v1"
OUT_DIR="app/${DOMAIN}/${TYPE}/internal/service"

kratos proto client ${PROTO_DIR}/*.proto
kratos proto server ${PROTO_DIR}/*.proto -t ${OUT_DIR}
```

### 5.10 更新 Dockerfile

```dockerfile
# 旧
COPY cmd/config-service ./cmd/config-service
RUN go build -o /app/config-service ./cmd/config-service

# 新
COPY app/config/service ./app/config/service
COPY platform ./platform
COPY pkg ./pkg
RUN go build -o /app/config-service ./app/config/service/cmd/config-service
```

### 5.11 本地验证

```bash
make build SERVICE=config-service DOMAIN=config TYPE=service
make run SERVICE=config-service DOMAIN=config TYPE=service
# 手动或脚本调用接口，确认行为与迁移前一致
```

### 5.12 删除旧目录并提交 PR

```bash
git rm -r cmd/config-service internal/config-service
```
PR 描述附第 7 节 checklist 勾选记录。

---

## 6. 服务间隔离的强制机制（关键补充）

**问题**：单 go.mod 下，Go 的 `internal/` 包可见性规则是"同一父目录树内可见"，同一仓库内 `app/relay/interface` 在语法上是可以 import `app/identity/service/internal/biz` 的，编译器不会自动拦截。"服务只能通过 API 通信"这条原则**必须靠 CI 静态检查强制**，仅写入文档不够。

**落地方案**：在 CI 中加入依赖检查脚本，禁止跨服务 import internal：

```bash
#!/bin/bash
# scripts/check-service-isolation.sh
# 检查 app/ 下任何服务是否 import 了其他服务的 internal 包

VIOLATIONS=0
for service_dir in app/*/*/; do
    service_name=$(echo "$service_dir" | sed 's/app\///' | sed 's/\///')
    imports=$(go list -deps "./${service_dir}..." 2>/dev/null | grep "yourmodule/app/" | grep -v "$service_dir")
    if [ -n "$imports" ]; then
        echo "❌ $service_dir 违规引用了其他服务的 internal 包："
        echo "$imports"
        VIOLATIONS=1
    fi
done

exit $VIOLATIONS
```

在 CI pipeline 里加一步：

```yaml
- name: Check service isolation
  run: ./scripts/check-service-isolation.sh
```

也可以用现成的 `depguard` linter 配置规则替代手写脚本，效果类似，团队可按熟悉程度选择。

---

## 7. 单服务迁移 Checklist

```
服务名：____________________

[ ] proto 已迁移到 api/<domain>/<type>/v1/
[ ] 用 kratos new --nomod 生成新骨架
[ ] proto client/server 代码已生成
[ ] 脚手架自带 greeter 示例已清理
[ ] 业务代码已迁移到 biz/data/service 对应层
[ ] wire 已重新生成，无报错
[ ] 基础设施代码已迁移到 platform/ 对应子目录
[ ] 纯工具函数已下沉到 pkg/
[ ] go mod tidy 无冲突
[ ] go build ./app/<domain>/<type>/... 通过
[ ] 单元测试通过
[ ] Makefile/scripts 中的调用已改为参数化形式
[ ] Dockerfile 已更新并本地构建成功
[ ] 本地启动服务，接口行为与迁移前一致
[ ] CI 服务隔离检查（check-service-isolation.sh）通过
[ ] 与该服务有调用关系的其他服务，联调测试通过
[ ] 旧目录已删除
[ ] PR 已提交并 review 通过
```

---

## 8. CI/CD 策略（按路径增量触发）

```yaml
# 伪代码示意，实际用 dorny/paths-filter 或类似 action 实现
on:
  push:
    paths:
      - 'api/**'      → 全量: proto 生成 + build 所有服务 + test 所有服务
      - 'app/identity/**' → 只 build/test/docker/deploy identity 服务
      - 'app/relay/**'    → 只 build/test/docker/deploy relay 服务
      - 'platform/**'  → 全量: build 所有服务 + 集成测试（基础设施变更影响面广）
      - 'pkg/**'       → 全量: build 所有服务（工具函数变更也需要全量验证）
```

这样服务数量增多后，CI 时间不会线性增长，只有真正受影响的服务才会触发完整的 build/deploy 流程。

---

## 9. Proto 版本管理

新增 proto 版本时不覆盖旧版本，并存：

```
api/identity/service/
├── v1/
│   └── identity.proto
└── v2/
    └── identity.proto
```

调用方按需选择依赖的版本，服务端可同时运行 v1/v2 handler 直到旧版本调用方全部下线。

---

## 10. 跨服务集成验证（每批迁移完成后执行）

1. 启动本批次涉及的所有服务（迁移后的新路径）
2. 跑 `test/` 目录下对应的集成测试/e2e 脚本
3. 跑 `scripts/check-service-isolation.sh`，确认没有新增跨服务 internal 引用
4. 检查服务间 gRPC 调用地址（环境变量配置）是否因构建路径变化而失效
5. 检查 K8s manifest 中的镜像构建 context、健康检查路径是否需要同步调整

---

## 11. 全部迁移完成后的收尾工作

1. 更新 README.md，替换所有旧路径引用
2. 更新 CI 配置，确认按路径触发规则生效
3. 更新 CONTRIBUTING.md，明确新增服务的标准流程：
   ```bash
   kratos new app/<domain>/<type> --nomod
   ```
   `<type>` 只能是 `interface | service | admin | job | task` 之一
4. 完成 log-service 的降级评估（若适用，迁移完成后清理该服务的独立部署配置）
5. 团队内部复盘迁移过程，把踩过的坑写入团队 wiki

---

## 12. 关于 client/ SDK 层的后续评估（第二阶段，不在本轮启动）

**何时考虑引入**：当团队发现多个服务里出现重复的手写 retry/超时/熔断逻辑（比如 3 个以上服务各自维护了一份类似的 gRPC 调用重试代码），说明统一 SDK 封装的收益开始显现，此时再单独立项：

```
client/
├── identity/
│   ├── client.go
│   ├── options.go
│   └── retry.go
```

**引入前需要评估的维护成本**：每次 proto 变更，除了自动生成的 client stub，还需要手工同步维护 `client/*/client.go` 里的封装方法签名，这是双层维护，服务数量越多，心智负担越重。建议先用一个试点服务验证收益，再决定是否推广到全部服务。

---

## 13. 风险与回滚预案

| 风险点 | 应对措施 |
|---|---|
| wire 依赖注入迁移后编译失败 | 保留旧目录直到新服务本地验证通过，随时可 revert 分支 |
| 服务间调用地址因 Docker 路径变化失效 | gRPC/HTTP 监听端口和服务发现注册名保持不变，只改构建路径 |
| 迁移过程中断，新旧路径混杂 | 每个服务独立 PR、独立分支，可单独回滚 |
| platform/ 基础设施抽离引入循环依赖 | 严格遵守"platform 只能被 app 依赖，不能反向依赖"，CI 中可加 import cycle 检查 |
| 服务间违规互相 import internal | 第 6 节的 CI 隔离检查脚本作为强制门禁，PR 无法合并 |
| client/ 层维护成本失控 | 本轮不引入，待明确痛点出现后再做小范围试点 |

---

## 14. 工作量预估

| 阶段 | 内容 | 预估人日 |
|---|---|---|
| 前期准备 | platform/ 目录建立 + 基础设施代码抽离设计评审 | 2-3 人日 |
| 第一批（3 个无依赖服务） | 打样迁移 + 固化 scripts/流程 + CI 隔离检查脚本 | 4-5 人日 |
| 第二批（3 个中等依赖服务） | 参照流程迁移，含 log-service 降级评估 | 2-3 人日/服务 |
| 第三批（identity-service） | 核心服务，需充分联调测试 | 3-4 人日 |
| 第四批（admin-api + relay-gateway） | 收尾 + 全链路联调 | 3-4 人日 |
| CI/CD 路径触发策略落地 | 配置 paths-filter，验证增量构建效果 | 2 人日 |
| 收尾工作 | 文档、复盘 | 1-2 人日 |
| **合计** | | **约 20-26 人日** |

> 相比只做目录规范化的版本（约 15-20 人日），本方案因新增 platform/ 抽离和 CI 隔离检查，工作量有所增加，但换来的是可规模化维护的工程体系，且明确排除了 client/ 层这类当前规模下收益不确定的投入。
