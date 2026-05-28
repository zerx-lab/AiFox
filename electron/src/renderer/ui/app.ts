// App shell. Owns the root mount and re-renders on any state change.
// Components are pure functions of state, so a full re-render is cheap
// for the data volumes we deal with (a few hundred entries max).

import { onLanguageChange } from "../i18n";
import { renderDetail } from "./detail";
import { h, mount } from "./dom";
import { renderSettings } from "./settings";
import { renderSidebar } from "./sidebar";
import { renderStatusbar } from "./statusbar";
import { renderTimeline } from "./timeline";
import { renderTitlebar } from "./titlebar";
import { renderTopbar } from "./topbar";
import { getState, onChange } from "./state";

export function mountApp(root: HTMLElement) {
  const render = () => {
    // Full re-mount blows away DOM, including scroll positions of the three
    // scroll containers. Snapshot them before, restore after — otherwise a
    // click on a bottom-of-list card jumps the timeline back to the top.
    const scrolls = snapshotScrolls(root);
    mount(root, renderShell());
    restoreScrolls(root, scrolls);
  };
  render();
  onChange(render);
  onLanguageChange(render);
}

const SCROLL_SELECTORS = [".side-list", ".tl-body", ".tl-generic", ".detail-body"] as const;

function snapshotScrolls(root: HTMLElement): Record<string, number> {
  const out: Record<string, number> = {};
  for (const sel of SCROLL_SELECTORS) {
    const el = root.querySelector(sel);
    if (el) out[sel] = el.scrollTop;
  }
  return out;
}

function restoreScrolls(root: HTMLElement, scrolls: Record<string, number>) {
  for (const sel of SCROLL_SELECTORS) {
    const el = root.querySelector(sel);
    const v = scrolls[sel];
    if (el && v != null) el.scrollTop = v;
  }
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
  return h(
    "div.view-traffic",
    null,
    renderSidebar(),
    renderTimeline(),
    renderDetail(),
  );
}
