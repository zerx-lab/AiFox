// Body formatting: try to render JSON / SSE responses in a readable shape,
// fall back to the raw string on any parse failure. The decision is driven
// by Content-Type first, then by a cheap shape sniff so untyped responses
// still get prettified when they look like JSON or SSE.

export interface FormattedBody {
  /** Text to render inside the codebox. */
  text: string;
  /** "json" | "sse" | "raw" — surfaced to the UI as a small label. */
  kind: "json" | "sse" | "raw";
}

export function formatBody(
  body: string,
  headers: Record<string, string> | null | undefined,
): FormattedBody {
  if (!body) return { text: body, kind: "raw" };
  const ct = headerLower(headers, "content-type");

  // SSE: a transcript of Server-Sent Events. We pretty-print the `data:`
  // payload of each event if it's JSON; everything else stays verbatim so
  // the framing remains readable.
  if (ct.includes("text/event-stream") || looksLikeSse(body)) {
    return { text: formatSse(body), kind: "sse" };
  }

  // JSON: parse + 2-space indent. Triggers on `application/json`, on
  // `application/<vendor>+json`, or when the payload structurally starts
  // with `{` / `[` (covers responses with no Content-Type at all).
  if (ct.includes("json") || looksLikeJson(body)) {
    const pretty = tryFormatJson(body);
    if (pretty !== null) return { text: pretty, kind: "json" };
  }

  return { text: body, kind: "raw" };
}

function tryFormatJson(body: string): string | null {
  try {
    const parsed = JSON.parse(body);
    return JSON.stringify(parsed, null, 2);
  } catch {
    return null;
  }
}

function looksLikeJson(body: string): boolean {
  const s = body.trimStart();
  return s.startsWith("{") || s.startsWith("[");
}

function looksLikeSse(body: string): boolean {
  // Either the first line, or any line after a blank-line event boundary,
  // begins with one of the SSE field names.
  return /^(event|data|id|retry):/m.test(body);
}

function formatSse(body: string): string {
  const parts: string[] = [];
  for (const rawEvent of body.split(/\n\n/)) {
    const event = rawEvent.replace(/\n+$/, "");
    if (event.length === 0) continue;
    const lines: string[] = [];
    for (const line of event.split("\n")) {
      if (line === "" || line.startsWith(":")) {
        lines.push(line);
        continue;
      }
      const idx = line.indexOf(":");
      const field = idx === -1 ? line : line.slice(0, idx);
      const value = idx === -1 ? "" : line.slice(idx + 1).replace(/^ /, "");
      if (field === "data") {
        const pretty = tryFormatJson(value);
        if (pretty !== null) {
          lines.push("data:");
          for (const l of pretty.split("\n")) lines.push(`  ${l}`);
          continue;
        }
      }
      lines.push(line);
    }
    parts.push(lines.join("\n"));
  }
  return parts.join("\n\n");
}

function headerLower(
  headers: Record<string, string> | null | undefined,
  name: string,
): string {
  if (!headers) return "";
  const lower = name.toLowerCase();
  for (const [k, v] of Object.entries(headers)) {
    if (k.toLowerCase() === lower) return v.toLowerCase();
  }
  return "";
}
