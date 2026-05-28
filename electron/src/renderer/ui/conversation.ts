// Structured "Conversation" view for captured /v1/messages traffic.
//
// Renders the parsed analysis from the Go analyzer (see internal/llmparse).
// Falls back to a JSON preview for fields the analyzer marked as unknown so
// new Anthropic API features surface immediately even without renderer
// updates.

import { t } from "../i18n";
import type { components } from "../../api/client";
import { h } from "./dom";
import { highlight } from "./highlight";
import type { TrafficEntry } from "./state";

type Analysis = components["schemas"]["Analysis"];
type AnthropicAnalysis = components["schemas"]["AnthropicAnalysis"];
type AnthropicRequest = components["schemas"]["AnthropicRequest"];
type AnthropicResponse = components["schemas"]["AnthropicResponse"];
type AnthropicBlock = components["schemas"]["AnthropicBlock"];
type AnthropicMessage = components["schemas"]["AnthropicMessage"];
type AnthropicTool = components["schemas"]["AnthropicTool"];
type AnthropicUsage = components["schemas"]["AnthropicUsage"];

// hasConversation reports whether the entry has a recognized structured view.
// Callers use this to decide whether to show the Conversation tab at all.
export function hasConversation(entry: TrafficEntry | null): boolean {
  if (!entry?.analysis) return false;
  return entry.analysis.kind === "anthropic.messages" && !!entry.analysis.anthropic;
}

export function renderConversation(entry: TrafficEntry): HTMLElement {
  const analysis = entry.analysis as Analysis | undefined;
  if (!analysis?.anthropic) {
    return h(
      "div.conversation",
      null,
      h("div.banner.info", null, t("conversation.unparseable")),
    );
  }

  const root = h("div.conversation");

  const warnings = analysis.warnings ?? [];
  if (warnings.length > 0) {
    root.appendChild(
      h(
        "div.banner.warn",
        null,
        h("strong", null, t("conversation.warningsTitle")),
        h(
          "ul.warnings",
          null,
          ...warnings.map((w) => h("li", null, w)),
        ),
      ),
    );
  }

  root.appendChild(renderAnthropic(analysis.anthropic));
  return root;
}

function renderAnthropic(a: AnthropicAnalysis): HTMLElement {
  const root = h("div.conv-anthropic");
  if (a.request) root.appendChild(renderRequest(a.request));
  if (a.response) root.appendChild(renderResponse(a.response));
  return root;
}

function renderRequest(req: AnthropicRequest): HTMLElement {
  const head = h(
    "div.conv-head",
    null,
    h("span.conv-model", null, req.model || "—"),
    chip(req.stream ? t("conversation.stream") : t("conversation.nonStream")),
    req.maxTokens
      ? chip(`max_tokens: ${req.maxTokens}`)
      : null,
    req.temperature !== undefined
      ? chip(`temp: ${req.temperature}`)
      : null,
    req.topP !== undefined ? chip(`top_p: ${req.topP}`) : null,
    req.topK !== undefined ? chip(`top_k: ${req.topK}`) : null,
  );

  const wrap = h("section.conv-section", null, h("h3", null, t("conversation.requestTitle")), head);

  if (req.system && req.system.length > 0) {
    wrap.appendChild(renderSystem(req.system));
  }
  if (req.tools && req.tools.length > 0) {
    wrap.appendChild(renderTools(req.tools));
  }
  if (req.messages && req.messages.length > 0) {
    const list = h("div.conv-msgs");
    for (const m of req.messages) list.appendChild(renderMessage(m));
    wrap.appendChild(
      h(
        "div.conv-subsection",
        null,
        h(
          "h4",
          null,
          t("conversation.messagesTitle"),
          h("span.conv-count", null, ` (${req.messages.length})`),
        ),
        list,
      ),
    );
  }
  if (req.unknownFields && Object.keys(req.unknownFields).length > 0) {
    wrap.appendChild(
      h(
        "div.conv-subsection",
        null,
        h("h4", null, t("conversation.unknownFields")),
        h("div.banner.info", null, t("conversation.unknownFieldsHint")),
        rawJsonBox(req.unknownFields),
      ),
    );
  }
  return wrap;
}

function renderResponse(resp: AnthropicResponse): HTMLElement {
  const head = h(
    "div.conv-head",
    null,
    h("span.conv-model", null, resp.model || "—"),
    resp.streamed ? chip(t("conversation.stream")) : null,
    resp.stopReason ? chip(`stop: ${resp.stopReason}`) : null,
    resp.id ? chip(resp.id) : null,
  );

  const wrap = h("section.conv-section", null, h("h3", null, t("conversation.responseTitle")), head);

  if (resp.error) {
    wrap.appendChild(
      h(
        "div.banner.err",
        null,
        h("strong", null, resp.error.type ?? "error"),
        " ",
        resp.error.message ?? "",
      ),
    );
  }

  if (resp.usage) wrap.appendChild(renderUsage(resp.usage));

  if (resp.content && resp.content.length > 0) {
    const blocks = h("div.conv-blocks");
    for (const b of resp.content) blocks.appendChild(renderBlock(b));
    wrap.appendChild(blocks);
  } else if (!resp.error) {
    wrap.appendChild(h("div.conv-empty", null, t("conversation.responseEmpty")));
  }
  return wrap;
}

