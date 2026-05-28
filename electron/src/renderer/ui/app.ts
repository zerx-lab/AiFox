// App shell. Owns the root mount and re-renders on any state change.
// Components are pure functions of state, so a full re-render is cheap
// for the data volumes we deal with (a few hundred entries max).

import { onLanguageChange } from "../i18n";
import { renderBottomPane } from "./bottom-pane";
import { renderDetail } from "./detail";
import { h, mount } from "./dom";
import { renderFilterPills } from "./filter";
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

const SCROLL_SELECTORS = [
  ".side-list",
  ".tl-body",
  ".tl-generic",
  ".detail-body",
  ".bottom-body",
] as const;

// Containers that behave like a log tail: if the user was already at the
// bottom we re-stick to the bottom after the re-render so newly appended
// content scrolls into view, instead of restoring the old (now mid-list)
// scrollTop. Scrolling up cancels the stickiness automatically — the
// snapshot below records the live position each frame.
const STICKY_BOTTOM: ReadonlySet<string> = new Set([".bottom-body"]);
const STICKY_THRESHOLD_PX = 4;

interface ScrollSnap {
  top: number;
  atBottom: boolean;
}

function snapshotScrolls(root: HTMLElement): Record<string, ScrollSnap> {
  const out: Record<string, ScrollSnap> = {};
  for (const sel of SCROLL_SELECTORS) {
    const el = root.querySelector(sel);
    if (!el) continue;
    const atBottom =
      STICKY_BOTTOM.has(sel) &&
      el.scrollHeight - el.scrollTop - el.clientHeight <= STICKY_THRESHOLD_PX;
    out[sel] = { top: el.scrollTop, atBottom };
  }
  return out;
}

function restoreScrolls(root: HTMLElement, scrolls: Record<string, ScrollSnap>) {
  for (const sel of SCROLL_SELECTORS) {
    const el = root.querySelector(sel);
    const snap = scrolls[sel];
    if (!el || !snap) continue;
    if (snap.atBottom) {
      el.scrollTop = el.scrollHeight;
    } else {
      el.scrollTop = snap.top;
    }
  }
}

function renderShell(): HTMLElement {
  const state = getState();
  const isMac = state.env?.platform === "darwin";
  const shell = h(
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
  // Drive the bottom-pane height from state so the user's resize sticks
  // across re-renders.
  const h2 = state.bottomCollapsed ? 0 : state.bottomHeight;
  shell.style.setProperty("--bottom-h", `${h2}px`);
  return shell;
}

function renderTrafficView(): HTMLElement {
  const center = h(
    "div.center-stack",
    null,
    renderFilterPills(),
    renderTimeline(),
    renderBottomPane(),
  );
  return h(
    "div.view-traffic",
    null,
    renderSidebar(),
    center,
    renderDetail(),
  );
}
