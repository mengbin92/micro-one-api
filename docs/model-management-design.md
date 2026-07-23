# 独立模型管理系统设计文档 (方案B)

## 1. 概述

当前系统的模型配置分散在渠道和订阅账户中，缺乏统一的模型管理。本设计提出独立的模型管理系统，支持：
- 查看所有可用模型
- 全局启用/禁用模型
- 追踪模型在哪些渠道/订阅账户中可用
- 灵活的模型分组和标签

## 2. 数据模型设计

### 2.1 models 表 (新增)

```sql
CREATE TABLE models (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    model_id VARCHAR(255) NOT NULL UNIQUE COMMENT '模型唯一标识，如 gpt-4, claude-3-5-sonnet-20250619',
    display_name VARCHAR(255) NOT NULL COMMENT '显示名称',
    description TEXT COMMENT '模型描述',
    provider VARCHAR(100) COMMENT '提供商: openai, anthropic, zhipu, etc.',
    model_type VARCHAR(50) COMMENT '模型类型: chat, completion, embedding, image',
    context_window INT COMMENT '上下文窗口大小',
    pricing_input DECIMAL(10,6) COMMENT '输入价格/1K tokens',
    pricing_output DECIMAL(10,6) COMMENT '输出价格/1K tokens',
    
    -- 状态管理
    status TINYINT DEFAULT 1 COMMENT '状态: 0=禁用, 1=启用, 2=测试中',
    is_public BOOLEAN DEFAULT TRUE COMMENT '是否公开显示给用户',
    
    -- 能力标签
    capabilities JSON COMMENT '能力标签: ["vision", "function_calling", "streaming"]',
    tags JSON COMMENT '自定义标签: ["large-context", "fast"]',
    
    -- 分组和层级
    category VARCHAR(100) COMMENT '分类: large-language, image, audio',
    tier VARCHAR(50) COMMENT '等级: entry, standard, premium',
    
    -- 元数据
    metadata JSON COMMENT '扩展元数据',
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    
    INDEX idx_provider (provider),
    INDEX idx_status (status),
    INDEX idx_type (model_type),
    INDEX idx_category (category)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='模型主表';
```

### 2.2 model_aliases 表 (新增)

```sql
CREATE TABLE model_aliases (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    model_id BIGINT NOT NULL COMMENT '关联的模型ID',
    alias VARCHAR(255) NOT NULL COMMENT '别名',
    is_primary BOOLEAN DEFAULT FALSE COMMENT '是否为主别名',
    created_at BIGINT NOT NULL,
    
    UNIQUE KEY uk_alias (alias),
    FOREIGN KEY (model_id) REFERENCES models(id) ON DELETE CASCADE,
    INDEX idx_model_id (model_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='模型别名表';
```

### 2.3 model_channel_mapping 表 (新增 - 替代当前 channels.models)

```sql
CREATE TABLE model_channel_mapping (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    channel_id BIGINT NOT NULL COMMENT '渠道ID',
    model_id BIGINT NOT NULL COMMENT '模型ID',
    enabled BOOLEAN DEFAULT TRUE COMMENT '是否启用',
    priority INT DEFAULT 0 COMMENT '优先级',
    config JSON COMMENT '特定于此渠道-模型组合的配置',
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    
    UNIQUE KEY uk_channel_model (channel_id, model_id),
    FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE CASCADE,
    FOREIGN KEY (model_id) REFERENCES models(id) ON DELETE CASCADE,
    INDEX idx_channel_id (channel_id),
    INDEX idx_model_id (model_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='渠道-模型映射表';
```

### 2.4 model_subscription_mapping 表 (新增 - 替代当前 subscription_account_abilities.models)

```sql
CREATE TABLE model_subscription_mapping (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    subscription_account_id BIGINT NOT NULL COMMENT '订阅账户ID',
    model_id BIGINT NOT NULL COMMENT '模型ID',
    group_name VARCHAR(100) NOT NULL COMMENT '用户组',
    enabled BOOLEAN DEFAULT TRUE COMMENT '是否启用',
    priority INT DEFAULT 0 COMMENT '优先级',
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    
    UNIQUE KEY uk_account_model (subscription_account_id, model_id, group_name),
    FOREIGN KEY (subscription_account_id) REFERENCES subscription_accounts(id) ON DELETE CASCADE,
    FOREIGN KEY (model_id) REFERENCES models(id) ON DELETE CASCADE,
    INDEX idx_subscription_account_id (subscription_account_id),
    INDEX idx_model_id (model_id),
    INDEX idx_group (group_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='订阅账户-模型映射表';
```

### 2.5 model_usage_stats 表 (新增)

```sql
CREATE TABLE model_usage_stats (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    model_id BIGINT NOT NULL COMMENT '模型ID',
    date DATE NOT NULL COMMENT '统计日期',
    request_count INT DEFAULT 0 COMMENT '请求数',
    token_count BIGINT DEFAULT 0 COMMENT 'token数',
    error_count INT DEFAULT 0 COMMENT '错误数',
    avg_latency INT COMMENT '平均延迟(ms)',
    
    UNIQUE KEY uk_model_date (model_id, date),
    FOREIGN KEY (model_id) REFERENCES models(id) ON DELETE CASCADE,
    INDEX idx_date (date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='模型使用统计表';
```

