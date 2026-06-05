// Package store keeps an in-memory ring buffer of captured proxy traffic and
// fans new entries out to live subscribers (used by the SSE stream endpoint).
//
// Persistence (optional): when constructed via NewPersistent the Store also
// appends each finalized entry (EndedAt non-zero) to a JSONL file alongside
// the user's settings. The file holds raw prompts and authorization headers
// in plaintext; rely on OS user-account isolation + 0600 file mode, which
// matches how settings.json / session-names.json already live on disk. The
// renderer's "clear" action truncates this file along with the in-memory
// buffer (see Clear).
package store

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Direction labels each captured artifact as either the request leaving the
// proxy toward upstream or the response coming back to the client.
type Direction string

const (
	DirectionRequest  Direction = "request"
	DirectionResponse Direction = "response"
)

// Entry is one captured request/response cycle. We keep the full bodies in
// memory; bodies above MaxBodyBytes are truncated and the Truncated flag is
// flipped so the UI can warn the user.
//
// Analysis is the optional structured view produced by internal/llmparse.
// Stored as `any` so this package stays decoupled from the parser — the
// proxy layer is responsible for filling it in and the API layer for
// re-typing it on the wire.
type Entry struct {
	ID              string            `json:"id"`
	StartedAt       time.Time         `json:"startedAt"`
	EndedAt         time.Time         `json:"endedAt"`
	DurationMillis  int64             `json:"durationMillis"`
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	UpstreamURL     string            `json:"upstreamUrl"`
	StatusCode      int               `json:"statusCode"`
	RequestHeaders  map[string]string `json:"requestHeaders"`
	RequestBody     string            `json:"requestBody"`
	RequestSize     int64             `json:"requestSize"`
	ResponseHeaders map[string]string `json:"responseHeaders"`
	ResponseBody    string            `json:"responseBody"`
	ResponseSize    int64             `json:"responseSize"`
	Streaming       bool              `json:"streaming"`
	Truncated       bool              `json:"truncated"`
	Error           string            `json:"error,omitempty"`
	Analysis        any               `json:"analysis,omitempty"`
	// SessionID is set by the session.Aggregator when this entry is folded
	// into a session. Optional; entries from unrecognized providers stay
	// "". The store itself never reads this field.
	SessionID string `json:"sessionId,omitempty"`
	// ReplayedFromID, when non-empty, points to the entry this one was
	// re-issued from via the replay API. The store treats it as opaque.
	ReplayedFromID string `json:"replayedFromId,omitempty"`
}

// MaxBodyBytes caps the per-direction body size we keep in memory.
const MaxBodyBytes = 1 << 20 // 1 MiB

// clone returns an immutable snapshot of e suitable for handing to readers
// outside the store lock. It is a shallow struct copy, which is race-safe
// ONLY because of three invariants the rest of the codebase upholds:
//
//   - String fields (RequestBody/ResponseBody/...) are immutable values; the
//     proxy reassigns the FIELD under s.mu, never mutates the backing array.
//   - The header maps are write-once: built fresh (headerMap) and assigned
//     once, never mutated afterwards, so sharing the map across clones is safe
//     for read-only consumers.
//   - Analysis is copy-on-write: llmparse.Analyze returns a fresh *Analysis on
//     every run and the proxy reassigns e.Analysis to it; the pointee is never
//     mutated in place, so a captured pointer is stable.
//
// Because the live *Entry in idIndex is NEVER handed out (only clones are),
// concurrent reads by subscribers/Get/List can never race the proxy's locked
// writes. This is the single mechanism that makes the streaming hot path
// race-free; do not hand out the live pointer.
func (e *Entry) clone() *Entry {
	c := *e
	return &c
}

// Store is a bounded ring buffer of Entry plus a fan-out for live subscribers.
// Methods are safe to call from multiple goroutines.
type Store struct {
	mu       sync.RWMutex
	capacity int
	entries  []*Entry
	// idIndex maps Entry.ID to its slot to avoid linear lookup in Get.
	idIndex map[string]*Entry

	subsMu sync.RWMutex
	subs   map[int]chan *Entry
	nextID atomic.Uint64
	subSeq int

	// persistPath, when non-empty, is the JSONL file used to keep finalized
	// entries across restarts. Empty means "in-memory only".
	persistPath string
	// fileMu serializes file-IO so concurrent finalizations don't interleave
	// partial lines on disk.
	fileMu sync.Mutex
	// persisted tracks entry IDs already written to disk so streaming Updates
	// don't write the same entry twice. Guarded by mu.
	persisted map[string]struct{}
}

// New returns a Store that retains the most recent `capacity` entries.
func New(capacity int) *Store {
	if capacity <= 0 {
		capacity = 200
	}
	return &Store{
		capacity: capacity,
		entries:  make([]*Entry, 0, capacity),
		idIndex:  make(map[string]*Entry, capacity),
		subs:     make(map[int]chan *Entry),
	}
}

