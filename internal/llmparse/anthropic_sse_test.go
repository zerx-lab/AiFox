package llmparse

import "testing"

// TestSplitSSE_CRLF guards G3: a stream rewritten to \r\n line endings must
// frame identically to one using \n, with no trailing \r leaking into event
// names or data values.
func TestSplitSSE_CRLF(t *testing.T) {
	body := "event: message_start\r\ndata: {\"a\":1}\r\n\r\nevent: message_stop\r\ndata: {}\r\n\r\n"
	events := splitSSE(body)
	if len(events) != 2 {
		t.Fatalf("want 2 events, got %d: %+v", len(events), events)
	}
	if events[0].Event != "message_start" {
		t.Fatalf("event name has stray CR or is wrong: %q", events[0].Event)
	}
	if events[0].Data != `{"a":1}` {
		t.Fatalf("data has stray CR or is wrong: %q", events[0].Data)
	}
	if events[1].Event != "message_stop" || events[1].Data != "{}" {
		t.Fatalf("second event wrong: %+v", events[1])
	}
}

// TestSplitSSE_MultilineData checks the spec's data-line concatenation (joined
// with \n) survives CRLF normalization.
func TestSplitSSE_MultilineData(t *testing.T) {
	body := "data: line1\r\ndata: line2\r\n\r\n"
	events := splitSSE(body)
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	if events[0].Data != "line1\nline2" {
		t.Fatalf("multiline data not concatenated with LF: %q", events[0].Data)
	}
}

// TestSplitSSE_HalfEventAndEmptyData covers a trailing half event (no blank-line
// terminator) and an empty data line.
func TestSplitSSE_HalfEventAndEmptyData(t *testing.T) {
	// Empty data line then a half event with no terminating blank line.
	body := "event: ping\ndata:\n\nevent: partial\ndata: tail"
	events := splitSSE(body)
	if len(events) != 2 {
		t.Fatalf("want 2 events (incl. half), got %d: %+v", len(events), events)
	}
	if events[0].Event != "ping" || events[0].Data != "" {
		t.Fatalf("empty data event wrong: %+v", events[0])
	}
	if events[1].Event != "partial" || events[1].Data != "tail" {
		t.Fatalf("half event not captured: %+v", events[1])
	}
}
