// Center pane: structured timeline of the selected traffic entry.
//
// For Anthropic Messages requests we render system / tools / messages /
// response as a chronological set of cards with role chips and clickable
// tool_use blocks. For everything else we fall back to a generic "no
// structured view" hint so the user can still inspect raw bodies on the
// right.

import type { components } from "../../api/client";
import { t } from "../i18n";
import { h } from "./dom";
import { fmtDuration } from "./format";
import { highlight } from "./highlight";
import {
  getState,
  selectMessage,
  selectToolUse,
  type TrafficEntry,
} from "./state";

type Analysis = components["schemas"]["Analysis"];
type AnthropicAnalysis = components["schemas"]["AnthropicAnalysis"];
type AnthropicRequest = components["schemas"]["AnthropicRequest"];
type AnthropicResponse = components["schemas"]["AnthropicResponse"];
type AnthropicBlock = components["schemas"]["AnthropicBlock"];
type AnthropicTool = components["schemas"]["AnthropicTool"];
type AnthropicUsage = components["schemas"]["AnthropicUsage"];

export function renderTimeline(): HTMLElement {
  const state = getState();
  const entry = state.entries.find((e) => e.id === state.selectedId) ?? null;

  if (!entry) {
    return h(
      "div.timeline",
      null,
      h(
        "div.tl-empty",
        null,
        h("div.tl-empty-mark"),
        h("div.tl-empty-title", null, t("timeline.emptyTitle")),
        h("div.tl-empty-hint", null, t("timeline.emptyHint")),
      ),
    );
  }

  const root = h("div.timeline");
  root.appendChild(renderHeader(entry));

  const analysis = entry.analysis as Analysis | undefined;
  if (analysis?.anthropic) {
    root.appendChild(renderAnthropic(analysis.anthropic, analysis.warnings ?? []));
  } else {
    root.appendChild(renderGeneric(entry, analysis));
  }
  return root;
}

function renderHeader(entry: TrafficEntry): HTMLElement {
  const analysis = entry.analysis as Analysis | undefined;
  const anth = analysis?.anthropic;
  const req = anth?.request;
  const resp = anth?.response;
  const usage = resp?.usage;

  const model = req?.model || resp?.model || "—";

  const chips: HTMLElement[] = [];
  chips.push(chip(req?.stream ? t("conversation.stream") : t("conversation.nonStream")));
  if (entry.statusCode > 0) {
    const ok = entry.statusCode < 400;
    chips.push(chip(`${entry.statusCode}`, ok ? "ok" : "err"));
  }
  if (entry.durationMillis > 0) {
    chips.push(chip(fmtDuration(entry.durationMillis)));
  }
  if (req?.maxTokens) chips.push(chip(`max ${req.maxTokens}`));
  if (req?.temperature !== undefined) chips.push(chip(`T ${req.temperature}`));
  if (req?.topP !== undefined) chips.push(chip(`top_p ${req.topP}`));
  if (req?.topK !== undefined) chips.push(chip(`top_k ${req.topK}`));
  if (usage) {
    const totals = totalUsage(usage);
    if (totals.input > 0) chips.push(chip(`${fmtTok(totals.input)} in`));
    if (totals.output > 0) chips.push(chip(`${fmtTok(totals.output)} out`, "tool"));
    if (totals.cacheRead > 0)
      chips.push(chip(`cache ${fmtTok(totals.cacheRead)}`, "ok"));
    if (totals.cacheCreate > 0)
      chips.push(chip(`cache+ ${fmtTok(totals.cacheCreate)}`, "warn"));
  }

  return h(
    "div.tl-header",
    null,
    h(
      "div.tl-header-top",
      null,
      h("span.tl-model", null, model),
      analysis?.endpoint
        ? h("span.tl-endpoint", null, analysis.endpoint)
        : h(
            "span.tl-endpoint",
            null,
            `${entry.method} ${entry.url || ""}`,
          ),
    ),
    h("div.tl-chips", null, ...chips),
  );
}

function renderGeneric(entry: TrafficEntry, _analysis?: Analysis): HTMLElement {
  return h(
    "div.tl-generic",
    null,
    h(
      "div.banner.info",
      null,
      t("timeline.noStructuredView"),
    ),
    h("div.tl-generic-row", null, h("dt", null, "Method"), h("dd", null, entry.method)),
    h(
      "div.tl-generic-row",
      null,
      h("dt", null, "URL"),
      h("dd", null, entry.url || "—"),
    ),
    h(
      "div.tl-generic-row",
      null,
      h("dt", null, "Status"),
      h("dd", null, entry.statusCode > 0 ? String(entry.statusCode) : "—"),
    ),
  );
}

