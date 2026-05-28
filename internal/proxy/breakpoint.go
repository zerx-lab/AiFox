// Endpoint-level breakpoints: pause a request before it's forwarded
// upstream, broadcast a "paused" event so the UI can show it, and resume
// (or abort) on the user's command.
//
// Design constraints:
//   - Only one in-flight pause per breakpoint at a time. A second matching
//     request while one is held is allowed through with a warning to the
//     captured entry; we don't want the breakpoint to silently swallow
//     traffic the user can't see.
//   - All blocking happens in the request-handling goroutine. The listener
//     keeps accepting connections normally.
//   - State is process-local; restarts wipe it. That's fine: breakpoints
//     are a debugging affordance, not a persistent rule.

package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MatchKind names the way a Breakpoint's pattern is interpreted.
type MatchKind string

const (
	// MatchEndpoint matches by exact "<METHOD> <path>" tuple, e.g.
	// "POST /v1/messages". Method comparison is case-insensitive.
	MatchEndpoint MatchKind = "endpoint"
	// MatchPath matches when the request path equals or has the pattern as
	// a suffix. Useful when the user pastes a full URL.
	MatchPath MatchKind = "path"
)

// Breakpoint is one user-configured pause rule.
type Breakpoint struct {
	ID      string    `json:"id"`
	Match   MatchKind `json:"match"`
	Pattern string    `json:"pattern"`
	Enabled bool      `json:"enabled"`
}

// Paused represents a request currently halted at a breakpoint, exposed to
// the API so the UI can show what's waiting and let the user continue/abort.
type Paused struct {
	EntryID      string    `json:"entryId"`
	BreakpointID string    `json:"breakpointId"`
	Method       string    `json:"method"`
	URL          string    `json:"url"`
	PausedAt     time.Time `json:"pausedAt"`
}

// Decision is what a held request waits for: resume forward or abort with
// a 503 to the client.
type Decision int

const (
	DecisionContinue Decision = iota
	DecisionAbort
)

type heldRequest struct {
	entryID      string
	breakpointID string
	method       string
	url          string
	pausedAt     time.Time
	decision     chan Decision
}

// Registry tracks breakpoints and their currently-held requests.
type Registry struct {
	mu          sync.Mutex
	breakpoints map[string]*Breakpoint
	held        map[string]*heldRequest // entryID -> held
	pausedByBp  map[string]string       // breakpointID -> entryID currently held
	seqBp       atomic.Uint64
	subsMu      sync.RWMutex
	subSeq      int
	subs        map[int]chan struct{}
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		breakpoints: make(map[string]*Breakpoint),
		held:        make(map[string]*heldRequest),
		pausedByBp:  make(map[string]string),
		subs:        make(map[int]chan struct{}),
	}
}

// Subscribe returns a channel that gets pinged on any breakpoint / paused
// change. Useful for SSE fan-out. Buffered at 1, coalesces aggressive
// updates.
func (r *Registry) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	r.subsMu.Lock()
	r.subSeq++
	id := r.subSeq
	r.subs[id] = ch
	r.subsMu.Unlock()
	return ch, func() {
		r.subsMu.Lock()
		if existing, ok := r.subs[id]; ok {
			delete(r.subs, id)
			close(existing)
		}
		r.subsMu.Unlock()
	}
}

func (r *Registry) notify() {
	r.subsMu.RLock()
	defer r.subsMu.RUnlock()
	for _, ch := range r.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// List returns every breakpoint, in creation order (stable).
func (r *Registry) List() []Breakpoint {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Breakpoint, 0, len(r.breakpoints))
	for _, bp := range r.breakpoints {
		out = append(out, *bp)
	}
	// Sort by ID for determinism — ID is a monotonic counter so older
	// breakpoints stay on top.
	for i := 1; i < len(out); i++ {
		j := i
		for j > 0 && out[j-1].ID > out[j].ID {
			out[j-1], out[j] = out[j], out[j-1]
			j--
		}
	}
	return out
}

// Add registers a new breakpoint and returns the assigned ID.
func (r *Registry) Add(bp Breakpoint) (Breakpoint, error) {
	if strings.TrimSpace(bp.Pattern) == "" {
		return Breakpoint{}, errors.New("breakpoint: pattern is required")
	}
	if bp.Match == "" {
		bp.Match = MatchEndpoint
	}
	r.mu.Lock()
	bp.ID = formatBpID(r.seqBp.Add(1))
	stored := bp
	r.breakpoints[bp.ID] = &stored
	r.mu.Unlock()
	r.notify()
	return stored, nil
}

