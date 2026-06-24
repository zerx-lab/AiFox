// Top bar: proxy endpoint chip + proxy on/off toggle.
// View navigation lives in the statusbar (settings cog) and theme / language
// toggles live in the titlebar (icon-only), so the topbar stays focused on
// the proxy state — the thing the user actually interacts with constantly.

import { t } from "../i18n";
import { setProxyEnabled } from "./api-service";
import { h } from "./dom";
import { getState, setReplayOpen, setState } from "./state";

export function renderTopbar(): HTMLElement {
  const state = getState();
  const proxyConfigured = state.proxy?.configured ?? false;
  const proxyEnabled = state.proxy?.enabled ?? false;
  const proxyUrl = state.proxy?.baseUrl ?? "";

  const chipClass = !proxyConfigured
    ? "proxy-chip warn"
    : proxyEnabled
      ? "proxy-chip"
      : "proxy-chip off";

  const proxyChip = h(
    "div",
    { class: chipClass, title: proxyUrl },
    h("span.dot"),
    h("span", null, t("topbar.proxyEndpoint")),
    h("strong", null, proxyUrl || "—"),
    h(
      "button.copy",
      {
        onclick: async (e: Event) => {
          e.stopPropagation();
          if (!proxyUrl) return;
          await navigator.clipboard.writeText(proxyUrl);
          flashCopied(e.currentTarget as HTMLElement);
        },
        title: t("topbar.copy"),
      },
      t("topbar.copy"),
    ),
  );

  const proxyToggle = h(
    "button",
    {
      class: `proxy-toggle${proxyEnabled ? " on" : " off"}`,
      title: proxyEnabled ? t("topbar.proxyToggleOff") : t("topbar.proxyToggleOn"),
      onclick: async () => {
        const res = await setProxyEnabled(!proxyEnabled);
        if (res.ok) {
          setState({ proxy: res.data });
          const current = getState().settings;
          if (current) {
            setState({ settings: { ...current, proxyEnabled: res.data.enabled } });
          }
        }
      },
    },
    h("span.proxy-toggle-dot"),
    h("span", null, proxyEnabled ? t("topbar.proxyOn") : t("topbar.proxyOff")),
  );

  const replayBtn = h(
    "button",
    {
      class: `topbar-action${state.replayOpen ? " active" : ""}`,
      disabled: !state.selectedId,
      title: t("replay.button"),
      onclick: () => setReplayOpen(!state.replayOpen),
    },
    h("span", null, "↻"),
    h("span", null, t("replay.button")),
  );

  return h(
    "div.topbar",
    null,
    proxyChip,
    proxyToggle,
    h("span.topbar-spacer"),
    replayBtn,
  );
}

function flashCopied(btn: HTMLElement) {
  const original = btn.textContent;
  btn.textContent = t("topbar.copied");
  window.setTimeout(() => {
    btn.textContent = original;
  }, 1100);
}
