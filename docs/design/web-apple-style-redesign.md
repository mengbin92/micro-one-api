# Web 界面 Apple 风格重设计

> 制定日期：2026-08-26
>
> 状态：**Phase A、Phase B、Phase C 已完成**（2026-08-26）；代码审查与自动化验收通过
>
> 范围：`web/` 前端界面与相关静态资源
>
> 设计目标：参考 Apple 平台的清晰、克制与层次感，形成 Micro-One API 自有的 Web 视觉语言
>
> 设计边界：本项目不是 Apple 官方产品，不复制 Apple 商标、Logo、专有字体或受保护素材

## v4 审查结论

本轮审查修复了以下问题：

1. **字体方案不完整**：Geist 虽为 OFL-1.1，但中文依赖不确定的系统回退；`logo-wordmark.svg` 还声明了未打包的 Inter。统一替换为自托管的 Noto Sans SC，并把许可证纳入发布物。
2. **Phase 0 不是纯机械删除**：36 处旧样式中只有 30 处位于 `Card`，其余 6 处是表格容器或标签，删除整个 `className` 会误删布局类或让表面样式丢失。
3. **Tailwind v4 圆角 token 位置错误**：`--radius-sm` 等必须在顶层 `@theme` 中定义，写进 `:root` 不会更新 `rounded-*` 工具类。
4. **图表颜色语法错误**：`--chart-*` 保存的是完整 hex 颜色，Recharts 应使用 `var(--chart-N)`；`hsl(var(--chart-N))` 是无效组合。
5. **语义 token 与组件直写色冲突**：按钮、输入框和标签仍直写 hex，无法随深色模式切换。本版统一要求使用语义类。
6. **对比度声明不完整**：`#007AFF` 上的白色小字约为 4.02:1，不满足 WCAG AA 普通文本 4.5:1；11px 的 `#86868B` 也不合格。本版改用可访问的交互色与最小字号。
7. **动效范围过宽**：`transition: all` 可能意外动画布局属性，且缺少 `prefers-reduced-motion`。本版改为属性级过渡并补充减弱动效策略。
8. **中文排版被过度压缩**：全局给 `h1/h2/h3` 设置负字距会伤害中文可读性。本版只在拉丁字符占主导的展示标题上按需使用 `tracking-tight`。
9. **文件清单与阶段数量不一致**：原文的 Phase A/B 数量、LoadingStates 归属以及图表组件清单互相冲突，本版重新归类并补齐。
10. **实施与设计约束仍有偏差**：最终代码审查发现输入框高度与内边距冲突、弹出菜单按钮仍参与按压缩放、主题切换按钮未做成圆形、部分配额条残留 `transition-all` 和 10/11px 文字，以及登录 tab 的方向键焦点未跟随；现已统一修复并补充回归测试。

---

## 背景与现状基线

当前前端使用 React 19、Tailwind CSS v4、shadcn/ui（base-nova）与 Recharts。实施前的源码基线如下，统计范围为 `web/src/**/*.{tsx,css}`：

| 项目 | 实测结果 | 说明 |
|---|---:|---|
| `ring-1 ring-slate-200` | 36 处 / 13 个文件 | 30 个 `Card`，6 个普通 `div` |
| `slate-*` | 440 处 | 需按语义逐处判断，禁止盲目全局替换 |
| `font-black` | 73 处 / 15 个文件 | 大多数应降为 600/700，但数据强调可保留个例 |
| `rounded-lg` | 156 处 | 表单、按钮、表格容器含义不同，不能一刀切 |
| 图表硬编码 hex | 多处 | 集中在 Dashboard、CostAnalysis、CostCharts、HealthCharts |
| 当前 Web 字体 | Geist Variable | OFL-1.1，但不完整覆盖简体中文 |
| Logo 字体声明 | Inter + 系统回退 | 字体未嵌入，独立打开 SVG 时结果不可控 |

复核命令：

