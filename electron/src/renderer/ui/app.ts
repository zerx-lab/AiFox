// App shell. Owns the root mount and re-renders on any state change.
// Components are pure functions of state, so a full re-render is cheap
// for the data volumes we deal with (a few hundred entries max).

import { onLanguageChange } from "../i18n";
import { renderDetail } from "./detail";
import { h, mount } from "./dom";
import { renderSettings } from "./settings";
import { renderSidebar } from "./sidebar";
import { renderStatusbar } from "./statusbar";
import { renderTitlebar } from "./titlebar";
import { renderTopbar } from "./topbar";
import { getState, onChange } from "./state";

export function mountApp(root: HTMLElement) {
  const render = () => mount(root, renderShell());
  render();
  onChange(render);
  onLanguageChange(render);
}

function renderShell(): HTMLElement {
  const state = getState();
  const isMac = state.env?.platform === "darwin";
  return h(
    "div",
    { class: `app${isMac ? " app-mac" : " app-frameless"}` },
    renderTitlebar(),
    renderTopbar(),
    h(
      "div.main",
      null,
      state.view === "settings" ? renderSettings() : renderTrafficView(),
    ),
    renderStatusbar(),
  );
}

function renderTrafficView(): HTMLElement {
  return h("div.view-traffic", null, renderSidebar(), renderDetail());
}
