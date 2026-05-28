// Single piece of shared app state. Components subscribe via onChange and
// re-render whatever they own. Mutations go through the setters so the
// notification fan-out is centralized.

import type { components, Env } from "../../api/client";

export type TrafficEntry = components["schemas"]["TrafficEntry"];
export type Settings = components["schemas"]["SettingsBody"];
export type ProxyInfo = components["schemas"]["ProxyInfoOutputBody"];
export type HeaderKV = components["schemas"]["HeaderKVBody"];

export type View = "traffic" | "settings";

export interface AppState {
  view: View;
  entries: TrafficEntry[];
  selectedId: string | null;
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
  Object.assign(state, patch);
  notify();
}

export function upsertEntry(next: TrafficEntry) {
  const idx = state.entries.findIndex((e) => e.id === next.id);
  if (idx === -1) {
    state.entries.unshift(next);
  } else {
    state.entries[idx] = next;
  }
  notify();
}

export function replaceEntries(items: TrafficEntry[]) {
  state.entries = [...items];
  notify();
}

export function clearEntries() {
  state.entries = [];
  state.selectedId = null;
  notify();
}

export function onChange(fn: Listener): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

function notify() {
  for (const fn of listeners) fn(state);
}