function renderAnthropic(a: AnthropicAnalysis, warnings: string[]): HTMLElement {
  const body = h("div.tl-body");

  if (warnings.length > 0) {
    body.appendChild(
      h(
        "div.banner.warn",
        null,
        h("strong", null, t("conversation.warningsTitle")),
        h("ul.warnings", null, ...warnings.map((w) => h("li", null, w))),
      ),
    );
  }

  // Build a tool_use_id → tool_result map looking through request.messages so
  // when the user clicks a tool_use in the assistant's response we can
  // surface the matching result on the right.
  const toolResults = collectToolResults(a.request);

  if (a.request) renderRequestTimeline(body, a.request, toolResults);
  if (a.response) renderResponseTimeline(body, a.response, toolResults);
  return body;
}

function renderRequestTimeline(
  parent: HTMLElement,
  req: AnthropicRequest,
  toolResults: Map<string, AnthropicBlock>,
) {
  if (req.system && req.system.length > 0) {
    parent.appendChild(messageCard("sys", "system", null, req.system, toolResults));
  }
  if (req.tools && req.tools.length > 0) {
    parent.appendChild(toolsCard(req.tools));
  }
  (req.messages ?? []).forEach((msg, i) => {
    parent.appendChild(
      messageCard(`req-${i}`, msg.role || "user", null, msg.content ?? [], toolResults),
    );
  });
}

function renderResponseTimeline(
  parent: HTMLElement,
  resp: AnthropicResponse,
  toolResults: Map<string, AnthropicBlock>,
) {
  if (resp.error) {
    parent.appendChild(
      h(
        "div.banner.err",
        null,
        h("strong", null, resp.error.type ?? "error"),
        " ",
        resp.error.message ?? "",
      ),
    );
  }
  const role = resp.role || "assistant";
  const sub = resp.stopReason
    ? `stop: ${resp.stopReason}`
    : resp.streamed
      ? t("conversation.stream")
      : null;
  parent.appendChild(
    messageCard("resp", role, sub, resp.content ?? [], toolResults),
  );
}

function messageCard(
  key: string,
  role: string,
  sub: string | null,
  blocks: AnthropicBlock[],
  toolResults: Map<string, AnthropicBlock>,
): HTMLElement {
  const state = getState();
  const selected = state.selection.messageKey === key && !state.selection.toolUseId;

  const card = h(
    `div.tl-card.role-${role}${selected ? ".selected" : ""}`,
    {
      onclick: () => selectMessage(key),
    },
    h(
      "div.tl-card-head",
      null,
      h(`span.role-chip.role-${role}`, null, role),
      sub ? h("span.tl-card-sub", null, sub) : null,
    ),
  );

  if (blocks.length === 0) {
    card.appendChild(h("div.tl-empty-block", null, t("conversation.responseEmpty")));
  } else {
    const body = h("div.tl-card-body");
    for (const blk of blocks) {
      body.appendChild(renderBlock(key, blk, toolResults));
    }
    card.appendChild(body);
  }
  return card;
}

function toolsCard(tools: AnthropicTool[]): HTMLElement {
  const list = h("div.tl-tools-list");
  for (const tl of tools) {
    const row = h(
      "div.tl-tool-row",
      null,
      h("span.tl-tool-name", null, tl.name ?? "?"),
      tl.description
        ? h("span.tl-tool-desc", null, ` — ${tl.description}`)
        : null,
    );
    list.appendChild(row);
  }
  return h(
    "div.tl-card.role-tools",
    null,
    h(
      "div.tl-card-head",
      null,
      h("span.role-chip.role-tools", null, t("conversation.toolsTitle")),
      h("span.tl-card-sub", null, `${tools.length}`),
    ),
    list,
  );
}

