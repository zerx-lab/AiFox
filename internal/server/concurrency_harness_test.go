// Concurrency measurement harness (design doc §6 L1).
//
// Purpose: reproduce the multi-session streaming load that makes ai-fox lag,
// and capture before/after metrics for the review-fix loop:
//
//   - bytes pushed over /v1/traffic/stream per response (the O(n^2) amplifier)
//   - goroutine count under load (per-chunk goroutine churn)
//   - that every entry reaches a terminal state and every session settles
//     (no lost-finalize) — this is the correctness gate the coalescing /
//     guaranteed-delivery work must not regress.
//
// Run as a normal test (correctness + perf gate, race-clean):
//
//	go test ./internal/server -run TestConcurrencyHarness -race -v
//
// Post-L2 the gates are always on: index-stream amplification must stay under
// 2x (meta-only + coalescing removed the per-chunk full-body re-send) and the
// system must converge — every entry terminal, every session settled, one
// session per distinct conversation (no entry lost to a dropped broadcast).
// The -v output logs the amplification ratio and goroutine delta for the
// before/after record in docs/harness-baseline.md.
package server_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zerx-lab/ai-fox/internal/config"
	"github.com/zerx-lab/ai-fox/internal/llmparse"
	"github.com/zerx-lab/ai-fox/internal/proxy"
	"github.com/zerx-lab/ai-fox/internal/server"
	"github.com/zerx-lab/ai-fox/internal/session"
	"github.com/zerx-lab/ai-fox/internal/store"
)

const (
	harnessConcurrency = 8  // simultaneous sessions through the proxy
	harnessChunks      = 60 // SSE events per upstream response
	harnessChunkBytes  = 4096
)

// mockUpstream streams a long Anthropic-style SSE response so the proxy's
// capture path runs many in-stream Update cycles per request.
func mockUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain the request body so the client's write side completes.
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		// message_start carries the model so llmparse can build a response view.
		_, _ = fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_x\",\"model\":\"claude-opus-4-8\",\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		filler := strings.Repeat("x", harnessChunkBytes)
		for i := 0; i < harnessChunks; i++ {
			_, _ = fmt.Fprintf(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"%s\"}}\n\n", filler)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(time.Millisecond)
		}
		_, _ = fmt.Fprintf(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":%d}}\n\n", harnessChunks)
		_, _ = fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
}

