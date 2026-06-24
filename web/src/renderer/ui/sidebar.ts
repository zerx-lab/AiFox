// Sidebar — sessions first, entries nested beneath. When an entry doesn't
// belong to a known session (parser returned no normalized request) we
// fall back to an "Unsessioned" bucket so nothing disappears.
//
// Filters apply to the entry layer; a session that ends up with zero
// matching entries is hidden until the filter clears.

import { t } from "../i18n";
import { clearTraffic, renameSession } from "./api-service";
import { registerDispose } from "./app";
import { colResizeHandle } from "./col-resize";
import { h } from "./dom";
import { fmtDuration, fmtSessionStamp, fmtTime, statusKind } from "./format";
import { applyFilter } from "./grouping";
import {
  clearEntries,
  type EntryMeta,
  getState,
  type SessionSummary,
  selectSession,
  setFilters,
  setRenamingSession,
  setState,
  toggleGroupCollapsed,
  toggleSessionExpanded,
} from "./state";

// Debounce window for the free-text filter (§4.1.4): typing fires setFilters at
// most once per FILTER_DEBOUNCE_MS so a fast typist doesn't trigger a re-filter
// (and a sidebar rebuild) on every keystroke. The 500-entry list stays smooth.
const FILTER_DEBOUNCE_MS = 150;

export function renderSidebar(): HTMLElement {
  const state = getState();
  const filtered = applyFilter(state.entries, state.filters);

  const filterInput = h("input", {
    type: "text",
    placeholder: t("sidebar.filterPlaceholder"),
    value: state.filters.text,
    "aria-label": t("sidebar.filterPlaceholder"),
  }) as HTMLInputElement;

  let debounce: number | null = null;
  filterInput.addEventListener("input", () => {
    if (debounce !== null) window.clearTimeout(debounce);
    debounce = window.setTimeout(() => {
      debounce = null;
      setFilters({ text: filterInput.value });
    }, FILTER_DEBOUNCE_MS);
  });

  // The clear (×) button only appears once there's text to clear (mirrors the
  // LLMFox sidebar affordance). Clearing flushes any pending debounce.
  const clearBtn = state.filters.text
    ? h(
        "button.side-filter-clear",
        {
          type: "button",
          title: t("sidebar.filterClear"),
          "aria-label": t("sidebar.filterClear"),
          onclick: () => {
            if (debounce !== null) {
              window.clearTimeout(debounce);
              debounce = null;
            }
            setFilters({ text: "" });
          },
        },
        clearIcon(),
      )
    : null;

  const filterBox = h(
    "div.side-filter",
    null,
    searchIcon(),
    filterInput,
    clearBtn,
  );

  const head = h(
    "div.side-head",
    null,
    h("span.label", null, t("nav.sessions")),
    h("span.count", null, `${filtered.length} / ${state.entries.length}`),
  );

  const list = h("div.side-list", { role: "listbox", tabindex: "0" }) as HTMLElement;
  if (filtered.length === 0) {
    list.appendChild(
      h(
        "div.side-empty",
        null,
        h("div.h", null, t("sidebar.empty")),
        h("div", null, t("sidebar.emptyHint")),
      ),
    );
  } else {
    const rows = buildRows(state.sessions, filtered);
    mountVirtualList(list, rows);
  }

  const actions = h(
    "div.side-actions",
    null,
    h(
      "button",
      {
        onclick: async () => {
          if (!confirm(t("sidebar.confirmClear"))) return;
          if (await clearTraffic()) clearEntries();
        },
      },
      t("sidebar.clear"),
    ),
  );

  return h(
    "div.sidebar",
    null,
    head,
    filterBox,
    actions,
    list,
    colResizeHandle("left"),
  );
}

function searchIcon(): SVGElement {
  return svgIcon(
    '<circle cx="6" cy="6" r="4.2" stroke="currentColor" stroke-width="1.4" fill="none"/><path d="M9.2 9.2 L13 13" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/>',
    14,
    "side-filter-icon",
  );
}

function clearIcon(): SVGElement {
  return svgIcon(
    '<path d="M3 3 L9 9 M9 3 L3 9" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/>',
    12,
    "side-filter-clear-icon",
  );
}

function svgIcon(inner: string, size: number, cls: string): SVGElement {
  const wrap = document.createElement("div");
  wrap.innerHTML = `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 ${size} ${size}" class="${cls}" aria-hidden="true">${inner}</svg>`;
  return wrap.firstElementChild as SVGElement;
}

// ---- Flat row model + virtualization (§4.1.3) -------------------------------
//
// The tree (session headers + nested entry rows + the unsessioned bucket) is
// flattened into a single fixed-height row list so the list can be windowed:
// only the rows intersecting the viewport (plus an overscan margin) are built
// into the DOM, with a top/bottom spacer reserving the rest of the scroll
// height. A long session (hundreds of turns) no longer maps the whole list to
// DOM on every rebuild. Fixed per-kind heights keep offset math exact and let
// pin-to-top / scroll-restore in app.ts keep working (it just reads scrollTop).

