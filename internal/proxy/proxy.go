// Package proxy implements the reverse proxy that fronts the user's AI
// provider. The proxy listens on its own 127.0.0.1 port (separate from the
// API server) and forwards every request to the upstream baseURL configured
// via internal/config. Each request/response cycle is captured into
// internal/store so the UI can show it live.
//
// Streaming responses (Server-Sent Events, the wire format Anthropic / OpenAI
// use for token streaming) are passed through chunk-by-chunk: we write each
// flush to the client AND append it to the captured body so the user sees
// progress in real time without sacrificing the recorded transcript.
package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zerx-lab/ai-fox/internal/config"
	"github.com/zerx-lab/ai-fox/internal/llmparse"
	"github.com/zerx-lab/ai-fox/internal/store"
)

// Proxy is the HTTP handler that fronts the user's AI provider. Its lifecycle
// (binding a listener, serving, shutting down) is owned entirely by Controller
// — Proxy itself holds only the references it needs to service a request.
type Proxy struct {
	cfg         *config.Store
	store       *store.Store
	breakpoints *Registry
}

// newProxy builds the request handler. The listener and http.Server are owned
// by the controller; Proxy holds only the references it needs per request.
func newProxy(cfg *config.Store, st *store.Store, reg *Registry) (*Proxy, error) {
	if cfg == nil || st == nil {
		return nil, errors.New("proxy: config and store are required")
	}
	return &Proxy{cfg: cfg, store: st, breakpoints: reg}, nil
}

// ServeHTTP is the single proxy handler. The work is split into small private
// steps so each concern (URL build, body read, breakpoint, forward, stream)
// stays independently readable and testable.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	settings := p.cfg.Get()
	started := time.Now()

	entry := &store.Entry{
		ID:             p.store.NextID(),
		StartedAt:      started,
		Method:         r.Method,
		URL:            r.URL.RequestURI(),
		RequestHeaders: headerMap(r.Header),
	}

	target, ok := p.resolveTarget(w, r, entry, started, settings)
	if !ok {
		return
	}

	bodyBytes, ok := p.readRequest(w, r, entry, started)
	if !ok {
		return
	}

	if !p.awaitBreakpoint(w, r, entry, started) {
		return
	}

	resp, ok := p.forward(w, r, entry, started, target, bodyBytes, settings)
	if !ok {
		return
	}
	defer func() { _ = resp.Body.Close() }()

	p.streamResponse(w, r, entry, started, resp)
}

// resolveTarget validates the configured upstream and builds the target URL.
// It records the entry and writes the client error itself on failure, returning
// ok=false so the caller stops.
func (p *Proxy) resolveTarget(w http.ResponseWriter, r *http.Request, entry *store.Entry, started time.Time, settings config.Settings) (*url.URL, bool) {
	if settings.UpstreamBaseURL == "" {
		entry.Error = "no upstream baseURL configured"
		entry.EndedAt = time.Now()
		entry.DurationMillis = entry.EndedAt.Sub(started).Milliseconds()
		p.store.Add(entry)
		http.Error(w, "ai-fox: upstream baseURL not configured. Open Settings to set one.", http.StatusBadGateway)
		return nil, false
	}

	target, err := buildUpstreamURL(settings.UpstreamBaseURL, r.URL)
	if err != nil {
		entry.Error = "invalid upstream baseURL: " + err.Error()
		entry.EndedAt = time.Now()
		entry.DurationMillis = entry.EndedAt.Sub(started).Milliseconds()
		p.store.Add(entry)
		http.Error(w, "ai-fox: "+entry.Error, http.StatusBadGateway)
		return nil, false
	}
	entry.UpstreamURL = target.String()
	return target, true
}

