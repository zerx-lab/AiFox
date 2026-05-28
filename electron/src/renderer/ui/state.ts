// Single piece of shared app state. Components subscribe via onChange and
// re-render whatever they own. Mutations go through the setters so the
// notification fan-out is centralized.

import type { components, Env } from "../../api/client";

export type TrafficEntry = components["schemas"]["TrafficEntry"];
export type Settings = components["schemas"]["SettingsBody"];
export type ProxyInfo = components["schemas"]["ProxyInfoOutputBody"];
export type HeaderKV = components["schemas"]["HeaderKVBody"];
export type SessionSummary = components["schemas"]["SessionSummaryBody"];
export type Breakpoint = components["schemas"]["Breakpoint"];
export type PausedRequest = components["schemas"]["Paused"];

export type View = "traffic" | "settings";

export type DetailTab =
  | "overview"
  | "cache"
  | "tokens"
  | "tools"
  | "headers"
  | "request"
  | "response";

export type CacheStyle = "segmented" | "heatmap" | "blame";

export type BottomTab = "console" | "variables" | "problems" | "breakpoints";

export type CenterView = "timeline" | "stack";

export interface TrafficFilter {
  /** Free-text filter applied to method/url/status. */
  text: string;
  /** Show only streaming entries when true. */
  streaming: boolean;
  /** Show only errors (4xx/5xx/proxy error) when true. */
  errors: boolean;
  /** Match a model name exactly; empty = no filter. */
  model: string;
}

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
  sessions: SessionSummary[];
  selectedSessionId: string | null;
  // The session whose label is currently being edited inline in the sidebar
  // (null when no rename is in progress). Sidebar consumes this to swap the
  // label span for an <input>.
  renamingSessionId: string | null;
  selectedId: string | null;
  selection: Selection;
  detailTab: DetailTab;
  cacheStyle: CacheStyle;
  bottomTab: BottomTab;
  bottomCollapsed: boolean;
  bottomHeight: number;
  centerView: CenterView;
  filter: string;
  filters: TrafficFilter;
  collapsedGroups: Set<string>;
  // Sessions that the user has explicitly expanded in the sidebar. Default
  // behaviour is "collapsed": the sidebar shows the session header only and
  // entries are revealed on demand via the chevron.
  expandedSessions: Set<string>;
  // Timeline message cards the user has opened up. Keyed by `messageKey`
  // (e.g. "sys", "tools", "req-2", "resp"). Cards start collapsed so a
  // single long prompt does not blow out the scroll height of the center
  // pane; the user expands the cards they actually want to read.
  expandedMessages: Set<string>;
  replayOpen: boolean;
  breakpoints: Breakpoint[];
  pausedRequests: PausedRequest[];
  proxy: ProxyInfo | null;
  settings: Settings | null;
  env: Env | null;
  windowMaximized: boolean;
}

type Listener = (s: AppState) => void;

