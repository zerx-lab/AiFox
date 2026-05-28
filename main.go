// Command ai-fox is the Go backend for the ai-fox Electron app.
//
// Subcommands:
//
//	(no args)         start the HTTP API + reverse proxy on 127.0.0.1, print
//	                  a single JSON handshake line to stdout, then block.
//	openapi [path]    dump the OpenAPI 3.1 schema as YAML and exit. The
//	                  default path is "openapi.yaml" relative to the cwd.
//
// The handshake JSON contains both the API and proxy coordinates:
//
//	{"port":54321,"token":"<hex>","baseUrl":"http://127.0.0.1:54321",
//	 "proxyPort":54322,"proxyBaseUrl":"http://127.0.0.1:54322"}
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/zerx-lab/ai-fox/internal/config"
	"github.com/zerx-lab/ai-fox/internal/proxy"
	"github.com/zerx-lab/ai-fox/internal/server"
	"github.com/zerx-lab/ai-fox/internal/store"
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

	if err := runServer(); err != nil {
		fmt.Fprintln(os.Stderr, "server failed:", err)
		os.Exit(1)
	}
}

func runServer() error {
	settings, err := config.Open(config.DefaultPath())
	if err != nil {
		return err
	}
	traffic := store.New(500)

	ctrl, err := proxy.NewController(0, settings, traffic)
	if err != nil {
		return fmt.Errorf("proxy: %w", err)
	}
	if settings.Get().ProxyEnabled {
		if err := ctrl.Start(); err != nil {
			return fmt.Errorf("proxy start: %w", err)
		}
	}

	built, err := server.Build(server.Config{
		Settings: settings,
		Traffic:  traffic,
		Proxy:    ctrl,
	})
	if err != nil {
		return err
	}

	apiAddr := built.Listener.Addr().(*net.TCPAddr)
	handshake := map[string]any{
		"port":         apiAddr.Port,
		"token":        built.Token,
		"baseUrl":      fmt.Sprintf("http://%s:%d", server.LoopbackHost, apiAddr.Port),
		"proxyPort":    ctrl.Port(),
		"proxyBaseUrl": "http://" + ctrl.Address(),
		"proxyEnabled": ctrl.Enabled(),
	}
	if err := json.NewEncoder(os.Stdout).Encode(handshake); err != nil {
		return err
	}

	return http.Serve(built.Listener, built.Handler)
}

func dumpOpenAPI(path string) error {
	// Use a throwaway settings store so the dump doesn't read or touch the
	// user's real config file. Token is irrelevant — listener is closed.
	tmp, err := os.MkdirTemp("", "ai-fox-openapi-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	cfg, err := config.Open(tmp + "/settings.json")
	if err != nil {
		return err
	}

	built, err := server.Build(server.Config{
		Token:    "dump",
		Settings: cfg,
		Traffic:  store.New(1),
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
