package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newEntry(id string) *Entry {
	return &Entry{ID: id, StartedAt: time.Now(), Method: "POST", URL: "/v1/messages"}
}

func TestRingBufferEvictsOldest(t *testing.T) {
	s := New(2)
	s.Add(newEntry("a"))
	s.Add(newEntry("b"))
	s.Add(newEntry("c")) // evicts a

	list := s.List()
	if len(list) != 2 {
		t.Fatalf("want 2 entries, got %d", len(list))
	}
	if list[0].ID != "c" || list[1].ID != "b" {
		t.Fatalf("newest-first order wrong: %s, %s", list[0].ID, list[1].ID)
	}
	if _, ok := s.Get("a"); ok {
		t.Fatalf("entry a should have been evicted")
	}
}

func TestSubscribeReceivesNewEntries(t *testing.T) {
	s := New(4)
	ch, cancel := s.Subscribe()
	defer cancel()

	e := newEntry("x")
	s.Add(e)

	select {
	case got := <-ch:
		if got.ID != "x" {
			t.Fatalf("got %s, want x", got.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive entry")
	}
}

func TestUpdateBroadcastsAndPersists(t *testing.T) {
	s := New(4)
	ch, cancel := s.Subscribe()
	defer cancel()

	s.Add(newEntry("y"))
	<-ch // drain initial

	s.Update("y", func(e *Entry) { e.StatusCode = 200 })

	select {
	case got := <-ch:
		if got.StatusCode != 200 {
			t.Fatalf("update did not propagate, status=%d", got.StatusCode)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber missed update")
	}

	persisted, ok := s.Get("y")
	if !ok || persisted.StatusCode != 200 {
		t.Fatalf("update not persisted in store")
	}
}

func TestPersistentReloadsFinalizedEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "traffic.jsonl")

	s, err := NewPersistent(4, path)
	if err != nil {
		t.Fatalf("NewPersistent: %v", err)
	}

	// In-flight entry: not yet finalized. Should not land on disk.
	live := newEntry(s.NextID())
	s.Add(live)

	done := newEntry(s.NextID())
	done.EndedAt = time.Now()
	s.Add(done)

	// Reopen: only the finalized entry should be restored.
	s2, err := NewPersistent(4, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, ok := s2.Get(live.ID); ok {
		t.Fatalf("in-flight entry should not have been persisted")
	}
	got, ok := s2.Get(done.ID)
	if !ok {
		t.Fatalf("finalized entry missing after reload")
	}
	if got.ID != done.ID {
		t.Fatalf("reloaded id mismatch: got %s, want %s", got.ID, done.ID)
	}

	// nextID must skip past any restored ID so a freshly-issued one cannot
	// collide with what we read back.
	next := s2.NextID()
	if _, exists := s2.Get(next); exists {
		t.Fatalf("new id %s collides with restored entry", next)
	}
}

func TestPersistentClearRemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "traffic.jsonl")

	s, err := NewPersistent(4, path)
	if err != nil {
		t.Fatalf("NewPersistent: %v", err)
	}
	e := newEntry(s.NextID())
	e.EndedAt = time.Now()
	s.Add(e)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected persistence file to exist, got %v", err)
	}

	s.Clear()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected persistence file to be removed, stat err=%v", err)
	}

	// A reopen after clear sees nothing.
	s2, err := NewPersistent(4, path)
	if err != nil {
		t.Fatalf("reopen after clear: %v", err)
	}
	if got := s2.List(); len(got) != 0 {
		t.Fatalf("expected empty store after clear+reopen, got %d", len(got))
	}
}

func TestNextIDIsMonotonic(t *testing.T) {
	s := New(4)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := s.NextID()
		if seen[id] {
			t.Fatalf("duplicate id %s", id)
		}
		seen[id] = true
	}
}

// TestEvictClearsPersistedAndIDIndex guards G4: when the ring buffer evicts an
// entry, both idIndex and the persisted-dedup marker must be cleared so neither
// map grows without bound on a long-running process.
func TestEvictClearsPersistedAndIDIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.jsonl")
	s, err := NewPersistent(2, path)
	if err != nil {
		t.Fatalf("new persistent: %v", err)
	}
	finalized := func(id string) *Entry {
		e := newEntry(id)
		e.EndedAt = time.Now() // mark finalized so it persists (and gets a marker)
		return e
	}
	s.Add(finalized("a"))
	s.Add(finalized("b"))
	s.Add(finalized("c")) // evicts a

	s.mu.RLock()
	_, idxOK := s.idIndex["a"]
	_, persistOK := s.persisted["a"]
	idxLen := len(s.idIndex)
	persistLen := len(s.persisted)
	s.mu.RUnlock()

	if idxOK {
		t.Fatalf("idIndex should not retain evicted entry a")
	}
	if persistOK {
		t.Fatalf("persisted should not retain evicted entry a")
	}
	if idxLen != 2 || persistLen != 2 {
		t.Fatalf("maps should track only live entries, got idIndex=%d persisted=%d", idxLen, persistLen)
	}
}

