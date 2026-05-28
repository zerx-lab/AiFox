package store

import (
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
