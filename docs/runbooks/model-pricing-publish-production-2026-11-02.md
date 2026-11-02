# 模型编辑价格发布修复：生产更新与验收

2026-11-02（北京时间）。根因及规则见 [模型价格后续工作](../model-pricing-follow-up.md)。

## 部署

- 本地运行 `scripts/deploy-update.sh admin-api`，构建 linux/amd64 镜像并上传，未在生产服务器构建。
- 镜像：`docker-compose-admin-api:deploy-20261102-182225-42a15d87`。
- 镜像 ID：`sha256:65e50ebaf400343f3ea7d5b97abc5f7b3250ed8823fe6dbe2093ab91377fbb96`。
- 本地构建输入、镜像及容器来源摘要一致：`0548ba58c62bd7bf914f115233f5623ce5c574842ef4af51ffed612503982bb8`。镜像包含当前工作区修复，标签中的 Git SHA 不代表修复已经提交。
- 本地 `web/npm run build` 通过，包括资源体积检查；单独更新挂载的 `/opt/web/dist`。
- 线上实际返回的 `index.html` 与本地产物 SHA256 一致：`b6ae310273b62ce4d7b7befe07c7ed2e83ae9c1acaca80e2ba9a5d7a6f094195`。

## 生产验收

1. 使用现有管理员浏览器会话，在模型页创建隔离的非公开模型 `pricing-verify-20261102-1825`（ID 11666），输入/输出/缓存读取价格为 0.123 / 0.456 / 0.012 USD/1M tokens。
2. 没有保存管理价格页。直接从实际 owner 数据库读回，模型库价格与正式 `ModelPrice` 一致；正式值为 0.000000123 / 0.000000456 / 0.000000012 USD/token。
3. 在模型编辑页再次修改保存，读回确认模型库与正式配置同步。验收模型始终非公开、没有上游映射。
4. 验收完成后仅清理该临时模型及对应正式价格键；读回两者均不存在，保留写入审计。
5. 生产 `/api/pricing` 成功返回 6 个公开模型；实际浏览器访问 `https://console.mengbin.top/pricing`，确认 `deepseek-v4.1-flash-1017` 显示输入 $0.30、输出 $1.20、缓存读取 $0.006 / 1M tokens。
6. admin-api 为 running，近期日志没有 panic/fatal。验收未发起上游模型请求或实际扣费。

## 回归与登记

- Admin biz/data/service/server 及 RBAC contract 全套测试通过；存储边界使用 SQLite，外部 MySQL/Postgres 测试未配置本地临时库 DSN。
- 前端相关 41 个测试此前通过，生产构建通过。
- 补齐新增价格发布错误适配文件的 SOURCE 登记，刷新模型 service 的审查摘要；`TestReviewedSourceAndNegativeDrift` 通过。仅修改审查文档，不改变已部署构建输入。

## 回滚点

- Admin 旧镜像：`docker-compose-admin-api:rollback-20261102-182225`；Compose 同时间戳备份由部署脚本保留。
- Web：`/opt/web/dist.bak.model-pricing-20261102-182354`。

异常情况下模型注册与价格发布仍可能部分成功；前端会明确提示，并要求在价格管理页重试。正常保存路径不再需要第二次保存。
