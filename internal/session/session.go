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
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zerx-lab/ai-fox/internal/llmparse"
	"github.com/zerx-lab/ai-fox/internal/store"
)

// Summary is a per-session rollup the API surfaces to the renderer.
type Summary struct {
	ID string `json:"id"`
	// Name is a user-supplied label. Empty means "no custom name set";
	// the renderer falls back to a time-based label in that case.
	Name        string `json:"name,omitempty"`
	Fingerprint string `json:"fingerprint"`
	Provider    string `json:"provider"`
	// Model is the session's primary model — the most recent non-utility model
	// seen. Models lists every distinct model used (primary first), so a mixed
	// opus+haiku session can be shown as "opus·haiku" without losing the anchor.
	Model         string    `json:"model,omitempty"`
	Models        []string  `json:"models,omitempty"`
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

// Coalescing/reconcile cadence. Per-chunk recompute + notify was the second
// concurrency amplifier (design §1.2 B): consume ran an O(turns) re-sum and a
// goroutine-spawning notify on every streamed chunk. Now consume only buckets
// + marks the session dirty; a flush tick recomputes dirty sessions and fires
// one coalesced notify. reconcile recovers entries whose bucketing broadcast
// was dropped on a full subscribe channel (the cause of the harness's 6/8
// under-aggregation).
const (
	// FlushInterval and ReconcileInterval are exported so the API layer's index
	// stream can share the exact same coalescing cadence instead of duplicating
	// the constants (the two streams are tuned together).
	FlushInterval     = 80 * time.Millisecond
	ReconcileInterval = 1 * time.Second

	flushInterval     = FlushInterval
	reconcileInterval = ReconcileInterval
)

// Aggregator builds and maintains sessions from a store fan-out.
type Aggregator struct {
	st *store.Store

	mu       sync.RWMutex
	sessions map[string]*Summary                     // sessionID -> rollup
	byKey    map[string]*Summary                     // client session key (header/replay) -> session
	bucket   map[string][]*Summary                   // fingerprint -> ordered session list (fallback only)
	last     map[string][]llmparse.NormalizedMessage // sessionID -> last seen messages, for prefix containment
	byEntry  map[string]string                       // entryID -> sessionID
	dirty    map[string]struct{}                     // sessionIDs needing a recompute on the next flush tick
	// names persists user-supplied labels keyed by session ANCHOR (a header key
	// for keyed sessions, a fingerprint for fallback ones) so they survive an
	// app restart: the aggregator forgets sessions on shutdown, but next time
	// the same conversation anchor shows up we re-apply the label.
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
		byKey:     make(map[string]*Summary),
		bucket:    make(map[string][]*Summary),
		last:      make(map[string][]llmparse.NormalizedMessage),
		byEntry:   make(map[string]string),
		dirty:     make(map[string]struct{}),
		names:     make(map[string]string),
		namesPath: namesPath,
		subs:      make(map[int]chan struct{}),
	}
	a.loadNames()
	return a
}

// Start launches the goroutines that drain the store fan-out and periodically
// flush coalesced session updates. Cancel via the returned stop func.
func (a *Aggregator) Start() (stop func()) {
	// Bootstrap with whatever is already in the buffer. store.List returns
	// newest first; consume oldest first so prefix detection sees them in
	// chronological order.
	existing := a.st.List()
	for i := len(existing) - 1; i >= 0; i-- {
		a.consume(existing[i])
	}
	a.flushDirty() // settle bootstrapped sessions before the first subscriber.

	// The aggregator is a correctness-critical consumer: a dropped bucketing
	// event leaves an entry unsessioned until the next reconcile. Take a large
	// buffer so realistic concurrent-stream bursts don't overflow; reconcile
	// remains the backstop for sustained overload.
	ch, cancel := a.st.SubscribeBuffer(1024)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for e := range ch {
			a.consume(e)
		}
	}()

	tickStop := make(chan struct{})
	tickDone := make(chan struct{})
	go a.tickLoop(tickStop, tickDone)

	return func() {
		cancel()
		<-stopped
		close(tickStop)
		<-tickDone
	}
}

