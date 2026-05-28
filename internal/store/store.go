// Package store keeps an in-memory ring buffer of captured proxy traffic and
// fans new entries out to live subscribers (used by the SSE stream endpoint).
//
// Persistence is deliberately out of scope here: the desktop app is short-lived
// per session and the data is sensitive (raw prompts / API keys headers may
// leak). If/when on-disk capture is added, it should land in a separate
// package and respect the OS keyring decisions documented in CLAUDE.md.
package store

import (
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
}

// MaxBodyBytes caps the per-direction body size we keep in memory.
const MaxBodyBytes = 1 << 20 // 1 MiB

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
	s.mu.Unlock()

	s.broadcast(e)
}

// Update applies fn to the entry identified by id and re-broadcasts it.
// If the entry was already evicted, Update is a no-op.
func (s *Store) Update(id string, fn func(*Entry)) {
	s.mu.Lock()
	e, ok := s.idIndex[id]
	if ok {
		fn(e)
	}
	s.mu.Unlock()
	if ok {
		s.broadcast(e)
	}
}

// List returns a copy of the buffer, newest first.
func (s *Store) List() []*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Entry, len(s.entries))
	for i, e := range s.entries {
		out[len(s.entries)-1-i] = e
	}
	return out
}

// Get returns the entry with the given id (and true) or nil/false.
func (s *Store) Get(id string) (*Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.idIndex[id]
	return e, ok
}

// Clear discards every retained entry. Live subscribers stay subscribed.
func (s *Store) Clear() {
	s.mu.Lock()
	s.entries = s.entries[:0]
	for k := range s.idIndex {
		delete(s.idIndex, k)
	}
	s.mu.Unlock()
}

// Subscribe returns a channel that receives Entry pointers as they are added
// or updated, along with an unsubscribe function. The channel has a small
// buffer; slow consumers will drop events rather than block producers.
func (s *Store) Subscribe() (<-chan *Entry, func()) {
	ch := make(chan *Entry, 16)
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
