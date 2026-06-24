# AGENTS.md

## 项目布局

整个项目就在仓库根 `AiFox/`，无子目录嵌套。所有命令、构建、测试都在仓库根（或其 `web/`）里执行。

```
AiFox/
├── go.mod  go.sum            module github.com/zerx-lab/ai-fox（huma + keyring + energy + golcl）
├── main.go                   Energy(CEF) 入口：进程内同时跑 Huma server + CEF 窗口 + IPC；保留 openapi 子命令
├── internal/                 全部 Go 业务逻辑（api/server/proxy/llmparse/store/config/session/pricing）
├── web/                      渲染进程（TypeScript）
│   ├── src/api/{client.ts,schema.ts}
│   ├── src/renderer/         UI（renderer.ts/index.html/styles.css/ui/i18n/public）
│   ├── src/bridge/energy-bridge.ts   window.aiFox 适配层（IPC ← → Energy 全局 ipc）
│   ├── package.json  vite.config.ts  tsconfig.json  biome.json  vitest.config.ts
│   └── pnpm-lock.yaml
├── resources/                Go embed 目录
│   ├── app/                  vite build 产物（index.html + assets/），由 assetserve 提供
│   └── icon.ico  icon.png
├── config/energy_*.json      energy package 读取的平台打包配置（darwin/linux/windows）
├── docs/PLAN.md              整体规划
├── LLMFox/                   UI 设计参考稿（vanilla JSX + CSS，非运行代码）
├── Taskfile.yml              唯一编排入口
└── .golangci.yml  openapi.yaml(生成)
```

## 架构铁律

单桌面应用，单进程。Go 进程内同时跑 Huma HTTP server（业务逻辑）和 CEF 窗口（Energy v2）。渲染进程是带类型的 HTTP 客户端。

```
Go struct → openapi.yaml → schema.ts → client.ts → renderer
```

- 业务逻辑**只**写在 Go。`main.go` 仅负责进程外壳：起 Huma server、起 assetserve、建 CEF 窗口、注册 IPC。
- TS 的请求/响应类型**只能**来自 OpenAPI 导出。`web/src/api/schema.ts` 自动生成、禁止手改。
- 端点必须通过 Huma 的 operation 注册（`huma.Register`）。裸 `net/http` handler 不会进 OpenAPI，对 TS 不可见。
- 桌面外壳用 Energy(CEF)，不要并行引入 Electron/Forge。

## 验收标准（提交/收尾前必过）

`task verify` 是硬门槛，聚合：

- `task typecheck` — `web/` 下 `tsc --noEmit`，含生成的 `schema.ts`。
- `task test:go` — `go test ./...`（另有 `test:go:race` 对 store/session/server 跑 -race）。
- `task test:ui` — Vitest，只测渲染进程纯逻辑模块（`web/src/renderer/**/*.test.ts`，node 环境，无 DOM）；UI 交互仍由 typecheck 兜底。
- `task lint` — `lint:go`（golangci-lint）+ `lint:ts`（Biome，`schema.ts` 已排除）。

不要为了过 lint 顺手"清理"无关代码。新加的 lint 规则要先在 `.golangci.yml` / `web/biome.json` 里登记，再写实现。

## 进程模型与窗口外壳

- `cef.GlobalInit` + `cef.NewApplication` 后，在 `cef.SetBrowserProcessStartAfterCallback` 内 `go` 起两个 server：assetserve（固定端口 22022，服务 embed 的 `resources/app`）+ Huma（OS 分配端口，loopback）。Huma 就绪后填充包级 `hs` 并 `close(ready)`。
- **自定义标题栏**：`cef.BrowserWindow.Config.EnableHideCaption = true` 去原生标题栏；渲染进程 `ui/titlebar.ts` 自绘，按钮经 IPC 调窗口方法。拖拽靠 CSS `-webkit-app-region: drag/no-drag`（CEF 原生支持）。
- **窗口操作必须在主 UI 线程**：IPC handler 里用 `cef.QueueAsyncCall(func(id int){...})` 包裹 `window.Minimize/Maximize/Restore/CloseBrowserWindow`，否则跨线程崩溃。最大化是 toggle：`window.WindowState() == lcltypes.WsMaximized` 时 `Restore()`，否则 `Maximize()`，并 `ipc.Emit("window:maximized-changed", ...)` 回传新态。
- `cef.Run(app)` 阻塞主线程直到窗口关闭；进程退出由 OS 回收后台 goroutine，无需显式关 Huma。

## IPC 桥接

Energy 自动向页面注入全局 `ipc` 对象。约定**单向**模式（不依赖 JS 端 callback 取返回值）：

- **Go → JS 数据**用 `ipc.Emit(name, payload)`。在 `event.SetOnLoadEnd` 内每次页面加载后下发：`app:env`（Node 风格 platform）、`app:handshake`（等 `<-ready` 后发，字段 `port/token/baseUrl/proxyPort/proxyBaseUrl/proxyEnabled`，匹配 `client.ts` 的 `Handshake`）、`window:maximized-changed`。
- **JS → Go 命令**用 `ipc.emit(name)`，Go 侧 `ipc.On(name, fn)` 处理：`window:minimize`/`window:maximize-toggle`/`window:close`。
- 渲染进程零改动的关键是 `web/src/bridge/energy-bridge.ts`：用全局 `ipc` 实现 `window.aiFox`（契约见 `client.ts` 的 `AiFoxBridge`），`renderer.ts` 第一行 `import "../bridge/energy-bridge"` 保证 bootstrap 前就绪。时序上 module script（defer）在 `OnLoadEnd` 前执行，故 `ipc.on` 注册先于 Go 的 `Emit`；bridge 用 awaiter 缓存避免错过事件。
- OS 主题不走 IPC：CEF/Chromium 遵循系统 `prefers-color-scheme`，bridge 的 `theme.native`/`onNativeChanged` 直接用渲染进程 `matchMedia`。