// readRequest reads the full request body for forwarding and records a capture
// snapshot (truncated to store.MaxBodyBytes) on the entry. It persists the
// request snapshot immediately so the UI shows the request even before the
// upstream responds. Returns the FULL body bytes for forwarding.
func (p *Proxy) readRequest(w http.ResponseWriter, r *http.Request, entry *store.Entry, started time.Time) ([]byte, bool) {
	bodyBytes, err := readBody(r.Body)
	if err != nil {
		entry.Error = "read request body: " + err.Error()
		entry.EndedAt = time.Now()
		entry.DurationMillis = entry.EndedAt.Sub(started).Milliseconds()
		p.store.Add(entry)
		if errors.Is(err, errBodyTooLarge) {
			http.Error(w, "ai-fox: request body exceeds the proxy limit", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "ai-fox: "+entry.Error, http.StatusBadGateway)
		}
		return nil, false
	}
	// Capture is truncated to MaxBodyBytes; forwarding always uses the full body.
	captureBody := bodyBytes
	if len(captureBody) > store.MaxBodyBytes {
		captureBody = captureBody[:store.MaxBodyBytes]
		entry.Truncated = true
	}
	entry.RequestBody = string(captureBody)
	entry.RequestSize = int64(len(bodyBytes))
	p.store.Add(entry)
	return bodyBytes, true
}

// awaitBreakpoint blocks the request if a breakpoint matches, until the user
// resolves it or the client disconnects. Returns false (after writing the
// client response, if appropriate) when the request must not proceed upstream.
func (p *Proxy) awaitBreakpoint(w http.ResponseWriter, r *http.Request, entry *store.Entry, started time.Time) bool {
	if p.breakpoints == nil {
		return true
	}
	bp := p.breakpoints.Match(r)
	if bp == nil {
		return true
	}
	switch p.breakpoints.Hold(r.Context(), bp.ID, entry.ID, r.Method, r.URL.RequestURI()) {
	case DecisionAbort:
		p.store.Update(entry.ID, func(e *store.Entry) {
			e.Error = "aborted at breakpoint " + bp.ID
			e.EndedAt = time.Now()
			e.DurationMillis = e.EndedAt.Sub(started).Milliseconds()
		})
		http.Error(w, "ai-fox: aborted at breakpoint", http.StatusServiceUnavailable)
		return false
	case DecisionClientGone:
		// The client hung up while paused. Don't treat this as an abort and
		// don't write to the (already-closed) connection — mirror the
		// client-abort semantics of the streaming path.
		p.store.Update(entry.ID, func(e *store.Entry) {
			e.EndedAt = time.Now()
			e.DurationMillis = e.EndedAt.Sub(started).Milliseconds()
		})
		return false
	default:
		return true
	}
}

// forward builds and issues the upstream request with the FULL body. On failure
// it records the entry and writes the client error, returning ok=false.
func (p *Proxy) forward(w http.ResponseWriter, r *http.Request, entry *store.Entry, started time.Time, target *url.URL, bodyBytes []byte, settings config.Settings) (*http.Response, bool) {
	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		p.store.Update(entry.ID, func(e *store.Entry) {
			e.Error = "build upstream request: " + err.Error()
			e.EndedAt = time.Now()
			e.DurationMillis = e.EndedAt.Sub(started).Milliseconds()
		})
		http.Error(w, "ai-fox: "+err.Error(), http.StatusBadGateway)
		return nil, false
	}
	buildForwardHeaders(upstreamReq.Header, r.Header, target.Host, settings)
	upstreamReq.Host = target.Host

	resp, err := http.DefaultTransport.RoundTrip(upstreamReq)
	if err != nil {
		p.store.Update(entry.ID, func(e *store.Entry) {
			e.Error = "upstream: " + err.Error()
			e.EndedAt = time.Now()
			e.DurationMillis = e.EndedAt.Sub(started).Milliseconds()
		})
		http.Error(w, "ai-fox: upstream error: "+err.Error(), http.StatusBadGateway)
		return nil, false
	}
	return resp, true
}

