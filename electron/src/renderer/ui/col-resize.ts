// Column resize handles for the left sidebar and right detail panel. Each
// handle is a thin strip on the panel's inner edge; dragging it commits the new
// width through setColLeft / setColRight, which bump only the `layout` version
// slice (no region depends on it) so the drag never rebuilds the panels — only
// app.ts's applyColumns() re-reads the width and writes the inline --col-*
// override. Move/up listeners live on window so an unrelated region rebuild
// re-creating the handle mid-drag can't drop the gesture (same idiom as the
// bottom-pane vertical resizer).

import { t } from "../i18n";
import { h } from "./dom";
import { scheduleLayoutSave } from "./layout-persist";
import { resetColLeft, resetColRight, setColLeft, setColRight } from "./state";

export function colResizeHandle(side: "left" | "right"): HTMLElement {
  return h("div", {
    class: `col-resize col-resize-${side}`,
    title: t("layout.resizeColumn"),
    onmousedown: (ev: MouseEvent) => startColResize(side, ev),
    // Double-click resets the column to its responsive default and persists it.
    ondblclick: () => {
      if (side === "left") resetColLeft();
      else resetColRight();
      scheduleLayoutSave();
    },
  });
}

function startColResize(side: "left" | "right", ev: MouseEvent) {
  ev.preventDefault();
  // Anchor on the rendered width, not the stored state: colLeft/colRight are
  // null until the first resize and the responsive default varies with window
  // width, so reading getBoundingClientRect avoids a first-drag jump.
  const panel = document.querySelector<HTMLElement>(side === "left" ? ".sidebar" : ".detail");
  if (!panel) return;
  const startX = ev.clientX;
  const startW = panel.getBoundingClientRect().width;
  document.body.classList.add("col-resizing");

  const move = (mv: MouseEvent) => {
    const delta = mv.clientX - startX;
    // Left grows when dragging right; right grows when dragging left.
    if (side === "left") setColLeft(startW + delta);
    else setColRight(startW - delta);
  };
  const up = () => {
    window.removeEventListener("mousemove", move);
    window.removeEventListener("mouseup", up);
    document.body.classList.remove("col-resizing");
    // Persist the final width once the gesture ends (debounced).
    scheduleLayoutSave();
  };
  window.addEventListener("mousemove", move);
  window.addEventListener("mouseup", up);
}
