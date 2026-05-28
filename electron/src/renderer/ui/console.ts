// Console tab — chronological log derived from captured entries.
// Each entry produces one or two lines (start + finish); tool calls produce
// indented children. Errors get an err level.

import type { components } from "../../api/client";
import { t } from "../i18n";
import { h } from "./dom";
import { fmtClock, fmtDuration } from "./format";
import { getState, setState, type TrafficEntry } from "./state";

type Analysis = components["schemas"]["Analysis"];

type Level = "info" | "warn" | "err";

interface Line {
  time: string;
  level: Level;
  text: string;
  entryId: string;
  indent?: number;
}

export function renderConsole(): HTMLElement {
  const state = getState();
  const lines = buildLines(state.entries);
  const wrap = h("div.console-log");
  if (lines.length === 0) {
    wrap.appendChild(h("div.detail-empty", null, t("bottom.consoleEmpty")));
    return wrap;
  }
  for (const ln of lines) {
    wrap.appendChild(
      h(
        "div.console-line",
        {
          onclick: () => setState({ selectedId: ln.entryId }),
        },
        h("span.ctime", null, ln.time),
        h(
          `span.clvl.${ln.level}`,
          null,
          ln.level === "info" ? "ℹ" : ln.level === "warn" ? "⚠" : "✕",
        ),
        h(
          "span.ctext",
          { style: ln.indent ? `padding-left: ${ln.indent * 16}px` : undefined },
          ln.text,
        ),
      ),
    );
  }
  return wrap;
}

function buildLines(entries: TrafficEntry[]): Line[] {
  const out: Line[] = [];
  // newest first in entries; reverse to get a chronological feed.
  const chrono = [...entries].reverse();
  for (const e of chrono) {
    out.push({
      entryId: e.id,
      time: fmtClock(e.startedAt),
      level: "info",
      text: t("bottom.consoleStart", {
        method: e.method,
        url: e.url || "/",
      }),
    });

    const analysis = e.analysis as Analysis | undefined;
    if (analysis?.anthropic?.response) {
      const u = analysis.anthropic.response.usage;
      const tokens = u
        ? `${(u.inputTokens ?? 0) + (u.cacheReadInputTokens ?? 0) + (u.cacheCreationInputTokens ?? 0)} in / ${u.outputTokens ?? 0} out`
        : "no usage";
      out.push({
        entryId: e.id,
        time: fmtClock(e.startedAt),
        level: "info",
        text: t("bottom.consoleAssistant", {
          model: analysis.anthropic.response.model || analysis.anthropic.request?.model || "—",
          tokens,
          duration: fmtDuration(e.durationMillis),
        }),
        indent: 1,
      });
    }

    // tool_use blocks
    if (analysis?.anthropic) {
      const tools: Array<{ name: string; id: string; ok: boolean | null }> = [];
      const results = new Map<string, boolean>();
      for (const m of analysis.anthropic.request?.messages ?? []) {
        for (const blk of m.content ?? []) {
          if (blk.type === "tool_result" && blk.toolUseId) {
            results.set(blk.toolUseId, blk.isError === true);
          }
        }
      }
      for (const m of analysis.anthropic.request?.messages ?? []) {
        for (const blk of m.content ?? []) {
          if (blk.type === "tool_use" && blk.id) {
            const err = results.get(blk.id);
            tools.push({
              name: blk.name ?? "?",
              id: blk.id,
              ok: err === undefined ? null : !err,
            });
          }
        }
      }
      for (const blk of analysis.anthropic.response?.content ?? []) {
        if (blk.type === "tool_use" && blk.id) {
          tools.push({
            name: blk.name ?? "?",
            id: blk.id,
            ok: null,
          });
        }
      }
      for (const tc of tools) {
        const lvl: Level = tc.ok === false ? "err" : "info";
        out.push({
          entryId: e.id,
          time: fmtClock(e.startedAt),
          level: lvl,
          text: t("bottom.consoleTool", {
            name: tc.name,
            status: tc.ok === null ? "pending" : tc.ok ? "ok" : "fail",
          }),
          indent: 2,
        });
      }
    }

    if (e.error) {
      out.push({
        entryId: e.id,
        time: fmtClock(e.endedAt || e.startedAt),
        level: "err",
        text: e.error,
        indent: 1,
      });
    } else if (e.statusCode > 0 && e.statusCode >= 400) {
      out.push({
        entryId: e.id,
        time: fmtClock(e.endedAt || e.startedAt),
        level: "warn",
        text: t("bottom.consoleHttpErr", { code: String(e.statusCode) }),
        indent: 1,
      });
    }
  }
  return out;
}
