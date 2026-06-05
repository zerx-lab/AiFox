// Single piece of shared app state. Components subscribe via onChange and
// re-render whatever they own. Mutations go through the setters so the
// notification fan-out is centralized.

import type { components, Env } from "../../api/client";

// EntryMeta is the lightweight list/sidebar projection streamed over the index
// SSE (no bodies, no full analysis — just the summary fields the list needs).
// The full TrafficEntry (bodies + analysis) is fetched on demand for the one
// selected entry and held in AppState.selectedEntry.
export type EntryMeta = components["schemas"]["EntryMeta"];
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
  // entries holds lightweight metadata only (no bodies/analysis). The detail
  // panes read selectedEntry for the full payload of the focused entry.
  entries: EntryMeta[];
  // selectedEntry is the full TrafficEntry for selectedId, fetched on demand
  // and live-updated while streaming. Null until loaded; its id may briefly lag
  // selectedId during a fetch, so detail consumers must check id match.
  selectedEntry: TrafficEntry | null;
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
  selectedEntry: null,
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

// Per-slice monotonic version counters (L4 region-scoped rendering). setState
// does Object.assign onto the same `state` object and several setters mutate
// arrays/Sets in place (upsertEntry writes state.entries[idx], Sets .add/.delete
// in place), so reference equality cannot detect a change on the streaming hot
// path. Each render region compares the counters it depends on to decide
// whether to rebuild, instead of the whole app re-mounting on every event.
//   struct — list/session structure: add/remove entry, sessionId/status/model
//            flip, sessions, breakpoints. Drives sidebar / filter pills / bottom.
//   sel    — selection + selected-entry detail: selectedId, selectedEntry,
//            selection, detailTab, centerView, expandedMessages. Drives the
//            (expensive) timeline + detail panes.
//   ui     — chrome / filters / layout: view, filters, expanded sets, bottom
//            pane geometry, proxy/settings/env, rename. Drives most regions.
//   meta   — per-entry token/size ticks that only the statusbar totals consume;
//            deliberately NOT a sidebar dependency so token updates don't churn
//            the list.
//   body   — the selected streaming entry's response bytes growing. Consumed
//            ONLY by an in-place DOM append in app.ts (the live Response view),
//            never by a region rebuild, so watching a stream never re-highlights
//            the whole body nor destroys the user's text selection.
export const versions = { struct: 0, sel: 0, ui: 0, meta: 0, body: 0 };
type VKind = keyof typeof versions;

function touch(...kinds: VKind[]) {
  for (const k of kinds) versions[k]++;
  notify();
}

// Maps a setState patch key to the version slice it dirties.
const KEY_VERSION: Record<keyof AppState, VKind> = {
  view: "ui",
  entries: "struct",
  selectedEntry: "sel",
  sessions: "struct",
  selectedSessionId: "sel",
  renamingSessionId: "ui",
  selectedId: "sel",
  selection: "sel",
  detailTab: "sel",
  cacheStyle: "sel",
  bottomTab: "ui",
  bottomCollapsed: "ui",
  bottomHeight: "ui",
  centerView: "sel",
  filter: "ui",
  filters: "ui",
  collapsedGroups: "ui",
  expandedSessions: "ui",
  expandedMessages: "sel",
  replayOpen: "sel",
  breakpoints: "struct",
  pausedRequests: "struct",
  proxy: "ui",
  settings: "ui",
  env: "ui",
  windowMaximized: "ui",
};

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
  const kinds = new Set<VKind>();
  for (const k of Object.keys(patch) as (keyof AppState)[]) {
    kinds.add(KEY_VERSION[k] ?? "ui");
  }
  touch(...kinds);
}

export function selectMessage(messageKey: string | null) {
  state.selection = { messageKey, toolUseId: null };
  touch("sel");
}

export function selectToolUse(messageKey: string, toolUseId: string) {
  state.selection = { messageKey, toolUseId };
  state.detailTab = "tools";
  touch("sel");
}

