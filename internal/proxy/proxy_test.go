package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zerx-lab/ai-fox/internal/config"
	"github.com/zerx-lab/ai-fox/internal/store"
)

func newConfig(t *testing.T, settings config.Settings) *config.Store {
	t.Helper()
	cfg, err := config.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatalf("config open: %v", err)
	}
	if err := cfg.Set(settings); err != nil {
		t.Fatalf("config set: %v", err)
	}
	return cfg
}

func startProxy(t *testing.T, cfg *config.Store, st *store.Store) string {
	t.Helper()
	p, err := New(0, cfg, st)
	if err != nil {
		t.Fatalf("proxy new: %v", err)
	}
	t.Cleanup(p.Close)
	go func() { _ = p.Serve() }()
	return "http://" + p.Addr().String()
}

func TestAnthropicPresetInjectsXApiKey(t *testing.T) {
	var capturedKey, capturedVersion, capturedPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedKey = r.Header.Get("x-api-key")
		capturedVersion = r.Header.Get("anthropic-version")
		capturedPath = r.URL.Path
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	st := store.New(8)
	cfg := newConfig(t, config.Settings{
		UpstreamBaseURL: upstream.URL,
		UpstreamAPIKey:  "sk-test",
		AuthPreset:      config.PresetAnthropic,
	})

	addr := startProxy(t, cfg, st)
	resp, err := http.Post(addr+"/v1/messages", "application/json", strings.NewReader(`{"hi":1}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" || resp.StatusCode != 200 {
		t.Fatalf("unexpected response %d %q", resp.StatusCode, body)
	}
	if capturedKey != "sk-test" {
		t.Fatalf("anthropic preset must inject x-api-key, got %q", capturedKey)
	}
	if capturedVersion != "2023-06-01" {
		t.Fatalf("anthropic preset must set anthropic-version, got %q", capturedVersion)
	}
	if capturedPath != "/v1/messages" {
		t.Fatalf("path not forwarded, got %q", capturedPath)
	}

	time.Sleep(50 * time.Millisecond)
	entries := st.List()
	if len(entries) != 1 {
		t.Fatalf("want 1 captured entry, got %d", len(entries))
	}
	e := entries[0]
	if e.StatusCode != 200 || e.RequestBody != `{"hi":1}` || e.ResponseBody != "ok" {
		t.Fatalf("entry not captured correctly: %+v", e)
	}
}

func TestOpenAIPresetInjectsBearer(t *testing.T) {
	var captured string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	cfg := newConfig(t, config.Settings{
		UpstreamBaseURL: upstream.URL,
		UpstreamAPIKey:  "sk-openai",
		AuthPreset:      config.PresetOpenAI,
	})
	addr := startProxy(t, cfg, store.New(2))
	resp, err := http.Post(addr+"/v1/chat/completions", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_ = resp.Body.Close()
	if captured != "Bearer sk-openai" {
		t.Fatalf("expected Bearer header, got %q", captured)
	}
}

func TestCustomPresetUsesArbitraryHeaders(t *testing.T) {
	var trace, secret, defaultKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace = r.Header.Get("X-Trace")
		secret = r.Header.Get("X-Secret")
		defaultKey = r.Header.Get("x-api-key")
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	cfg := newConfig(t, config.Settings{
		UpstreamBaseURL: upstream.URL,
		UpstreamAPIKey:  "sk-ignored-in-custom",
		AuthPreset:      config.PresetCustom,
		CustomHeaders: []config.HeaderKV{
			{Name: "X-Trace", Value: "abc"},
			{Name: "X-Secret", Value: "shhh"},
		},
	})
	addr := startProxy(t, cfg, store.New(2))
	resp, err := http.Get(addr + "/v1/anything")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = resp.Body.Close()
	if trace != "abc" || secret != "shhh" {
		t.Fatalf("custom headers not injected (trace=%q secret=%q)", trace, secret)
	}
	if defaultKey != "" {
		t.Fatalf("custom preset must not inject x-api-key, got %q", defaultKey)
	}
}

func TestStripsAcceptEncodingToForceIdentity(t *testing.T) {
	var captured string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("Accept-Encoding")
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	cfg := newConfig(t, config.Settings{UpstreamBaseURL: upstream.URL})
	addr := startProxy(t, cfg, store.New(2))

	req, err := http.NewRequest(http.MethodGet, addr+"/v1/models", nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Header.Set("Accept-Encoding", "gzip, br, zstd")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_ = resp.Body.Close()
	if captured != "identity" {
		t.Fatalf("upstream should have seen identity encoding, got %q", captured)
	}
}

func TestMissingUpstreamReturns502(t *testing.T) {
	st := store.New(8)
	cfg := newConfig(t, config.Settings{})

	addr := startProxy(t, cfg, st)
	resp, err := http.Get(addr + "/anything")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", resp.StatusCode)
	}
	time.Sleep(50 * time.Millisecond)
	if len(st.List()) != 1 {
		t.Fatalf("entry should still be recorded on misconfig")
	}
}

func TestStreamingResponseIsCaptured(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		f := w.(http.Flusher)
		for _, ev := range []string{"event: a\ndata: 1\n\n", "event: b\ndata: 2\n\n"} {
			_, _ = io.WriteString(w, ev)
			f.Flush()
		}
	}))
	defer upstream.Close()

	st := store.New(4)
	cfg := newConfig(t, config.Settings{UpstreamBaseURL: upstream.URL})

	addr := startProxy(t, cfg, st)
	resp, err := http.Get(addr + "/stream")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if !strings.Contains(string(body), "data: 1") || !strings.Contains(string(body), "data: 2") {
		t.Fatalf("stream content lost: %q", body)
	}

	time.Sleep(50 * time.Millisecond)
	entries := st.List()
	if len(entries) != 1 || !entries[0].Streaming {
		t.Fatalf("streaming flag not set on entry: %+v", entries)
	}
	if !strings.Contains(entries[0].ResponseBody, "data: 2") {
		t.Fatalf("captured response missing later chunk: %q", entries[0].ResponseBody)
	}
}

func TestControllerStopReturns503AndKeepsAddress(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	cfg := newConfig(t, config.Settings{UpstreamBaseURL: upstream.URL})
	st := store.New(4)
	ctrl, err := NewController(0, cfg, st)
	if err != nil {
		t.Fatalf("controller new: %v", err)
	}
	t.Cleanup(ctrl.Close)
	if err := ctrl.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	addr := ctrl.Address()
	if addr == "" || !ctrl.Enabled() {
		t.Fatalf("controller should be running, addr=%q", addr)
	}

	resp, err := http.Get("http://" + addr + "/ping")
	if err != nil {
		t.Fatalf("get while enabled: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("enabled proxy should forward, got %d", resp.StatusCode)
	}

	ctrl.Stop()
	if ctrl.Enabled() {
		t.Fatalf("controller should be stopped")
	}
	if ctrl.Address() != addr {
		t.Fatalf("stopped address should remain stable, got %q vs %q", ctrl.Address(), addr)
	}

	resp2, err := http.Get("http://" + addr + "/ping")
	if err != nil {
		t.Fatalf("get while disabled: %v", err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("disabled proxy should return 503, got %d", resp2.StatusCode)
	}

	if err := ctrl.Start(); err != nil {
		t.Fatalf("re-start: %v", err)
	}
	resp3, err := http.Get("http://" + addr + "/ping")
	if err != nil {
		t.Fatalf("get after re-start: %v", err)
	}
	_ = resp3.Body.Close()
	if resp3.StatusCode != 200 {
		t.Fatalf("re-started proxy should forward, got %d", resp3.StatusCode)
	}
}