## 3. API 设计

### 3.1 模型管理 API

#### 3.1.1 列出所有模型

```http
GET /api/admin/models
```

查询参数:
- `page`: 页码
- `page_size`: 每页数量
- `provider`: 提供商过滤
- `status`: 状态过滤
- `category`: 分类过滤
- `keyword`: 搜索关键词

响应:
```json
{
    "total": 100,
    "items": [{
        "id": 1,
        "model_id": "gpt-4",
        "display_name": "GPT-4",
        "description": "最强大的模型",
        "provider": "openai",
        "model_type": "chat",
        "context_window": 8192,
        "pricing": {
            "input": 0.03,
            "output": 0.06
        },
        "status": 1,
        "capabilities": ["vision", "function_calling"],
        "category": "large-language",
        "tier": "premium",
        "channel_count": 3,
        "subscription_count": 2
    }]
}
```

#### 3.1.2 获取模型详情

```http
GET /api/admin/models/{model_id}
```

响应包含:
- 基本信息
- 关联的渠道列表
- 关联的订阅账户列表
- 使用统计

#### 3.1.3 创建/更新模型

```http
POST /api/admin/models
PUT /api/admin/models/{model_id}
```

请求体:
```json
{
    "model_id": "claude-3-5-sonnet-20250619",
    "display_name": "Claude 3.5 Sonnet",
    "description": "Anthropic 最新模型",
    "provider": "anthropic",
    "model_type": "chat",
    "context_window": 200000,
    "pricing": {
        "input": 0.003,
        "output": 0.015
    },
    "status": 1,
    "capabilities": ["vision", "function_calling", "artifact"],
    "category": "large-language",
    "tier": "premium"
}
```

#### 3.1.4 启用/禁用模型

```http
PATCH /api/admin/models/{model_id}/status
```

请求体:
```json
{
    "status": 1
}
```

#### 3.1.5 批量操作

```http
POST /api/admin/models/batch
```

请求体:
```json
{
    "action": "enable|disable|delete",
    "model_ids": [1, 2, 3]
}
```

### 3.2 渠道模型映射 API

#### 3.2.1 获取渠道的模型列表

```http
GET /api/admin/channels/{channel_id}/models
```

#### 3.2.2 为渠道添加模型

```http
POST /api/admin/channels/{channel_id}/models
```

请求体:
```json
{
    "model_id": 1,
    "enabled": true,
    "priority": 10,
    "config": {
        "temperature": 0.7,
        "max_tokens": 4096
    }
}
```

#### 3.2.3 更新渠道模型配置

```http
PUT /api/admin/channels/{channel_id}/models/{model_id}
```

### 3.3 订阅账户模型映射 API

类似渠道模型映射 API

## 4. 前端设计

### 4.1 模型管理页面 (/admin/models)

#### 功能特性
1. **模型列表视图**
   - 表格展示所有模型
   - 支持多维度筛选（提供商、状态、分类）
   - 搜索功能
   - 批量操作

2. **模型详情面板**
   - 基本信息展示
   - 价格信息
   - 能力标签
   - 关联渠道/订阅账户

3. **快捷操作**
   - 启用/禁用切换
   - 编辑模型
   - 添加别名
   - 查看使用统计

### 4.2 渠道配置页面增强

在现有渠道管理页面中：
- 添加"模型配置"标签页
- 可视化选择可用模型
- 批量配置模型优先级

### 4.3 订阅账户配置页面增强

类似渠道配置页面的模型管理功能

## 5. 数据迁移策略

### 5.1 阶段1: 创建新表结构 (无停机)

1. 创建新表
2. 添加索引

### 5.2 阶段2: 数据迁移 (后台运行)

1. 扫描 `channels` 表的 `models` 字段
2. 为每个模型在 `models` 表创建记录（如不存在）
3. 在 `model_channel_mapping` 中建立映射

```sql
-- 迁移脚本示例
INSERT INTO models (model_id, display_name, provider, model_type, status, created_at, updated_at)
SELECT DISTINCT 
    TRIM(model) as model_id,
    TRIM(model) as display_name,
    'unknown' as provider,
    'chat' as model_type,
    1 as status,
    UNIX_TIMESTAMP() as created_at,
    UNIX_TIMESTAMP() as updated_at
FROM (
    SELECT SUBSTRING_INDEX(SUBSTRING_INDEX(models, ',', n), ',', -1) as model
    FROM channels
    JOIN (
        SELECT 1 n UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL 
        SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7
    ) numbers ON CHAR_LENGTH(models) - CHAR_LENGTH(REPLACE(models, ',', '')) >= n - 1
    WHERE models IS NOT NULL AND models != ''
) distinct_models
ON DUPLICATE KEY UPDATE updated_at = UNIX_TIMESTAMP();
```

### 5.3 阶段3: 代码切换

1. 更新查询逻辑使用新表
2. 保留旧字段作为兼容（双写模式）

### 5.4 阶段4: 清理

1. 验证迁移成功
2. 删除旧字段


## 5.5 实现记录 (Sprint 1)

> **状态: 已完成 (2026-07-22)**

### 已交付文件

