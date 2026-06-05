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
	"path/filepath"

	"github.com/zerx-lab/ai-fox/internal/config"
	"github.com/zerx-lab/ai-fox/internal/llmparse"
	"github.com/zerx-lab/ai-fox/internal/proxy"
	"github.com/zerx-lab/ai-fox/internal/server"
	"github.com/zerx-lab/ai-fox/internal/session"
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
	// Persist finalized traffic alongside settings.json so the renderer's
	// session list survives a restart. The file is recreated on demand
	// (after a user-triggered clear) and lives at user-only 0600 perms.
	trafficPath := filepath.Join(filepath.Dir(config.DefaultPath()), "traffic.jsonl")
	traffic, err := store.NewPersistent(500, trafficPath)
	if err != nil {
		return fmt.Errorf("traffic store: %w", err)
	}
	// Entries restored from disk decode their Analysis as map[string]any; re-type
	// them to *llmparse.Analysis so the API projections and session aggregator
	// see the structured view (otherwise restored traffic shows no model/tokens
	// and never re-aggregates into sessions). Runs before the aggregator starts.
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
	// Honor the persisted enabled flag, but boot defaults to disabled — the
	// user has to manually press "Connect" in the UI on a fresh install.
	if settings.Get().ProxyEnabled {
		if err := ctrl.Start(); err != nil {
			// Don't fail the whole app — let the user see the port conflict
			// in the UI and pick a different port. The handshake still goes
			// out so the renderer can boot and surface the error.
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

	// Pass an unstarted aggregator and an unstarted proxy controller so
	// optional session / replay endpoints make it into the schema. Neither
	// is started here; both are throwaways.
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