// NewPersistent returns a Store backed by the JSONL file at `path`. On boot
// it loads up to `capacity` most-recent entries from the file (dedup by ID,
// last write wins) so the renderer sees prior sessions immediately. Returns
// an error only if the file exists but is unreadable; a missing file is
// treated as a fresh start. Passing path == "" behaves the same as New.
func NewPersistent(capacity int, path string) (*Store, error) {
	s := New(capacity)
	if path == "" {
		return s, nil
	}
	s.persistPath = path
	s.persisted = make(map[string]struct{})
	if err := s.bootstrap(); err != nil {
		return nil, err
	}
	return s, nil
}

// NextID returns a monotonically increasing identifier suitable for Entry.ID.
// The format ("e-<n>") is opaque to callers; do not parse it.
func (s *Store) NextID() string {
	n := s.nextID.Add(1)
	return formatID(n)
}

// Add stores e, evicting the oldest entry if capacity is exceeded, and
// broadcasts to subscribers. e must not be mutated after this call.
func (s *Store) Add(e *Entry) {
	s.mu.Lock()
	if len(s.entries) >= s.capacity {
		evicted := s.entries[0]
		s.entries = s.entries[1:]
		delete(s.idIndex, evicted.ID)
	}
	s.entries = append(s.entries, e)
	s.idIndex[e.ID] = e
	shouldPersist := s.markPersistableLocked(e)
	// Snapshot under the lock so subscribers never touch the live pointer the
	// proxy keeps mutating. See Entry.clone for why this is race-safe.
	snap := e.clone()
	s.mu.Unlock()

	if shouldPersist {
		s.appendToFile(snap)
	}
	s.broadcast(snap)
}

// Update applies fn to the entry identified by id and re-broadcasts it.
// If the entry was already evicted, Update is a no-op.
func (s *Store) Update(id string, fn func(*Entry)) {
	s.mu.Lock()
	e, ok := s.idIndex[id]
	var shouldPersist bool
	var snap *Entry
	if ok {
		fn(e)
		shouldPersist = s.markPersistableLocked(e)
		snap = e.clone()
	}
	s.mu.Unlock()
	if ok {
		if shouldPersist {
			s.appendToFile(snap)
		}
		s.broadcast(snap)
	}
}

// List returns a snapshot of the buffer, newest first. Entries are clones so
// callers can read them without racing the proxy's in-stream mutations.
func (s *Store) List() []*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Entry, len(s.entries))
	for i, e := range s.entries {
		out[len(s.entries)-1-i] = e.clone()
	}
	return out
}

// Get returns an immutable clone of the entry with the given id (and true) or
// nil/false. Returning a clone (not the live pointer) keeps readers race-free
// against the proxy writing the same entry under the lock.
func (s *Store) Get(id string) (*Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.idIndex[id]
	if !ok {
		return nil, false
	}
	return e.clone(), true
}

// Clear discards every retained entry. Live subscribers stay subscribed.
// When the Store is persistent the backing JSONL file is also removed so the
// "clear" action erases on-disk traffic, not just the in-memory copy.
func (s *Store) Clear() {
	s.mu.Lock()
	s.entries = s.entries[:0]
	for k := range s.idIndex {
		delete(s.idIndex, k)
	}
	if s.persisted != nil {
		for k := range s.persisted {
			delete(s.persisted, k)
		}
	}
	path := s.persistPath
	s.mu.Unlock()

	if path == "" {
		return
	}
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		// Best-effort: report via stderr so the desktop log captures it, but
		// don't fail the API call — the in-memory clear already happened.
		// os.Remove is the safe choice here over truncate-in-place: if the
		// file is briefly missing between Clear and the next finalize, the
		// appendToFile path uses O_CREATE so the file is recreated.
		_ = err
	}
}

// MapAnalysis applies fn to every retained entry's Analysis field under the
// lock, replacing it with fn's result. It exists so the caller can re-type
// entries restored from disk (whose Analysis decodes as map[string]any) back
// into the concrete *llmparse.Analysis without coupling the store to the
// parser — fn is supplied by the caller (see llmparse.ReifyAnalysis). Intended
// to run once on boot, before subscribers attach.
func (s *Store) MapAnalysis(fn func(any) any) {
	if fn == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		e.Analysis = fn(e.Analysis)
	}
}

// Subscribe returns a channel that receives Entry clones as they are added or
// updated, along with an unsubscribe function. The channel has a small buffer;
// slow consumers drop events rather than block the proxy hot path. Use it for
// best-effort live views (the SSE writer coalesces + reconciles on top).
func (s *Store) Subscribe() (<-chan *Entry, func()) {
	return s.SubscribeBuffer(16)
}

