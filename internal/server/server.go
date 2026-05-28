// Package server builds the HTTP server that hosts the Huma API.
//
// Security model:
//   - The server binds to 127.0.0.1 on an OS-assigned port. It is never
//     reachable from another host.
//   - A random handshake token is generated on startup and required on every
//     request via the X-Ai-fox-Token header. The Electron main process reads
//     the token from stdout and forwards it to the renderer through preload.
package server

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/zerx-lab/ai-fox/internal/api"
	"github.com/zerx-lab/ai-fox/internal/config"
	"github.com/zerx-lab/ai-fox/internal/store"
)

const (
	// AuthHeader is the request header that must carry the handshake token.
	AuthHeader = "X-Ai-fox-Token"

	// LoopbackHost is the only address we ever bind to.
	LoopbackHost = "127.0.0.1"
)

// Config controls how the HTTP server is constructed. All fields are optional;
// zero values produce a production-safe default.
type Config struct {
	// Port to bind. 0 means "let the OS pick a free port".
	Port int
	// Token to require. Empty means "generate a fresh random token".
	Token string
	// Settings, when set, is reused instead of opened from disk. Tests/dump
	// paths pass a throwaway store; production callers pass nil and let
	// Build() open the default location.
	Settings *config.Store
	// Traffic is the in-memory ring buffer of captured requests. Optional;
	// Build() will create a default-sized one if nil.
	Traffic *store.Store
	// Proxy lets the API surface read/control the reverse proxy. Optional;
	// when nil the proxy info endpoint reports stopped + empty address and
	// PUT /v1/proxy returns 500.
	Proxy api.ProxyController
}

// Built is the result of Build. It exposes everything the caller needs to
// start serving, print the handshake to stdout, or dump the OpenAPI schema.
type Built struct {
	Listener net.Listener
	// Handler is the mux wrapped with CORS. Use this when calling http.Serve.
	Handler  http.Handler
	Mux      *http.ServeMux
	API      huma.API
	Token    string
	Settings *config.Store
	Traffic  *store.Store
}

// Build constructs the listener, mux, Huma API, and auth middleware without
// starting Serve. Callers decide when (and whether) to block.
func Build(cfg Config) (*Built, error) {
	token := cfg.Token
	if token == "" {
		var err error
		token, err = randomToken(32)
		if err != nil {
			return nil, err
		}
	}

	settings := cfg.Settings
	if settings == nil {
		var err error
		settings, err = config.Open(config.DefaultPath())
		if err != nil {
			return nil, err
		}
	}
	traffic := cfg.Traffic
	if traffic == nil {
		traffic = store.New(500)
	}

	addr := net.JoinHostPort(LoopbackHost, strconv.Itoa(cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	humaCfg := huma.DefaultConfig("Ai-fox API", "0.3.0")
	humaCfg.Info.Description = "HTTP API exposed by the Go backend to the Electron renderer."
	humaAPI := humago.New(mux, humaCfg)

	// Auth middleware: reject any request missing the handshake token.
	humaAPI.UseMiddleware(authMiddleware(token))

	api.Register(humaAPI, api.Deps{
		Config:  settings,
		Traffic: traffic,
		Proxy:   cfg.Proxy,
	})

	return &Built{
		Listener: ln,
		Handler:  corsMiddleware(mux),
		Mux:      mux,
		API:      humaAPI,
		Token:    token,
		Settings: settings,
		Traffic:  traffic,
	}, nil
}

// corsMiddleware allows the Electron renderer (loaded from file:// in packaged
// mode or http://localhost:5173 in dev) to call the loopback backend. The
// server only binds to 127.0.0.1, so reflecting the origin does not widen the
// attack surface — anything that can reach the port could already speak HTTP
// to it directly. Preflight (OPTIONS) requests are short-circuited here so
// they never hit the auth middleware, which rightfully has no token to check.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Vary", "Origin")
		h.Set("Access-Control-Allow-Headers", "Content-Type, "+AuthHeader)
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		h.Set("Access-Control-Max-Age", "600")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authMiddleware(token string) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if ctx.Header(AuthHeader) != token {
			ctx.SetStatus(http.StatusUnauthorized)
			return
		}
		next(ctx)
	}
}

func randomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
