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
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/zerx-lab/ai-fox/internal/config"
	"github.com/zerx-lab/ai-fox/internal/store"
)

// Proxy is the runtime state: listener + HTTP server + config/store refs.
type Proxy struct {
	listener net.Listener
	server   *http.Server
	cfg      *config.Store
	store    *store.Store

	mu     sync.Mutex
	closed bool
}

// New binds a loopback listener on `port` (0 = OS picks) and returns a Proxy
// that has not yet started serving. Call Serve in a goroutine to start.
func New(port int, cfg *config.Store, st *store.Store) (*Proxy, error) {
	if cfg == nil || st == nil {
		return nil, errors.New("proxy: config and store are required")
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	p := &Proxy{listener: ln, cfg: cfg, store: st}
	p.server = &http.Server{
		Handler:           p,
		ReadHeaderTimeout: 30 * time.Second,
	}
	return p, nil
}

// Addr returns the actual bound address. Useful when port=0 was passed to New.
func (p *Proxy) Addr() *net.TCPAddr {
	return p.listener.Addr().(*net.TCPAddr)
}

// Serve runs the HTTP server until Close is called. Blocks.
func (p *Proxy) Serve() error {
	if err := p.server.Serve(p.listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Close shuts the server down with a short grace period.
func (p *Proxy) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.server.Shutdown(ctx)
}

// ServeHTTP is the single proxy handler. Steps:
//  1. Build the upstream URL from settings + incoming path.
//  2. Read the request body (capped at store.MaxBodyBytes for capture).
//  3. Forward to upstream with auth header injected.
//  4. Stream the response back, capturing chunks as they arrive.
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

	if settings.UpstreamBaseURL == "" {
		entry.Error = "no upstream baseURL configured"
		entry.EndedAt = time.Now()
		entry.DurationMillis = entry.EndedAt.Sub(started).Milliseconds()
		p.store.Add(entry)
		http.Error(w, "ai-fox: upstream baseURL not configured. Open Settings to set one.", http.StatusBadGateway)
		return
	}

	target, err := buildUpstreamURL(settings.UpstreamBaseURL, r.URL)
	if err != nil {
		entry.Error = "invalid upstream baseURL: " + err.Error()
		entry.EndedAt = time.Now()
		entry.DurationMillis = entry.EndedAt.Sub(started).Milliseconds()
		p.store.Add(entry)
		http.Error(w, "ai-fox: "+entry.Error, http.StatusBadGateway)
		return
	}
	entry.UpstreamURL = target.String()

	bodyBytes, bodyTruncated, err := readBody(r.Body)
	if err != nil {
		entry.Error = "read request body: " + err.Error()
		entry.EndedAt = time.Now()
		p.store.Add(entry)
		http.Error(w, "ai-fox: "+entry.Error, http.StatusBadGateway)
		return
	}
	entry.RequestBody = string(bodyBytes)
	entry.RequestSize = int64(len(bodyBytes))
	entry.Truncated = bodyTruncated
	// Persist the request snapshot immediately so it appears in the UI even
	// before the upstream responds (important for long-running streams).
	p.store.Add(entry)

	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		p.store.Update(entry.ID, func(e *store.Entry) {
			e.Error = "build upstream request: " + err.Error()
			e.EndedAt = time.Now()
			e.DurationMillis = e.EndedAt.Sub(started).Milliseconds()
		})
		http.Error(w, "ai-fox: "+err.Error(), http.StatusBadGateway)
		return
	}
	copyHeaders(upstreamReq.Header, r.Header, hopByHopHeaders)
	upstreamReq.Host = target.Host
	upstreamReq.Header.Set("Host", target.Host)
	// Force identity encoding so the captured response body is readable text
	// instead of zstd/br/gzip bytes. Go's default transport only auto-decodes
	// gzip; brotli and zstd would pass through as binary garbage and break
	// the detail view. This mirrors how Charles / Fiddler / mitmproxy behave.
	upstreamReq.Header.Set("Accept-Encoding", "identity")
	injectAuth(upstreamReq, settings)

	resp, err := http.DefaultTransport.RoundTrip(upstreamReq)
	if err != nil {
		p.store.Update(entry.ID, func(e *store.Entry) {
			e.Error = "upstream: " + err.Error()
			e.EndedAt = time.Now()
			e.DurationMillis = e.EndedAt.Sub(started).Milliseconds()
		})
		http.Error(w, "ai-fox: upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	p.store.Update(entry.ID, func(e *store.Entry) {
		e.StatusCode = resp.StatusCode
		e.ResponseHeaders = headerMap(resp.Header)
		e.Streaming = isStreaming(resp.Header)
	})

	// Set response headers on the client connection before any body write.
	copyHeaders(w.Header(), resp.Header, hopByHopHeaders)
	w.WriteHeader(resp.StatusCode)

	captureAndStream(w, resp.Body, entry.ID, p.store)

	p.store.Update(entry.ID, func(e *store.Entry) {
		e.EndedAt = time.Now()
		e.DurationMillis = e.EndedAt.Sub(started).Milliseconds()
	})
}

