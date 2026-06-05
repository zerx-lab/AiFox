// Collapsible JSON tree for request/response bodies. Renders a parsed JSON
// value as a DOM tree where every non-empty object/array can be folded. Built
// with plain DOM (no deps — same spirit as highlight.ts) and reuses the
// existing `tok-*` syntax classes so colours follow the theme.
//
// Fold state survives re-renders: each foldable node has a stable key
// (`<entryId>|<kind>|<path>`) and `getState().jsonFold` holds the keys whose
// fold DIFFERS from the node's default. Clicking toggles the DOM class AND the
// Set without bumping any version slice (the DOM is already updated), so the
// detail region rebuilding for unrelated reasons re-derives the same fold for
// free.

import { h } from "./dom";
import { getState } from "./state";

export interface JsonTreeOpts {
  /** Stable prefix identifying the body, e.g. `${entry.id}|response`. */
  keyPrefix: string;
  /** Containers with more children than this start collapsed by default. */
  collapseOver?: number;
  /** Collapse the top-level container by default (used for noisy SSE events). */
  collapseRoot?: boolean;
}

// SSE streams with more events than this fall back to the flat highlighter:
// a real /v1/messages stream is mostly content_block_delta events (hundreds to
// thousands) and one collapsible tree per event would be a wall of widgets that
// is slow to build. The caller surfaces a note when it trips this.
export const SSE_FOLD_MAX_EVENTS = 200;
// Above this many events, each event's `data` payload starts collapsed so the
// view is a scannable list of event lines rather than a fully-expanded dump.
const SSE_DATA_COLLAPSE_OVER = 6;

export function renderJsonTree(value: unknown, opts: JsonTreeOpts): HTMLElement {
  const root = h("div.jt-root");
  root.appendChild(buildEntry(null, value, "$", true, opts, 0));
  return root;
}

// renderSseTree renders an SSE transcript: each event's framing lines as text
// and its JSON `data:` payload as a collapsible tree. Returns null when the
// event count exceeds SSE_FOLD_MAX_EVENTS so the caller can fall back.
export function renderSseTree(body: string, opts: { keyPrefix: string }): HTMLElement | null {
  const events = splitSseEvents(body);
  if (events.length === 0 || events.length > SSE_FOLD_MAX_EVENTS) return null;
  const collapseData = events.length > SSE_DATA_COLLAPSE_OVER;
  const root = h("div.jt-sse");
  events.forEach((evt, idx) => {
    root.appendChild(renderSseEvent(evt, idx, collapseData, opts.keyPrefix));
  });
  return root;
}

export function sseEventCount(body: string): number {
  return splitSseEvents(body).length;
}

// --- JSON tree ----------------------------------------------------------

function buildEntry(
  keyText: string | null,
  value: unknown,
  path: string,
  isLast: boolean,
  opts: JsonTreeOpts,
  depth: number,
): HTMLElement {
  if (value !== null && typeof value === "object") {
    const isArray = Array.isArray(value);
    const entries: [string, unknown][] = isArray
      ? (value as unknown[]).map((v, i) => [String(i), v])
      : Object.entries(value as Record<string, unknown>);
    if (entries.length > 0) {
      return buildContainer(keyText, entries, isArray, path, isLast, opts, depth);
    }
    // Empty object/array renders as an inline leaf — nothing to fold.
    return leafRow(keyText, punct(isArray ? "[]" : "{}"), isLast);
  }
  return leafRow(keyText, primitive(value), isLast);
}

function buildContainer(
  keyText: string | null,
  entries: [string, unknown][],
  isArray: boolean,
  path: string,
  isLast: boolean,
  opts: JsonTreeOpts,
  depth: number,
): HTMLElement {
  const open = isArray ? "[" : "{";
  const close = isArray ? "]" : "}";
  const key = `${opts.keyPrefix}|${path}`;
  const overflow = entries.length > (opts.collapseOver ?? Number.POSITIVE_INFINITY);
  const defCollapsed = overflow || (depth === 0 && !!opts.collapseRoot);
  // jsonFold stores keys whose state DIFFERS from default, so membership flips it.
  const collapsed = getState().jsonFold.has(key) ? !defCollapsed : defCollapsed;

  const node = h("div", {
    class: `jt-node${collapsed ? " collapsed" : ""}`,
    dataset: { jkey: key, jdef: defCollapsed ? "1" : "0" },
  });

  const head = h("div.jt-head");
  head.appendChild(span("", "jt-toggle"));
  if (keyText !== null) {
    head.appendChild(keySpan(keyText));
    head.appendChild(punct(": "));
  }
  head.appendChild(punct(open));
  // Collapsed-only hint: `…}` (plus trailing comma) and a dim child count.
  const hint = h("span.jt-hint");
  hint.appendChild(text("…"));
  hint.appendChild(punct(close));
  if (!isLast) hint.appendChild(punct(","));
  hint.appendChild(span(` ${entries.length}`, "jt-count"));
  head.appendChild(hint);
  head.addEventListener("click", (ev) => onToggle(ev, node, head));

  const kids = h("div.jt-children");
  entries.forEach(([k, v], i) => {
    const childKey = isArray ? null : k;
    kids.appendChild(
      buildEntry(childKey, v, `${path}/${k}`, i === entries.length - 1, opts, depth + 1),
    );
  });

  const foot = h("div.jt-foot");
  foot.appendChild(punct(close));
  if (!isLast) foot.appendChild(punct(","));

  node.appendChild(head);
  node.appendChild(kids);
  node.appendChild(foot);
  return node;
}