export function setDetailTab(tab: DetailTab) {
  state.detailTab = tab;
  touch("sel");
}

export function setCacheStyle(style: CacheStyle) {
  state.cacheStyle = style;
  touch("sel");
}

export function setBottomTab(tab: BottomTab) {
  state.bottomTab = tab;
  if (state.bottomCollapsed) state.bottomCollapsed = false;
  touch("ui");
}

export function toggleBottomCollapsed() {
  state.bottomCollapsed = !state.bottomCollapsed;
  touch("ui");
}

export function setBottomHeight(px: number) {
  state.bottomHeight = Math.max(80, Math.min(600, Math.round(px)));
  touch("ui");
}

export function setFilters(patch: Partial<TrafficFilter>) {
  state.filters = { ...state.filters, ...patch };
  touch("ui");
}

export function toggleGroupCollapsed(key: string) {
  if (state.collapsedGroups.has(key)) state.collapsedGroups.delete(key);
  else state.collapsedGroups.add(key);
  touch("ui");
}

export function toggleSessionExpanded(id: string) {
  if (state.expandedSessions.has(id)) state.expandedSessions.delete(id);
  else state.expandedSessions.add(id);
  touch("ui");
}

export function setRenamingSession(id: string | null) {
  state.renamingSessionId = id;
  touch("ui");
}

export function toggleMessageExpanded(messageKey: string) {
  if (state.expandedMessages.has(messageKey)) state.expandedMessages.delete(messageKey);
  else state.expandedMessages.add(messageKey);
  touch("sel");
}

export function setCenterView(view: CenterView) {
  state.centerView = view;
  touch("sel");
}

export function setReplayOpen(open: boolean) {
  state.replayOpen = open;
  touch("sel");
}

export function upsertEntry(next: EntryMeta) {
  const idx = state.entries.findIndex((e) => e.id === next.id);
  const isNew = idx === -1;
  const prev = isNew ? undefined : state.entries[idx];
  const selectedBefore = state.selectedId;
  // A structural change is one the sidebar/list cares about (grouping, badge,
  // model label, utility tag). A token/size-only tick is "meta" and must NOT
  // re-render the sidebar — only the statusbar totals.
  const structChanged =
    isNew ||
    !prev ||
    prev.sessionId !== next.sessionId ||
    prev.statusCode !== next.statusCode ||
    !!prev.error !== !!next.error ||
    prev.streaming !== next.streaming ||
    prev.truncated !== next.truncated ||
    prev.isUtility !== next.isUtility ||
    prev.model !== next.model ||
    prev.endpoint !== next.endpoint;

  // Tail-follow: if the user is currently parked on the latest entry of a
  // session, advance their selection when that session's tip moves. We
  // snapshot the pre-update tip here because the session aggregator only
  // assigns sessionId once the request body is parseable, so the *first*
  // broadcast for a brand-new entry typically has sessionId="" — only the
  // follow-up update carries the session id. Gating on `isNew` would miss
  // that follow-up entirely.
  // Tail-follow tracks the latest NON-utility turn of the session: a haiku
  // title-gen / tool-summary sub-task must never steal the user's focus. The
  // user "was tailing" if they're parked on that latest real turn.
  const sid = next.sessionId;
  const latestReal = (s: string) =>
    state.entries.find((e) => e.sessionId === s && !e.isUtility)?.id ?? null;
  const wasTailing =
    !!sid &&
    state.selectedId !== null &&
    latestReal(sid) === state.selectedId;

  if (isNew) {
    state.entries.unshift(next);
  } else {
    state.entries[idx] = next;
  }

  if (state.selectedId === null) {
    state.selectedId = autoPickEntry(state.entries);
  } else if (wasTailing && sid) {
    const newLatestId = latestReal(sid);
    if (newLatestId && newLatestId !== state.selectedId) {
      state.selectedId = newLatestId;
      state.selection = { messageKey: null, toolUseId: null };
      state.detailTab = "overview";
      state.expandedMessages.clear();
    }
  }
  const kinds: VKind[] = ["meta"];
  if (structChanged) kinds.push("struct");
  if (state.selectedId !== selectedBefore) kinds.push("sel");
  touch(...kinds);
}

