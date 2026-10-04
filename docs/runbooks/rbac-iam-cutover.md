# 平台 IAM 生产交接执行手册

> D1 执行清单；实施方案第 7 节的状态顺序是硬门槛。未建立真实屏障和旧写通道隔离，不运行 `block`。任何失败保持屏障，不恢复旧写二进制。

## 部署前准备

1. 核准停写窗口、维护入口、实际部署目标及 root 审查的自定义角色/受限委派 manifest。空 manifest 也须明确审查；不能拿猜测的用户/组范围放权。清点所有实例、CLI、脚本、任务和 users 写者，含财务路径。
2. 在本机从已验证源码交叉构建全部 9 个服务的 `linux/amd64` 镜像，并为镜像加 `micro-one-api.source.digest=<源码 SHA256>` 标签。运输并先部署兼容 IAM 的 legacy 版本，逐项核对 image ID、digest、服务身份与健康；保留同一完整版本的回滚镜像。服务器不得构建镜像。
3. 前端单独构建与发布 `/opt/web/dist`，备份并核对实际资源摘要；admin-api 镜像替换不更新前端挂载。
4. 用 `prepare-rbac-service-identities.py --output <新的私密文件>` 准备 9 个独立 outbound token 与按真实 fixed method 生成的 receiver map。核对 `check-service-identities.py --env <文件>` 后一并安装；不只改 caller 名称，也不继续使用共享 token 当服务身份。
5. 按 migrations ownership 和实际跨库调用核准各服务的 DB GRANT。配置 `IDENTITY_SQL_DSN`、`BILLING_SQL_DSN` 等 Compose 覆盖。服务账号不能持有全库 root 或 DDL 权限；identity 以外不能写身份授权列。迁移通道独立、限时启用且不注入普通容器。
6. MySQL/PostgreSQL 财务用户仅能更新 users 的 `balance/frozen_amount/used_amount/request_count`，不授予 users INSERT/DELETE 或角色/凭证写；账本、预留、幂等记录等财务表保持其所有者必要权限。实际审查 GRANT，而不是只看账号名称。
7. SQLite 或共享凭证无法有效列隔离时，暂停 relay 流量、结算/消费/支付回调及相关任务，先排空所有在途预留/事务，记录可重放任务和幂等键。屏障期间持久队列/上游回调必须保留，恢复时按原幂等流程重放；不丢弃财务写。
8. 运行 `rbac-cutover-preflight.py --remote "$DEPLOY_REMOTE_SERVER" --source-digest "$SOURCE_DIGEST"`，保存脱敏报告；检查实际 GRANT、旧写者退出、直接 HTTP/gRPC/兼容入口拒绝与六组强制回归的独立证据。没有报告或任意能力未核验时不推进。

## 唯一迁移工具与 root 核准

本机编译迁移工具，也采用 linux/amd64；`SOURCE_DIGEST` 必须是本次已经验收的源码 SHA256，不是分支名或猜测的值。`rbac-source-digest.py` 对 Git 清点的构建输入按路径/内容长度加 SHA256；排除 docs 和运行时环境文件，包含未提交的新源码，构建前后摘要必须一致。

```sh
SOURCE_DIGEST="$(python3 scripts/rbac-source-digest.py)"
docker buildx build --platform linux/amd64 --load --progress=plain \
  -f app/identity/Dockerfile \
  --build-arg SERVICE_NAME=iam-migrate \
  --build-arg SERVICE_PATH=./app/identity/cmd/iam-migrate \
  --build-arg "BUILD_EXTRA_LDFLAGS=-X main.sourceDigest=$SOURCE_DIGEST" \
  --label "micro-one-api.source.digest=$SOURCE_DIGEST" \
  -t micro-one-api/iam-migrate:cutover .
```

该构建在本机现有 Docker/buildx 的目标平台阶段使用 CGO，适用于三种数据库。用 docker save/scp/docker load 运输镜像，生产只运行镜像；不在 arm64 宿主上直接使用没有 C 交叉工具链的 GOARCH 命令，也不在服务器构建。

root 操作者离线执行 `iam-migrate keygen -private-key <私钥> -public-key <公钥>`，通过独立管理通道配置迁移进程的 `IAM_MIGRATION_TRUST_KEY_FILE`。私钥不得复制到迁移容器；丢失的回执不需要重新签署或再次生成密钥。

