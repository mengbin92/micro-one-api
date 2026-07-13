# micro-one-api → Kratos 大仓模式迁移方案（v3 修正版）

> 本版修正了 v2 版本中与官方 `kratos-layout` 模板及 CLI 大仓约定不符的部分（详见文末"与官方结构的对齐说明"）。所有标注"非官方扩展"的目录/机制是在官方最小大仓结构之上的可选增量，不属于 kratos 官方规范，采纳与否由团队自行决定。

---

## 0. 背景与目标

**现状**：单仓库、单 `go.mod`、多服务并存，服务按 `cmd/<service>` + `internal/<service>` 组织。

**目标**：改造为 kratos 官方 CLI 实际支持的大仓结构——`kratos new <root-service>` 建主项目，`kratos new app/<service> --nomod` 挂载子服务，全部共用一份根 `go.mod`。

**范围控制**：本次做**结构对齐官方 + 视需要叠加基础设施统一层**。SDK 客户端封装（`client/`）、`platform/` 基础设施层均为非官方扩展，按第 12 节标准评估是否引入，不因为"看起来更企业级"就默认采纳。

---

## 1. 官方大仓结构的真实约定（先对齐认知）

官方操作：

```bash
kratos new relay-gateway          # 主项目本身也是一个服务
cd relay-gateway
kratos new app/admin --nomod       # 子服务，共用根 go.mod
kratos new app/identity --nomod
```

生成结构：

```
relay-gateway/
├── api/
│   └── relay-gateway/v1/         # 主项目自己的 proto
├── app/
│   ├── admin/                     # 子服务，直接以服务名命名，不带 type 分类
│   │   ├── Dockerfile             # 每个服务自带 Dockerfile
│   │   ├── Makefile                # 每个服务自带 Makefile
│   │   ├── cmd/admin/
│   │   ├── configs/
│   │   ├── internal/{biz,conf,data,server,service}
│   │   └── openapi.yaml  # 暂缓（详见第 13 节）
│   └── identity/
│       └── ...(同上结构)
├── cmd/relay-gateway/              # 主项目自己的入口，主项目本身承担业务，不是空容器
├── internal/{biz,conf,data,server,service}
├── configs/
├── Dockerfile
├── Makefile
├── go.mod                          # 唯一根 go.mod
├── go.sum
├── openapi.yaml
└── third_party/
```

**关键约定，逐条记牢**：
1. 子服务目录名**直接是服务名**（`app/admin`），**没有** interface/service/admin/job/task 这层"类型"分类
2. proto 组织为 `api/<service>/v1/`，**扁平**，不套"领域/类型"两层
3. **每个服务自带 Dockerfile 和 Makefile**，不是集中放在一个 `deployments/` 目录
4. **主项目本身是一个正常服务**，有自己的 `cmd/`、`internal/`，不是纯粹的壳

---

## 2. 服务归属与主项目选择

现有 9 个服务里，选一个作为"主项目"，其余以 `--nomod` 方式挂载到它的 `app/` 下。

**主项目选择建议**：选 `relay-gateway`（对外网关），因为它是所有请求的入口，团队日常也最常需要单独关注它，放在根目录心智负担最低。

| 服务 | 角色 | 迁移后路径 |
|---|---|---|
| relay-gateway | **主项目** | 根目录（`cmd/`、`internal/` 直接在仓库根） |
| admin-api | 子服务 | `app/admin/` |
| identity-service | 子服务 | `app/identity/` |
| channel-service | 子服务 | `app/channel/` |
| billing-service | 子服务 | `app/billing/` |
| config-service | 子服务 | `app/config/` |
| log-service | 见第 3 节判断 | `app/log/`（如保留）或降级为非官方扩展 `platform/logging`（如降级，见独立文档） |
| monitor-worker | 子服务 | `app/monitor/` |
| notify-worker | 子服务 | `app/notify/` |

> 服务名去掉了原来的 `-service`/`-worker`/`-api` 后缀，跟官方示例（`app/admin`、`app/user`）的命名风格保持一致；是否保留原命名后缀不影响功能，团队可按现有习惯决定，本方案仅示例官方风格。

