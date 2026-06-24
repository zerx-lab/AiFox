// Call-stack view of one captured request — same data as the timeline but
// presented as a flat list of frames, with tool_use blocks indented under
// the message that issued them. Closest analogue to a debugger's stack.

import type { components } from "../../api/client";
import { t } from "../i18n";
import { h } from "./dom";
import {
  getState,
  selectMessage,
  selectToolUse,
  type TrafficEntry,
} from "./state";

type Analysis = components["schemas"]["Analysis"];

interface Frame {
  kind: "user" | "assistant" | "system" | "tool";
  label: string;
  args: string;
  status: "ok" | "fail" | null;
  depth: number;
  messageKey: string;
  toolUseId?: string;
}

export function renderCallStack(entry: TrafficEntry | null): HTMLElement {
  if (!entry) {
    return h(
      "div.cs-empty",
      null,
      h("div", null, t("timeline.emptyTitle")),
      h("div.muted", null, t("timeline.emptyHint")),
    );
  }
  const frames = buildFrames(entry);
  if (frames.length === 0) {
    return h(
      "div.cs-empty",
      null,
      h("div", null, t("timeline.noStructuredView")),
    );
  }

  const root = h("div.cs");
  root.appendChild(
    h(
      "div.cs-header",
      null,
      h("span", null, t("stack.title")),
      h(
        "span.cs-summary",
        null,
        t("stack.summary", {
          frames: String(frames.length),
        }),
      ),
    ),
  );
  const state = getState();
  for (const f of frames) {
    const selected =
      state.selection.messageKey === f.messageKey &&
      (f.kind === "tool"
        ? state.selection.toolUseId === f.toolUseId
        : !state.selection.toolUseId);
    root.appendChild(
      h(
        `div.cs-frame.${f.kind}${selected ? ".selected" : ""}`,
        {
          style: { paddingLeft: `${8 + f.depth * 20}px` },
          onclick: () => {
            if (f.kind === "tool" && f.toolUseId) selectToolUse(f.messageKey, f.toolUseId);
            else selectMessage(f.messageKey);
          },
        },
        h("span.name", null, f.label),
        h("span.args", { title: f.args }, f.args),
        f.status === null
          ? null
          : h(`span.stat.${f.status}`, null, f.status),
      ),
    );
  }
  return root;
}

function buildFrames(entry: TrafficEntry): Frame[] {
  const a = (entry.analysis as Analysis | undefined)?.anthropic;
  if (!a) return [];
  const out: Frame[] = [];

  // tool_result lookup so tool frames know if they failed.
  const results = new Map<string, boolean>();
  for (const m of a.request?.messages ?? []) {
    for (const blk of m.content ?? []) {
      if (blk.type === "tool_result" && blk.toolUseId) {
        results.set(blk.toolUseId, blk.isError === true);
      }
    }
  }

  if (a.request?.system && a.request.system.length > 0) {
    const total = a.request.system.reduce((acc, b) => acc + (b.text?.length ?? 0), 0);
    out.push({
      kind: "system",
      label: "system",
      args: `${total} chars`,
      status: null,
      depth: 0,
      messageKey: "sys",
    });
  }

  (a.request?.messages ?? []).forEach((m, i) => {
    const role = (m.role || "user") as "user" | "assistant";
    const msgKey = `req-${i}`;
    const blocks = m.content ?? [];
    const firstText = blocks.find((b) => b.type === "text")?.text ?? "";
    out.push({
      kind: role === "assistant" ? "assistant" : "user",
      label: role === "assistant" ? "assistant.respond" : "user.message",
      args: truncate(firstText, 60),
      status: null,
      depth: 0,
      messageKey: msgKey,
    });
    for (const blk of blocks) {
      if (blk.type === "tool_use" && blk.id) {
        const err = results.get(blk.id);
        out.push({
          kind: "tool",
          label: blk.name ?? "?",
          args: summarizeInput(blk.input),
          status: err === undefined ? null : err ? "fail" : "ok",
          depth: 1,
          messageKey: msgKey,
          toolUseId: blk.id,
        });
      }
    }
  });

  if (a.response) {
    const respKey = "resp";
    const blocks = a.response.content ?? [];
    const firstText = blocks.find((b) => b.type === "text")?.text ?? "";
    out.push({
      kind: "assistant",
      label: "assistant.respond",
      args: truncate(firstText, 60) || (a.response.stopReason ? `stop: ${a.response.stopReason}` : ""),
      status: a.response.error ? "fail" : null,
      depth: 0,
      messageKey: respKey,
    });
    for (const blk of blocks) {
      if (blk.type === "tool_use" && blk.id) {
        const err = results.get(blk.id);
        out.push({
          kind: "tool",
          label: blk.name ?? "?",
          args: summarizeInput(blk.input),
          status: err === undefined ? null : err ? "fail" : "ok",
          depth: 1,
          messageKey: respKey,
          toolUseId: blk.id,
        });
      }
    }
  }
  return out;
}

function summarizeInput(input: unknown): string {
  if (input === null || input === undefined) return "{}";
  if (typeof input === "string") return truncate(input, 60);
  try {
    return truncate(JSON.stringify(input), 60);
  } catch {
    return String(input);
  }
}

function truncate(s: string, n: number): string {
  if (s.length <= n) return s;
  return `${s.slice(0, n)}…`;
}