type Row =
  | { kind: "session-header"; height: number; session: SessionSummary; count: number; in: number; out: number }
  | { kind: "raw-header"; height: number; key: string; label: string; count: number; collapsed: boolean }
  | { kind: "entry"; height: number; entry: EntryMeta };

// Fixed row heights (px) — must match the CSS so the spacer math is exact.
const H_ENTRY = 46;
const H_SESSION_HEADER = 28;
const H_RAW_HEADER = 26;
// Extra rows rendered above/below the viewport so fast scrolling doesn't flash
// blank rows before the scroll handler catches up.
const OVERSCAN = 6;

function buildRows(sessions: SessionSummary[], filtered: EntryMeta[]): Row[] {
  const state = getState();
  const visibleIds = new Set(filtered.map((e) => e.id));
  const bySession = new Map<string, EntryMeta[]>();

  for (const s of sessions) {
    const visible: EntryMeta[] = [];
    for (const id of s.entryIds ?? []) {
      if (!visibleIds.has(id)) continue;
      const entry = state.entries.find((e) => e.id === id);
      if (entry) visible.push(entry);
    }
    if (visible.length > 0) bySession.set(s.id, visible);
  }

  const sessionedIds = new Set<string>();
  for (const arr of bySession.values()) for (const e of arr) sessionedIds.add(e.id);
  const unsessioned = filtered.filter((e) => !sessionedIds.has(e.id));

  const rows: Row[] = [];
  for (const s of sessions) {
    const entries = bySession.get(s.id);
    if (!entries || entries.length === 0) continue;
    const totalIn = (s.inputTokens ?? 0) + (s.cacheRead ?? 0) + (s.cacheCreate ?? 0);
    rows.push({
      kind: "session-header",
      height: H_SESSION_HEADER,
      session: s,
      count: entries.length,
      in: totalIn,
      out: s.outputTokens ?? 0,
    });
    if (state.expandedSessions.has(s.id)) {
      for (const e of entries) rows.push({ kind: "entry", height: H_ENTRY, entry: e });
    }
  }

  if (unsessioned.length > 0) {
    const collapsed = state.collapsedGroups.has("unsessioned");
    rows.push({
      kind: "raw-header",
      height: H_RAW_HEADER,
      key: "unsessioned",
      label: t("sidebar.unsessioned"),
      count: unsessioned.length,
      collapsed,
    });
    if (!collapsed) {
      for (const e of unsessioned) rows.push({ kind: "entry", height: H_ENTRY, entry: e });
    }
  }
  return rows;
}

// mountVirtualList wires a windowed renderer onto the scroll container. It keeps
// a spacer (total height) and renders only the visible row window into an
// absolutely-positioned layer offset by the first visible row's top. A scroll
// listener re-renders the window; registerDispose removes it when the sidebar
// region is rebuilt (§4.3.3 dispose protocol).
function mountVirtualList(viewport: HTMLElement, rows: Row[]) {
  const offsets: number[] = new Array(rows.length + 1);
  offsets[0] = 0;
  for (let i = 0; i < rows.length; i++) offsets[i + 1] = offsets[i]! + rows[i]!.height;
  const totalHeight = offsets[rows.length]!;

  // Cache built row elements by a stable identity so a re-window (scroll) reuses
  // already-built rows instead of rebuilding — keeps focus/highlight stable.
  const layer = h("div.side-vlist-layer") as HTMLElement;
  const spacer = h("div.side-vlist-spacer") as HTMLElement;
  spacer.style.height = `${totalHeight}px`;
  spacer.appendChild(layer);
  viewport.appendChild(spacer);

  let renderedStart = -1;
  let renderedEnd = -1;

  const render = () => {
    const scrollTop = viewport.scrollTop;
    const viewH = viewport.clientHeight || 400;
    let start = lowerBound(offsets, scrollTop) - 1;
    if (start < 0) start = 0;
    let end = lowerBound(offsets, scrollTop + viewH);
    if (end > rows.length) end = rows.length;
    start = Math.max(0, start - OVERSCAN);
    end = Math.min(rows.length, end + OVERSCAN);
    if (start === renderedStart && end === renderedEnd) return;
    renderedStart = start;
    renderedEnd = end;
    layer.replaceChildren();
    layer.style.transform = `translateY(${offsets[start]!}px)`;
    for (let i = start; i < end; i++) {
      const el = buildRowEl(rows[i]!);
      el.style.height = `${rows[i]!.height}px`;
      layer.appendChild(el);
    }
  };

  viewport.addEventListener("scroll", render, { passive: true });
  registerDispose(viewport, () => viewport.removeEventListener("scroll", render));
  // Initial paint. clientHeight may be 0 before layout, so also schedule a
  // post-layout pass via rAF to fill the real viewport.
  render();
  requestAnimationFrame(render);

  // Keyboard navigation (§4.1.6): the listbox responds to Up/Down to move the
  // selection across ENTRY rows (headers are skipped), scrolling the new row
  // into view (which triggers the windowed render).
  viewport.addEventListener("keydown", (e) => onListKey(e, rows, viewport));
}

