// Sidebar — sessions first, entries nested beneath. When an entry doesn't
// belong to a known session (parser returned no normalized request) we
// fall back to an "Unsessioned" bucket so nothing disappears.
//
// Filters apply to the entry layer; a session that ends up with zero
// matching entries is hidden until the filter clears.

import type { components } from "../../api/client";
import { getClient } from "../../api/client";
import { t } from "../i18n";
import { h } from "./dom";
import { fmtDuration, fmtSessionStamp, fmtTime, statusKind } from "./format";
import { applyFilter } from "./grouping";
import {
  clearEntries,
  getState,
  selectSession,
  setFilters,
  setRenamingSession,
  setState,
  toggleGroupCollapsed,
  toggleSessionExpanded,
  type SessionSummary,
  type TrafficEntry,
} from "./state";

type Analysis = components["schemas"]["Analysis"];

export function renderSidebar(): HTMLElement {
  const state = getState();
  const filtered = applyFilter(state.entries, state.filters);

  const filterInput = h("input", {
    type: "text",
    placeholder: t("sidebar.filterPlaceholder"),
    value: state.filters.text,
    oninput: (e: Event) => setFilters({ text: (e.target as HTMLInputElement).value }),
  }) as HTMLInputElement;

  const head = h(
    "div.side-head",
    null,
    h("span.label", null, t("nav.sessions")),
    h("span.count", null, `${filtered.length} / ${state.entries.length}`),
  );

  const list = h("div.side-list");
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
    appendSessionList(list, state.sessions, filtered);
  }

  const actions = h(
    "div.side-actions",
    null,
    h(
      "button",
      {
        onclick: async () => {
          if (!confirm(t("sidebar.confirmClear"))) return;
          const client = await getClient();
          await client.DELETE("/v1/traffic", {});
          clearEntries();
        },
      },
      t("sidebar.clear"),
    ),
  );

  return h("div.sidebar", null, head, h("div.side-filter", null, filterInput), actions, list);
}

function appendSessionList(
  root: HTMLElement,
  sessions: SessionSummary[],
  filtered: TrafficEntry[],
) {
  const state = getState();

  // Build a fast lookup: visible entries by sessionId; ones without any
  // session land in the "unsessioned" bucket.
  const visibleIds = new Set(filtered.map((e) => e.id));
  const bySession = new Map<string, TrafficEntry[]>();
  const unsessioned: TrafficEntry[] = [];

  // First, fold known sessions in.
  for (const s of sessions) {
    const visible: TrafficEntry[] = [];
    for (const id of s.entryIds ?? []) {
      if (!visibleIds.has(id)) continue;
      const entry = state.entries.find((e) => e.id === id);
      if (entry) visible.push(entry);
    }
    if (visible.length > 0) bySession.set(s.id, visible);
  }

  // Anything that didn't show up under a known session lands in unsessioned.
  const sessionedIds = new Set<string>();
  for (const arr of bySession.values()) for (const e of arr) sessionedIds.add(e.id);
  for (const e of filtered) if (!sessionedIds.has(e.id)) unsessioned.push(e);

  // Sessions newest first (matches API List).
  for (const s of sessions) {
    const entries = bySession.get(s.id);
    if (!entries || entries.length === 0) continue;
    root.appendChild(renderSessionGroup(s, entries));
  }

  if (unsessioned.length > 0) {
    root.appendChild(
      renderRawGroup(
        "unsessioned",
        t("sidebar.unsessioned"),
        unsessioned,
      ),
    );
  }
}

function renderSessionGroup(s: SessionSummary, entries: TrafficEntry[]): HTMLElement {
  const state = getState();
  const expanded = state.expandedSessions.has(s.id);
  const active = state.selectedSessionId === s.id;
  const renaming = state.renamingSessionId === s.id;
  const totalIn = (s.inputTokens ?? 0) + (s.cacheRead ?? 0) + (s.cacheCreate ?? 0);
  const totalOut = s.outputTokens ?? 0;
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
      onclick: () => {
        if (renaming) return;
        selectSession(s.id);
      },
    },
    chev,
    h(
      `span.session-dot.${statusDot(s)}`,
      null,
    ),
    labelNode,
    renameBtn,
    h(
      "span.tree-group-meta",
      null,
      `${entries.length} · ${fmtTok(totalIn)}/${fmtTok(totalOut)}`,
    ),
  );

  const items = expanded
    ? h(
        "div.tree-group-items",
        null,
        ...entries.map((e) => entryRow(e)),
      )
    : null;

  return h(
    `div.tree-group${expanded ? "" : ".collapsed"}`,
    null,
    header,
    items,
  );
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
    try {
      const client = await getClient();
      await client.PATCH("/v1/sessions/{id}", {
        params: { path: { id: s.id } },
        body: { name: next },
      });
    } catch {
      // Roll back silently on failure — the next /v1/sessions refresh will
      // bring whatever the server has back into the renderer state.
    }
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

function renderRawGroup(key: string, label: string, entries: TrafficEntry[]): HTMLElement {
  const state = getState();
  const collapsed = state.collapsedGroups.has(key);
  return h(
    `div.tree-group${collapsed ? ".collapsed" : ""}`,
    null,
    h(
      "div.tree-group-hdr",
      { onclick: () => toggleGroupCollapsed(key) },
      h("span.chev", null, collapsed ? "▸" : "▾"),
      h("span.tree-group-label", null, label),
      h("span.tree-group-meta", null, String(entries.length)),
    ),
    collapsed
      ? null
      : h("div.tree-group-items", null, ...entries.map((e) => entryRow(e))),
  );
}

function entryRow(entry: TrafficEntry): HTMLElement {
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

  const analysis = entry.analysis as Analysis | undefined;
  const model = analysis?.anthropic?.request?.model || analysis?.anthropic?.response?.model;
  const usage = analysis?.anthropic?.response?.usage;
  const totalTok = usage
    ? (usage.inputTokens ?? 0) +
      (usage.outputTokens ?? 0) +
      (usage.cacheReadInputTokens ?? 0) +
      (usage.cacheCreationInputTokens ?? 0)
    : 0;

  return h(
    "div",
    {
      class: `entry${active ? " active" : ""}`,
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
      totalTok > 0 ? h("span", null, `${totalTok.toLocaleString()} tok`) : null,
      entry.streaming ? h("span", null, "stream") : null,
      entry.truncated ? h("span", null, "truncated") : null,
      entry.replayedFromId ? h("span.entry-replayed", null, `↩ ${entry.replayedFromId}`) : null,
    ),
  );
}

function statusDot(s: SessionSummary): string {
  if (s.hasError) return "err";
  if (s.hasUnfinished) return "warn";
  return "ok";
}

function fmtTok(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0";
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
  return String(n);
}
