# Micro-One-API v0.10.0 发布：模型管理系统 + 国内订阅账户支持

> 2026-07-25 · 上一版：[v0.9.3](./release-v0.9.3.md)（2026-07-20）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.10.0)

v0.10.0 是一次**重大功能版本**，主要引入两大核心功能：

1. **独立模型管理系统**：通过 4 个 Sprint 迭代完成从设计到实现的全过程，支持按账户配置模型映射、通配符模型匹配、模型别名管理、使用统计分析等功能，彻底解决了多渠道模型管理复杂性。
2. **国内编程计划订阅账户支持**：新增对智谱 GLM、MiniMax、Kimi 等国内平台订阅账户的支持，包括动态模型发现、配额状态查询、路由恢复探测等能力。

同时修复了多个生产问题，包括 OpenTelemetry 安全漏洞（GO-2026-5158）、Web 前端依赖漏洞、用量统计优化等。

本版**包含新增业务表迁移**，**无 API 破坏性变更**，**新增多个端点**，所有变更均向后兼容，从 v0.9.3 平滑升级即可。

## 主要变更

### 1. 独立模型管理系统（完整实现）

经过 4 个 Sprint 的迭代开发，模型管理系统现已完整实现：

**Sprint 1 - 独立模型管理系统基础架构**
- 实现独立模型管理系统设计（方案B）
- 创建完整的后端 API 和数据模型
- 建立模型映射、账户配置的核心逻辑

**Sprint 2 - 模型管理前端界面**
- 实现完整的 Web 前端管理界面
- 模型列表、账户配置、映射关系可视化
- 代码审查反馈的修复和优化

**Sprint 3 - 模型管理集成**
- 模型管理系统与网关核心集成
- 路由逻辑、负载均衡、账单统计集成
- 端到端功能验证

**Sprint 4 - 高级功能完善**
- 模型使用统计分析
- 模型别名管理
- 大小写不敏感匹配优化
- 缓存性能优化

**核心功能特性：**
- **按账户模型映射**：每个账户可独立配置可用模型列表
- **通配符匹配**：支持 `*` 通配符进行模型匹配（可通过 `RestrictModels` 开关控制）
- **模型别名**：为复杂模型名称提供简洁别名
- **使用统计**：详细的模型调用统计和用量分析
- **缓存优化**：`/v1/models` 端点响应缓存，提升性能
- **大小写不敏感**：模型 ID 匹配支持大小写不敏感

### 2. 国内编程计划订阅账户支持

**新增平台支持：**
- **智谱 GLM**：完整支持 GLM 系列模型
- **MiniMax**：支持 MiniMax 各类模型
- **Kimi**：支持 Moonshot Kimi 模型

**核心功能：**
- **动态模型发现**：通过 `/v1/models` 端点动态获取各平台可用模型列表
- **配额状态查询**：实时显示上游平台配额使用情况
- **路由恢复探测**：自动探测和恢复最优路由
- **订阅账户管理**：完整的订阅账户生命周期管理

**技术实现：**
- 专门的订阅账户路由策略
- 智能配额层级排序
- 流式用量解析优化（支持 Anthropic cache tokens）
- 错误恢复和重试机制

### 3. 安全漏洞修复

**OpenTelemetry 安全漏洞（GO-2026-5158）**
- 升级 `go.opentelemetry.io/otel` 从 v1.43.0 → v1.44.0
- 升级相关 OpenTelemetry 包（otel/sdk、otel/trace、otel/metric）
- govulncheck 验证通过，无已知漏洞

**前端依赖漏洞修复**
- 修复 4 个 Web 前端 Dependabot 告警
- 更新 axios、body-parser 等关键依赖
- npm audit 验证通过

### 4. 其他重要修复

**用量统计优化**
- 修正用量合并策略，与 OpenAI 语义对齐
- 优化流式响应的用量分块统计
- 支持 Anthropic cache_read_input_tokens 解析

**部署配置改进**
- 尊重 `DEPLOY_IMAGE_TAG_PREFIX` 环境变量
- 修正 web/dist 同步问题
- 完善 schema 拆分相关迁移文件

**Web 界面改进**
- 修复 CreateAccountDialog 滚动问题
- 规范化订阅账户数据结构
- 改善前端用户体验

