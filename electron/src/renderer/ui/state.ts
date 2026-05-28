// Single piece of shared app state. Components subscribe via onChange and
// re-render whatever they own. Mutations go through the setters so the
// notification fan-out is centralized.

import type { components, Env } from "../../api/client";

export type TrafficEntry = components["schemas"]["TrafficEntry"];
export type Settings = components["schemas"]["SettingsBody"];
export type ProxyInfo = components["schemas"]["ProxyInfoOutputBody"];
export type HeaderKV = components["schemas"]["HeaderKVBody"];

export type View = "traffic" | "settings";

export type DetailTab = "overview" | "tool" | "headers" | "request" | "response";

export interface Selection {
  /** Which message card is highlighted in the center timeline.
   *  Format: "sys" | "tools" | "req-<index>" | "resp" — derived in renderer. */
  messageKey: string | null;
  /** When the user clicks a tool_use block in the timeline, the right pane
   *  pivots to the Tool tab and renders args + the matching tool_result. */
  toolUseId: string | null;
}

export interface AppState {
  view: View;
  entries: TrafficEntry[];
  selectedId: string | null;
  selection: Selection;
  detailTab: DetailTab;
  filter: string;
  proxy: ProxyInfo | null;
  settings: Settings | null;
  env: Env | null;
  windowMaximized: boolean;
}

type Listener = (s: AppState) => void;

const state: AppState = {
  view: "traffic",
  entries: [],
  selectedId: null,
  selection: { messageKey: null, toolUseId: null },
  detailTab: "overview",
  filter: "",
  proxy: null,
  settings: null,
  env: null,
  windowMaximized: false,
};

const listeners = new Set<Listener>();

export function getState(): AppState {
  return state;
}

export function setState(patch: Partial<AppState>) {
  // Switching to a different entry resets timeline-local selection so the
  // right pane doesn't keep highlighting a tool_use that belongs to the
  // previous request.
  if (patch.selectedId !== undefined && patch.selectedId !== state.selectedId) {
    state.selection = { messageKey: null, toolUseId: null };
    state.detailTab = "overview";
  }
  Object.assign(state, patch);
  notify();
}

export function selectMessage(messageKey: string | null) {
  state.selection = { messageKey, toolUseId: null };
  notify();
}

export function selectToolUse(messageKey: string, toolUseId: string) {
  state.selection = { messageKey, toolUseId };
  state.detailTab = "tool";
  notify();
}

export function setDetailTab(tab: DetailTab) {
  state.detailTab = tab;
  notify();
}

export function upsertEntry(next: TrafficEntry) {
  const idx = state.entries.findIndex((e) => e.id === next.id);
  if (idx === -1) {
    state.entries.unshift(next);
  } else {
    state.entries[idx] = next;
  }
  if (state.selectedId === null) state.selectedId = autoPickEntry(state.entries);
  notify();
}

export function replaceEntries(items: TrafficEntry[]) {
  state.entries = [...items];
  if (state.selectedId === null) state.selectedId = autoPickEntry(state.entries);
  notify();
}

// Pick a sensible entry to focus when no selection exists yet. Prefer the
// newest entry that has a structured Anthropic analysis — internal probe
// endpoints (e.g. /internal/whoami) don't and would land on the fallback
// view, which makes a worse first impression.
function autoPickEntry(entries: TrafficEntry[]): string | null {
  for (const e of entries) {
    const analysis = e.analysis as { anthropic?: unknown } | undefined;
    if (analysis?.anthropic) return e.id;
  }
  return entries[0]?.id ?? null;
}

export function clearEntries() {
  state.entries = [];
  state.selectedId = null;
  state.selection = { messageKey: null, toolUseId: null };
  state.detailTab = "overview";
  notify();
}

export function onChange(fn: Listener): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

function notify() {
  for (const fn of listeners) fn(state);
}
