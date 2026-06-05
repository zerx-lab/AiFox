// App shell with rAF-coalesced, region-scoped rendering.
//
// The old shell re-mounted the ENTIRE DOM on every state change. Under
// concurrent streaming that meant dozens of full re-mounts per second — the
// dominant cause of UI lag. Now:
//   - state changes are coalesced to at most one render per animation frame
//     (requestAnimationFrame), so a burst of SSE events collapses to one pass;
//   - the tree is split into independent regions, each re-rendered only when
//     the version slice it depends on (see state.ts `versions`) actually
//     changed. A token tick re-renders only the statusbar; a new entry
//     re-renders the sidebar but NOT the (expensive) selected-entry timeline;
//     a selection change re-renders the timeline/detail but leaves the rest.
//
// Switching the top-level view (traffic ↔ settings) restructures the tree, so
// that path does a full rebuild — it is rare and user-driven.

import { onLanguageChange } from "../i18n";
import { renderBottomPane } from "./bottom-pane";
import { renderDetail } from "./detail";
import { clear, h } from "./dom";
import { renderFilterPills } from "./filter";
import { renderSettings } from "./settings";
import { renderSidebar } from "./sidebar";
import { renderStatusbar } from "./statusbar";
import { renderTimeline } from "./timeline";
import { renderTitlebar } from "./titlebar";
import { renderTopbar } from "./topbar";
import { getState, onChange, versions } from "./state";

type VKind = keyof typeof versions;

interface Region {
  deps: VKind[];
  build: () => HTMLElement;
  el: HTMLElement;
  key: string;
  // Optional in-place updater. When present and it returns true, the region's
  // root element is kept (no replaceWith) — used by the detail panel to swap
  // only its inner body, preserving element identity so the panel doesn't flash.
  reconcile?: (oldEl: HTMLElement, newEl: HTMLElement) => boolean;
}

const SCROLL_SELECTORS = [
  ".side-list",
  ".tl-body",
  ".tl-generic",
  ".detail-body",
  ".bottom-body",
] as const;
// Log-tail containers: if the user was at the bottom, re-stick to the bottom
// after a rebuild so appended content scrolls into view.
const STICKY_BOTTOM: ReadonlySet<string> = new Set([".bottom-body"]);
const STICKY_THRESHOLD_PX = 4;

export function mountApp(root: HTMLElement) {
  let regions: Region[] = [];
  let shellEl: HTMLElement;
  let lastView = getState().view;
  let lastBodyVer = versions.body;

  const depsKey = (deps: VKind[]) => deps.map((d) => versions[d]).join(":");

  const region = (
    deps: VKind[],
    build: () => HTMLElement,
    reconcile?: (oldEl: HTMLElement, newEl: HTMLElement) => boolean,
  ): HTMLElement => {
    const el = build();
    regions.push({ deps, build, el, key: depsKey(deps), reconcile });
    return el;
  };

  function buildShell(): HTMLElement {
    regions = [];
    const state = getState();
    const isMac = state.env?.platform === "darwin";

    const titlebar = region(["ui"], renderTitlebar);
    const topbar = region(["ui"], renderTopbar);

    let mainChild: HTMLElement;
    if (state.view === "settings") {
      mainChild = region(["ui"], renderSettings);
    } else {
      const sidebar = region(["struct", "sel", "ui"], renderSidebar);
      const filterPills = region(["struct", "ui"], renderFilterPills);
      const timeline = region(["sel", "ui"], renderTimeline);
      // bottom-pane (console/problems) reads meta fields (token sub-lines,
      // warningCount, hasResponseError) so it must rebuild on meta ticks too.
      const bottom = region(["struct", "ui", "meta"], renderBottomPane);
      const detail = region(["sel", "ui", "detail"], renderDetail, reconcileDetail);
      const center = h("div.center-stack", null, filterPills, timeline, bottom);
      mainChild = h("div.view-traffic", null, sidebar, center, detail);
    }

    const statusbar = region(["struct", "meta", "ui"], renderStatusbar);

    shellEl = h(
      "div",
      { class: `app${isMac ? " app-mac" : " app-frameless"}` },
      titlebar,
      topbar,
      h("div.main", null, mainChild),
      statusbar,
    );
    applyBottomHeight();
    return shellEl;
  }

  function applyBottomHeight() {
    const state = getState();
    const hgt = state.bottomCollapsed ? 0 : state.bottomHeight;
    shellEl.style.setProperty("--bottom-h", `${hgt}px`);
  }

  function fullRender() {
    clear(root);
    root.appendChild(buildShell());
    lastView = getState().view;
  }

  function sync() {
    if (getState().view !== lastView) {
      fullRender();
      return;
    }
    applyBottomHeight();
    for (const r of regions) {
      const key = depsKey(r.deps);
      if (key === r.key) continue;
      // Don't rebuild a region whose element currently holds the user's editing
      // focus (the inline session-rename input, the sidebar filter box): a
      // replaceWith would discard the live input and clobber the caret/selection
      // mid-type. Leave r.key stale so the region rebuilds on the next event
      // after the input blurs.
      if (holdsEditingFocus(r.el)) continue;
      r.key = key;
      const next = r.build();
      // A region with a reconciler (the detail panel) updates its existing
      // element in place — keeping the scrollable, syntax-highlighted
      // `.detail-body` layer alive instead of tearing the whole subtree down.
      // That wholesale teardown/recreate is what flashed the right pane on
      // every tab switch. reconcile returns false when the structure changed
      // (empty ↔ filled) so we fall back to a full replace.
      if (r.reconcile?.(r.el, next)) continue;
      const snap = snapshotScrolls(r.el);
      r.el.replaceWith(next);
      restoreScrolls(next, snap);
      r.el = next;
    }
    // After regions settle, append any newly-streamed response bytes in place
    // (no region rebuild) so watching a stream never re-highlights the whole
    // body or drops the user's text selection. Runs last so a concurrent `sel`
    // rebuild of the detail pane (which already renders the full body) makes
    // this a no-op rather than a redundant append.
    if (versions.body !== lastBodyVer) {
      lastBodyVer = versions.body;
      patchLiveBody();
    }
  }

  // rAF coalescing: many state changes within a frame → one sync.
  let scheduled = false;
  const schedule = () => {
    if (scheduled) return;
    scheduled = true;
    requestAnimationFrame(() => {
      scheduled = false;
      sync();
    });
  };

  fullRender();
  onChange(schedule);
  onLanguageChange(fullRender);
}

