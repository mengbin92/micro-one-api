# 管理页面授权轮询导致控件消失：诊断与修复

> 2026-10-08（Asia/Shanghai）。用户反馈线上 `/admin/channels` 仍约一分钟闪刷，并要求检查其它管理页面。修复基线 `develop@2c695044`。源码修复 `d1573f9b` 已提交并推送 develop，用户随后授权更新线上；生产静态资源已于 17:05 CST 发布并完成校验。

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

## 生产部署（用户授权后）

- 源码提交：`d1573f9b6f5d6f2181105911f657dc47bd88d2d9`，已推送 `origin/develop`；提交正文记录根因、影响和回归，pre-push gosec 通过。
- 本机执行 `cd web && npm run build`，生产构建及六项体积预算全部通过。未在远程主机构建。
- 确认 admin-api 将 `/opt/web/dist` 只读挂载到 `/web`，按前端发布流程备份后更新静态目录，无容器重启。
- 回滚备份：`/opt/web/dist.bak.20261008-170528`；旧 `index.html` SHA256 为 `7c9b23d4b4e03af1efa6efffc7161d4525730c78d3ce792844e9c785e8ebcd6a`。
- 新 `index.html` SHA256：`9f5bb76e786054e17f6bef54b4c7c9ee2b31a1aa9483a0ae790a347fae5fe821`；入口 chunk：`index-D-yeAoCd.js`。
- 远程目录 77 个文件的完整路径与 SHA256 均与本机构建一致；公网 `index.html`、ChannelsPage、PermissionButton、ExportButton 的 JS 内容逐一核对一致，`/admin/channels` 返回 200 和新入口。
- admin-api 仍正常运行，主机直连 `/healthz` 返回 `{"status":"ok"}`。
- 已登录 Chrome 重新加载线上 `/admin/channels`，打开编辑弹窗并输入仅用于验证的未保存名称；跨过一分钟及自然授权轮询后，两次间隔观察均确认弹窗、草稿和保存控件仍存在。最后关闭弹窗丢弃测试草稿，列表与编辑/导出控件正常显示，未点击保存。此为线上人工状态抽样，连续 DOM 与请求计数仍由前述桌面/手机 Playwright 回归验证。
- 首次 tar 包包含 macOS 自动生成的 `._*` 元数据文件，完整清单校验发现后仅清理这些生成文件，再次严格核对 77 个业务文件全部一致；没有忽略清单差异。后续跨平台打包应禁用 Apple 元数据（`COPYFILE_DISABLE=1 tar --no-xattrs ...`）。

脱敏部署清单见 [生产部署证据](evidence/console-management-controls-deploy-2026-10-08.json)。未修改业务数据、权限或后端二进制，也未进行版本发布或打 tag。

## 后续 CI 门禁修复

上述提交触发的 Frontend job `113234424472` / `113236762217` 在普通测试全部通过后，因关键测试函数覆盖率 `158/174 = 90.8%` 低于既有 91% 门槛而失败。管理控件回归此前只进入普通测试，关键测试未调用授权 hook 的 `canAll/canAny`；路由的手动「刷新权限」动作也缺少行为回归。

将管理控件及 channels 轮询回归纳入关键测试集，并新增拒绝页面在显式刷新、新权限获准后恢复访问的测试。保留原有被测源文件和所有覆盖率门槛：关键测试 15 文件 / 148 项通过，函数覆盖率 `161/174 = 92.52%`；全量 65 文件 / 330 项、lint 和类型检查通过。

Security job `113236762352` 的实际失败步骤为 Gitleaks：部署清单用 JS 路径作 JSON key，含 token/api/oauth/credential 的文件名使对应的 SHA256 值被 generic-api-key 规则误报。改成显式 `{path, sha256}` 记录数组，逐项确认全部 77 个路径和摘要保持一致。使用与 CI 相同的 Gitleaks 8.24.3 对前后清单运行扫描：原格式 4 项、退出码 1；新格式 0 项、退出码 0。未修改安全扫描配置或添加 ignore；本次改动为测试和证据格式，无生产运行代码变更。
