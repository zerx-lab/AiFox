// Center pane: structured timeline of the selected traffic entry.
//
// For Anthropic Messages requests we render system / tools / messages /
// response as a chronological set of cards with role chips and clickable
// tool_use blocks. For everything else we fall back to a generic "no
// structured view" hint so the user can still inspect raw bodies on the
// right.

import type { components } from "../../api/client";
import { t } from "../i18n";
import { renderCallStack } from "./callstack";
import { h } from "./dom";
import { fmtDuration, fmtTime, statusKind } from "./format";
import { highlight } from "./highlight";
import { renderReplayPopover } from "./replay";
import {
  type CenterView,
  type EntryMeta,
  getState,
  selectedFull,
  selectMessage,
  selectToolUse,
  setCenterView,
  setState,
  type TrafficEntry,
  toggleMessageExpanded,
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
  // selectedFull() returns the loaded full TrafficEntry for the current
  // selection; null means nothing selected or still loading.
  const entry = selectedFull();

  if (!entry) {
    // Loading window after an entry-switch: selectedId is set but the full
    // analysis hasn't been fetched yet. Keep the stable shell — a lightweight
    // header + the turn strip (both derivable from the EntryMeta already in the
    // list) + the view switcher — and show a loading placeholder for the
    // message area, instead of collapsing the whole center pane to the empty
    // state. That collapse is what flashed the center pane on every
    // entry-switch (the sibling of the detail-pane flicker). The genuine
    // "nothing selected" case still shows the empty hint.
    const meta = state.selectedId
      ? state.entries.find((e) => e.id === state.selectedId)
      : undefined;
    if (!meta) {
      return h(
        "div.timeline",
        null,
        viewSwitcher(state.centerView),
        h(
          "div.tl-empty",
          null,
          h("div.tl-empty-mark"),
          h("div.tl-empty-title", null, t("timeline.emptyTitle")),
          h("div.tl-empty-hint", null, t("timeline.emptyHint")),
        ),
      );
    }
    const skeleton = h("div.timeline");
    skeleton.appendChild(renderHeaderMeta(meta));
    const skelBar = renderEntryBar(meta);
    if (skelBar) skeleton.appendChild(skelBar);
    skeleton.appendChild(viewSwitcher(state.centerView));
    skeleton.appendChild(h("div.tl-loading", null, t("detail.loading")));
    return skeleton;
  }

  const root = h("div.timeline");
  root.appendChild(renderHeader(entry));
  const entryBar = renderEntryBar(entry);
  if (entryBar) root.appendChild(entryBar);
  root.appendChild(viewSwitcher(state.centerView));

  if (state.centerView === "stack") {
    root.appendChild(renderCallStack(entry));
  } else {
    const analysis = entry.analysis as Analysis | undefined;
    if (analysis?.anthropic) {
      root.appendChild(renderAnthropic(analysis.anthropic, analysis.warnings ?? []));
    } else {
      root.appendChild(renderGeneric(entry, analysis));
    }
  }
  const popover = renderReplayPopover();
  if (popover) root.appendChild(popover);
  return root;
}

function viewSwitcher(active: CenterView): HTMLElement {
  return h(
    "div.center-view-switch",
    null,
    viewBtn("timeline", t("centerView.timeline"), active),
    viewBtn("stack", t("centerView.stack"), active),
  );
}

function viewBtn(view: CenterView, label: string, active: CenterView): HTMLElement {
  return h(
    "button",
    {
      class: active === view ? "active" : "",
      onclick: () => setCenterView(view),
    },
    label,
  );
}