```bash
rg -n "ring-1 ring-slate-200" web/src -g '*.tsx'
rg -o "slate-[0-9]+" web/src -g '*.tsx' | wc -l
rg -l "font-black" web/src -g '*.tsx' | wc -l
rg -o "rounded-lg" web/src -g '*.tsx' | wc -l
```

> 基线只是迁移导航，不是“所有匹配项必须归零”的依据。状态色、信息密度和组件尺寸都需要保留语义。

---

## 设计原则

| 原则 | Web 落地方式 |
|---|---|
| 清晰 | 高对比层级、14px 以上正文、明确焦点态、内容优先 |
| 克制 | 单一主交互色，红/绿/橙仅表达状态，不为每张卡片随机配色 |
| 层次 | 页面、表面、浮层三级背景；低对比描边配合柔和阴影 |
| 适度透明 | 毛玻璃只用于导航和浮层，不在所有卡片上滥用模糊 |
| 适度动效 | 150–250ms 属性级过渡，支持 reduced motion |
| 自有品牌 | 参考交互原则，不复制 Apple 字体、资产、文案或商标呈现 |

### 非目标

- 不追求像素级复刻 macOS/iOS。
- 不在本次视觉改造中重写业务流程、路由结构或 API。
- 不把全部 `slate-*`、`rounded-lg` 或 `font-black` 做无语义的全局替换。
- 不依赖 Google Fonts 或其他第三方字体 CDN，避免隐私、可用性和版本漂移问题。

---

## Phase A — 字体、许可与设计令牌

### A1. 字体替换与许可合规

统一字体为 **Noto Sans SC Variable**：覆盖简体中文与拉丁字符，字重范围 100–900，采用 SIL Open Font License 1.1，可随 Web 应用自托管和再分发。实现时仍需履行许可证随附义务；“开源字体”不等于可以删除版权与许可声明。

官方依据：

