// Variables tab — request parameters of the currently-selected entry,
// shown as a name / type / value table. Like Chrome DevTools' Scope panel
// but specific to LLM call parameters.

import type { components } from "../../api/client";
import { t } from "../i18n";
import { h } from "./dom";
import { getState } from "./state";

type Analysis = components["schemas"]["Analysis"];

interface Row {
  name: string;
  type: string;
  value: string;
}

export function renderVariables(): HTMLElement {
  const state = getState();
  const entry = state.entries.find((e) => e.id === state.selectedId);
  if (!entry) {
    return h("div.detail-empty", null, t("bottom.varsEmpty"));
  }
  const analysis = entry.analysis as Analysis | undefined;
  const rows = collectRows(analysis);
  if (rows.length === 0) {
    return h("div.detail-empty", null, t("bottom.varsNoParsed"));
  }

  const head = h(
    "div.var-row.var-head",
    null,
    h("span.vname", null, t("bottom.varsName")),
    h("span.vtype", null, t("bottom.varsType")),
    h("span.vval", null, t("bottom.varsValue")),
  );

  const wrap = h("div.vars");
  wrap.appendChild(head);
  for (const r of rows) {
    wrap.appendChild(
      h(
        "div.var-row",
        null,
        h("span.vname", null, r.name),
        h("span.vtype", null, r.type),
        h("span.vval", { title: r.value }, r.value),
      ),
    );
  }
  return wrap;
}

function collectRows(analysis: Analysis | undefined): Row[] {
  const out: Row[] = [];
  const req = analysis?.anthropic?.request;
  if (!req) return out;

  if (req.model) out.push({ name: "model", type: "string", value: `"${req.model}"` });
  if (req.maxTokens) out.push({ name: "max_tokens", type: "number", value: String(req.maxTokens) });
  if (req.temperature !== undefined)
    out.push({ name: "temperature", type: "number", value: String(req.temperature) });
  if (req.topP !== undefined) out.push({ name: "top_p", type: "number", value: String(req.topP) });
  if (req.topK !== undefined) out.push({ name: "top_k", type: "number", value: String(req.topK) });
  if (req.stream !== undefined)
    out.push({ name: "stream", type: "boolean", value: String(req.stream) });
  if (req.stopSequences && req.stopSequences.length > 0) {
    out.push({
      name: "stop_sequences",
      type: `array(${req.stopSequences.length})`,
      value: `[${req.stopSequences.map((s) => JSON.stringify(s)).join(", ")}]`,
    });
  }
  const systemLen = (req.system ?? []).reduce((acc, b) => acc + (b.text?.length ?? 0), 0);
  if (systemLen > 0)
    out.push({ name: "system", type: `text(${systemLen} chars)`, value: previewSystem(req.system) });
  if (req.tools && req.tools.length > 0) {
    out.push({
      name: "tools",
      type: `array(${req.tools.length})`,
      value: `[${req.tools.map((tl) => tl.name ?? "?").join(", ")}]`,
    });
  }
  if (req.toolChoice !== undefined)
    out.push({ name: "tool_choice", type: typeOf(req.toolChoice), value: stringify(req.toolChoice) });
  if (req.metadata !== undefined && req.metadata !== null)
    out.push({ name: "metadata", type: typeOf(req.metadata), value: stringify(req.metadata) });
  if (req.unknownFields && Object.keys(req.unknownFields).length > 0) {
    for (const [k, v] of Object.entries(req.unknownFields)) {
      out.push({ name: k, type: typeOf(v), value: stringify(v) });
    }
  }

  // Response-side runtime knobs
  const resp = analysis?.anthropic?.response;
  if (resp?.stopReason)
    out.push({ name: "stop_reason", type: "string", value: `"${resp.stopReason}"` });
  if (resp?.id) out.push({ name: "response.id", type: "string", value: `"${resp.id}"` });
  return out;
}

function previewSystem(
  system: components["schemas"]["AnthropicBlock"][] | null | undefined,
): string {
  if (!system || system.length === 0) return "";
  const first = system[0]?.text ?? "";
  const trimmed = first.length > 64 ? `${first.slice(0, 64)}…` : first;
  return `"${trimmed}"`;
}

function typeOf(v: unknown): string {
  if (v === null) return "null";
  if (Array.isArray(v)) return `array(${v.length})`;
  return typeof v;
}

function stringify(v: unknown): string {
  if (v === null || v === undefined) return "—";
  if (typeof v === "string") return `"${v}"`;
  try {
    return JSON.stringify(v);
  } catch {
    return String(v);
  }
}
