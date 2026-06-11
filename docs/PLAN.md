# ai-fox 优化与重构规划

> 基于 2026-06 对全量代码（约 1.8 万行 Go + TS）的三路深度审查：Go 侧逐文件审查、渲染层交互与架构审查、LLM API 协议兼容性差距分析。所有结论均有 `file:line` 依据，高危项已人工复核。本文档只做规划，不含实现。

> **实施状态（2026-06-11）**：M1–M5 全部阶段已在 `claude/ai-observability-planning-tb970o` 分支落地（PR #2）。文中行号为审查时点快照，重构后已漂移，仅作问题定位的历史依据。两项已知偏差：§3.3 密钥加密在无 Secret Service/Keychain/DPAPI 的环境降级为明文 + stderr 警告（设计如此，发布前需确认目标平台凭据库可用）；pricing 表对价格不确定的模型有意留空（宁缺勿错）。

---

## 0. 项目目标与现状定位

**目标**：高性能 AI/LLM 可观测与调试桌面工具。Go 侧反向代理接管 Claude Code / Codex / opencode 等客户端到上游 provider 的全部流量，做落盘、实时解析、断点、重放与可视化。

**现状**（与 CLAUDE.md 的描述已严重脱节，见 §7 文档修正）：

- 反向代理、断点、重放、会话聚合、ring-buffer 存储、Anthropic Messages 全量解析（流式 + 非流式）均已落地。
- OpenAI chat completions 仅有指纹级骨架解析；Codex 的 Responses API 完全不识别；Gemini 只有常量没有实现。
- 渲染层约 30 个模块，版本切片 + region 重渲架构成型，但存在状态丢失、无重连、无虚拟化等结构性问题。

**总体判断**：骨架健康（边界清晰、契约链路完整、并发基础经过测试），但有 **2 个破坏转发正确性的高危 bug**、**3 个让工具在真实场景"假死/丢数据"的前端 P0**、以及 **Codex/OpenAI 兼容性完全缺位**。以下按"先正确、再兼容、后体验、最后性能打磨"排序。

---

## 1. P0：正确性 Bug（必须最先修，全部需要回归测试）

### 1.1 Go 侧

| # | 问题 | 位置 | 说明 |
|---|------|------|------|
| G1 | **>1MiB 请求体被静默截断后仍转发上游** | `internal/proxy/proxy.go:137,170,180` | `readBody` 把请求体截断到 `MaxBodyBytes`(1MiB) 用于捕获，但同一份截断 buffer 被 `bytes.NewReader` 重建为上游请求体，而原始 `Content-Length` 头经 `copyHeaders` 原样复制。长上下文 + base64 图片极易超 1MiB → 上游收到声明长度 > 实际长度，请求 hang 或失败。**修法**：转发与捕获解耦——上游用 `io.TeeReader` 流式透传完整 body，捕获侧另存截断副本。 |
| G2 | **响应 `Content-Encoding`/`Content-Length` 转发不一致** | `internal/proxy/proxy.go:187,209,417` | 已设 `Accept-Encoding: identity`，但上游（尤其 CDN/网关）可能仍返回 gzip；此时 gzip 字节进入捕获（详情页是乱码），且 `Content-Encoding`/`Content-Length` 原样转给客户端。**修法**：检测响应 `Content-Encoding`，非 identity 时在代理层解码（gzip/br/zstd），并删除 `Content-Encoding` + `Content-Length`（改 chunked）；`hopByHopHeaders` 按 RFC 7230 §6.1 同时剥离 `Connection` 头中列举的字段。 |
| G3 | **SSE 拆帧不支持 CRLF** | `internal/llmparse/anthropic_sse.go:245` | `splitSSE` 只按 `\n\n`/`\n` 切分；任何中间层改写为 `\r\n` 后所有事件名带尾随 `\r`，整条流解析失效（全部进 warning）。**修法**：拆帧前规范化行结尾或对每行 `TrimRight "\r"`，补 CRLF/半事件/空 data 行测试。 |
| G4 | **store `persisted` map 只增不减** | `internal/store/store.go:115,162` | entry 被 ring buffer 淘汰后 `idIndex` 已清理但 `persisted` 永久保留 ID。常驻桌面进程无界增长。**修法**：evict 时同步清理；同时给 session 包的 `byEntry`/`bucket`/`last`（`internal/session/session.go:79-84`）加与 store 容量对齐的淘汰。 |
| G5 | **断点 Hold 期间客户端断开被误标为 "aborted at breakpoint"** | `internal/proxy/proxy.go:157-164`, `internal/proxy/breakpoint.go:326` | `Hold` 在 `ctx.Done()` 时也返回 `DecisionAbort`，与用户主动 abort 语义混淆，UI 状态错误。**修法**：区分 `DecisionAbort` 与 `DecisionClientGone` 两种返回值。 |

