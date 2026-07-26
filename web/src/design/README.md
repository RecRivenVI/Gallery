# Gallery 设计系统

本目录是画廊与管理两套界面**唯一**的视觉与交互事实源。任何一端都不得复制一份外观不同的按钮、
颜色或间距；差异只允许通过密度 token 和组合方式表达。

本文件是产品语言的定稿，其余工作线按它实现。与实际代码不一致时，以 `tokens.css`、
`primitives.tsx` 的代码为准并回来修正本文档。

---

## 1. 术语表

| 术语                    | 含义                                                        | 不是什么             |
| ----------------------- | ----------------------------------------------------------- | -------------------- |
| **画廊（Gallery）**     | 面向浏览与观看的界面，入口 `/`，可安装 PWA                  | 不是"登录后的管理端" |
| **管理（Manage）**      | 面向诊断与操作的界面，入口 `/manage`，不进 PWA scope        | 不是画廊的一个页签   |
| **界面身份（Surface）** | `gallery` \| `manage`。决定默认密度与 `:root[data-surface]` | 不是主题             |
| **主题（Theme）**       | `system` \| `light` \| `dark` \| `high-contrast`，两端共享  | 不是密度             |
| **密度（Density）**     | `comfortable` \| `compact`，按界面分别记忆                  | 不是缩放             |
| **token**               | `tokens.css` 中的 CSS 自定义属性                            | 不是组件 props       |
| **primitive**           | `primitives.tsx` 导出的共享组件                             | 不是页面级布局       |
| **快照（Snapshot）**    | 服务端签发的 HTTP 结果；实时事件只是"去重新问一次"的提示    | 不是本地累积的列表   |
| **capability**          | 服务端 global scope 的能力名，只用于隐藏明显不可用的入口    | 不是授权判断         |

一致的中文用词（不要混用同义词）：

- **作品** / **媒体** / **创作者** / **Source** / **Library** / **规则** / **任务** / **绑定** / **快照**
- 动作用词：**登记** Source、**扫描**、**发布**、**确认内容**、**重试**、**取消**、**吊销**
- 不用"同步""导入媒体""上传"——Gallery 永不写入 Source，这些词会误导用户。

---

## 2. 产品语言：两端共享设计语言，职责与信息密度分离

### 画廊：沉浸、内容优先

- **大图先行。** 封面与媒体是页面的主体，控件退到边缘。默认卡片最小宽度不小于 15rem。
- **低 chrome。** 导航、工具栏、状态条合计不超过视口高度的 15%；滚动时非必要 chrome 收起。
- **宽松间距。** 区块间距用 `--space-5`/`--space-6`，卡片内边距 `--space-4`。
- **少即是多。** 一屏最多一个主操作。批量操作、诊断信息、内部 ID 不出现在画廊主路径上。
- **密度固定 `comfortable`**，控件高度 `--control-height` = 2.75rem，符合 44px 触控目标。
- **文字层级。** 页面标题 `--text-2xl`，区块标题 `--text-lg`，正文 `--text-base`。

### 管理：信息密集、状态优先

- **表格是一等公民。** 列表默认用表格而不是卡片；行高由 `--row-height` 统一。
- **状态先于名称。** 每行第一眼要能看出成功/进行中/失败/被阻塞，用 `Badge` 表达。
- **紧凑行高。** 密度固定 `compact`，`--control-height` = 2rem、`--row-height` = 2.25rem。
  区块间距用 `--space-3`/`--space-4`。
- **可诊断。** 稳定 code、关联 ID、快照 ID、协议版本必须可见且可复制，用 `--font-mono`。
- **危险操作显式化。** 吊销、删除、维护类操作一律经 `Dialog` 二次确认，按钮用 `danger`。
- **不隐藏失败。** 部分失败要逐项列出，不允许只显示一个汇总的"操作失败"。

### 两端共同遵守

- 服务端拥有排序、过滤、分页与授权语义，界面**不得**本地重排服务端列表。
- 任何列表都要显示它来自哪个快照（画廊可以弱化到详情里，管理端必须常驻）。
- 用户事实（收藏、进度、覆盖、隐藏）与规则派生事实在视觉上必须可区分，不能让用户以为
  重扫会覆盖自己的编辑。

---

## 3. 状态语义

每个数据区域必须能表达以下五种状态之一，且**不得互相冒充**。

