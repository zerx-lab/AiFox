// Package api registers every HTTP operation Huma serves.
//
// Adding a new endpoint:
//  1. Define request/response structs in this package (or a sibling file).
//  2. Call huma.Register(api, op, handler) inside Register().
//  3. Re-run `task codegen` so the TS client picks up the new types.
//
// All operations must register through Huma; bare net/http handlers do NOT
// surface in the OpenAPI schema and therefore are invisible to the renderer.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zerx-lab/ai-fox/internal/config"
	"github.com/zerx-lab/ai-fox/internal/llmparse"
	"github.com/zerx-lab/ai-fox/internal/store"
)

// ProxyController is the surface api/ needs from internal/proxy. Defined here
// to avoid an import cycle with proxy/, which itself depends on config/.
type ProxyController interface {
	Enabled() bool
	Address() string
	Port() int
	SetEnabled(bool) (bool, error)
	SetPort(int) error
}

// Deps groups the runtime collaborators handlers need. Wired in main.
type Deps struct {
	Config  *config.Store
	Traffic *store.Store
	Proxy   ProxyController
}

// Register wires every operation onto the provided Huma API.
func Register(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "get-health",
		Method:      http.MethodGet,
		Path:        "/health",
		Summary:     "Health probe",
		Description: "Returns ok when the Go backend is reachable.",
		Tags:        []string{"system"},
	}, healthHandler)

	huma.Register(api, huma.Operation{
		OperationID: "get-settings",
		Method:      http.MethodGet,
		Path:        "/v1/settings",
		Summary:     "Read persisted user settings",
		Tags:        []string{"settings"},
	}, getSettingsHandler(deps.Config))

	huma.Register(api, huma.Operation{
		OperationID: "put-settings",
		Method:      http.MethodPut,
		Path:        "/v1/settings",
		Summary:     "Replace persisted user settings",
		Tags:        []string{"settings"},
	}, putSettingsHandler(deps.Config, deps.Proxy))

	huma.Register(api, huma.Operation{
		OperationID: "get-proxy-info",
		Method:      http.MethodGet,
		Path:        "/v1/proxy",
		Summary:     "Proxy address and configuration status",
		Tags:        []string{"proxy"},
	}, getProxyInfoHandler(deps.Config, deps.Proxy))

	huma.Register(api, huma.Operation{
		OperationID: "set-proxy-enabled",
		Method:      http.MethodPut,
		Path:        "/v1/proxy",
		Summary:     "Start or stop the proxy listener",
		Tags:        []string{"proxy"},
	}, setProxyEnabledHandler(deps.Config, deps.Proxy))

	huma.Register(api, huma.Operation{
		OperationID: "list-traffic",
		Method:      http.MethodGet,
		Path:        "/v1/traffic",
		Summary:     "List captured proxy traffic (newest first)",
		Tags:        []string{"traffic"},
	}, listTrafficHandler(deps.Traffic))

	huma.Register(api, huma.Operation{
		OperationID: "get-traffic",
		Method:      http.MethodGet,
		Path:        "/v1/traffic/{id}",
		Summary:     "Fetch a single captured entry by id",
		Tags:        []string{"traffic"},
	}, getTrafficHandler(deps.Traffic))

	huma.Register(api, huma.Operation{
		OperationID: "clear-traffic",
		Method:      http.MethodDelete,
		Path:        "/v1/traffic",
		Summary:     "Discard every captured entry",
		Tags:        []string{"traffic"},
		// 204 keeps the response side trivially typed in the TS client.
		DefaultStatus: http.StatusNoContent,
	}, clearTrafficHandler(deps.Traffic))

	registerTrafficStream(api, deps.Traffic)
}

// --- health ---

type HealthOutput struct {
	Body struct {
		Status string    `json:"status" example:"ok" doc:"Always 'ok' when the server is healthy."`
		Time   time.Time `json:"time" doc:"Server time in RFC3339."`
	}
}

func healthHandler(_ context.Context, _ *struct{}) (*HealthOutput, error) {
	out := &HealthOutput{}
	out.Body.Status = "ok"
	out.Body.Time = time.Now().UTC()
	return out, nil
}

// --- settings ---

