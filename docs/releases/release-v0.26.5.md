# Micro-One-API v0.26.5 发布：Responses 用量来源归因修复

> 2026-09-02 · 上一版：[v0.26.4](./release-v0.26.4.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.26.5)

v0.26.5 是 v0.26.4 之后的 **PATCH Relay 计费归因修复版本**：修复 legacy `/v1/responses`
与显式 OneAPI 渠道成功结算时未写入 `source_kind` / `upstream_model_id` 的问题，
使 canonical usage observe 记录能还原实际上游来源和定价模型。本版同时修正 release note
提交被 commit-body 门禁误报的 CI 规则。

**无公共 API / proto 变更、无新增数据库迁移、无新增配置项**。受影响的运行时服务仅为
`relay-gateway`；从 v0.26.4 升级无需重复执行迁移，从更早版本升级仍需按顺序应用至 `089`。

## 1. Responses 成功结算补齐上游来源

**根因**：legacy Responses 的普通、流式、fallback 与 previous-response stored-route 成功分支
构造 canonical usage envelope 后，遗漏了已有的 `applyChannelInputs`；显式 OneAPI 渠道路径
也未把已选渠道的上游模型与来源写入 usage log input。这些记录的五桶数值、dedupe key、
pricing hash 和 verified verdict 均正常，但来源覆盖率无法达到 canonical observe 要求的 100%。

**修复**：在 12 个 legacy Responses 成功结算分支统一应用渠道输入，覆盖流式 /
非流式、直连 / fallback 和 stored-route；OneAPI 显式渠道结算显式设置
`source_kind=channel` 与渠道 `upstream_model_id`。回归测试同时断言普通 Responses、
流式 Responses、previous-response 和 OneAPI 的 billing commit 归因。

**影响服务**：`relay-gateway`。不改变 Token 计数、价格、扣费模式、路由选择或对外响应。

## 2. Release note 提交与 commit-body 门禁对齐

**根因**：仓库规范将 release-notes-only 提交列为可以只有标题的平凡提交，但
`scripts/check-commit-bodies.sh` 的豁免正则只包含 `docs(changelog)`，未包含实际使用的
`docs(release)`，导致合并发布历史后 Backend gate 误报。

**修复**：将 `docs(release)` 加入已记录的平凡主题正则，非平凡代码提交仍必须包含
根因与影响说明。

**影响服务**：仅 CI，无运行时影响。

## 3. 生产验证与 observe 起点

**根因与处置**：Executor 第五次观察因 Messages 流式 P95 回归约 69.7% 于
2026-09-02 10:50:06 CST 回滚到 legacy。回滚后首批 Responses 样本暴露上述来源缺失；
修复等价代码已于 11:05:58 CST 紧急部署，首条合格自然样本于 11:12:00.225 CST 写入。

**验证**：该样本为 `/v1/responses` 流式请求，记录 `source_kind=channel`、
`upstream_model_id=glm-5.3`、v1/verified/openai_subset，`cost_mismatch=0`、
ambiguous=0、dedupe 唯一，billing/log 归因一致。

**影响服务**：当前生产 `relay-gateway` 已运行与 v0.26.5 修复等价的镜像；本次
发布 tag 用于固化可重现制品，**不因打 tag 重建或重启当前生产容器**，以免中断
自 11:12:00.225 CST 起算的 48 小时 canonical observe。

## 兼容性说明

- **API / proto**：无新增或破坏性公共 API、HTTP 路由或 proto 变更。
- **数据库**：无新增迁移；v0.26.4 的 `089` 仍是当前最新迁移。
- **配置**：无新增配置项；canonical usage 仍遵循 `observe → charge` 顺序。
- **运行时**：只改变成功结算的审计来源字段；无重复 ledger、余额或价格变更。
- **部署**：未运行修复的环境需更新 `relay-gateway`；已运行 2026-09-02 紧急修复镜像的
  生产环境在 observe 满窗前不重启。
- **回滚**：可回滚 `relay-gateway` 镜像；若回到 v0.26.4，应同时关闭
  `RELAY_CANONICAL_USAGE_PRODUCER`，避免继续生成缺失来源的 observe 记录。

## 升级步骤

```bash
git fetch --tags
git checkout v0.26.5
```

1. 从 v0.26.4 升级时无需新的数据库迁移；从更早版本升级时先应用至 `089`。
2. 在本地或 CI 交叉构建 `linux/amd64` 的 `relay-gateway`，不在资源受限的生产主机构建。
3. 对未运行本修复的环境，仅更新 `relay-gateway`：
   `docker compose up -d --no-deps relay-gateway`。
4. 若生产已运行 2026-09-02 紧急修复镜像，只发布 tag 和制品，不为替换成 tag 镜像而
   中断当前 48 小时 observe。
5. 验证新 Responses / OneAPI 成功记录的 `source_kind`、`upstream_model_id`、
   pricing hash 和 dedupe key 完整，且 billing/log 归因一致。

## 验证

- `make verify`（Go unit/race、architecture、migration-check、前端 lint/test/build）
- `go test ./internal/server`
- `go test -race ./internal/server/...`
- `git diff --check`
- 生产首条合格自然样本：`source_kind=channel`、`upstream_model_id=glm-5.3`、
  v1/verified/openai_subset，billing/log 各 1 条且归因一致

## 完整变更日志

- fix(ci): exempt release-notes commits from body gate
- fix(relay): restore Responses source attribution
