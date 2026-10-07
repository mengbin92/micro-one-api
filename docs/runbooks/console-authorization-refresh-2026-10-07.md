# 控制台约一秒等待与周期性闪刷：诊断和修复

> 2026-10-07（Asia/Shanghai）。用户确认是线上生产页面，数据区域打开后约一秒才出现，并经常像页面重新刷新。基线 `develop@5c17986f`，修复位于当前工作区；生产仅只读取证。**更新（2026-10-07 17:35 CST）：已连同 D7 变更整包构建并发布生产静态站点，新产物 SHA256 `6598982adfe66c4729ef23755bdc4dfef0e070dd810aff8107c34f8720d45d7f`，旧产物备份 `/opt/web/dist.bak.*`；仍未提交、推送或打 tag。**
> 本轮为 [D7 体验验收](d7-experience-acceptance-2026-10-07.md) 后的反馈修复。原 D7 构建/哈希/覆盖率数字保留原轮次；当前候选另按本记录验收。

## 生产事实与边界

读取时间约 15:09–15:26 CST，浏览器正在 `/admin/redemptions`，Network 显示没有限速。未修改生产权限、配置、镜像或前端静态站点，也未产生模型计费、支付或通知发送。

| 对照 | 实际观测 |
| --- | --- |
| 浏览器现有连接中的兑换码 GET | 总计 776.37 ms；排队 1.40 ms、stalled 0.80 ms、发送 0.11 ms、等待首字节 772.46 ms、下载 1.60 ms |
| 浏览器另一轮三个数据 GET | 通知约 586 ms、dashboard 776 ms、兑换码 1.36 s；是不同请求样本，不能解释为修复前后对照 |
| 本机 HTTPS `/api/status` | 三次 TTFB 1.099 / 1.130 / 1.104 s 左右；TLS 建连约 0.4 s |
| 生产主机直连 `127.0.0.1:3000/api/status` | TTFB 3.008 ms，总计 3.047 ms |
| 生产主机直连 `/healthz` | TTFB 0.872 ms |
| 未登录 authorization 控制请求 | 公网约 1.120 s；主机直连 0.681 ms，均为 401；不代表已登录 IAM 计算耗时 |
| 服务状态 | 九业务服务运行；admin/identity/channel/billing 单次 CPU 样本均低于 0.2%，MySQL 约 4.05% |
| 本机测试进程与代理环境 | 上轮 Vite/Vitest/Playwright 进程已退出；curl 代理环境变量均未设置，`--noproxy '*'` 未改善公开接口耗时 |
| 生产前端摘要 | `index.html` SHA256 为 `883962cd93a71bb335946d97e044a0528a17448849cabc6c2b28980d9b2820df`，与原 D7 本机构建摘要不同；本轮未更新该文件 |

公开控制接口显示公网/入口链路有明显等待；兑换码 Timing 的“等待服务响应”包含网络、代理及业务处理，不能仅凭该标签把 772 ms 全归给后端或网络。没有获得同一已登录业务请求的服务器完整耗时：服务日志没有可用请求 latency 字段，Jaeger 只读查询不可用。因此本轮不承诺修复后首次冷加载会低于一秒，也不宣称已排除所有私有接口的服务端问题。

一次主机只读探针误用了 zsh 特殊变量 `path`，改变了该临时 shell 的 PATH，命令未执行；已改成 `probe_route` 并完整重测。没有修改服务器持久环境。

## 可复现的界面缺陷

最小命令：

```bash
cd web
npm test -- src/lib/authorization.test.tsx -t 'same-version background'
```

后台 authorization 返回同一版本，但响应被暂停时，已显示的 `stable data` 消失，DOM 变为 `hidden`，首次红色反馈约 40 ms。另一个测试证实其他标签页修改 `web:theme` 同样会清空数据。

三个行为叠加导致闪刷：

1. `useAuthorization` 在每次 authorization 请求开始时，取消在途 owner 请求并移除全部 protected cache；30 秒轮询与窗口重新获得焦点都会触发。
2. 后台请求期间 `snapshot` 被置空，查询 generation 也暂时丢失授权版本，owner query key 来回变化，已验证数据无法复用。
3. `AdminRoute` 把任意 `isFetching` 当首次加载，整个 `<Outlet>` 被卸载；详情弹窗与未保存的本地草稿随之消失。

此外，所有 `storage` 事件都触发权限重载，主题/语言等非凭证变化也会刷新整个保护数据区。既有逻辑来自 IAM 管理前端接线；本轮 D7 没有修改这段生产逻辑。

## 最终修复