---

## 3. log-service 归属判断（沿用之前的判断标准）

若只负责日志采集/格式化，无对外查询 API、无独立存储依赖 → 降级为 `platform/logging`（非官方扩展库，见独立的《log-service 降级方案》文档，其中路径需要相应调整：`app/log/service/...` 改为直接在 `platform/logging/...` 落地，不再经过 `app/log` 这一层，因为它不再是独立服务）。

若有独立查询/分析职责 → 保留为 `app/log/` 子服务，按第 5 节流程正常迁移，不降级。

---

## 4. 目标目录结构

```
micro-one-api/                       # 以 relay-gateway 为主项目重命名或保持原仓库名
├── api/
│   ├── admin/v1/
│   ├── identity/v1/
│   ├── channel/v1/
│   ├── billing/v1/
│   ├── config/v1/
│   ├── monitor/v1/
│   ├── notify/v1/
│   └── relay-gateway/v1/            # 主项目自己的 proto
│
├── app/
│   ├── admin/
│   │   ├── Dockerfile
│   │   ├── Makefile
│   │   ├── cmd/admin/main.go
│   │   ├── configs/config.yaml
│   │   ├── internal/{biz,conf,data,server,service}
│   │   └── openapi.yaml  # 暂缓（详见第 13 节）
│   ├── identity/
│   ├── channel/
│   ├── billing/
│   ├── config/
│   ├── monitor/
│   └── notify/
│
├── cmd/relay-gateway/                # 主项目入口
├── internal/{biz,conf,data,server,service}
├── configs/config.yaml
├── Dockerfile                        # 主项目自己的 Dockerfile
├── Makefile                          # 主项目自己的 Makefile
├── go.mod
├── go.sum
├── openapi.yaml
├── third_party/                      # google/api 等 proto 依赖，全局共用一份
│
│  ── 以下为非官方扩展，视团队规模决定是否引入 ──
│
├── platform/                         # [非官方扩展] 基础设施统一层
│   ├── database/
│   ├── cache/
│   ├── registry/
│   ├── logging/                      # log-service 降级后落地于此
│   ├── tracing/
│   └── security/
├── pkg/                               # [非官方扩展] 纯工具函数
│   ├── utils/
│   └── xerror/
├── migrations/                        # 数据库迁移脚本，全局统一（合理的补充，官方模板未涉及但不冲突）
├── scripts/                            # [非官方扩展] 统一脚本
└── test/                               # 集成/e2e 测试
```

---

## 5. 迁移原则（不变）

1. 一次只迁一个服务，每完成一个就跑通编译、单测、Docker 构建。
2. 保留旧目录直到新服务本地验证通过，通过独立分支隔离。
3. 对外接口（gRPC/HTTP 地址、端口、服务发现注册名）保持不变。
4. 每个服务迁移单独建 PR，附 checklist 勾选记录。
5. `platform/`、`pkg/`、`client/` 等非官方扩展，只在团队确认有实际收益时才引入，不预先铺开。

---

## 6. 迁移顺序

不变，仍按依赖关系从低到高：

```
第一批（无依赖方，用于打样）：config → monitor → notify
第二批（被 1-2 个服务依赖）：channel → billing → log（含降级判断）
第三批（核心服务）：identity
第四批（对外入口 + 管理后台，最后迁移）：admin → relay-gateway（作为主项目，最后处理）
```

> 由于 relay-gateway 被定为主项目，它的迁移方式跟其他子服务不同——不是"挂载"而是"作为根项目改造"，建议放在最后单独处理，其余子服务先按 `--nomod` 挂载到临时的主项目骨架下验证通畅后，再把 relay-gateway 的业务代码迁移进根目录。

---

## 7. 详细迁移步骤

### 7.1 建立主项目骨架

```bash
git checkout -b refactor/monorepo-init

# 用官方 CLI 新建主项目骨架（先在临时目录生成，再手动合并业务代码）
kratos new relay-gateway-scaffold
cd relay-gateway-scaffold
go mod edit -module yourorg/micro-one-api
```

