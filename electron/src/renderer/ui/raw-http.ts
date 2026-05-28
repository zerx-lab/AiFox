// Raw HTTP view — render a captured request or response in HTTP/1.1 wire
// shape (request-line / headers / blank line / body). The body still goes
// through the existing JSON / SSE pretty-printer + highlighter; what's new
// here is the framing.

import { t } from "../i18n";
import { formatBody } from "./body";
import { highlight } from "./highlight";
import type { TrafficEntry } from "./state";

export function renderRawRequest(entry: TrafficEntry): HTMLElement {
  const startLine = `${entry.method || "?"} ${entry.url || "/"} HTTP/1.1`;
  return renderRaw(
    entry,
    startLine,
    entry.requestHeaders ?? {},
    entry.requestBody ?? "",
    "request",
  );
}

export function renderRawResponse(entry: TrafficEntry): HTMLElement {
  const code = entry.statusCode > 0 ? entry.statusCode : 0;
  const startLine = code > 0 ? `HTTP/1.1 ${code} ${statusText(code)}` : "HTTP/1.1 ··· pending";
  return renderRaw(
    entry,
    startLine,
    entry.responseHeaders ?? {},
    entry.responseBody ?? "",
    "response",
  );
}

function renderRaw(
  entry: TrafficEntry,
  startLine: string,
  headers: Record<string, string>,
  body: string,
  kind: "request" | "response",
): HTMLElement {
  const pre = document.createElement("pre");
  pre.className = "raw-http";

  // Start line in distinctive color.
  pre.appendChild(span(startLine, kind === "request" ? "rh-req" : "rh-resp"));
  pre.appendChild(text("\n"));

  for (const [k, v] of Object.entries(headers)) {
    pre.appendChild(span(k, "rh-key"));
    pre.appendChild(text(": "));
    pre.appendChild(span(redact(k, v), "rh-val"));
    pre.appendChild(text("\n"));
  }
  pre.appendChild(text("\n"));

  if (body) {
    const formatted = formatBody(body, headers);
    pre.appendChild(highlight(formatted.text, formatted.kind));
  } else {
    pre.appendChild(span(`(${t("detail.bodyEmpty")})`, "rh-empty"));
  }

  if (kind === "response" && entry.streaming && !entry.endedAt) {
    pre.appendChild(text("\n"));
    pre.appendChild(span(`▍ ${t("detail.streamingLive")}`, "rh-streaming"));
  }
  return pre;
}

function span(content: string, klass: string): HTMLSpanElement {
  const el = document.createElement("span");
  el.className = klass;
  el.textContent = content;
  return el;
}

function text(s: string): Text {
  return document.createTextNode(s);
}

function redact(key: string, value: string): string {
  const k = key.toLowerCase();
  if (k === "authorization" || k.includes("api-key") || k.includes("apikey")) {
    if (value.length <= 8) return "•".repeat(value.length);
    return `${value.slice(0, 4)}…${value.slice(-2)}`;
  }
  return value;
}

// Subset of canonical reason phrases. Anything not listed renders without a
// suffix — better than printing "Unknown" because the wire format is just
// status-code + reason-phrase and the phrase is informational.
const REASON: Record<number, string> = {
  200: "OK",
  201: "Created",
  202: "Accepted",
  204: "No Content",
  301: "Moved Permanently",
  302: "Found",
  304: "Not Modified",
  400: "Bad Request",
  401: "Unauthorized",
  403: "Forbidden",
  404: "Not Found",
  408: "Request Timeout",
  413: "Payload Too Large",
  422: "Unprocessable Entity",
  429: "Too Many Requests",
  500: "Internal Server Error",
  502: "Bad Gateway",
  503: "Service Unavailable",
  504: "Gateway Timeout",
};

function statusText(code: number): string {
  return REASON[code] ?? "";
}
