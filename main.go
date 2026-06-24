// Command ai-fox is the Energy(CEF) desktop app for ai-fox.
//
// It runs the Huma HTTP API + reverse proxy in-process and renders the
// TypeScript UI in a CEF window. The renderer talks to the backend over
// loopback HTTP using a per-launch token, exactly as the former Electron
// shell did; the only thing replaced is the process shell.
//
// Subcommands:
//
//	(no args)         launch the CEF window + start the HTTP API + reverse
//	                  proxy on 127.0.0.1.
//	openapi [path]    dump the OpenAPI 3.1 schema as YAML and exit. The
//	                  default path is "openapi.yaml" relative to the cwd.
package main

import (
	"embed"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/energye/energy/v2/cef"
	"github.com/energye/energy/v2/cef/ipc"
	"github.com/energye/energy/v2/pkgs/assetserve"
	"github.com/energye/golcl/lcl"
	lcltypes "github.com/energye/golcl/lcl/types"

	"github.com/zerx-lab/ai-fox/internal/config"
	"github.com/zerx-lab/ai-fox/internal/llmparse"
	"github.com/zerx-lab/ai-fox/internal/proxy"
	"github.com/zerx-lab/ai-fox/internal/server"
	"github.com/zerx-lab/ai-fox/internal/session"
	"github.com/zerx-lab/ai-fox/internal/store"
)

//go:embed resources
var resources embed.FS

// assetPort serves the embedded renderer bundle (resources/app/*) over
// loopback. Fixed because a single-instance desktop app rarely collides; if
// it ever does, change here and Config.Url stays in sync via the constant.
const assetPort = 22022

// handshake mirrors the renderer's Handshake interface (api/client.ts). The
// JSON tags MUST match: port/token/baseUrl/proxyPort/proxyBaseUrl/proxyEnabled.
type handshake struct {
	Port         int    `json:"port"`
	Token        string `json:"token"`
	BaseURL      string `json:"baseUrl"`
	ProxyPort    int    `json:"proxyPort"`
	ProxyBaseURL string `json:"proxyBaseUrl"`
	ProxyEnabled bool   `json:"proxyEnabled"`
}

// Shared handshake, filled once the Huma server is listening; `ready` is
// closed at that point so the IPC layer can wait before emitting.
var (
	hs    handshake
	ready = make(chan struct{})
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "openapi" {
		path := "openapi.yaml"
		if len(os.Args) > 2 {
			path = os.Args[2]
		}
		if err := dumpOpenAPI(path); err != nil {
			fmt.Fprintln(os.Stderr, "openapi dump failed:", err)
			os.Exit(1)
		}
		_, _ = fmt.Fprintln(os.Stdout, "wrote", path)
		return
	}

	cef.GlobalInit(nil, &resources)
	app := cef.NewApplication()

	cef.BrowserWindow.Config.Url = fmt.Sprintf("http://127.0.0.1:%d/app/index.html", assetPort)
	cef.BrowserWindow.Config.Title = "ai-fox"
	cef.BrowserWindow.Config.EnableHideCaption = true
	cef.BrowserWindow.Config.Width = 1100
	cef.BrowserWindow.Config.Height = 720
	cef.BrowserWindow.Config.MinWidth = 720
	cef.BrowserWindow.Config.MinHeight = 480
	// Window/taskbar icon for the dev/run window. config/energy_*.json only
	// affects packaged artifacts; the live window needs the icon set here.
	// IconFS reads from the embedded FS (resources/icon.ico); on Linux VF
	// windows a .png is used instead.
	cef.BrowserWindow.Config.IconFS = "resources/icon.ico"

	cef.SetBrowserProcessStartAfterCallback(func(b bool) {
		// Serve the embedded renderer bundle.
		asset := assetserve.NewAssetsHttpServer()
		asset.PORT = assetPort
		asset.AssetsFSName = "resources"
		asset.Assets = &resources
		go asset.StartHttpServer()

		// Start the Huma API + reverse proxy. Fills `hs` and closes `ready`.
		go func() {
			if err := serve(); err != nil {
				fmt.Fprintln(os.Stderr, "server failed:", err)
				os.Exit(1)
			}
		}()
	})

	cef.BrowserWindow.SetBrowserInit(browserInit)
	cef.Run(app)
}