// anthropicBody builds a minimal /v1/messages request that the analyzer will
// normalize, so the session aggregator buckets it.
func anthropicBody(userText string) string {
	return `{"model":"claude-opus-4-8","max_tokens":1024,"stream":true,` +
		`"system":[{"type":"text","text":"You are OpenCode."}],` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"` + userText + `"}]}]}`
}

func TestConcurrencyHarness(t *testing.T) {
	upstream := mockUpstream(t)
	defer upstream.Close()

	dir := t.TempDir()
	cfg, err := config.Open(dir + "/settings.json")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if err := cfg.Set(config.Settings{
		UpstreamBaseURL: upstream.URL,
		AuthPreset:      config.PresetAnthropic,
		UpstreamAPIKey:  "test-key",
	}); err != nil {
		t.Fatalf("set config: %v", err)
	}

	traffic := store.New(500)
	agg := session.New(traffic, "")
	stopAgg := agg.Start()
	defer stopAgg()

	ctrl, err := proxy.NewController(0, cfg, traffic)
	if err != nil {
		t.Fatalf("controller: %v", err)
	}
	if err := ctrl.Start(); err != nil {
		t.Fatalf("proxy start: %v", err)
	}
	defer ctrl.Close()
	proxyURL := "http://" + ctrl.Address()

	built, err := server.Build(server.Config{
		Token:    "harness-token",
		Settings: cfg,
		Traffic:  traffic,
		Proxy:    ctrl,
		Sessions: agg,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	srv := &http.Server{Handler: built.Handler}
	go func() { _ = srv.Serve(built.Listener) }()
	defer func() { _ = srv.Shutdown(context.Background()) }()
	apiURL := "http://" + built.Listener.Addr().String()

	// --- SSE stream consumer: count every byte the index stream pushes. ---
	var streamBytes atomic.Int64
	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()
	streamReady := make(chan struct{})
	go func() {
		req, _ := http.NewRequestWithContext(streamCtx, http.MethodGet, apiURL+"/v1/traffic/stream", nil)
		req.Header.Set(server.AuthHeader, "harness-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			close(streamReady)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		close(streamReady)
		buf := make([]byte, 32*1024)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				streamBytes.Add(int64(n))
			}
			if err != nil {
				return
			}
		}
	}()
	<-streamReady
	// Let the snapshot/sessions/breakpoints preamble settle so it isn't counted
	// against per-request amplification.
	time.Sleep(50 * time.Millisecond)
	preambleBytes := streamBytes.Load()

	gAtStart := runtime.NumGoroutine()

	// --- Fire N concurrent sessions through the proxy. ---
	start := time.Now()
	var wg sync.WaitGroup
	var reqErrors atomic.Int64
	for i := 0; i < harnessConcurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := anthropicBody(fmt.Sprintf("session %d hello", i))
			req, _ := http.NewRequest(http.MethodPost, proxyURL+"/v1/messages", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Session-Affinity", "ses_harness_"+strconv.Itoa(i))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				reqErrors.Add(1)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)
	if reqErrors.Load() > 0 {
		t.Fatalf("%d proxy requests errored", reqErrors.Load())
	}

	// Poll until the system converges: every entry terminal, every session
	// settled, and one session per distinct conversation. The aggregator's
	// reconcile guarantees convergence within ~1s even if a bucketing broadcast
	// was dropped; we poll (deadline 3s) instead of asserting on a fixed sleep
	// so the gate is deterministic, not timing-flaky.
	converged := false
	deadline := time.Now().Add(3 * time.Second)
	var sessions []*session.Summary
	for time.Now().Before(deadline) {
		allTerminal := true
		for _, e := range traffic.List() {
			if e.EndedAt.IsZero() {
				allTerminal = false
				break
			}
		}
		sessions = agg.List()
		settled := true
		for _, s := range sessions {
			if s.Status == "active" || s.HasUnfinished {
				settled = false
				break
			}
		}
		if allTerminal && settled && len(sessions) == harnessConcurrency {
			converged = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	gAfter := runtime.NumGoroutine()

	entries := traffic.List()
	if len(entries) != harnessConcurrency {
		t.Fatalf("expected %d entries, got %d", harnessConcurrency, len(entries))
	}

	// --- Metrics ---
	perRequestResponseBytes := int64(harnessChunks*harnessChunkBytes + 512) // approx
	totalResponseBytes := perRequestResponseBytes * harnessConcurrency
	streamPayload := streamBytes.Load() - preambleBytes
	amplification := float64(streamPayload) / float64(totalResponseBytes)

	t.Logf("=== concurrency harness metrics ===")
	t.Logf("concurrency=%d chunks/resp=%d chunkBytes=%d", harnessConcurrency, harnessChunks, harnessChunkBytes)
	t.Logf("wall(requests)=%s converged=%v sessions=%d", elapsed, converged, len(sessions))
	t.Logf("upstream response bytes (all reqs) ~= %d", totalResponseBytes)
	t.Logf("index-stream payload bytes          = %d", streamPayload)
	t.Logf("AMPLIFICATION (stream/response)      = %.3fx  (post-L2 target: well under 2x)", amplification)
	t.Logf("goroutines: start=%d after=%d delta=%d", gAtStart, gAfter, gAfter-gAtStart)

	// --- Correctness + perf gates (always on, post-L2). ---
	if !converged {
		// On failure, surface which entries are unsessioned so a regression is
		// diagnosable from the test output alone.
		inSession := map[string]string{}
		for _, s := range sessions {
			for _, id := range s.EntryIDs {
				inSession[id] = s.ID
			}
		}
		for _, e := range entries {
			if e.EndedAt.IsZero() {
				t.Errorf("entry %s never reached terminal state (EndedAt zero)", e.ID)
			}
			if _, ok := inSession[e.ID]; !ok {
				hasNorm := false
				if a, ok := e.Analysis.(*llmparse.Analysis); ok && a != nil {
					hasNorm = a.Normalized != nil
				}
				t.Logf("UNSESSIONED entry %s: ended=%v hasNorm=%v", e.ID, !e.EndedAt.IsZero(), hasNorm)
			}
		}
		for _, s := range sessions {
			if s.Status == "active" || s.HasUnfinished {
				t.Errorf("session %s stuck active/unfinished after all requests done", s.ID)
			}
		}
		t.Errorf("system did not converge: got %d sessions, want %d (all-terminal + settled)", len(sessions), harnessConcurrency)
	}
	if amplification > 2.0 {
		t.Errorf("index-stream amplification %.3fx exceeds 2x — body still re-sent per chunk", amplification)
	}
}
