// Bottom pane — DevTools-style dock with Console / Variables / Problems /
// Breakpoints tabs. Height is user-resizable via the top edge; collapse
// button parks it at zero. Breakpoints tab is rendered as a placeholder
// until Milestone G replaces it.

import { t } from "../i18n";
import { renderBreakpoints } from "./breakpoints";
import { renderConsole } from "./console";
import { h } from "./dom";
import { renderProblems } from "./problems";
import {
  type BottomTab,
  getState,
  setBottomHeight,
  setBottomTab,
  toggleBottomCollapsed,
} from "./state";
import { renderVariables } from "./variables";

export function renderBottomPane(): HTMLElement {
  const state = getState();
  const tab = state.bottomTab;
  const collapsed = state.bottomCollapsed;

  const counts = problemCount();

  const wrap = h(
    "div",
    {
      class: `bottom-pane${collapsed ? " collapsed" : ""}`,
    },
    h(
      "div.bottom-resize",
      {
        onmousedown: startResize,
        title: t("bottom.resize"),
      },
    ),
    h(
      "div.bottom-tabs",
      null,
      tabBtn("console", t("bottom.tabs.console"), tab),
      tabBtn("variables", t("bottom.tabs.variables"), tab),
      tabBtn("problems", t("bottom.tabs.problems"), tab, counts > 0 ? counts : undefined),
      tabBtn(
        "breakpoints",
        t("bottom.tabs.breakpoints"),
        tab,
        getState().pausedRequests.length > 0
          ? getState().pausedRequests.length
          : undefined,
      ),
      h("span.bottom-tabs-spacer"),
      h(
        "button.bottom-collapse",
        {
          onclick: toggleBottomCollapsed,
          title: collapsed ? t("bottom.expand") : t("bottom.collapse"),
        },
        collapsed ? "▴" : "▾",
      ),
    ),
    collapsed
      ? null
      : h("div.bottom-body", null, renderTabBody(tab)),
  );
  return wrap;
}

function tabBtn(
  tab: BottomTab,
  label: string,
  active: BottomTab,
  badge?: number,
): HTMLElement {
  return h(
    "button",
    {
      class: `bottom-tab${active === tab ? " active" : ""}`,
      onclick: () => setBottomTab(tab),
    },
    label,
    badge !== undefined ? h("span.bottom-badge", null, String(badge)) : null,
  );
}

function renderTabBody(tab: BottomTab): HTMLElement {
  switch (tab) {
    case "console":
      return renderConsole();
    case "variables":
      return renderVariables();
    case "problems":
      return renderProblems();
    case "breakpoints":
      return renderBreakpoints();
  }
}

function problemCount(): number {
  // Cheap heuristic so the badge doesn't run a second collector. Matches the
  // first-pass criteria used by problems.ts.
  let n = 0;
  for (const e of getState().entries) {
    if (e.error || e.statusCode >= 400) n += 1;
  }
  return n;
}

// ---- Resize ----------------------------------------------------------------

function startResize(ev: MouseEvent) {
  ev.preventDefault();
  const startY = ev.clientY;
  const startH = getState().bottomHeight;
  const move = (mv: MouseEvent) => {
    const delta = startY - mv.clientY;
    setBottomHeight(startH + delta);
  };
  const up = () => {
    window.removeEventListener("mousemove", move);
    window.removeEventListener("mouseup", up);
  };
  window.addEventListener("mousemove", move);
  window.addEventListener("mouseup", up);
}
