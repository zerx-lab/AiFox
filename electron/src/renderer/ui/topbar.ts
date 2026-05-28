// Top bar: brand, proxy endpoint chip, proxy on/off toggle, view tabs,
// theme + language selectors. Sits below the titlebar so the drag region
// stays clean.

import { getClient } from "../../api/client";
import { getLanguage, setLanguage, supportedLanguages, t } from "../i18n";
import { h } from "./dom";
import { getState, setState, type Settings } from "./state";
import { setTheme, type ThemeChoice } from "./theme";

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
        const client = await getClient();
        const desired = !proxyEnabled;
        const { data } = await client.PUT("/v1/proxy", {
          body: { enabled: desired },
        });
        if (data) {
          setState({ proxy: data });
          const current = getState().settings;
          if (current) {
            setState({ settings: { ...current, proxyEnabled: data.enabled } });
          }
        }
      },
    },
    h("span.proxy-toggle-dot"),
    h("span", null, proxyEnabled ? t("topbar.proxyOn") : t("topbar.proxyOff")),
  );

  const nav = h(
    "div.nav",
    null,
    navButton("traffic", t("nav.traffic")),
    navButton("settings", t("nav.settings")),
  );

  const themeSelect = makeSelect(
    state.settings?.theme ?? "",
    [
      ["", t("topbar.themeSystem")],
      ["dark", t("topbar.themeDark")],
      ["light", t("topbar.themeLight")],
    ],
    async (val) => {
      setTheme(val as ThemeChoice);
      await persistSetting({ theme: val as Settings["theme"] });
    },
  );

  const langSelect = makeSelect(
    getLanguage(),
    supportedLanguages().map((c) => [c, c === "en" ? "English" : "中文"]),
    async (val) => {
      setLanguage(val as never);
      await persistSetting({ language: val as Settings["language"] });
    },
  );

  return h(
    "div.topbar",
    null,
    proxyChip,
    proxyToggle,
    h("span.topbar-spacer"),
    nav,
    themeSelect,
    langSelect,
  );
}

function navButton(view: "traffic" | "settings", label: string): HTMLElement {
  const state = getState();
  return h(
    "button",
    {
      class: state.view === view ? "active" : "",
      onclick: () => setState({ view }),
    },
    label,
  );
}

function makeSelect(
  value: string,
  options: readonly (readonly [string, string])[],
  onchange: (v: string) => void | Promise<void>,
): HTMLSelectElement {
  const sel = document.createElement("select");
  sel.className = "topbar-select";
  for (const [val, label] of options) {
    const opt = document.createElement("option");
    opt.value = val;
    opt.textContent = label;
    if (val === value) opt.selected = true;
    sel.appendChild(opt);
  }
  sel.addEventListener("change", () => {
    void onchange(sel.value);
  });
  return sel;
}

async function persistSetting(patch: Partial<Settings>) {
  const client = await getClient();
  const current = getState().settings;
  if (!current) return;
  const next: Settings = { ...current, ...patch };
  const { data } = await client.PUT("/v1/settings", { body: next });
  setState({ settings: data ?? next });
}

function flashCopied(btn: HTMLElement) {
  const original = btn.textContent;
  btn.textContent = t("topbar.copied");
  window.setTimeout(() => {
    btn.textContent = original;
  }, 1100);
}
