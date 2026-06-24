# ai-fox

面向 AI/LLM 的调试与流量可视化桌面工具。把 AI 应用调用栈的 HTTP 流量、prompt、MCP 调用、tool call 做成可观测、可调试、可视化的细节。

## 架构

单桌面应用，单进程：Go 进程内同时跑 Huma HTTP server（业务逻辑）和 CEF 窗口（Energy v2）。渲染进程是带类型的 HTTP 客户端。

```
Go struct ──► openapi.yaml ──► schema.ts ──► client.ts ──► renderer
   ▲                                                            │
   └──────────────── HTTP（127.0.0.1，X-Ai-fox-Token）──────────┘
```

- 业务逻辑只写在 Go（`internal/`）；CEF 外壳（`main.go`）只负责窗口、双 server、IPC 桥接。
- TS 的请求/响应类型只来自 OpenAPI 导出，`web/src/api/schema.ts` 自动生成、禁止手改。
- **网关模式**：Go 侧反向代理。在 ai-fox 配置上游 provider 的 `baseURL`/`apiKey`，把工具（Claude Code、Codex 等）端点指向 ai-fox，由其接管流量并落盘/转发/打点。

## 目录结构

```
AiFox/
├── main.go                   Energy(CEF) 入口：Huma server + CEF 窗口 + IPC
├── internal/                 Go 业务逻辑（api/server/proxy/llmparse/store/config/session/pricing）
├── web/                      渲染进程（TypeScript）
│   ├── src/api/              client.ts + 生成的 schema.ts
│   ├── src/bridge/           energy-bridge.ts（window.aiFox 适配层）
│   └── src/renderer/         UI
├── resources/app/            vite build 产物，Go embed
├── config/energy_*.json      energy package 平台打包配置
├── docs/PLAN.md              整体规划
├── LLMFox/                   UI 设计参考稿（非运行代码）
└── Taskfile.yml              唯一编排入口
```

## 一次性前置

CEF 框架必须先装好，否则 app 起不来：

```bash
go install github.com/energye/energy/cmd/energy@latest
energy install   # 在仓库根执行，下载 CEF 到 ~/.energy
```

## 常用命令

所有编排走 `Taskfile.yml`，不要直接调 `pnpm` / `go`。完整列表 `task --list`。

| 命令 | 用途 |
| --- | --- |
| `task dev` | build:web 后 `go run .` 起 CEF 窗口（无热重启，改代码后重跑）|
| `task verify` | 提交前硬门槛：typecheck + test:go + test:go:race + test:ui + lint |
| `task openapi` | 从 Go 导出 `openapi.yaml` |
| `task codegen` | 由 `openapi.yaml` 重生成 `web/src/api/schema.ts` |
| `task build` | build:web + build:go |
| `task package` | `energy package` 出可分发产物 |

## 文档

- `AGENTS.md` — 权威规则：项目布局、架构铁律、进程模型、IPC 桥接、流式 API 边界、安全模型、构建发行。
- `CLAUDE.md` — Claude Code 视角的导航补充。
- `docs/PLAN.md` — 整体规划。
