// Right-hand inspect panel for the selected traffic entry. Tabs span the
// "raw" facets (overview, headers, request, response) plus three structured
// analyses (cache, tokens, tools) that surface what the parser has
// extracted from an Anthropic Messages call.

import type { components } from "../../api/client";
import { t } from "../i18n";
import { renderCache } from "./cache";
import { colResizeHandle } from "./col-resize";
import { h } from "./dom";
import { fmtBytes, fmtClock, fmtDuration, isPending } from "./format";
import { renderRawRequest, renderRawResponse } from "./raw-http";
import {
  type DetailTab,
  getState,
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
      detailTabs("overview", meta.hasStructured),
      h("div.detail-body", null, h("div.detail-loading", null, t("detail.loading"))),
    );
  }

  const analysis = entry.analysis as Analysis | undefined;
  const hasAnthropic = !!analysis?.anthropic;

  // Tabs are dynamic: cache/tokens/tools only show up if we actually have
  // structured data; otherwise the user sees the raw side only.
  let tab: DetailTab = state.detailTab;
  if (!hasAnthropic && (tab === "cache" || tab === "tokens" || tab === "tools")) {
    tab = "overview";
  }

  return h(
    "aside.detail",
    null,
    colResizeHandle("right"),
    detailHead(entry),
    detailTabs(tab, hasAnthropic),
    h("div.detail-body", null, renderTabBody(entry, tab)),
  );
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

function detailTabs(tab: DetailTab, hasAnthropic: boolean): HTMLElement {
  const tabs: HTMLElement[] = [];
  tabs.push(tabBtn("overview", t("detail.tabs.overview"), tab));
  if (hasAnthropic) {
    tabs.push(tabBtn("cache", t("detail.tabs.cache"), tab));
    tabs.push(tabBtn("tokens", t("detail.tabs.tokens"), tab));
    tabs.push(tabBtn("tools", t("detail.tabs.tools"), tab));
  }
  tabs.push(tabBtn("headers", t("detail.tabs.headers"), tab));
  tabs.push(tabBtn("request", t("detail.tabs.request"), tab));
  tabs.push(tabBtn("response", t("detail.tabs.response"), tab));
  return h("div.detail-tabs", null, ...tabs);
}

function tabBtn(tab: DetailTab, label: string, active: DetailTab): HTMLElement {
  return h(
    "button",
    {
      class: active === tab ? "active" : "",
      onclick: () => setDetailTab(tab),
    },
    label,
  );
}

function renderTabBody(entry: TrafficEntry, tab: DetailTab): HTMLElement {
  const analysis = entry.analysis as Analysis | undefined;
  switch (tab) {
    case "overview":
      return overviewBody(entry);
    case "cache":
      return renderCache(analysis?.anthropic);
    case "tokens": {
      const usage = analysis?.anthropic?.response?.usage as AnthropicUsage | undefined;
      const model =
        analysis?.anthropic?.request?.model ||
        analysis?.anthropic?.response?.model ||
        undefined;
      return renderTokens(usage, model);
    }
    case "tools":
      return renderTools(entry);
    case "headers":
      return headersBody(entry);
    case "request":
      return renderRawRequest(entry);
    case "response":
      return renderRawResponse(entry);
  }
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