// reconcileDetail updates the right-hand inspect panel in place. The detail
// region rebuilds on a selection change AND on a tab / cache-style change;
// replacing the whole `aside.detail` subtree each time tore down `.detail-body`
// (a scrollable, often syntax-highlighted compositing layer) and recreated it
// empty for a frame — the flicker the user saw when switching tabs. Instead we
// keep the panel root, replace the tiny head/tabs wholesale (no scroll area →
// no flash) and move only the freshly-built body's children into the existing
// `.detail-body`, so its element identity and layer survive. Returns false when
// the structure changed (selection cleared/loaded toggles between the empty
// state and the full panel), letting the caller fall back to a full replace.
function reconcileDetail(oldEl: HTMLElement, newEl: HTMLElement): boolean {
  const oldBody = oldEl.querySelector<HTMLElement>(".detail-body");
  const newBody = newEl.querySelector<HTMLElement>(".detail-body");
  const oldHead = oldEl.querySelector<HTMLElement>(".detail-head");
  const newHead = newEl.querySelector<HTMLElement>(".detail-head");
  const oldTabs = oldEl.querySelector<HTMLElement>(".detail-tabs");
  const newTabs = newEl.querySelector<HTMLElement>(".detail-tabs");
  if (!oldBody || !newBody || !oldHead || !newHead || !oldTabs || !newTabs) {
    return false;
  }
  oldHead.replaceWith(newHead);
  oldTabs.replaceWith(newTabs);
  // Preserve the scroll position across an in-place body refresh — e.g.
  // expanding a timeline message bumps `sel`, which also rebuilds detail.
  const top = oldBody.scrollTop;
  clear(oldBody);
  while (newBody.firstChild) oldBody.appendChild(newBody.firstChild);
  oldBody.scrollTop = top;
  return true;
}

// patchLiveBody appends the newly-streamed response bytes to the live <pre>
// holder in place (if the Response tab is showing a streaming entry), instead
// of rebuilding the detail region. Preserves text selection and avoids the
// O(n^2) full re-highlight of the growing body.
function patchLiveBody() {
  const pre = document.querySelector<HTMLElement>("pre[data-live-response]");
  if (!pre) return;
  const holder = pre.querySelector<HTMLElement>(".rh-live-body");
  if (!holder) return;
  const full = getState().selectedEntry?.responseBody ?? "";
  const rendered = Number(pre.dataset.renderedLen ?? "0");
  if (full.length <= rendered) return;
  holder.appendChild(document.createTextNode(full.slice(rendered)));
  pre.dataset.renderedLen = String(full.length);
  // Stick to the bottom of the detail scroll container if the user is near it.
  const body = pre.closest<HTMLElement>(".detail-body");
  if (body && body.scrollHeight - body.scrollTop - body.clientHeight < 60) {
    body.scrollTop = body.scrollHeight;
  }
}

// holdsEditingFocus reports whether the active element is an editable control
// inside scope — used to defer rebuilding a region the user is typing in.
function holdsEditingFocus(scope: HTMLElement): boolean {
  const active = document.activeElement;
  if (!active || active === document.body) return false;
  const tag = active.tagName;
  const editable =
    tag === "INPUT" ||
    tag === "TEXTAREA" ||
    (active as HTMLElement).isContentEditable;
  return editable && scope.contains(active);
}

interface ScrollSnap {
  top: number;
  atBottom: boolean;
}

// Snapshot scroll positions WITHIN a region's element (so a rebuilt region
// keeps its scroll). Regions that didn't rebuild are never touched, so their
// scroll/focus/selection are preserved for free.
function snapshotScrolls(scope: HTMLElement): Record<string, ScrollSnap> {
  const out: Record<string, ScrollSnap> = {};
  for (const sel of SCROLL_SELECTORS) {
    const el = scope.matches?.(sel) ? scope : scope.querySelector(sel);
    if (!el) continue;
    const atBottom =
      STICKY_BOTTOM.has(sel) &&
      el.scrollHeight - el.scrollTop - el.clientHeight <= STICKY_THRESHOLD_PX;
    out[sel] = { top: el.scrollTop, atBottom };
  }
  return out;
}

function restoreScrolls(scope: HTMLElement, scrolls: Record<string, ScrollSnap>) {
  for (const sel of SCROLL_SELECTORS) {
    const el = scope.matches?.(sel) ? scope : scope.querySelector(sel);
    const snap = scrolls[sel];
    if (!el || !snap) continue;
    if (snap.atBottom) el.scrollTop = el.scrollHeight;
    else el.scrollTop = snap.top;
  }
}