// HeaderKVBody is one user-defined header on the wire.
type HeaderKVBody struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SettingsBody mirrors config.Settings on the wire. The API key is returned
// to the renderer in plaintext because the renderer needs to display it in
// the settings form. The transport (loopback + token) and renderer (sandbox
// + contextIsolation) keep that exposure local.
type SettingsBody struct {
	UpstreamBaseURL string         `json:"upstreamBaseUrl" doc:"Upstream AI provider base URL, e.g. https://api.anthropic.com"`
	UpstreamAPIKey  string         `json:"upstreamApiKey" doc:"API key forwarded to upstream (ignored when authPreset=custom)"`
	AuthPreset      string         `json:"authPreset" enum:"anthropic,openai,openai-responses,custom" doc:"Picks the header shape used to authenticate upstream"`
	CustomHeaders   []HeaderKVBody `json:"customHeaders" doc:"User-defined headers injected on every forwarded request (only when authPreset=custom)"`
	ProxyEnabled    bool           `json:"proxyEnabled" doc:"Whether the proxy listener should be running"`
	ProxyPort       int            `json:"proxyPort" minimum:"1" maximum:"65535" doc:"Fixed loopback port the proxy binds to"`
	Language        string         `json:"language" enum:",en,zh-CN" doc:"UI language code; empty means follow OS"`
	Theme           string         `json:"theme" enum:",dark,light" doc:"UI color scheme; empty means follow OS"`
}

func wireSettings(s config.Settings) SettingsBody {
	headers := make([]HeaderKVBody, 0, len(s.CustomHeaders))
	for _, h := range s.CustomHeaders {
		headers = append(headers, HeaderKVBody{Name: h.Name, Value: h.Value})
	}
	return SettingsBody{
		UpstreamBaseURL: s.UpstreamBaseURL,
		UpstreamAPIKey:  s.UpstreamAPIKey,
		AuthPreset:      string(s.AuthPreset),
		CustomHeaders:   headers,
		ProxyEnabled:    s.ProxyEnabled,
		ProxyPort:       s.ProxyPort,
		Language:        string(s.Language),
		Theme:           string(s.Theme),
	}
}

func fromWireSettings(b SettingsBody) config.Settings {
	headers := make([]config.HeaderKV, 0, len(b.CustomHeaders))
	for _, h := range b.CustomHeaders {
		if h.Name == "" {
			continue
		}
		headers = append(headers, config.HeaderKV{Name: h.Name, Value: h.Value})
	}
	return config.Settings{
		UpstreamBaseURL: b.UpstreamBaseURL,
		UpstreamAPIKey:  b.UpstreamAPIKey,
		AuthPreset:      config.AuthPreset(b.AuthPreset),
		CustomHeaders:   headers,
		ProxyEnabled:    b.ProxyEnabled,
		ProxyPort:       b.ProxyPort,
		Language:        config.Language(b.Language),
		Theme:           config.Theme(b.Theme),
	}
}

type GetSettingsOutput struct {
	Body SettingsBody
}

func getSettingsHandler(cfg *config.Store) func(context.Context, *struct{}) (*GetSettingsOutput, error) {
	return func(_ context.Context, _ *struct{}) (*GetSettingsOutput, error) {
		return &GetSettingsOutput{Body: wireSettings(cfg.Get())}, nil
	}
}

type PutSettingsInput struct {
	Body SettingsBody
}

// putSettingsHandler persists the new settings AND reconciles the proxy
// controller: if the user toggled ProxyEnabled or changed the port here, it
// takes effect right away without a separate call to /v1/proxy.
func putSettingsHandler(cfg *config.Store, prox ProxyController) func(context.Context, *PutSettingsInput) (*GetSettingsOutput, error) {
	return func(_ context.Context, in *PutSettingsInput) (*GetSettingsOutput, error) {
		if err := cfg.Set(fromWireSettings(in.Body)); err != nil {
			return nil, huma.Error500InternalServerError("persist settings: " + err.Error())
		}
		updated := cfg.Get()
		if prox != nil {
			if prox.Port() != updated.ProxyPort {
				if err := prox.SetPort(updated.ProxyPort); err != nil {
					return nil, huma.Error500InternalServerError("change proxy port: " + err.Error())
				}
			}
			if prox.Enabled() != updated.ProxyEnabled {
				if _, err := prox.SetEnabled(updated.ProxyEnabled); err != nil {
					return nil, huma.Error500InternalServerError("reconcile proxy: " + err.Error())
				}
			}
		}
		return &GetSettingsOutput{Body: wireSettings(updated)}, nil
	}
}

