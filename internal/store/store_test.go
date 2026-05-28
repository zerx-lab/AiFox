package store

import (
	"os"
	"path/filepath"
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
