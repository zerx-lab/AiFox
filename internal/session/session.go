// Package session aggregates captured proxy entries into conversational
// sessions. Two entries belong to the same session when their
// llmparse.NormalizedRequest projections share a fingerprint AND the newer
// request's message-prefix is the previous request's full message list.
//
// The aggregator is provider-agnostic: it consumes NormalizedRequest from
// any analyzer (Anthropic today, OpenAI / Gemini later) and produces
// SessionSummary records keyed by an opaque session ID.
//
// Implementation notes:
//   - Aggregator subscribes to a store.Store fan-out so it sees both new
//     entries and in-flight updates. On startup it also calls List() to
//     bootstrap with the buffer's existing content.
//   - The map of sessions is small (one entry per fingerprint anchor) and
//     fits comfortably in memory; we don't bother with eviction. A future
//     persistence layer can add a TTL.
package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zerx-lab/ai-fox/internal/llmparse"
	"github.com/zerx-lab/ai-fox/internal/store"
)

// Summary is a per-session rollup the API surfaces to the renderer.
type Summary struct {
	ID string `json:"id"`
	// Name is a user-supplied label. Empty means "no custom name set";
	// the renderer falls back to a time-based label in that case.
	Name          string    `json:"name,omitempty"`
	Fingerprint   string    `json:"fingerprint"`
	Provider      string    `json:"provider"`
	Model         string    `json:"model,omitempty"`
	EntryIDs      []string  `json:"entryIds"`
	StartedAt     time.Time `json:"startedAt"`
	LastAt        time.Time `json:"lastAt"`
	TurnCount     int       `json:"turnCount"`
	InputTokens   int       `json:"inputTokens"`
	OutputTokens  int       `json:"outputTokens"`
	CacheRead     int       `json:"cacheRead"`
	CacheCreate   int       `json:"cacheCreate"`
	Status        string    `json:"status"`
	HasError      bool      `json:"hasError"`
	HasStreaming  bool      `json:"hasStreaming"`
	HasUnfinished bool      `json:"hasUnfinished"`
}

// Aggregator builds and maintains sessions from a store fan-out.
type Aggregator struct {
	st *store.Store

	mu       sync.RWMutex
	sessions map[string]*Summary                     // sessionID -> rollup
	bucket   map[string][]*Summary                   // fingerprint -> ordered session list
	last     map[string][]llmparse.NormalizedMessage // sessionID -> last seen messages, for prefix containment
	byEntry  map[string]string                       // entryID -> sessionID
	// names persists user-supplied labels keyed by fingerprint so they
	// survive an app restart: the aggregator forgets sessions on shutdown,
	// but next time the same conversation anchor shows up we re-apply the
	// label.
	names     map[string]string
	namesPath string

	subsMu sync.RWMutex
	subSeq int
	subs   map[int]chan struct{}
}

// New returns an Aggregator wired to st. Call Start to begin processing.
// namesPath is an optional JSON file used to persist user-supplied session
// labels across restarts; pass "" to keep names in-memory only.
func New(st *store.Store, namesPath string) *Aggregator {
	a := &Aggregator{
		st:        st,
		sessions:  make(map[string]*Summary),
		bucket:    make(map[string][]*Summary),
		last:      make(map[string][]llmparse.NormalizedMessage),
		byEntry:   make(map[string]string),
		names:     make(map[string]string),
		namesPath: namesPath,
		subs:      make(map[int]chan struct{}),
	}
	a.loadNames()
	return a
}

// Start launches the goroutine that drains the store fan-out and updates
// the aggregator. Cancel by closing the returned stop channel.
func (a *Aggregator) Start() (stop func()) {
	// Bootstrap with whatever is already in the buffer. store.List returns
	// newest first; consume oldest first so prefix detection sees them in
	// chronological order.
	existing := a.st.List()
	for i := len(existing) - 1; i >= 0; i-- {
		a.consume(existing[i])
	}

	ch, cancel := a.st.Subscribe()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for e := range ch {
			a.consume(e)
		}
	}()
	return func() {
		cancel()
		<-stopped
	}
}

