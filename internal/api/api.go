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
	"github.com/zerx-lab/ai-fox/internal/session"
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

// ReplayOverrides mirrors proxy.ReplayOverrides on the API boundary. Same
// pointer-as-tristate convention.
type ReplayOverrides struct {
	Model       *string
	Temperature *float64
	TopP        *float64
	TopK        *int
	MaxTokens   *int
	Stream      *bool
}

// Replayer is the optional surface a controller may expose. Build wires it
// in when available; if absent, the replay endpoint reports 503.
type Replayer interface {
	Replay(ctx context.Context, originalID string, overrides ReplayOverrides) (string, error)
}

// Deps groups the runtime collaborators handlers need. Wired in main.
type Deps struct {
	Config      *config.Store
	Traffic     *store.Store
	Proxy       ProxyController
	Replay      Replayer
	Breakpoints BreakpointController
	Sessions    *session.Aggregator
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
		Summary:     "List captured proxy traffic metadata (newest first)",
		Tags:        []string{"traffic"},
	}, listTrafficHandler(deps.Traffic, deps.Sessions))

	huma.Register(api, huma.Operation{
		OperationID: "get-traffic",
		Method:      http.MethodGet,
		Path:        "/v1/traffic/{id}",
		Summary:     "Fetch a single captured entry by id (full bodies + analysis)",
		Tags:        []string{"traffic"},
	}, getTrafficHandler(deps.Traffic))

	huma.Register(api, huma.Operation{
		OperationID: "tail-traffic",
		Method:      http.MethodGet,
		Path:        "/v1/traffic/{id}/tail",
		Summary:     "Poll appended response bytes for a streaming entry",
		Tags:        []string{"traffic"},
	}, tailTrafficHandler(deps.Traffic))

	huma.Register(api, huma.Operation{
		OperationID: "clear-traffic",
		Method:      http.MethodDelete,
		Path:        "/v1/traffic",
		Summary:     "Discard every captured entry, session label, and breakpoint.",
		Description: "Wipes the in-memory ring buffer, the on-disk JSONL log, " +
			"the session aggregator's persisted name map, and the active " +
			"breakpoint set. Held requests are released as continue.",
		Tags: []string{"traffic"},
		// 204 keeps the response side trivially typed in the TS client.
		DefaultStatus: http.StatusNoContent,
	}, clearTrafficHandler(deps.Traffic, deps.Sessions, deps.Breakpoints))

	registerTrafficStream(api, deps.Traffic, deps.Sessions, deps.Breakpoints)
	registerSessions(api, deps.Sessions)
	registerReplay(api, deps.Replay)
	registerBreakpoints(api, deps.Breakpoints)
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
	SessionID       string             `json:"sessionId,omitempty"`
	ReplayedFromID  string             `json:"replayedFromId,omitempty"`
	Analysis        *llmparse.Analysis `json:"analysis,omitempty"`
}

