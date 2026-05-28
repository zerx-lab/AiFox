// Right-hand detail pane for the selected traffic entry. Three tabs:
// overview / request / response. Local tab state is kept in a module-level
// var (single pane shown at a time, so this is fine without a state lib).

import { t } from "../i18n";
import { formatBody } from "./body";
import { hasConversation, renderConversation } from "./conversation";
import { h } from "./dom";
import { fmtBytes, fmtClock, fmtDuration, isPending } from "./format";
import { highlight } from "./highlight";
import { getState, type TrafficEntry } from "./state";

type Tab = "overview" | "conversation" | "request" | "response";
let activeTab: Tab = "overview";

export function renderDetail(): HTMLElement {
  const state = getState();
  const entry = state.entries.find((e) => e.id === state.selectedId) ?? null;

  if (!entry) {
    return h("div.detail", null, h("div.detail-empty", null, t("detail.selectPrompt")));
  }

  // Clamp the active tab so when the user moves to an entry without a
  // structured analysis we don't get stuck on a hidden tab.
  if (activeTab === "conversation" && !hasConversation(entry)) {
    activeTab = "overview";
  }

  return h(
    "div.detail",
    null,
    detailHead(entry),
    detailTabs(entry),
    h("div.detail-body", null, renderTabBody(entry)),
  );
}

function detailHead(entry: TrafficEntry): HTMLElement {
  return h(
    "div.detail-head",
    null,
    h("div.h1", null, `${entry.method} ${entry.url}`),
    h(
      "div.h2",
      null,
      h("span", null, `${entry.statusCode || "···"}`),
      h("span", null, fmtDuration(entry.durationMillis)),
      h("span", null, fmtClock(entry.startedAt)),
      entry.streaming ? h("span", null, t("detail.streaming")) : null,
    ),
  );
}

function detailTabs(entry: TrafficEntry): HTMLElement {
  const tabs: HTMLElement[] = [tabBtn("overview", t("detail.tabs.overview"))];
  if (hasConversation(entry)) {
    tabs.push(tabBtn("conversation", t("detail.tabs.conversation")));
  }
  tabs.push(tabBtn("request", t("detail.tabs.request")));
  tabs.push(tabBtn("response", t("detail.tabs.response")));
  return h("div.detail-tabs", null, ...tabs);
}

function tabBtn(tab: Tab, label: string): HTMLElement {
  return h(
    "button",
    {
      class: activeTab === tab ? "active" : "",
      onclick: () => {
        activeTab = tab;
        // Re-render is triggered by parent on state change, but tab switch
        // doesn't touch app state — so re-render the detail pane in place.
        const node = document.querySelector(".detail");
        if (!node || !(node instanceof HTMLElement)) return;
        node.replaceWith(renderDetail());
      },
    },
    label,
  );
}

function renderTabBody(entry: TrafficEntry): HTMLElement {
  if (activeTab === "overview") return overviewBody(entry);
  if (activeTab === "conversation") return renderConversation(entry);
  if (activeTab === "request")
    return ioBody({ headers: entry.requestHeaders ?? {}, body: entry.requestBody ?? "" });
  return ioBody({ headers: entry.responseHeaders ?? {}, body: entry.responseBody ?? "" });
}

function overviewBody(entry: TrafficEntry): HTMLElement {
  const banners = h("div");
  if (entry.error) banners.appendChild(h("div.banner.err", null, `${t("detail.error")}: ${entry.error}`));
  if (entry.truncated) banners.appendChild(h("div.banner.warn", null, t("detail.truncated")));
  if (isPending(entry)) banners.appendChild(h("div.banner.info", null, t("detail.pending")));

  return h(
    "div",
    null,
    banners,
    h(
      "dl.kv",
      null,
      kv(t("detail.method"), entry.method),
      kv(t("detail.status"), entry.statusCode > 0 ? String(entry.statusCode) : "—"),
      kv(t("detail.started"), fmtClock(entry.startedAt)),
      kv(t("detail.duration"), fmtDuration(entry.durationMillis)),
      kv(t("detail.upstream"), entry.upstreamUrl || "—"),
      kv(t("detail.requestSize"), fmtBytes(entry.requestSize)),
      kv(t("detail.responseSize"), fmtBytes(entry.responseSize)),
      kv(t("detail.streaming"), entry.streaming ? "✓" : "—"),
    ),
  );
}

function kv(label: string, value: string): HTMLElement {
  const dt = h("dt", null, label);
  const dd = h("dd", null, value);
  const wrap = document.createDocumentFragment();
  wrap.appendChild(dt);
  wrap.appendChild(dd);
  // Returning an HTMLElement is required, so wrap in a no-op span; the parent
  // dl uses flex via grid so structure is preserved.
  const span = h("span");
  span.style.display = "contents";
  span.appendChild(dt);
  span.appendChild(dd);
  return span;
}

function ioBody(io: { headers: Record<string, string>; body: string }): HTMLElement {
  const headerRows = h("div.headers");
  const entries = Object.entries(io.headers);
  if (entries.length === 0) {
    headerRows.appendChild(h("span.hk", null, "—"));
    headerRows.appendChild(h("span.hv", null, ""));
  }
  for (const [k, v] of entries) {
    headerRows.appendChild(h("span.hk", null, k));
    headerRows.appendChild(h("span.hv", null, redact(k, v)));
  }

  let bodyEl: HTMLElement;
  let kindBadge: HTMLElement | null = null;
  if (io.body) {
    const formatted = formatBody(io.body, io.headers);
    const pre = document.createElement("pre");
    pre.className = `codebox kind-${formatted.kind}`;
    pre.appendChild(highlight(formatted.text, formatted.kind));
    bodyEl = pre;
    kindBadge = h("span.body-kind", null, formatted.kind.toUpperCase());
  } else {
    bodyEl = h("pre.codebox.empty", null, t("detail.bodyEmpty"));
  }

  const bodyHeader = h(
    "div.section-head",
    null,
    h("h3", null, t("detail.body")),
    kindBadge,
  );

  return h(
    "div",
    null,
    h("div.section", null, h("h3", null, t("detail.headers")), headerRows),
    h("div.section", null, bodyHeader, bodyEl),
  );
}

// Hide the API key when it appears in a header value (e.g. Authorization,
// x-api-key) to avoid shoulder-surfing in screenshots.
function redact(key: string, value: string): string {
  const k = key.toLowerCase();
  if (k === "authorization" || k.includes("api-key") || k.includes("apikey")) {
    if (value.length <= 8) return "•".repeat(value.length);
    return `${value.slice(0, 4)}…${value.slice(-2)}`;
  }
  return value;
}
