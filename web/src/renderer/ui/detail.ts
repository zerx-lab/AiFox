// Right-hand inspect panel for the selected traffic entry. Tabs span the
// "raw" facets (overview, headers, request, response) plus three structured
// analyses (cache, tokens, tools) that surface what the parser has
// extracted from an Anthropic Messages call.

import type { components } from "../../api/client";
import { t } from "../i18n";
import { renderCache } from "./cache";
import { colResizeHandle } from "./col-resize";
import { renderDiff } from "./diff";
import { h } from "./dom";
import { fmtBytes, fmtClock, fmtDuration, isPending } from "./format";
import { renderRawRequest, renderRawResponse } from "./raw-http";
import {
  type DetailTab,
  getState,
  replayOriginOf,
  type SessionSummary,
  selectedFull,
  setDetailTab,
  type TrafficEntry,
} from "./state";

// Fields detailHead renders — shared by the full TrafficEntry and the
// lightweight EntryMeta so the loading skeleton can reuse the same header.
type DetailHeadFields = Pick<
  TrafficEntry,
  "method" | "url" | "statusCode" | "durationMillis" | "startedAt" | "streaming"
>;

import { renderTokens } from "./tokens";
import { renderTools } from "./tools";

type Analysis = components["schemas"]["Analysis"];
type AnthropicUsage = components["schemas"]["AnthropicUsage"];

export function renderDetail(): HTMLElement {
  const state = getState();
  // selectedFull() returns the loaded full TrafficEntry for the current
  // selection; null means nothing selected or still loading.
  const entry = selectedFull();

  if (!entry) {
    // selectedId is set but the full body/analysis hasn't been fetched yet.
    // Render a stable skeleton — head from the lightweight EntryMeta already in
    // the list plus a loading body — instead of collapsing to the empty prompt.
    // Switching turns/entries always passes through this state; the empty prompt
    // here is what made the whole right pane flash blank for a frame. The
    // skeleton keeps the same head/tabs/body structure as the loaded panel, so
    // reconcileDetail (app.ts) swaps it in place once data lands — no flicker.
    // Only a genuinely empty selection falls through to the prompt.
    const meta = state.selectedId
      ? state.entries.find((e) => e.id === state.selectedId)
      : undefined;
    if (!meta) {
      return h(
        "aside.detail",
        null,
        colResizeHandle("right"),
        h("div.detail-empty", null, t("detail.selectPrompt")),
      );
    }
    return h(
      "aside.detail",
      null,
      colResizeHandle("right"),
      detailHead(meta),
      detailTabs(skeletonTabs(meta.hasStructured), "overview"),
      h("div.detail-body", null, h("div.detail-loading", null, t("detail.loading"))),
    );
  }

  // Single source of truth for which tabs exist for this entry. The fallback
  // (a stale tab no longer available) and the body dispatch both read this one
  // table, so they can never drift (§ task 7).
  const tabs = availableTabs(entry);
  let tab: DetailTab = state.detailTab;
  if (!tabs.some((d) => d.tab === tab)) tab = "overview";

  // When the timeline selection (message/tool) changes, reset the detail body
  // scroll to the top so the user lands at the start of the newly-inspected
  // content rather than wherever the previous entry was scrolled (§ task 5).
  // reconcileDetail otherwise preserves scrollTop across in-place body refreshes
  // (e.g. streaming bumps), so we only force a reset on an actual selection flip.
  const selKey = `${state.selectedId}|${state.selection.messageKey}|${state.selection.toolUseId}`;
  if (selKey !== lastSelKey) {
    lastSelKey = selKey;
    requestAnimationFrame(() => {
      const body = document.querySelector<HTMLElement>(".detail-body");
      if (body) body.scrollTop = 0;
    });
  }

  return h(
    "aside.detail",
    null,
    colResizeHandle("right"),
    detailHead(entry),
    detailTabs(tabs, tab),
    h("div.detail-body", null, renderTabBody(entry, tab)),
  );
}

// Tracks the last timeline-selection key so renderDetail can reset the body
// scroll only on a genuine selection change, not on every re-render.
let lastSelKey = "";

interface TabDef {
  tab: DetailTab;
  label: string;
  body: (entry: TrafficEntry) => HTMLElement;
}