// EntryMeta is the lightweight projection of a captured entry used by the
// traffic LIST endpoint and the index SSE stream. It deliberately omits the
// request/response bodies and the full analysis tree: those are large and
// growing during a stream, and re-sending them per chunk to every renderer is
// the O(n^2) amplifier the refactor removes (design §1.2). The renderer pulls
// full bodies on demand via GET /v1/traffic/{id} and live-updates the one
// selected streaming entry via GET /v1/traffic/{id}/tail. The few summary
// fields the sidebar/list need (model, endpoint, token tallies, structured
// flag) are derived here so the renderer never has to parse a body to render
// the list.
type EntryMeta struct {
	ID             string    `json:"id"`
	StartedAt      time.Time `json:"startedAt"`
	EndedAt        time.Time `json:"endedAt"`
	DurationMillis int64     `json:"durationMillis"`
	Method         string    `json:"method"`
	URL            string    `json:"url"`
	UpstreamURL    string    `json:"upstreamUrl"`
	StatusCode     int       `json:"statusCode"`
	RequestSize    int64     `json:"requestSize"`
	ResponseSize   int64     `json:"responseSize"`
	Streaming      bool      `json:"streaming"`
	Truncated      bool      `json:"truncated"`
	Error          string    `json:"error,omitempty"`
	SessionID      string    `json:"sessionId,omitempty"`
	ReplayedFromID string    `json:"replayedFromId,omitempty"`
	// Derived summary fields (so the list never parses a body):
	Model         string `json:"model,omitempty" doc:"Model from the request/response analysis."`
	Endpoint      string `json:"endpoint,omitempty" doc:"Human-readable endpoint label for grouping."`
	HasStructured bool   `json:"hasStructured" doc:"True when a structured Anthropic analysis is available."`
	IsUtility     bool   `json:"isUtility" doc:"Sub-task call (title-gen/tool summary); excluded from turn counts."`
	// HasResponseError flags an upstream/API error carried in the response body
	// (e.g. an Anthropic error envelope on a streamed HTTP 200). Entry.Error
	// covers only transport/proxy failures, so the list/Problems view needs this
	// to surface body-level errors without fetching the full entry.
	HasResponseError bool `json:"hasResponseError"`
	// WarningCount is the number of soft parser warnings on the analysis, so the
	// Problems view can flag entries worth inspecting without the full body.
	WarningCount int `json:"warningCount"`
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	CacheRead    int `json:"cacheRead"`
	CacheCreate  int `json:"cacheCreate"`
}

func toMeta(e *store.Entry) EntryMeta {
	m := EntryMeta{
		ID:             e.ID,
		StartedAt:      e.StartedAt,
		EndedAt:        e.EndedAt,
		DurationMillis: e.DurationMillis,
		Method:         e.Method,
		URL:            e.URL,
		UpstreamURL:    e.UpstreamURL,
		StatusCode:     e.StatusCode,
		RequestSize:    e.RequestSize,
		ResponseSize:   e.ResponseSize,
		Streaming:      e.Streaming,
		Truncated:      e.Truncated,
		Error:          e.Error,
		SessionID:      e.SessionID,
		ReplayedFromID: e.ReplayedFromID,
	}
	if a, ok := e.Analysis.(*llmparse.Analysis); ok && a != nil {
		m.Endpoint = a.Endpoint
		m.WarningCount = len(a.Warnings)
		m.IsUtility = llmparse.IsUtilityRequest(a)
		if a.Anthropic != nil {
			m.HasStructured = true
			if a.Anthropic.Request != nil {
				m.Model = a.Anthropic.Request.Model
			}
			if r := a.Anthropic.Response; r != nil {
				if r.Model != "" {
					m.Model = r.Model
				}
				if r.Error != nil {
					m.HasResponseError = true
				}
				if u := r.Usage; u != nil {
					m.InputTokens = u.InputTokens
					m.OutputTokens = u.OutputTokens
					m.CacheRead = u.CacheReadInputTokens
					m.CacheCreate = u.CacheCreationInputTokens
				}
			}
		}
	}
	return m
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
		SessionID:       e.SessionID,
		ReplayedFromID:  e.ReplayedFromID,
	}
	if a, ok := e.Analysis.(*llmparse.Analysis); ok {
		out.Analysis = a
	}
	return out
}

type ListTrafficOutput struct {
	Body struct {
		Items []EntryMeta `json:"items"`
	}
}

// listTrafficHandler returns lightweight EntryMeta, not full TrafficEntry: the
// list/sidebar never needs bodies, and projecting here keeps the index path
// O(entries) in metadata instead of O(total body bytes). Full bodies are
// fetched per entry via GET /v1/traffic/{id}.
func listTrafficHandler(st *store.Store, agg *session.Aggregator) func(context.Context, *struct{}) (*ListTrafficOutput, error) {
	return func(_ context.Context, _ *struct{}) (*ListTrafficOutput, error) {
		entries := st.List()
		out := &ListTrafficOutput{}
		out.Body.Items = make([]EntryMeta, 0, len(entries))
		for _, e := range entries {
			m := toMeta(e)
			if agg != nil && m.SessionID == "" {
				m.SessionID = agg.SessionOf(e.ID)
			}
			out.Body.Items = append(out.Body.Items, m)
		}
		return out, nil
	}
}