| 层 | 文件 | 说明 |
|---|---|---|
| 迁移 | `migrations/062_create_model_management_tables.sql` | MySQL DDL，5 张表 |
| 迁移 | `migrations/sqlite/000_create_full_schema.sql` | SQLite 方言同步 |
| 迁移 | `migrations/postgres/000_create_full_schema.sql` | Postgres 方言同步 |
| 迁移 | `migrations/ownership.yaml` | 归属 channel-service |
| Proto | `api/channel/v1/channel.proto` | 16 个新 RPC + message |
| Biz | `app/channel/internal/biz/model.go` | Model DO、ModelRepo 接口、ModelUsecase、类型化错误 |
| Data | `app/channel/internal/data/model.go` | PO 结构体、DO↔PO 转换、Repository 实现 (DB + 内存) |
| Service | `app/channel/internal/service/model.go` | DTO↔DO 转换、gRPC handler (channel-service) |
| Service | `app/admin/internal/service/model.go` | admin-api gRPC passthrough |
| Server | `app/admin/internal/server/models.go` | admin-api HTTP 路由 handler |
| Wire | `app/channel/cmd/channel/wire.go` | ModelUsecase + ModelRepo 绑定 |
| Errors | `pkg/errors/errors.go` | 6 个模型错误原因 + HTTP 状态码映射 |

### API 端点清单

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | `/api/admin/models` | 列表 (分页/筛选/搜索) |
| POST | `/api/admin/models` | 创建模型 |
| PUT | `/api/admin/models` | 更新模型 |
| GET | `/api/admin/models/{pk}` | 模型详情 (含别名+映射) |
| DELETE | `/api/admin/models/{pk}` | 删除模型 |
| PATCH | `/api/admin/models/{pk}/status` | 启用/禁用 |
| POST | `/api/admin/models/batch` | 批量 enable/disable/delete |
| GET/POST/DELETE | `/api/admin/models/{pk}/aliases` | 别名管理 |
| GET/POST/DELETE | `/api/admin/channels/{id}/models` | 渠道-模型映射 |
| GET/POST/DELETE | `/api/admin/subscription-accounts/{id}/models` | 订阅-模型映射 |

### 测试覆盖

| 包 | 用例数 | 状态 |
|---|---|---|
| `app/channel/internal/biz` | 13 | ✅ |
| `app/channel/internal/data` | 16 | ✅ |
| `app/channel/internal/service` | 10 | ✅ |
| `app/admin/internal/server` | 8 | ✅ |

### 架构决策

1. **模型表归属 channel-service**：channel-service 已拥有 channels/abilities/subscription_accounts 表，模型注册表是其自然扩展。
2. **admin-api 透传**：admin-api 通过 gRPC 调用 channel-service，不直接访问数据库，与现有渠道管理 RPC 模式一致。
3. **内存回退**：当 DB 未配置时，Repository 使用内存 map 回退，与 channels/abilities 的内存模式保持一致。
4. **nil 安全**：ModelUsecase 为 nil 时所有方法返回空/错误，确保未启用模型管理的部署不受影响。

## 6. 实现计划

### Sprint 1: 基础架构 (2周) ✅ 已完成
- [x] 创建数据库迁移脚本 (`migrations/062_create_model_management_tables.sql`)
- [x] 实现 Models repository 和 business logic (`app/channel/internal/biz/model.go`)
- [x] 实现 Models API 端点 (gRPC: `app/channel/internal/service/model.go` + HTTP: `app/admin/internal/server/models.go`)
- [x] 编写单元测试 (biz/data/service/admin 共 47 个用例全部通过)

### Sprint 2: 前端基础 (1周) ✅ 已完成
- [x] 创建模型管理页面框架
- [x] 实现模型列表视图
- [x] 实现模型详情面板

### 5.6 实现记录 (Sprint 2)

> **状态: 已完成 (2026-07-22)**

#### 已交付文件

| 层 | 文件 | 说明 |
|---|---|---|
| Lib | `web/src/lib/model-management.ts` | TypeScript 类型定义 + API 请求封装 + 格式化辅助函数 |
| Page | `web/src/pages/admin/ModelsPage.tsx` | 模型列表页 (搜索/筛选/排序/分页/CRUD/批量操作) |
| Page | `web/src/pages/admin/ModelDetailPanel.tsx` | 模型详情对话框 (基本信息+别名+渠道映射+订阅映射) |
| Test | `web/src/pages/admin/ModelsPage.test.tsx` | 3 个单元测试 (列表渲染/空状态/创建) |
| Router | `web/src/router.tsx` | 新增 `/admin/models` 路由 (lazy load) |
| Nav | `web/src/components/AppNavigation.tsx` | 侧边栏新增「模型」导航项 (Boxes 图标) |

#### 功能清单

