package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zerx-lab/ai-fox/internal/store"
)

// addEntry is a helper that creates and adds an entry to the store,
// returning the entry so the caller can further configure it.
func addEntry(st *store.Store, id, body string, streaming bool, ended bool) *store.Entry {
	e := &store.Entry{
		ID:           id,
		StartedAt:    time.Now(),
		Method:       "POST",
		URL:          "/v1/messages",
		ResponseBody: body,
		Streaming:    streaming,
	}
	if ended {
		e.EndedAt = time.Now()
	}
	st.Add(e)
	return e
}

func callTail(st *store.Store, id string, since int64) (*TrafficTailOutput, error) {
	handler := tailTrafficHandler(st)
	return handler(context.Background(), &TrafficTailInput{ID: id, Since: since})
}

// TestTailSinceZeroReturnsWholeBody: since=0 → AppendBytes is the full body.
func TestTailSinceZeroReturnsWholeBody(t *testing.T) {
	st := store.New(16)
	body := "hello world"
	addEntry(st, "t1", body, false, true)

	out, err := callTail(st, "t1", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Body.AppendBytes != body {
		t.Fatalf("AppendBytes = %q, want %q", out.Body.AppendBytes, body)
	}
}

// TestTailSinceInRangeReturnsSuffix: since in [1, len-1] → suffix only.
func TestTailSinceInRangeReturnsSuffix(t *testing.T) {
	st := store.New(16)
	body := "abcdefgh"
	addEntry(st, "t2", body, false, true)

	out, err := callTail(st, "t2", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := body[3:]
	if out.Body.AppendBytes != want {
		t.Fatalf("AppendBytes = %q, want %q", out.Body.AppendBytes, want)
	}
	if out.Body.ResponseSize != int64(len(body)) {
		t.Fatalf("ResponseSize = %d, want %d", out.Body.ResponseSize, len(body))
	}
}

// TestTailSinceEqualsLenReturnsEmpty: since == len(body) → empty appendBytes.
func TestTailSinceEqualsLenReturnsEmpty(t *testing.T) {
	st := store.New(16)
	body := "exact"
	addEntry(st, "t3", body, false, true)

	out, err := callTail(st, "t3", int64(len(body)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Body.AppendBytes != "" {
		t.Fatalf("AppendBytes = %q, want empty", out.Body.AppendBytes)
	}
}

// TestTailSinceBeyondLenClampsToEmpty: since > len(body) → no panic, empty result.
func TestTailSinceBeyondLenClampsToEmpty(t *testing.T) {
	st := store.New(16)
	body := "short"
	addEntry(st, "t4", body, false, true)

	out, err := callTail(st, "t4", int64(len(body))+9999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Body.AppendBytes != "" {
		t.Fatalf("AppendBytes = %q, want empty", out.Body.AppendBytes)
	}
}

// TestTailSinceNegativeClampsToZero: negative since → treated as 0, full body.
func TestTailSinceNegativeClampsToZero(t *testing.T) {
	st := store.New(16)
	body := "full content"
	addEntry(st, "t5", body, false, true)

	out, err := callTail(st, "t5", -42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Body.AppendBytes != body {
		t.Fatalf("AppendBytes = %q, want %q", out.Body.AppendBytes, body)
	}
}

// TestTailStreamingEntryDoneIsFalse: EndedAt zero → Done=false.
func TestTailStreamingEntryDoneIsFalse(t *testing.T) {
	st := store.New(16)
	addEntry(st, "t6", "partial", true, false /* not ended */)

	out, err := callTail(st, "t6", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Body.Done {
		t.Fatal("Done = true, want false for streaming (not finalized) entry")
	}
}

// TestTailFinalizedEntryDoneIsTrueWithCorrectBytes: EndedAt set → Done=true,
// and the tail byte count is correct.
func TestTailFinalizedEntryDoneIsTrueWithCorrectBytes(t *testing.T) {
	st := store.New(16)
	body := strings.Repeat("z", 100)
	addEntry(st, "t7", body, false, true /* ended */)

	since := int64(60)
	out, err := callTail(st, "t7", since)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.Body.Done {
		t.Fatal("Done = false, want true for finalized entry")
	}
	wantAppend := body[since:]
	if out.Body.AppendBytes != wantAppend {
		t.Fatalf("AppendBytes len = %d, want %d", len(out.Body.AppendBytes), len(wantAppend))
	}
}

// TestTailUnknownIDReturns404: unknown id → error (404).
func TestTailUnknownIDReturns404(t *testing.T) {
	st := store.New(16)

	_, err := callTail(st, "does-not-exist", 0)
	if err == nil {
		t.Fatal("expected error for unknown id, got nil")
	}
}