// streamResponse writes the upstream response back to the client while
// capturing it, handling Content-Encoding normalization, then finalizes the
// entry with a full re-analysis.
func (p *Proxy) streamResponse(w http.ResponseWriter, r *http.Request, entry *store.Entry, started time.Time, resp *http.Response) {
	p.store.Update(entry.ID, func(e *store.Entry) {
		e.StatusCode = resp.StatusCode
		e.ResponseHeaders = headerMap(resp.Header)
		e.Streaming = isStreaming(resp.Header)
	})

	bodyReader, encodingWarning := decodeResponseBody(resp)

	// Set response headers on the client connection before any body write.
	copyHeaders(w.Header(), resp.Header, responseSkipHeaders(resp.Header))
	w.WriteHeader(resp.StatusCode)

	capture(r.Context(), bodyReader, entry.ID, p.store, w)

	p.store.Update(entry.ID, func(e *store.Entry) {
		e.EndedAt = time.Now()
		e.DurationMillis = e.EndedAt.Sub(started).Milliseconds()
		// Final pass overwrites whatever the in-stream analyses left behind,
		// so the recorded entry ends up with a clean, complete view.
		runAnalysis(e)
		if encodingWarning != "" {
			appendWarning(e, encodingWarning)
		}
	})
}

// runAnalysis re-parses the entry's current bodies and writes the structured
// view back into e.Analysis. Safe to call repeatedly during a stream — the
// analyzer is tolerant of partial SSE bodies and we always replace the prior
// snapshot rather than merge. Cheap on small bodies; for large ones the
// caller throttles by captured-size thresholds (see capture).
func runAnalysis(e *store.Entry) {
	analysis := llmparse.Analyze(llmparse.Input{
		Method:          e.Method,
		URL:             e.URL,
		RequestBody:     e.RequestBody,
		ResponseBody:    e.ResponseBody,
		ResponseHeaders: lowerKeys(e.ResponseHeaders),
		Streaming:       e.Streaming,
	})
	if analysis != nil {
		e.Analysis = analysis
	}
}

// lowerKeys returns a copy of h with lowercased keys, so analyzers can look
// up content-type without juggling Go's CanonicalHeaderKey casing.
func lowerKeys(h map[string]string) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[strings.ToLower(k)] = v
	}
	return out
}

// firstAnalyzeBytes is the captured-size threshold at which the SECOND in-stream
// analysis fires (the first fires on the first chunk). After that the threshold
// doubles each time — see capture.
const firstAnalyzeBytes = 64 * 1024

// flushSnapshotInterval bounds how often capture materializes the accumulated
// body into a fresh string for the store Update. Taking captured.String() on
// EVERY ~32 KiB chunk is an O(captured-size) copy, so a long stream pays
// O(size²/chunk) total — the streaming hot path's biggest amplifier. Throttling
// the snapshot to a wall-clock tick bounds the copies to O(size × duration /
// interval) of fixed-cost work while still giving the UI live progress (the
// per-entry tail polls at ~10 Hz, so a 150 ms snapshot cadence is below the
// renderer's own refresh rate). The final, authoritative snapshot always runs
// at EOF/error regardless of the timer.
const flushSnapshotInterval = 150 * time.Millisecond