// tickLoop coalesces session recomputes (flushInterval) and recovers entries
// whose bucketing broadcast was dropped under load (reconcileInterval).
func (a *Aggregator) tickLoop(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	flush := time.NewTicker(flushInterval)
	defer flush.Stop()
	reconcile := time.NewTicker(reconcileInterval)
	defer reconcile.Stop()
	for {
		select {
		case <-stop:
			return
		case <-flush.C:
			a.flushDirty()
		case <-reconcile.C:
			a.reconcile()
			a.flushDirty()
		}
	}
}

// flushDirty recomputes every session marked dirty since the last tick and
// fires a single coalesced notify. Recompute is gated here (not per chunk) so
// the O(turns) re-sum runs at most ~12 Hz per session under load.
//
// The per-entry store reads happen OUTSIDE a.mu: we snapshot each dirty
// session's entry-id list under the lock, drop the lock, scan the store (which
// has its own lock — order a.mu -> store, never inverted), then re-acquire a.mu
// only to write the rollups back. Holding a.mu across the st.Get loop would
// stall the SSE hot path (agg.SessionOf is on it) and back up the drain
// goroutine, which makes the dropped-broadcast window the reconcile heals below
// more likely.
func (a *Aggregator) flushDirty() {
	a.mu.Lock()
	if len(a.dirty) == 0 {
		a.mu.Unlock()
		return
	}
	work := make(map[string][]string, len(a.dirty))
	for sid := range a.dirty {
		if s := a.sessions[sid]; s != nil {
			work[sid] = append([]string(nil), s.EntryIDs...)
		}
		delete(a.dirty, sid)
	}
	a.mu.Unlock()

	rollups := make(map[string]rollup, len(work))
	for sid, ids := range work {
		rollups[sid] = a.computeRollup(ids)
	}

	a.mu.Lock()
	for sid, r := range rollups {
		if s := a.sessions[sid]; s != nil {
			r.applyTo(s)
		}
	}
	a.mu.Unlock()
	a.notify()
}

// reconcile self-heals two ways the store fan-out's drop-on-full can corrupt
// the session view under sustained K-stream overload:
//
//  1. A new entry whose bucketing broadcast was dropped is never bucketed —
//     we re-consume any store entry not yet in byEntry.
//  2. An already-bucketed entry whose *finalize* broadcast was dropped leaves
//     its session marked HasUnfinished/active forever (consume never re-marks
//     it dirty, and the unbucketed pass above skips it as `known`). We re-mark
//     every still-unfinished session dirty so the next flush recomputes it from
//     store truth (where EndedAt is now set) and flips it to completed.
func (a *Aggregator) reconcile() {
	entries := a.st.List()
	live := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		live[e.ID] = struct{}{}
		a.mu.RLock()
		_, known := a.byEntry[e.ID]
		a.mu.RUnlock()
		if known {
			continue
		}
		a.consume(e)
	}
	a.mu.Lock()
	// Prune entry-id bookkeeping for entries the store has since evicted. byEntry
	// would otherwise grow without bound on a long-running process (the store's
	// ring buffer drops old entries but the aggregator never heard about it). We
	// keep the session itself (the UI list is keyed by session, not entry) and
	// only trim it to live entries so its EntryIDs/rollup stay bounded by the
	// store capacity. A session that loses all its entries is left in place with
	// its last computed rollup — harmless and avoids reshuffling the sidebar.
	for eid := range a.byEntry {
		if _, ok := live[eid]; ok {
			continue
		}
		sid := a.byEntry[eid]
		delete(a.byEntry, eid)
		if s := a.sessions[sid]; s != nil {
			s.EntryIDs = filterLive(s.EntryIDs, live)
			a.dirty[sid] = struct{}{}
		}
	}
	for sid, s := range a.sessions {
		if s.HasUnfinished {
			a.dirty[sid] = struct{}{}
		}
	}
	a.mu.Unlock()
}