## 升级步骤

```bash
# 拉取版本
git fetch --tags
git checkout v0.10.0

# 开发者环境：重装工具链并重新生成 proto
make init
make proto

# 部署环境：执行数据库迁移
make migrate

# 重新构建镜像并启动
docker compose build
docker compose up -d
```

**注意事项：**

- **数据库迁移**：本版包含新增表迁移，必须执行 `make migrate`
- **Web 缓存**：升级后建议清除浏览器缓存，确保加载最新前端资源
- **环境变量**：如使用国内订阅账户，需配置相应的平台 API 密钥
- **监控验证**：升级后请验证模型管理功能和订阅账户功能正常

## 兼容性说明

- **API**：无破坏性变更，新增多个模型管理端点
- **数据库**：包含迁移，新增模型管理相关表
- **配置**：无新增必需环境变量
- **运行时**：与 v0.9.3 完全兼容，支持平滑升级

## 验证

发布前已确认：

- `make build` 全量编译通过
- 全部单元测试通过
- 模型管理系统端到端验证通过
- 订阅账户功能集成测试通过
- govulncheck 安全扫描通过
- gosec 静态分析通过
- Web 前端 npm audit 通过

## 完整变更日志

- 9e2c211 fix: upgrade OpenTelemetry to address GO-2026-5158 vulnerability
- 858d613 fix: address model management review findings
- 05dbcef fix(channel): close model-management review follow-ups (🟡#1–#8 + low-priority)
- 1c2f134 fix(model-mgmt): fix all P0-P3 review must-fix items (#1-#7)
- a37815c feat: add model routing, load-aware account selector, billing model source (P2 #3/#7, P3 #6)
- 12bbd93 chore(deps): bump the npm_and_yarn group across 1 directory with 3 updates
- cabaf34 feat: implement P1 wildcard model matching + RestrictModels switch
- 4b790a5 chore(deps): bump body-parser
- d0fb428 feat: add per-account model mapping and /v1/models cache (P0)
- e860573 fix: add missing test stub methods and ensure model_id case sensitivity
- 32b07d5 feat: Sprint 4 model usage statistics, alias management and case-insensitive matching
- 4d33cda feat: Sprint 3 model management integration
- 9319958 feat: implement Sprint 2 model management frontend with code review fixes
- 94b5a81 feat: implement independent model management system (方案B Sprint 1)
- 977bea7 feat(channel): dynamic model discovery via /v1/models for domestic platforms
- fa3cf87 fix(deploy): respect DEPLOY_IMAGE_TAG_PREFIX and sync web/dist; fix(migrations): include subscription_account_abilities and account_quota_snapshots in schema_split
- d09e6e2 fix(channel): route recovery probe for claude and domestic coding-plan accounts
- 0992a2b fix(channel): correct zhipu quota tier fallback sort in coding-plan probe
- 412701a refactor(web): normalize subscription-account payload at the API boundary
- 6172bdc feat(subscription-accounts): show upstream coding-plan quota status for Zhipu/Kimi/MiniMax
- c4b942b fix(web): make CreateAccountDialog scrollable when content overflows
- a176de2 fix(relay): use nil-safe GetKimi() getter for KimiOAuth config
- adc9742 chore(deps): patch 4 open Dependabot alerts in web
- f3c5955 fix(relay): clean up cn-subscription commit artifacts from 7cfeec0
- 7cfeec0 feat(relay): support domestic Coding Plan subscription accounts (Zhipu GLM / MiniMax / Kimi)
- 79bd4dc fix(relay): clarify usage merge strategy and align stream usage chunk with OpenAI semantics
- ec3a9b8 fix(relay): parse Anthropic cache_read_input_tokens and stream usage for GLM
- 93c70dd chore(deps): bump axios
- 4407a6e docs(design): add CN subscription accounts roadmap (Kimi/MiniMax/Zhipu)
- d5763a8 docs: update README for v0.9.x releases

## 下一步

后续版本计划：

- 模型管理高级功能（批量操作、导入导出）
- 更多国内平台订阅账户支持
- 模型使用成本分析和优化建议
- 进一步性能优化和缓存策略改进

欢迎反馈与参与：[github.com/mengbin92/micro-one-api](https://github.com/mengbin92/micro-one-api)
