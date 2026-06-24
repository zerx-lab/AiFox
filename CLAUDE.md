# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目目标

ai-fox 是一个面向 AI/LLM 的调试与流量可视化桌面工具：

- **核心定位**：把 AI 应用调用栈的 HTTP 流量、prompt、MCP 调用、tool call 等环节做成可观测、可调试、可视化的细节。
- **网关模式**：Go 侧实现反向代理。用户在 ai-fox 中配置上游 AI provider 的 `baseURL` / `apiKey`，把工具（Claude Code、Codex 等）的端点指向 ai-fox，由 ai-fox 接管全部流量并落盘/转发/打点。
- **职责切分**：CEF 外壳只负责显示 UI 窗口；**所有业务逻辑（代理、解析、存储、SSE 拆帧等）一律写在 Go 侧**，通过 OpenAPI 暴露给渲染进程。
- **UI 参考**：`LLMFox/` 是设计参考稿（vanilla JSX + CSS，非运行代码）。借鉴其视觉元素与信息密度，但不要照抄设计稿的功能清单。

## 项目位置

整个项目就在仓库根 `AiFox/`，无子目录嵌套。所有命令在仓库根（或 `web/`）下执行。

## 与 AGENTS.md 的关系

`AGENTS.md` 是权威规则文档，包含项目布局、架构铁律、进程模型、IPC 桥接、流式 API 边界、安全模型、构建与发行细节。**遇到任何"该不该这么做"的判断，先读 AGENTS.md**。本文件只补充 Claude Code 视角的导航。

读了 AGENTS.md 还有疑问，再回到代码：`Taskfile.yml`、`main.go`、`internal/server/server.go` 是最高密度的信息源。

## 一次性前置

CEF 框架必须先装好，否则 app 起不来：

```bash
go install github.com/energye/energy/cmd/energy@latest
energy install   # 在仓库根执行，下载 CEF 到 ~/.energy
```

## 常用命令

所有命令走 `Taskfile.yml`，**不要**直接调 `pnpm` / `go`。

| 命令 | 用途 |
| --- | --- |
| `task dev` | build:web 后 `go run .` 起 CEF 窗口（无热重启，改代码后重跑）|
| `task verify` | 提交前硬门槛：`typecheck + test:go + test:go:race + test:ui + lint` |
| `task openapi` | 从 Go 导出 `openapi.yaml` |
| `task codegen` | 由 `openapi.yaml` 重生成 `web/src/api/schema.ts` |
| `task build:web` | vite build 渲染进程到 `resources/app/` |
| `task build:go` | 编译 CEF 二进制（embed `resources/app`）|
| `task build` | build:web + build:go |
| `task package` | `energy package` 出可分发产物 |
| `task clean` | 清掉 `resources/app openapi.yaml schema.ts` 与二进制 |

跑单个 Go 测试：`go test ./internal/<pkg> -run TestName -v`。

## 高层架构

```
Go struct ──► openapi.yaml ──► schema.ts ──► client.ts ──► renderer
   ▲                                                            │
   └──────────────── HTTP（127.0.0.1，X-Ai-fox-Token）──────────┘
```

单进程：Go 进程内同时跑 Huma server 与 CEF 窗口。

- `main.go` — Energy 入口。`cef.GlobalInit` → 起窗口 + 在 `SetBrowserProcessStartAfterCallback` 内 `go` 起 assetserve（22022，服务 embed 的 `resources/app`）+ Huma（loopback，OS 端口）。Huma 就绪填 `hs` + `close(ready)`。`browserInit` 注册 IPC。`openapi <path>` 子命令 dump schema 后退出。
- `internal/server/` — `Build(Config)` 构造 listener + Huma API + auth/CORS 中间件。监听**仅** 127.0.0.1；每次启动随机 hex token，要求 `X-Ai-fox-Token` 头。
- `internal/api/` — 所有 HTTP operation 的注册点。**新端点必须经 `huma.Register`**；裸 `net/http` handler 对 TS 不可见。
- `web/src/bridge/energy-bridge.ts` — 用 Energy 全局 `ipc` 实现 `window.aiFox`（handshake/env/window/theme），渲染进程零改动的关键。token 经 `app:handshake` IPC 下发。
- `web/src/api/client.ts` — `openapi-fetch` 薄封装，注入鉴权头；类型**只**来自生成的 `schema.ts`。
- `web/src/api/schema.ts` — `task codegen` 生成，**禁手改**，Biome 已排除。

### 改 API 的固定路径

1. 在 `internal/api/` 加请求/响应 struct + `huma.Register(api, op, handler)`。
2. 跑 `task codegen`（依赖 openapi）让 TS 拿到新类型。
3. 在 `web/src/renderer/` 用 `getClient()` 拿到的客户端发请求。
4. `task verify` 通过再提交。

**不要**手写 TS 请求/响应类型——唯一例外是 WebSocket 帧。能用 SSE 就别用 WS。

## 业务包导航（现状）

- `internal/proxy/` — 反向代理、断点拦截、重放。转发用完整 body（64MiB 上限），捕获截断到 `MaxBodyBytes`。
- `internal/store/` — 流量 ring buffer + JSONL 落盘 bootstrap。`clone()` 并发安全依赖"整体替换"约定。
- `internal/session/` — 会话聚合：指纹前缀匹配 + `X-Session-Affinity` 关联，1Hz flush/reconcile。
- `internal/llmparse/` — provider 协议解析（Anthropic Messages 全量），`normalize.go` 是跨 provider 归一化层。
- `internal/api/` — HTTP 端点边界（traffic、SSE、sessions、breakpoints、replay、settings、proxy 控制）。**业务实现不要塞进这里或 `internal/server`**。
- 流式事件走 SSE：判别式 `OneOf` 命名 schema，TS 端薄解析器拆联合。

整体规划见 `docs/PLAN.md`。

## 代码风格

- Go 严格 `gofmt`。lint 集合在 `.golangci.yml`。
- 渲染进程只用 TypeScript。
- 注释与标识符英文；面向终端用户的字符串可中文。
- **Surgical changes 原则**：不要为了过 lint 顺手"清理"无关代码。
