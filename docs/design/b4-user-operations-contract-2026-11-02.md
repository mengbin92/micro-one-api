# B4 用户运营可见性契约

状态：工程验收通过，待部署。维护者于 2026-11-02 确认以下范围。

- 用户只查看本人可用模型及聚合可用状态；不返回渠道名称、ID、数量、上游供应商或故障详情。
- 公告支持全体、指定用户分组、指定用户，定时发布、到期下架、本人已读和管理员管理页。
- 用户分组使用 Identity 的当前默认路由组 ID，不使用浏览器声明、IAM 角色或 Token 分组。每次读取重新查询；分组变化立即影响可见性。
- 前置：已有 Identity 路由 v2 迁移、用户默认组和专用 Admin 服务身份。状态表示路由配置可用性快照，复用 Channel 最长 15 秒缓存，不代表实时健康、并发余量或账户余额；不保证每次请求成功，不探测真实付费来源。依赖失败返回错误，不能显示为正常或空结果。
- Admin 为公告 owner，复用 `admin.content` / `system.content.notice.update` 权限；管理读取也要求该权限。HTTP 与 gRPC 都在 usecase 授权，legacy 模式也验证管理员。
- 公告正文为纯文本；draft/published/archived，发布时间包含、到期时间不包含。“立即发布”由 owner 冻结实际发布时间；没有后台定时任务，读时按 owner 时间判断。下架保留记录。
- 保存使用 revision 条件更新；创建 revision=0，修改携带当前 revision 和至少三个字符的操作原因。保存与审计同事务。任何编辑增加 revision，旧已读不覆盖新版本。
- 用户读取不返回定向对象、管理状态及审计。已读按公告/用户/版本唯一，先在同一事务确认当前版本和可见性；越权、未发布、到期统一不存在。
- 三库迁移 148 归 Admin；上线先迁移并按 [148 运行账号权限模板](../../migrations/manual/148_announcement_runtime_grants.example.sql)授权，再更新 Channel/Admin，Web 单独发布。保留旧公共 Notice，无隐式转为定向公告。

## 验收

已通过 biz 时间/受众/权限、三库迁移/CAS/已读并发/审计回滚、真实 HTTP/gRPC 会话与权限、MySQL 最小运行权限、桌面/移动正常/空/异常与慢请求验收，以及完整工程门禁。机器评审已完成并逐项复核，见 [B4 验收记录](../runbooks/b4-user-operations-acceptance-2026-11-02.md)与[证据汇总](../runbooks/evidence/b4-user-operations-2026-11-02.json)。

生产部署、真实付费探测与发版/tag 尚未执行。