| 功能 | 说明 |
|---|---|
| 模型列表 | 表格展示 ID/模型ID/显示名称/提供商/类型/状态/渠道数/订阅数，支持排序 |
| 搜索 | 关键词搜索 (匹配 model_id 或 display_name) |
| 筛选 | 按状态、类型、提供商筛选 |
| 分页 | 复用 AdminPagination 组件，支持 20/50/100 每页 |
| 创建 | 对话框表单，含模型ID/名称/提供商/类型/上下文窗口/价格/分类/等级/能力标签/自定义标签/元数据 |
| 编辑 | 对话框表单 (model_id 只读) |
| 删除 | 单条删除 + 批量删除 (confirm 确认) |
| 状态切换 | 单条启用/禁用 + 批量启用/禁用 |
| 批量选择 | 全选/反选 checkbox + 批量操作按钮 |
| 详情面板 | Dialog 展示完整模型信息 + 别名列表 + 渠道映射表 + 订阅映射表 + 能力/自定义标签 |

#### 测试覆盖

| 包 | 用例数 | 状态 |
|---|---|---|
| `web/src/pages/admin` | 3 | ✅ |
| 全部前端测试 | 84 (26 files) | ✅ 全通过 |

#### 验证结果

- TypeScript 编译: ✅ 零错误
- ESLint: ✅ 零错误零警告
- Vitest: ✅ 88/88 通过 (含新增 7 个模型管理用例)
- Vite build: ✅ 构建成功

#### Code Review 修复 (P0/P1/P2)

| 优先级 | 问题 | 修复 |
|---|---|---|
| P0 | 编辑数据丢失 (openEdit 仅从 summary 初始化) | openEdit 改为 async，先调用 getModel() 获取完整 ModelInfo 再填充 draft |
| P1 | 缺少 category 筛选器 | 新增 CATEGORY_OPTIONS + category 下拉筛选器，传入 listModels API |
| P2 | DraftFields 内嵌页面文件 | 提取为 `web/src/components/admin/ModelDraftFields.tsx` (组件) + `web/src/lib/model-draft.ts` (类型/常量/工具) |
| P2 | confirm() 非 React 最佳实践 | 替换为自定义 Dialog 确认组件 (confirmState 状态驱动) |
| P2 | metadata 无 JSON 验证 | 新增 validateMetadata() 函数，表单内实时校验 + 提交时拦截 |
| P2 | 测试覆盖不足 | 新增编辑/删除/详情面板/筛选 4 个测试用例 (共 7 个) |

### Sprint 3: 集成 (1周) ✅ 已完成
- [x] 更新渠道配置页面
- [x] 更新订阅账户配置页面
- [x] /v1/models 端点适配新表

### 5.7 实现记录 (Sprint 3)

> **状态: 已完成 (2026-07-23)**

#### 已交付文件

| 层 | 文件 | 说明 |
|---|---|---|
| Component | `web/src/components/admin/ModelMultiSelect.tsx` | 可搜索的模型多选组件，从模型注册表加载已启用模型，支持自由输入未注册模型 ID |
| Page | `web/src/pages/admin/ChannelsPage.tsx` | 渠道创建/编辑对话框的模型字段从 CSV 文本输入替换为 ModelMultiSelect |
| Page | `web/src/pages/admin/SubscriptionAccountsPage.tsx` | 订阅账户创建/编辑表单的模型字段从 CSV 文本输入替换为 ModelMultiSelect |
| Data | `app/channel/internal/data/data.go` | `listAvailableModelsDB` + `listAvailableModelsMemory` 新增双读逻辑：同时查询 legacy abilities 表和 model registry 映射表 |
| Test | `app/channel/internal/data/model_test.go` | 新增 2 个双读测试用例 (legacy+registry 合并 / 禁用模型过滤) |
| Test | `web/src/pages/admin/SubscriptionAccountsPage.test.tsx` | 适配 ModelMultiSelect 的编辑测试 |

#### 功能清单

| 功能 | 说明 |
|---|---|
| 渠道模型选择 | 创建/编辑渠道时，模型字段从手动输入 CSV 变为从模型注册表勾选，支持搜索过滤 |
| 订阅账户模型选择 | 创建/编辑订阅账户时，模型字段同样使用 ModelMultiSelect |
| 模型多选组件 | 搜索框 + checkbox 列表，显示模型 ID/显示名/提供商，已选数量提示，支持未注册的自定义模型 ID |
| /v1/models 双读 | ListAvailableModels 同时查询 legacy abilities 表和 model_channel_mapping/model_subscription_mapping，合并去重后返回 |
| 禁用模型过滤 | 注册表中 status=0 的模型不会出现在 /v1/models 结果中 |
| 向后兼容 | legacy abilities 表数据仍然有效，迁移期间双写双读，无需停机 |

### Sprint 4: 增强功能 (1周) ✅ 已完成
- [x] 使用统计功能
- [x] 模型别名管理
- [x] 高级筛选和搜索
- [x] 文档和测试
- [x] 模型名称大小写不敏感匹配 (GLM-5.2 vs glm-5.2)

### 5.8 实现记录 (Sprint 4)

> **状态: 已完成 (2026-07-23)**

#### 已交付文件

