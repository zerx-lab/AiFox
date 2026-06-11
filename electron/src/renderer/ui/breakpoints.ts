// Breakpoints tab — list configured rules + paused requests, with the
// affordances to add / enable / disable / delete / continue / abort.

import type { components } from "../../api/client";
import { t } from "../i18n";
import {
  abortPaused,
  addBreakpoint,
  continuePaused,
  deleteBreakpoint,
  updateBreakpoint,
} from "./api-service";
import { h } from "./dom";
import { fmtClock } from "./format";
import { getState, setState } from "./state";

type BreakpointWire = components["schemas"]["Breakpoint"];

interface DraftForm {
  match: "endpoint" | "path";
  pattern: string;
  enabled: boolean;
}

// Module-scoped draft so the input box doesn't reset every full re-render.
const draft: DraftForm = { match: "endpoint", pattern: "", enabled: true };

// entryIds with an in-flight continue/abort. Used to disable BOTH buttons on a
// paused row while the request is being resolved so a double-click can't fire
// two continue/abort calls (§4.1.1). Survives re-renders because it's
// module-scoped; entries clear when the row leaves the paused list (the SSE
// breakpoints event drops it) or the call settles.
const resolving = new Set<string>();

export function renderBreakpoints(): HTMLElement {
  const state = getState();
  const bps = state.breakpoints;
  const paused = state.pausedRequests;

  // Drop pending flags for rows that are no longer paused (resolved upstream).
  if (resolving.size > 0) {
    const live = new Set(paused.map((p) => p.entryId));
    for (const id of resolving) if (!live.has(id)) resolving.delete(id);
  }

  const wrap = h("div.bp-wrap");

  // ---- Paused section (top, only when something is held) ----
  if (paused.length > 0) {
    const pSec = h("div.bp-section.bp-paused");
    pSec.appendChild(h("h4", null, t("bp.pausedTitle", { count: String(paused.length) })));
    for (const p of paused) {
      pSec.appendChild(renderPausedRow(p));
    }
    wrap.appendChild(pSec);
  }

  // ---- New breakpoint form ----
  const form = h("div.bp-form");
  const matchSelect = document.createElement("select");
  for (const opt of [
    { v: "endpoint", l: t("bp.matchEndpoint") },
    { v: "path", l: t("bp.matchPath") },
  ]) {
    const o = document.createElement("option");
    o.value = opt.v;
    o.textContent = opt.l;
    if (draft.match === opt.v) o.selected = true;
    matchSelect.appendChild(o);
  }
  matchSelect.addEventListener("change", () => {
    draft.match = matchSelect.value as "endpoint" | "path";
  });

  const submit = async () => {
    if (!draft.pattern.trim()) return;
    const res = await addBreakpoint({
      match: draft.match,
      pattern: draft.pattern.trim(),
      enabled: draft.enabled,
    });
    if (!res.ok) return; // service toasted the failure
    draft.pattern = "";
    // Trigger re-render; the SSE 'breakpoints' event also updates state,
    // but the click handler's caller doesn't re-render, so nudge the
    // 'struct' slice (the breakpoints list lives there).
    setState({ breakpoints: getState().breakpoints });
  };

  const patternInput = document.createElement("input");
  patternInput.type = "text";
  patternInput.placeholder = t("bp.patternPlaceholder");
  patternInput.value = draft.pattern;
  patternInput.addEventListener("input", () => {
    draft.pattern = patternInput.value;
  });
  // Enter in the pattern field submits the new breakpoint (§4.1.6).
  patternInput.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      void submit();
    }
  });

  const addBtn = h(
    "button.btn",
    {
      onclick: () => {
        void submit();
      },
    },
    t("bp.add"),
  );

  form.appendChild(h("label", null, t("bp.match")));
  form.appendChild(matchSelect);
  form.appendChild(h("label", null, t("bp.pattern")));
  form.appendChild(patternInput);
  form.appendChild(addBtn);
  wrap.appendChild(form);

  // ---- Existing breakpoints list ----
  if (bps.length === 0) {
    wrap.appendChild(h("div.detail-empty", null, t("bp.empty")));
  } else {
    const list = h("div.bp-list");
    for (const bp of bps) list.appendChild(renderBpRow(bp));
    wrap.appendChild(list);
  }

  return wrap;
}

function renderBpRow(bp: BreakpointWire): HTMLElement {
  return h(
    "div.bp-row",
    null,
    h(
      "button.bp-toggle",
      {
        class: `bp-toggle${bp.enabled ? " on" : ""}`,
        title: bp.enabled ? t("bp.disable") : t("bp.enable"),
        onclick: async () => {
          await updateBreakpoint(bp.id, !bp.enabled);
        },
      },
      bp.enabled ? "●" : "○",
    ),
    h("span.bp-id", null, bp.id),
    h("span.bp-match", null, bp.match),
    h("span.bp-pattern", null, bp.pattern),
    h(
      "button.bp-del",
      {
        title: t("bp.delete"),
        onclick: async () => {
          await deleteBreakpoint(bp.id);
        },
      },
      "✕",
    ),
  );
}

function renderPausedRow(p: components["schemas"]["Paused"]): HTMLElement {
  const pending = resolving.has(p.entryId);

  // resolve runs a continue/abort through the service with a pending guard:
  // marks the entry resolving, disables both buttons (visually + functionally),
  // then re-renders. The paused row vanishes via the SSE breakpoints event when
  // the backend releases the request; on failure the service toasts and we drop
  // the pending flag so the user can retry.
  const resolve = (
    action: (id: string) => Promise<boolean>,
  ) => async () => {
    if (resolving.has(p.entryId)) return;
    resolving.add(p.entryId);
    setState({ breakpoints: getState().breakpoints }); // bump struct → re-render
    const ok = await action(p.entryId);
    if (!ok) {
      resolving.delete(p.entryId);
      setState({ breakpoints: getState().breakpoints });
    }
    // On success leave the flag set; the breakpoints SSE event removes the row.
  };

  return h(
    "div.bp-paused-row",
    null,
    h(
      "span.bp-paused-mark",
      null,
      "⏸",
    ),
    h(
      "span.bp-paused-where",
      { onclick: () => setState({ selectedId: p.entryId }) },
      `${p.entryId} · ${p.method} ${p.url}`,
    ),
    h("span.bp-paused-time", null, fmtClock(p.pausedAt)),
    h(
      "button.btn",
      {
        disabled: pending,
        "aria-busy": pending ? "true" : "false",
        onclick: resolve(continuePaused),
      },
      pending ? t("bp.resolving") : t("bp.continue"),
    ),
    h(
      "button.btn.secondary",
      {
        disabled: pending,
        "aria-busy": pending ? "true" : "false",
        onclick: resolve(abortPaused),
      },
      t("bp.abort"),
    ),
  );
}