### 7.2 逐个挂载子服务（以 config 为例）

```bash
cd micro-one-api   # 主项目根目录
kratos new app/config --nomod
```

### 7.3 迁移 proto

```bash
mkdir -p api/config/v1
git mv <旧proto路径>/*.proto api/config/v1/
```

### 7.4 生成 proto 代码

```bash
kratos proto client api/config/v1/config.proto
kratos proto server api/config/v1/config.proto -t app/config/internal/service
```

### 7.5 清理脚手架自带示例

```bash
rm -f app/config/internal/service/greeter.go
rm -f api/config/v1/greeter.proto 2>/dev/null
```

### 7.6 迁移业务代码

| 旧位置 | 新位置 |
|---|---|
| `internal/config-service/handler/*.go` | `app/config/internal/service/*.go` |
| `internal/config-service/logic/*.go` | `app/config/internal/biz/*.go` |
| `internal/config-service/repository/*.go` | `app/config/internal/data/*.go` |
| `internal/config-service/config/*.go` | `app/config/internal/conf/*.go`（暂用手写 Go struct，proto-based conf 暂缓，详见第 13 节） |
| `cmd/config-service/main.go` | `app/config/cmd/config/main.go` |

重新生成 wire：

```bash
cd app/config/cmd/config
wire
```