| 状态              | 何时使用                                          | 组件                        | 要点                                                                                                                 |
| ----------------- | ------------------------------------------------- | --------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| **加载中**        | 请求进行中，尚无可显示结果                        | `Spinner`（含无障碍文本）   | 首屏用骨架或占位块避免布局跳动；已有旧数据时保留旧数据并局部提示，不要整块清空                                       |
| **空**            | 请求成功但结果为 0                                | `EmptyState`                | 说明"为什么空"和"下一步做什么"；没有可执行动作时不要放按钮                                                           |
| **错误**          | 服务端返回结构化失败，或网络失败                  | `ErrorState`                | 文案必须由 `shared/errors.ts` 的 `describeError()` 生成；同时展示稳定 code 与关联 ID；可重试时给"重试"               |
| **无权限**        | 服务端返回 `FORBIDDEN`                            | `ErrorState`                | **不能**当成"空"。注意服务端会把部分 `FORBIDDEN` 伪装成 `404`，所以 `NOT_FOUND` 的文案必须同时覆盖"不存在或无权查看" |
| **离线 / 不可达** | `SOURCE_UNAVAILABLE`、`MEDIA_OFFLINE`、fetch 失败 | `ErrorState` 或行内 `Badge` | 明确区分"Gallery 连不上"与"Source 离线"：后者不影响已发布 Catalog 的浏览                                             |

补充规则：

- **capability 只用于隐藏入口，永远不作为最终判断。** 即使 capability 通过，服务端仍可能
  返回 `FORBIDDEN`/`404`；界面必须优雅呈现，不得白屏或无声吞掉。
- **实时连接状态不是数据状态。** WebSocket 断开只影响"多快知道变化"，不影响已加载数据的
  有效性。用状态条弱提示，不要把整页切成错误态。
- **通知（Toast）只承担已发生事实的短反馈。** 阻塞性错误、需要用户决策的冲突、不可恢复的
  失败必须留在页面里（`ErrorState` / `Dialog`），不能只用一条会自动消失的通知表达。
  危险级通知默认不自动消失。

---

## 4. 间距与密度规则

### 间距刻度

`--space-1` … `--space-8` = 4 / 8 / 12 / 16 / 24 / 32 / 48 / 64 px（以 rem 表达，跟随用户字号）。

- **不允许**出现刻度之外的间距值，也不允许用 `margin: 13px` 这类字面量。
- 相关性越强，间距越小：控件内 `--space-2`，同组控件之间 `--space-3`，
  区块之间 `--space-5`（画廊）/`--space-4`（管理），页面分区 `--space-6`。
- 垂直节奏优先用 `display: grid` + `gap`，不要用相邻 margin 叠加。

### 密度

| token              | comfortable（画廊） | compact（管理） |
| ------------------ | ------------------- | --------------- |
| `--control-height` | 2.75rem             | 2rem            |
| `--row-height`     | 3.5rem              | 2.25rem         |

- 密度写在 `:root[data-density]` 上，由 `ThemeProvider` 负责，组件**不得**内联覆盖控件高度。
- 密度按界面分别记忆（存储键 `gallery.density.gallery` / `gallery.density.manage`），
  避免管理端的紧凑设置漏进画廊。
- **触控设备下 compact 自动抬回 44px**（`@media (pointer: coarse)`）。信息密度不能牺牲
  触控可达性。

---

## 5. 主题与对比度策略

### 四种偏好、三套主题

`system` / `light` / `dark` / `high-contrast`。`system` 解析成 light 或 dark。

层叠顺序（写在 `tokens.css` 里，不要调换）：

1. `:root` —— 浅色基线
2. `@media (prefers-color-scheme: dark) { :root:not([data-theme]) }` —— 系统偏好
3. `:root[data-theme='light' | 'dark' | 'high-contrast']` —— 用户显式选择，**覆盖**系统偏好

`system` 模式必须**移除** `data-theme` 属性而不是写成 `data-theme="system"`，否则第 2 条的
`:not([data-theme])` 永远不成立，系统深色偏好会完全失效。

主题在两端**共享**（存储键 `gallery.theme`）：同一个人在同一台设备上应当看到同一套视觉。

### 对比度

- 正文与 `--color-text-muted` 对背景 ≥ **4.5:1**（AA）。
- `--color-border` 对背景 ≥ **3:1**：输入框边框是控件边界的唯一指示，不能是几乎看不见的浅灰。
  纯装饰性分隔线可以用 `color-mix()` 减淡。