// Update flips a breakpoint's enabled flag.
func (r *Registry) Update(id string, enabled bool) error {
	r.mu.Lock()
	bp, ok := r.breakpoints[id]
	if !ok {
		r.mu.Unlock()
		return errors.New("breakpoint: not found")
	}
	bp.Enabled = enabled
	r.mu.Unlock()
	r.notify()
	return nil
}

// Delete removes a breakpoint. Any request currently held by it is
// implicitly continued.
func (r *Registry) Delete(id string) {
	r.mu.Lock()
	delete(r.breakpoints, id)
	if entryID, ok := r.pausedByBp[id]; ok {
		if held, ok := r.held[entryID]; ok {
			select {
			case held.decision <- DecisionContinue:
			default:
			}
			delete(r.held, entryID)
		}
		delete(r.pausedByBp, id)
	}
	r.mu.Unlock()
	r.notify()
}

// PausedSnapshot returns the currently-held requests. Used by the API.
func (r *Registry) PausedSnapshot() []Paused {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Paused, 0, len(r.held))
	for _, h := range r.held {
		out = append(out, Paused{
			EntryID:      h.entryID,
			BreakpointID: h.breakpointID,
			Method:       h.method,
			URL:          h.url,
			PausedAt:     h.pausedAt,
		})
	}
	return out
}

// Continue resumes a held request as if the user pressed "continue". Returns
// an error if no such hold exists.
func (r *Registry) Continue(entryID string) error {
	return r.decide(entryID, DecisionContinue)
}

// Abort drops a held request — the client receives a 503 from the proxy.
func (r *Registry) Abort(entryID string) error {
	return r.decide(entryID, DecisionAbort)
}

func (r *Registry) decide(entryID string, d Decision) error {
	r.mu.Lock()
	held, ok := r.held[entryID]
	if !ok {
		r.mu.Unlock()
		return errors.New("breakpoint: no held request with this entry id")
	}
	delete(r.held, entryID)
	delete(r.pausedByBp, held.breakpointID)
	r.mu.Unlock()
	select {
	case held.decision <- d:
	default:
	}
	r.notify()
	return nil
}

// Match returns the first breakpoint that matches a request, or nil. Caller
// is the proxy handler; if a non-nil bp is returned the handler should
// register a hold via Hold() and wait for the decision.
func (r *Registry) Match(req *http.Request) *Breakpoint {
	endpoint := strings.ToUpper(req.Method) + " " + req.URL.Path
	path := req.URL.Path
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, bp := range r.breakpoints {
		if !bp.Enabled {
			continue
		}
		if _, busy := r.pausedByBp[bp.ID]; busy {
			// Already holding a request; let this one through instead of
			// chaining (would deadlock the renderer if the user never gets
			// to the first one).
			continue
		}
		switch bp.Match {
		case MatchEndpoint:
			want := strings.ToUpper(bp.Pattern)
			if strings.EqualFold(want, endpoint) {
				cp := *bp
				return &cp
			}
		case MatchPath:
			if strings.HasSuffix(path, bp.Pattern) || path == bp.Pattern {
				cp := *bp
				return &cp
			}
		}
	}
	return nil
}

// Hold registers a pause for entryID and blocks until either the user
// resolves it or ctx is cancelled (client abort). Returns the final
// Decision; on ctx cancellation returns DecisionAbort.
func (r *Registry) Hold(ctx context.Context, bpID, entryID, method, url string) Decision {
	hr := &heldRequest{
		entryID:      entryID,
		breakpointID: bpID,
		method:       method,
		url:          url,
		pausedAt:     time.Now(),
		decision:     make(chan Decision, 1),
	}
	r.mu.Lock()
	r.held[entryID] = hr
	r.pausedByBp[bpID] = entryID
	r.mu.Unlock()
	r.notify()

	select {
	case d := <-hr.decision:
		return d
	case <-ctx.Done():
		r.mu.Lock()
		delete(r.held, entryID)
		if id, ok := r.pausedByBp[bpID]; ok && id == entryID {
			delete(r.pausedByBp, bpID)
		}
		r.mu.Unlock()
		r.notify()
		return DecisionAbort
	}
}

func formatBpID(n uint64) string {
	return fmt.Sprintf("bp-%x", n)
}