| 层 | 文件 | 说明 |
|---|---|---|
| Proto | `api/channel/v1/channel.proto` | 新增 RecordModelUsage + ListModelUsageStats 2 个 RPC |
| Biz | `app/channel/internal/biz/model.go` | NormalizeModelID/ModelIDEqual 工具函数；CreateModel/GetModelByID/CreateModelAlias 大小写归一化；RecordModelUsage/ListModelUsageStats usecase 方法 |
| Data | `app/channel/internal/data/model.go` | modelUsageStatModel PO + DO↔PO 转换；RecordModelUsage (DB upsert 累加 + 内存) + ListModelUsageStats (DB + 内存)；GetModelByID/CreateModel 大小写不敏感查询 |
| Data | `app/channel/internal/data/data.go` | modelUsageStats 内存存储字段；ListAbilitiesByGroupAndModel/ListSubscriptionAccountAbilities/ListAvailableModels 大小写不敏感匹配 + 去重 |
| Service | `app/channel/internal/service/model.go` | RecordModelUsage + ListModelUsageStats gRPC handler |
| Service | `app/admin/internal/service/model.go` | admin-api 透传 |
| Server | `app/admin/internal/server/models.go` | `/api/admin/models/{pk}/usage-stats` HTTP 路由 |
| Relay | `internal/biz/relay.go` | AllowedModels 检查改为大小写不敏感 (strings.EqualFold) |
| Billing | `internal/server/http_billing.go` | recordModelUsage 方法，在 commitQuota 路径中与 recordChannelUsage 并行调用 |
| Orchestrator | `internal/server/http_orchestrator.go` | LogUsage hook 中调用 recordModelUsage |
| Resilient | `internal/data/resilient_clients.go` | RecordModelUsage + ListModelUsageStats 熔断包装 |
| Lib | `web/src/lib/model-management.ts` | ModelUsageStat 类型 + listModelUsageStats/createModelAlias/deleteModelAlias API |
| Page | `web/src/pages/admin/ModelDetailPanel.tsx` | 别名创建/删除 UI + 使用统计表格展示 |
| Page | `web/src/pages/admin/ModelsPage.tsx` | 新增 tier (等级) 筛选器 |

#### 功能清单

| 功能 | 说明 |
|---|---|
| 使用统计记录 | 每次 relay 请求完成后，通过 gRPC 调用 channel-service 的 RecordModelUsage，按 (model_id, date) 累加请求数/token 数/错误数/平均延迟 |
| 使用统计查询 | `GET /api/admin/models/{pk}/usage-stats` 支持日期范围筛选和分页 |
| 使用统计展示 | 模型详情面板中展示最近 10 条使用统计 (日期/请求数/Token 数/错误数/平均延迟) |
| 别名管理 UI | 模型详情面板中可创建/删除别名，支持标记主别名，创建后实时刷新 |
| 高级筛选 | 模型列表页新增等级 (tier) 筛选器，与状态/类型/提供商/分类筛选器并列 |
| 大小写不敏感匹配 | `NormalizeModelID` 将模型 ID 归一化为小写+去空格；CreateModel/GetModelByID/CreateModelAlias 在 biz 层归一化；DB 查询使用 `LOWER()` 函数；内存模式使用 `strings.EqualFold`；ListAvailableModels 去重时使用小写键 |
| AllowedModels 不敏感 | relay.go 中的 token 级别模型白名单检查改为 `strings.EqualFold`，确保 GLM-5.2 和 glm-5.2 都能通过 |
| 能力查询不敏感 | ListAbilitiesByGroupAndModel 和 ListSubscriptionAccountAbilities 的 DB 和内存路径均改为大小写不敏感匹配 |

#### 测试覆盖

| 包 | 新增用例 | 状态 |
|---|---|---|
| `app/channel/internal/biz` | 4 (大小写不敏感模型 ID/别名/NormalizeModelID/ModelIDEqual) | ✅ |
| `app/channel/internal/data` | 7 (DB+内存大小写不敏感查询/重复检测/可用模型去重/使用统计 DB+内存) | ✅ |
| `app/channel/internal/service` | 3 (RecordModelUsage/ListModelUsageStats/NilUC) | ✅ |
| `app/admin/internal/server` | 1 (ListModelUsageStats HTTP) | ✅ |
| `web/src/pages/admin` | 2 (别名创建/tier 筛选) | ✅ |
| 全部前端测试 | 90 (26 files) | ✅ 全通过 |

#### 验证结果

- Go 编译: ✅ 零错误 (go vet 通过)
- TypeScript 编译: ✅ 零错误
- ESLint: ✅ 零错误零警告
- Vitest: ✅ 90/90 通过 (含新增 9 个模型管理用例)
- Vite build: ✅ 构建成功

## 7. 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| 数据迁移失败 | 高 | 充分测试迁移脚本，保留备份，分阶段迁移 |
| 性能下降 | 中 | 添加适当索引，缓存常用查询 |
| API 兼容性 | 中 | 保留旧字段，双写模式过渡 |
| 前端复杂度 | 低 | 分阶段实现，复用现有组件 |

## 8. 后续扩展

1. **模型版本管理** - 支持同一模型的多个版本
2. **智能推荐** - 基于使用情况推荐最优渠道
3. **A/B 测试** - 支持模型对比测试
4. **成本分析** - 详细的成本分析仪表板
5. **模型评分** - 用户对模型的评分反馈

---

## 9. 多上游供应商同模型管理（对照 sub2api 落地分析）

> **状态: 规划中 (2026-07-23)**
> 本节记录对照同级项目 `sub2api` 后的差距分析与落地清单。

### 9.1 sub2api 机制摘要