const state: AppState = {
  view: "traffic",
  entries: [],
  sessions: [],
  selectedSessionId: null,
  renamingSessionId: null,
  selectedId: null,
  selection: { messageKey: null, toolUseId: null },
  detailTab: "overview",
  cacheStyle: "segmented",
  bottomTab: "console",
  bottomCollapsed: false,
  bottomHeight: 200,
  centerView: "timeline",
  filter: "",
  filters: { text: "", streaming: false, errors: false, model: "" },
  collapsedGroups: new Set<string>(),
  expandedSessions: new Set<string>(),
  expandedMessages: new Set<string>(),
  replayOpen: false,
  breakpoints: [],
  pausedRequests: [],
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
  // previous request. Also reset per-card expansion since messageKey
  // namespacing is per-entry.
  if (patch.selectedId !== undefined && patch.selectedId !== state.selectedId) {
    state.selection = { messageKey: null, toolUseId: null };
    state.detailTab = "overview";
    state.expandedMessages.clear();
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
  state.detailTab = "tools";
  notify();
}

export function setDetailTab(tab: DetailTab) {
  state.detailTab = tab;
  notify();
}

export function setCacheStyle(style: CacheStyle) {
  state.cacheStyle = style;
  notify();
}

export function setBottomTab(tab: BottomTab) {
  state.bottomTab = tab;
  if (state.bottomCollapsed) state.bottomCollapsed = false;
  notify();
}

export function toggleBottomCollapsed() {
  state.bottomCollapsed = !state.bottomCollapsed;
  notify();
}

export function setBottomHeight(px: number) {
  state.bottomHeight = Math.max(80, Math.min(600, Math.round(px)));
  notify();
}

export function setFilters(patch: Partial<TrafficFilter>) {
  state.filters = { ...state.filters, ...patch };
  notify();
}

export function toggleGroupCollapsed(key: string) {
  if (state.collapsedGroups.has(key)) state.collapsedGroups.delete(key);
  else state.collapsedGroups.add(key);
  notify();
}

export function toggleSessionExpanded(id: string) {
  if (state.expandedSessions.has(id)) state.expandedSessions.delete(id);
  else state.expandedSessions.add(id);
  notify();
}

export function setRenamingSession(id: string | null) {
  state.renamingSessionId = id;
  notify();
}

export function toggleMessageExpanded(messageKey: string) {
  if (state.expandedMessages.has(messageKey)) state.expandedMessages.delete(messageKey);
  else state.expandedMessages.add(messageKey);
  notify();
}

export function setCenterView(view: CenterView) {
  state.centerView = view;
  notify();
}

export function setReplayOpen(open: boolean) {
  state.replayOpen = open;
  notify();
}

export function upsertEntry(next: TrafficEntry) {
  const idx = state.entries.findIndex((e) => e.id === next.id);
  const isNew = idx === -1;

  // Tail-follow: if the user is currently parked on the latest entry of a
  // session, advance their selection when that session's tip moves. We
  // snapshot the pre-update tip here because the session aggregator only
  // assigns sessionId once the request body is parseable, so the *first*
  // broadcast for a brand-new entry typically has sessionId="" — only the
  // follow-up update carries the session id. Gating on `isNew` would miss
  // that follow-up entirely.
  const sid = next.sessionId;
  const wasTailing =
    !!sid &&
    state.selectedId !== null &&
    state.entries.find((e) => e.sessionId === sid)?.id === state.selectedId;

  if (isNew) {
    state.entries.unshift(next);
  } else {
    state.entries[idx] = next;
  }

  if (state.selectedId === null) {
    state.selectedId = autoPickEntry(state.entries);
  } else if (wasTailing && sid) {
    const newLatestId =
      state.entries.find((e) => e.sessionId === sid)?.id ?? null;
    if (newLatestId && newLatestId !== state.selectedId) {
      state.selectedId = newLatestId;
      state.selection = { messageKey: null, toolUseId: null };
      state.detailTab = "overview";
      state.expandedMessages.clear();
    }
  }
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
  state.sessions = [];
  state.selectedSessionId = null;
  state.selectedId = null;
  state.selection = { messageKey: null, toolUseId: null };
  state.detailTab = "overview";
  state.expandedSessions.clear();
  state.expandedMessages.clear();
  notify();
}

export function replaceSessions(items: SessionSummary[]) {
  state.sessions = [...items];
  // If our currently selected session vanished (cleared on the server), drop
  // the selection so the sidebar doesn't keep a stale highlight.
  if (state.selectedSessionId && !items.some((s) => s.id === state.selectedSessionId)) {
    state.selectedSessionId = null;
  }
  notify();
}

export function replaceBreakpoints(items: Breakpoint[], paused: PausedRequest[]) {
  state.breakpoints = [...items];
  state.pausedRequests = [...paused];
  notify();
}

export function selectSession(id: string | null) {
  state.selectedSessionId = id;
  // Default to the latest entry of the session so the right pane has
  // something to display immediately.
  if (id) {
    const s = state.sessions.find((x) => x.id === id);
    const lastEntryId = s?.entryIds?.[s.entryIds.length - 1];
    if (lastEntryId && lastEntryId !== state.selectedId) {
      state.selectedId = lastEntryId;
      state.selection = { messageKey: null, toolUseId: null };
      state.detailTab = "overview";
    }
  }
  notify();
}

export function onChange(fn: Listener): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

function notify() {
  for (const fn of listeners) fn(state);
}
