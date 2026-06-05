// Console tab — chronological log derived from captured entries.
// Each entry produces one or two lines (start + finish/error).
// Errors get an err level.
//
// NOTE: Per-tool call detail (tool_use/tool_result rows) and per-request
// analysis data (warnings, model response errors) are no longer available
// in the list — they live in the selected-entry detail view (meta-only list).

import { t } from "../i18n";
import { h } from "./dom";
import { fmtClock, fmtDuration } from "./format";
import { getState, setState, type EntryMeta } from "./state";

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
  const lines = buildLines(state.entries as EntryMeta[]);
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

function buildLines(entries: EntryMeta[]): Line[] {
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

    // Assistant summary line: available via meta fields when hasStructured.
    // Per-tool call detail now lives in the selected-entry detail view.
    if (e.hasStructured) {
      const tokens = `${(e.inputTokens ?? 0) + (e.cacheRead ?? 0) + (e.cacheCreate ?? 0)} in / ${e.outputTokens ?? 0} out`;
      out.push({
        entryId: e.id,
        time: fmtClock(e.startedAt),
        level: "info",
        text: t("bottom.consoleAssistant", {
          model: e.model || "—",
          tokens,
          duration: fmtDuration(e.durationMillis),
        }),
        indent: 1,
      });
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