sub2api 没有"全局模型→渠道"单一映射表，而是 **分组（Group）→ 账号（Account）→ 渠道（Channel）** 三层动态叠加，模型在每层都能被改写/限制/路由。所谓"多个上游供应商提供同一模型"体现为：同一个分组下挂多个账号（不同上游凭证），都声明支持同一个模型名，调度器在该分组账号集合里做负载均衡/粘性选择。

关键控制点（sub2api）：

| 控制点 | 机制 | 代码位置 |
|---|---|---|
| `/v1/models` 列表可见 | `Group.CustomModelsListEnabled` + `ModelsListConfig` 白名单；否则聚合账号 mapping key | `gateway_handler.go:985`、`gateway_service.go:10450` |
| 账号是否支持该模型 | `Account.IsModelSupported`：无 mapping=全开，有 mapping=必须命中 | `account.go:624` |
| 渠道是否限制该模型 | `Channel.RestrictModels` + `ModelPricing` 列表 | `channel_service.go:546` |
| 渠道模型映射 | `Channel.ModelMapping`（平台分组，通配符） | `channel.go SupportedModels` |
| 账号运行时可用 | `IsSchedulable`：状态=active、未过期、未限速、未过载、配额未超 | `account.go:118` |
| 路由到指定供应商 | `Group.ModelRouting`（模型名→账号 ID，仅 anthropic） | `group.go:136` |
| 粘性会话 | `session_hash` / `previous_response_id` 绑账号 + 运行时逃逸 | `openai_account_scheduler.go` |
| 计费基准选择 | `BillingModelSource`：requested/upstream/channel_mapped | `gateway_service.go:9764` |

### 9.2 micro-one-api 现状对照

micro-one-api 已有比 sub2api 更规范的模型注册表（方案 B），很多能力 sub2api 靠账号级 `model_mapping` 字符串拼出，micro-one-api 用独立表 + ORM 实现。

**已具备（无需新建）**：

| sub2api 机制 | micro-one-api 对应物 | 位置 |
|---|---|---|
| `Model.Status` enable/disable | `Model.Status`（0/1/2）+ `Model.IsPublic` | `app/channel/internal/biz/model.go:25-45` |
| 渠道级模型列表 | `ModelChannelMapping{ChannelID, ModelPK, Enabled, Priority, Config}` | `model.go:71-80` |
| 账号级模型列表 | `ModelSubscriptionMapping{SubscriptionAccountID, ModelPK, GroupName, Enabled, Priority}` | `model.go:82-92` |
| `(group, model) → 候选 channel` | `Ability` + `ListAbilitiesByGroupAndModel` | `channel.go:155`、`data.go:249` |
| `(group, model, platform) → 候选 subscription account` | `SubscriptionAccountAbility` + `ListSubscriptionAccountAbilities` | `channel.go:163`、`data.go:262` |
| 渠道级模型映射 src→dst | `Channel.ModelMapping string`（JSON） | `channel.go:76` |
| 全局模型名映射 | `ModelMapper`（文件级 YAML/JSON） | `internal/biz/model_mapping.go` |
| 优先级分层 + 负载均衡选择 | `WeightedSelector`（smooth WRR + health/latency/circuit-breaker） | `selector.go` |
| 订阅账号优先级分层选择 | `SelectSubscriptionAccount`（priority tier → 随机） | `channel.go:365` |
| 粘性会话 | `RelayUsecase.trySubscriptionSticky` + `SessionAccountStore` | `internal/biz/relay.go` |
| `/v1/models` 聚合 | `ListAvailableModels(group)` 查 abilities 表 | `channel.go:529`、`data.go:641` |
| 调度编排 | `RelayUsecase.Plan`：model resolve → auth → permission → channel → subscription fallback | `relay.go:200` |

**核心结论**：micro-one-api 的"多上游供应商提供同一模型"已天然成立——只要多个 `Channel` 或多个 `SubscriptionAccount` 在 `abilities` / `subscription_account_abilities` 表里挂同一个 `(group, model)`，`SelectChannel` / `SelectSubscriptionAccount` 就会在它们之间做优先级分层 + 加权/随机选择。

### 9.3 差距清单

#### 1. 订阅账号级模型映射 src→dst（缺失）⭐ P0
sub2api 每个账号凭证带 `model_mapping`，可把客户端模型名映射成不同供应商上游真实模型名。micro-one-api 的 `Channel` 有 `ModelMapping`，但 `SubscriptionAccount` 没有。

#### 2. 限制模式 `RestrictModels`（缺失）⭐ P1
sub2api 有 `Channel.RestrictModels bool`。micro-one-api 当前逻辑隐式等价于 `RestrictModels=true`（abilities 表无记录就选不到），缺"放行所有未注册模型"开关。

#### 3. 模型路由 `model→指定账号`（缺失）⭐ P2
sub2api `Group.ModelRouting map[string][]int64`。micro-one-api 只有 `Priority` 分层，无精确路由。

#### 4. 通配符模型匹配（缺失）⭐ P1
sub2api 在 `model_mapping`、`ModelRouting`、pricing 支持 `claude-*` / `*`。micro-one-api abilities 查询是精确匹配。

