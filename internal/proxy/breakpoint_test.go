package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func mustAdd(t *testing.T, r *Registry, bp Breakpoint) Breakpoint {
	t.Helper()
	stored, err := r.Add(bp)
	if err != nil {
		t.Fatalf("add breakpoint: %v", err)
	}
	return stored
}

// TestHoldCtxCancelReturnsClientGone guards G5: a held request whose context is
// cancelled (the inbound client disconnected) resolves to DecisionClientGone,
// distinct from a user/registry-driven DecisionAbort.
func TestHoldCtxCancelReturnsClientGone(t *testing.T) {
	r := NewRegistry()
	bp := mustAdd(t, r, Breakpoint{Match: MatchEndpoint, Pattern: "POST /v1/messages", Enabled: true})

	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan Decision, 1)
	go func() { got <- r.Hold(ctx, bp.ID, "e-1", "POST", "/v1/messages") }()

	// Wait until the hold is registered, then cancel the client context.
	waitForPaused(t, r, 1)
	cancel()

	select {
	case d := <-got:
		if d != DecisionClientGone {
			t.Fatalf("ctx cancel should yield DecisionClientGone, got %v", d)
		}
	case <-time.After(time.Second):
		t.Fatal("Hold did not return after ctx cancel")
	}
	if len(r.PausedSnapshot()) != 0 {
		t.Fatal("hold should be cleared after ctx cancel")
	}
}

// TestDeleteReleasesHeld checks Delete continues an in-flight hold.
func TestDeleteReleasesHeld(t *testing.T) {
	r := NewRegistry()
	bp := mustAdd(t, r, Breakpoint{Match: MatchEndpoint, Pattern: "POST /x", Enabled: true})

	got := make(chan Decision, 1)
	go func() { got <- r.Hold(context.Background(), bp.ID, "e-1", "POST", "/x") }()
	waitForPaused(t, r, 1)

	r.Delete(bp.ID)
	select {
	case d := <-got:
		if d != DecisionContinue {
			t.Fatalf("Delete should continue the held request, got %v", d)
		}
	case <-time.After(time.Second):
		t.Fatal("Delete did not release the hold")
	}
}

// TestClearReleasesHeld checks Clear continues every in-flight hold.
func TestClearReleasesHeld(t *testing.T) {
	r := NewRegistry()
	bp := mustAdd(t, r, Breakpoint{Match: MatchEndpoint, Pattern: "POST /x", Enabled: true})

	got := make(chan Decision, 1)
	go func() { got <- r.Hold(context.Background(), bp.ID, "e-1", "POST", "/x") }()
	waitForPaused(t, r, 1)

	r.Clear()
	select {
	case d := <-got:
		if d != DecisionContinue {
			t.Fatalf("Clear should continue held requests, got %v", d)
		}
	case <-time.After(time.Second):
		t.Fatal("Clear did not release the hold")
	}
	if len(r.List()) != 0 {
		t.Fatal("Clear should remove all breakpoints")
	}
}

// TestDecideVsCtxRaceNoPanic exercises Continue/Abort racing with ctx
// cancellation on the same hold. Whichever wins, Hold must return exactly once
// without panicking or deadlocking.
func TestDecideVsCtxRaceNoPanic(t *testing.T) {
	for i := 0; i < 200; i++ {
		r := NewRegistry()
		bp := mustAdd(t, r, Breakpoint{Match: MatchEndpoint, Pattern: "POST /x", Enabled: true})
		ctx, cancel := context.WithCancel(context.Background())

		done := make(chan Decision, 1)
		go func() { done <- r.Hold(ctx, bp.ID, "e-1", "POST", "/x") }()
		waitForPaused(t, r, 1)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); cancel() }()
		go func() { defer wg.Done(); _ = r.Continue("e-1") }()
		wg.Wait()

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("Hold deadlocked under decide/ctx race")
		}
	}
}

// TestSecondMatchWhileHeldPassesThrough documents breakpoint.go's design: while
// one request is held on a breakpoint, a second matching request is let through
// (Match returns nil) rather than chaining, so the renderer can't deadlock.
func TestSecondMatchWhileHeldPassesThrough(t *testing.T) {
	r := NewRegistry()
	bp := mustAdd(t, r, Breakpoint{Match: MatchEndpoint, Pattern: "POST /v1/messages", Enabled: true})

	go func() { _ = r.Hold(context.Background(), bp.ID, "e-1", "POST", "/v1/messages") }()
	waitForPaused(t, r, 1)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	if got := r.Match(req); got != nil {
		t.Fatalf("second match while held should pass through (nil), got %+v", got)
	}

	// Release the first hold so the goroutine exits cleanly.
	_ = r.Continue("e-1")
}

func waitForPaused(t *testing.T, r *Registry, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(r.PausedSnapshot()) >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d paused request(s)", n)
}
