// Shared conversation-rendering primitives used by the timeline for all three
// providers (Anthropic Messages, OpenAI Chat Completions, OpenAI Responses).
//
// The timeline groups a request/response into "rail rows": each row is a
// timestamped node on a vertical rail plus a card. The card body is built from
// provider-neutral "parts" (text / thinking / tool-call / tool-result) so the
// same renderers paint Anthropic blocks, OpenAI messages, and Responses items
// with one visual language — no three copies of the block code.

import type { components } from "../../api/client";
import { t } from "../i18n";
import { h } from "./dom";
import { highlight } from "./highlight";

type OpenAIAnalysis = components["schemas"]["OpenAIAnalysis"];
type OpenAIMessage = components["schemas"]["OpenAIMessage"];
type OpenAIToolCall = components["schemas"]["OpenAIToolCall"];
type OpenAIUsage = components["schemas"]["OpenAIUsage"];
type ResponsesAnalysis = components["schemas"]["ResponsesAnalysis"];
type ResponsesItem = components["schemas"]["ResponsesItem"];
type ResponsesUsage = components["schemas"]["ResponsesUsage"];

// A normalized content part. Providers map their native shapes onto this small
// union before rendering so the card body code is provider-agnostic.
export type ConvoPart =
  | { kind: "text"; text: string }
  | { kind: "thinking"; label: string; text: string }
  | {
      kind: "tool-call";
      name: string;
      id?: string;
      input: unknown;
      // Selecting a tool call pivots the right pane to its result.
      onSelect?: () => void;
      selected?: boolean;
      resultError?: boolean;
    }
  | { kind: "tool-result"; id?: string; isError?: boolean; content: unknown }
  | { kind: "image"; meta: string; raw?: unknown }
  | { kind: "raw"; label: string; value: unknown };

// Provider-neutral usage figures driving the per-card footer (cache-read /
// new-input / output pills + cache-ratio bar). Built by each provider mapper.
export interface ConvoUsage {
  cacheRead: number;
  // Uncached new input tokens (input minus cache read).
  input: number;
  output: number;
}

// A provider-neutral conversation row. The OpenAI / Responses mappers return a
// list of these; the timeline wraps each in a rail row (timestamp + node +
// card). Anthropic keeps its own richer card path (clickable tool_use → result
// pivot) but shares the same visual language via renderPart.
export interface ConvoRow {
  // Stable per-entry key used for expand/collapse + selection (e.g. "oai-0").
  key: string;
  role: string;
  // Optional sub-label next to the role chip (e.g. "stop: tool_calls").
  sub?: string | null;
  parts: ConvoPart[];
  // Assistant-row usage → footer pills; null on request rows.
  usage?: ConvoUsage | null;
  // Entry-level cost estimate (USD) shown in the footer; null when unknown.
  cost?: number | null;
}

export function rolePalette(role: string): string {
  const r = role.toLowerCase();
  if (r === "assistant" || r === "model") return "assistant";
  if (r === "system" || r === "developer") return "system";
  if (r === "tool" || r === "tools" || r === "function") return "tools";
  return "user";
}

export function roleChip(role: string): HTMLElement {
  const palette = rolePalette(role);
  return h(`span.role-chip.role-${palette}`, null, role);
}

export function renderPart(part: ConvoPart): HTMLElement {
  switch (part.kind) {
    case "text":
      return h("div.blk.text", null, part.text);
    case "thinking":
      return h(
        "div.blk.thinking",
        null,
        h("div.blk-tag", null, part.label),
        h("div", null, part.text),
      );
    case "tool-call":
      return renderToolCall(part);
    case "tool-result": {
      const isErr = part.isError === true;
      return h(
        `div.blk.tool-result${isErr ? ".is-error" : ""}`,
        null,
        h(
          "div.blk-tag",
          null,
          h("span.blk-kind", null, "tool_result"),
          part.id ? h("span.tool-id", null, part.id) : null,
          isErr ? h("span.tool-err", null, "ERROR") : null,
        ),
        renderResultBody(part.content),
      );
    }
    case "image":
      return h(
        "div.blk.image",
        null,
        h("div.blk-tag", null, "image"),
        h("div.blk-img-meta", null, part.meta),
        part.raw !== undefined ? rawJsonBox(part.raw) : null,
      );
    case "raw":
      return h(
        "div.blk.unknown",
        null,
        h("div.blk-tag", null, part.label),
        rawJsonBox(part.value),
      );
  }
}

