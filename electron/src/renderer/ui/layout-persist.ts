// Layout persistence (§4.1.2). Debounced save of the user's dragged panel
// geometry (sidebar/detail widths + bottom pane height) through the dedicated
// /v1/settings/layout endpoint, plus the initial apply on startup.
//
// The save is debounced (500ms) and fired on a drag's mouseup (not per
// mousemove) so a resize gesture results in a single PUT. The endpoint
// read-modify-writes only the layout fields server-side, so this never races
// with an in-progress settings-form edit.

import { putLayout } from "./api-service";
import { getState, type Settings, setBottomHeight, setColLeft, setColRight } from "./state";

const DEBOUNCE_MS = 500;

let timer: number | null = null;

// scheduleLayoutSave snapshots the current geometry from state and PUTs it after
// the debounce window. A null column width (never resized) persists as 0, which
// the backend treats as "not set" so the responsive default is restored.
export function scheduleLayoutSave(): void {
  if (timer !== null) window.clearTimeout(timer);
  timer = window.setTimeout(() => {
    timer = null;
    const s = getState();
    void putLayout({
      colLeft: s.colLeft ?? 0,
      colRight: s.colRight ?? 0,
      bottomHeight: s.bottomHeight,
    });
  }, DEBOUNCE_MS);
}

// applyPersistedLayout seeds the renderer state from the persisted settings on
// startup. Zero on a field means "not set" → leave the responsive default
// (colLeft/colRight stay null; bottomHeight keeps its initial value).
export function applyPersistedLayout(settings: Settings | null | undefined): void {
  const layout = settings?.layout;
  if (!layout) return;
  if (layout.colLeft && layout.colLeft > 0) setColLeft(layout.colLeft);
  if (layout.colRight && layout.colRight > 0) setColRight(layout.colRight);
  if (layout.bottomHeight && layout.bottomHeight > 0) setBottomHeight(layout.bottomHeight);
}