// capture copies body into the store entry's ResponseBody and, when client is
// non-nil, simultaneously streams it back to the client. Each ~32 KiB chunk
// triggers a flush + store update so the UI sees streaming progress (raw bytes;
// the meta-only index stream + per-entry tail keep the renderer live). It is the
// single implementation shared by the live proxy path (client non-nil) and the
// replay path (client nil — there is no inbound connection to push to).
//
// Analyze is O(captured-size), so re-running it on a fixed time interval makes
// total in-stream parse work O(size × duration) — quadratic for long streams
// and a real CPU amplifier under concurrency. Instead we re-analyze on
// GEOMETRIC size thresholds (first chunk, then every time the captured body
// doubles): ~log2(size) analyses whose sizes form a geometric series, so total
// work is bounded to O(size) regardless of stream duration, while still filling
// the structured view progressively. The full, authoritative analysis always
// runs once more at finalize (see streamResponse / Replay).
//
// ctx is the inbound request's context. When it is cancelled (the client
// hung up — e.g. the user pressed Esc in opencode, or the chat window was
// closed) both the client Write and body.Read start failing. That is a normal
// termination, not an error: we record what we captured and return without
// setting `Entry.Error`, so the UI shows the entry as a completed stream
// instead of a red failure banner. Only genuine upstream/write failures
// (those that occur while ctx is still live) are reported as errors.
func capture(ctx context.Context, body io.Reader, entryID string, st *store.Store, client io.Writer) {
	flusher, _ := client.(http.Flusher)
	buf := make([]byte, 32*1024)
	var captured bytes.Buffer
	totalBytes := int64(0)
	truncated := false
	// Next captured-size threshold that triggers an analysis. 0 ⇒ the first
	// chunk always fires; thereafter it doubles, bounding total analysis work to
	// O(final size).
	var nextAnalyzeLen int
	// lastSnapshot throttles the O(captured-size) captured.String() copy + store
	// Update to flushSnapshotInterval. The final snapshot at EOF/error/abort is
	// unconditional, so nothing the client sees is lost — only the intermediate
	// progress ticks are coalesced.
	var lastSnapshot time.Time

	// finalize takes the authoritative snapshot and writes it once. setErr lets
	// the EOF/abort paths share the same closure while still recording a genuine
	// transport error (client-abort terminations stay error-free).
	finalize := func(setErr string) {
		snapshot := captured.String()
		st.Update(entryID, func(e *store.Entry) {
			if setErr != "" {
				e.Error = setErr
			}
			e.ResponseBody = snapshot
			e.ResponseSize = totalBytes
			if truncated {
				e.Truncated = true
			}
		})
	}

	for {
		n, err := body.Read(buf)
		if n > 0 {
			totalBytes += int64(n)
			if client != nil {
				if _, werr := client.Write(buf[:n]); werr != nil {
					setErr := ""
					if !isClientAbort(ctx, werr) {
						setErr = "write to client: " + werr.Error()
					}
					finalize(setErr)
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			remaining := int64(store.MaxBodyBytes) - int64(captured.Len())
			switch {
			case remaining <= 0:
				truncated = true
			case int64(n) > remaining:
				captured.Write(buf[:remaining])
				truncated = true
			default:
				captured.Write(buf[:n])
			}
			shouldAnalyze := captured.Len() >= nextAnalyzeLen
			if shouldAnalyze {
				// Schedule the next analysis at double the current size (with a
				// floor so the first few KiB don't fire on every chunk).
				nextAnalyzeLen = captured.Len() * 2
				if nextAnalyzeLen < firstAnalyzeBytes {
					nextAnalyzeLen = firstAnalyzeBytes
				}
			}
			// Snapshot for live UI progress, throttled to a wall-clock tick so we
			// don't pay an O(captured-size) string copy on every chunk. An analysis
			// tick forces a snapshot regardless (it needs the body to parse), so the
			// structured view still fills in on its geometric schedule.
			now := time.Now()
			if shouldAnalyze || now.Sub(lastSnapshot) >= flushSnapshotInterval {
				lastSnapshot = now
				snapshot := captured.String()
				st.Update(entryID, func(e *store.Entry) {
					e.ResponseBody = snapshot
					e.ResponseSize = totalBytes
					if truncated {
						e.Truncated = true
					}
					if shouldAnalyze {
						runAnalysis(e)
					}
				})
			}
		}
		if errors.Is(err, io.EOF) {
			finalize("")
			return
		}
		if err != nil {
			setErr := ""
			if !isClientAbort(ctx, err) {
				setErr = "read upstream: " + err.Error()
			}
			finalize(setErr)
			return
		}
	}
}

// isClientAbort returns true when err is the result of the inbound client
// disconnecting mid-stream (request context cancelled, or a low-level
// "broken pipe" / "connection reset" while writing back). These are
// expected terminations — they happen every time an LLM client (opencode,
// Claude Desktop, etc.) cancels generation — and should not surface as
// errors in the UI.
func isClientAbort(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return false
}

func buildUpstreamURL(base string, incoming *url.URL) (*url.URL, error) {
	baseURL, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return nil, err
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q", baseURL.Scheme)
	}
	target := *baseURL
	target.Path = singleSlashJoin(baseURL.Path, incoming.Path)
	target.RawQuery = mergeRawQuery(baseURL.RawQuery, incoming.RawQuery)
	return &target, nil
}

// mergeRawQuery combines the query string baked into the configured baseURL with
// the one on the incoming request. A gateway baseURL often carries its own
// params (e.g. ?api-version=… for Azure-style endpoints); the old code dropped
// them by overwriting RawQuery with the incoming query. We keep both, with the
// incoming request's params appended after the base's so a per-request value
// wins on the upstream's last-one-wins parsing while the base default is still
// present. Empty sides short-circuit so the common no-base-query case is
// unchanged.
func mergeRawQuery(base, incoming string) string {
	switch {
	case base == "":
		return incoming
	case incoming == "":
		return base
	default:
		return base + "&" + incoming
	}
}

func singleSlashJoin(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	case strings.HasSuffix(a, "/") && strings.HasPrefix(b, "/"):
		return a + strings.TrimPrefix(b, "/")
	case !strings.HasSuffix(a, "/") && !strings.HasPrefix(b, "/"):
		return a + "/" + b
	default:
		return a + b
	}
}