function renderToolCall(part: Extract<ConvoPart, { kind: "tool-call" }>): HTMLElement {
  return h(
    `div.blk.tool-use${part.selected ? ".selected" : ""}`,
    part.onSelect
      ? {
          onclick: (e: Event) => {
            e.stopPropagation();
            part.onSelect?.();
          },
        }
      : null,
    h(
      "div.blk-tag",
      null,
      h("span.blk-icon", null, "▶"),
      h("span.blk-kind", null, "tool_use"),
      h("span.tool-name", null, part.name || "?"),
      part.id ? h("span.tool-id", null, part.id) : null,
      part.resultError !== undefined
        ? h(`span.tl-stat.${part.resultError ? "err" : "ok"}`, null, part.resultError ? "error" : "ok")
        : null,
    ),
    h("div.blk-args", null, formatArgs(part.input)),
  );
}

function renderResultBody(content: unknown): HTMLElement {
  if (typeof content === "string") {
    return h("div.blk-result-text", null, content);
  }
  if (Array.isArray(content)) {
    const wrap = h("div.blk-result-blocks");
    for (const c of content as Array<{ type?: string; text?: string }>) {
      if (c.type === "text") wrap.appendChild(h("div.blk-result-text", null, c.text ?? ""));
      else wrap.appendChild(rawJsonBox(c));
    }
    return wrap;
  }
  if (content === null || content === undefined) {
    return h("div.blk-empty", null, t("detail.toolResultEmpty"));
  }
  return rawJsonBox(content);
}

// formatArgs renders tool arguments. Plain strings (OpenAI tool_calls carry
// arguments as a JSON string) are pretty-printed if they parse as JSON.
export function formatArgs(input: unknown): string {
  if (input === null || input === undefined) return "{}";
  if (typeof input === "string") {
    const trimmed = input.trim();
    if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
      try {
        return JSON.stringify(JSON.parse(trimmed), null, 2);
      } catch {
        return input;
      }
    }
    return input;
  }
  try {
    return JSON.stringify(input, null, 2);
  } catch {
    return String(input);
  }
}

export function rawJsonBox(value: unknown): HTMLElement {
  let text: string;
  if (typeof value === "string") text = value;
  else {
    try {
      text = JSON.stringify(value, null, 2);
    } catch {
      text = String(value);
    }
  }
  const pre = document.createElement("pre");
  pre.className = "codebox kind-json blk-raw";
  pre.appendChild(highlight(text, "json"));
  return pre;
}

// ---- OpenAI Chat Completions → ConvoRow[] ------------------------------------

export function mapOpenAI(a: OpenAIAnalysis): ConvoRow[] {
  const rows: ConvoRow[] = [];
  const req = a.request;
  (req?.messages ?? []).forEach((msg, i) => {
    rows.push({
      key: `oai-req-${i}`,
      role: msg.role || "user",
      parts: openAIMessageParts(msg),
    });
  });
  const resp = a.response;
  if (resp) {
    if (resp.error) {
      rows.push({
        key: "oai-err",
        role: "assistant",
        sub: resp.error.type ?? "error",
        parts: [{ kind: "raw", label: resp.error.type ?? "error", value: resp.error }],
      });
    }
    (resp.choices ?? []).forEach((ch, i) => {
      const msg = ch.message;
      const parts = msg ? openAIMessageParts(msg) : [];
      if (ch.text !== undefined && ch.text !== "") {
        // legacy /v1/completions text completion
        parts.push({ kind: "text", text: ch.text });
      }
      rows.push({
        key: `oai-resp-${i}`,
        role: msg?.role || "assistant",
        sub: ch.finishReason ? `stop: ${ch.finishReason}` : resp.streamed ? t("conversation.stream") : null,
        parts,
        usage: i === 0 ? openAIUsage(resp.usage) : null,
      });
    });
  }
  return rows;
}

function openAIMessageParts(msg: OpenAIMessage): ConvoPart[] {
  const parts: ConvoPart[] = [];
  if (msg.content) parts.push({ kind: "text", text: msg.content });
  // tool role message → render as a tool result keyed by tool_call_id
  if ((msg.role === "tool" || msg.role === "function") && msg.content !== undefined) {
    // already pushed content above as text; replace with a result block for clarity
    parts.length = 0;
    parts.push({ kind: "tool-result", id: msg.toolCallId, content: msg.content });
  }
  for (const tc of (msg.toolCalls ?? []) as OpenAIToolCall[]) {
    parts.push({
      kind: "tool-call",
      name: tc.name || "?",
      id: tc.id,
      input: tc.arguments ?? "",
    });
  }
  // legacy function_call object
  if (msg.functionCall?.name) {
    parts.push({
      kind: "tool-call",
      name: msg.functionCall.name,
      input: msg.functionCall.arguments ?? "",
    });
  }
  return parts;
}

