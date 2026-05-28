// Cache tab — three views of the cache layout of the current request.
//
// segmented: each block (system / tool / message) is one labeled row with
//            its cache status badge. Best at-a-glance.
// heatmap:   each block's preview text is tinted by hit count.
// blame:     each line is tagged with the block label that owns it (akin to
//            git blame). Useful for very long system prompts.
//
// All three pull from the same input: the parsed AnthropicRequest plus the
// AnthropicUsage in the response. A block is considered a "cache breakpoint"
// when it carries cacheControl; everything from the previous breakpoint up
// to this one is one cache region.

import type { components } from "../../api/client";
import { t } from "../i18n";
import { h } from "./dom";
import { type CacheStyle, getState, setCacheStyle } from "./state";

type AnthropicAnalysis = components["schemas"]["AnthropicAnalysis"];
type AnthropicBlock = components["schemas"]["AnthropicBlock"];
type AnthropicTool = components["schemas"]["AnthropicTool"];

interface Segment {
  label: string;
  text: string;
  status: "hit" | "new" | "passthrough";
  /** approximate token count, for hit/new ratios on the bar */
  tokens: number;
  /** number of times this segment is estimated to have been served from cache */
  hits: number;
}

export function renderCache(a: AnthropicAnalysis | undefined): HTMLElement {
  const wrap = h("div");
  if (!a?.request) {
    wrap.appendChild(h("div.detail-empty", null, t("detail.cacheNoRequest")));
    return wrap;
  }
  const segments = buildSegments(a);
  if (segments.length === 0) {
    wrap.appendChild(h("div.detail-empty", null, t("detail.cacheNoSegments")));
    return wrap;
  }

  const hitTok = segments
    .filter((s) => s.status === "hit")
    .reduce((acc, s) => acc + s.tokens, 0);
  const newTok = segments
    .filter((s) => s.status === "new")
    .reduce((acc, s) => acc + s.tokens, 0);
  const passthroughTok = segments
    .filter((s) => s.status === "passthrough")
    .reduce((acc, s) => acc + s.tokens, 0);
  const total = Math.max(1, hitTok + newTok + passthroughTok);
  const hitPct = (hitTok / total) * 100;
  const newPct = (newTok / total) * 100;

  wrap.appendChild(
    h(
      "div.cache-stats",
      null,
      statCell(t("detail.cacheHits"), hitTok.toLocaleString(), "hit"),
      statCell(t("detail.cacheNew"), newTok.toLocaleString(), "new"),
      statCell(t("detail.cacheHitRate"), `${hitPct.toFixed(0)}%`),
    ),
  );

  const bar = h("div.cache-bar");
  if (hitPct > 0) {
    const seg = h("div.hit");
    seg.style.width = `${hitPct}%`;
    bar.appendChild(seg);
  }
  if (newPct > 0) {
    const seg = h("div.new");
    seg.style.width = `${newPct}%`;
    bar.appendChild(seg);
  }
  wrap.appendChild(bar);

  wrap.appendChild(
    h(
      "div.cache-legend",
      null,
      h("span.lh", null, h("i"), t("detail.cacheLegendHit")),
      h("span.ln", null, h("i"), t("detail.cacheLegendNew")),
    ),
  );

  wrap.appendChild(styleSelector(getState().cacheStyle));

  const style = getState().cacheStyle;
  if (style === "heatmap") wrap.appendChild(renderHeatmap(segments));
  else if (style === "blame") wrap.appendChild(renderBlame(segments));
  else wrap.appendChild(renderSegmented(segments));

  return wrap;
}

function statCell(label: string, value: string, variant?: string): HTMLElement {
  return h(
    `div.cstat${variant ? `.v-${variant}` : ""}`,
    null,
    h("div.l", null, label),
    h(`div.v${variant ? `.${variant}` : ""}`, null, value),
  );
}

function styleSelector(active: CacheStyle): HTMLElement {
  return h(
    "div.cache-style-seg",
    null,
    styleBtn("segmented", active),
    styleBtn("heatmap", active),
    styleBtn("blame", active),
  );
}

function styleBtn(style: CacheStyle, active: CacheStyle): HTMLElement {
  return h(
    "button",
    {
      class: active === style ? "active" : "",
      onclick: () => setCacheStyle(style),
    },
    t(`detail.cacheStyle.${style}`),
  );
}

// ---- Segment builders ----------------------------------------------------
//
// Strategy: walk system → tools → messages, build one segment per "cache
// region" (separated by a `cacheControl` marker), then label hit vs new
// using the response usage as a budget.

function buildSegments(a: AnthropicAnalysis): Segment[] {
  const req = a.request;
  if (!req) return [];
  const usage = a.response?.usage;
  const cacheRead = usage?.cacheReadInputTokens ?? 0;
  const cacheCreate = usage?.cacheCreationInputTokens ?? 0;

  const out: Segment[] = [];

  // System prompt segments
  for (const blk of req.system ?? []) {
    pushBlockSegment(out, "system", blk);
  }

  // Tools — collapsed into one segment unless a cacheControl appears on a tool
  if ((req.tools?.length ?? 0) > 0) {
    pushToolsSegment(out, req.tools ?? []);
  }

  // Messages
  (req.messages ?? []).forEach((msg, i) => {
    for (const blk of msg.content ?? []) {
      pushBlockSegment(out, `${msg.role || "?"}#${i}`, blk);
    }
  });

  // Now distribute hit vs new across segments by token budget. We approximate
  // tokens with `text.length / 4` (the ~average for English) — close enough
  // for the proportional bar / heatmap, but not authoritative.
  let hitBudget = cacheRead;
  let createBudget = cacheCreate;
  for (const seg of out) {
    if (hitBudget > 0) {
      seg.status = "hit";
      seg.hits = 1;
      hitBudget -= seg.tokens;
    } else if (createBudget > 0) {
      seg.status = "new";
      createBudget -= seg.tokens;
    } else {
      seg.status = "passthrough";
    }
  }
  return out;
}

