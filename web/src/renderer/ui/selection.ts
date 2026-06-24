// Selection controller: keeps AppState.selectedEntry (the full body + analysis)
// in sync with selectedId.
//
// The index SSE stream only carries lightweight EntryMeta, so the detail panes
// can't read bodies off the list. On selection we fetch GET /v1/traffic/{id}
// once. While that entry is still streaming we poll GET /v1/traffic/{id}/tail
// and APPEND only the new bytes (via appendSelectedBody → an in-place DOM patch
// in app.ts), instead of re-fetching + re-rendering the whole entry. That keeps
// the structured timeline/detail stable during a stream (they fill at finalize)
// and preserves the user's text selection. When the entry finalizes we fetch
// the full entry once more for the authoritative analysis.

import { fetchEntry, fetchTail } from "./api-service";
import {
  appendSelectedBody,
  getState,
  onChange,
  setSelectedEntry,
  type TrafficEntry,
} from "./state";

const POLL_MS = 250;
const encoder = new TextEncoder();
const byteLength = (s: string) => encoder.encode(s).length;

let trackedId: string | null = null;
let pollTimer: number | null = null;
// Consecutive tail failures (e.g. a 404 after the entry was evicted from the
// ring buffer); cap retries so a vanished entry doesn't poll forever.
let tailFailures = 0;
const MAX_TAIL_FAILURES = 8;
// Monotonic token so a slow fetch for a since-changed selection can't clobber
// the current selectedEntry when it finally resolves.
let fetchToken = 0;

export function initSelection() {
  onChange(() => {
    void reconcile();
  });
  void reconcile();
}

async function reconcile() {
  const id = getState().selectedId;
  if (id === trackedId) return; // selection unchanged — ignore unrelated notifies
  trackedId = id;
  tailFailures = 0;
  stopPoll();
  if (!id) {
    setSelectedEntry(null);
    return;
  }
  // Drop the stale full entry so detail panes show their loading/empty state
  // until the new fetch lands (avoids briefly rendering the previous entry).
  setSelectedEntry(null);
  await loadOnce(id);
}

async function loadOnce(id: string) {
  const token = ++fetchToken;
  const res = await fetchEntry(id);
  if (token !== fetchToken || getState().selectedId !== id) return;
  if (!res.ok) return; // service already toasted the failure
  const entry = res.data;
  setSelectedEntry(entry);
  if (!isFinished(entry)) {
    scheduleTail(id);
  }
}

function scheduleTail(id: string) {
  stopPoll();
  pollTimer = window.setTimeout(() => {
    pollTimer = null;
    if (getState().selectedId !== id) return;
    void pollTail(id);
  }, POLL_MS);
}

// pollTail appends the bytes streamed since our current offset; on completion it
// re-fetches the full entry once for the finalized analysis.
async function pollTail(id: string) {
  const cur = getState().selectedEntry;
  if (!cur || cur.id !== id) return;
  // The Go /tail handler slices the body by BYTE offset; a JS string .length is
  // UTF-16 code units. For non-ASCII responses (e.g. CJK) those differ, which
  // would re-send/duplicate bytes and cut mid-rune. Send the UTF-8 byte length.
  const since = byteLength(cur.responseBody ?? "");
  const tail = await fetchTail(id, since);
  if (getState().selectedId !== id) return;
  if (!tail) {
    // Transient failure or the entry was evicted (404). Retry a bounded number
    // of times, then give up — the next selection does a full reload.
    if (++tailFailures <= MAX_TAIL_FAILURES) scheduleTail(id);
    return;
  }
  tailFailures = 0;
  if (tail.appendBytes) {
    appendSelectedBody(id, tail.appendBytes, tail.responseSize);
  }
  if (tail.done) {
    await loadOnce(id); // final authoritative fetch (analysis + clean body)
    return;
  }
  scheduleTail(id);
}

function stopPoll() {
  if (pollTimer !== null) {
    window.clearTimeout(pollTimer);
    pollTimer = null;
  }
}

function isFinished(e: TrafficEntry): boolean {
  // Go's zero time marshals to a pre-1970 string (epoch <= 0); a real finish
  // timestamp is positive. Mirrors format.ts isPending.
  const ended = e.endedAt ? new Date(e.endedAt).getTime() : 0;
  return ended > 0;
}
