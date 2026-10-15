# P2 develop 合并与生产部署

2026-10-14，按用户要求修复 CI、合并 develop 并更新线上。合并代码为 `5bd327517870cb48633b44a9f7bf4b38d5b45c75`，源码摘要 `87e372faca54b2891c5c39fe93a9363a07d5042cedf860c693bf16ed47e8b20d`。部署后追加本记录；未更新 main 或创建版本标签。

关联：[合并前完整验证](p2-final-validation-2026-10-14.md)、[部署证据](evidence/billing-upstream-p2-deploy-2026-10-14/index.json)、[P2 工程交付](billing-upstream-p2-completion-2026-10-14.md)、[DeepSeek 修复](deepseek-catalog-repair-2026-10-14.md)。

## 制品与迁移

identity-service、billing-service、channel-service、admin-api、relay-gateway、monitor-worker 六个服务均在本机通过标准 Dockerfile 跨构建 linux/amd64，生产主机只加载镜像。统一镜像标签为 `deploy-20261014-131552-5bd32751`，镜像 ID 见证据索引。六个服务健康检查均通过、restart=0，不可变镜像标签的源码摘要与本地一致。生产 Compose 的旧容器标签会覆盖同名镜像标签，因此采集器按实际 image ID 读取镜像元数据，同时保留 container_source_digest，不把旧容器标签当成新制品身份。

通过本机跨构建的正式 migrate CLI，在 MySQL 本地 socket 管理通道执行 billing 132、channel 133、identity 134；迁移记录均已确认。运行身份只增加所属七张新表的 SELECT/INSERT/UPDATE/DELETE，未授予全库或 DDL 权限。IAM catalog revision=2、policy revision=7、mode=iam、cutover=complete；七项新权限均为 draft/unbound，没有新增角色授权。

前端独立发布到宿主机 `/opt/web/dist`，index.html SHA256=`e28b15663a13da96140640def799b50d578ba77f163577acfb08409d68e2b0dc`，与本机相同。旧哈希资源保留供既有浏览器页面使用。

## 现场结果与门禁

- 管理员登录会话正常；实际浏览器渠道管理中，渠道 1 为 OpenAI / 正常 / 启用，渠道 11 为 DeepSeek / 正常 / 启用。owner 状态均 healthy、circuit_opened_until=0。
- 目录实际包含模型 519 `deepseek-flash`。管理员在部署期间执行启用后禁用，审计为 actor 1 的正常 model.enable/model.disable；最终配置 status=0、private，部署保持该最后决定，没有覆盖管理员操作。
- 渠道 11 库存已有 `deepseek-flash`、`deepseek-v4-pro`，最近三次库存探测均 inventory_observed / HTTP 200；新 relay/monitor 启动后 05:25:51 UTC 的实际探测再次成功。对应两条模型映射仍 disabled。渠道 1 原映射保持原值。
- `UPSTREAM_CONTRACTS_V1_ENABLED=false` 明确配置于 channel/relay/monitor；生产 binding 数为 0。新 vendor report 数为 0，未把离线材料伪装为生产导入。
- P1 execution delivery、receipt、probe 开关继续开启；native usage v2 收费名单仍为空。monitor 保持 5m、四个目标，未扩大每日预算或发起额外真实付费推理。

持久 outbox 验证全部 ACK、pending=0。部署后追加扫描器兼容性修复和安全说明注释，运行逻辑与所部署合并版本相同；完整扫描没有发现项或类型导入错误。

完整代表性供应商账期、其他计费来源和生产契约灰度/晋级窗口仍按设计方案独立验收。DeepSeek 六次受控调用的官方费用 `0.14036956 CNY` 已核对，但不能替代完整账期验收。工程部署不代表 P2 全部现场门槛已通过。

## 备份与回退

- 配置、三个所属数据库完整备份：`/opt/micro-one-api/docker-compose/backups/p2-20261014-051310`，目录 0700、文件 0600，压缩完整性检查通过。
- 六个原镜像保留 `rollback-20261014-131552`；Compose 镜像修改也有对应备份。此前 DeepSeek 单独修复的备份继续保留。
- 前端备份：`/opt/web/dist.bak.p2-20261014-051624`。
- 回退先停新准入，保留新消费者及持久 outbox 排空，不删除账本、新表、证据或持久卷。配置备份含秘密，保留于生产主机，不进入 Git。