// browserInit wires the IPC bridge consumed by web/src/bridge/energy-bridge.ts.
// Go->JS data goes through ipc.Emit; JS->Go commands through ipc.On (one-way).
func browserInit(event *cef.BrowserEvent, window cef.IBrowserWindow) {
	// JS->Go window commands. Window mutations are scheduled on the main UI
	// thread via golcl to avoid cross-thread crashes.
	ipc.On("window:minimize", func() {
		cef.QueueAsyncCall(func(id int) {
			window.Minimize()
		})
	})
	ipc.On("window:maximize-toggle", func() {
		cef.QueueAsyncCall(func(id int) {
			if window.WindowState() == lcltypes.WsMaximized {
				window.Restore()
			} else {
				window.Maximize()
			}
			ipc.Emit("window:maximized-changed", window.WindowState() == lcltypes.WsMaximized)
		})
	})
	ipc.On("window:close", func() {
		cef.QueueAsyncCall(func(id int) {
			window.CloseBrowserWindow()
		})
	})

	// Push initial data to the renderer after each page load. Module scripts
	// (defer) register their ipc.on listeners before OnLoadEnd fires, so these
	// emits are not missed.
	event.SetOnLoadEnd(func(sender lcl.IObject, browser *cef.ICefBrowser, frame *cef.ICefFrame, httpStatusCode int32, window cef.IBrowserWindow) {
		ipc.Emit("app:env", nodePlatform(runtime.GOOS))
		ipc.Emit("window:maximized-changed", window.WindowState() == lcltypes.WsMaximized)
		go func() {
			<-ready
			ipc.Emit("app:handshake", hs)
		}()
	})
}

// nodePlatform maps Go's GOOS to Node's process.platform values so the
// renderer's Env.platform matches what the Electron shell reported.
func nodePlatform(goos string) string {
	switch goos {
	case "windows":
		return "win32"
	default:
		return goos
	}
}

// serve starts the Huma API + reverse proxy, fills the shared handshake, then
// blocks serving HTTP. Mirrors the former Electron sidecar's runServer, minus
// the stdout handshake (replaced by the shared `hs` + `ready` close).
func serve() error {
	settings, err := config.Open(config.DefaultPath())
	if err != nil {
		return err
	}
	trafficPath := filepath.Join(filepath.Dir(config.DefaultPath()), "traffic.jsonl")
	traffic, err := store.NewPersistent(500, trafficPath)
	if err != nil {
		return fmt.Errorf("traffic store: %w", err)
	}
	traffic.MapAnalysis(func(a any) any {
		if r := llmparse.ReifyAnalysis(a); r != nil {
			return r
		}
		return a
	})

	ctrl, err := proxy.NewController(settings.Get().ProxyPort, settings, traffic)
	if err != nil {
		return fmt.Errorf("proxy: %w", err)
	}
	if settings.Get().ProxyEnabled {
		if err := ctrl.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "proxy start (disabled):", err)
		}
	}

	namesPath := filepath.Join(filepath.Dir(config.DefaultPath()), "session-names.json")
	aggregator := session.New(traffic, namesPath)
	_ = aggregator.Start()

	built, err := server.Build(server.Config{
		Settings: settings,
		Traffic:  traffic,
		Proxy:    ctrl,
		Sessions: aggregator,
	})
	if err != nil {
		return err
	}

	apiAddr := built.Listener.Addr().(*net.TCPAddr)
	hs = handshake{
		Port:         apiAddr.Port,
		Token:        built.Token,
		BaseURL:      fmt.Sprintf("http://%s:%d", server.LoopbackHost, apiAddr.Port),
		ProxyPort:    ctrl.Port(),
		ProxyBaseURL: "http://" + ctrl.Address(),
		ProxyEnabled: ctrl.Enabled(),
	}
	close(ready)

	return http.Serve(built.Listener, built.Handler)
}

func dumpOpenAPI(path string) error {
	tmp, err := os.MkdirTemp("", "ai-fox-openapi-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	cfg, err := config.Open(tmp + "/settings.json")
	if err != nil {
		return err
	}

	traffic := store.New(1)
	ctrl, err := proxy.NewController(0, cfg, traffic)
	if err != nil {
		return err
	}
	built, err := server.Build(server.Config{
		Token:    "dump",
		Settings: cfg,
		Traffic:  traffic,
		Proxy:    ctrl,
		Sessions: session.New(traffic, ""),
	})
	if err != nil {
		return err
	}
	_ = built.Listener.Close()

	yaml, err := built.API.OpenAPI().YAML()
	if err != nil {
		return err
	}
	return os.WriteFile(path, yaml, 0o644)
}