### 1.2 渲染层

| # | 问题 | 位置 | 说明 |
|---|------|------|------|
| F1 | **SSE 断线无重连、无提示** | `electron/src/renderer/ui/sse.ts:41-72`, `renderer.ts:71` | `openSse` 注释明言"重连是调用方的责任"，但调用方从未实现；错误全部吞掉。后端重启 / 系统休眠唤醒后 UI 静默假死。**修法**：在 `renderer.ts` 包一层指数退避重连（重连成功靠服务端 snapshot 事件自愈），断线期间 statusbar 显示"已断开"指示。 |
| F2 | **settings 草稿被任意 `ui` 版本重渲清空** | `electron/src/renderer/ui/settings.ts:58-60` | `renderSettings` 每次重渲都 `toDraft(getState().settings)` 重建 draft；用户正输入 API key 时任何 SSE 事件触发 `ui` tick 即丢失全部未保存输入。**修法**：draft 提升为模块级（参照 `breakpoints.ts:20` 的做法），或 settings 页改为独立挂载、不随 `ui` 版本重建。 |
| F3 | **侧边栏无法跟踪实时流量** | `electron/src/renderer/ui/app.ts:44-52,271` | `STICKY_BOTTOM` 不含 `.side-list` 与 `.tl-body`；新条目插入头部 + `restoreScrolls` 强制恢复旧位置，高频流量下用户找不到最新请求。**修法**：实现 auto-scroll pin（列表顶部插入模式下"pin to top"，用户手动滚走后解除，提供回到最新按钮）。 |
| F4 | **`select.ts` document 级监听器泄漏** | `electron/src/renderer/ui/select.ts:93-98` | 菜单打开期间所在 region 被 `replaceWith` 重建 → close 永不调用，每次泄漏一对 `mousedown`/`keydown` listener。**修法**：用 `AbortController` 绑定 listener，并在 region 重建钩子里统一 abort。 |
| F5 | **前端 `entries` 无上限增长且与后端淘汰不同步** | `electron/src/renderer/ui/state.ts:378` | SSE `entry` 事件只 upsert 不删除，前端数组超过后端 ring buffer 仍保留；`replaceEntries` 后 `selectedId` 可能指向已淘汰条目。**修法**：服务端 snapshot/reconcile 事件携带存活 ID 集，前端按之裁剪；`replaceEntries` 后校验 `selectedId`。 |

---

## 2. API 兼容性路线图（核心业务方向）

### 2.1 现状矩阵

| 端点 | 识别 | 流式解析 | 非流式解析 |
|---|---|---|---|
| Anthropic `POST /v1/messages` | ✅ | ✅ 7 类事件全覆盖 | ✅ |
| Anthropic `POST /v1/messages/count_tokens` | ❌（被 `anthropic.go:46` 后缀匹配排除） | N/A | ❌ |
| OpenAI `POST /v1/chat/completions` | ✅（仅识别） | ❌ | ❌（仅指纹骨架） |
| OpenAI `POST /v1/responses`（Codex） | ❌ | ❌ | ❌ |
| OpenAI `POST /v1/completions`（legacy） | ❌ | ❌ | ❌ |
| Gemini | 仅 `ProviderGemini` 常量（`normalize.go:15`） | ❌ | ❌ |