// --- proxy info / control ---

type ProxyInfoOutput struct {
	Body struct {
		Address    string `json:"address" doc:"Loopback host:port the proxy listens on (stable across enable/disable)"`
		BaseURL    string `json:"baseUrl" doc:"Full http://host:port URL clients should point at"`
		Port       int    `json:"port" doc:"Fixed loopback port the proxy is configured to use"`
		Configured bool   `json:"configured" doc:"True when upstreamBaseUrl is set"`
		Enabled    bool   `json:"enabled" doc:"True when the listener is accepting connections"`
	}
}

func getProxyInfoHandler(cfg *config.Store, prox ProxyController) func(context.Context, *struct{}) (*ProxyInfoOutput, error) {
	return func(_ context.Context, _ *struct{}) (*ProxyInfoOutput, error) {
		out := &ProxyInfoOutput{}
		if prox != nil {
			out.Body.Address = prox.Address()
			out.Body.Port = prox.Port()
			out.Body.Enabled = prox.Enabled()
			if out.Body.Address != "" {
				out.Body.BaseURL = fmt.Sprintf("http://%s", out.Body.Address)
			}
		}
		out.Body.Configured = cfg.Get().UpstreamBaseURL != ""
		return out, nil
	}
}

type SetProxyEnabledInput struct {
	Body struct {
		Enabled bool `json:"enabled" doc:"Desired state of the proxy listener"`
	}
}

func setProxyEnabledHandler(cfg *config.Store, prox ProxyController) func(context.Context, *SetProxyEnabledInput) (*ProxyInfoOutput, error) {
	return func(_ context.Context, in *SetProxyEnabledInput) (*ProxyInfoOutput, error) {
		if prox == nil {
			return nil, huma.Error500InternalServerError("proxy controller not wired")
		}
		if _, err := prox.SetEnabled(in.Body.Enabled); err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		// Mirror the change into persisted settings so a restart respects it.
		current := cfg.Get()
		if current.ProxyEnabled != in.Body.Enabled {
			current.ProxyEnabled = in.Body.Enabled
			if err := cfg.Set(current); err != nil {
				return nil, huma.Error500InternalServerError("persist proxy state: " + err.Error())
			}
		}
		out := &ProxyInfoOutput{}
		out.Body.Address = prox.Address()
		out.Body.Port = prox.Port()
		out.Body.Enabled = prox.Enabled()
		if out.Body.Address != "" {
			out.Body.BaseURL = fmt.Sprintf("http://%s", out.Body.Address)
		}
		out.Body.Configured = cfg.Get().UpstreamBaseURL != ""
		return out, nil
	}
}

// --- traffic list/get/clear ---

// TrafficEntry is the on-wire representation. It must stay in sync with
// store.Entry; using a separate struct keeps OpenAPI in control of the
// schema rather than leaking Go-only field tags.
type TrafficEntry struct {
	ID              string             `json:"id"`
	StartedAt       time.Time          `json:"startedAt"`
	EndedAt         time.Time          `json:"endedAt"`
	DurationMillis  int64              `json:"durationMillis"`
	Method          string             `json:"method"`
	URL             string             `json:"url"`
	UpstreamURL     string             `json:"upstreamUrl"`
	StatusCode      int                `json:"statusCode"`
	RequestHeaders  map[string]string  `json:"requestHeaders"`
	RequestBody     string             `json:"requestBody"`
	RequestSize     int64              `json:"requestSize"`
	ResponseHeaders map[string]string  `json:"responseHeaders"`
	ResponseBody    string             `json:"responseBody"`
	ResponseSize    int64              `json:"responseSize"`
	Streaming       bool               `json:"streaming"`
	Truncated       bool               `json:"truncated"`
	Error           string             `json:"error,omitempty"`
	Analysis        *llmparse.Analysis `json:"analysis,omitempty"`
}

