// Tools tab — lists every tool_use in the conversation along with its
// matching tool_result, grouped per turn. Click a card to focus that
// tool_use on the timeline (and the right pane's existing Tool tab).

import type { components } from "../../api/client";
import { t } from "../i18n";
import { h } from "./dom";
import { highlight } from "./highlight";
import { selectToolUse, type TrafficEntry } from "./state";

type AnthropicBlock = components["schemas"]["AnthropicBlock"];

interface ToolCall {
  use: AnthropicBlock;
  result: AnthropicBlock | null;
  /** Which message this tool_use lives on — used to drive selection. */
  messageKey: string;
}

export function renderTools(entry: TrafficEntry): HTMLElement {
  const calls = collectToolCalls(entry);
  if (calls.length === 0) {
    return h("div.detail-empty", null, t("detail.toolsEmpty"));
  }
  const list = h("div.tools-list");
  for (const c of calls) list.appendChild(renderCard(c));
  return list;
}

function collectToolCalls(entry: TrafficEntry): ToolCall[] {
  const out: ToolCall[] = [];
  const a = (entry.analysis as { anthropic?: components["schemas"]["AnthropicAnalysis"] })
    ?.anthropic;
  if (!a) return out;

  // Map every tool_result by id so we can pair them with a tool_use.
  const results = new Map<string, AnthropicBlock>();
  (a.request?.messages ?? []).forEach((m) => {
    for (const blk of m.content ?? []) {
      if (blk.type === "tool_result" && blk.toolUseId) {
        results.set(blk.toolUseId, blk);
      }
    }
  });

  // tool_use blocks in request.messages (assistant role)
  (a.request?.messages ?? []).forEach((m, i) => {
    for (const blk of m.content ?? []) {
      if (blk.type === "tool_use" && blk.id) {
        out.push({
          use: blk,
          result: results.get(blk.id) ?? null,
          messageKey: `req-${i}`,
        });
      }
    }
  });

  // tool_use blocks in response.content
  for (const blk of a.response?.content ?? []) {
    if (blk.type === "tool_use" && blk.id) {
      out.push({
        use: blk,
        result: results.get(blk.id) ?? null,
        messageKey: "resp",
      });
    }
  }
  return out;
}

function renderCard(c: ToolCall): HTMLElement {
  const use = c.use;
  const result = c.result;
  const isErr = result?.isError === true;

  return h(
    "div.tools-card",
    { onclick: () => use.id && selectToolUse(c.messageKey, use.id) },
    h(
      "div.th",
      null,
      h("span.tname", null, use.name ?? "?"),
      use.id ? h("span.tool-id", null, use.id) : null,
      result
        ? h(
            `span.tstat.${isErr ? "fail" : "ok"}`,
            null,
            isErr ? t("detail.toolStatusError") : t("detail.toolStatusOk"),
          )
        : h("span.tstat.pending", null, t("detail.toolStatusPending")),
    ),
    h("div.l", null, t("detail.toolArgs")),
    jsonBox(use.input ?? {}),
    result
      ? h(
          "div",
          null,
          h("div.l", null, t("detail.toolResult")),
          renderToolResult(result.content),
        )
      : null,
  );
}

function renderToolResult(content: unknown): HTMLElement {
  if (typeof content === "string") {
    return h("pre.tres", null, content);
  }
  if (Array.isArray(content)) {
    const wrap = h("div");
    for (const c of content as AnthropicBlock[]) {
      if (c.type === "text") wrap.appendChild(h("pre.tres", null, c.text ?? ""));
      else wrap.appendChild(jsonBox(c));
    }
    return wrap;
  }
  if (content === null || content === undefined) {
    return h("div.blk-empty", null, t("detail.toolResultEmpty"));
  }
  return jsonBox(content);
}

function jsonBox(value: unknown): HTMLElement {
  let text: string;
  try {
    text = typeof value === "string" ? value : JSON.stringify(value, null, 2);
  } catch {
    text = String(value);
  }
  const pre = document.createElement("pre");
  pre.className = "targs";
  pre.appendChild(highlight(text, "json"));
  return pre;
}