function pushBlockSegment(out: Segment[], label: string, blk: AnthropicBlock) {
  const text = blockPreview(blk);
  const tokens = Math.max(1, Math.round(text.length / 4));
  out.push({
    label,
    text,
    status: "passthrough",
    tokens,
    hits: 0,
  });
}

function pushToolsSegment(out: Segment[], tools: AnthropicTool[]) {
  const text = tools.map((tl) => `${tl.name ?? "?"} — ${tl.description ?? ""}`).join("\n");
  const tokens = Math.max(1, Math.round(text.length / 4));
  out.push({
    label: `tools (${tools.length})`,
    text,
    status: "passthrough",
    tokens,
    hits: 0,
  });
}

function blockPreview(blk: AnthropicBlock): string {
  const type = blk.type ?? "unknown";
  switch (type) {
    case "text":
      return blk.text ?? "";
    case "thinking":
    case "redacted_thinking":
      return `[${type}] ${blk.text ?? ""}`;
    case "tool_use":
      return `[tool_use ${blk.name ?? "?"}] ${truncate(stringify(blk.input))}`;
    case "tool_result":
      return `[tool_result ${blk.toolUseId ?? "?"}] ${truncate(stringify(blk.content))}`;
    case "image":
      return "[image]";
    default:
      return `[${type}]`;
  }
}

function stringify(v: unknown): string {
  if (v === null || v === undefined) return "";
  if (typeof v === "string") return v;
  try {
    return JSON.stringify(v);
  } catch {
    return String(v);
  }
}

function truncate(s: string, n = 160): string {
  if (s.length <= n) return s;
  return `${s.slice(0, n)}…`;
}

// ---- View 1: segmented ---------------------------------------------------

function renderSegmented(segments: Segment[]): HTMLElement {
  const pre = h("pre.prompt-pre");
  for (const seg of segments) {
    pre.appendChild(
      h(
        `div.pseg.${seg.status}`,
        null,
        h(
          "div.pslabel",
          null,
          h("span", null, seg.label),
          h(
            "span.psbadge",
            null,
            seg.status === "hit"
              ? t("detail.cacheBadgeHit")
              : seg.status === "new"
                ? t("detail.cacheBadgeNew")
                : t("detail.cacheBadgePass"),
          ),
        ),
        h("div.pstxt", null, seg.text),
      ),
    );
  }
  return pre;
}

// ---- View 2: heatmap -----------------------------------------------------
//
// Renders each segment as a horizontal bar — width scaled to the largest
// segment so the relative weight of every block reads at a glance. This is
// the "no-scrolling" alternative to the segmented / blame views: useful for
// spotting which block is bloating the cache budget without paging through
// the actual prompt text.

function renderHeatmap(segments: Segment[]): HTMLElement {
  const wrap = h("div.heatmap-chart");
  const total = segments.reduce((acc, s) => acc + s.tokens, 0) || 1;
  const max = Math.max(1, ...segments.map((s) => s.tokens));
  for (const seg of segments) {
    const pct = (seg.tokens / total) * 100;
    const fillPct = (seg.tokens / max) * 100;
    wrap.appendChild(
      h(
        "div.hmrow",
        { title: seg.text || seg.label },
        h("div.hmrow-label", null, seg.label),
        h(
          "div.hmrow-track",
          null,
          (() => {
            const fill = h(`div.hmrow-fill.${seg.status}`);
            fill.style.width = `${fillPct}%`;
            return fill;
          })(),
        ),
        h(
          "div.hmrow-meta",
          null,
          h("span.hmrow-tok", null, seg.tokens.toLocaleString()),
          h("span.hmrow-pct", null, `${pct.toFixed(pct < 1 ? 1 : 0)}%`),
          h(`span.hmrow-status.${seg.status}`, null, heatmapStatusLabel(seg.status)),
        ),
      ),
    );
  }
  return wrap;
}

function heatmapStatusLabel(status: Segment["status"]): string {
  switch (status) {
    case "hit":
      return t("detail.cacheBadgeHit");
    case "new":
      return t("detail.cacheBadgeNew");
    default:
      return t("detail.cacheBadgePass");
  }
}

// ---- View 3: blame -------------------------------------------------------

function renderBlame(segments: Segment[]): HTMLElement {
  const wrap = h("div.blame");
  let lineNo = 1;
  for (const seg of segments) {
    for (const ln of seg.text.split("\n")) {
      const row = h(
        `div.blame-row.${seg.status}`,
        null,
        h("span.ln", null, String(lineNo)),
        h(
          "span.src",
          null,
          seg.status === "hit" ? `${seg.label} · ${seg.hits}×` : seg.label,
        ),
        h("span.txt", null, ln || " "),
      );
      wrap.appendChild(row);
      lineNo += 1;
    }
  }
  return wrap;
}
