// Breakpoints tab — list configured rules + paused requests, with the
// affordances to add / enable / disable / delete / continue / abort.

import type { components } from "../../api/client";
import { getClient } from "../../api/client";
import { t } from "../i18n";
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

export function renderBreakpoints(): HTMLElement {
  const state = getState();
  const bps = state.breakpoints;
  const paused = state.pausedRequests;

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

  const patternInput = document.createElement("input");
  patternInput.type = "text";
  patternInput.placeholder = t("bp.patternPlaceholder");
  patternInput.value = draft.pattern;
  patternInput.addEventListener("input", () => {
    draft.pattern = patternInput.value;
  });

  const addBtn = h(
    "button.btn",
    {
      onclick: async () => {
        if (!draft.pattern.trim()) return;
        const client = await getClient();
        await client.POST("/v1/breakpoints", {
          body: {
            match: draft.match,
            pattern: draft.pattern.trim(),
            enabled: draft.enabled,
          },
        });
        draft.pattern = "";
        // Trigger re-render; the SSE 'breakpoints' event also updates state,
        // but the click handler's caller doesn't re-render, so nudge here.
        setState({});
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
          const client = await getClient();
          await client.PUT("/v1/breakpoints/{id}", {
            params: { path: { id: bp.id } },
            body: { enabled: !bp.enabled },
          });
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
          const client = await getClient();
          await client.DELETE("/v1/breakpoints/{id}", {
            params: { path: { id: bp.id } },
          });
        },
      },
      "✕",
    ),
  );
}

function renderPausedRow(p: components["schemas"]["Paused"]): HTMLElement {
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
        onclick: async () => {
          const client = await getClient();
          await client.POST("/v1/breakpoints/paused/{entryId}/continue", {
            params: { path: { entryId: p.entryId } },
          });
        },
      },
      t("bp.continue"),
    ),
    h(
      "button.btn.secondary",
      {
        onclick: async () => {
          const client = await getClient();
          await client.POST("/v1/breakpoints/paused/{entryId}/abort", {
            params: { path: { entryId: p.entryId } },
          });
        },
      },
      t("bp.abort"),
    ),
  );
}
