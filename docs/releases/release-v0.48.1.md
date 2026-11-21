# Micro-One API v0.48.1 发布公告

> 2026-11-21 · 上一版：[v0.48.0](./release-v0.48.0.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.48.1)

v0.48.1 是 **PATCH 修复版本**。公告的发布校验以 Admin 时间为准，管理页明确提示如何处理已过期的下架时间。已有草稿留空发布时间时可以正常发布。无新增数据库迁移。

## 1. 修复过期公告保存成功却不可见

**根因**。编辑已过期的公告时，服务端曾只比较下架时间与原发布时间，允许保存一条下架时间早于当前时间的公告。用户侧按可见性规则过滤这条公告。

**修复**。Admin 拒绝把已过期的公告保存为发布状态，返回 HTTP 400 和 `ANNOUNCEMENT_EXPIRED`。管理页保留表单内容，提示延后或清空下架时间。浏览器校验所填时间的先后顺序，服务端负责判断当前时间。既有草稿省略发布时间时，仍可在保存为发布状态时立即发布。 [公告契约](../design/b4-user-operations-contract-2026-11-02.md)

**影响服务**。Admin API、Web。

## 兼容性说明

- 沿用现有公告表、权限、受众和已读记录。没有新增配置或迁移。
- 合法发布、草稿和下架仍走原有入口。已有过期公告可通过调整下架时间重新发布。

## 升级步骤

1. 保存旧 Admin 镜像与 Compose 回滚点，升级 Admin API。 [部署手册](../deployment.md)
2. 备份 Web 静态目录，先更新资源，再切换入口。核对 `/admin/announcements`、`/operations` 和 Admin 健康检查。
3. 如需回滚，先恢复旧 Web 入口，再按 Compose 备份恢复 Admin 镜像和来源标签。

## 验证

- [x] `go build ./...`、`go vet ./...`、`make verify`、`make test-integration` 通过。前端 810 项测试和生产构建桌面、移动端 12 项公告用例通过。
- [x] 修复提交通过 SQLite、MySQL、PostgreSQL 公告 HTTP/gRPC 回归。前后端 OCR 均完成，未发现 P0/P1。
- [x] 生产更新于 11 月 21 日 09:25（北京时间）完成。Admin 镜像与修复提交 `9d72e84b` 的来源摘要一致，`/healthz` 正常，容器重启计数为 0。Web 88 个文件校验一致，公网公告页面入口和对应资源均与本地制品一致，三个匿名模型及公告接口均返回 401。
- [x] 回滚镜像标签为 `rollback-20261121-092423`，Web 回滚目录为 `/opt/web/dist.bak.announcements-20261121-092527`。
- [ ] 生产管理员发布及普通用户已读回执的写入复验待补，部署验证未写入公告数据。

## 完整变更日志

- `fix(admin): reject expired announcement publication`
- `docs(release): v0.48.1`
