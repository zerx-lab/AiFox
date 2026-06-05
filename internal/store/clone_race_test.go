package store

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

// TestCloneIsolatesReadersFromWriter proves that Get and List hand out clones
// rather than the live pointer. The race detector is the real assertion:
// concurrent reads and writes must produce no data races. Additionally, a
// captured broadcast clone must not reflect later mutations — proof that
// broadcast also sends a clone.
func TestCloneIsolatesReadersFromWriter(t *testing.T) {
	const iterations = 2000

	st := New(16)
	e := newEntry("race-1")
	st.Add(e)

	var wg sync.WaitGroup

	// Writer: repeatedly Update mutating ResponseBody (string field) and
	// Analysis (pointer field) under the store lock.
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := &bytes.Buffer{}
		for i := 0; i < iterations; i++ {
			st.Update("race-1", func(e *Entry) {
				// Grow the string so a clone taken at any point has a different length.
				e.ResponseBody = e.ResponseBody + "x"
				// Alternate between nil and a non-nil pointer (copy-on-write: we
				// always reassign, never mutate the pointed-to value in place).
				if i%2 == 0 {
					e.Analysis = any(buf) // non-nil *bytes.Buffer
				} else {
					e.Analysis = nil
				}
			})
		}
	}()

	// Readers: concurrently loop reading fields from Get and List snapshots.
	// No t.Fatal/Fatalf inside goroutines — the race detector is the assertion.
	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if g, ok := st.Get("race-1"); ok {
					_ = len(g.ResponseBody)
					_ = g.Analysis != nil
				}
				clones := st.List()
				for _, c := range clones {
					_ = len(c.ResponseBody)
					_ = c.Analysis != nil
				}
			}
		}()
	}

	wg.Wait()
}

// TestBroadcastCloneIsolation proves that a clone received on a Subscribe
// channel does not alias the live entry: further Updates must not modify the
// captured snapshot.
func TestBroadcastCloneIsolation(t *testing.T) {
	st := New(16)
	// Add the initial entry (broadcasts snapshot; subscribe AFTER).
	st.Add(newEntry("iso-1"))

	ch, cancel := st.Subscribe()
	defer cancel()

	// Update with a known body so the subscriber receives it.
	const knownBody = "AAAA"
	st.Update("iso-1", func(e *Entry) {
		e.ResponseBody = knownBody
	})

	// Receive the broadcast clone with a short timeout.
	var captured *Entry
	select {
	case captured = <-ch:
	case <-time.After(time.Second):
		t.Fatal("did not receive broadcast within 1s")
	}

	if captured.ResponseBody != knownBody {
		t.Fatalf("captured clone ResponseBody = %q, want %q", captured.ResponseBody, knownBody)
	}

	// Now run more Updates that grow the body. The captured clone must NOT change.
	for i := 0; i < 50; i++ {
		st.Update("iso-1", func(e *Entry) {
			e.ResponseBody = e.ResponseBody + "more"
		})
	}

	// Drain the channel (not strictly necessary, but avoids goroutine leak).
	for {
		select {
		case <-ch:
		default:
			goto drained
		}
	}
drained:

	if captured.ResponseBody != knownBody {
		t.Fatalf("captured clone was mutated: ResponseBody = %q, want %q",
			captured.ResponseBody, knownBody)
	}
}