export function replaceEntries(items: EntryMeta[]) {
  const selectedBefore = state.selectedId;
  state.entries = [...items];
  if (state.selectedId === null) state.selectedId = autoPickEntry(state.entries);
  const kinds: VKind[] = ["struct", "meta"];
  if (state.selectedId !== selectedBefore) kinds.push("sel");
  touch(...kinds);
}

// Pick a sensible entry to focus when no selection exists yet. Prefer the
// newest entry that has a structured Anthropic analysis — internal probe
// endpoints (e.g. /internal/whoami) don't and would land on the fallback
// view, which makes a worse first impression. EntryMeta carries hasStructured
// so this works without the full body.
function autoPickEntry(entries: EntryMeta[]): string | null {
  // Prefer a real (non-utility) structured turn over a title-gen sub-task.
  for (const e of entries) {
    if (e.hasStructured && !e.isUtility) return e.id;
  }
  for (const e of entries) {
    if (e.hasStructured) return e.id;
  }
  return entries[0]?.id ?? null;
}

// setSelectedEntry stores the full body fetched for the focused entry. Guarded
// by id so a late-arriving fetch for a previously-selected entry can't clobber
// the current one (the selection controller passes the id it fetched).
export function setSelectedEntry(entry: TrafficEntry | null) {
  state.selectedEntry = entry;
  touch("sel");
}

// appendSelectedBody grows the selected streaming entry's response in place and
// bumps only `body` — no region rebuilds. app.ts patches the live <pre> text
// node directly, so the structured timeline/detail stay put and the user's text
// selection survives. Ignored if the entry id no longer matches the selection.
export function appendSelectedBody(id: string, bytes: string, responseSize: number) {
  const e = state.selectedEntry;
  if (!e || e.id !== id || !bytes) return;
  e.responseBody = (e.responseBody ?? "") + bytes;
  e.responseSize = responseSize;
  touch("body");
}

// selectedFull returns the loaded full entry only when it matches the current
// selection; null means "not selected" or "still loading". Detail panes use it
// instead of scanning state.entries (which now holds metadata only).
export function selectedFull(): TrafficEntry | null {
  if (!state.selectedId) return null;
  if (state.selectedEntry && state.selectedEntry.id === state.selectedId) {
    return state.selectedEntry;
  }
  return null;
}

export function clearEntries() {
  state.entries = [];
  state.selectedEntry = null;
  state.sessions = [];
  state.selectedSessionId = null;
  state.selectedId = null;
  state.selection = { messageKey: null, toolUseId: null };
  state.detailTab = "overview";
  state.expandedSessions.clear();
  state.expandedMessages.clear();
  touch("struct", "sel", "ui", "meta");
}

export function replaceSessions(items: SessionSummary[]) {
  state.sessions = [...items];
  // If our currently selected session vanished (cleared on the server), drop
  // the selection so the sidebar doesn't keep a stale highlight.
  const selectedSessionBefore = state.selectedSessionId;
  if (state.selectedSessionId && !items.some((s) => s.id === state.selectedSessionId)) {
    state.selectedSessionId = null;
  }
  const kinds: VKind[] = ["struct"];
  if (state.selectedSessionId !== selectedSessionBefore) kinds.push("sel");
  touch(...kinds);
}

export function replaceBreakpoints(items: Breakpoint[], paused: PausedRequest[]) {
  state.breakpoints = [...items];
  state.pausedRequests = [...paused];
  touch("struct");
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
  touch("sel");
}

export function onChange(fn: Listener): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

function notify() {
  for (const fn of listeners) fn(state);
}