// Subscribe returns a channel that gets pinged after each session update.
// The channel is buffered (size 1); slow consumers see coalesced updates.
func (a *Aggregator) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	a.subsMu.Lock()
	a.subSeq++
	id := a.subSeq
	a.subs[id] = ch
	a.subsMu.Unlock()
	return ch, func() {
		a.subsMu.Lock()
		if existing, ok := a.subs[id]; ok {
			delete(a.subs, id)
			close(existing)
		}
		a.subsMu.Unlock()
	}
}

func (a *Aggregator) notify() {
	a.subsMu.RLock()
	defer a.subsMu.RUnlock()
	for _, ch := range a.subs {
		select {
		case ch <- struct{}{}:
		default:
			// Slow consumer: a tick is already queued, no need to coalesce.
		}
	}
}

// List returns every session, newest first by LastAt.
func (a *Aggregator) List() []*Summary {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]*Summary, 0, len(a.sessions))
	for _, s := range a.sessions {
		// Return a shallow copy so callers can't accidentally mutate our
		// EntryIDs slice in-place.
		clone := *s
		clone.EntryIDs = append([]string(nil), s.EntryIDs...)
		out = append(out, &clone)
	}
	// Newest LastAt first.
	for i := 1; i < len(out); i++ {
		j := i
		for j > 0 && out[j-1].LastAt.Before(out[j].LastAt) {
			out[j-1], out[j] = out[j], out[j-1]
			j--
		}
	}
	return out
}

// Get returns the session for a single ID.
func (a *Aggregator) Get(id string) (*Summary, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	s, ok := a.sessions[id]
	if !ok {
		return nil, false
	}
	clone := *s
	clone.EntryIDs = append([]string(nil), s.EntryIDs...)
	return &clone, true
}

// ErrNotFound is returned by SetName when the given session id is unknown.
var ErrNotFound = errors.New("session: not found")

// SetName stores a user-supplied label for the session with the given id.
// The name is also persisted by fingerprint so it survives a restart. An
// empty name clears the label (and removes the fingerprint entry from the
// names file).
func (a *Aggregator) SetName(id, name string) error {
	a.mu.Lock()
	s, ok := a.sessions[id]
	if !ok {
		a.mu.Unlock()
		return ErrNotFound
	}
	s.Name = name
	fp := s.Fingerprint
	if name == "" {
		delete(a.names, fp)
	} else {
		a.names[fp] = name
	}
	snapshot := make(map[string]string, len(a.names))
	for k, v := range a.names {
		snapshot[k] = v
	}
	path := a.namesPath
	a.mu.Unlock()
	go a.notify()
	if path == "" {
		return nil
	}
	return writeNames(path, snapshot)
}