// TrafficTailInput requests the bytes appended to a streaming entry's response
// since a client-held offset. The renderer polls this (~10Hz) only while the
// selected entry is still streaming, then stops once done=true. Offset
// bookkeeping is entirely client-side, so the store needs no per-subscriber
// state and there is no second long-lived connection.
type TrafficTailInput struct {
	ID    string `path:"id" maxLength:"64"`
	Since int64  `query:"since" minimum:"0" doc:"Byte offset already seen by the client."`
}

type TrafficTailOutput struct {
	Body struct {
		AppendBytes  string `json:"appendBytes" doc:"Response bytes after the requested offset."`
		ResponseSize int64  `json:"responseSize" doc:"Total response bytes captured so far."`
		Truncated    bool   `json:"truncated"`
		Done         bool   `json:"done" doc:"True once the entry has finalized; stop polling."`
		StatusCode   int    `json:"statusCode"`
	}
}

func tailTrafficHandler(st *store.Store) func(context.Context, *TrafficTailInput) (*TrafficTailOutput, error) {
	return func(_ context.Context, in *TrafficTailInput) (*TrafficTailOutput, error) {
		e, ok := st.Get(in.ID)
		if !ok {
			return nil, huma.Error404NotFound("no such traffic entry")
		}
		out := &TrafficTailOutput{}
		body := e.ResponseBody
		since := in.Since
		if since < 0 {
			since = 0
		}
		if since > int64(len(body)) {
			since = int64(len(body))
		}
		out.Body.AppendBytes = body[since:]
		out.Body.ResponseSize = int64(len(body))
		out.Body.Truncated = e.Truncated
		out.Body.Done = !e.EndedAt.IsZero()
		out.Body.StatusCode = e.StatusCode
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

func clearTrafficHandler(st *store.Store, agg *session.Aggregator, bp BreakpointController) func(context.Context, *struct{}) (*struct{}, error) {
	return func(_ context.Context, _ *struct{}) (*struct{}, error) {
		// Order: traffic first (source of truth for entries), then sessions
		// (their entryIds become stale once entries are gone), then
		// breakpoints (independent, but resetting them together matches the
		// "clear everything" UX the renderer offers).
		st.Clear()
		if agg != nil {
			if err := agg.Clear(); err != nil {
				return nil, huma.Error500InternalServerError("clear sessions: " + err.Error())
			}
		}
		if bp != nil {
			bp.Clear()
		}
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

func registerTrafficStream(api huma.API, st *store.Store, agg *session.Aggregator, bp BreakpointController) {
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
				writeTrafficStream(streamCtx, st, agg, bp)
			},
		}, nil
	})
}

// Index-stream coalescing/reconcile cadence. flushInterval bounds how often a
// streaming entry's metadata is pushed (latest-wins), keeping the renderer to
// ~12 list updates/sec/entry instead of one per 32 KiB chunk. reconcileInterval
// backstops the store fan-out's drop-on-full: a finalize broadcast that was
// dropped under load is recovered here so an entry never stays "streaming"
// forever (the meta-only design removed the old polling fallback). The values
// are shared with the session aggregator (the two streams are tuned together).
const (
	flushInterval     = session.FlushInterval
	reconcileInterval = session.ReconcileInterval
)

