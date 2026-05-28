# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目目标

ai-fox 是一个面向 AI/LLM 的调试与流量可视化桌面工具：

- **核心定位**：把 AI 应用调用栈的 HTTP 流量、prompt、MCP 调用、tool call 等环节做成可观测、可调试、可视化的细节。
- **网关模式**：Go 侧实现反向代理。用户在 ai-fox 中配置上游 AI provider 的 `baseURL` / `apiKey`，把工具（Claude Code、Codex 等）的端点指向 ai-fox，由 ai-fox 接管全部流量并落盘/转发/打点。
- **职责切分**：Electron 只负责 UI；**所有业务逻辑（代理、解析、存储、SSE 拆帧等）一律写在 Go 侧**，通过 OpenAPI 暴露给渲染进程。
- **UI 参考**：`LLMFox/` 是设计参考稿（vanilla JSX + CSS，非运行代码）。借鉴其视觉元素与信息密度，但布局、配色、功能边界需结合实际重新设计——**不要照抄设计稿的功能清单**。

## 与 AGENTS.md 的关系

`AGENTS.md` 是这个项目的权威规则文档，包含架构铁律、流式 API 边界、热重启机制、安全模型、打包与发行细节等。**遇到任何"该不该这么做"的判断，先读 AGENTS.md**。本文件只补充 Claude Code 视角的导航与高层结构，不重复 AGENTS.md 已有的细节。

读了 AGENTS.md 还有疑问，再回到代码本身——`Taskfile.yml`、`forge.config.ts`、`internal/server/server.go` 是其它三个最高密度的信息源。

## 常用命令

所有命令走 `Taskfile.yml`，**不要**直接调 `pnpm` / `go`（详见 AGENTS.md "代码生成链路"）。

| 命令 | 用途 |
| --- | --- |
| `task dev` | 启动 Electron + Go sidecar + Go 文件 watcher（热重启） |
| `task verify` | 提交前硬门槛：`typecheck + test:go + lint`（Go + TS）|
| `task openapi` | 从 Go 导出 `openapi.yaml` |
| `task codegen` | 由 `openapi.yaml` 重生成 `electron/src/api/schema.ts` |
| `task build:go` | 编译 Go sidecar 到 `bin/<os>-<node-arch>/` |
| `task build` | 打 Electron 可分发产物（`out/`）|
| `task make` | 各平台 installer（AppImage / Squirrel / ZIP）|
| `task build:arch` | Arch Linux pacman 包（独立路径，见 AGENTS.md "发行包格式"）|
| `task clean` | 清掉 `bin/ .vite/ out/ openapi.yaml schema.ts .dev-reload` |

跑单个 Go 测试：`go test ./internal/<pkg> -run TestName -v`。Go 测试不会被 `task verify` 之外的链路自动触发。

调试启动延迟：`RELAY_BOOT_TRACE=1 task dev`，stderr 会打出 `[trace:main]` 时间戳（见 AGENTS.md "启动性能"）。

## 高层架构

```
Go struct ──► openapi.yaml ──► schema.ts ──► client.ts ──► renderer
   ▲                                                            │
   └──────────────── HTTP（127.0.0.1，X-Ai-fox-Token）──────────┘
```

- `main.go` — 两种模式：默认起 HTTP server 并把 `{port, token, baseUrl}` 作为单行 JSON 打到 stdout；`openapi <path>` 子命令只 dump schema 后退出。
- `internal/server/` — `Build(Config)` 构造 listener + Huma API + auth/CORS 中间件。监听**仅** 127.0.0.1；每次启动随机生成十六进制 token，要求 `X-Ai-fox-Token` 头。
- `internal/api/` — 所有 HTTP operation 的注册点。**新端点必须经 `huma.Register` 注册才能进 OpenAPI**；裸 `net/http` handler 对 TS 不可见。
- `electron/src/main/backend.ts` — 用 `spawn` 启 Go sidecar，读 stdout 第一行 JSON 拿握手信息。
- `electron/src/main/main.ts` — 在 `app.whenReady()` 之前就 spawn sidecar 并注册 `ipcMain.handle("ai-fox:handshake", …)`，与 Electron init 并行；dev 模式 watch `.dev-reload` 实现 sidecar 热重启。
- `electron/src/preload/preload.ts` — 用 `contextBridge` 把 `window.ai-fox.handshake()` 暴露给渲染进程。这是 token 唯一的传递通道（不走 env / URL / 磁盘）。
- `electron/src/api/client.ts` — `openapi-fetch` 的薄封装，注入鉴权头；类型**只**来自生成的 `schema.ts`。
- `electron/src/api/schema.ts` — `task codegen` 生成，**禁手改**，Biome 已排除。
- `scripts/dev-watcher.mjs` — `*.go`/`go.mod`/`go.sum` 防抖 300ms → `openapi → codegen → build:go` → 触摸 `.dev-reload`。

### 改 API 的固定路径

1. 在 `internal/api/` 加请求/响应 struct + `huma.Register(api, op, handler)`。
2. 跑 `task openapi`（或直接 `task codegen`，它依赖 openapi）让 TS 拿到新类型。
3. 在 `electron/src/renderer/` 用 `getClient()` 拿到的客户端发请求。
4. `task verify` 通过再提交。

**不要**手写 TS 请求/响应类型——唯一例外是 WebSocket 帧（详见 AGENTS.md "流式 API 边界"）。能用 SSE 就别用 WS。

## 业务方向上待落地的结构（提示，非现状）

当前 `internal/api/api.go` 还只有 `/health` 与 `/greet/{name}` 两个 demo 端点；`LLMFox/` 是参考稿。把代理与可视化功能落进来时：

- **反向代理与流量记录**放在 `internal/` 新包（如 `internal/proxy`、`internal/store`），不要塞进 `internal/server` 或 `internal/api`——后两者是边界，不是业务实现。
- **流式事件**（SSE/逐 token / tool-call 增量）走 SSE：Huma 注册 `text/event-stream`，事件 payload 用判别式 `OneOf` 命名 schema，TS 端再写薄解析器把流文本切成联合类型。**`openapi-typescript` 给 SSE body 的类型只到 `string` 层**，再往下需要你自己拆。
- 上游 provider 配置（baseURL / apiKey）属于敏感数据：落盘前考虑加密；任何持久化都要先和用户对齐存储位置（macOS Keychain / Windows DPAPI / Linux libsecret 之类，不要直接明文 JSON）。

## 代码风格

- Go 严格 `gofmt`。lint 集合在 `.golangci.yml`（errcheck / govet+nilness / ineffassign / staticcheck / unused）。
- 渲染进程只用 TypeScript，整条流水线就是为了端到端类型安全。
- 注释与标识符英文；面向终端用户的字符串可中文。
- **Surgical changes 原则**：不要为了过 lint 顺手"清理"无关代码（详见 AGENTS.md）。