未识别端点均透明转发（不报错），但 `Analysis` 为 nil，UI 退化为原始 JSON。

### 2.2 阶段 A：Claude Code 完整兼容（工作量小、收益直接）

1. **`knownAnthropicRequestKeys` 补 `thinking`、`betas`、`service_tier`**（`internal/llmparse/anthropic.go:89-102`）——当前每个 Claude Code 请求都误报 "unknown top-level fields" 警告；`thinking` 配置应结构化展示（type/budget_tokens）。
2. **停止覆写 `anthropic-version`**（`internal/proxy/proxy.go:479`）：客户端已带版本头时透传，仅在缺失时注入默认值；`anthropic-beta` 头透传并在 entry 详情中展示。
3. **识别并解析 `count_tokens`**：请求结构与 messages 一致，响应只有 `input_tokens`；归类为 utility 请求，不计入会话 turn。
4. `image` 块结构化（media_type / URL / base64 大小，UI 不内联渲染原图，仅显示元信息 + 可展开）；`cache_control` 解析为具体类型（ephemeral / TTL）。

### 2.3 阶段 B：OpenAI Chat Completions 完整解析（旧 API 兼容）

1. 新建 `Analysis.OpenAI *OpenAIAnalysis` 字段（类比 `Anthropic`），`HasStructured`（`internal/api/api.go:422`）改为多 provider 判定。
2. 非流式响应解析：`choices[].message`（content / `tool_calls` 新格式 / legacy `function_call` 对象）、`usage.prompt_tokens/completion_tokens`（含 `prompt_tokens_details.cached_tokens`）、`finish_reason`、错误体。
3. 流式 SSE 解析器：`choices[].delta` 增量合并（content、tool_calls 按 index 聚合 arguments 分片）、`data: [DONE]` 终止符、`stream_options.include_usage` 的末尾 usage chunk。
4. `POST /v1/completions`（text completion）做最小解析：prompt / choices[].text / usage，满足"旧 API 兼容"要求即可，不做会话聚合优化。
5. fingerprint 路径补 legacy `function_call`（`internal/llmparse/openai.go:100-104`）。

### 2.4 阶段 C：OpenAI Responses API（Codex CLI）

1. 新增 `isOpenAIResponses` 路由识别（`/v1/responses`）——`PresetOpenAIResponses`（`internal/config/config.go:44`）配置已存在但无路由对应。
2. 独立 SSE 解析器：事件族与 chat completions 完全不同（`response.created` / `response.output_item.added` / `response.output_text.delta` / `response.function_call_arguments.delta` / `response.completed` 等），输出结构是 `output[]` 而非 `choices[]`。
3. 会话关联：Responses API 用 `previous_response_id` 串联有状态对话——这是比指纹更可靠的关联信号，session 包应优先采用（新增关联策略，与现有 prefix-match 并存）。

### 2.5 统一归一化模型（支撑以上全部）

当前 `NormalizedRequest`（`internal/llmparse/normalize.go:26-33`）只服务会话指纹，缺：

- `NormalizedUsage`：跨 provider token 用量（input / output / cache_read / cache_write），`computeRollup`（`internal/session/session.go:555-629`）从直接读 `ana.Anthropic.Response.Usage` 改为读统一接口——**这是 OpenAI 会话 token 恒为 0 的根因**。
- `NormalizedToolCall` / stop_reason 映射（`end_turn|tool_use|max_tokens` ↔ `stop|tool_calls|length`）、统一错误结构、`stream` 标志。
- 成本计算需要 model→价格表（内置 + 可配置覆盖），落在 Go 侧新包（如 `internal/pricing`），UI 不做价格逻辑。