// captureAndStream copies upstream into the client while appending to the
// store entry's ResponseBody. Each ~32 KiB chunk triggers a flush + store
// update so the UI sees streaming progress.
func captureAndStream(w http.ResponseWriter, body io.Reader, entryID string, st *store.Store) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	var captured bytes.Buffer
	totalBytes := int64(0)
	truncated := false

	for {
		n, err := body.Read(buf)
		if n > 0 {
			totalBytes += int64(n)
			if _, werr := w.Write(buf[:n]); werr != nil {
				st.Update(entryID, func(e *store.Entry) {
					e.Error = "write to client: " + werr.Error()
					e.ResponseBody = captured.String()
					e.ResponseSize = totalBytes
				})
				return
			}
			if flusher != nil {
				flusher.Flush()
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
			// Periodic snapshot so the UI sees live progress.
			snapshot := captured.String()
			st.Update(entryID, func(e *store.Entry) {
				e.ResponseBody = snapshot
				e.ResponseSize = totalBytes
				if truncated {
					e.Truncated = true
				}
			})
		}
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			st.Update(entryID, func(e *store.Entry) {
				e.Error = "read upstream: " + err.Error()
				e.ResponseBody = captured.String()
				e.ResponseSize = totalBytes
			})
			return
		}
	}
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
	target.RawQuery = incoming.RawQuery
	return &target, nil
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

// hopByHopHeaders are stripped per RFC 7230 §6.1. Plus a few proxy-specific
// ones (Host, Origin) we want to set ourselves.
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

// injectAuth applies the AuthPreset (or the user-defined custom headers) to
// the outgoing request. Built-in presets overwrite both the chosen auth
// header and any preset-specific extras (e.g. anthropic-version).
//
// PresetCustom skips key-based injection entirely and just applies the
// configured CustomHeaders list verbatim; the user is responsible for
// putting the secret in there themselves.
func injectAuth(req *http.Request, s config.Settings) {
	headers := PresetHeaders(s)
	for _, kv := range headers {
		if kv.Name == "" {
			continue
		}
		req.Header.Set(kv.Name, kv.Value)
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

func readBody(r io.ReadCloser) ([]byte, bool, error) {
	if r == nil {
		return nil, false, nil
	}
	defer func() { _ = r.Close() }()
	// Read up to MaxBodyBytes+1 so we can tell whether the body was larger.
	limited := io.LimitReader(r, int64(store.MaxBodyBytes)+1)
	buf, err := io.ReadAll(limited)
	if err != nil {
		return nil, false, err
	}
	if len(buf) > store.MaxBodyBytes {
		return buf[:store.MaxBodyBytes], true, nil
	}
	return buf, false, nil
}
