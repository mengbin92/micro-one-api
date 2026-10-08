# 管理页面授权轮询导致控件消失：诊断与修复

> 2026-10-08（Asia/Shanghai）。用户反馈线上 `/admin/channels` 仍约一分钟闪刷，并要求检查其它管理页面。修复基线 `develop@2c695044`。本轮完成源码修复和本地回归；尚未构建或发布生产静态资源。

## 复现与根因

只读观察现有生产 Chrome 的 `/admin/logs`：授权请求返回 200、单次约 815 ms；续验期间清理、导出、逐行详情按钮消失，随后重新出现。关闭本轮打开的 DevTools，恢复原页面布局。未修改业务数据或授权。

确定性回归：

```bash
cd web
npm test -- src/pages/admin/ChannelsPage.refresh.test.tsx
```

修复前首次运行失败于续验期间查找「编辑」按钮：`Unable to find an accessible element with the role "button" and name /编辑/`。表格数据仍存在；该回归没有复现浏览器整页导航或列表重新请求。最终回归直接触发 React Query 实际注册的 30 秒授权 interval，连续验证三轮续验。

排查假设及结论：

1. 共享权限按钮以执行权限控制存在性：确认。`useAuthorization` 在 fetching 期间暂时清空执行 `snapshot`，`PermissionButton` 的 `canAll/canAny` 随之为 false，卸载按钮。
2. 导出组件同样卸载：确认。`AuthorizedExport` 用 `auth.can` 控制内部下载组件存在性，导致按钮和局部状态被移除。
3. 授权 generation 改变或到期导致整页重建：此次相同授权续验回归没有发生；既有路由/授权测试继续验证真实版本变化、失败和到期时清理旧内容。

此前修复已保留页面、数据缓存及 IAM 页面动作，但普通管理页面仍使用未修改的共享组件，留下这个缺口。

## 管理页面检查范围

检查 `adminPages` 的全部 28 个入口及管理组件的 `can`、`snapshot`、interval 和存在性判断。

| 页面或组件 | 发现与处理 |
| --- | --- |
| 渠道、用户、模型、订阅账号、路由分组、订阅分组、订阅套餐、用户订阅、兑换码、系统设置、定价、上游成本、调用日志 | 使用共享 `PermissionButton`；统一修复续验期间卸载控件的问题 |
| 渠道、用户、兑换码、调用日志导出 | 使用带权限的 `ExportButton`；统一保留内部下载组件并禁用执行 |
| 模型详情、模型导入导出、OAuth、路由访问和计费编辑等子组件 | 共享按钮修复同时覆盖这些组件 |
| IAM 页面及编辑器 | 现有展示 gate 保持控件，执行 gate 禁用操作；既有浏览器回归继续通过 |
| 总览、支付订单、对账、经营分析、渠道健康、模型健康、选路运维 | 未发现同样的共享控件卸载路径；健康页的 30 秒自动更新、选路运维的 60 秒数据轮询保留 |

页面范围由实际导入和调用点审查确认，不代表逐页生产浏览器验收；本轮完整页面浏览器测试覆盖渠道、用户、调用日志及原 IAM 权限页。

## 修复行为

- `PermissionButton` 用 `displayCan/displayCanAll` 决定存在性；用 `canAll/canAny` 决定续验期间是否禁用，与调用方已有 `disabled` 合并。
- IAM 对象动作仍按 `displaySnapshot` 和 owner 返回的 `permitted_actions/permittedActions` 判断。对象动作缺失或被拒绝的按钮始终隐藏，续验期间也不会暂时出现。
- `Permission` 展示容器使用有效展示快照，保留子组件草稿。
- 带权限的导出保留同一个 `ExportDownload`，续验期间禁用按钮并阻止下载处理；保留已有 pending、空数据和错误处理。
- 不修改授权轮询、deadline、缓存 generation、后端权限或 managed-write 校验。真正撤权、失败、过期或对象权限撤销仍移除受保护控件。

## 验证

| 检查 | 结果 |
| --- | --- |
| 全量 Vitest，`npm test -- --maxWorkers=2` | 65 文件、329 项通过，无跳过 |
| 新增真实 channels 组件回归 | 连续三轮实际授权 timer，编辑/导出 DOM 实例保留、续验期间禁用、列表只读一次 |
| 共享控件回归 | 13 类页面动作及四类导出；IAM/legacy、any/all、对象权限、原 disabled、草稿、撤权、失败、到期均覆盖 |
| ESLint，`npm run lint` | 通过 |
| 类型，`npx tsc --noEmit -p tsconfig.app.json` | 通过 |
| Playwright，`npx playwright test authorization-refresh.spec.ts --workers=1` | 桌面/手机 8 项通过：渠道、用户、日志连续 90 秒轮询及原 IAM 续验场景；渠道未保存编辑草稿保留，真实撤权移除控件、弹窗和旧页面，owner 请求不增加 |

未执行生产构建、部署、提交或发布。线上生效需按仓库前端流程更新主机挂载的 `web/dist`；仅重建 admin-api 镜像不会更新这些静态资源。