#### 5. `/v1/models` 聚合多源模型 + 缓存（部分缺失）⭐ P0
sub2api `GetAvailableModels` 聚合所有可调度账号 mapping key 并集 + 15s TTL 缓存。micro-one-api `ListAvailableModels` 已聚合 abilities + registry，但**无缓存**，直连 DB。

#### 6. `BillingModelSource` 三态（可选）⭐ P3
micro-one-api 若不按渠道差异化计费可跳过。

#### 7. 订阅账号选择器升级为加权选择（可选优化）⭐ P2
micro-one-api `SelectSubscriptionAccount` 同优先级 tier 内纯随机，sub2api 用 `filterByMinLoadRate → selectByLRU` + EWMA + 粘性逃逸。

### 9.4 落地优先级

| 优先级 | 事项 | 理由 |
|---|---|---|
| P0 | #5 `/v1/models` 聚合 + 缓存 | 客户端发现模型入口，当前无缓存直连 DB |
| P0 | #1 订阅账号级 `ModelMapping` | 没有它，不同供应商同名模型无法映射到各自上游真实模型名 |
| P1 | #4 通配符匹配 | 让 `claude-*` 类路由不用为每个小版本建一行 |
| P1 | #2 `RestrictModels` 开关 | 给管理员"放行未注册模型"选项 |
| P2 | #3 模型路由到指定账号 | 精确控制某模型走指定供应商 |
| P2 | #7 订阅账号加权选择 | 从随机升级为负载感知 |
| P3 | #6 `BillingModelSource` | 仅在需按渠道差异化计费时 |

### 9.5 P0 实现记录

> 见下文 §10、§11。proto + DO + migration 代码改动已落地。

## 10. P0 实现方案：订阅账号级模型映射 + `/v1/models` 缓存

> **状态: 实施中 (2026-07-23)**

### 10.1 P0-#1 订阅账号级 `ModelMapping`

#### 背景
`Channel`（API-key 渠道）已有 `ModelMapping string` 字段（migration 018），但
`SubscriptionAccount`（OAuth 订阅账号）没有。不同上游供应商对同一客户端模型名
可能使用不同的上游真实模型名（如 A 供应商 `claude-sonnet-4-5`→`claude-sonnet-4`，
B 供应商 `claude-sonnet-4-5`→`gpt-4o`），没有 per-account mapping 就无法支持。

#### 变更清单

| 层 | 文件 | 变更 |
|---|---|---|
| Migration | `migrations/063_add_subscription_account_model_mapping.sql` | 新建：`ALTER TABLE subscription_accounts ADD COLUMN model_mapping` |
| Migration | `migrations/sqlite/000_create_full_schema.sql` | 追加 `model_mapping` 列 |
| Migration | `migrations/ownership.yaml` | channel 服务加 `063` |
| Proto (common) | `api/common/v1/common.proto` | `SubscriptionAccountInfo` 加 `model_mapping = 35` |
| Proto (channel) | `api/channel/v1/channel.proto` | `Create/UpdateSubscriptionAccountRequest` 加 `model_mapping = 30` |
| Proto (admin) | `api/admin/v1/admin.proto` | `AdminCreate/UpdateSubscriptionAccountRequest` 加 `model_mapping = 30` |
| DO (channel biz) | `app/channel/internal/biz/channel.go` | `SubscriptionAccount` 加 `ModelMapping string` |
| DO (relay biz) | `internal/biz/relay.go` | `SubscriptionAccount` + `Channel` 加 `ModelMapping string` |
| PO | `app/channel/internal/data/data.go` | `subscriptionAccountModel` 加 `ModelMapping *string`；双向转换 |
| Service (channel) | `app/channel/internal/service/channel.go` | Create/Update/Info/Summary 透传 |
| Service (admin) | `app/admin/internal/service/admin.go` | Create/Update 透传 |
| Relay adapter | `internal/data/adapters.go` | `subscriptionAccountInfoToBiz` 映射 `ModelMapping` |
| Relay adapter | `internal/data/data.go` | `subscriptionAccountInfoToBiz` + `SelectChannel` 映射 `ModelMapping` |
| Relay adapter | `internal/data/cached_channel.go` | `channelInfoToBizChannel` 映射 `ModelMapping` |
| Relay biz | `internal/biz/relay.go` | `subscriptionAccountToChannel` 透传 `account.ModelMapping` |

#### 模型映射 JSON 格式
复用 `Channel.ModelMapping` 已有格式（JSON `{"src":"dst"}`），与 sub2api 的
`model_mapping` 结构一致，例：
```json
{"claude-sonnet-4-5":"claude-sonnet-4","gpt-4o":"gpt-4o-2024-08-06"}
```

#### relay 路径映射应用
`RelayUsecase.Plan` 在选中 subscription account 后，用 `account.ModelMapping`
对 `resolvedModel` 做二次映射（全局 `ModelMapper` 先跑，per-account mapping 再跑），
结果写入 `RelayPlan.ResolvedModel`，server 层用 `plan.ResolvedModel` 替换请求体。

### 10.2 P0-#5 `/v1/models` 聚合 + 缓存

#### 背景
`ListAvailableModels` 已聚合 abilities + registry 表，但无缓存，每次 `/v1/models`
请求都打 DB。sub2api 用 15s TTL 进程内缓存 + 失效事件。

