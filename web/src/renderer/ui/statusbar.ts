import { t } from "../i18n";
import { h } from "./dom";
import { fmtBytes } from "./format";
import { getEntryTotals, getState, setBottomTab, setState } from "./state";

export function renderStatusbar(): HTMLElement {
  const s = getState();
  const total = s.entries.length;
  const lastError = s.entries.find((e) => e.error)?.error ?? "";

  const proxyText =
    s.proxy?.enabled && s.proxy.address
      ? t("status.listening", { address: s.proxy.address })
      : t("status.notListening");

  // Byte + token sums come from the incrementally-maintained totals (§4.3.4)
  // instead of an O(n) reduce on every render. Session-level cache hit rate
  // lives inside the right-hand Overview tab; the statusbar keeps the global
  // counters so the bottom strip still tells the user "is anything happening?".
  const totals = getEntryTotals();
  const bytesIn = totals.bytesIn;
  const bytesOut = totals.bytesOut;
  const tokAll =
    totals.inputTokens + totals.cacheRead + totals.cacheCreate + totals.outputTokens;
  // Global cache hit rate = cache reads / total input-side tokens (§ task 8).
  const inputAll = totals.inputTokens + totals.cacheRead + totals.cacheCreate;
  const hitPct = inputAll > 0 ? Math.round((totals.cacheRead / inputAll) * 100) : 0;
  const cost = totals.cost;

  const inSettings = s.view === "settings";
  const settingsBtn = h(
    "button",
    {
      class: `statusbar-settings${inSettings ? " active" : ""}`,
      title: t("nav.settings"),
      "aria-label": t("nav.settings"),
      "aria-pressed": inSettings ? "true" : "false",
      onclick: () => setState({ view: inSettings ? "traffic" : "settings" }),
    },
    gearIcon(),
  );

  // Paused-requests indicator (§4.1.1). Always visible while any request is
  // held at a breakpoint — even when the bottom pane is collapsed — because a
  // held request can stall the user's agent for minutes without other UI cues.
  // Clicking jumps to the Breakpoints tab (setBottomTab un-collapses the pane).
  const pausedCount = s.pausedRequests.length;
  const pausedEl =
    pausedCount > 0
      ? h(
          "button.statusbar-paused",
          {
            title: t("status.pausedTitle"),
            "aria-label": t("status.paused", { count: pausedCount }),
            onclick: () => setBottomTab("breakpoints"),
          },
          h("span.statusbar-paused-mark", null, "⏸"),
          t("status.paused", { count: pausedCount }),
        )
      : null;

  // SSE connection indicator — only shown when disconnected or reconnecting.
  const connEl =
    s.connection !== "live"
      ? h(
          "span.sse-status",
          { class: "sse-status sse-disconnected" },
          h("span.sse-dot", null),
          s.connection === "reconnecting"
            ? t("status.sseReconnecting")
            : t("status.sseDisconnected"),
        )
      : null;

  return h(
    "div.statusbar",
    null,
    settingsBtn,
    pausedEl,
    pausedEl ? h("span", null, "·") : null,
    connEl,
    connEl ? h("span", null, "·") : null,
    h("span", { class: s.proxy?.configured ? "ok" : "warn" }, proxyText),
    h("span", null, "·"),
    h("span", null, t("status.entries", { count: total })),
    h("span", null, "·"),
    h("span", null, `↑ ${fmtBytes(bytesIn)}`),
    h("span", null, `↓ ${fmtBytes(bytesOut)}`),
    tokAll > 0
      ? h(
          "span",
          null,
          "·",
          " ",
          t("status.tokens", { total: fmtTok(tokAll) }),
        )
      : null,
    inputAll > 0
      ? h("span", null, "·", " ", t("status.cached", { pct: String(hitPct) }))
      : null,
    cost > 0
      ? h("span", null, "·", " ", t("status.cost", { cost: `$${cost.toFixed(4)}` }))
      : null,
    h("span.spacer"),
    lastError ? h("span.err", null, lastError) : null,
  );
}

function fmtTok(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0";
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
  return String(n);
}

function gearIcon(): SVGElement {
  // Lucide-style outline settings cog (24-grid path rendered into 14px).
  const wrap = document.createElement("div");
  wrap.innerHTML =
    '<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1Z"/>' +
    '<circle cx="12" cy="12" r="3"/>' +
    "</svg>";
  return wrap.firstElementChild as SVGElement;
}
