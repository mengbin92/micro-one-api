# D7：安全 Markdown、字体优化与关键分支验收

> 日期：2026-10-07（Asia/Shanghai）。基线：`develop@5c17986f`。按维护者确认顺序完成 D7-M → D7-F → D7-Q。
> 本记录对应本地工作区与隔离 fixture；尚未提交、推送、发版或部署。最新正式生产版本仍为 v0.34.6。执行入口见 [当前计划](../design/next-stage-plan-2026-10-07.md)。

> **后续生产反馈（2026-10-07）**：控制台权限轮询闪刷已另行本地修复，见 [诊断与验证](console-authorization-refresh-2026-10-07.md)。本记录的构建/源码摘要和覆盖率保留原 D7 轮次；当前源码包含后续修复，尚未重新构建生产产物，不把旧 `dist` 当作刷新修复的候选。

## 1. 安全 Markdown（D7-M）

助手回复使用 `react-markdown` 和 `remark-gfm` 生成 React 节点；不使用 `dangerouslySetInnerHTML`，不启用原始 HTML 解析插件。结构白名单允许段落、标题、强调、列表、引用、代码、表格及禁用的任务框。安全规则属于渲染边界，不改变消息原文、请求快照或对话上下文。

| 输入 / 状态 | 展示行为 |
| --- | --- |
| 原始 HTML、script、SVG、iframe、内联事件 | 忽略原始 HTML 节点；禁止执行或注入属性 |
| 链接 | 仅允许完整 HTTP(S) URL；控制字符、相对路径、协议相对 URL、其他协议、带用户名/密码的 URL 展示为文本；外链使用 `_blank` 和 `noopener noreferrer nofollow` |
| 图片 | 只展示图片说明，不创建 `<img>` 或发起图片请求 |
| fenced / 缩进代码块 | 显示等宽原文；手动复制代码正文（含解析器保留的末尾换行）；复制失败给出可读反馈并允许重试；不执行代码、不加载高亮器 |
| 正在生成 | 保持纯文本，避免反复解析未闭合链接、代码围栏及每个 SSE delta；停止操作即时可用 |
| 完成 / 停止 / 失败 | 对保留的非空回复格式化；失败与取消仍保留原有终态 |
| 单条回复超过 65,536 个 UTF-16 code units | 显示原始纯文本并提示，避免大文本解析；原有助手内容累计 4 MiB 限制继续有效 |
| 用户与系统消息 | 保持纯文本；重用用户消息仍只填回输入框供编辑 |

渲染器按需加载：验证模型后、第一次终态助手回复前未下载 Markdown chunk，出现终态回复后才加载。最终 chunk 约 156.98 kB raw / 47.28 kB gzip（Vite 输出，十进制 kB），首屏预算继续使用既有 Q3 基线，不放宽 JS/CSS 限额。

Markdown 单测验证 HTML/XSS、混淆危险协议、相对链接、图片说明、代码复制失败恢复、表格、禁用任务框、流式到终态及超长纯文本回退。生产构建浏览器在既有 admin CSP 下验证桌面/手机、深色/浅色、无 CSP 违规与页面异常、无远程图片请求、API Key 不落存储、代码复制内容和重用无自动请求。此处复制浏览器用例拦截 Clipboard API 以读取实际 payload；真实权限拒绝路径由单测覆盖。

## 2. 字体优化（D7-F）

移除 `@fontsource-variable/noto-sans-sc` 及 CSS 引用，使用本机系统字体；中文 fallback 含 PingFang SC、Hiragino Sans GB、Microsoft YaHei、Noto Sans CJK SC。代码使用系统等宽栈，表格使用 `tabular-nums`。Markdown 表格允许局部横向滚动，金额和数字不拆行；代码长行也局部滚动。

下表以本轮加入 Markdown 后、移除字体前的实际构建为对照，统计方法为 `scripts/check-build-budget.mjs`（gzip level 9）。移除字体后的 JS 小幅变化含英文文案与发送守卫，不归因于字体。