**迁移时必须遵守的分层契约**（来自官方 `kratos-layout` 的 [AGENTS.md](https://github.com/go-kratos/kratos-layout/blob/main/AGENTS.md)，这是比"哪个文件放哪个目录"更重要的规则，迁移时容易只做了目录搬家，却没有把旧代码里混杂的调用关系理清楚）：

```
client ──► DTO ──► service ──► DO ──► biz ──► DO ──► data ──► PO ──► storage

DTO  Data Transfer Object — proto 请求/响应
DO   Domain Object        — 纯业务模型，不带 proto 标签、不带存储标签
PO   Persistent Object    — 存储层数据结构，只存在于 data 内部，不外泄
```

| 层 | 拥有 | 边界处转换 | 禁止 import |
|---|---|---|---|
| `service` | — | DTO ↔ DO | `data`、存储客户端 |
| `biz` | DO | DO | `service`、`data`、DTO、PO |
| `data` | PO | DO ↔ PO | `service`、DTO |

- `service` 只能 import `api/`（拿到 DTO 类型）和 `biz`（拿到 DO 类型），**不能** import `data`
- `biz` 只能为了错误码枚举 import `api/`，**不能** import `service` 或 `data`；repo 接口在这一层**声明**（`type <Resource>Repo interface`），由 `data` 实现，这是控制反转的关键点
- `data` import `biz` 是为了实现 repo 接口，构造函数返回接口类型而非具体类型：`func New<Resource>Repo(d *Data) biz.<Resource>Repo`
- `cmd` 是唯一通过 Wire 把三层组装在一起的地方

**旧代码迁移时的常见坑**：原来 `internal/config-service/handler` 里如果直接操作了数据库（比如直接调用 `repository` 包的 SQL 方法拼接返回值给 HTTP handler），这种"跳层调用"在新结构里是不允许的——迁移时要把这类代码拆开，`service` 层只做 DTO↔DO 转换和参数校验，具体的业务判断逻辑挪进 `biz`，PO↔DO 转换挪进 `data`，不能原样保留"service 直接摸 PO"这种写法。

**新增业务字段/接口时遵循官方给出的 checklist**：
1. 在 `api/config/v1/` 定义 DTO（`Create<Resource>`/`Get<Resource>`/`List<Resources>`/`Update<Resource>`/`Delete<Resource>`），跑 `make api`
2. 在 `biz` 里声明 DO 和 repo 接口，基于接口写 usecase
3. 在 `data` 里实现 repo 接口，返回 `biz.<Resource>Repo` 类型；存储结构和 DO 不一致时定义 PO，配 `new<Resource>`（DO→PO）和 `toBiz`（PO→DO）两个转换函数
4. 在 `data.ProviderSet`、`biz.ProviderSet`、`service.ProviderSet` 里注册对应的构造函数，在 `internal/server` 里注册 HTTP/gRPC 服务
5. 跑 `make all` 重新生成 Wire 和 go.mod

### 7.7 使用子服务自带的 Dockerfile/Makefile

`kratos new app/config --nomod` 已经自动生成了 `app/config/Dockerfile` 和 `app/config/Makefile`，按需微调其中的构建参数即可，**不需要**像 v2 方案那样另建一个集中的 `deployments/docker/` 目录去手写 Dockerfile。

```bash
cd app/config
cat Dockerfile   # 确认基础镜像、构建参数符合团队规范，通常只需改动版本号或加几行环境变量
```

### 7.8 更新 go.mod

```bash
go mod tidy
go build ./app/config/...
```

### 7.9 本地验证

```bash
cd app/config
make build
make run
# 或者用官方的 kratos run（会在多项目时弹出选择菜单）
kratos run
```

### 7.10 删除旧目录，提交 PR

```bash
git rm -r cmd/config-service internal/config-service
```

对其余子服务（admin、identity、channel、billing、monitor、notify）重复 7.2-7.10。

### 7.11 最后处理主项目（relay-gateway）

把 7.1 中临时生成的 `relay-gateway-scaffold` 里的业务代码，按同样的映射表迁移进仓库根目录的 `cmd/relay-gateway`、`internal/`，替换掉脚手架自带的示例代码。

---

## 8. 非官方扩展评估标准（platform / pkg / client / scripts / CI 路径触发）

这些机制**在官方模板里不存在**，是否引入取决于实际痛点是否出现，不要因为"看起来更专业"而默认采纳：

| 扩展 | 引入信号 | 不引入的代价 |
|---|---|---|
| `platform/`（基础设施统一层） | 3 个以上服务里出现重复的 DB/Redis/MQ 连接初始化代码 | 各服务各自维护一份连接封装，重复但不至于失控（9 个服务规模下可接受） |
| `pkg/`（纯工具函数） | 出现跨服务复用的无状态工具函数 | 同上，重复度可控 |
| `client/`（SDK 封装层） | 3 个以上服务出现重复的 gRPC 调用 retry/熔断逻辑 | 每次改动需要多处同步 |
| `scripts/` 统一脚本 | Makefile 里出现大量重复的 proto/wire 生成命令 | 每个服务自带的 Makefile 已经能覆盖单服务需求，9 个服务规模下手动维护成本不高 |
| CI 按路径触发 | CI 构建时间因为"改一个服务却全量构建所有服务"变得明显过长 | 服务少时全量构建通常几分钟内完成，暂不构成瓶颈 |

**给 9 个服务规模的建议**：现阶段大概率**不需要**引入以上任何一项，先把结构对齐官方跑顺，等服务数量明显增长（比如 20+）或团队规模扩大到多团队并行开发时，再按上表信号逐项评估引入。log-service 降级为 `platform/logging` 是个例外——因为它本身就是要把一个独立服务降级为库，`platform/` 只是承载这个库的容器目录，即使团队暂不打算大规模引入 platform 分层，也可以只为这一个用途新建这一个子目录，不需要整体照搬。

---

## 9. 单服务迁移 Checklist

```
服务名：____________________

[ ] 用 kratos new app/<service> --nomod 生成新骨架
[ ] proto 已迁移到 api/<service>/v1/（扁平结构，不分领域/类型两层）
[ ] proto client/server 代码已生成
[ ] 脚手架自带 greeter 示例已清理
[ ] 业务代码已迁移到 biz/data/service 对应层
[ ] proto-based conf（暂缓，详见第 13 节）
[ ] 服务本地 openapi.yaml（暂缓，详见第 13 节）
[ ] wire 已重新生成，无报错
[ ] go mod tidy 无冲突
[ ] go build ./app/<service>/... 通过
[ ] 单元测试通过
[ ] 服务自带的 Dockerfile/Makefile 已确认可用（无需额外建集中式 deployments 目录）
[ ] 本地启动服务（make run 或 kratos run），接口行为与迁移前一致
[ ] 与该服务有调用关系的其他服务，联调测试通过
[ ] 旧目录已删除
[ ] PR 已提交并 review 通过
```

---

## 10. 风险与回滚预案

| 风险点 | 应对措施 |
|---|---|
| wire 依赖注入迁移后编译失败 | 保留旧目录直到新服务本地验证通过，随时可 revert 分支 |
| 服务间调用地址因路径变化失效 | gRPC/HTTP 监听端口和服务发现注册名保持不变，只改构建路径 |
| relay-gateway 作为主项目迁移时改动面大 | 放在最后一批，且用"临时 scaffold + 手动合并"的方式，而非直接在生产分支上原地改造根目录 |
| 迁移过程中断，新旧路径混杂 | 每个服务独立 PR、独立分支，可单独回滚 |
| 误引入不必要的 platform/client 等非官方扩展 | 第 8 节的信号表作为引入前的强制检查项，PR review 时确认是否真的触发了信号 |

---

## 11. 工作量预估

| 阶段 | 内容 | 预估人日 |
|---|---|---|
| 主项目骨架搭建 | relay-gateway-scaffold 生成 + 目录规划确认 | 1 人日 |
| 第一批（3 个无依赖服务） | 打样迁移，固化流程 | 3-4 人日 |
| 第二批（3 个中等依赖服务） | 含 log-service 降级评估 | 2 人日/服务 |
| 第三批（identity） | 核心服务，充分联调 | 3 人日 |
| 第四批（admin + relay-gateway 主项目改造） | 收尾 + 全链路联调 | 4-5 人日 |
| 收尾 | 文档、复盘 | 1 人日 |
| **合计** | | **约 16-20 人日** |

比 v2 方案（20-26 人日）少，因为去掉了默认铺开 `platform/`、`client/`、CI 路径触发这些非官方扩展的工作量，改成按需引入。

---

## 12. 与官方结构的对齐说明（本次修正的内容记录）

| v2 版本的问题 | 本版修正 |
|---|---|
| `app/<domain>/<type>` 五分类（interface/service/admin/job/task） | 改为官方的 `app/<service-name>` 扁平结构，不分类 |
| proto 放 `api/<domain>/<type>/v1/` | 改为 `api/<service>/v1/` 扁平结构 |
| 集中式 `deployments/docker/` 存放所有 Dockerfile | 改为每个服务自带 Dockerfile/Makefile（官方 `kratos new app/xxx --nomod` 自动生成） |
| 顶层项目视为纯容器，不承担业务 | 明确主项目（relay-gateway）本身是一个正常服务，有自己的 cmd/internal |
| `platform/`、`client/`、CI 路径触发默认纳入方案 | 改为"非官方扩展"，需满足第 8 节信号才引入，避免过度设计 |

这一版的目录结构、CLI 命令、迁移步骤均与官方 `kratos-layout` 模板及 [CLI 工具文档](https://go-kratos.dev/zh-cn/docs/getting-started/usage/) 的实际输出保持一致。

**补充说明（cmd 路径与 third_party 目录的确认）**：
- `third_party/` 目录本方案第 1、4 节均已包含，全局共用一份，放仓库根目录
- 每个子服务的 `cmd/<service>/` 嵌套在自己的 `app/<service>/` 目录下，不会跑到根目录 `cmd/`；根目录 `cmd/` 只属于主项目自己（本方案里是 relay-gateway）。这一点已经过官方 [kratos-layout AGENTS.md](https://github.com/go-kratos/kratos-layout/blob/main/AGENTS.md) 和 CLI 文档实际输出的树交叉核实，AGENTS.md 中 `cmd/<app>/` 描述的是单服务模板自身的结构，在"大仓模式"下这份模板被完整复制进每个 `app/<service>/` 子目录，因此嵌套关系不变
- 第 7.6 节新增了 AGENTS.md 里更细致的 DTO/DO/PO 三层数据流转规范，这是 v3 之前版本缺失的内容，比单纯的目录搬家更能保证迁移后的代码符合官方分层契约

---

## 13. 暂缓项说明（proto-based conf 与 per-service openapi.yaml）

> **决策记录**：v3-corrected 原方案要求每个服务使用 Kratos proto-based conf（`internal/conf/*.proto` + `make config` 生成）并生成服务本地 `openapi.yaml`。经评估，这两项在当前阶段暂缓执行，理由如下。此节是对第 4 节目录结构和第 7.6 节迁移映射表中相关条目的正式修订，验收时应以本节为准。

### 13.1 proto-based conf 暂缓

**现状**：所有 `app/*/internal/conf/config.go` 和 `internal/conf/config.go` 均为手写 Go struct，运行时行为正确，配置加载和 YAML 解析均工作正常。

**暂缓理由**：

1. **不影响运行时行为**：proto-based conf 与手写 struct 在功能上等价，仅是定义方式不同（proto → 生成 Go struct vs 直接写 Go struct）。当前手写 struct 已能满足所有服务的配置需求。
2. **改动面大但收益低**：9 个服务 + 主项目共 10 份 conf 需要转换为 proto 定义，还需调整 `make config` 生成目标、更新所有引用路径，属于纯机械性重构，不改变运行时语义。
3. **依赖 `internal/conf/conf.proto` 基础设施**：当前仓库根目录 `internal/conf/` 也没有 conf.proto（只有手写 `config.go`），需要先建立全局 conf.proto 模板，再逐个服务迁移。
4. **风险可控**：手写 struct 的类型安全性由 Go 编译器保证，不会引入运行时错误。

**后续触发条件**（满足任一即可启动迁移）：

- 需要对配置做 schema 校验（如启动时校验必填字段、类型约束）；
- 配置字段频繁变动，团队希望用 proto 作为配置 schema 的 single source of truth；
- 引入 Kratos 配置中心（如 Nacos/Consul KV）需要 proto 描述配置结构。

**验收替代标准**：在 conf 暂缓期间，以下条件视为"配置层已就绪"：

- [x] 每个服务的 `internal/conf/config.go` 能正确解析 `configs/config.yaml`；
- [x] `CONF_PATH` 环境变量在 Dockerfile / docker-compose / k8s / Makefile 中一致指向 `/configs/config.yaml`；
- [x] `make run` 能正确加载服务本地配置。

### 13.2 per-service openapi.yaml 暂缓

**现状**：仅仓库根目录 `openapi.yaml` 一份，由 `make api` 从所有 `api/*/v1/*.proto` 统一生成。各子服务目录下没有独立的 `openapi.yaml`。

**暂缓理由**：

1. **当前单一 openapi.yaml 已覆盖所有服务**：`make api` 的 `--openapi_out=.` 输出到仓库根目录，包含全部 9 个服务 + 主项目的 HTTP API 定义，功能上无缺失。
2. **per-service openapi 需要拆分 protoc 调用**：当前 `make api` 一次性编译所有 `API_PROTO_FILES`，拆分为 per-service 需要每个服务独立 `protoc --openapi_out=app/<service>/openapi.yaml`，改动 Makefile 生成逻辑。
3. **下游消费方无拆分需求**：前端、API 网关、文档站点均引用根 `openapi.yaml`，没有按服务拆分引用的场景。
4. **不影响 API 行为**：openapi.yaml 是文档产物，不参与运行时。

**后续触发条件**（满足任一即可启动拆分）：

- 服务独立部署后，前端需要按服务获取独立的 API 文档；
- 团队引入 API gateway 按服务路由，需要 per-service schema；
- 服务数量增长到需要独立文档站点按服务浏览。

**验收替代标准**：在 per-service openapi 暂缓期间，以下条件视为"API 文档层已就绪"：

- [x] 仓库根 `openapi.yaml` 存在且 `make api-check` 通过；
- [x] `openapi.yaml` 包含所有服务的 HTTP 路径定义。

### 13.3 验收结论

在 proto-based conf 和 per-service openapi.yaml 两项暂缓期间，v3-corrected 的验收以第 13.1 和 13.2 节的"验收替代标准"为准。当后续满足触发条件时，应在独立 PR 中完成迁移，并更新本节状态。
