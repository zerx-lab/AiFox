// Right-hand inspect panel for the selected traffic entry. Tabs are the
// "raw" facets — overview, headers, request body, response body — plus a
// contextual "Tool" tab that lights up when a tool_use block is selected in
// the center timeline.

import type { components } from "../../api/client";
import { t } from "../i18n";
import { formatBody } from "./body";
import { h } from "./dom";
import { fmtBytes, fmtClock, fmtDuration, isPending } from "./format";
import { highlight } from "./highlight";
import {
  getState,
  setDetailTab,
  type DetailTab,
  type TrafficEntry,
} from "./state";
import { findToolUseInEntry } from "./timeline";

type Analysis = components["schemas"]["Analysis"];
type AnthropicUsage = components["schemas"]["AnthropicUsage"];

export function renderDetail(): HTMLElement {
  const state = getState();
  const entry = state.entries.find((e) => e.id === state.selectedId) ?? null;

  if (!entry) {
    return h(
      "aside.detail",
      null,
      h("div.detail-empty", null, t("detail.selectPrompt")),
    );
  }

  // If the user clicked a tool_use we surface the Tool tab; otherwise clamp
  // back to "overview" so we don't get stuck on an inactive tab.
  let tab: DetailTab = state.detailTab;
  if (tab === "tool" && !state.selection.toolUseId) tab = "overview";

  return h(
    "aside.detail",
    null,
    detailHead(entry),
    detailTabs(tab),
    h("div.detail-body", null, renderTabBody(entry, tab)),
  );
}

function detailHead(entry: TrafficEntry): HTMLElement {
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

function detailTabs(tab: DetailTab): HTMLElement {
  const tabs: HTMLElement[] = [];
  tabs.push(tabBtn("overview", t("detail.tabs.overview"), tab));
  if (getState().selection.toolUseId) {
    tabs.push(tabBtn("tool", t("detail.tabs.tool"), tab));
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
  switch (tab) {
    case "overview":
      return overviewBody(entry);
    case "tool":
      return toolBody(entry);
    case "headers":
      return headersBody(entry);
    case "request":
      return ioBody(entry.requestBody ?? "", entry.requestHeaders ?? {});
    case "response":
      return ioBody(entry.responseBody ?? "", entry.responseHeaders ?? {});
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
  const model =
    analysis?.anthropic?.request?.model ||
    analysis?.anthropic?.response?.model ||
    "—";

  const usagePanel = usage ? renderUsage(usage) : null;

  return h(
    "div",
    null,
    banners,
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

function toolBody(entry: TrafficEntry): HTMLElement {
  const state = getState();
  const id = state.selection.toolUseId;
  if (!id) return h("div.detail-empty", null, t("detail.toolEmpty"));
  const { use, result } = findToolUseInEntry(entry, id);
  if (!use) return h("div.detail-empty", null, t("detail.toolMissing"));

  const sections = h("div");
  sections.appendChild(
    h(
      "div.section",
      null,
      h(
        "div.section-head",
        null,
        h("h3", null, t("detail.toolName")),
        h("span.body-kind", null, use.name ?? "?"),
      ),
      use.id ? h("div.detail-mono", null, use.id) : null,
    ),
  );
  sections.appendChild(
    h(
      "div.section",
      null,
      h("h3", null, t("detail.toolArgs")),
      jsonBox(use.input ?? {}),
    ),
  );
  if (result) {
    const isErr = result.isError === true;
    sections.appendChild(
      h(
        "div.section",
        null,
        h(
          "div.section-head",
          null,
          h("h3", null, t("detail.toolResult")),
          h(`span.body-kind${isErr ? ".err" : ".ok"}`, null, isErr ? "ERROR" : "OK"),
        ),
        renderToolResult(result.content),
      ),
    );
  } else {
    sections.appendChild(
      h("div.banner.info", null, t("detail.toolNoResult")),
    );
  }
  return sections;
}

function renderToolResult(content: unknown): HTMLElement {
  if (typeof content === "string") {
    return h("pre.codebox.kind-raw", null, content);
  }
  if (Array.isArray(content)) {
    const wrap = h("div");
    for (const c of content as Array<{ type?: string; text?: string }>) {
      if (c.type === "text" && typeof c.text === "string") {
        wrap.appendChild(h("pre.codebox.kind-raw", null, c.text));
      } else {
        wrap.appendChild(jsonBox(c));
      }
    }
    return wrap;
  }
  return jsonBox(content ?? null);
}

function jsonBox(value: unknown): HTMLElement {
  let text: string;
  try {
    text = typeof value === "string" ? value : JSON.stringify(value, null, 2);
  } catch {
    text = String(value);
  }
  const pre = document.createElement("pre");
  pre.className = "codebox kind-json";
  pre.appendChild(highlight(text, "json"));
  return pre;
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

function ioBody(body: string, headers: Record<string, string>): HTMLElement {
  let bodyEl: HTMLElement;
  let kindBadge: HTMLElement | null = null;
  if (body) {
    const formatted = formatBody(body, headers);
    const pre = document.createElement("pre");
    pre.className = `codebox kind-${formatted.kind}`;
    pre.appendChild(highlight(formatted.text, formatted.kind));
    bodyEl = pre;
    kindBadge = h("span.body-kind", null, formatted.kind.toUpperCase());
  } else {
    bodyEl = h("pre.codebox.empty", null, t("detail.bodyEmpty"));
  }

  return h(
    "div",
    null,
    h(
      "div.section",
      null,
      h("div.section-head", null, h("h3", null, t("detail.body")), kindBadge),
      bodyEl,
    ),
  );
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