迁移进程只配置 `IAM_MIGRATION_DSN/DRIVER/SCHEMA`，执行 `status` 得到 policy CAS、编译源码摘要和数据库通道指纹。`inventory` 返回全部用户与待审查的 `Manifest`（角色/分配/委派）。报告含用户/策略关系，保存为私密文件，不能发布到公开日志。

root 核准的证据 payload 字段：`BatchID`、`SourceDigest`、`DatabaseIdentity`、`ManifestDigest`、`RootUserID`、UTC `CapturedAt/ExpiresAt`，以及 `Barrier/Drained/OldWritersExited/OldDBChannelsRevoked/FinanceIsolation/FinanceReplay/Rollback/Frontend/Regression` 的实际证据引用，`Instances` 包含 `admin/identity/channel/billing/config/log/monitor/notify/relay` 九类所有运行实例的镜像/能力证据。有效期最多 24 小时。用 `iam-migrate manifest-digest -manifest <已审查manifest.json>` 离线计算精确 `ManifestDigest`；工具按 `IAMMigrationDigest` 的 jsonx 标准序列化规则计算，不自行换成 proto JSON 或对文件原始字节求哈希。签署命令会规范化 JSON 后签署准确的保存字节。

```sh
iam-migrate sign-evidence -payload <已审查payload.json> \
  -private-key <root私钥> -output <新的证据文件.json>
```

一个 reference 不能用“已确认”等占位语句代替实际证据。重签新的 evidence 不改变已完成状态，也不能恢复 legacy 写。root 的启用状态及 root 分配在真正推进时仍会重新核实。

## 固定执行顺序

每个写子命令传 `-batch`、`-expected-policy-revision`（最近 status）、唯一稳定 `-request-id`、`-reason`。`block/rebuild/import/activate/complete/resume` 另传 `-evidence`，有自定义关系时传同一已核准 `-manifest`。不同阶段或更换内容使用新的 request ID；丢响应原样重试，不能更换内容复用同 ID。

1. legacy/idle 执行 `inventory → apply → shadow`。候选只用于比较。审查未知角色、默认/启动分配、异常差异、预期认证/路由收紧，及所有不活跃用户；异常差异不通过加权限自动消除。
2. 建立所有入口的外部维护屏障，停止全部旧账号/角色写者和任务，排空请求/事务，撤销旧进程/脚本 DB 写通道。对残留旧连接实际执行身份写拒绝探针。立即执行 `block` 持久化 batch；若记录失败仍保持屏障。
3. `rebuild` 以最新数据库旧 role 重建/对账全部用户，不只补缺行。有 root 核准的受限角色/委派时执行 `import`，再 `verify`。新建角色先获得实际 ID；重新导出完整 Manifest，由 root 核准最终 ID/关系/范围/时间，再作为 activate 的 signed manifest。导入是整批原子操作；失败不留下部分角色或关系。
4. `activate` 再次全量重建、目录/身份/约束/shadow 验证，审查报告，同事务记录 `iam/verified` 与验证时间。保持停写，此后不再根据 `users.role` 重建。任何实例不得回退至旧粗粒度 guard。
5. 全部 IAM 兼容实例与前端部署完毕，核对 source digest、独立身份、直接入口、救援默认关闭、root/受限管理员日常流程。root 以新的真实证据签署 complete，然后执行 `complete`。
6. 确认 `iam/complete` 后仅恢复新账号默认分配/授权治理/审计写链、业务流量与财务幂等重放。验证资金无丢写/重复，旧角色 CLI/旧回填仍拒绝；保存生产成功报告与审计事件 ID。

## 故障恢复

- idle 候选失败：legacy 继续为事实源；修正未知角色或报告问题后重建候选，不能让候选放权。
- blocked 失败：保持维护/停写，修复失败依赖，以同 batch 完整 `rebuild/import/activate` 或 `resume`。迁移部分结果和成功审计在同一事务内，不靠人工补缺行恢复。
- verified 失败：只检查 IAM 关系和执行能力，修复后 `complete/resume`；不要运行旧回填，不重启旧写者。
- complete 后故障：只回滚到事先验收的 IAM 兼容镜像和前端；保留 IAM 为事实源。回到数值角色模型需要另一次停写、人工映射审查和反向全量对账，本工具不提供自动降级。
- 签名/身份/源码/通道/CAS/审计/依赖失败：请求拒绝。未经新审查不能修改 expected revision 来重试旧内容，不能伪造签名引用或跳过屏障。
