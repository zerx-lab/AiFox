// Custom titlebar that adapts per platform.
//
//   macOS  — native traffic lights live in the top-left (Electron's
//            `hiddenInset` style). We only render a draggable spacer that
//            preserves room for them and shows the app title centered.
//   Win/Linux — no native chrome, so we draw min / max / close buttons on
//            the right. The whole bar is draggable via -webkit-app-region.
//
// The theme + language toggles render as icon-only buttons in a `titlebar-
// tools` strip, placed to the left of the window controls (or at the trailing
// edge on macOS where there are no controls). Buttons sit outside the drag
// region (`no-drag`) so clicks reach them.

import { getLanguage, setLanguage, supportedLanguages, t } from "../i18n";
import { h } from "./dom";
import { customSelect, type SelectOption } from "./select";
import { getState, setState, type Settings } from "./state";
import { setTheme, type ThemeChoice } from "./theme";
import { getClient } from "../../api/client";

export function renderTitlebar(): HTMLElement {
  const isMac = getState().env?.platform === "darwin";
  const maximized = getState().windowMaximized;

  const tools = h(
    "div.titlebar-tools",
    null,
    renderThemeToggle(),
    renderLangToggle(),
  );

  if (isMac) {
    return h(
      "div.titlebar.mac",
      null,
      h("div.titlebar-spacer-traffic"),
      h(
        "div.titlebar-drag",
        null,
        h("span.titlebar-title", null, t("app.title")),
      ),
      tools,
    );
  }

  return h(
    "div.titlebar",
    null,
    h(
      "div.titlebar-drag",
      null,
      h("span.titlebar-brand-mark"),
      h("span.titlebar-title", null, t("app.title")),
    ),
    tools,
    h(
      "div.titlebar-actions",
      null,
      windowButton("min", t("titlebar.minimize"), minimizeIcon(), () => {
        void window.aiFox.window.minimize();
      }),
      windowButton(
        "max",
        maximized ? t("titlebar.restore") : t("titlebar.maximize"),
        maximized ? restoreIcon() : maximizeIcon(),
        () => {
          void window.aiFox.window.maximizeToggle();
        },
      ),
      windowButton("close", t("titlebar.close"), closeIcon(), () => {
        void window.aiFox.window.close();
      }),
    ),
  );
}

function renderThemeToggle(): HTMLElement {
  const state = getState();
  const value = state.settings?.theme ?? "";
  const options: SelectOption[] = [
    { value: "", label: t("topbar.themeSystem"), icon: themeIcon("system") },
    { value: "dark", label: t("topbar.themeDark"), icon: themeIcon("dark") },
    { value: "light", label: t("topbar.themeLight"), icon: themeIcon("light") },
  ];
  return customSelect({
    value,
    options,
    iconOnly: true,
    ariaLabel: t("topbar.theme"),
    className: "titlebar-tool-btn",
    align: "end",
    menuClassName: "titlebar-tool-menu",
    // Fixed contrast icon so the trigger is recognizably "theme" no matter
    // which option is selected — otherwise the system/monitor icon at 14px
    // gets visually confused with the neighbouring globe.
    triggerIcon: contrastIcon(),
    onChange: async (val) => {
      setTheme(val as ThemeChoice);
      await persistSetting({ theme: val as Settings["theme"] });
    },
  });
}

function renderLangToggle(): HTMLElement {
  const value = getLanguage();
  const options: SelectOption[] = supportedLanguages().map((code) => ({
    value: code,
    label: code === "en" ? "English" : "中文",
  }));
  return customSelect({
    value,
    options,
    iconOnly: true,
    ariaLabel: t("topbar.language"),
    className: "titlebar-tool-btn",
    triggerIcon: globeIcon(),
    align: "end",
    menuClassName: "titlebar-tool-menu",
    onChange: async (val) => {
      setLanguage(val as never);
      await persistSetting({ language: val as Settings["language"] });
    },
  });
}

async function persistSetting(patch: Partial<Settings>) {
  const client = await getClient();
  const current = getState().settings;
  if (!current) return;
  const next: Settings = { ...current, ...patch };
  const { data } = await client.PUT("/v1/settings", { body: next });
  setState({ settings: data ?? next });
}