## 流式 API 边界

- **SSE** 经 OpenAPI 暴露。Huma 注册 `text/event-stream` 响应；事件 payload 注册为命名 schema（`OneOf` 判别式联合），TS 端薄解析器把流文本切成判别式联合——`openapi-typescript` 只给 `string`。渲染进程 `ui/sse.ts` 用 `fetch + ReadableStream` 手动带 `X-Ai-fox-Token` 解析，不依赖 EventSource，CEF 下原样工作。
- **WebSocket** 不进 OpenAPI。如必须用 WS，帧类型是**唯一**允许手写 TS 接口的例外（Go 侧用同一份 struct 序列化，注释互引文件路径，两侧同提交）。能用 SSE 就别用 WS。

## 代码生成链路

改 Go API → `task openapi`（`go run . openapi openapi.yaml`）→ `task codegen`（`cd web && pnpm codegen`，由 openapi.yaml 重生 `schema.ts`）→ TS 拿到新类型。`task dev` / `task build` 已串好这条链；单独跑中间步会有静默类型漂移。

所有编排走 `Taskfile.yml`，不要在文档/脚本里直接调 `pnpm` 或 `go`。命令清单用 `task --list` 查。

## 构建与运行时依赖

- **一次性前置**：`go install github.com/energye/designer/cmd/energy@latest` 装 CLI（CLI 已迁移到 designer 仓库，旧的 `energye/energy/cmd/energy` 因 v2 模块路径冲突已不可用）；在仓库根执行 `energy install` 下载 CEF 二进制框架到 `~/.energy`（开发/运行/CI **必需**，否则起不来）。CI 须缓存 `~/.energy`。
- 日常编译：`task build:go` → `go build`（Windows 隐藏控制台 `-ldflags "-H windowsgui -s -w"`，非 Win 去掉 `-H windowsgui`）。`build:go` 依赖 `resources/app` 已经由 `task build:web` 产出（Go embed 之）。`task build` 串好 `build:web` → `build:go`。
- 分发：`task package` → `energy package`，读 `config/energy_*.json`（已把 name/productName/icon 改为 ai-fox）。

## dev 模式（无热重启）

Energy 不支持 Go 代码热替换。`task dev` 先 `build:web` 再 `go run .`。改 TS 重跑 `task build:web`，改 Go 重启 `go run .`。没有 Electron 那套 sidecar 热重启（dev-watcher / .dev-reload）。

## 安全模型

- Huma server 只绑 `127.0.0.1`，OS 分配端口。
- 每次启动生成新十六进制 token，要求 `X-Ai-fox-Token` 头（常量 `server.AuthHeader`）。token 经 `app:handshake` IPC 下发给渲染进程——不走 env / URL / 磁盘。
- 渲染进程从 assetserve（`127.0.0.1:22022`）加载、fetch Huma（`127.0.0.1:<api-port>`），跨端口同主机。CSP `connect-src 'self' http://127.0.0.1:*` 已覆盖；`internal/server` 的 `corsMiddleware` 在鉴权中间件**之前**反射 origin 并短路 `OPTIONS`，必要且因 loopback-only 监听而安全。`style-src 'unsafe-inline'` 因 `index.html` 用了内联占位样式。

## 关键路径

- `main.go` — Energy 入口。窗口配置（EnableHideCaption）、assetserve + Huma 双 server、IPC 桥接、`browserInit`、`serve`（原 Electron sidecar 的 runServer，去掉 stdout 握手）、`dumpOpenAPI`。
- `internal/api/` — 新增端点写这里。
- `internal/server/server.go` — `Build(Config{Port:0,Token:""})` → listener + Huma API + auth/CORS 中间件；`AuthHeader`/`LoopbackHost` 常量。
- `web/src/bridge/energy-bridge.ts` — `window.aiFox` 适配层。
- `web/src/api/client.ts` — `openapi-fetch` 封装，注入鉴权头；`AiFoxBridge` 是 bridge 契约来源。
- `web/vite.config.ts` — `root=src/renderer`、`base="./"`、`outDir=../resources/app`。
- `openapi.yaml` — Go ↔ TS 的契约检查点。

## 依赖策略

不要手改版本号。Go 用 `go get pkg@latest` + `go mod tidy`；TS 用 `pnpm add pkg`。提交生成的 `go.sum` / `pnpm-lock.yaml`。

## 代码约定

- Go 遵循 `gofmt` 与标准布局。lint 集合在 `.golangci.yml`（v2 schema）。
- 渲染进程用 TypeScript，不用 JavaScript——整条流水线的目的就是端到端类型。Biome 配置在 `web/biome.json`，`schema.ts` 已排除。
- 代码注释与标识符用英文；面向用户的字符串可用中文。

## 维护本文件

跨 agent 会话的持久化记忆。**何时更新**：用户要求记录；新增/删除/重命名顶层目录、`task` 项、Go/TS 包边界；改动依赖策略、安全模型、codegen 链路、进程模型；引入/淘汰工具；发现非显然的坑。

**怎么更新**：原位编辑，不追加 changelog；规则冲突时**替换**旧条目而非并列；删过期内容与加新内容同次提交。**不要写进来**：会话临时笔记、`task --list` 一条命令可得的信息。