function renderHeader(entry: TrafficEntry): HTMLElement {
  const analysis = entry.analysis as Analysis | undefined;
  const anth = analysis?.anthropic;
  const req = anth?.request;
  const resp = anth?.response;
  const usage = resp?.usage;

  // Model falls back across providers: Anthropic request/response, then OpenAI
  // chat, then OpenAI Responses, so every recognized entry shows a model in the
  // header instead of a dash.
  const oai = analysis?.openai;
  const resp_ = analysis?.responses;
  const model =
    req?.model ||
    resp?.model ||
    oai?.response?.model ||
    oai?.request?.model ||
    resp_?.response?.model ||
    resp_?.request?.model ||
    "—";

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

// Lightweight header for the loading skeleton, derived from the EntryMeta list
// projection. Mirrors renderHeader's shape (model line + a couple of chips)
// closely enough that the turn strip below it doesn't shift when the full
// header swaps in once the analysis loads.
function renderHeaderMeta(meta: EntryMeta): HTMLElement {
  const chips: HTMLElement[] = [];
  if (meta.statusCode > 0) {
    chips.push(chip(`${meta.statusCode}`, meta.statusCode < 400 ? "ok" : "err"));
  }
  if (meta.durationMillis > 0) chips.push(chip(fmtDuration(meta.durationMillis)));
  return h(
    "div.tl-header",
    null,
    h(
      "div.tl-header-top",
      null,
      h("span.tl-model", null, meta.model || "—"),
      h("span.tl-endpoint", null, `${meta.method} ${meta.url || ""}`),
    ),
    h("div.tl-chips", null, ...chips),
  );
}

// When the current entry belongs to a multi-turn session, render a horizontal
// strip of chips — one per turn — so the user can hop between calls without
// opening the sidebar tree. Hidden for solo / unsessioned entries; the chips
// would be visual noise.
function renderEntryBar(entry: { id: string }): HTMLElement | null {
  const state = getState();
  const session = state.sessions.find((s) => (s.entryIds ?? []).includes(entry.id));
  if (!session) return null;
  const ids = session.entryIds ?? [];
  if (ids.length <= 1) return null;

  const chips: HTMLElement[] = [];
  ids.forEach((id, i) => {
    // state.entries holds EntryMeta — these are peer turns, not the full entry
    const target = state.entries.find((e) => e.id === id);
    if (!target) return;
    chips.push(entryChip(target, i + 1, id === entry.id));
  });
  if (chips.length === 0) return null;
  return h("div.tl-entry-bar", null, ...chips);
}

// entryChip uses EntryMeta because it operates on list entries (session
// siblings), not the selected full entry.
function entryChip(target: EntryMeta, ordinal: number, active: boolean): HTMLElement {
  const totalTok =
    (target.inputTokens ?? 0) +
    (target.outputTokens ?? 0) +
    (target.cacheRead ?? 0) +
    (target.cacheCreate ?? 0);
  const kind = statusKind(target);
  const classes = [
    "tl-entry-chip",
    active ? "active" : "",
    kind === "err" ? "err" : "",
    target.streaming ? "streaming" : "",
  ]
    .filter(Boolean)
    .join(" ");
  return h(
    "button",
    {
      class: classes,
      title: `${target.method} ${target.url}`,
      onclick: () => setState({ selectedId: target.id }),
    },
    h("span.tl-entry-chip-num", null, `#${ordinal}`),
    h("span.tl-entry-chip-time", null, fmtTime(target.startedAt)),
    totalTok > 0 ? h("span.tl-entry-chip-tok", null, fmtTok(totalTok)) : null,
  );
}

function renderGeneric(entry: TrafficEntry, analysis?: Analysis): HTMLElement {
  // OpenAI chat and Responses (Codex) entries are recognized but their rich
  // structured view is a later milestone; show a provider-aware hint so the user
  // knows the parser saw it, plus the model. Token/cost still surface via meta.
  const oai = analysis?.openai;
  const resp = analysis?.responses;
  const hint = resp
    ? t("timeline.responsesPending")
    : oai
      ? t("timeline.openaiPending")
      : t("timeline.noStructuredView");
  const model =
    oai?.response?.model ||
    oai?.request?.model ||
    resp?.response?.model ||
    resp?.request?.model ||
    "";
  return h(
    "div.tl-generic",
    null,
    h("div.banner.info", null, hint),
    model
      ? h("div.tl-generic-row", null, h("dt", null, "Model"), h("dd", null, model))
      : null,
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

// Rough character-count threshold below which a card always renders fully
// (no toggle, no fade). Picked empirically: ~600 chars is roughly 8 lines of
// chat text, which fits inside the 160px collapsed cap without truncation.
// Anything shorter doesn't benefit from a "click to expand" affordance.
const COLLAPSE_THRESHOLD_CHARS = 600;

function messageCard(
  key: string,
  role: string,
  sub: string | null,
  blocks: AnthropicBlock[],
  toolResults: Map<string, AnthropicBlock>,
): HTMLElement {
  const state = getState();
  const selected = state.selection.messageKey === key && !state.selection.toolUseId;
  const collapsible = estimateBlocksLength(blocks) >= COLLAPSE_THRESHOLD_CHARS;
  const expanded = !collapsible || state.expandedMessages.has(key);

  const head = h(
    "div.tl-card-head",
    null,
    collapsible
      ? h(
          "button",
          {
            class: "tl-card-toggle",
            title: expanded ? t("conversation.collapse") : t("conversation.expand"),
            "aria-expanded": expanded ? "true" : "false",
            onclick: (e: Event) => {
              e.stopPropagation();
              toggleMessageExpanded(key);
            },
          },
          expanded ? "▾" : "▸",
        )
      : null,
    h(`span.role-chip.role-${role}`, null, role),
    sub ? h("span.tl-card-sub", null, sub) : null,
    copyButton(blocks),
  );

  const cardClasses = [
    "tl-card",
    `role-${role}`,
    selected ? "selected" : "",
    collapsible && !expanded ? "collapsed" : "",
  ]
    .filter(Boolean)
    .join(".");

  const card = h(
    `div.${cardClasses}`,
    {
      onclick: () => selectMessage(key),
    },
    head,
  );

  if (blocks.length === 0) {
    card.appendChild(h("div.tl-empty-block", null, t("conversation.responseEmpty")));
    return card;
  }

  const body = h("div.tl-card-body");
  for (const blk of blocks) {
    body.appendChild(renderBlock(key, blk, toolResults));
  }
  card.appendChild(body);

  if (collapsible && !expanded) {
    card.appendChild(
      h(
        "button.tl-card-expand",
        {
          onclick: (e: Event) => {
            e.stopPropagation();
            toggleMessageExpanded(key);
          },
        },
        t("conversation.expand"),
      ),
    );
  }
  return card;
}

function copyButton(blocks: AnthropicBlock[]): HTMLElement {
  const btn = h("button", {
    class: "tl-card-copy",
    title: t("conversation.copy"),
    "aria-label": t("conversation.copy"),
  });
  btn.appendChild(copyIcon());
  btn.addEventListener("click", async (e: Event) => {
    e.stopPropagation();
    const text = blocksToPlainText(blocks);
    try {
      await navigator.clipboard.writeText(text);
      setCopyButtonState(btn, "ok");
    } catch {
      setCopyButtonState(btn, "err");
    }
  });
  return btn;
}

function copyIcon(): SVGSVGElement {
  // Lucide-style "copy" outline. 14px in a 24-grid; matches the visual
  // weight of role chips and existing chevrons.
  const ns = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(ns, "svg");
  svg.setAttribute("width", "14");
  svg.setAttribute("height", "14");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "2");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  const rect = document.createElementNS(ns, "rect");
  rect.setAttribute("x", "9");
  rect.setAttribute("y", "9");
  rect.setAttribute("width", "13");
  rect.setAttribute("height", "13");
  rect.setAttribute("rx", "2");
  rect.setAttribute("ry", "2");
  svg.appendChild(rect);
  const path = document.createElementNS(ns, "path");
  path.setAttribute("d", "M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1");
  svg.appendChild(path);
  return svg;
}

function checkIcon(): SVGSVGElement {
  const ns = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(ns, "svg");
  svg.setAttribute("width", "14");
  svg.setAttribute("height", "14");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "2.4");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  const p = document.createElementNS(ns, "path");
  p.setAttribute("d", "M20 6L9 17l-5-5");
  svg.appendChild(p);
  return svg;
}

function setCopyButtonState(btn: HTMLElement, status: "ok" | "err") {
  btn.classList.add("copied");
  if (status === "err") btn.classList.add("err");
  btn.replaceChildren(checkIcon());
  window.setTimeout(() => {
    btn.classList.remove("copied", "err");
    btn.replaceChildren(copyIcon());
  }, 1200);
}

function blocksToPlainText(blocks: AnthropicBlock[]): string {
  const out: string[] = [];
  for (const blk of blocks) {
    const type = blk.type ?? "unknown";
    switch (type) {
      case "text":
        out.push(blk.text ?? "");
        break;
      case "thinking":
      case "redacted_thinking":
        out.push(`[${type}] ${blk.text ?? ""}`);
        break;
      case "tool_use": {
        const inputStr =
          typeof blk.input === "string" ? blk.input : safeStringify(blk.input);
        out.push(`[tool_use ${blk.name ?? "?"} ${blk.id ?? ""}]\n${inputStr}`);
        break;
      }
      case "tool_result": {
        let content = "";
        if (typeof blk.content === "string") {
          content = blk.content;
        } else if (Array.isArray(blk.content)) {
          content = (blk.content as AnthropicBlock[])
            .map((c) => (typeof c.text === "string" ? c.text : safeStringify(c)))
            .join("\n");
        } else if (blk.content !== undefined && blk.content !== null) {
          content = safeStringify(blk.content);
        }
        out.push(`[tool_result ${blk.toolUseId ?? ""}]\n${content}`);
        break;
      }
      case "image":
        out.push("[image]");
        break;
      default:
        out.push(`[${type}] ${safeStringify(blk.raw ?? blk)}`);
    }
  }
  return out.join("\n\n");
}

function safeStringify(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function estimateBlocksLength(blocks: AnthropicBlock[]): number {
  let total = 0;
  for (const blk of blocks) {
    if (typeof blk.text === "string") {
      total += blk.text.length;
    }
    if (blk.type === "tool_use") {
      total += 80;
      total += safeStringifyLength(blk.input);
    } else if (blk.type === "tool_result") {
      if (typeof blk.content === "string") {
        total += blk.content.length;
      } else if (Array.isArray(blk.content)) {
        for (const c of blk.content as AnthropicBlock[]) {
          if (typeof c.text === "string") total += c.text.length;
        }
      }
    }
    total += 24; // tag / wrapper overhead per block
  }
  return total;
}

function safeStringifyLength(value: unknown): number {
  if (value === null || value === undefined) return 0;
  if (typeof value === "string") return value.length;
  try {
    return JSON.stringify(value).length;
  } catch {
    return 0;
  }
}

// Tools cards live in the same expansion namespace as message cards under
// the reserved key "tools" — messageCard keys never collide ("sys", "resp",
// "req-N"). Long tool catalogs (Claude Code's default ~63 entries) blow out
// the timeline if they render fully by default.
function toolsCard(tools: AnthropicTool[]): HTMLElement {
  const state = getState();
  const key = "tools";
  const collapsible = estimateToolsLength(tools) >= COLLAPSE_THRESHOLD_CHARS;
  const expanded = !collapsible || state.expandedMessages.has(key);

  const head = h(
    "div.tl-card-head",
    null,
    collapsible
      ? h(
          "button",
          {
            class: "tl-card-toggle",
            title: expanded ? t("conversation.collapse") : t("conversation.expand"),
            "aria-expanded": expanded ? "true" : "false",
            onclick: (e: Event) => {
              e.stopPropagation();
              toggleMessageExpanded(key);
            },
          },
          expanded ? "▾" : "▸",
        )
      : null,
    h("span.role-chip.role-tools", null, t("conversation.toolsTitle")),
    h("span.tl-card-sub", null, `${tools.length}`),
  );

  const list = h("div.tl-tools-list");
  for (const tl of tools) {
    list.appendChild(
      h(
        "div.tl-tool-row",
        null,
        h("span.tl-tool-name", null, tl.name ?? "?"),
        tl.description
          ? h("span.tl-tool-desc", null, ` — ${tl.description}`)
          : null,
      ),
    );
  }

  const cardClasses = ["tl-card", "role-tools", collapsible && !expanded ? "collapsed" : ""]
    .filter(Boolean)
    .join(".");

  const card = h(`div.${cardClasses}`, null, head, list);

  if (collapsible && !expanded) {
    card.appendChild(
      h(
        "button.tl-card-expand",
        {
          onclick: (e: Event) => {
            e.stopPropagation();
            toggleMessageExpanded(key);
          },
        },
        t("conversation.expand"),
      ),
    );
  }
  return card;
}

function estimateToolsLength(tools: AnthropicTool[]): number {
  let total = 0;
  for (const tl of tools) {
    total += (tl.name?.length ?? 0) + (tl.description?.length ?? 0) + 16;
  }
  return total;
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
