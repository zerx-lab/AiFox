package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
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
	ctrl, err := NewController(0, cfg, st)
	if err != nil {
		t.Fatalf("controller new: %v", err)
	}
	if err := ctrl.Start(); err != nil {
		t.Fatalf("controller start: %v", err)
	}
	t.Cleanup(ctrl.Close)
	return "http://" + ctrl.Address()
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

func TestAnthropicVersionPassthroughWhenClientSendsOne(t *testing.T) {
	var capturedVersion string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedVersion = r.Header.Get("anthropic-version")
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	cfg := newConfig(t, config.Settings{
		UpstreamBaseURL: upstream.URL,
		UpstreamAPIKey:  "sk-test",
		AuthPreset:      config.PresetAnthropic,
	})
	addr := startProxy(t, cfg, store.New(2))

	req, err := http.NewRequest(http.MethodPost, addr+"/v1/messages", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("anthropic-version", "2099-12-31")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_ = resp.Body.Close()
	if capturedVersion != "2099-12-31" {
		t.Fatalf("client anthropic-version must pass through, got %q", capturedVersion)
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

// TestStreamingAnalysisAppearsBeforeEnd guards the live-conversation behavior:
// the proxy must run llmparse on a partial body during the stream (the first
// chunk always triggers an analysis) so a structured view exists before EOF.
// In-stream re-analysis now fires on geometric size thresholds rather than a
// fixed time interval (see capture), but the first-chunk analysis that
// this test asserts is unchanged.
func TestStreamingAnalysisAppearsBeforeEnd(t *testing.T) {
	gate := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		f := w.(http.Flusher)
		// Enough SSE to produce a non-trivial partial Analysis: message_start
		// gives id/model, content_block_delta gives a streamed text block.
		first := strings.Join([]string{
			"event: message_start",
			`data: {"type":"message_start","message":{"id":"msg_live","model":"claude-test","role":"assistant","usage":{"input_tokens":1}}}`,
			"",
			"event: content_block_start",
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			"",
			"event: content_block_delta",
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
			"",
			"",
		}, "\n")
		_, _ = io.WriteString(w, first)
		f.Flush()
		<-gate
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer upstream.Close()

	st := store.New(4)
	cfg := newConfig(t, config.Settings{UpstreamBaseURL: upstream.URL})
	addr := startProxy(t, cfg, st)

	sub, unsub := st.Subscribe()
	defer unsub()

	done := make(chan struct{})
	go func() {
		resp, err := http.Post(addr+"/v1/messages", "application/json",
			strings.NewReader(`{"model":"claude-test","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		close(done)
	}()

	// Wait for an Update event that carries a non-nil Analysis while the
	// upstream is still blocked. The first Add fires before any chunk lands,
	// so we must consume events until Analysis is populated.
	var partial *store.Entry
	deadline := time.After(2 * time.Second)
loop:
	for {
		select {
		case e := <-sub:
			if e != nil && e.Analysis != nil {
				partial = e
				break loop
			}
		case <-deadline:
			break loop
		}
	}
	if partial == nil {
		close(gate)
		<-done
		t.Fatalf("expected an Analysis update during stream, got none")
	}
	if !partial.EndedAt.IsZero() {
		close(gate)
		<-done
		t.Fatalf("partial entry should not have EndedAt set yet, got %v", partial.EndedAt)
	}

	// Let the upstream finish so the goroutine can return cleanly.
	close(gate)
	<-done
}

// TestClientCancelDuringStreamIsNotAnError guards the "normal termination"
// path: when the inbound client hangs up mid-stream (opencode pressing
// Esc, chat window closed, parent process killed, etc.) the request
// context cancels and `body.Read` returns context.Canceled. That is not a
// proxy/upstream failure and must not surface as a red error banner in
// the UI — we keep whatever was captured and leave Entry.Error empty.
func TestClientCancelDuringStreamIsNotAnError(t *testing.T) {
	upstreamDone := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		f := w.(http.Flusher)
		// Emit one chunk so the client has something to read, then block
		// until the inbound request is cancelled. Once cancelled, RoundTrip
		// closes the upstream connection and our handler returns.
		_, _ = io.WriteString(w, "event: ping\ndata: 1\n\n")
		f.Flush()
		<-r.Context().Done()
		close(upstreamDone)
	}))
	defer upstream.Close()

	st := store.New(4)
	cfg := newConfig(t, config.Settings{UpstreamBaseURL: upstream.URL})
	addr := startProxy(t, cfg, st)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr+"/stream", nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	// Read the first chunk so we know the stream is established, then
	// cancel from the client side.
	buf := make([]byte, 64)
	if _, err := resp.Body.Read(buf); err != nil {
		t.Fatalf("read first chunk: %v", err)
	}
	cancel()
	_ = resp.Body.Close()

	select {
	case <-upstreamDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("upstream handler did not observe cancellation")
	}

	// Give the proxy a moment to finalize the entry after the goroutine
	// servicing the cancelled request unwinds.
	deadline := time.Now().Add(time.Second)
	var entry *store.Entry
	for time.Now().Before(deadline) {
		entries := st.List()
		if len(entries) == 1 && !entries[0].EndedAt.IsZero() {
			entry = entries[0]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if entry == nil {
		t.Fatalf("entry was not finalized after client cancel")
	}
	if entry.Error != "" {
		t.Fatalf("client cancel must not be recorded as an error, got %q", entry.Error)
	}
	if !strings.Contains(entry.ResponseBody, "data: 1") {
		t.Fatalf("partial response should still be captured, got %q", entry.ResponseBody)
	}
}

func TestControllerStopFreesPortAndRebindsOnStart(t *testing.T) {
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

	// Listener is closed — request errors at the TCP level, not 503.
	client := http.Client{Timeout: time.Second}
	if _, err := client.Get("http://" + addr + "/ping"); err == nil {
		t.Fatalf("stopped proxy should refuse connections")
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

func TestControllerSetPortRebinds(t *testing.T) {
	cfg := newConfig(t, config.Settings{})
	ctrl, err := NewController(0, cfg, store.New(2))
	if err != nil {
		t.Fatalf("controller new: %v", err)
	}
	t.Cleanup(ctrl.Close)
	if err := ctrl.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	first := ctrl.Port()

	// Pick a different free port by binding+immediately closing.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	target := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	if err := ctrl.SetPort(target); err != nil {
		t.Fatalf("set port: %v", err)
	}
	if ctrl.Port() != target {
		t.Fatalf("port should now be %d, got %d", target, ctrl.Port())
	}
	if ctrl.Port() == first {
		t.Fatalf("port should have changed")
	}
	if !ctrl.Enabled() {
		t.Fatalf("controller should still be running after port change")
	}
}

// TestLargeRequestBodyForwardedInFull guards G1: a >1MiB request body must be
// forwarded to the upstream in full (the capture copy is truncated separately).
// The upstream asserts it received every byte and a matching Content-Length.
func TestLargeRequestBodyForwardedInFull(t *testing.T) {
	const size = (1 << 20) + 4096 // just over store.MaxBodyBytes
	payload := strings.Repeat("a", size)

	type recv struct {
		n             int
		contentLength int64
		match         bool
	}
	got := make(chan recv, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- recv{n: len(b), contentLength: r.ContentLength, match: string(b) == payload}
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	st := store.New(4)
	cfg := newConfig(t, config.Settings{UpstreamBaseURL: upstream.URL})
	addr := startProxy(t, cfg, st)

	resp, err := http.Post(addr+"/v1/messages", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_ = resp.Body.Close()

	select {
	case r := <-got:
		if r.n != size {
			t.Fatalf("upstream received %d bytes, want full %d", r.n, size)
		}
		if !r.match {
			t.Fatalf("forwarded body does not match original")
		}
		if r.contentLength != int64(size) {
			t.Fatalf("Content-Length %d != body size %d", r.contentLength, size)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream never received the request")
	}

	// The captured copy is truncated and flagged, but forwarding stayed whole.
	time.Sleep(50 * time.Millisecond)
	entries := st.List()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if !entries[0].Truncated {
		t.Fatalf("capture copy of an over-limit body should be flagged Truncated")
	}
	if entries[0].RequestSize != int64(size) {
		t.Fatalf("RequestSize should reflect the full body size %d, got %d", size, entries[0].RequestSize)
	}
}

// TestGzipResponseDecoded guards G2: an upstream that ignores identity and
// returns gzip is decoded by the proxy; the client sees plain text and the
// Content-Encoding / Content-Length headers are removed.
func TestGzipResponseDecoded(t *testing.T) {
	const plain = "hello gzipped world"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write([]byte(plain))
		_ = zw.Close()
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
		w.WriteHeader(200)
		_, _ = w.Write(buf.Bytes())
	}))
	defer upstream.Close()

	st := store.New(4)
	cfg := newConfig(t, config.Settings{UpstreamBaseURL: upstream.URL})
	addr := startProxy(t, cfg, st)

	resp, err := http.Get(addr + "/v1/models")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if string(body) != plain {
		t.Fatalf("client should receive decoded body, got %q", body)
	}
	if ce := resp.Header.Get("Content-Encoding"); ce != "" {
		t.Fatalf("Content-Encoding should be stripped after decode, got %q", ce)
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		t.Fatalf("Content-Length should be stripped after decode, got %q", cl)
	}

	time.Sleep(50 * time.Millisecond)
	entries := st.List()
	if len(entries) != 1 || !strings.Contains(entries[0].ResponseBody, plain) {
		t.Fatalf("captured body should be decoded text, got %+v", entries)
	}
}

// TestConnectionHeaderHopByHopStripped guards the RFC 7230 §6.1 upgrade: a
// header named in the request's Connection header must not be forwarded.
func TestConnectionHeaderHopByHopStripped(t *testing.T) {
	type recv struct{ xFoo, connection string }
	got := make(chan recv, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- recv{xFoo: r.Header.Get("X-Foo"), connection: r.Header.Get("Connection")}
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	cfg := newConfig(t, config.Settings{UpstreamBaseURL: upstream.URL})
	addr := startProxy(t, cfg, store.New(2))

	req, _ := http.NewRequest(http.MethodGet, addr+"/v1/models", nil)
	req.Header.Set("X-Foo", "secret")
	req.Header.Set("Connection", "X-Foo")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_ = resp.Body.Close()

	select {
	case r := <-got:
		if r.xFoo != "" {
			t.Fatalf("X-Foo named in Connection must be stripped, got %q", r.xFoo)
		}
		if r.connection != "" {
			t.Fatalf("Connection header must be stripped, got %q", r.connection)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream never received the request")
	}
}