// availableTabs returns the ordered tab set for an entry: always overview /
// headers / request / response, plus cache/tokens/tools when an Anthropic
// analysis is present, plus a Diff tab when the entry was produced by a replay.
function availableTabs(entry: TrafficEntry): TabDef[] {
  const analysis = entry.analysis as Analysis | undefined;
  const hasAnthropic = !!analysis?.anthropic;
  const defs: TabDef[] = [
    { tab: "overview", label: t("detail.tabs.overview"), body: overviewBody },
  ];
  if (hasAnthropic) {
    defs.push({ tab: "cache", label: t("detail.tabs.cache"), body: (e) => renderCache((e.analysis as Analysis | undefined)?.anthropic) });
    defs.push({ tab: "tokens", label: t("detail.tabs.tokens"), body: tokensBody });
    defs.push({ tab: "tools", label: t("detail.tabs.tools"), body: renderTools });
  }
  if (replayOriginOf(entry.id)) {
    defs.push({ tab: "diff", label: t("detail.tabs.diff"), body: renderDiff });
  }
  defs.push({ tab: "headers", label: t("detail.tabs.headers"), body: headersBody });
  defs.push({ tab: "request", label: t("detail.tabs.request"), body: renderRawRequest });
  defs.push({ tab: "response", label: t("detail.tabs.response"), body: renderRawResponse });
  return defs;
}

// skeletonTabs mirrors the loaded tab order for the loading placeholder, using
// only the hasStructured hint from the EntryMeta (no full body yet). Diff/raw
// resolve once the entry loads. Body builders are unused in the skeleton.
function skeletonTabs(hasStructured: boolean): TabDef[] {
  const noop = () => h("div");
  const defs: TabDef[] = [{ tab: "overview", label: t("detail.tabs.overview"), body: noop }];
  if (hasStructured) {
    defs.push({ tab: "cache", label: t("detail.tabs.cache"), body: noop });
    defs.push({ tab: "tokens", label: t("detail.tabs.tokens"), body: noop });
    defs.push({ tab: "tools", label: t("detail.tabs.tools"), body: noop });
  }
  defs.push({ tab: "headers", label: t("detail.tabs.headers"), body: noop });
  defs.push({ tab: "request", label: t("detail.tabs.request"), body: noop });
  defs.push({ tab: "response", label: t("detail.tabs.response"), body: noop });
  return defs;
}

function tokensBody(entry: TrafficEntry): HTMLElement {
  const analysis = entry.analysis as Analysis | undefined;
  const usage = analysis?.anthropic?.response?.usage as AnthropicUsage | undefined;
  const model =
    analysis?.anthropic?.request?.model ||
    analysis?.anthropic?.response?.model ||
    undefined;
  return renderTokens(usage, model);
}

function detailHead(entry: DetailHeadFields): HTMLElement {
  const url = entry.url || "—";
  return h(
    "div.detail-head",
    null,
    h(
      "div.detail-head-line",
      null,
      h("span.detail-method", null, entry.method),
      h(
        "span.detail-url",
        { title: url },
        url.length > 64 ? `…${url.slice(-60)}` : url,
      ),
    ),
    h(
      "div.detail-head-meta",
      null,
      h(
        "span",
        null,
        entry.statusCode > 0 ? String(entry.statusCode) : "···",
      ),
      h("span", null, fmtDuration(entry.durationMillis)),
      h("span", null, fmtClock(entry.startedAt)),
      entry.streaming ? h("span.detail-pill-stream", null, t("detail.streaming")) : null,
    ),
  );
}

// detailTabs renders the tablist. ARIA: role=tablist / tab + roving tabindex +
// Left/Right arrow navigation (§4.1.6). The active tab is the only tab in the
// tab order (tabindex 0); arrows move focus AND selection between tabs.
function detailTabs(defs: TabDef[], active: DetailTab): HTMLElement {
  const order = defs.map((d) => d.tab);
  const bar = h("div.detail-tabs", { role: "tablist" });
  for (const d of defs) {
    bar.appendChild(tabBtn(d.tab, d.label, active, order));
  }
  return bar;
}

function tabBtn(
  tab: DetailTab,
  label: string,
  active: DetailTab,
  order: DetailTab[],
): HTMLElement {
  const isActive = active === tab;
  return h(
    "button",
    {
      class: isActive ? "active" : "",
      role: "tab",
      "aria-selected": isActive ? "true" : "false",
      tabindex: isActive ? "0" : "-1",
      onclick: () => setDetailTab(tab),
      onkeydown: (e: KeyboardEvent) => onTabKey(e, tab, order),
    },
    label,
  );
}

