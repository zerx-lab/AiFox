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
	// Build forwarding headers from the original capture, then drop the bits a
	// replay must not echo: the captured auth (we re-inject the current preset so
	// a rotated key is honored) and the client's session-affinity header (the
	// replay is a fresh session keyed by ReplayedFromID in the aggregator).
	src := mapToHeader(original.RequestHeaders)
	for _, k := range append(authHeaderNames(), "X-Session-Affinity") {
		src.Del(k)
	}
	buildForwardHeaders(req.Header, src, target.Host, settings)
	req.Host = target.Host

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

	bodyReader, encodingWarning := decodeResponseBody(resp)
	capture(ctx, bodyReader, entry.ID, prox.store, nil)

	prox.store.Update(entry.ID, func(e *store.Entry) {
		e.EndedAt = time.Now()
		e.DurationMillis = e.EndedAt.Sub(started).Milliseconds()
		runAnalysis(e)
		if encodingWarning != "" {
			appendWarning(e, encodingWarning)
		}
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

// authHeaderNames lists the header keys whose captured values must be dropped on
// replay so the current auth preset (a possibly-rotated key) is re-injected
// rather than the stale captured credential.
func authHeaderNames() []string {
	return []string{"Authorization", "X-Api-Key", "Anthropic-Version"}
}

// mapToHeader rebuilds an http.Header from a captured single-value-per-key map
// so the shared buildForwardHeaders path can operate on it.
func mapToHeader(m map[string]string) http.Header {
	h := make(http.Header, len(m))
	for k, v := range m {
		h.Set(k, v)
	}
	return h
}