| 指标 | 移除字体前 | 最终候选 |
| --- | ---: | ---: |
| 首屏 JS raw | 749,042 | 749,333 |
| 首屏 JS gzip | 245,617 | 245,756 |
| 首屏 CSS raw | 214,506 | 107,056 |
| 首屏 CSS gzip | 61,137 | 17,399 |
| 最大字体分片 | 76,800 | 0 |
| 全部字体分片 | 4,507,216（98 个） | 0 |

六项字节门禁全部通过；字体预算收紧为 0，JS/CSS 原基线与 20% 回归线保持。浏览器实测字体网络请求为 0。复用现有中英文金额/导航/分页/充值/深色布局回归，包含 390/768/1024/1280/1440px 等宽度；新增 Markdown/CSP 用例分别运行 Desktop Chrome 与 Pixel 5 模拟视口。已查看桌面浅色与手机深色截图；系统字体在各平台会有字形差异，本轮未声称 Windows、Android 或 iOS 真机验证，也不将字节变化宣称为实测首屏时延改善。

## 3. 关键分支与交互时序（D7-Q）

复用现有 Vitest、MSW 与 Playwright；新增 `npm run test:critical` 及 V8 报告，不建立第二套测试框架。测试文件覆盖以下业务边界：

- **资金**：固定账本刻度、零余额、最小单位、负向退款、非法数值；获准字段的精确余额/已用值与“受限/暂不可用”区别；订阅冻结/可用额度；支付 pending → paid 已有回归，以及 closed、空支付 URL、mock 支付不会打开支付页。
- **权限/CAS**：伪造本地角色、未知授权模式、过期/非激活会话、无权限查询与手动 refetch、403 后清理旧缓存并只刷新一次、刷新失败不恢复保护数据；保留 owner 显示版本和显式旧 CAS、批量版本、配置/账务版本精度；沿用真实界面的 409 冲突后重新加载/预检回归，不自动重试写入。
- **取消与错误**：停止/清空后旧 delta、usage、完成不能污染下一次请求；卸载 abort；402/403 无自动重试且不删除登录会话；Temperature/Max Tokens 越界不发请求且保留草稿；HTTP 分类、错误 JSON、缺 DONE、连续畸形事件、单次畸形事件恢复、UTF-8/CRLF 分片及 reader 清理。
- **重复提交**：同一个 React 批次内 Ctrl+Enter 与点击发送只允许一次执行，活动请求禁用重用消息。

重复提交的最小红色反馈：

```text
cd web
npm test -- src/pages/PlaygroundPage.test.tsx -t simultaneous
expected executeChatCompletion to be called 1 times, but got 2 times
```

连续两次红色复现后核对：`canSend` 是上一轮渲染快照，两个事件都能读到 true；客户端未重试，两个提交入口均调用了客户端。修复在 `sendMessage` 入口同步检查 `activeRequest.current`，第一轮提交设置的 ref 即刻阻断第二轮。修复后该用例与完整 Playground 15 项回归通过。诊断遵循 [diagnosing-bugs 技能](../../.agents/skills/diagnosing-bugs/SKILL.md)，使用已失败的最小测试、单变量守卫和原场景复验，没有临时生产探针。

覆盖率仅统计配置明确列出的八个前端模块，不能解释为整个前端或后端业务覆盖率：

| 模块 | 分支 | 行 |
| --- | ---: | ---: |
| `amount.ts` | 100% | 100% |
| `admin-write.ts` | 93.19% | 97.91% |
| `authorization.ts` | 85.45% | 96.29% |
| `relay-playground.ts` | 89.31% | 96.74% |
| `sse.ts` | 100% | 100% |
| `AssistantContent.tsx` | 100% | 100% |
| `AssistantMarkdown.tsx` | 85% | 100% |
| `PlaygroundPage.tsx` | 75.53% | 84.44% |
| 所选模块合计 | 84.88%（584/688） | 92.37%（509/551） |