### 2.6 明确不做

- **协议翻译**（OpenAI 格式请求转 Anthropic 上游等）：与"透明网关"定位冲突，当前无雏形，列为远期可选，不进本轮规划。

---

## 3. Go 侧重构与性能规划

### 3.1 重构（修 P0 时顺路完成，避免二次返工）

1. **拆分 `ServeHTTP`**（`internal/proxy/proxy.go:105-221`，约 115 行）：URL 构造 / 捕获 / 断点 / 转发 / 流式五段拆为私有方法；G1/G2 的修复天然要求这次拆分。
2. **合并重复实现**：`captureAndStream`（`proxy.go:281`）与 `captureToStore`（`replay.go:213`）抽成带可选 `io.Writer` 的单一函数；`injectAuth`/`isAuthHeader`/`hopByHopHeaders` 统一为一处"构造转发 header"函数，proxy 与 replay 共用。
3. **删死代码**：`Proxy.Serve/Close` 与 `mu/closed` 字段（`proxy.go:39-98`）在 controller 模式下未使用，生命周期统一交给 Controller。
4. **常量去重**：`flushInterval`/`reconcileInterval` 在 `session/session.go:69` 与 `api/api.go:614` 重复定义。
5. **解析器瘦身**：`parseAnthropicRequest` 手工 `raw["x"].(float64)` 逐字段提取（`anthropic.go:115-159`）改为 `json.RawMessage` + struct tag + 单独 unknown-fields 检测，为 OpenAI/Responses 解析器树立模板。
6. **provider usage 抽象**：`computeRollup` 解耦 Anthropic 具体结构（见 §2.5）。

### 3.2 性能

1. **消除 `captured.String()` 每 chunk 全量拷贝**（`proxy.go:331`）：每 32KiB chunk 对已捕获 body 做 O(n) 拷贝，长流累计 O(n²/chunk)——流式热路径最大放大点。只在广播 flush tick 取值。
2. **store 增加字节预算淘汰**：当前按条数（500）限容，满载大 body 最坏 ~1GB 常驻；按字节预算（如默认 256MiB，可配置）双重淘汰。
3. **SSE writer 增量化**：`writeTrafficStream` 每个 session 更新全量 `agg.List()` + marshal（`internal/api/api.go:771-774`）；`dirty` 中已 evict entry 的清理（`api.go:737-742`）。
4. **`tailTrafficHandler` 按字节切 UTF-8 不安全**（`api.go:535`）：tail offset 落在多字节字符中间会发半个 rune，改为对齐到 rune 边界或文档化为字节流语义。
5. 低优先：`authMiddleware` 用 `crypto/subtle.ConstantTimeCompare`（`internal/server/server.go:169`）；`buildUpstreamURL` 保留 baseURL 自带 query（`proxy.go:388`）。

### 3.3 安全（release blocker，单列）

- **API key 明文落盘**（`internal/config/config.go:5-8` TODO 已承认）：接 OS 凭据库（macOS Keychain / Windows DPAPI / Linux libsecret），Electron 侧可经 `safeStorage` 走 IPC，但按架构铁律加解密逻辑应留在 Go（评估 go-keyring 类库）。落地前与用户对齐存储方案。

---

## 4. UI / 交互优化规划

### 4.1 修完 F1–F5 后的体验优先级（按影响排序）

