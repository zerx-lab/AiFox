# AGENTS.md

## 架构铁律

单桌面应用。Go (Huma) 承载全部业务逻辑并以 HTTP API 暴露；Electron 渲染进程是带类型的客户端。

```
Go struct → openapi.yaml → schema.ts → client.ts → renderer
```

- 业务逻辑**只**写在 Go。Electron 主进程仅守护 sidecar，渲染进程仅消费生成的 client。
- TS 的请求/响应类型**只能**来自 OpenAPI 导出。`electron/src/api/schema.ts` 自动生成、禁止手改。
- 端点必须通过 Huma 的 operation 注册。裸 `net/http` handler 不会进 OpenAPI，对 TS 不可见。
- 打包只用 Electron Forge，不要并行引入 electron-builder。

## 验收标准（提交/收尾前必过）

`task verify` 是硬门槛，它聚合：

- `task typecheck` — TS 全量 `tsc --noEmit`，含生成的 `schema.ts`。
- `task test:go` — `go test ./...`（另有 `test:go:race` 对 store/session/server 跑 -race）。
- `task test:ui` — Vitest，只测渲染进程纯逻辑模块（`electron/src/renderer/**/*.test.ts`，node 环境，无 DOM）；UI 交互仍由 typecheck 兜底。
- `task lint` — `lint:go`（golangci-lint：`govet`+`nilness`/`staticcheck`/`errcheck`/`ineffassign`/`unused`/`gofmt`）+ `lint:ts`（Biome，`schema.ts` 已排除）。

不要为了过 lint 顺手"清理"无关代码——见 surgical changes 原则。新加的 lint 规则要先在 `.golangci.yml` / `biome.json` 里登记，再写实现。`task build` 自身依赖 typecheck，可独立用于验证可分发产物能编。

## 流式 API 边界

- **SSE** 可经 OpenAPI 暴露。Huma 注册 `text/event-stream` 响应；事件 payload 必须注册为命名 schema（`OneOf` 判别式联合），TS 端再写薄解析器把流文本切成判别式联合——`openapi-typescript` 只会给 `string` 类型，自动生成不到这一层。
- **WebSocket** 不进 OpenAPI（OpenAPI 3.x 无 WS 帧定义；AsyncAPI 才是对应规范）。如果必须用 WS，帧类型是**唯一**允许手写 TS 接口的例外：放在 `electron/src/api/ws/` 下，Go 侧用同一份 struct 通过 `json.Marshal` 序列化，注释里互引文件路径，提交时一并改两侧。能用 SSE 就别用 WS。

## 关键路径

- `/internal/api/` — 新增端点写这里。
- `/internal/server/` — loopback 绑定与 token 鉴权中间件。
- `/electron/src/main/backend.ts` — 拉起 Go sidecar、解析 stdout 握手 JSON。
- `/electron/src/api/client.ts` — 手写的 `openapi-fetch` 封装，注入鉴权头。
- `/openapi.yaml` — Go ↔ TS 的契约检查点，入库提交。
- `/bin/<os>-<arch>/relay-backend[.exe]` — sidecar 产物。`<arch>` 使用 **Node 命名**（`x64`/`arm64`/`ia32`），不是 Go 的（`amd64`/`386`）；运行时 `backend.ts` 用 `process.arch` 解析目录，Taskfile 负责映射。
- `/scripts/dev-watcher.mjs` — dev-only Go 文件 watcher。`task dev` 用 `concurrently` 并行起它和 Electron。
- `/scripts/make-arch.sh` — `task build:arch` 调用的 pacman 打包脚本；生成 PKGBUILD + 调 makepkg，详见下文“发行包格式”。

## Dev 热重启

`task dev` 期间改 Go 文件会自动 hot restart：watcher 监听 `*.go`/`go.mod`/`go.sum`，防抖 300ms 后串跑 `openapi → codegen → build:go`，再写 `.dev-reload`（内容为 epoch ms）。Electron 主进程 `fs.watch` 父目录、按文件名过滤，看到变化就 respawn sidecar（新 port/token）并 `reloadIgnoringCache()` 所有窗口，渲染进程的 `getClient()` 缓存随之失效。

机制要点：
- IPC 走 trigger 文件而非 POSIX 信号——`process.kill(pid, "SIGUSR2")` 在 Windows 上是 no-op，文件机制三大平台一致（inotify / FSEvents / ReadDirectoryChangesW）。
- 主进程按 `mtimeMs` 去重，避免 atomic write 或 Windows rename+change 双事件触发两次 reload。
- watcher 自身 `chokidar.watch` 排除 `.dev-reload`，防止 watch→build→write→watch 自环。
- chokidar v4+ 不再支持 glob：必须传目录 + `ignored` 谓词（按扩展名过滤）。
- 不是 Flutter 的热重载——Go 不支持代码热替换；UI 状态会丢，但渲染进程的 TS HMR（含 `schema.ts` 变化）独立由 Forge 的 Vite 处理。

