// Problems tab — aggregates actionable issues from the entry list.
// Covers proxy errors, 4xx/5xx HTTP responses, streamed (HTTP-200) upstream
// API errors, and parser warnings — all from the lightweight EntryMeta
// projection (hasResponseError / warningCount are server-derived flags). Click
// a row to jump to the offending entry, where the full message is shown.
//
// NOTE: Per-tool-result errors and individual warning/error messages are not in
// the list view; the row points at the entry and the selected-entry detail view
// shows the specifics.

import { t } from "../i18n";
import { h } from "./dom";
import { fmtClock } from "./format";
import { getState, setState, type EntryMeta } from "./state";

interface Problem {
  entryId: string;
  time: string;
  where: string;
  message: string;
  level: "warn" | "err";
}

export function renderProblems(): HTMLElement {
  const state = getState();
  const problems = collectProblems(state.entries as EntryMeta[]);
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

function collectProblems(entries: EntryMeta[]): Problem[] {
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
    } else if (e.hasResponseError) {
      // Upstream/API error returned in the response body (e.g. an Anthropic
      // error envelope on a streamed HTTP 200) — invisible to e.error/status.
      out.push({
        entryId: e.id,
        time: fmtClock(e.endedAt || e.startedAt),
        where: `${e.id} · API`,
        message: t("bottom.problemApiErr"),
        level: "err",
      });
    }

    if (e.warningCount > 0) {
      out.push({
        entryId: e.id,
        time: fmtClock(e.startedAt),
        where: `${e.id} · parser`,
        message: t("bottom.problemWarnings", { count: e.warningCount }),
        level: "warn",
      });
    }
  }
  return out;
}
