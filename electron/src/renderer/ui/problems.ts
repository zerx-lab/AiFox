// Problems tab — aggregates anything actionable across all entries:
// proxy errors, 4xx/5xx responses, failing tool_results, parser warnings.
// Click a row to jump to the offending entry.

import type { components } from "../../api/client";
import { t } from "../i18n";
import { h } from "./dom";
import { fmtClock } from "./format";
import { getState, setState, type TrafficEntry } from "./state";

type Analysis = components["schemas"]["Analysis"];

interface Problem {
  entryId: string;
  time: string;
  where: string;
  message: string;
  level: "warn" | "err";
}

export function renderProblems(): HTMLElement {
  const state = getState();
  const problems = collectProblems(state.entries);
  if (problems.length === 0) {
    return h("div.detail-empty", null, t("bottom.problemsNone"));
  }
  const wrap = h("div.console-log");
  for (const p of problems) {
    wrap.appendChild(
      h(
        "div.console-line",
        {
          onclick: () => setState({ selectedId: p.entryId }),
        },
        h("span.ctime", null, p.time),
        h(
          `span.clvl.${p.level}`,
          null,
          p.level === "err" ? "✕" : "⚠",
        ),
        h(
          "span.ctext",
          null,
          h(
            `span.problem-where${p.level === "err" ? ".err" : ".warn"}`,
            null,
            p.where,
          ),
          " ",
          p.message,
        ),
      ),
    );
  }
  return wrap;
}

function collectProblems(entries: TrafficEntry[]): Problem[] {
  const out: Problem[] = [];
  // chronological (oldest first) so the user reads top-down
  const chrono = [...entries].reverse();
  for (const e of chrono) {
    if (e.error) {
      out.push({
        entryId: e.id,
        time: fmtClock(e.endedAt || e.startedAt),
        where: `${e.id} · ${e.method} ${e.url}`,
        message: e.error,
        level: "err",
      });
    } else if (e.statusCode > 0 && e.statusCode >= 400) {
      out.push({
        entryId: e.id,
        time: fmtClock(e.endedAt || e.startedAt),
        where: `${e.id} · ${e.statusCode}`,
        message: t("bottom.problemHttpErr", { url: e.url || "/" }),
        level: e.statusCode >= 500 ? "err" : "warn",
      });
    }

    const analysis = e.analysis as Analysis | undefined;
    if (analysis?.anthropic?.response?.error) {
      const err = analysis.anthropic.response.error;
      out.push({
        entryId: e.id,
        time: fmtClock(e.endedAt || e.startedAt),
        where: `${e.id} · ${err.type || "error"}`,
        message: err.message ?? "",
        level: "err",
      });
    }

    // Failing tool_results
    for (const m of analysis?.anthropic?.request?.messages ?? []) {
      for (const blk of m.content ?? []) {
        if (blk.type === "tool_result" && blk.isError === true) {
          out.push({
            entryId: e.id,
            time: fmtClock(e.startedAt),
            where: `${e.id} · tool ${blk.toolUseId ?? "?"}`,
            message: t("bottom.problemToolErr"),
            level: "err",
          });
        }
      }
    }

    if ((analysis?.warnings?.length ?? 0) > 0) {
      for (const w of analysis?.warnings ?? []) {
        out.push({
          entryId: e.id,
          time: fmtClock(e.startedAt),
          where: `${e.id} · parser`,
          message: w,
          level: "warn",
        });
      }
    }
  }
  return out;
}