#### 变更清单

| 层 | 文件 | 变更 |
|---|---|---|
| Biz | `app/channel/internal/biz/channel.go` | `ChannelUsecase` 加 `modelsListCache`（TTL map）|
| Biz | `app/channel/internal/biz/channel.go` | `ListAvailableModels` 先查缓存；`CreateChannel`/`UpdateChannel`/`DeleteChannel`/`ChangeStatus`/`CreateSubscriptionAccount`/`UpdateSubscriptionAccount` 等变更后失效 |

#### 缓存设计
- 进程内 `sync.Map` + 单 TTL（默认 15s，可配置）
- key = group（与 sub2api 一致）
- 值 = `[]string`（clone 后返回，不暴露内部 slice）
- 变更路径（channel CRUD、subscription account CRUD）调 `invalidateModelsListCache()`
- 依赖现有 `eventBus` 的 `TopicChannelChanged` 事件做跨实例失效（L1 短 TTL 容忍最终一致）

### 10.3 P0 实现记录

> **状态: 已完成 (2026-07-23)**

#### 已交付文件

| 层 | 文件 | 变更 |
|---|---|---|
| Migration | `migrations/063_add_subscription_account_model_mapping.sql` | 新建：`ALTER TABLE subscription_accounts ADD COLUMN model_mapping` |
| Migration | `migrations/sqlite/000_create_full_schema.sql` | 追加 `model_mapping TEXT DEFAULT ''` 列 |
| Migration | `migrations/sqlite/002_add_subscription_account_model_mapping.sql` | 新建：SQLite ALTER TABLE |
| Migration | `migrations/ownership.yaml` | channel 服务加 `063` |
| Proto (common) | `api/common/v1/common.proto` | `SubscriptionAccountInfo` + `SubscriptionAccountSummary` 加 `model_mapping` 字段 |
| Proto (channel) | `api/channel/v1/channel.proto` | `Create/UpdateSubscriptionAccountRequest` 加 `model_mapping = 30` |
| Proto (admin) | `api/admin/v1/admin.proto` | `AdminCreate/UpdateSubscriptionAccountRequest` 加 `model_mapping = 30` |
| DO (channel biz) | `app/channel/internal/biz/channel.go` | `SubscriptionAccount` 加 `ModelMapping string`；`ChannelUsecase` 加 `modelsListCache` + TTL 缓存 + 变更失效 |
| DO (relay biz) | `internal/biz/relay.go` | `SubscriptionAccount` + `Channel` 加 `ModelMapping`；`applyPerAccountModelMapping` helper；`Plan` 三条返回路径应用 per-account/channel mapping |
| PO | `app/channel/internal/data/data.go` | `subscriptionAccountModel` 加 `ModelMapping`；双向转换补 `ModelMapping` |
| Service (channel) | `app/channel/internal/service/channel.go` | Create/Update/Info/Summary 透传 `ModelMapping` |
| Service (admin) | `app/admin/internal/service/admin.go` | Create/Update 透传 `ModelMapping` |
| Relay adapter | `internal/data/adapters.go` | `subscriptionAccountInfoToBiz` 映射 `ModelMapping` |
| Relay adapter | `internal/data/data.go` | `subscriptionAccountInfoToBiz` + `SelectChannel` 映射 `ModelMapping` |
| Relay adapter | `internal/data/cached_channel.go` | `channelInfoToBizChannel` 映射 `ModelMapping` |
| Test | `app/channel/internal/data/data_test.go` | 测试 schema 加 `model_mapping` 列 |

#### 验证结果

- `go build ./...`: ✅ 零错误
- `go test ./internal/biz/`: ✅ 通过
- `go test ./app/channel/internal/biz/`: ✅ 通过
- `go test ./app/channel/internal/data/`: ✅ 通过
- `go test ./internal/data/`: ✅ 通过
- `go test ./app/channel/internal/service/`: ✅ 通过
- `go test ./app/admin/internal/service/`: ✅ 通过

#### 模型映射链路

```
客户端请求 model="claude-sonnet-4-5"
  │
  ▼ global ModelMapper (文件级 YAML/JSON)
  │ resolvedModel = "claude-sonnet-4" (全局别名)
  │
  ▼ SelectChannel / SelectSubscriptionAccount (按 resolvedModel 查 abilities)
  │ 选中 account A (platform=codex)
  │
  ▼ applyPerAccountModelMapping(account.ModelMapping, resolvedModel)
  │ account A mapping = {"claude-sonnet-4":"gpt-4o"}
  │ finalModel = "gpt-4o"
  │
  ▼ RelayPlan.ResolvedModel = "gpt-4o"
  │ server 层用 plan.ResolvedModel 替换请求体 model 字段
```

#### `/v1/models` 缓存

- 进程内 `sync.Map` + 15s TTL（`modelsListCache`）
- key = group，值 = `[]string` clone
- 变更路径（channel CRUD、subscription account CRUD、ChangeStatus、AutoPause）调 `invalidateModelsListCache()`
- RecordHealth / RecordUsage 不触发失效（不影响模型列表）
- 依赖现有 `eventBus.TopicChannelChanged` 事件做跨实例 L1 最终一致（L1 短 TTL 容忍）