function renderBlock(
  messageKey: string,
  blk: AnthropicBlock,
  toolResults: Map<string, AnthropicBlock>,
): HTMLElement {
  const type = blk.type ?? "unknown";
  switch (type) {
    case "text":
      return h("div.blk.text", null, blk.text ?? "");
    case "thinking":
    case "redacted_thinking":
      return h(
        "div.blk.thinking",
        null,
        h("div.blk-tag", null, type),
        h("div", null, blk.text ?? ""),
      );
    case "tool_use": {
      const state = getState();
      const selected = state.selection.toolUseId === blk.id;
      const result = blk.id ? toolResults.get(blk.id) : undefined;
      return h(
        `div.blk.tool-use${selected ? ".selected" : ""}`,
        {
          onclick: (e: Event) => {
            e.stopPropagation();
            if (blk.id) selectToolUse(messageKey, blk.id);
          },
        },
        h(
          "div.blk-tag",
          null,
          h("span.blk-icon", null, "▶"),
          h("span.blk-kind", null, "tool_use"),
          h("span.tool-name", null, blk.name ?? "?"),
          blk.id ? h("span.tool-id", null, blk.id) : null,
          result ? statusBadge(result.isError === true) : null,
        ),
        h("div.blk-args", null, summarizeInput(blk.input)),
      );
    }
    case "tool_result": {
      const isErr = blk.isError === true;
      return h(
        `div.blk.tool-result${isErr ? ".is-error" : ""}`,
        null,
        h(
          "div.blk-tag",
          null,
          h("span.blk-kind", null, "tool_result"),
          blk.toolUseId ? h("span.tool-id", null, blk.toolUseId) : null,
          isErr ? h("span.tool-err", null, "ERROR") : null,
        ),
        renderToolResultBody(blk.content),
      );
    }
    case "image":
      return h(
        "div.blk.image",
        null,
        h("div.blk-tag", null, "image"),
        rawJsonBox(blk.raw ?? null),
      );
    default:
      return h(
        "div.blk.unknown",
        null,
        h("div.blk-tag", null, type),
        h("div.banner.info", null, t("conversation.unknownBlockHint")),
        rawJsonBox(blk.raw ?? blk),
      );
  }
}

function renderToolResultBody(content: unknown): HTMLElement {
  if (typeof content === "string") {
    return h("div.blk-result-text", null, content);
  }
  if (Array.isArray(content)) {
    const wrap = h("div.blk-result-blocks");
    for (const c of content as AnthropicBlock[]) {
      if (c.type === "text") wrap.appendChild(h("div.blk-result-text", null, c.text ?? ""));
      else wrap.appendChild(rawJsonBox(c));
    }
    return wrap;
  }
  if (content === null || content === undefined) {
    return h("div.blk-empty", null, "(empty)");
  }
  return rawJsonBox(content);
}

// Collect tool_use → tool_result mapping from request.messages so a click on
// a tool_use in the response can light up its outcome on the right.
function collectToolResults(
  req: AnthropicRequest | undefined,
): Map<string, AnthropicBlock> {
  const out = new Map<string, AnthropicBlock>();
  if (!req?.messages) return out;
  for (const msg of req.messages) {
    for (const blk of msg.content ?? []) {
      if (blk.type === "tool_result" && blk.toolUseId) {
        out.set(blk.toolUseId, blk);
      }
    }
  }
  return out;
}

export function findToolUseInEntry(
  entry: TrafficEntry,
  toolUseId: string,
): { use: AnthropicBlock | null; result: AnthropicBlock | null } {
  const analysis = entry.analysis as Analysis | undefined;
  const a = analysis?.anthropic;
  let use: AnthropicBlock | null = null;
  let result: AnthropicBlock | null = null;
  if (!a) return { use, result };
  for (const msg of a.request?.messages ?? []) {
    for (const blk of msg.content ?? []) {
      if (blk.type === "tool_use" && blk.id === toolUseId) use = blk;
      if (blk.type === "tool_result" && blk.toolUseId === toolUseId) result = blk;
    }
  }
  for (const blk of a.response?.content ?? []) {
    if (blk.type === "tool_use" && blk.id === toolUseId) use = blk;
  }
  return { use, result };
}

function totalUsage(u: AnthropicUsage): {
  input: number;
  output: number;
  cacheRead: number;
  cacheCreate: number;
} {
  const cacheRead = u.cacheReadInputTokens ?? 0;
  const cacheCreate = u.cacheCreationInputTokens ?? 0;
  return {
    input: (u.inputTokens ?? 0) + cacheRead + cacheCreate,
    output: u.outputTokens ?? 0,
    cacheRead,
    cacheCreate,
  };
}

function summarizeInput(input: unknown): string {
  if (input === null || input === undefined) return "{}";
  if (typeof input === "string") return input;
  try {
    return JSON.stringify(input);
  } catch {
    return String(input);
  }
}

function chip(label: string, variant?: "ok" | "err" | "warn" | "tool"): HTMLElement {
  return h(`span.tl-chip${variant ? `.v-${variant}` : ""}`, null, label);
}

function statusBadge(isErr: boolean): HTMLElement {
  return h(
    `span.tl-stat.${isErr ? "err" : "ok"}`,
    null,
    isErr ? "error" : "ok",
  );
}

function rawJsonBox(value: unknown): HTMLElement {
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

function fmtTok(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0";
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
  return String(n);
}