// writeTrafficStream pushes events as the store/aggregator emit updates.
//
// The first event ("snapshot") replays the current buffer as EntryMeta (no
// bodies) so the renderer boots without a separate fetch. Live "entry" events
// are coalesced: non-terminal updates accumulate in a dirty set flushed every
// flushInterval (latest-wins), while terminal (finalized) updates are sent
// immediately and exactly once. A reconcile tick guarantees terminal delivery
// even if the store dropped a finalize broadcast on a full channel.
func writeTrafficStream(ctx huma.Context, st *store.Store, agg *session.Aggregator, bp BreakpointController) {
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

	metaOf := func(e *store.Entry) EntryMeta {
		m := toMeta(e)
		if agg != nil && m.SessionID == "" {
			m.SessionID = agg.SessionOf(e.ID)
		}
		return m
	}

	// Subscribe BEFORE snapshotting so an entry added in the gap between the two
	// queues in the channel buffer instead of being lost until finalize. The
	// renderer upserts by id, so the snapshot/subscribe overlap dedups for free.
	ch, cancel := st.Subscribe()
	defer cancel()

	// Initial snapshot — newest first to match the list endpoint.
	snap := make([]EntryMeta, 0)
	for _, e := range st.List() {
		snap = append(snap, metaOf(e))
	}
	if !send("snapshot", snap) {
		return
	}

	if agg != nil {
		sessions := make([]SessionSummaryBody, 0)
		for _, s := range agg.List() {
			sessions = append(sessions, toSessionBody(s))
		}
		if !send("sessions", sessions) {
			return
		}
	}

	var sessCh <-chan struct{}
	var sessCancel func()
	if agg != nil {
		sessCh, sessCancel = agg.Subscribe()
		defer sessCancel()
	}

	var bpCh <-chan struct{}
	var bpCancel func()
	if bp != nil {
		bpCh, bpCancel = bp.Subscribe()
		defer bpCancel()
		// Initial breakpoints snapshot for fast UI bootstrap.
		if !send("breakpoints", map[string]any{
			"items":  bp.List(),
			"paused": bp.PausedSnapshot(),
		}) {
			return
		}
	}

	// Coalescing state. dirty holds the latest snapshot of each in-flight
	// entry; sentTerminal records which entries we've already delivered as
	// finalized so the reconcile pass never double-sends.
	dirty := make(map[string]*store.Entry)
	sentTerminal := make(map[string]struct{})
	sendEntry := func(e *store.Entry) bool { return send("entry", metaOf(e)) }

	clientGone := ctx.Context().Done()
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	flush := time.NewTicker(flushInterval)
	defer flush.Stop()
	reconcile := time.NewTicker(reconcileInterval)
	defer reconcile.Stop()

	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return
			}
			if e.EndedAt.IsZero() {
				dirty[e.ID] = e // coalesce live updates; latest wins
				continue
			}
			// Terminal: deliver immediately and exactly once.
			delete(dirty, e.ID)
			if _, done := sentTerminal[e.ID]; done {
				continue
			}
			if !sendEntry(e) {
				return
			}
			sentTerminal[e.ID] = struct{}{}
		case <-flush.C:
			for id, e := range dirty {
				if !sendEntry(e) {
					return
				}
				delete(dirty, id)
			}
		case <-reconcile.C:
			// Backstop dropped finalize broadcasts and prune sentTerminal of
			// entries that have been evicted from the ring buffer.
			present := make(map[string]struct{})
			for _, e := range st.List() {
				present[e.ID] = struct{}{}
				if e.EndedAt.IsZero() {
					continue
				}
				if _, done := sentTerminal[e.ID]; done {
					continue
				}
				delete(dirty, e.ID)
				if !sendEntry(e) {
					return
				}
				sentTerminal[e.ID] = struct{}{}
			}
			for id := range sentTerminal {
				if _, ok := present[id]; !ok {
					delete(sentTerminal, id)
				}
			}
		case _, ok := <-sessCh:
			if !ok {
				sessCh = nil
				continue
			}
			sessions := make([]SessionSummaryBody, 0)
			for _, s := range agg.List() {
				sessions = append(sessions, toSessionBody(s))
			}
			if !send("sessions", sessions) {
				return
			}
		case _, ok := <-bpCh:
			if !ok {
				bpCh = nil
				continue
			}
			if !send("breakpoints", map[string]any{
				"items":  bp.List(),
				"paused": bp.PausedSnapshot(),
			}) {
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