func toWire(e *store.Entry) TrafficEntry {
	out := TrafficEntry{
		ID:              e.ID,
		StartedAt:       e.StartedAt,
		EndedAt:         e.EndedAt,
		DurationMillis:  e.DurationMillis,
		Method:          e.Method,
		URL:             e.URL,
		UpstreamURL:     e.UpstreamURL,
		StatusCode:      e.StatusCode,
		RequestHeaders:  e.RequestHeaders,
		RequestBody:     e.RequestBody,
		RequestSize:     e.RequestSize,
		ResponseHeaders: e.ResponseHeaders,
		ResponseBody:    e.ResponseBody,
		ResponseSize:    e.ResponseSize,
		Streaming:       e.Streaming,
		Truncated:       e.Truncated,
		Error:           e.Error,
	}
	if a, ok := e.Analysis.(*llmparse.Analysis); ok {
		out.Analysis = a
	}
	return out
}

type ListTrafficOutput struct {
	Body struct {
		Items []TrafficEntry `json:"items"`
	}
}

func listTrafficHandler(st *store.Store) func(context.Context, *struct{}) (*ListTrafficOutput, error) {
	return func(_ context.Context, _ *struct{}) (*ListTrafficOutput, error) {
		entries := st.List()
		out := &ListTrafficOutput{}
		out.Body.Items = make([]TrafficEntry, 0, len(entries))
		for _, e := range entries {
			out.Body.Items = append(out.Body.Items, toWire(e))
		}
		return out, nil
	}
}

type GetTrafficInput struct {
	ID string `path:"id" maxLength:"64"`
}

type GetTrafficOutput struct {
	Body TrafficEntry
}

func getTrafficHandler(st *store.Store) func(context.Context, *GetTrafficInput) (*GetTrafficOutput, error) {
	return func(_ context.Context, in *GetTrafficInput) (*GetTrafficOutput, error) {
		e, ok := st.Get(in.ID)
		if !ok {
			return nil, huma.Error404NotFound("no such traffic entry")
		}
		return &GetTrafficOutput{Body: toWire(e)}, nil
	}
}

func clearTrafficHandler(st *store.Store) func(context.Context, *struct{}) (*struct{}, error) {
	return func(_ context.Context, _ *struct{}) (*struct{}, error) {
		st.Clear()
		return nil, nil
	}
}

// --- traffic stream (SSE) ---
//
// We bypass Huma's auto-generated body schema for this endpoint because SSE
// payloads can't be expressed in OpenAPI 3 in a way openapi-typescript would
// turn into a useful type. Instead the route is registered via the raw mux
// (see RegisterRawRoutes) and the renderer parses lines manually.
//
// The wrapper here exists only so the operation shows up in OpenAPI as an
// event-stream response, which is enough for the user-facing API reference.

func registerTrafficStream(api huma.API, st *store.Store) {
	op := huma.Operation{
		OperationID: "stream-traffic",
		Method:      http.MethodGet,
		Path:        "/v1/traffic/stream",
		Summary:     "Server-sent events stream of new and updated entries",
		Tags:        []string{"traffic"},
	}
	huma.Register(api, op, func(_ context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{
			Body: func(streamCtx huma.Context) {
				writeTrafficStream(streamCtx, st)
			},
		}, nil
	})
}

// writeTrafficStream pushes an event each time the store sees a new or
// updated entry. The first event ("snapshot") replays the current buffer so
// the renderer can render an initial list without a separate fetch.
func writeTrafficStream(ctx huma.Context, st *store.Store) {
	ctx.SetHeader("Content-Type", "text/event-stream")
	ctx.SetHeader("Cache-Control", "no-cache")
	ctx.SetHeader("Connection", "keep-alive")
	w := ctx.BodyWriter()
	flusher, _ := w.(http.Flusher)

	send := func(event string, payload any) bool {
		raw, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}

	// Initial snapshot — newest first to match the list endpoint.
	snap := make([]TrafficEntry, 0)
	for _, e := range st.List() {
		snap = append(snap, toWire(e))
	}
	if !send("snapshot", snap) {
		return
	}

	ch, cancel := st.Subscribe()
	defer cancel()

	clientGone := ctx.Context().Done()
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return
			}
			if !send("entry", toWire(e)) {
				return
			}
		case <-keepalive.C:
			// Comment-line keepalive; ignored by EventSource but keeps proxies
			// from closing the idle connection.
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		case <-clientGone:
			return
		}
	}
}