// lowerBound: first index i where offsets[i] > value (offsets is ascending).
function lowerBound(offsets: number[], value: number): number {
  let lo = 0;
  let hi = offsets.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (offsets[mid]! <= value) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

function buildRowEl(row: Row): HTMLElement {
  if (row.kind === "session-header") {
    return renderSessionHeaderRow(row.session, row.count, row.in, row.out);
  }
  if (row.kind === "raw-header") {
    return renderRawHeaderRow(row.key, row.label, row.count, row.collapsed);
  }
  return entryRow(row.entry);
}

function onListKey(e: KeyboardEvent, rows: Row[], viewport: HTMLElement) {
  if (e.key !== "ArrowDown" && e.key !== "ArrowUp" && e.key !== "Home" && e.key !== "End") {
    return;
  }
  e.preventDefault();
  // Index list of entry rows in display order.
  const entryRows = rows.filter((r): r is Extract<Row, { kind: "entry" }> => r.kind === "entry");
  if (entryRows.length === 0) return;
  const selectedId = getState().selectedId;
  const cur = entryRows.findIndex((r) => r.entry.id === selectedId);
  let next = cur;
  if (e.key === "ArrowDown") next = cur < entryRows.length - 1 ? cur + 1 : cur < 0 ? 0 : cur;
  else if (e.key === "ArrowUp") next = cur > 0 ? cur - 1 : 0;
  else if (e.key === "Home") next = 0;
  else if (e.key === "End") next = entryRows.length - 1;
  const target = entryRows[next];
  if (!target) return;
  setState({ selectedId: target.entry.id });
  // Scroll the row into view; the windowed render fills it in. Compute its
  // absolute offset from the flat row order.
  const flatIdx = rows.indexOf(target);
  scrollRowIntoView(viewport, rows, flatIdx);
}

function scrollRowIntoView(viewport: HTMLElement, rows: Row[], idx: number) {
  let top = 0;
  for (let i = 0; i < idx; i++) top += rows[i]!.height;
  const bottom = top + rows[idx]!.height;
  const viewTop = viewport.scrollTop;
  const viewBottom = viewTop + viewport.clientHeight;
  if (top < viewTop) viewport.scrollTop = top;
  else if (bottom > viewBottom) viewport.scrollTop = bottom - viewport.clientHeight;
}

// renderSessionHeaderRow builds just the session header (the nested entry rows
// are now separate flat rows in the virtual list). Wrapped in .tree-group so the
// existing header CSS applies; height is fixed by the virtualizer.
function renderSessionHeaderRow(
  s: SessionSummary,
  count: number,
  totalIn: number,
  totalOut: number,
): HTMLElement {
  const state = getState();
  const expanded = state.expandedSessions.has(s.id);
  const active = state.selectedSessionId === s.id;
  const renaming = state.renamingSessionId === s.id;
  const defaultLabel = fmtSessionStamp(s.startedAt);
  const label = s.name || defaultLabel;
  const modelHint = s.model || s.provider || "";

  const chev = h(
    "span.chev",
    {
      // The chevron toggles expansion independently of which session is
      // selected — clicking it should never change the selection.
      onclick: (e: Event) => {
        e.stopPropagation();
        toggleSessionExpanded(s.id);
      },
    },
    expanded ? "▾" : "▸",
  );

  const labelNode = renaming
    ? renameInput(s, s.name || "")
    : h(
        "span.tree-group-label",
        { title: modelHint ? `${label} · ${modelHint}` : label },
        label,
      );

  const renameBtn = renaming
    ? null
    : h(
        "button",
        {
          class: "tree-group-rename",
          title: t("sidebar.rename"),
          "aria-label": t("sidebar.rename"),
          onclick: (e: Event) => {
            e.stopPropagation();
            setRenamingSession(s.id);
          },
        },
        "✎",
      );

  const header = h(
    `div.tree-group-hdr${active ? ".active" : ""}`,
    {
      role: "group",
      "aria-expanded": expanded ? "true" : "false",
      onclick: () => {
        if (renaming) return;
        selectSession(s.id);
      },
    },
    chev,
    h(`span.session-dot.${statusDot(s)}`, null),
    labelNode,
    modelLabel(s) ? h("span.tree-group-model", { title: modelHint }, modelLabel(s)) : null,
    renameBtn,
    h("span.tree-group-meta", null, `${count} · ${fmtTok(totalIn)}/${fmtTok(totalOut)}`),
  );

  return h("div.tree-group.side-vrow", null, header);
}

function renameInput(s: SessionSummary, initial: string): HTMLInputElement {
  const input = h("input", {
    class: "tree-group-rename-input",
    type: "text",
    value: initial,
    placeholder: fmtSessionStamp(s.startedAt),
    maxlength: "128",
  }) as HTMLInputElement;

  // Suppress card-level click and keep selection state stable while editing.
  input.addEventListener("click", (e) => e.stopPropagation());

  const commit = async () => {
    const next = input.value.trim();
    if (next === (s.name ?? "")) {
      setRenamingSession(null);
      return;
    }
    // The service toasts on failure; the next /v1/sessions refresh reconciles
    // the renderer state either way.
    await renameSession(s.id, next);
    setRenamingSession(null);
  };

  const cancel = () => setRenamingSession(null);

  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      void commit();
    } else if (e.key === "Escape") {
      e.preventDefault();
      cancel();
    }
  });
  input.addEventListener("blur", () => {
    void commit();
  });
  // Focus after this render cycle so the input is editable immediately.
  window.setTimeout(() => {
    input.focus();
    input.select();
  }, 0);
  return input;
}