// bodyEntry builds a finalized entry whose response body is n bytes, so tests
// can drive the byte-budget accounting with predictable sizes.
func bodyEntry(id string, n int) *Entry {
	e := newEntry(id)
	e.ResponseBody = strings.Repeat("x", n)
	return e
}

// TestByteBudgetEvictsLargeBodies: with a small byte budget, adding large-body
// entries evicts the oldest by byte cost even though the count cap is far from
// reached.
func TestByteBudgetEvictsLargeBodies(t *testing.T) {
	s := New(100) // count cap is irrelevant here
	s.byteBudget = 250

	s.Add(bodyEntry("a", 100))
	s.Add(bodyEntry("b", 100))
	// Adding a third 100-byte entry pushes total to 300 > 250 budget, so the
	// oldest (a) is evicted back under budget.
	s.Add(bodyEntry("c", 100))

	if _, ok := s.Get("a"); ok {
		t.Fatalf("entry a should have been evicted by byte budget")
	}
	if _, ok := s.Get("b"); !ok {
		t.Fatalf("entry b should still be present")
	}
	if _, ok := s.Get("c"); !ok {
		t.Fatalf("entry c should still be present")
	}

	s.mu.RLock()
	cur := s.curBytes
	nEntries := len(s.entries)
	nBytesIdx := len(s.entryBytes)
	s.mu.RUnlock()
	if cur != 200 {
		t.Fatalf("curBytes = %d, want 200", cur)
	}
	if nEntries != 2 || nBytesIdx != 2 {
		t.Fatalf("entries=%d entryBytes=%d, want 2/2", nEntries, nBytesIdx)
	}
}

// TestByteBudgetKeepsSingleOversizeEntry: an entry larger than the whole budget
// is retained rather than discarded the moment it lands (we never evict below
// one entry, and never evict the just-added entry).
func TestByteBudgetKeepsSingleOversizeEntry(t *testing.T) {
	s := New(100)
	s.byteBudget = 100

	s.Add(bodyEntry("big", 500)) // 5x the budget
	if _, ok := s.Get("big"); !ok {
		t.Fatalf("oversize entry must be retained, not dropped on arrival")
	}

	// A second small entry: the oversize one is now the oldest. evictToBudget
	// must NOT evict it just to fit the small one (it would empty the buffer of
	// the only-over-budget item we just decided to keep). Instead the older big
	// entry is evicted because it is no longer the just-added keepID.
	s.Add(bodyEntry("small", 10))
	if _, ok := s.Get("big"); ok {
		t.Fatalf("older oversize entry should be evicted once a newer entry arrives over budget")
	}
	if _, ok := s.Get("small"); !ok {
		t.Fatalf("newest entry must be retained")
	}
}

// TestByteBudgetEvictsOnStreamingUpdate: a streaming Update that grows
// ResponseBody re-accounts the entry and triggers eviction of older entries
// once the growth pushes total bytes over budget.
func TestByteBudgetEvictsOnStreamingUpdate(t *testing.T) {
	s := New(100)
	s.byteBudget = 250

	s.Add(bodyEntry("a", 100))
	s.Add(bodyEntry("b", 100)) // total 200, both fit

	// Stream-grow b's body from 100 -> 200 bytes. Total would be 300 > 250, so
	// the oldest (a) must be evicted; b (the one being updated) is kept.
	s.Update("b", func(e *Entry) {
		e.ResponseBody = strings.Repeat("y", 200)
	})

	if _, ok := s.Get("a"); ok {
		t.Fatalf("entry a should be evicted after b's streaming growth")
	}
	if _, ok := s.Get("b"); !ok {
		t.Fatalf("entry b (actively updated) must be retained")
	}

	s.mu.RLock()
	cur := s.curBytes
	s.mu.RUnlock()
	if cur != 200 {
		t.Fatalf("curBytes = %d, want 200 (only b's grown body)", cur)
	}
}