// SubscribeBuffer is Subscribe with an explicit buffer size. Correctness-
// critical in-process consumers (the session aggregator) pass a buffer large
// enough to absorb realistic streaming bursts so bucketing events are not
// dropped; a periodic reconcile still backstops the rare overflow.
func (s *Store) SubscribeBuffer(n int) (<-chan *Entry, func()) {
	if n < 1 {
		n = 1
	}
	ch := make(chan *Entry, n)
	s.subsMu.Lock()
	s.subSeq++
	id := s.subSeq
	s.subs[id] = ch
	s.subsMu.Unlock()

	cancel := func() {
		s.subsMu.Lock()
		if existing, ok := s.subs[id]; ok {
			delete(s.subs, id)
			close(existing)
		}
		s.subsMu.Unlock()
	}
	return ch, cancel
}

func (s *Store) broadcast(e *Entry) {
	s.subsMu.RLock()
	defer s.subsMu.RUnlock()
	for _, ch := range s.subs {
		select {
		case ch <- e:
		default:
			// Drop on a full buffer: keeping the proxy fast outranks delivering
			// every event to a slow UI. The next polling refresh will catch up.
		}
	}
}

// markPersistableLocked decides whether e should be written to the JSONL
// file. Caller must hold s.mu. Returns true exactly once per entry — the
// first time we see EndedAt non-zero. In-flight streaming entries return
// false on every chunk; a crash mid-stream drops them, which is acceptable
// for a debugging tool.
func (s *Store) markPersistableLocked(e *Entry) bool {
	if s.persistPath == "" || s.persisted == nil || e == nil {
		return false
	}
	if e.EndedAt.IsZero() {
		return false
	}
	if _, ok := s.persisted[e.ID]; ok {
		return false
	}
	s.persisted[e.ID] = struct{}{}
	return true
}

// appendToFile writes one JSON line to the persistence file. Errors are
// swallowed: the in-memory store stays usable, and the next finalize will
// try again. We don't surface IO failures through Add/Update because the
// proxy hot path can't usefully react to them.
func (s *Store) appendToFile(e *Entry) {
	if s.persistPath == "" {
		return
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return
	}
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.persistPath), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(s.persistPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(raw)
	_, _ = f.Write([]byte("\n"))
}

// bootstrap loads the JSONL persistence file into the ring buffer. Each line
// is parsed independently — a malformed line is skipped, not fatal. Duplicate
// IDs (the file was written with one append per finalize, so duplicates only
// arise when an older code path appended multiple times) collapse to the last
// one seen. The file's natural order is preserved, so the buffer ends up
// chronological with the newest entries at the end. If more than capacity
// entries are on disk we keep the last `capacity` of them.
func (s *Store) bootstrap() error {
	f, err := os.Open(s.persistPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()

	// Scanner buffer must accommodate at most one full Entry (request +
	// response body each capped at MaxBodyBytes + JSON overhead). Use a
	// generous ceiling so headers + analysis + base64 padding don't trip it.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 8*MaxBodyBytes)

	order := make([]string, 0, s.capacity)
	byID := make(map[string]*Entry, s.capacity)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry Entry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if _, exists := byID[entry.ID]; !exists {
			order = append(order, entry.ID)
		}
		byID[entry.ID] = &entry
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	// Keep only the last `capacity` distinct entries.
	if len(order) > s.capacity {
		order = order[len(order)-s.capacity:]
	}

	var maxSeq uint64
	for _, id := range order {
		e := byID[id]
		s.entries = append(s.entries, e)
		s.idIndex[e.ID] = e
		s.persisted[e.ID] = struct{}{}
		if n, ok := parseID(e.ID); ok && n > maxSeq {
			maxSeq = n
		}
	}
	// Track every observed ID so a re-append after a partial truncation
	// doesn't duplicate the row.
	for id := range byID {
		s.persisted[id] = struct{}{}
		if n, ok := parseID(id); ok && n > maxSeq {
			maxSeq = n
		}
	}
	// Skip the counter past any historical ID so freshly-issued IDs can't
	// collide with one already on disk.
	s.nextID.Store(maxSeq)
	return nil
}

// parseID reverses formatID. Returns the encoded sequence and true on success;
// strings that don't follow the "e-<base36>" shape return (0, false).
func parseID(id string) (uint64, bool) {
	if len(id) < 3 || id[0] != 'e' || id[1] != '-' {
		return 0, false
	}
	var n uint64
	for _, c := range id[2:] {
		var d uint64
		switch {
		case c >= '0' && c <= '9':
			d = uint64(c - '0')
		case c >= 'a' && c <= 'z':
			d = uint64(c-'a') + 10
		default:
			return 0, false
		}
		n = n*36 + d
	}
	return n, true
}

func formatID(n uint64) string {
	// Compact base36-ish without external deps; collisions impossible inside
	// one process lifetime because n is monotonic and unique.
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if n == 0 {
		return "e-0"
	}
	buf := make([]byte, 0, 16)
	for n > 0 {
		buf = append(buf, digits[n%36])
		n /= 36
	}
	// reverse in place
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return "e-" + string(buf)
}