// onTabKey implements roving-tabindex arrow navigation across the tablist.
function onTabKey(e: KeyboardEvent, tab: DetailTab, order: DetailTab[]) {
  let next: DetailTab | undefined;
  const idx = order.indexOf(tab);
  if (e.key === "ArrowRight" || e.key === "ArrowDown") next = order[(idx + 1) % order.length];
  else if (e.key === "ArrowLeft" || e.key === "ArrowUp")
    next = order[(idx - 1 + order.length) % order.length];
  else if (e.key === "Home") next = order[0];
  else if (e.key === "End") next = order[order.length - 1];
  if (!next) return;
  e.preventDefault();
  setDetailTab(next);
  // Move focus to the newly active tab after the region re-renders.
  requestAnimationFrame(() => {
    document
      .querySelector<HTMLElement>(`.detail-tabs button[aria-selected="true"]`)
      ?.focus();
  });
}

function renderTabBody(entry: TrafficEntry, tab: DetailTab): HTMLElement {
  const def = availableTabs(entry).find((d) => d.tab === tab);
  return def ? def.body(entry) : overviewBody(entry);
}

function overviewBody(entry: TrafficEntry): HTMLElement {
  const banners = h("div");
  if (entry.error) {
    banners.appendChild(
      h("div.banner.err", null, `${t("detail.error")}: ${entry.error}`),
    );
  }
  if (entry.truncated) {
    banners.appendChild(h("div.banner.warn", null, t("detail.truncated")));
  }
  if (isPending(entry)) {
    banners.appendChild(h("div.banner.info", null, t("detail.pending")));
  }

  const analysis = entry.analysis as Analysis | undefined;
  const usage = analysis?.anthropic?.response?.usage;
  // Model falls back across providers so OpenAI entries show a model too.
  const model =
    analysis?.anthropic?.request?.model ||
    analysis?.anthropic?.response?.model ||
    analysis?.openai?.response?.model ||
    analysis?.openai?.request?.model ||
    analysis?.responses?.response?.model ||
    analysis?.responses?.request?.model ||
    "—";

  const sessionPanel = renderSessionOverview(entry);
  const usagePanel = usage ? renderUsage(usage) : null;

  return h(
    "div",
    null,
    banners,
    sessionPanel,
    usagePanel,
    h(
      "dl.kv",
      null,
      kv(t("detail.method"), entry.method),
      kv(t("detail.status"), entry.statusCode > 0 ? String(entry.statusCode) : "—"),
      kv(t("detail.model"), model),
      kv(t("detail.started"), fmtClock(entry.startedAt)),
      kv(t("detail.duration"), fmtDuration(entry.durationMillis)),
      kv(t("detail.upstream"), entry.upstreamUrl || "—"),
      kv(t("detail.requestSize"), fmtBytes(entry.requestSize)),
      kv(t("detail.responseSize"), fmtBytes(entry.responseSize)),
      kv(t("detail.streaming"), entry.streaming ? "✓" : "—"),
    ),
  );
}

// Session-level rollup shown above the per-entry usage panel. Pulls token
// totals from the SessionSummary the Go aggregator produced — that's the
// source of truth for "across all turns of this conversation".
function renderSessionOverview(entry: TrafficEntry): HTMLElement | null {
  const state = getState();
  const session = state.sessions.find((s) => (s.entryIds ?? []).includes(entry.id));
  if (!session) return null;
  // turnCount excludes utility sub-tasks (title-gen/summaries) — keep it
  // consistent with the server's rollup, not a raw entry count.
  const turns = session.turnCount ?? session.entryIds?.length ?? 0;
  const input = session.inputTokens ?? 0;
  const cacheRead = session.cacheRead ?? 0;
  const cacheCreate = session.cacheCreate ?? 0;
  const output = session.outputTokens ?? 0;
  const inputAll = input + cacheRead + cacheCreate;
  const hitPct = inputAll > 0 ? Math.round((cacheRead / inputAll) * 100) : 0;

  const cells = [
    sessionCell(t("detail.sessionTurns"), String(turns)),
    sessionCell(t("detail.sessionInput"), inputAll.toLocaleString()),
    sessionCell(t("detail.sessionOutput"), output.toLocaleString(), "tool"),
  ];
  if (cacheRead > 0) {
    cells.push(sessionCell(t("detail.sessionCacheRead"), cacheRead.toLocaleString(), "ok"));
  }
  if (cacheCreate > 0) {
    cells.push(sessionCell(t("detail.sessionCacheCreate"), cacheCreate.toLocaleString(), "warn"));
  }
  cells.push(
    sessionCell(t("detail.sessionHitRate"), `${hitPct}%`, cacheRead > 0 ? "ok" : undefined),
  );

  return h(
    "div.det-session",
    null,
    h(
      "div.det-session-head",
      null,
      h("span.det-session-title", null, t("detail.sessionTitle")),
      h(
        "span.det-session-model",
        null,
        sessionLabel(session),
      ),
    ),
    h("div.det-session-stats", null, ...cells),
  );
}