function openAIUsage(u: OpenAIUsage | undefined): ConvoUsage | null {
  if (!u) return null;
  const cacheRead = u.cachedTokens ?? 0;
  const prompt = u.promptTokens ?? 0;
  return {
    cacheRead,
    input: Math.max(0, prompt - cacheRead),
    output: u.completionTokens ?? 0,
  };
}

// ---- OpenAI Responses (Codex) → ConvoRow[] ----------------------------------

export function mapResponses(a: ResponsesAnalysis): ConvoRow[] {
  const rows: ConvoRow[] = [];
  const req = a.request;
  if (req?.instructions) {
    rows.push({
      key: "resp-instr",
      role: "system",
      parts: [{ kind: "text", text: req.instructions }],
    });
  }
  (req?.input ?? []).forEach((item, i) => {
    rows.push({
      key: `resp-in-${i}`,
      role: item.role || itemRole(item),
      parts: responsesItemParts(item),
    });
  });
  const resp = a.response;
  if (resp) {
    if (resp.error) {
      rows.push({
        key: "resp-err",
        role: "assistant",
        sub: resp.error.type ?? "error",
        parts: [{ kind: "raw", label: resp.error.type ?? "error", value: resp.error }],
      });
    }
    const out = resp.output ?? [];
    out.forEach((item, i) => {
      rows.push({
        key: `resp-out-${i}`,
        role: item.role || itemRole(item),
        sub:
          i === out.length - 1
            ? resp.incomplete?.reason
              ? `incomplete: ${resp.incomplete.reason}`
              : resp.status
                ? `status: ${resp.status}`
                : resp.streamed
                  ? t("conversation.stream")
                  : null
            : null,
        parts: responsesItemParts(item),
        usage: i === out.length - 1 ? responsesUsage(resp.usage) : null,
      });
    });
  }
  return rows;
}

// itemRole infers a role chip for a Responses item that doesn't carry one
// (function_call / reasoning items are assistant-side; outputs default to
// assistant, tool outputs to tool).
function itemRole(item: ResponsesItem): string {
  const ty = item.type ?? "";
  if (ty === "function_call" || ty === "reasoning" || ty.startsWith("output")) return "assistant";
  if (ty === "function_call_output" || ty === "tool_call_output") return "tool";
  return "user";
}

function responsesItemParts(item: ResponsesItem): ConvoPart[] {
  const parts: ConvoPart[] = [];
  const ty = item.type ?? "";

  // reasoning summary → thinking style
  if (ty === "reasoning") {
    if (item.summary) parts.push({ kind: "thinking", label: "reasoning", text: item.summary });
    return parts.length > 0 ? parts : [{ kind: "thinking", label: "reasoning", text: "" }];
  }

  // function/tool call
  if (ty === "function_call" || ty === "tool_call") {
    parts.push({
      kind: "tool-call",
      name: item.name || "?",
      id: item.callId || item.id,
      input: item.arguments ?? "",
    });
    return parts;
  }

  // function/tool call output
  if (ty === "function_call_output" || ty === "tool_call_output") {
    parts.push({ kind: "tool-result", id: item.callId, content: item.output ?? "" });
    return parts;
  }

  // message / output_text items carry either `parts` or `content`
  if (item.parts && item.parts.length > 0) {
    for (const p of item.parts) {
      if (p.type === "input_image" || p.image) {
        const meta = p.image?.url ? `image: ${truncMeta(p.image.url)}` : "image";
        parts.push({ kind: "image", meta, raw: p.raw ?? p });
      } else if (p.text !== undefined) {
        parts.push({ kind: "text", text: p.text });
      } else {
        parts.push({ kind: "raw", label: p.type ?? "part", value: p.raw ?? p });
      }
    }
  } else if (item.content !== undefined && item.content !== "") {
    parts.push({ kind: "text", text: item.content });
  } else if (item.summary) {
    parts.push({ kind: "text", text: item.summary });
  }

  if (parts.length === 0 && item.raw !== undefined) {
    parts.push({ kind: "raw", label: ty || "item", value: item.raw });
  }
  return parts;
}

function truncMeta(s: string): string {
  return s.length > 64 ? `${s.slice(0, 60)}…` : s;
}

function responsesUsage(u: ResponsesUsage | undefined): ConvoUsage | null {
  if (!u) return null;
  const cacheRead = u.cachedTokens ?? 0;
  const input = u.inputTokens ?? 0;
  return {
    cacheRead,
    input: Math.max(0, input - cacheRead),
    output: (u.outputTokens ?? 0) + (u.reasoningTokens ?? 0),
  };
}
