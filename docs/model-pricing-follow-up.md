# 模型价格与上游成本后续工作

## 当前约定

- 用户价格以公开模型 ID 为键，并统一使用小写规范名，例如 `glm-5.2`。
- 同一个公开模型无论命中普通渠道、订阅账号还是其他供应商，都使用同一份 `ModelPrice` 售价。
- `GLM-5.2`、`glm-5.2` 和上游的 `z-ai/glm-5.2` 不应分别建立用户价格。
- 上游真实模型 ID 只用于路由映射，不出现在用户价格表。
- 线上 relay 使用 `BILLING_MODEL_SOURCE=requested`，账单按用户请求的公开模型名查询 `ModelPrice`。
- 未填写价格的模型不会写入 `ModelPrice`，价格页展示候选模型不等于已经启用收费。

## 后续事项

> v0.11.0 Phase 2（§2.1 规范 ID、§2.2 成本治理）实现状态：第 1-6 项已落地代码、
> 迁移与回归测试，第 7 项为上线后核对动作，详见各条状态标记。

1. ✅ **清理模型库中的历史重复记录**（v0.11.0 Phase 2 §2.1）。新增只读
   `CanonicalModelPreflight` 报告（`/api/admin/models/canonical/preflight`）列出
   重复模型及其别名、渠道映射、订阅映射、使用统计依赖计数，以及引用了重复 id 的
   `ModelPrice`/`UpstreamModelPrice` 价格键（admin-api 读取 system_options 后回填到
   报告的 `price_references` 字段，channel-service 自身不耦合定价存储）；新增事务式
   `MergeCanonicalModels`（`/api/admin/models/canonical/merge`）把所有外键和统计
   重指向到幸存行后再删除重复行，发现真实键冲突时整体回滚并返回
   `MODEL_CANONICAL_CONFLICT`，不做 `INSERT IGNORE` 式静默覆盖。线上执行顺序：
   preflight → merge → 068 迁移。
2. ✅ **数据库级规范 ID 约束**（v0.11.0 Phase 2 §2.1）。迁移 068
   （MySQL 函数索引 `LOWER(TRIM(model_id))`）/ postgres 005 / sqlite 006 先把存量
   `model_id` 规范化为小写，再创建大小写不敏感唯一索引；若仍存在仅大小写不同的
   重复，UPDATE 会在旧唯一键上冲突，整批迁移回滚，强制运维先跑 merge。
   `biz.NormalizeModelID` 仍是 create / update / alias / 自动发现的唯一入口。
3. ✅ **独立上游成本键 + 管理视图 + 旧键迁移工具**（v0.11.0 Phase 2 §2.2）。
   固定成本键命名 `channel:<id>:<upstream_model_id>` 与
   `subscription:<id>:<upstream_model_id>`；
   `billing.calculateUpstreamCostWithUsage` 先查规范键，再回退到旧
   `<channel_id>:<model>` 键和裸模型 ID，兼容存量配置。relay 把
   `plan.Channel/Account.UpstreamModelID` 与来源类型通过 `CommitQuotaRequest`
   的 `upstream_model_id` / `source_kind` 字段传到 billing。新增独立管理视图
   `GET/POST/DELETE /api/admin/upstream-costs`（按来源类型/名称/精确上游 ID 配置，
   不写入公开模型售价）和旧键迁移工具 `POST /api/admin/upstream-costs/migrate`
   （默认 dry-run，把 `<channel_id>:<model>` 转为
   `channel:<id>:<upstream_model_id>`，冲突跳过并报告）。
4. ✅ **订阅账号成本稳定键**（v0.11.0 Phase 2 §2.2）。`source_kind=subscription`
   时用 `subscription:<account_id>:<upstream_model_id>`，与普通渠道命名空间彻底
   分离，即使数值 id 相同也不会混淆（见
   `TestCalculateUpstreamCostWithUsage_SubscriptionVsChannel`）。
5. ✅ **未定价审计 + 审计事件 + 指标 + 保存前集成**（v0.11.0 Phase 2 §2.2）。
   新增 `/api/admin/models/unpriced`（`ListUnpricedRoutedModels` RPC），admin-api 从
   `ModelPrice` 系统选项读取已定价集合，channel-service 计算差集，返回公开、启用、
   有可用上游映射但没有 `ModelPrice` 的模型清单。未定价不阻断保存，只作醒目状态。
   每次查询更新 Prometheus 指标 `micro_one_api_model_unpriced_routed{source=channel|subscription}`
   并写结构化审计事件（含操作者、模型清单、request id）；价格保存
   （`PUT /api/option` key=ModelPrice）成功后响应体携带 `unpriced_routed_count`。
6. ✅ **计费回归用例**（v0.11.0 Phase 2 §2.2）。
   `TestCalculateUpstreamCostWithUsage_KeyResolution` 与
   `TestCalculateUpstreamCostWithUsage_SubscriptionVsChannel` 覆盖：同一公开模型走
   channel / subscription / 不同供应商时，用户售价一致、上游成本按键分别记录。
7. ⏳ **线上一致性审计**（上线后执行）。价格正式录入后，对 `/v1/models` 返回值、
   计费日志 `model_name`、`ModelPrice` 键做一次核对；`/v1/models` 与日志已统一
   小写化，合并 + 068 迁移后存储层也规范化，核对主要用于发现历史脏数据残留。

## 上线价格时的操作顺序

1. 在模型管理页确认目标模型为公开、启用状态，并至少有一个可用上游映射。
2. 在模型价格页只填写规范模型行，例如 `glm-5.2`，金额单位为 USD / 1M tokens。
3. 保存后检查系统选项中的 `ModelPrice`，确认键为小写公开模型 ID。
4. 使用小额测试账号分别调用各个上游路径，核对用户扣费、token 数和日志中的上游供应商。
5. 完成用户售价验证后，再单独录入和验证 `UpstreamModelPrice`，不要把采购成本当作用户售价。