function sessionCell(label: string, value: string, variant?: string): HTMLElement {
  return h(
    `div.cstat${variant ? `.v-${variant}` : ""}`,
    null,
    h("div.l", null, label),
    h("div.v", null, value),
  );
}

function sessionLabel(s: SessionSummary): string {
  return s.model || s.provider || s.id;
}

function renderUsage(u: AnthropicUsage): HTMLElement {
  const cacheRead = u.cacheReadInputTokens ?? 0;
  const cacheCreate = u.cacheCreationInputTokens ?? 0;
  const input = (u.inputTokens ?? 0) + cacheRead + cacheCreate;
  const output = u.outputTokens ?? 0;
  const total = input + output || 1;
  const cachePct = Math.round((cacheRead / Math.max(1, input)) * 100);

  const cells = [
    statCell(t("detail.usageInput"), input.toLocaleString()),
    statCell(t("detail.usageOutput"), output.toLocaleString(), "tool"),
  ];
  if (cacheRead > 0)
    cells.push(statCell(t("conversation.usageCacheRead"), cacheRead.toLocaleString(), "ok"));
  if (cacheCreate > 0)
    cells.push(statCell(t("conversation.usageCacheCreate"), cacheCreate.toLocaleString(), "warn"));

  const bar = h("div.det-bar");
  if (cacheRead > 0) {
    const span = h("div.b-cache");
    span.style.width = `${(cacheRead / total) * 100}%`;
    bar.appendChild(span);
  }
  if (cacheCreate > 0) {
    const span = h("div.b-cache-create");
    span.style.width = `${(cacheCreate / total) * 100}%`;
    bar.appendChild(span);
  }
  const uncached = (u.inputTokens ?? 0);
  if (uncached > 0) {
    const span = h("div.b-input");
    span.style.width = `${(uncached / total) * 100}%`;
    bar.appendChild(span);
  }
  if (output > 0) {
    const span = h("div.b-output");
    span.style.width = `${(output / total) * 100}%`;
    bar.appendChild(span);
  }

  return h(
    "div.det-usage",
    null,
    h("div.det-usage-stats", null, ...cells),
    bar,
    cacheRead > 0
      ? h(
          "div.det-usage-note",
          null,
          t("detail.cacheHit", { pct: String(cachePct) }),
        )
      : null,
  );
}

function statCell(label: string, value: string, variant?: string): HTMLElement {
  return h(
    `div.cstat${variant ? `.v-${variant}` : ""}`,
    null,
    h("div.l", null, label),
    h("div.v", null, value),
  );
}

function headersBody(entry: TrafficEntry): HTMLElement {
  return h(
    "div",
    null,
    h(
      "div.section",
      null,
      h("h3", null, t("detail.requestHeaders")),
      headersTable(entry.requestHeaders ?? {}),
    ),
    h(
      "div.section",
      null,
      h("h3", null, t("detail.responseHeaders")),
      headersTable(entry.responseHeaders ?? {}),
    ),
  );
}

function headersTable(headers: Record<string, string>): HTMLElement {
  const entries = Object.entries(headers);
  const wrap = h("div.headers");
  if (entries.length === 0) {
    wrap.appendChild(h("span.hk", null, "—"));
    wrap.appendChild(h("span.hv", null, ""));
    return wrap;
  }
  for (const [k, v] of entries) {
    wrap.appendChild(h("span.hk", null, k));
    wrap.appendChild(h("span.hv", null, redact(k, v)));
  }
  return wrap;
}

function kv(label: string, value: string): HTMLElement {
  const span = h("span");
  span.style.display = "contents";
  span.appendChild(h("dt", null, label));
  span.appendChild(h("dd", null, value));
  return span;
}

function redact(key: string, value: string): string {
  const k = key.toLowerCase();
  if (k === "authorization" || k.includes("api-key") || k.includes("apikey")) {
    if (value.length <= 8) return "•".repeat(value.length);
    return `${value.slice(0, 4)}…${value.slice(-2)}`;
  }
  return value;
}