function leafRow(keyText: string | null, valueEl: HTMLElement, isLast: boolean): HTMLElement {
  const row = h("div.jt-row");
  if (keyText !== null) {
    row.appendChild(keySpan(keyText));
    row.appendChild(punct(": "));
  }
  row.appendChild(valueEl);
  if (!isLast) row.appendChild(punct(","));
  return row;
}

function onToggle(ev: MouseEvent, node: HTMLElement, head: HTMLElement) {
  // Don't toggle when the user is selecting text inside this head — keeps keys
  // and values copyable by drag-select.
  const sel = window.getSelection();
  if (sel && !sel.isCollapsed && sel.anchorNode && head.contains(sel.anchorNode)) return;
  ev.stopPropagation();
  const target = !node.classList.contains("collapsed");
  if (ev.altKey) {
    // Recursive: fold/unfold this node and every descendant to the same target.
    setFold(node, target);
    for (const n of node.querySelectorAll<HTMLElement>(".jt-node")) setFold(n, target);
  } else {
    setFold(node, target);
  }
}

function setFold(node: HTMLElement, collapsed: boolean) {
  node.classList.toggle("collapsed", collapsed);
  const key = node.dataset.jkey;
  if (!key) return;
  const def = node.dataset.jdef === "1";
  const set = getState().jsonFold;
  // Persist only the delta from default so the default stays implicit. Mutated
  // in place with no version bump — the DOM is already updated.
  if (collapsed === def) set.delete(key);
  else set.add(key);
}

// --- SSE ----------------------------------------------------------------

function splitSseEvents(body: string): string[] {
  const out: string[] = [];
  for (const raw of body.split(/\n\n+/)) {
    const evt = raw.replace(/^\n+|\n+$/g, "");
    if (evt.length > 0) out.push(evt);
  }
  return out;
}

function renderSseEvent(
  rawEvent: string,
  idx: number,
  collapseData: boolean,
  keyPrefix: string,
): HTMLElement {
  const block = h("div.jt-event");
  const dataParts: string[] = [];

  for (const line of rawEvent.split("\n")) {
    if (line === "") continue;
    if (line.startsWith(":")) {
      block.appendChild(h("div.jt-row", null, span(line, "tok-comment")));
      continue;
    }
    const colon = line.indexOf(":");
    if (colon === -1) {
      block.appendChild(h("div.jt-row", null, text(line)));
      continue;
    }
    const field = line.slice(0, colon);
    const value = line.slice(colon + 1).replace(/^ /, "");
    if (field === "data") {
      dataParts.push(value);
      continue;
    }
    const row = h("div.jt-row", null, span(field, "tok-sse-field"), punct(": "));
    row.appendChild(field === "event" ? span(value, "jt-event-name") : text(value));
    block.appendChild(row);
  }

  if (dataParts.length > 0) {
    const raw = dataParts.join("\n");
    const parsed = tryParse(raw);
    const label = h("div.jt-row jt-data-label", null, span("data", "tok-sse-field"), punct(":"));
    block.appendChild(label);
    if (parsed.ok) {
      block.appendChild(
        buildEntry(null, parsed.value, `$/ev${idx}`, true, { keyPrefix, collapseRoot: collapseData }, 0),
      );
    } else {
      block.appendChild(h("div.jt-row", null, text(raw)));
    }
  }
  return block;
}

// --- helpers ------------------------------------------------------------

function tryParse(s: string): { ok: true; value: unknown } | { ok: false } {
  try {
    return { ok: true, value: JSON.parse(s) };
  } catch {
    return { ok: false };
  }
}

function primitive(value: unknown): HTMLElement {
  if (typeof value === "string") return span(JSON.stringify(value), "tok-string");
  if (typeof value === "number") return span(String(value), "tok-num");
  if (typeof value === "boolean") return span(String(value), "tok-bool");
  return span("null", "tok-null");
}

function keySpan(key: string): HTMLElement {
  return span(JSON.stringify(key), "tok-key");
}

function span(content: string, cls: string): HTMLElement {
  const s = document.createElement("span");
  s.className = cls;
  s.textContent = content;
  return s;
}

function punct(content: string): HTMLElement {
  return span(content, "tok-punct");
}

function text(content: string): Text {
  return document.createTextNode(content);
}