// Clear discards every aggregated session and the persisted name map. The
// underlying store is the source of truth for entries; the renderer is
// expected to clear it separately (typically in the same DELETE handler).
func (a *Aggregator) Clear() error {
	a.mu.Lock()
	a.sessions = make(map[string]*Summary)
	a.bucket = make(map[string][]*Summary)
	a.last = make(map[string][]llmparse.NormalizedMessage)
	a.byEntry = make(map[string]string)
	a.names = make(map[string]string)
	path := a.namesPath
	a.mu.Unlock()

	go a.notify()
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SessionOf returns the session id we assigned the given entry, or "" when
// the entry wasn't part of any aggregated session.
func (a *Aggregator) SessionOf(entryID string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.byEntry[entryID]
}

// consume folds a single entry update into the session map.
func (a *Aggregator) consume(e *store.Entry) {
	if e == nil {
		return
	}
	analysis, _ := e.Analysis.(*llmparse.Analysis)
	norm := normalizedOf(analysis)
	if norm == nil {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// If the entry has already been bucketed (typical for re-broadcasts on
	// in-stream chunk updates) just refresh the per-session rollup.
	if sid, ok := a.byEntry[e.ID]; ok {
		a.refreshSummary(sid, e, norm)
		go a.notify()
		return
	}

	fp := norm.Fingerprint()
	var match *Summary
	for _, s := range a.bucket[fp] {
		prev := a.last[s.ID]
		if isPrefix(prev, norm.Messages) {
			match = s
			break
		}
	}

	if match == nil {
		match = &Summary{
			ID:          newSessionID(),
			Name:        a.names[fp],
			Fingerprint: fp,
			Provider:    norm.Provider,
			Model:       norm.Model,
			StartedAt:   e.StartedAt,
		}
		a.sessions[match.ID] = match
		a.bucket[fp] = append(a.bucket[fp], match)
	}

	match.EntryIDs = append(match.EntryIDs, e.ID)
	a.byEntry[e.ID] = match.ID
	a.last[match.ID] = norm.Messages
	a.refreshSummary(match.ID, e, norm)
	go a.notify()
}

func (a *Aggregator) refreshSummary(sid string, e *store.Entry, norm *llmparse.NormalizedRequest) {
	s := a.sessions[sid]
	if s == nil {
		return
	}
	if e.StartedAt.Before(s.StartedAt) || s.StartedAt.IsZero() {
		s.StartedAt = e.StartedAt
	}
	if e.EndedAt.After(s.LastAt) {
		s.LastAt = e.EndedAt
	} else if s.LastAt.IsZero() {
		s.LastAt = e.StartedAt
	}
	s.TurnCount = len(s.EntryIDs)
	if norm.Model != "" {
		s.Model = norm.Model
	}
	s.HasStreaming = s.HasStreaming || e.Streaming
	if e.Error != "" {
		s.HasError = true
	}
	if e.EndedAt.IsZero() {
		s.HasUnfinished = true
	}

	// Re-aggregate token usage from scratch — cheap, and avoids drift across
	// in-stream updates where the same entry's usage changes multiple times.
	in, out, cr, cc := 0, 0, 0, 0
	anyError := false
	anyStream := false
	anyUnfinished := false
	for _, eid := range s.EntryIDs {
		entry, ok := a.st.Get(eid)
		if !ok {
			continue
		}
		if entry.Error != "" {
			anyError = true
		}
		if entry.Streaming {
			anyStream = true
		}
		if entry.EndedAt.IsZero() {
			anyUnfinished = true
		}
		ana, _ := entry.Analysis.(*llmparse.Analysis)
		if ana == nil || ana.Anthropic == nil || ana.Anthropic.Response == nil {
			continue
		}
		if u := ana.Anthropic.Response.Usage; u != nil {
			in += u.InputTokens
			out += u.OutputTokens
			cr += u.CacheReadInputTokens
			cc += u.CacheCreationInputTokens
		}
	}
	s.InputTokens = in
	s.OutputTokens = out
	s.CacheRead = cr
	s.CacheCreate = cc
	s.HasError = anyError
	s.HasStreaming = anyStream
	s.HasUnfinished = anyUnfinished
	switch {
	case s.HasError:
		s.Status = "failed"
	case s.HasUnfinished:
		s.Status = "active"
	default:
		s.Status = "completed"
	}
}

// isPrefix returns true when prev is an exact prefix of curr — same length
// or one element shorter is what we expect for "next turn in the same
// conversation".
func isPrefix(prev, curr []llmparse.NormalizedMessage) bool {
	if len(prev) > len(curr) {
		return false
	}
	for i := range prev {
		if prev[i].Role != curr[i].Role || prev[i].Fingerprint != curr[i].Fingerprint {
			return false
		}
	}
	return true
}

func normalizedOf(a *llmparse.Analysis) *llmparse.NormalizedRequest {
	if a == nil {
		return nil
	}
	return a.Normalized
}

// loadNames reads the fingerprint->name map from disk. Missing file or
// malformed JSON is treated as "no saved names" — we never want a bad file
// to block boot.
func (a *Aggregator) loadNames() {
	if a.namesPath == "" {
		return
	}
	raw, err := os.ReadFile(a.namesPath)
	if err != nil {
		return
	}
	var loaded map[string]string
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return
	}
	for k, v := range loaded {
		if v == "" {
			continue
		}
		a.names[k] = v
	}
}

func writeNames(path string, names map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		return err
	}
	// 0600: names are user data and live alongside settings.json which
	// stores secrets — keep the file private to the current user.
	return os.WriteFile(path, raw, 0o600)
}

var sessionSeq uint64

func newSessionID() string {
	// Combine wall-clock + a monotonic counter so two sessions born in the
	// same millisecond don't collide.
	sessionSeq++
	return formatID(time.Now().UnixMilli(), sessionSeq)
}

func formatID(now int64, n uint64) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	buf := make([]byte, 0, 16)
	v := uint64(now) ^ (n << 1)
	if v == 0 {
		return "s-0"
	}
	for v > 0 {
		buf = append(buf, digits[v%36])
		v /= 36
	}
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return "s-" + string(buf)
}