function renderUsage(u: AnthropicUsage): HTMLElement {
  const totalCached =
    (u.cacheReadInputTokens ?? 0) + (u.cacheCreationInputTokens ?? 0);
  const totalInput = (u.inputTokens ?? 0) + totalCached;
  return h(
    "div.conv-usage",
    null,
    pill(t("conversation.usageInput"), String(totalInput || (u.inputTokens ?? 0))),
    pill(t("conversation.usageOutput"), String(u.outputTokens ?? 0)),
    u.cacheReadInputTokens
      ? pill(t("conversation.usageCacheRead"), String(u.cacheReadInputTokens), "ok")
      : null,
    u.cacheCreationInputTokens
      ? pill(
          t("conversation.usageCacheCreate"),
          String(u.cacheCreationInputTokens),
          "tool",
        )
      : null,
  );
}

function renderSystem(blocks: AnthropicBlock[]): HTMLElement {
  const body = h("div.conv-blocks");
  for (const b of blocks) body.appendChild(renderBlock(b));
  return h(
    "div.conv-subsection",
    null,
    h("h4", null, t("conversation.systemTitle")),
    body,
  );
}

function renderTools(tools: AnthropicTool[]): HTMLElement {
  const list = h("div.conv-tools");
  for (const tl of tools) {
    if (tl.raw && !tl.name) {
      list.appendChild(
        h(
          "div.conv-tool unknown",
          null,
          h("div.conv-tool-head", null, h("strong", null, "(unknown tool)")),
          rawJsonBox(tl.raw),
        ),
      );
      continue;
    }
    list.appendChild(
      h(
        "div.conv-tool",
        null,
        h(
          "div.conv-tool-head",
          null,
          h("strong", null, tl.name ?? "?"),
          tl.description
            ? h("span.conv-tool-desc", null, ` — ${tl.description}`)
            : null,
        ),
        tl.inputSchema !== undefined && tl.inputSchema !== null
          ? rawJsonBox(tl.inputSchema)
          : null,
      ),
    );
  }
  return h(
    "div.conv-subsection",
    null,
    h(
      "h4",
      null,
      t("conversation.toolsTitle"),
      h("span.conv-count", null, ` (${tools.length})`),
    ),
    list,
  );
}

function renderMessage(msg: AnthropicMessage): HTMLElement {
  const role = msg.role || "?";
  const blocks = h("div.conv-blocks");
  for (const b of msg.content ?? []) blocks.appendChild(renderBlock(b));
  return h(
    `div.conv-msg.role-${role}`,
    null,
    h("div.conv-msg-head", null, h("span.conv-role", null, role)),
    blocks,
  );
}

function renderBlock(blk: AnthropicBlock): HTMLElement {
  const type = blk.type ?? "unknown";
  switch (type) {
    case "text":
      return h("div.conv-block.text", null, blk.text ?? "");
    case "thinking":
    case "redacted_thinking":
      return h(
        "div.conv-block.thinking",
        null,
        h("div.conv-block-tag", null, type),
        h("div", null, blk.text ?? ""),
      );
    case "tool_use":
      return h(
        "div.conv-block.tool-use",
        null,
        h(
          "div.conv-block-tag",
          null,
          "tool_use",
          h("span.conv-tool-name", null, ` ${blk.name ?? ""}`),
          blk.id ? h("span.conv-id", null, ` ${blk.id}`) : null,
        ),
        rawJsonBox(blk.input ?? {}),
      );
    case "tool_result": {
      const isErr = blk.isError === true;
      const body = renderToolResultContent(blk.content);
      return h(
        `div.conv-block.tool-result${isErr ? " is-error" : ""}`,
        null,
        h(
          "div.conv-block-tag",
          null,
          "tool_result",
          blk.toolUseId
            ? h("span.conv-id", null, ` ${blk.toolUseId}`)
            : null,
          isErr ? h("span.conv-err", null, " is_error") : null,
        ),
        body,
      );
    }
    case "image":
      return h(
        "div.conv-block.image",
        null,
        h("div.conv-block-tag", null, "image"),
        rawJsonBox(blk.raw ?? null),
      );
    default:
      return h(
        "div.conv-block.unknown",
        null,
        h("div.conv-block-tag", null, type),
        h("div.banner.info", null, t("conversation.unknownBlockHint")),
        rawJsonBox(blk.raw ?? blk),
      );
  }
}

// tool_result.content can be a plain string OR an array of blocks (text /
// image). Normalize so the renderer has one rule.
function renderToolResultContent(content: unknown): HTMLElement {
  if (typeof content === "string") {
    return h("div.conv-tool-result-text", null, content);
  }
  if (Array.isArray(content)) {
    const body = h("div.conv-blocks");
    for (const b of content as AnthropicBlock[]) body.appendChild(renderBlock(b));
    return body;
  }
  if (content === null || content === undefined) {
    return h("div.conv-empty", null, "(empty)");
  }
  return rawJsonBox(content);
}

function chip(label: string): HTMLElement {
  return h("span.conv-chip", null, label);
}

function pill(label: string, value: string, variant?: string): HTMLElement {
  return h(
    `span.conv-pill${variant ? ` v-${variant}` : ""}`,
    null,
    h("span.k", null, label),
    h("span.v", null, value),
  );
}

function rawJsonBox(value: unknown): HTMLElement {
  let text: string;
  if (typeof value === "string") {
    text = value;
  } else {
    try {
      text = JSON.stringify(value, null, 2);
    } catch {
      text = String(value);
    }
  }
  const pre = document.createElement("pre");
  pre.className = "codebox kind-json conv-raw";
  pre.appendChild(highlight(text, "json"));
  return pre;
}