## 代码生成链路

改 Go API → `task openapi` → `task codegen` → TS 拿到新类型。`task dev` / `task build` 已串好这条链；单独跑中间步会有静默类型漂移。

所有编排走 `Taskfile.yml`，不要在文档/脚本里直接调 `pnpm` 或 `go`。命令清单用 `task --list` 查。

## 启动性能

冷启时间分布在 packaged 模式下实测约 350–550ms（双击 → window-loaded）。**Go sidecar 整个冷启 ~5ms，不是瓶颈**，因此别再尝试用裸 socket / 平台原生 HTTP 栈替代 `net/http`——会撕掉 OpenAPI 契约链路、收益接近 0。

主进程在 module top-level **同步** spawn sidecar 并 `ipcMain.handle("relay:handshake", …)`，不要把这两步搬回 `app.whenReady()`：sidecar 与 Electron 自身 init 并行能省 ~100ms Go-ready 时间、~20ms 用户感知。`startBackend()` 只用 `app.isPackaged` / `app.getAppPath()`，两者在 ready 前可调用；`ipcMain.handle` 是纯 JS event emitter。

`BrowserWindow` 不要套 `show: false` + `ready-to-show`。文档常见做法在 **Wayland 上反而拖慢窗口出现 500–700ms**（compositor "showable" 信号晚到）。`index.html` 已经 inline CSS + "loading…" 占位符，默认 `show: true` 的 first-paint 就是有内容的，没有白屏可隐藏。

`main.ts` 在 `app.setName` 之后、`app.whenReady()` 之前批量 `app.commandLine.appendSwitch` 设置 Chromium 开关：`disable-features=CalculateNativeWinOcclusion,Vulkan`（前者 Win-only 省 200–300ms，后者修 Wayland GPU process fatal crash 与 ~500ms swiftshader fallback 重试）、`disable-renderer-backgrounding`、`no-default-browser-check`，Linux 上额外 `ozone-platform-hint=auto`。新增开关一律放这一段，**不要**写进 `app.whenReady()` 回调——GPU/utility 子进程在那之前就已经 fork。

调试启动延迟用 `RELAY_BOOT_TRACE=1 ./out/<...>/relay`（dev: `RELAY_BOOT_TRACE=1 task dev`）——main process 会向 stderr 打印 `[trace:main]` 时间戳，覆盖 `module-load → backend-spawned → backend-handshake → app-ready → handshake-ipc-resolved → window-loaded`。trace 代码在 `electron/src/main/main.ts`，env 未设时是 no-op。

## 打包瘦身

`forge.config.ts` 的 `packagerConfig.afterExtract` hook 在 Electron 解压后、app 拷入前删两类文件：locales 只保留 `KEEP_LOCALES` 集合里的 `.pak`（默认 `en-US.pak`、`zh-CN.pak`，Chromium 强依赖 en-US 别删），以及 `LICENSES.chromium.html`（~20 MB 法务 dump）。Linux x64 打包从 316 MB 降到 251 MB。

要加新语言：往 `KEEP_LOCALES` 加 `<lang>.pak` 即可，hook 自己处理 linux/win32 和 darwin 两种布局。**不要**在这一层删 `libffmpeg.so`/`libvulkan*.so`——它们看着是死代码，但删了某些 codec 探测和 GPU init 路径会 fatal。

## 发行包格式

Forge maker 产出（`task make`）：`.deb` / `.rpm` / AppImage（Linux）、Squirrel.Windows（Win）、ZIP（三平台备用）。Forge 生态里**没有 pacman maker**（到 2026：`@reforged/maker-pacman` 不存在，`@electron-forge/maker-pkgbuild` 也不存在），所以 Arch 走独立路径：`task build:arch` → `scripts/make-arch.sh`。

**桌面注册（关键）**：AppImage 是便携单文件，**双击/命令行能跑但不会在应用菜单里注册**（系统集成要靠 AppImageLauncher 之类外部工具）。要让应用像原生程序一样进菜单，必须装 `.deb`/`.rpm`（`MakerDeb`/`MakerRpm`，底层 `electron-installer-debian`/`-redhat`）——它们的安装脚本会写 `/usr/share/applications/ai-fox.desktop`、hicolor 图标集、`/usr/bin/ai-fox` 启动器。`.desktop` 的 basename（`ai-fox`）= `app.setName("ai-fox")` 的 app_id，Wayland 任务栏图标匹配才成立；`Icon=ai-fox` 也要对上装进 hicolor 的 `ai-fox.png`。改 deb/rpm 的菜单元数据在 `forge.config.ts` 的 `MakerDeb`/`MakerRpm` options 里（`productName`/`genericName`/`categories`/`icon`/`bin`）。CI 在 Linux runner 上需 `dpkg fakeroot rpm` 三件套。