1. **断点暂停全局可见**：statusbar 常驻 `⏸ paused (n)` 指示 + 顶层提示，bottom pane 折叠时也能看到（当前 `statusbar.ts:7` 无此信息，请求被 pause 可能卡住 agent 数分钟而用户不知）；Continue/Abort 按钮加 pending 态防重复提交（`breakpoints.ts:157-175`）。
2. **布局持久化**：`colLeft/colRight/bottomHeight`（`col-resize.ts:30`, `state.ts:81-84`）经 `/v1/settings` 持久化（业务状态走 Go，符合架构铁律）；resize handle 双击复位。
3. **侧边栏虚拟化**：`sidebar.ts:208` 直接 map 全量 entries，长会话数百条即掉帧；先实现固定行高窗口化（无需引库），同步处理 F5 的裁剪。
4. **搜索体验统一**：filter 文本纳入 `model` 字段（`grouping.ts:19`）；输入加 150ms 防抖（`sidebar.ts:35`）；补搜索图标与清空按钮；侧边栏 filter 与 center filter bar 两处同字段 UI 二选一或视觉上明确关联。
5. **Replay diff 视图**：replay 的核心价值是对比，当前只跳转新 entry；参照 `LLMFox/details.jsx:269-287` 实现原始 vs 重放的按行 diff tab。
6. **键盘导航**：侧边栏列表上下键移动选中（`sidebar.ts:293` 补 role/tabindex/aria-selected）、tab 组 ARIA tablist + 箭头导航（`detail.ts:134`, `bottom-pane.ts:92`）、断点 pattern 输入 Enter 提交（`breakpoints.ts:56`）、自定义 select 补 Enter/方向键（`select.ts:105`）。
7. **错误不再静默**：统一 toast/banner 通道；`fetchEntry`（`selection.ts:127`）、rename（`sidebar.ts:245`）、settings 刷新失败需有提示；settings Save 按钮 pending + disabled（`settings.ts:194`）。
8. **选中联动修正**：entry-bar chip 切换后 `scrollIntoView` 对应 card（`timeline.ts:237`）；`selectMessage` 重置 detail-body scrollTop（`state.ts:241`）。
9. **主题闪白**：`index.html` 内联脚本在首帧前从 localStorage 镜像读取主题设 `data-theme`（持久化仍走 Go settings，localStorage 仅作首帧镜像缓存）。
10. **断点添加乐观更新**：`breakpoints.ts:67-82` 当前依赖 SSE 回推刷新且 `setState({})` 是无效空 patch（不触发任何 slice），先本地插入再以服务端事件校正。

### 4.2 从 LLMFox 参考稿吸收（重设计，不照抄）

值得借鉴：

- **Timeline rail 布局**（`LLMFox/styles.css:292-360`）：时间戳 + 轨道线 + 节点 + card 的三列结构，信息密度与"调试器感"显著优于当前卡片堆叠（`timeline.ts`）。
- **Per-turn token/cost footer + cache ratio bar**（`LLMFox/timeline.jsx:66-78`）：每张 card 底部内联 cache hit/new/out pill 与 4px 缓存比例条——"哪个 turn 最贵"从逐个点击变成一次扫视。依赖 §2.5 的 pricing 包。
- **Gutter 点断点**（`LLMFox/timeline.jsx:22-27`）：在 rail 上点一下设断点，替代现在"打开 bottom pane → Breakpoints tab → 手填 pattern"的三层操作。
- **Statusbar 信息密度**（`LLMFox/app.jsx:232-250`）：paused/running 状态、累计成本、cache 命中率。
- **Session tabs**：多 session 快速切换；以及 SSE 流回放控件（`details.jsx:253-261`）列为远期。

明确不抄：React useState 顶层全量状态、`window.LLMFOX_DATA` mock 架构、TweaksPanel 调参面板、超短 CSS 类名。

### 4.3 前端架构（支撑功能增长）