func headerMap(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = strings.Join(v, ", ")
	}
	return out
}

// hopByHopHeaders are the fixed hop-by-hop headers stripped per RFC 7230 §6.1.
// Plus a few proxy-specific ones (Host) we want to set ourselves. The dynamic
// list named in the message's own Connection header is added per-message by
// hopByHopSet.
var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Proxy-Connection":    {},
	"Keep-Alive":          {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Host":                {},
}

// hopByHopSet returns a fresh skip set: the fixed hop-by-hop headers plus every
// field name listed in this message's Connection header (RFC 7230 §6.1 requires
// those to be removed before forwarding). Returning a copy keeps the package
// var immutable across concurrent requests.
func hopByHopSet(h http.Header) map[string]struct{} {
	skip := make(map[string]struct{}, len(hopByHopHeaders)+2)
	for k := range hopByHopHeaders {
		skip[k] = struct{}{}
	}
	for _, v := range h.Values("Connection") {
		for _, tok := range strings.Split(v, ",") {
			tok = strings.TrimSpace(tok)
			if tok != "" {
				skip[http.CanonicalHeaderKey(tok)] = struct{}{}
			}
		}
	}
	return skip
}

func copyHeaders(dst, src http.Header, skip map[string]struct{}) {
	for k, vs := range src {
		if _, drop := skip[http.CanonicalHeaderKey(k)]; drop {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// buildForwardHeaders constructs the upstream request headers from the inbound
// client headers: copy everything except hop-by-hop (including the dynamic
// Connection-listed set), set Host to the upstream, force identity encoding so
// the captured body is readable, then inject the configured auth. This is the
// single "construct forwarding headers" path shared with replay.
func buildForwardHeaders(dst, src http.Header, host string, settings config.Settings) {
	copyHeaders(dst, src, hopByHopSet(src))
	dst.Set("Host", host)
	// Force identity encoding so the captured response body is readable text
	// instead of zstd/br/gzip bytes. Go's default transport only auto-decodes
	// gzip; brotli and zstd would pass through as binary garbage and break the
	// detail view. This mirrors how Charles / Fiddler / mitmproxy behave.
	dst.Set("Accept-Encoding", "identity")
	for _, kv := range PresetHeaders(settings) {
		if kv.Name == "" {
			continue
		}
		// anthropic-version is a default, not an override: when the client
		// (Claude Code, the SDK, …) already sent its own version header, pass it
		// through untouched. Only inject the preset default when it's absent.
		// anthropic-beta is copied verbatim by copyHeaders above (never in the
		// preset list), so client beta flags are always forwarded.
		if strings.EqualFold(kv.Name, "anthropic-version") && src.Get("anthropic-version") != "" {
			continue
		}
		dst.Set(kv.Name, kv.Value)
	}
}

// PresetHeaders returns the concrete list of headers to inject for a given
// settings snapshot. Exported so the UI can preview/explain what will be
// sent without re-implementing the rules.
func PresetHeaders(s config.Settings) []config.HeaderKV {
	switch s.AuthPreset {
	case config.PresetOpenAI, config.PresetOpenAIResponses:
		if s.UpstreamAPIKey == "" {
			return nil
		}
		return []config.HeaderKV{
			{Name: "Authorization", Value: "Bearer " + s.UpstreamAPIKey},
		}
	case config.PresetCustom:
		return s.CustomHeaders
	case config.PresetAnthropic:
		fallthrough
	default:
		if s.UpstreamAPIKey == "" {
			return nil
		}
		return []config.HeaderKV{
			{Name: "x-api-key", Value: s.UpstreamAPIKey},
			{Name: "anthropic-version", Value: "2023-06-01"},
		}
	}
}

func isStreaming(h http.Header) bool {
	ct := h.Get("Content-Type")
	return strings.HasPrefix(strings.ToLower(ct), "text/event-stream")
}

// maxForwardBodyBytes is a safety ceiling on the request body we buffer before
// forwarding. It is far above any legitimate LLM request (long context + base64
// images) yet bounds memory against a malicious/runaway client. Bodies over the
// limit are rejected with 413 rather than silently truncated — truncating and
// forwarding would corrupt the upstream request (see G1).
const maxForwardBodyBytes = 64 << 20 // 64 MiB

// errBodyTooLarge signals the request body exceeded maxForwardBodyBytes.
var errBodyTooLarge = errors.New("request body too large")

// readBody reads the COMPLETE request body so it can be forwarded upstream
// faithfully. The caller truncates a capture copy to store.MaxBodyBytes
// separately; forwarding must never use a truncated body or the upstream's
// Content-Length would mismatch the bytes sent.
func readBody(r io.ReadCloser) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	defer func() { _ = r.Close() }()
	// Read up to the forward ceiling +1 so we can detect an over-limit body.
	limited := io.LimitReader(r, maxForwardBodyBytes+1)
	buf, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(buf) > maxForwardBodyBytes {
		return nil, errBodyTooLarge
	}
	return buf, nil
}

// decodeResponseBody returns a reader over the response body that yields
// readable text. When the upstream ignored our `Accept-Encoding: identity` and
// returned gzip, we wrap it in a gzip.Reader and drop Content-Encoding /
// Content-Length from the client-bound headers (Go then chunks the response).
// Other encodings (br, zstd, …) are passed through untouched — we can't decode
// them here, so we leave the headers intact and return a warning to attach to
// the captured entry so the detail view explains the binary body.
func decodeResponseBody(resp *http.Response) (io.Reader, string) {
	enc := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	switch enc {
	case "", "identity":
		return resp.Body, ""
	case "gzip":
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			// Couldn't start a gzip stream: fall back to passthrough and warn.
			return resp.Body, "response declared Content-Encoding: gzip but could not be decoded: " + err.Error()
		}
		// Mark the headers so responseSkipHeaders strips Content-Encoding/Length.
		resp.Header.Set("X-Ai-Fox-Decoded", "gzip")
		return zr, ""
	default:
		return resp.Body, "response body is " + enc + "-encoded and shown as raw bytes (proxy cannot decode " + enc + ")"
	}
}

// responseSkipHeaders returns the set of headers to drop when copying the
// upstream response to the client. Always the hop-by-hop set; additionally
// Content-Encoding + Content-Length when decodeResponseBody decoded the body
// (the client must not see a stale encoding/length for the now-plain bytes).
func responseSkipHeaders(respHeader http.Header) map[string]struct{} {
	skip := hopByHopSet(respHeader)
	if respHeader.Get("X-Ai-Fox-Decoded") != "" {
		skip["Content-Encoding"] = struct{}{}
		skip["Content-Length"] = struct{}{}
		skip["X-Ai-Fox-Decoded"] = struct{}{}
	}
	return skip
}

// appendWarning attaches a soft warning to the entry's analysis, creating a
// minimal Analysis if none exists (e.g. an unrecognized endpoint). This is how
// non-fatal proxy-layer notes (a non-gzip compressed body) reach the UI without
// commandeering Entry.Error, which is reserved for transport failures.
func appendWarning(e *store.Entry, msg string) {
	a, ok := e.Analysis.(*llmparse.Analysis)
	if !ok || a == nil {
		e.Analysis = &llmparse.Analysis{Warnings: []string{msg}}
		return
	}
	a.Warnings = append(a.Warnings, msg)
}