脚本拿 `task build` 产出的 `out/relay-linux-<node-arch>/` 当预编译产物，现生成 PKGBUILD（`package()` 只拷贝不编译，跟 electron-builder 同路子），调 `makepkg` 出 `out/make/arch/relay-<version>-1-<pkg-arch>.pkg.tar.zst`。布局：`/opt/relay/`（全套）、`/usr/bin/relay` 软链、`/usr/share/applications/relay.desktop`、`/usr/share/icons/hicolor/<size>/apps/relay.png`。

陷阱：`chrome-sandbox` 必须 `chmod 4755`（setuid root），否则 Electron 启动报“SUID sandbox helper binary was found, but is not configured correctly”；PKGBUILD 里 `options=('!strip' '!debug')` 是必须的，让 makepkg 别去动 Electron 的 `.so`，否则会破 ASAR integrity。要改依赖清单改 `depends=()`；pacman arch 名跟 Node arch 名不同，`make-arch.sh` 里有 `uname -m → NODE_ARCH/PKG_ARCH` 双映射表。

另一个不显然的坑：`cp -a` 会保留源目录的 perm。如果开发机的 umask 是 077/027，`task build` 产出的 `out/relay-linux-<arch>/` 顶层是 700，拷进 `$pkgdir/opt/relay/` 后同样是 700——desktop launcher 走 `/usr/bin/relay → /opt/relay/relay` 时 traversal 失败，KDE/GNOME 弹“无法找到程序 'relay'”。`make-arch.sh` 在 cp 后加了 `chmod -R a+rX`，顺序必须在 `chmod 4755 chrome-sandbox` 之前——否则 setuid 位被递归 chmod 清掉。

## 依赖策略

不要手改版本号。用 `go get pkg@latest` / `pnpm add pkg` 让工具挑版本，提交生成的 `go.sum` / `pnpm-lock.yaml`。若 peer dependency 强制非最新主版本，用 `pnpm view pkg@<major> version` 找该主版本最高兼容版，以 `pnpm add pkg@^<major>` 锁定。

## 安全模型（不显然的约束）

- Go 只绑 `127.0.0.1`，OS 分配端口。
- 每次启动生成新十六进制 token，要求 `X-Relay-Token` 头。token **只**经 preload 传给渲染进程——不要走 env / URL / 磁盘。
- Electron：`contextIsolation: true`、`sandbox: true`、`nodeIntegration: false`。渲染进程 CSP `connect-src` 限定 `http://127.0.0.1:*`。`style-src` 含 `'unsafe-inline'` 是因为脚手架 `index.html` 用了内联 `<style>`；改外部样式表时要同步收紧。
- Go server 在鉴权中间件**之前**用 CORS 中间件包裹 mux，反射 origin 并短路 `OPTIONS`。必要，因为渲染进程 origin（dev `http://localhost:5173`，打包后 `file://`）与 `http://127.0.0.1:<port>` 不同源；loopback-only 监听让这样做安全。
- Forge fuses 已加固打包应用（禁 `ELECTRON_RUN_AS_NODE`、开 ASAR 完整性等），见 `forge.config.ts`。

## 代码约定

- Go 遵循 `gofmt` 与标准布局。lint 集合定义在 `.golangci.yml`（v2 schema）；扩充前先评估收益再启用。
- 渲染进程用 TypeScript，不用 JavaScript——整条流水线的目的就是端到端类型。Biome 配置在 `biome.json`，`schema.ts` 已排除。
- 代码注释与标识符用英文；面向用户的字符串可用中文。

## 维护本文件

跨 agent 会话的持久化记忆。**何时更新**：

- 用户要求"记一下 / remember / 写进 AGENTS"。
- 新增/删除/重命名顶层目录、`task` 项、Go/TS 包边界。
- 改动依赖策略、安全模型、codegen 链路。
- 引入/淘汰工具（linter、formatter、test runner、CI）。
- 发现非显然的坑（如"Forge 要求 pnpm `node-linker=hoisted`"）。

**怎么更新**：原位编辑，不追加 changelog；规则冲突时**替换**旧条目而非并列；删过期内容与加新内容同次提交。

**不要写进来**：会话临时笔记、教程散文、`task --list` / `go doc` 一条命令可得的信息。