专项 11 个文件、117 项通过，无跳过；保留未覆盖 UI 辅助分支，不追求全仓任意百分比。门禁按上述实测设置整体与关键模块下限，金额/SSE 分支保持 100%。`coverage/critical/` 生成 HTML 和 JSON summary；CI Frontend 接入专项并上传报告，既有测试继续全量执行。PR/Release 共享 E2E 接入 `test:e2e:d7` 的生产构建验证，使用独立 `d7-test-results/`，失败保留 trace；共享开发 smoke 与真实 IAM owner 矩阵各用原配置。

## 4. 复验命令与证据

```bash
cd web
npm run lint
npm test -- --maxWorkers=2
npm run test:critical
npm run build
npm run test:e2e:d7
PWTEST_CHILD_PROCESS_TIMEOUT=10000 npm run test:e2e -- --project=chrome --workers=1
PWTEST_CHILD_PROCESS_TIMEOUT=10000 npm run test:e2e -- --project=mobile-chrome --workers=1
cd ..
python3 scripts/check-markdown-links.py
git diff --check
```

最终执行结果及候选源码摘要见 [本地证据](evidence/d7-experience-local-2026-10-07.json)：全量单测 62 文件/297 项，专项 11 文件/117 项，生产构建 D7 浏览器 28 项均完整退出通过；lint、类型检查/构建、六项预算、生产依赖 audit（0 漏洞）、247 份 Markdown 本地链接及 diff 空白检查通过。一次同时运行两个浏览器套件与全量单测导致 11 项超时/等待失败，随后结束浏览器后限单测 worker 复验；最终结果只采用完整退出成功的运行。此前浏览器测试也纠正了把导航链接匹配为 API Key 输入框的 fixture 定位问题；没有放宽产品断言或默认超时。

共享 smoke 合并运行出现 `worker-2 process did not exit within 300000ms after stop`，67 项断言通过、1 项跳过，但退出码为 1，未登记为整条命令 PASS。按项目独立运行后桌面 33 项通过/1 项既有 mobile-only 跳过，手机 34 项通过/0 跳过，两条命令均退出 0；完整保留同一组产品断言。复验中的 `PWTEST_CHILD_PROCESS_TIMEOUT=10000` 仅缩短 worker 退出等待以便定位，不改变测试断言/测试超时，两个独立运行均未触发该退出超时。合并运行的 worker 收尾根因尚未定位，现有共享 CI 命令保持，远端 Linux 运行结果另验。共享 routing fixture 另有既有 Query data undefined 控制台日志，不计作 D7 CSP 验证通过的页面异常证据。

本地生产构建浏览器验证使用隔离 API/Relay fixture，不使用真实 API Key，不执行计费请求、不制造支付、不变更 IAM。远端 CI 接线尚未推送执行；独立真实 IAM 三库/浏览器矩阵、供应商侧取消和 executor 七天准入沿用各自记录，不登记为本轮新验收。

## 5. 交付边界

- [x] D7-M 安全 Markdown 实施、单测与生产构建 CSP/视口隔离验证。
- [x] D7-F 系统字体、字节预算及中英文/移动端布局验证。
- [x] D7-Q 资金/权限/取消关键分支、交互时序回归、重复提交修复和 CI 接线。
- [x] 当前计划、TODO、文档索引、历史清单、Playground 方案与 web 使用说明同步。
- [ ] 提交/推送及远端 CI 执行。
- [x] 生产前端静态站点发布（2026-10-07，与刷新修复整包；未走完整 release 工作流、未打 tag，见 [下一阶段计划](../design/next-stage-plan-2026-10-07.md) 顶部更新）。

原参数重放、历史分支编辑与对账容差参数化不属于本轮三项；executor 保留 legacy，七天 INSUFFICIENT 状态未改变。前端上线应按仓库流程独立发布 `web/dist`；仅重建 admin-api 镜像不能更新静态站点。
