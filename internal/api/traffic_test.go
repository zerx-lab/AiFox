package api

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

// TestTailRuneBoundarySinceMidRune: when `since` lands inside a multi-byte rune,
// the handler backs it up to the rune start so the client never gets a partial
// rune. Across two tails the concatenated bytes must equal the full body.
func TestTailRuneBoundarySinceMidRune(t *testing.T) {
	st := store.New(16)
	// "a" (1B) + "你" (3B, e4 bd a0) + "好" (3B). Total 7 bytes. A since of 2 lands
	// inside the first Chinese rune (1 + 1 of 3).
	body := "a你好"
	addEntry(st, "r1", body, false, true)

	out, err := callTail(st, "r1", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// since=2 backs up to 1 (start of 你), so AppendBytes is the whole "你好".
	if out.Body.AppendBytes != "你好" {
		t.Fatalf("AppendBytes = %q, want %q", out.Body.AppendBytes, "你好")
	}
	// AppendBytes must be valid UTF-8 (no split rune).
	if !utf8.ValidString(out.Body.AppendBytes) {
		t.Fatalf("AppendBytes is not valid UTF-8: %q", out.Body.AppendBytes)
	}
}

// TestTailRuneBoundaryStreamingTrimsEnd: while an entry is still streaming, the
// emitted slice end is trimmed back to the last complete rune so a body whose
// current tail is a partial multi-byte rune doesn't ship half a character.
func TestTailRuneBoundaryStreamingTrimsEnd(t *testing.T) {
	st := store.New(16)
	// Streaming body whose last rune is incomplete: "ab" + first 2 bytes of "好".
	full := "好"             // 3 bytes
	body := "ab" + full[:2] // 2 + 2 = 4 bytes, last rune truncated
	addEntry(st, "r2", body, true /*streaming*/, false /*not ended*/)

	out, err := callTail(st, "r2", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// End is trimmed back to 2 (drop the dangling partial rune); only "ab" ships.
	if out.Body.AppendBytes != "ab" {
		t.Fatalf("AppendBytes = %q, want %q (partial trailing rune trimmed)", out.Body.AppendBytes, "ab")
	}
	if out.Body.Done {
		t.Fatalf("streaming entry should report Done=false")
	}
	// ResponseSize still reports the true total so the renderer's display stays honest.
	if out.Body.ResponseSize != int64(len(body)) {
		t.Fatalf("ResponseSize = %d, want %d", out.Body.ResponseSize, len(body))
	}
}

// TestTailRuneBoundaryFinalizedEmitsThrough: once finalized the body is whole, so
// the end is NOT trimmed even though the byte stream ends on a multi-byte rune.
func TestTailRuneBoundaryFinalizedEmitsThrough(t *testing.T) {
	st := store.New(16)
	body := "ab好" // finalized; trailing rune is complete
	addEntry(st, "r3", body, false, true /*ended*/)

	out, err := callTail(st, "r3", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Body.AppendBytes != body {
		t.Fatalf("finalized AppendBytes = %q, want full %q", out.Body.AppendBytes, body)
	}
}

// TestTailRuneBoundaryEmojiAnyOffset: an emoji (4-byte rune) sliced at every
// interior offset always realigns to its rune start, so the appended slice is
// valid UTF-8 and concatenation reconstructs the body exactly.
func TestTailRuneBoundaryEmojiAnyOffset(t *testing.T) {
	st := store.New(16)
	body := "x🦊y" // 1 + 4 + 1 = 6 bytes; 🦊 spans bytes [1,5)
	addEntry(st, "r4", body, false, true)

	for since := int64(0); since <= int64(len(body)); since++ {
		out, err := callTail(st, "r4", since)
		if err != nil {
			t.Fatalf("since=%d: unexpected error: %v", since, err)
		}
		if !utf8.ValidString(out.Body.AppendBytes) {
			t.Fatalf("since=%d: AppendBytes not valid UTF-8: %q", since, out.Body.AppendBytes)
		}
		// AppendBytes must be a suffix of body once both ends are rune-aligned:
		// it should always be reconstructable as the tail of body.
		if !strings.HasSuffix(body, out.Body.AppendBytes) {
			t.Fatalf("since=%d: AppendBytes %q is not a suffix of body %q", since, out.Body.AppendBytes, body)
		}
	}
}