- [Noto CJK 官方许可证（SIL OFL 1.1）](https://github.com/notofonts/noto-cjk/blob/main/Sans/LICENSE)
- [Fontsource 的 Noto Sans SC 安装说明](https://fontsource.org/fonts/noto-sans-sc/install)

依赖变更：

```bash
cd web
npm uninstall @fontsource-variable/geist
npm install @fontsource-variable/noto-sans-sc
```

`web/src/index.css`：

```css
@import "@fontsource-variable/noto-sans-sc/wght.css";

@theme inline {
  --font-sans: "Noto Sans SC Variable", ui-sans-serif, system-ui, sans-serif;
  --font-heading: var(--font-sans);
}
```

实施要求：

- 提交 `web/package.json` 与 `web/package-lock.json` 的配套变更，不手改 lockfile。
- 将安装包中的 `LICENSE` 原文复制为 `web/public/licenses/NotoSansSC-OFL-1.1.txt`，不得摘要或改写许可证正文。
- 字体由 Vite 打包到同源静态资源；生产运行时不得请求 Google Fonts、Fontsource CDN 或 jsDelivr。
- 不预加载全部 CJK 字体分片；只在真实首屏性能数据证明有收益时预加载必要资源。
- UI 常用字重限定为 400/500/600/700，避免继续依赖 900 制造层级。
- `web/public/logo-wordmark.svg` 中的活字必须显式加载同源 Noto Sans SC Latin 子集，或转为路径；不得继续依赖客户端安装的 Inter、SF Pro 等字体。
- 支付跳转页中的临时 `sans-serif` 文本属于浏览器原生兜底，不分发字体文件，可保留；若改为品牌字体，必须注入同源 `@font-face`，不能引用外部 CDN。

字体验收：

```bash
! rg -n "Geist|Inter|SF Pro|-apple-system|BlinkMacSystemFont" \
  web/src web/public web/package.json web/package-lock.json
test -f web/public/licenses/NotoSansSC-OFL-1.1.txt
```

并在构建产物中确认：

- 中文、英文、数字无 FOUT 后跳回其他字体的现象；
- Network 面板没有第三方字体请求；
- `dist/assets` 不再包含 Geist 文件；
- 字体资源使用长期缓存，首屏传输量与改造前基线对比并记录。

### A2. Tailwind v4 token 结构

现有 shadcn 颜色映射继续保留在顶层 `@theme inline`。颜色的运行时值写在 `:root` / `.dark`；会生成工具类的圆角、阴影、字体和缓动值写在顶层 `@theme inline`。不要在 `.dark` 内嵌套 `@theme`。

```css
@theme inline {
  --font-sans: "Noto Sans SC Variable", ui-sans-serif, system-ui, sans-serif;
  --font-heading: var(--font-sans);

  --radius-sm: 0.25rem;   /* 4px */
  --radius-md: 0.375rem;  /* 6px */
  --radius-lg: 0.5rem;    /* 8px */
  --radius-xl: 0.75rem;   /* 12px */
  --radius-2xl: 1rem;     /* 16px */
  --radius-3xl: 1.25rem;  /* 20px */

  --shadow-surface-sm: var(--surface-shadow-sm);
  --shadow-surface-md: var(--surface-shadow-md);
  --shadow-surface-lg: var(--surface-shadow-lg);
  --ease-standard: cubic-bezier(0.25, 0.1, 0.25, 1);

  --color-primary-hover: var(--primary-hover);
  --color-chart-grid: var(--chart-grid);
  --color-chart-label: var(--chart-label);
}
```

> 当前 `index.css` 用 `--radius` 推导整套圆角。实施时应删除这组推导，直接采用上表的离散值，否则无法得到 4/6/8/12/16/20px 的目标体系。

### A3. 浅色模式语义值

```css
:root {
  --background: #F5F5F7;
  --foreground: #1D1D1F;
  --card: #FFFFFF;
  --card-foreground: #1D1D1F;
  --popover: #FFFFFF;
  --popover-foreground: #1D1D1F;
  --primary: #0066CC;
  --primary-hover: #0055B3;
  --primary-foreground: #FFFFFF;
  --secondary: #F2F2F7;
  --secondary-foreground: #1D1D1F;
  --muted: #F2F2F7;
  --muted-foreground: #6E6E73;
  --accent: #EAF3FF;
  --accent-foreground: #004C99;
  --destructive: #D70015;
  --border: rgb(0 0 0 / 0.08);
  --input: rgb(0 0 0 / 0.12);
  --ring: #0066CC;
  --sidebar: #F5F5F7;
  --sidebar-foreground: #1D1D1F;
  --sidebar-primary: #0066CC;
  --sidebar-primary-foreground: #FFFFFF;
  --sidebar-accent: #EAF3FF;
  --sidebar-accent-foreground: #004C99;
  --sidebar-border: rgb(0 0 0 / 0.08);
  --sidebar-ring: #0066CC;

  --chart-1: #007AFF;
  --chart-2: #5E5CE6;
  --chart-3: #248A3D;
  --chart-4: #B25000;
  --chart-5: #6E6E73;
  --chart-grid: #D2D2D7;
  --chart-label: #6E6E73;

  --surface-shadow-sm: 0 1px 3px rgb(0 0 0 / 0.04), 0 1px 2px rgb(0 0 0 / 0.06);
  --surface-shadow-md: 0 4px 12px rgb(0 0 0 / 0.06), 0 1px 3px rgb(0 0 0 / 0.04);
  --surface-shadow-lg: 0 12px 40px rgb(0 0 0 / 0.08), 0 2px 8px rgb(0 0 0 / 0.04);
}
```

交互主色使用比 `#007AFF` 更深的 `#0066CC`，确保白色 14px 按钮文字达到约 5.57:1；`#007AFF` 只保留给不承载小号白字的图表或装饰元素。

### A4. 深色模式语义值

```css
.dark {
  --background: #000000;
  --foreground: #F5F5F7;
  --card: #1C1C1E;
  --card-foreground: #F5F5F7;
  --popover: #1C1C1E;
  --popover-foreground: #F5F5F7;
  --primary: #0A84FF;
  --primary-hover: #409CFF;
  --primary-foreground: #000000;
  --secondary: #2C2C2E;
  --secondary-foreground: #F5F5F7;
  --muted: #2C2C2E;
  --muted-foreground: #98989D;
  --accent: #26364A;
  --accent-foreground: #D6EAFF;
  --destructive: #FF6961;
  --border: rgb(255 255 255 / 0.10);
  --input: rgb(255 255 255 / 0.16);
  --ring: #0A84FF;
  --sidebar: #000000;
  --sidebar-foreground: #F5F5F7;
  --sidebar-primary: #0A84FF;
  --sidebar-primary-foreground: #000000;
  --sidebar-accent: #26364A;
  --sidebar-accent-foreground: #D6EAFF;
  --sidebar-border: rgb(255 255 255 / 0.10);
  --sidebar-ring: #0A84FF;

  --chart-1: #0A84FF;
  --chart-2: #7D7AFF;
  --chart-3: #30D158;
  --chart-4: #FF9F0A;
  --chart-5: #98989D;
  --chart-grid: #38383A;
  --chart-label: #98989D;

  --surface-shadow-sm: 0 1px 3px rgb(0 0 0 / 0.28);
  --surface-shadow-md: 0 6px 18px rgb(0 0 0 / 0.36);
  --surface-shadow-lg: 0 16px 48px rgb(0 0 0 / 0.48);
}
```

深色主按钮使用黑色文字；`#0A84FF` 与黑色约为 5.76:1。不能沿用白色文字。

### A5. 排版、数字与动效规则

- `body` 使用 `bg-background text-foreground font-sans`。
- 不全局修改 `h1/h2/h3` 字距。中文标题默认 `tracking-normal`；拉丁字符占主导的展示标题可局部使用 `tracking-tight`。
- 正文与表格内容不低于 14px；12px 只用于辅助元数据且必须使用 `text-muted-foreground`，不得使用低对比的 `#86868B`。
- 金额、Token 数、百分比、日期列按需使用 `tabular-nums`，不要给所有页面文字全局开启。
- 禁止新增 `transition-all`。颜色变化用 `transition-colors`，位移/缩放用 `transition-transform`，阴影用 `transition-shadow`。
- 只在 `motion-safe:` 下启用按压缩放、微光和弹窗缩放；`motion-reduce:` 下移除非必要动画并把时长降为 0。

---

## Phase B — 共享组件与布局

### B1. `web/src/components/ui/card.tsx`

基础表面使用语义 token：

```text
rounded-2xl border border-border bg-card text-card-foreground shadow-surface-sm
```

- 不给不透明卡片添加 `backdrop-blur`，它没有视觉收益且会增加合成开销。
- 统一梳理容器垂直间距，避免 Card 自身 `py-*` 与 Header/Content 再次叠加；建议 Card `gap-0 py-0`、Header `px-6 py-5`、Content `px-6 py-5`。
- 分割线保持 opt-in：只有调用方传入 `border-b` 时才显示，不强制所有 CardHeader 出现分割线。
- 保留 `size="sm"` 的紧凑规则及首尾图片圆角行为，避免共享组件回归。

### B2. `web/src/components/ui/button.tsx`

- `default`：`bg-primary text-primary-foreground hover:bg-primary-hover`。
- `secondary`：`bg-secondary text-secondary-foreground hover:bg-secondary/80`。
- `outline`：`border-border bg-background hover:bg-muted`。
- `ghost`：`hover:bg-muted`，不硬编码浅色或深色背景。
- 保留 destructive、link、icon 与 button-group 的现有语义和尺寸差异；不能把所有尺寸粗暴改成同一个圆角。
- 焦点态使用 `focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/30`，键盘焦点不可被 hover/active 覆盖。
- 按压反馈使用 `motion-safe:active:scale-[0.98] transition-transform duration-150 ease-standard`，弹出菜单按钮除外。

### B3. `web/src/components/ui/input.tsx`

```text
rounded-xl border border-input bg-background px-4 py-2.5 text-sm
placeholder:text-muted-foreground
focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/25
transition-[border-color,box-shadow] duration-200 ease-standard
```

保留 `aria-invalid`、disabled、文件输入和 dark variant 的现有行为；不要用 `focus:` 代替 `focus-visible:`，不要重新直写 `#007AFF`。

### B4. `web/src/components/ui/table.tsx`

- 表头最小 12px、`font-semibold text-muted-foreground`；仅英文标签可用 uppercase，中文不做无效的 uppercase。
- 表体保持 14px，行高约 48px，金额和数量列使用 `tabular-nums`。
- 行 hover 使用 `hover:bg-muted/50`；选中态必须比 hover 更明确。
- 分割线使用 `border-border`，不得直写只适合浅色模式的黑色透明值。
- 状态不能只靠颜色表达，必须同时保留文本或图标。
- 保留横向滚动容器、checkbox padding 和 `whitespace-nowrap` 行为。

### B5. `web/src/components/ui/dialog.tsx`

- 遮罩：`bg-black/30 supports-backdrop-filter:backdrop-blur-sm`。
- 内容：`rounded-2xl border border-border bg-popover shadow-surface-lg`。
- 延续 Base UI 的 `data-open` / `data-closed` 状态类，淡入与 0.95 → 1 缩放为 200–250ms。
- `motion-reduce:` 下只保留瞬时显隐；关闭按钮继续具备可访问名称和可见焦点。

### B6. `web/src/components/AppNavigation.tsx`

- 桌面侧边栏：`w-64 bg-sidebar`；支持 backdrop-filter 时再叠加 `bg-sidebar/70 backdrop-blur-xl backdrop-saturate-[1.8]`。
- 同步三处布局类：aside `w-72 → w-64`、header `md:left-72 → md:left-64`、main `md:ml-72 → md:ml-64`。
- `MobileNav.tsx` 的 `max-w-72` 是移动抽屉最大宽度，不属于这组三处联动，禁止误改。
- 导航项保持至少 40px 高；激活态同时使用背景、文字和 `aria-current`，不能只用颜色。
- 分组标签使用 12px `text-muted-foreground`，不再使用 11px 低对比装饰字。

### B7. `web/src/components/ProtectedRoute.tsx`

- 根容器改为 `bg-background text-foreground`，删除 `bg-[#f3f7ff]` 与重复 dark 类。
- main 使用 `md:ml-64`，内容 padding 保持移动端 `px-4`，逐级提升到 `sm:px-6 lg:px-8`。
- header、aside、main 在 768px 边界必须无空隙、无重叠。

---

## Phase C — 旧覆盖迁移与页面润色

### C1. 迁移 36 处旧表面样式

先区分元素类型，禁止删除整个 `className`：

- **30 个 Card**：只移除 `rounded-* border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10` 等旧视觉 token；保留 `min-h-*`、`w-full`、`flex`、`xl:col-span-*` 等布局类。
- **6 个普通 div**：不能回落到 Card 默认值，需显式改为语义表面。
  - PricingPage 的信息标签：`rounded-xl border border-border bg-card shadow-surface-sm`。
  - UsagePage、OrdersPage、PaymentOrdersPage 与 ReconciliationPage 的 5 个表格容器：`overflow-x-auto rounded-2xl border border-border bg-card shadow-surface-sm`。

文件与现有匹配数：

| 文件 | 数量 |
|---|---:|
| `web/src/components/AdminRoute.tsx` | 1 |
| `web/src/pages/admin/ChannelHealthPage.tsx` | 1 |
| `web/src/pages/admin/CostAnalysisPage.tsx` | 1 |
| `web/src/pages/admin/OverviewPage.tsx` | 11 |
| `web/src/pages/admin/PaymentOrdersPage.tsx` | 1 |
| `web/src/pages/admin/ReconciliationPage.tsx` | 2 |
| `web/src/pages/ApiGuidePage.tsx` | 5 |
| `web/src/pages/DashboardPage.tsx` | 4 |
| `web/src/pages/OrdersPage.tsx` | 1 |
| `web/src/pages/PlaygroundPage.tsx` | 3 |
| `web/src/pages/PricingPage.tsx` | 2 |
| `web/src/pages/ProfilePage.tsx` | 3 |
| `web/src/pages/UsagePage.tsx` | 1 |

验收命令使用 `rg` 的退出码，不使用会逐文件输出计数的 `grep -rc`：

```bash
! rg -n "ring-1 ring-slate-200" web/src -g '*.tsx'
```

### C2. Dashboard

- 问候语采用响应式字号：`text-3xl sm:text-4xl lg:text-5xl font-bold`，中文保持 `tracking-normal`。
- 图表、快捷操作和指标卡迁移到共享 Card，不在页面重复写 `bg-white` / `dark:bg-card`。
- Recharts 的 `stroke`、`fill`、渐变 stop、坐标轴和网格线分别使用 `var(--chart-N)`、`var(--chart-label)`、`var(--chart-grid)`。
- 多序列图除颜色外还使用图例、标签或不同线型，照顾色觉差异。

### C3. Admin Overview 与图表组件

- `StatCard` / `CostCard` 复用 Card 表面；图标色保留业务语义，不再随机配色。
- 进度条用 `h-1.5 rounded-full`，动画仅在 `motion-safe:` 下启用。
- 修改 `web/src/components/admin/CostCharts.tsx`、`HealthCharts.tsx` 与 `web/src/pages/admin/CostAnalysisPage.tsx` 的硬编码色。
- Recharts 属性必须写成 `stroke="var(--chart-1)"` 或 React 字符串值；禁止 `hsl(var(--chart-1))`。
- Tooltip 也使用 `bg-popover text-popover-foreground border-border`，不能只迁移图形颜色。

### C4. Login

- 桌面端可使用左右分屏；小于 `lg` 时隐藏纯装饰品牌面板，表单单列显示，避免 375px 视口溢出。
- 表单字段必须保留可见 label、自动完成属性、错误关联和键盘顺序。
- 登录/注册切换使用 tab/segmented control 语义，不只做 pill 外观。
- 同步更新 `LoginPage.test.tsx`；断言优先按 role、label、可见文本定位，不绑定无意义 DOM 层级。

### C5. Playground、加载与空状态

- Playground 消息气泡和输入区使用语义 token，用户/助手身份同时以对齐、标签或图标区分。
- 流式文本淡入、骨架微光只在 `motion-safe:` 下启用；reduced motion 下使用静态占位。
- `LoadingStates.tsx` 与 `ui/skeleton.tsx` 共用同一骨架动画，避免两套 keyframes。
- EmptyState 增加留白，但操作按钮在移动端仍保持可见且触控目标不小于 40px。

### C6. 细节组件

- `AccountStatusBadge.tsx`：`rounded-lg px-2.5 py-1`，保留状态文字/图标。
- `ui/label.tsx`：`text-sm font-medium text-foreground`，禁止直写浅色文字值。
- `ui/dropdown-menu.tsx`：`rounded-xl border-border shadow-surface-md`，条目保持键盘焦点与选中态。
- `ThemeToggle.tsx`：圆形图标按钮，保持可访问名称、tooltip 与 focus-visible。

---

## 完整修改清单

| 阶段 | 文件 |
|---|---|
| 字体/许可 | `web/package.json`、`web/package-lock.json`、`web/src/index.css`、`web/public/fonts/noto-sans-sc-latin-wght-normal.woff2`、`web/public/licenses/NotoSansSC-OFL-1.1.txt`、`web/public/logo-wordmark.svg` |
| 共享基础 | `ui/card.tsx`、`ui/button.tsx`、`ui/input.tsx`、`ui/table.tsx`、`ui/dialog.tsx`、`AppNavigation.tsx`、`ProtectedRoute.tsx` |
| 旧覆盖迁移 | C1 表格列出的 13 个文件 |
| 重点页面 | `DashboardPage.tsx`、`LoginPage.tsx`、`PlaygroundPage.tsx`、`admin/OverviewPage.tsx`、`admin/CostAnalysisPage.tsx` |
| 图表 | `components/admin/CostCharts.tsx`、`components/admin/HealthCharts.tsx` |
| 状态与细节 | `LoadingStates.tsx`、`EmptyState.tsx`、`components/admin/AccountStatusBadge.tsx`、`ui/skeleton.tsx`、`ui/dropdown-menu.tsx`、`ui/label.tsx`、`ThemeToggle.tsx` |
| 测试 | 至少复核 `LoginPage.test.tsx`、`PlaygroundPage.test.tsx`、`AppNavigation.test.tsx`、`CostCharts.test.tsx` |

> 同一文件可能跨多个阶段，例如 `index.css` 同时承载字体与 token；清单按职责列出，不代表重复修改。

---

## 验证方案

### 自动化

```bash
cd web
npm run lint
npm run test
npm run build
npm run test:e2e
```

同时执行：

```bash
! rg -n "ring-1 ring-slate-200" src -g '*.tsx'
! rg -n "Geist|Inter|SF Pro|-apple-system|BlinkMacSystemFont" \
  src public package.json package-lock.json
test -f public/licenses/NotoSansSC-OFL-1.1.txt
test -f public/fonts/noto-sans-sc-latin-wght-normal.woff2
```

2026-08-26 实施验收结果：ESLint 通过，Vitest 40 个测试文件 / 141 个测试通过，生产构建通过，Playwright E2E 35 项通过、1 项按桌面项目配置跳过；浏览器复核覆盖 1440px 桌面登录页与 390px 移动登录/注册页，未发现控制台错误。

### 视觉与无障碍

1. 对 375px、768px、1440px 三个宽度分别检查浅色和深色模式。
2. 覆盖登录、Dashboard、Playground、管理总览、长表格、空状态、加载状态与弹窗。
3. 仅用键盘完成导航、主题切换、表单提交、Dialog 打开/关闭和下拉菜单选择；焦点始终可见。
4. 抽查普通文本 4.5:1、非文本 UI 边界/焦点 3:1；不能只验证 `muted-foreground` 一个组合。
5. 系统开启“减少动态效果”后，确认按压缩放、微光、流式淡入和弹窗缩放停止。
6. 图表在两种主题下检查坐标轴、网格、Tooltip、Legend 与各序列；信息不能只靠颜色区分。
7. DevTools Network 确认字体同源加载、无第三方请求、无重复 Geist/Noto 资源。

---

## 风险评估

| 风险 | 等级 | 缓解措施 |
|---|---|---|
| CJK 字体增加首屏资源量 | 高 | 使用 Fontsource unicode-range、自托管、禁止整字体预加载，记录改造前后传输量 |
| 误删布局类 | 高 | Card 只删视觉 token，普通 div 按 C1 单独迁移；逐文件 diff 审查 |
| 深浅主题直写色残留 | 中 | 共享组件只用语义类；按页面扫描 hex 与 `slate-*` 并人工判断 |
| 主色或小字对比度不足 | 中 | 使用本版可访问交互色；普通文字 ≥14px；逐状态抽查而非只测静态页面 |
| Recharts CSS 变量失效 | 中 | 使用 `var(--chart-N)`，覆盖渐变、轴线、Tooltip 和导出/截图场景 |
| reduced motion 遗漏 | 中 | 动效统一加 `motion-safe:` / `motion-reduce:`，系统偏好下专项验证 |
| Logo 字体渲染漂移 | 中 | SVG 显式加载同源 Noto Latin 子集（或转路径）并保留 OFL 许可证，不依赖客户端本地字体 |
| 侧边栏宽度联动遗漏 | 低 | 只同步 aside/header/main 三处，并明确排除 MobileNav 的 `max-w-72` |
