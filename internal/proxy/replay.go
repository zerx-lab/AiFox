// Replay re-issues a previously captured request against the configured
// upstream, optionally overriding a handful of common LLM-call parameters.
// The result is a fresh store.Entry with ReplayedFromID pointing at the
// original — the renderer uses that to render the "↩ replay of …" badge.

package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/zerx-lab/ai-fox/internal/store"
)

// ReplayOverrides bundles every parameter the renderer is allowed to tweak.
// Pointer fields stand in for tri-state ("don't touch" / "set to value"),
// since the JSON-decoded request body may already have any of these keys.
type ReplayOverrides struct {
	Model       *string
	Temperature *float64
	TopP        *float64
	TopK        *int
	MaxTokens   *int
	Stream      *bool
}

// Replay constructs a synthetic *Entry (capturing the new request/response
// cycle) and runs it through the same forwarding path as a live request.
// The original entry isn't mutated; the new entry's ReplayedFromID is set
// so the UI can link them.
//
// Returns the new entry's ID on success, or an error if the original couldn't
// be located / the request couldn't be rebuilt / the upstream call failed.
func (c *Controller) Replay(ctx context.Context, originalID string, overrides ReplayOverrides) (string, error) {
	c.mu.Lock()
	prox := c.proxy
	c.mu.Unlock()
	if prox == nil {
		return "", ErrNotRunning
	}

	original, ok := prox.store.Get(originalID)
	if !ok {
		return "", errors.New("replay: original entry not found")
	}

	body := original.RequestBody
	if hasOverrides(overrides) {
		patched, err := applyOverridesToBody(body, overrides)
		if err != nil {
			return "", fmt.Errorf("replay: %w", err)
		}
		body = patched
	}

	settings := prox.cfg.Get()
	if settings.UpstreamBaseURL == "" {
		return "", errors.New("replay: no upstream baseURL configured")
	}
	// Rebuild upstream URL from the original's URL — we want the exact same
	// endpoint, even if the user has since changed providers.
	parsedURL, err := url.Parse(original.URL)
	if err != nil {
		return "", fmt.Errorf("replay: parse url: %w", err)
	}
	target, err := buildUpstreamURL(settings.UpstreamBaseURL, parsedURL)
	if err != nil {
		return "", fmt.Errorf("replay: %w", err)
	}

	started := time.Now()
	entry := &store.Entry{
		ID:             prox.store.NextID(),
		StartedAt:      started,
		Method:         original.Method,
		URL:            original.URL,
		UpstreamURL:    target.String(),
		RequestHeaders: copyHeaderMap(original.RequestHeaders),
		RequestBody:    body,
		RequestSize:    int64(len(body)),
		ReplayedFromID: originalID,
	}
	prox.store.Add(entry)

	req, err := http.NewRequestWithContext(ctx, original.Method, target.String(), bytes.NewReader([]byte(body)))
	if err != nil {
		prox.store.Update(entry.ID, func(e *store.Entry) {
			e.Error = "build replay request: " + err.Error()
			e.EndedAt = time.Now()
			e.DurationMillis = e.EndedAt.Sub(started).Milliseconds()
		})
		return entry.ID, err
	}
	// Copy original client headers minus hop-by-hop ones and auth (we apply
	// the current preset so a rotated key is honored).
	for k, v := range original.RequestHeaders {
		if _, drop := hopByHopHeaders[http.CanonicalHeaderKey(k)]; drop {
			continue
		}
		if isAuthHeader(k) {
			continue
		}
		// Drop the client's session-affinity header: the replay is a fresh
		// session (keyed by ReplayedFromID in the aggregator), and forwarding a
		// stale sticky-routing token upstream serves no purpose.
		if http.CanonicalHeaderKey(k) == "X-Session-Affinity" {
			continue
		}
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept-Encoding", "identity")
	injectAuth(req, settings)

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		prox.store.Update(entry.ID, func(e *store.Entry) {
			e.Error = "replay upstream: " + err.Error()
			e.EndedAt = time.Now()
			e.DurationMillis = e.EndedAt.Sub(started).Milliseconds()
		})
		return entry.ID, err
	}
	defer func() { _ = resp.Body.Close() }()

	prox.store.Update(entry.ID, func(e *store.Entry) {
		e.StatusCode = resp.StatusCode
		e.ResponseHeaders = headerMap(resp.Header)
		e.Streaming = isStreaming(resp.Header)
	})

	captureToStore(ctx, resp.Body, entry.ID, prox.store)

	prox.store.Update(entry.ID, func(e *store.Entry) {
		e.EndedAt = time.Now()
		e.DurationMillis = e.EndedAt.Sub(started).Milliseconds()
		runAnalysis(e)
	})
	return entry.ID, nil
}

// hasOverrides reports whether the user wants to mutate the request body at
// all — used to skip a JSON round-trip on a plain "rerun verbatim" replay.
func hasOverrides(o ReplayOverrides) bool {
	return o.Model != nil ||
		o.Temperature != nil ||
		o.TopP != nil ||
		o.TopK != nil ||
		o.MaxTokens != nil ||
		o.Stream != nil
}

func applyOverridesToBody(body string, o ReplayOverrides) (string, error) {
	if body == "" {
		// Nothing to patch into — synthesize a minimal object so the override
		// at least surfaces on the wire.
		body = "{}"
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return "", fmt.Errorf("decode request body: %w", err)
	}
	if o.Model != nil {
		raw["model"] = *o.Model
	}
	if o.Temperature != nil {
		raw["temperature"] = *o.Temperature
	}
	if o.TopP != nil {
		raw["top_p"] = *o.TopP
	}
	if o.TopK != nil {
		raw["top_k"] = *o.TopK
	}
	if o.MaxTokens != nil {
		raw["max_tokens"] = *o.MaxTokens
	}
	if o.Stream != nil {
		raw["stream"] = *o.Stream
	}
	out, err := json.Marshal(raw)
	if err != nil {
		return "", fmt.Errorf("encode patched body: %w", err)
	}
	return string(out), nil
}

func copyHeaderMap(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = v
	}
	return out
}

func isAuthHeader(k string) bool {
	lk := http.CanonicalHeaderKey(k)
	switch lk {
	case "Authorization", "X-Api-Key", "Anthropic-Version":
		return true
	}
	return false
}

// captureToStore mirrors captureAndStream's append behavior but writes only
// to the store (no client connection to push to during a replay).
func captureToStore(ctx context.Context, body io.Reader, entryID string, st *store.Store) {
	buf := make([]byte, 32*1024)
	var captured bytes.Buffer
	totalBytes := int64(0)
	truncated := false
	// Geometric size-doubling analysis trigger, same as captureAndStream: bounds
	// total in-stream parse work to O(size) instead of O(size × duration).
	var nextAnalyzeLen int

	for {
		n, err := body.Read(buf)
		if n > 0 {
			totalBytes += int64(n)
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
				nextAnalyzeLen = captured.Len() * 2
				if nextAnalyzeLen < firstAnalyzeBytes {
					nextAnalyzeLen = firstAnalyzeBytes
				}
			}
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
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			st.Update(entryID, func(e *store.Entry) {
				if !isClientAbort(ctx, err) {
					e.Error = "read replay upstream: " + err.Error()
				}
				e.ResponseBody = captured.String()
				e.ResponseSize = totalBytes
			})
			return
		}
	}
}
