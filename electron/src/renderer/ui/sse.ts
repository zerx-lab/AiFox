// Manual SSE consumer.
//
// EventSource has no API to attach custom headers, but the Go backend's auth
// middleware requires X-Ai-fox-Token. We use fetch + ReadableStream and parse
// the SSE wire format by hand. The grammar we actually need is small:
//
//   event: <name>\n
//   data: <json>\n
//   \n            ← dispatch
//
// Comment lines (starting with ":") are ignored.

import { AUTH_HEADER, getHandshake } from "../../api/client";

export interface SseEvent {
  event: string;
  data: string;
}

export interface SseHandle {
  close(): void;
}

export type SseHandler = (ev: SseEvent) => void;

/** Callback invoked when the SSE stream ends or errors (not called when
 *  close() is used intentionally). */
export type SseCloseHandler = () => void;

/** Subscribe to a streaming endpoint on the backend. The returned handle's
 *  close() aborts the underlying fetch and stops the parser loop.
 *  onClose is called when the stream ends unexpectedly (error or server
 *  disconnect) so the caller can implement reconnection. */
export async function openSse(
  path: string,
  onEvent: SseHandler,
  onClose?: SseCloseHandler,
): Promise<SseHandle> {
  const hs = await getHandshake();
  const ctrl = new AbortController();
  const url = hs.baseUrl.replace(/\/$/, "") + path;

  let stopped = false;
  const handle: SseHandle = {
    close() {
      stopped = true;
      ctrl.abort();
    },
  };

  // Fire-and-forget; reconnection is the caller's job.
  void (async () => {
    try {
      const resp = await fetch(url, {
        headers: { [AUTH_HEADER]: hs.token, Accept: "text/event-stream" },
        signal: ctrl.signal,
      });
      if (!resp.ok || !resp.body) {
        if (!stopped) onClose?.();
        return;
      }
      const reader = resp.body.getReader();
      const decoder = new TextDecoder();
      let buf = "";
      while (!stopped) {
        const { value, done } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        // SSE events are separated by a blank line ("\n\n").
        while (true) {
          const idx = buf.indexOf("\n\n");
          if (idx === -1) break;
          const raw = buf.slice(0, idx);
          buf = buf.slice(idx + 2);
          const parsed = parseEvent(raw);
          if (parsed) onEvent(parsed);
        }
      }
    } catch (_err) {
      // Aborted fetch throws; that's expected on close. Other errors signal
      // an unexpected disconnect — notify the caller.
    }
    if (!stopped) onClose?.();
  })();

  return handle;
}

function parseEvent(raw: string): SseEvent | null {
  let event = "message";
  const dataLines: string[] = [];
  for (const line of raw.split("\n")) {
    if (!line || line.startsWith(":")) continue;
    const idx = line.indexOf(":");
    const field = idx === -1 ? line : line.slice(0, idx);
    const value = idx === -1 ? "" : line.slice(idx + 1).replace(/^ /, "");
    if (field === "event") event = value;
    else if (field === "data") dataLines.push(value);
  }
  if (dataLines.length === 0) return null;
  return { event, data: dataLines.join("\n") };
}