function renderRawHeaderRow(
  key: string,
  label: string,
  count: number,
  collapsed: boolean,
): HTMLElement {
  return h(
    "div.tree-group.side-vrow",
    null,
    h(
      "div.tree-group-hdr",
      {
        role: "group",
        "aria-expanded": collapsed ? "false" : "true",
        onclick: () => toggleGroupCollapsed(key),
      },
      h("span.chev", null, collapsed ? "▸" : "▾"),
      h("span.tree-group-label", null, label),
      h("span.tree-group-meta", null, String(count)),
    ),
  );
}

function entryRow(entry: EntryMeta): HTMLElement {
  const state = getState();
  const active = state.selectedId === entry.id;
  const kind = statusKind(entry);
  const badgeLabel =
    kind === "pending"
      ? "…"
      : kind === "err"
        ? entry.statusCode > 0
          ? String(entry.statusCode)
          : "ERR"
        : entry.streaming
          ? "SSE"
          : String(entry.statusCode);

  const model = entry.model || undefined;
  const totalTok =
    (entry.inputTokens ?? 0) +
    (entry.outputTokens ?? 0) +
    (entry.cacheRead ?? 0) +
    (entry.cacheCreate ?? 0);

  return h(
    "div",
    {
      class: `entry side-vrow${active ? " active" : ""}`,
      role: "option",
      "aria-selected": active ? "true" : "false",
      onclick: () => setState({ selectedId: entry.id }),
    },
    h("span", { class: `badge ${kind}` }, badgeLabel),
    h(
      "span.path",
      { title: entry.url },
      model ? h("span.entry-model", null, model) : null,
      h("span.entry-id", null, entry.id),
    ),
    h("span.meta", null, fmtDuration(entry.durationMillis)),
    h(
      "span.sub",
      null,
      h("span", null, fmtTime(entry.startedAt)),
      entry.isUtility ? h("span.entry-utility", { title: t("sidebar.utilityHint") }, "sub-task") : null,
      totalTok > 0 ? h("span", null, `${totalTok.toLocaleString()} tok`) : null,
      entry.streaming ? h("span", null, "stream") : null,
      entry.truncated ? h("span", null, "truncated") : null,
      entry.replayedFromId ? h("span.entry-replayed", null, `↩ ${entry.replayedFromId}`) : null,
    ),
  );
}

function statusDot(s: SessionSummary): string {
  if (s.hasError) return "err";
  if (s.hasUnfinished) return "live"; // in-flight turn → pulsing dot
  return "ok";
}

// modelLabel renders a session's model(s) compactly: a single short name, or a
// mixed "opus·haiku" for sessions that span models (e.g. opencode's main model
// plus its haiku title-gen), or "opus +2" when many.
function modelLabel(s: SessionSummary): string {
  const models = s.models && s.models.length > 0 ? s.models : s.model ? [s.model] : [];
  const short = models.map(shortModel).filter((m, i, a) => a.indexOf(m) === i);
  if (short.length === 0) return "";
  if (short.length <= 2) return short.join("·");
  return `${short[0]} +${short.length - 1}`;
}

// shortModel strips the vendor prefix and date suffix: "claude-opus-4-8" →
// "opus", "claude-haiku-4-5-20251001" → "haiku", "gpt-4o" → "gpt-4o".
function shortModel(model: string): string {
  const m = model.match(/(?:claude-)?([a-z]+)/i);
  return m?.[1] ? m[1] : model;
}

function fmtTok(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0";
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
  return String(n);
}