// filterLive returns the subset of ids still present in live, preserving order.
func filterLive(ids []string, live map[string]struct{}) []string {
	out := ids[:0:0]
	for _, id := range ids {
		if _, ok := live[id]; ok {
			out = append(out, id)
		}
	}
	return out
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
	// Newest StartedAt first. Sorting by StartedAt (immutable) instead of LastAt
	// keeps the sidebar order STABLE: an active session updating at ~12 Hz no
	// longer reshuffles to the top on every tick (the jitter the UX layer must
	// avoid). Liveness is conveyed by the per-session status dot, not by
	// reordering. Tie-break by ID so equal StartedAt is deterministic.
	for i := 1; i < len(out); i++ {
		j := i
		for j > 0 && lessRecent(out[j-1], out[j]) {
			out[j-1], out[j] = out[j], out[j-1]
			j--
		}
	}
	return out
}

// lessRecent reports whether a should sort AFTER b (b is newer). Newest
// StartedAt first; ID descending as a stable tie-break.
func lessRecent(a, b *Summary) bool {
	if !a.StartedAt.Equal(b.StartedAt) {
		return a.StartedAt.Before(b.StartedAt)
	}
	return a.ID < b.ID
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
// The name is persisted by the session's ANCHOR (its Fingerprint field — a
// header key like "hdr:X-Session-Affinity:…" for keyed sessions, or a request
// fingerprint hash for fallback sessions) so it re-applies when the same
// conversation re-aggregates after a restart. An empty name clears the label.
// Note: custom names saved before the L3 header-keying upgrade were keyed by a
// pure fingerprint and are orphaned for conversations now keyed by header — an
// accepted one-time loss (this is a pre-release debug tool).
func (a *Aggregator) SetName(id, name string) error {
	a.mu.Lock()
	s, ok := a.sessions[id]
	if !ok {
		a.mu.Unlock()
		return ErrNotFound
	}
	s.Name = name
	anchor := s.Fingerprint
	if name == "" {
		delete(a.names, anchor)
	} else {
		a.names[anchor] = name
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
	a.byKey = make(map[string]*Summary)
	a.bucket = make(map[string][]*Summary)
	a.last = make(map[string][]llmparse.NormalizedMessage)
	a.byEntry = make(map[string]string)
	a.dirty = make(map[string]struct{})
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

// consume folds a single entry update into the session map. It only buckets
// the entry and marks its session dirty; the actual rollup recompute + notify
// is deferred to the coalescing flush tick (flushDirty). This keeps the
// streamed hot path O(1) per chunk instead of O(turns) + a goroutine.
//
// Correlation precedence (design §3):
//  1. An explicit client session key — the X-Session-Affinity header (opencode)
//     or a replay's ReplayedFromID. This is derived from headers BEFORE
//     requiring a parsed analysis, so a session forms on the very first frame
//     and survives mixed models / mixed system prompts within one conversation.
//  2. Otherwise the request-fingerprint + message-prefix fallback (keeps Model;
//     forks on divergence). Used for clients that send no session header.
func (a *Aggregator) consume(e *store.Entry) {
	if e == nil {
		return
	}
	key, keyed := sessionKeyOf(e)
	analysis, _ := e.Analysis.(*llmparse.Analysis)
	norm := normalizedOf(analysis)
	// Without an explicit key we need the analysis (for the fingerprint anchor);
	// with a key we can place the entry even before its body parses.
	if !keyed && norm == nil {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Already bucketed (typical for in-stream chunk re-broadcasts): refresh the
	// prefix anchor (the message list grows as the turn streams) and mark dirty.
	if sid, ok := a.byEntry[e.ID]; ok {
		if norm != nil {
			a.last[sid] = norm.Messages
		}
		a.dirty[sid] = struct{}{}
		return
	}

	var match *Summary
	if keyed {
		// Explicit client session key: bucket directly. Model/system/first-
		// message changes within the same key never split the session.
		match = a.byKey[key]
		if match == nil {
			match = a.newSession(key, e, norm)
			a.byKey[key] = match
		}
	} else {
		// Fallback: fingerprint bucket + longest message-prefix match; fork when
		// the incoming request diverges from every candidate in the bucket.
		fp := norm.Fingerprint()
		match = a.bestPrefixMatch(fp, norm.Messages)
		if match == nil {
			match = a.newSession(fp, e, norm)
			a.bucket[fp] = append(a.bucket[fp], match)
		}
	}

	match.EntryIDs = append(match.EntryIDs, e.ID)
	a.byEntry[e.ID] = match.ID
	if norm != nil {
		a.last[match.ID] = norm.Messages
	}
	a.dirty[match.ID] = struct{}{}
}

// newSession mints a session anchored on `anchor` (a client key or a
// fingerprint). The anchor doubles as the names-persistence key so a
// user-supplied label re-applies when the same conversation re-aggregates.
func (a *Aggregator) newSession(anchor string, e *store.Entry, norm *llmparse.NormalizedRequest) *Summary {
	s := &Summary{
		ID:          newSessionID(),
		Name:        a.names[anchor],
		Fingerprint: anchor,
		StartedAt:   e.StartedAt,
	}
	if norm != nil {
		s.Provider = norm.Provider
		s.Model = norm.Model
	}
	a.sessions[s.ID] = s
	return s
}

// bestPrefixMatch returns the fallback-bucket session whose recorded message
// list is the LONGEST prefix of msgs (the most specific continuation). Returns
// nil when every candidate diverges, so the caller forks a new session — this
// is what keeps two concurrent same-anchor conversations from cross-attributing
// each other's turns.
func (a *Aggregator) bestPrefixMatch(fp string, msgs []llmparse.NormalizedMessage) *Summary {
	var best *Summary
	bestLen := -1
	for _, s := range a.bucket[fp] {
		prev := a.last[s.ID]
		if isPrefix(prev, msgs) && len(prev) > bestLen {
			best = s
			bestLen = len(prev)
		}
	}
	return best
}

// sessionHeaderSources is the provider-namespaced allowlist of request headers
// that carry a stable per-conversation id. Only opencode's X-Session-Affinity
// is supported today; add a row to extend to another client whose header is
// verified to be a conversation id (not a load-balancer sticky-routing token).
var sessionHeaderSources = []string{"X-Session-Affinity"}

// sessionKeyOf derives an explicit client session key, or ("", false) when none
// is present. A replayed entry always gets its own key so it forks into a fresh
// session (user decision: replays do not pollute the original's rollups).
func sessionKeyOf(e *store.Entry) (string, bool) {
	if e.ReplayedFromID != "" {
		return "replay:" + e.ID, true
	}
	for _, h := range sessionHeaderSources {
		if v := headerValue(e.RequestHeaders, h); v != "" {
			return "hdr:" + h + ":" + v, true
		}
	}
	return "", false
}

// headerValue looks up a header case-insensitively. The store keeps canonical
// keys (e.g. "X-Session-Affinity"), but a case-insensitive fallback guards
// against any non-canonical capture path.
func headerValue(hdrs map[string]string, name string) string {
	if len(hdrs) == 0 {
		return ""
	}
	if v, ok := hdrs[http.CanonicalHeaderKey(name)]; ok {
		return v
	}
	for k, v := range hdrs {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

// rollup is the recomputed summary of one session, produced outside a.mu by
// computeRollup and written back under a.mu by applyTo.
type rollup struct {
	started, last                   time.Time
	turnCount                       int // non-utility entries only
	provider                        string
	model                           string
	models                          []string
	in, out, cr, cc                 int
	anyError, anyStream, unfinished bool
}

// computeRollup rebuilds a session's rollup from scratch by scanning its entries
// in the store. It takes NO aggregator lock (only store.Get's own lock), so the
// caller must pass an entry-id snapshot taken under a.mu. Recompute (not
// incremental accumulation) is deliberate: in-stream output_tokens is REPLACED
// on each chunk, not summed, so an incremental delta scheme would drift —
// re-summing at the flush tick is both correct and cheap.
func (a *Aggregator) computeRollup(entryIDs []string) rollup {
	var r rollup
	seenModel := map[string]bool{}
	for _, eid := range entryIDs {
		entry, ok := a.st.Get(eid)
		if !ok {
			continue
		}
		if r.started.IsZero() || entry.StartedAt.Before(r.started) {
			r.started = entry.StartedAt
		}
		end := entry.EndedAt
		if end.IsZero() {
			r.unfinished = true
			end = entry.StartedAt
		}
		if end.After(r.last) {
			r.last = end
		}
		if entry.Error != "" {
			r.anyError = true
		}
		if entry.Streaming {
			r.anyStream = true
		}
		ana, _ := entry.Analysis.(*llmparse.Analysis)
		if ana == nil {
			continue
		}
		// Backfill provider from the normalized projection. Header-keyed
		// sessions mint their Summary at request time (before any analysis), so
		// newSession can't set Provider — the rollup is the only place it lands.
		if ana.Normalized != nil && ana.Normalized.Provider != "" {
			r.provider = ana.Normalized.Provider
		}
		if ana.Anthropic == nil {
			continue
		}
		utility := llmparse.IsUtilityRequest(ana)
		if !utility {
			r.turnCount++ // sub-task calls (title-gen/summaries) are not turns
		}
		model := ""
		if req := ana.Anthropic.Request; req != nil {
			model = req.Model
		}
		if resp := ana.Anthropic.Response; resp != nil {
			if resp.Model != "" {
				model = resp.Model
			}
			if u := resp.Usage; u != nil {
				r.in += u.InputTokens
				r.out += u.OutputTokens
				r.cr += u.CacheReadInputTokens
				r.cc += u.CacheCreationInputTokens
			}
		}
		if model != "" {
			if !seenModel[model] {
				seenModel[model] = true
				r.models = append(r.models, model)
			}
			// Primary model prefers the most recent non-utility turn; oldest-first
			// iteration means the last assignment wins.
			if !utility || r.model == "" {
				r.model = model
			}
		}
	}
	// Order Models with the primary first so the UI can render "primary +N".
	if r.model != "" && len(r.models) > 1 {
		r.models = append([]string{r.model}, removeStr(r.models, r.model)...)
	}
	return r
}

func removeStr(xs []string, target string) []string {
	out := xs[:0:0]
	for _, x := range xs {
		if x != target {
			out = append(out, x)
		}
	}
	return out
}

// applyTo writes a computed rollup onto the live summary. Caller holds a.mu.
func (r rollup) applyTo(s *Summary) {
	if !r.started.IsZero() {
		s.StartedAt = r.started
	}
	s.LastAt = r.last
	s.TurnCount = r.turnCount
	if r.provider != "" {
		s.Provider = r.provider
	}
	if r.model != "" {
		s.Model = r.model
	}
	s.Models = r.models
	s.InputTokens, s.OutputTokens, s.CacheRead, s.CacheCreate = r.in, r.out, r.cr, r.cc
	s.HasError, s.HasStreaming, s.HasUnfinished = r.anyError, r.anyStream, r.unfinished
	switch {
	case r.anyError:
		s.Status = "failed"
	case r.unfinished:
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

var sessionSeq atomic.Uint64

// newSessionID returns a process-unique, roughly time-ordered session ID.
//
// The previous implementation hashed `millis ^ (counter<<1)` into one base36
// number, which COLLIDES: two sessions born in adjacent milliseconds with the
// right counter values map to the same string, and the later
// `a.sessions[id] = match` silently overwrites the earlier session — entries of
// the lost session then read as "unsessioned". The monotonic counter alone
// guarantees uniqueness; the millisecond prefix only gives a rough creation
// order. The '-' separator keeps the variable-length prefix/suffix from
// concatenating ambiguously (e.g. "abc"+"1" vs "ab"+"c1").
func newSessionID() string {
	n := sessionSeq.Add(1)
	return "s-" + strconv.FormatInt(time.Now().UnixMilli(), 36) + "-" + strconv.FormatUint(n, 36)
}