- `--color-accent-text` 对 `--color-accent` ≥ 4.5:1，保证强调按钮上的文字可读。
- 语义色（danger / warning / success）作为文字时同样 ≥ 4.5:1，因此深色主题下它们是**浅色**的，
  不要照搬浅色主题的深红深黄。
- **高对比主题**：纯黑底、纯白字与白边框，前景对背景 ≥ 7:1。此时阴影不是可靠的层级信号
  （低视力用户可能完全看不到），`--shadow-*` 退化为实边框描边。
- **颜色不是唯一信号。** 状态必须同时有文字或形状；只靠红/绿区分成功失败不可接受。

### 动效

`--motion-fast` 120ms / `--motion-base` 200ms / `--motion-slow` 320ms。

- 组件只允许用这三个 token 表达时长。
- `@media (prefers-reduced-motion: reduce)` 下三者归零，`reset.css` 另有 `!important` 兜底
  拦住第三方写死的动画；`Spinner` 停止旋转，"进行中"完全由它的无障碍文本承担。
- 逻辑动效（自动滚动、自动轮播、自动聚焦跳转）必须读 `useTheme().reducedMotion` 主动关闭，
  CSS 兜不住它们。

---

## 6. 焦点与触控目标

### 焦点

- 全局 `:focus-visible` 使用 `--focus-ring`：**底色描边 + 强调色环**的双层结构，任何主题下都能
  与背景分离。不要改成单层 outline。
- 只用 `:focus-visible`，不用 `:focus`：鼠标点击不应出现焦点环。
- **绝不 `outline: none` 而不给替代。** 任何自定义控件都必须有可见焦点态。
- 焦点顺序 = DOM 顺序。用 `transform` 把元素移出视口（例如 `SkipLink`）不会把它移出 Tab 序，
  这是有意的；真要移出可聚焦树必须用 `visibility: hidden` 或 `display: none`。
- 每个页面的**第一个**可聚焦元素是 `SkipLink`，目标是主内容区的 `id`。
- 弹出层（`Dialog` / `Menu` / `Select` / `Tooltip`）的焦点收束与返还由 react-aria-components
  负责，不要手写。

### 触控目标

- 最小触控目标 **44 × 44 px**。`--control-height` 在 comfortable 下就是 2.75rem；
  compact 在 `pointer: coarse` 下自动抬回。
- `IconButton` 宽高固定等于 `--control-height`，保证紧凑密度下不塌成小方块。
- 相邻可点区域之间至少留 `--space-2`。
- **Tooltip 在触屏上不可达**，只能承载补充信息。操作必需的说明写成可见文本或 `Field`
  的 description。

---

## 7. 文件与依赖方向

```
design/tokens.css       设计 token（唯一颜色/间距/字号/动效来源）
design/reset.css        最小 reset + 全局焦点 + 减少动效兜底
design/primitives.css   共享组件外观（只用 token，不出现字面值）
design/primitives.tsx   共享组件（交互与可访问性交给 react-aria-components）
design/index.ts         统一入口，同时装载 tokens/reset/primitives
design/README.md        本文件
```

依赖方向是单向的：

```
gallery/** 、manage/**  →  shared/**  →  design/**  →  （react-aria-components）
                        →  api/**
```

- `design/**` **不依赖** `shared/**` 或 `api/**`。因此 `ErrorState` 只接受已本地化的中文文案，
  由调用方先用 `shared/errors.ts` 的 `describeError()` 翻译。
- 页面只从 `design`（即 `design/index.ts`）导入组件，不要直接 import `primitives`，也不要
  复制 `ui-` 类名。

### 关于 react-aria-components

交互与可访问性一律交给它。**不要升级它的版本**：`internal/webapp/handler.go` 的 CSP 放行了一段
与该版本绑定的 RAC 内联样式哈希（用于 pressable 元素的 `touch-action`），升级会让移动端触控
行为被 CSP 拦掉。确需升级时先停下，由主 Agent 同步 CSP。

唯一没有使用 RAC 的是 `Toast`：RAC 1.19 只提供 `UNSTABLE_Toast*`，前缀本身就是"随时可能变形"
的声明，不适合作为多条并行工作线共同依赖的契约。这里改用语义正确的 aria live region
（危险级 `role="alert"`，其余 `role="status"`），关闭按钮仍是 RAC `Button`。