- 无变更后台轮询保留仍有效的展示摘要、页面实例和 read cache；已有有效摘要时，新页面的获准 owner read 与后台续验并行，避免再串行等待一次权限网络请求；保留 30 秒轮询与焦点核对，没有通过停轮询或延长授权有效期规避问题。
- 展示/读取与执行 gate 分开：`displaySnapshot` / `displayCan` 用于仍有效的展示及 owner 读取；`snapshot` / `can` 在刷新期间仍不可用于写入或预检。owner 继续对每个实际资源请求重新授权。
- authorization mode、全部版本、session、可执行操作或角色变化时，清除旧保护数据。错误、未知 mode、无效/过期 deadline、403 和凭证变化保持拒绝与失效行为；不使用失败或过期的摘要降级。
- 查询 generation 在后台请求期间保持稳定，包含凭证身份、mode、版本、session 与操作/角色边界；对 deadline 续期、版本键顺序及角色/操作集合顺序变化保持稳定，并 memoize，避免每次输入草稿时重复排序/编码。
- managed write preparer 在调用时读取实时 query 状态，阻断“刷新已开始、React 还没重绘”的写入窗口；IAM 写入也不能借早期兼容分支绕过刷新期间拒绝。
- 权限边界真正变化时用 AdminRoute generation key 丢弃本地详情/编辑副本，即使仍有页面进入权也不沿用旧字段访问；共享 user-self/dashboard 标为 protected 并接入取消 signal，凭证切换不复用另一身份的头像/余额。只读字段和角色编辑器使用展示 gate 保持稳定，写控件仍用实时执行 gate。
- `storage` 只对 `token` 或 clear-all 事件执行权限重载；外观偏好和本地展示角色不是授权事实源。
- 强制权限重验仍清摘要与 protected cache。`/session-roles` 初始化页可以保留同一凭证身份的本地选择/原因/冲突提示，加载期间不呈现旧权限数据；凭证变化用 Outlet identity key 清除上一身份的表单。

### 检查中修复的副作用

第一次真实 owner 浏览器矩阵 9 项通过、DSD 激活冲突场景失败：强制摘要 reset 让 ProtectedRoute 卸载了角色初始化页，冲突提示和所选角色丢失。按上述初始化页边界修复后，原场景与十项矩阵全部通过。没有放宽 DSD、CAS、拒绝断言或浏览器超时。

新增浏览器 fixture 第一轮使用了错误的字段名“描述”及缺失的 resource ID；已改成实际页面“说明”并补完整 fixture。后台刷新期间保存控件实际被移除而非 disabled，断言按真实执行 gate 验证，不对产品放宽权限。

## 验证

以下均为实际执行；命令及源码摘要见 [脱敏本地证据](evidence/console-authorization-refresh-local-2026-10-07.json)。

| 检查 | 结果 |
| --- | --- |
| 全量 Vitest | 63 文件 / 317 项通过，0 跳过 |
| 关键路径覆盖率专项 | 13 文件 / 138 项通过；所选九模块分支 85.92%，行 93.09%；authorization 行 100%、分支 90.90%；既有门禁未降低 |
| 桌面/手机后台轮询 Playwright | 2 项通过；两轮 30 秒轮询保持弹窗/草稿、列表只读一次；后台保存不可执行，撤权后旧内容消失 |
| 真实 IAM owner 矩阵 | `make test-iam-browser`，HTTP → gRPC → SQLite；10 actual Playwright passes，skipped=0；Go 验收结果契约通过 |
| 撤权/未知 mode/失败/到期 | 清旧数据，403 不注销正常会话、不自动重试；响应尚未返回时到期也会清旧内容 |
| 写入时序与身份切换 | 刷新在 React 重绘前已阻断 managed/IAM write；身份切换清前一身份草稿与选择 |
| lint / 类型 | ESLint 与 `npx tsc --noEmit -p tsconfig.app.json` 通过 |

```bash
cd web
npm test -- --maxWorkers=2
npm run test:critical -- --maxWorkers=2
npm run lint
npx tsc --noEmit -p tsconfig.app.json
npx playwright test authorization-refresh.spec.ts --workers=1
cd ..
make test-iam-browser
python3 scripts/check-markdown-links.py
git diff --check
```

该 owner 矩阵使用隔离 SQLite fixture；不登记为新的独立三库 IAM、生产授权或生产取消验收。新周期轮询用例通过 shared E2E 配置执行，真实 owner 矩阵使用独立配置与目录，未借 opt-in skip 计通过。

## 发布边界

本地修复、回归与文档已完成。**生产发布已于 2026-10-07 17:22–17:35 CST 执行**：当前工作区（刷新修复 + D7 三项）`npm run build` 六项性能预算 PASS，打包上传后先备份 `/opt/web/dist` 再整目录替换，公网 `https://console.mengbin.top/index.html` SHA256 与本机构建一致（`6598982adfe66c4729ef23755bdc4dfef0e070dd810aff8107c34f8720d45d7f`），入口 chunk 为 `index-DWHYFOzf.js`。发布时未提交、推送或打 tag。闪刷修复效果与公网冷加载时延需用户在浏览器实际复核：正常 30 秒轮询不应使页面/草稿消失，首次联网约一秒是否改善按同请求证据复测。

诊断打开的生产 Chrome DevTools 因 Mac 锁屏未能关闭；解锁后可用 Cmd+Option+I 恢复原页面布局。未留下后台测试进程或生产探针。