1. **拆细 `ui` 版本桶**（`state.ts:187`）：`settings/proxy/bottomTab/filters` 全映射到 `ui`，一次 proxy 状态变化重建 topbar/filter bar/settings 全部 region——拆出 `proxy`、`filters` 独立 slice。这是 F2 的结构性根因。
2. **统一 API service 层**：组件直接 `getClient()` 散调（`breakpoints.ts:69` 等），抽一层带错误通道的 service，集中处理 toast 与 pending。
3. **region 销毁钩子**：`replaceWith` 重建无清理回调是 F4 类泄漏的根因，给 region 框架加 dispose 协议（配合 AbortController）。
4. **`renderStatusbar` 的 O(n) reduce**（`statusbar.ts:17-23`）每 tick 全量遍历 entries，改为增量聚合或由 Go 端下发聚合值（后者更符合"业务逻辑在 Go"）。
5. **json-tree 懒渲染**（`json-tree.ts:34`）：大 JSON 整树建 DOM，引入按需展开/IntersectionObserver 懒构建。

---

## 5. 测试补全计划（与修复同 PR 落地）

Go 侧（按优先级）：

1. 转发正确性：>1MiB 请求体完整转发、gzip/br/zstd 上游响应、`Content-Length` 一致性、`Connection` 头动态 hop-by-hop（修 G1/G2 时同步）。
2. 断点：`Hold` ctx 取消、`Delete`/`Clear` 释放 held、decide 与 ctx 分支竞态、held 期间二次匹配放行——目前 `breakpoint.go` 完全无测试。
3. `replay.go` 整文件无测试：override 应用、header 过滤、非法 JSON/空 body 分支。
4. SSE 边界：CRLF、半事件、空 data 行（修 G3 时同步）。
5. store evict 后 `persisted`/`idIndex`/session map 清理（修 G4 时同步）。
6. 新解析器（OpenAI chat 流式/非流式、Responses API）各配 golden-file 测试，含 legacy `function_call`。
7. `config.Store` 并发 race 测试。

前端：渲染层目前零测试。优先给纯逻辑模块（`grouping.ts`、`format.ts`、`sse.ts` 的 parseEvent、state 版本切片）补 Vitest 单测；UI 交互暂以 `task verify` 的 typecheck 兜底，不强行上 E2E。

---

## 6. 实施阶段划分

| 阶段 | 内容 | 验收 |
|---|---|---|
| **M1 正确性** | G1–G5、F1–F5、§3.1 重构第 1–4 项（顺路）、对应回归测试 | `task verify` 过；1MiB+ 请求/gzip 上游/后端重启三个场景手工验证 |
| **M2 兼容性·Claude Code + OpenAI** | §2.2 阶段 A、§2.3 阶段 B、§2.5 归一化模型 + pricing 包 | Claude Code 真实会话零误报 warning；opencode 走 OpenAI 上游时 token/会话统计正确 |
| **M3 兼容性·Codex** | §2.4 Responses API 识别 + SSE 解析 + `previous_response_id` 会话关联 | Codex CLI 真实会话在 timeline 中结构化展示 |
| **M4 体验** | §4.1 第 1–8 项、§4.2 timeline rail + per-turn footer + gutter 断点、§4.3 架构项 | 长会话（500+ entries）侧边栏流畅；断点状态全局可见 |
| **M5 打磨** | §3.2 性能项、§3.3 密钥加密、§4.1 第 9–10 项、虚拟化深化 | 常驻 24h 内存平稳；密钥不再明文 |

依赖关系：M2 的归一化模型是 M3 与 M4 中 token/cost UI 的前置；M1 的 region dispose 协议是 M4 多数交互项的前置。每阶段独立可发布。

---

## 7. 文档修正（随 M1 提交）

- **CLAUDE.md 已严重过时**：仍称 `internal/api/api.go` 只有 `/health` 与 `/greet` 两个 demo 端点，实际已有 proxy/session/store/breakpoints/replay 全套。按 AGENTS.md 的维护规则原位替换"业务方向上待落地的结构"一节为现状导航。
- README "状态"一节同步（Anthropic 解析已完整、断点/重放已可用）。
- `normalize.go:70` 注释提到的 `gemini.go` 不存在——注释与现实对齐，或在 M3 后补 Gemini 时一并处理。