function windowButton(
  variant: "min" | "max" | "close",
  label: string,
  svg: SVGElement,
  onclick: () => void,
): HTMLElement {
  const btn = h("button", {
    class: `titlebar-btn ${variant}`,
    title: label,
    "aria-label": label,
    onclick,
  });
  btn.appendChild(svg);
  return btn;
}

function minimizeIcon(): SVGElement {
  return iconSvg(
    '<path d="M2 6 H10" stroke="currentColor" stroke-width="1.2" fill="none" />',
  );
}
function maximizeIcon(): SVGElement {
  return iconSvg(
    '<rect x="2" y="2" width="8" height="8" stroke="currentColor" stroke-width="1.2" fill="none" />',
  );
}
function restoreIcon(): SVGElement {
  return iconSvg(
    '<rect x="2" y="4" width="6" height="6" stroke="currentColor" stroke-width="1.2" fill="none" />' +
      '<path d="M4 4 V2 H10 V8 H8" stroke="currentColor" stroke-width="1.2" fill="none" />',
  );
}
function closeIcon(): SVGElement {
  return iconSvg(
    '<path d="M2 2 L10 10 M10 2 L2 10" stroke="currentColor" stroke-width="1.2" fill="none" />',
  );
}

function iconSvg(inner: string): SVGElement {
  const wrap = document.createElement("div");
  wrap.innerHTML = `<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 12 12">${inner}</svg>`;
  return wrap.firstElementChild as SVGElement;
}

function themeIcon(kind: "system" | "dark" | "light"): SVGElement {
  if (kind === "dark") {
    // crescent moon
    return iconSvg14(
      '<path d="M11.5 9.2 A5.5 5.5 0 1 1 4.8 2.5 A4.4 4.4 0 0 0 11.5 9.2 Z" stroke="currentColor" stroke-width="1.1" fill="currentColor" fill-opacity="0.18" stroke-linejoin="round"/>',
    );
  }
  if (kind === "light") {
    // sun
    return iconSvg14(
      '<circle cx="7" cy="7" r="2.4" stroke="currentColor" stroke-width="1.1" fill="currentColor" fill-opacity="0.18"/>' +
        '<g stroke="currentColor" stroke-width="1.1" stroke-linecap="round">' +
        '<path d="M7 1.6 V3.0"/><path d="M7 11.0 V12.4"/>' +
        '<path d="M1.6 7 H3.0"/><path d="M11.0 7 H12.4"/>' +
        '<path d="M2.9 2.9 L3.9 3.9"/><path d="M10.1 10.1 L11.1 11.1"/>' +
        '<path d="M11.1 2.9 L10.1 3.9"/><path d="M3.9 10.1 L2.9 11.1"/>' +
        "</g>",
    );
  }
  // monitor / system
  return iconSvg14(
    '<rect x="2" y="3" width="10" height="7" rx="1.2" stroke="currentColor" stroke-width="1.1" fill="none"/>' +
      '<path d="M5 12 H9" stroke="currentColor" stroke-width="1.1" stroke-linecap="round"/>' +
      '<path d="M7 10 V12" stroke="currentColor" stroke-width="1.1"/>',
  );
}

function globeIcon(): SVGElement {
  return iconSvg14(
    '<circle cx="7" cy="7" r="5.2" stroke="currentColor" stroke-width="1.1" fill="none"/>' +
      '<ellipse cx="7" cy="7" rx="2.4" ry="5.2" stroke="currentColor" stroke-width="1.1" fill="none"/>' +
      '<path d="M1.9 7 H12.1" stroke="currentColor" stroke-width="1.1"/>',
  );
}

function contrastIcon(): SVGElement {
  // Half-filled circle — universally read as a theme/contrast switcher.
  return iconSvg14(
    '<circle cx="7" cy="7" r="5.2" stroke="currentColor" stroke-width="1.1" fill="none"/>' +
      '<path d="M7 1.8 A5.2 5.2 0 0 0 7 12.2 Z" fill="currentColor"/>',
  );
}

function iconSvg14(inner: string): SVGElement {
  const wrap = document.createElement("div");
  wrap.innerHTML = `<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 14 14">${inner}</svg>`;
  return wrap.firstElementChild as SVGElement;
}
