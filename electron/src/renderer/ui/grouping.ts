// Grouping & filtering helpers shared by sidebar / statusbar / filter pill.
// Single source of truth so a future "session" grouping (Milestone E) only
// needs to swap groupKey() without rewriting the callers.

import type { components } from "../../api/client";
import type { TrafficEntry, TrafficFilter } from "./state";

type Analysis = components["schemas"]["Analysis"];
type AnthropicUsage = components["schemas"]["AnthropicUsage"];

export function groupKey(entry: TrafficEntry): string {
  const a = entry.analysis as Analysis | undefined;
  return a?.endpoint || `${entry.method} ${entry.url || "/"}`;
}

export function applyFilter(
  entries: TrafficEntry[],
  filter: TrafficFilter,
): TrafficEntry[] {
  const text = filter.text.trim().toLowerCase();
  return entries.filter((e) => {
    if (text) {
      const hay = `${e.method} ${e.url} ${e.statusCode}`.toLowerCase();
      if (!hay.includes(text)) return false;
    }
    if (filter.streaming && !e.streaming) return false;
    if (filter.errors) {
      const isErr = !!e.error || (e.statusCode > 0 && e.statusCode >= 400);
      if (!isErr) return false;
    }
    if (filter.model) {
      const m =
        (e.analysis as Analysis | undefined)?.anthropic?.request?.model ||
        (e.analysis as Analysis | undefined)?.anthropic?.response?.model ||
        "";
      if (!m.includes(filter.model)) return false;
    }
    return true;
  });
}

export interface AggregateTotals {
  entries: number;
  input: number;
  output: number;
  cacheRead: number;
  cacheCreate: number;
}

export function aggregateUsage(entries: TrafficEntry[]): AggregateTotals {
  const out: AggregateTotals = {
    entries: entries.length,
    input: 0,
    output: 0,
    cacheRead: 0,
    cacheCreate: 0,
  };
  for (const e of entries) {
    const u = (e.analysis as Analysis | undefined)?.anthropic?.response?.usage as
      | AnthropicUsage
      | undefined;
    if (!u) continue;
    out.input += u.inputTokens ?? 0;
    out.output += u.outputTokens ?? 0;
    out.cacheRead += u.cacheReadInputTokens ?? 0;
    out.cacheCreate += u.cacheCreationInputTokens ?? 0;
  }
  return out;
}

/** Collect distinct model labels across entries (sorted). */
export function distinctModels(entries: TrafficEntry[]): string[] {
  const seen = new Set<string>();
  for (const e of entries) {
    const a = e.analysis as Analysis | undefined;
    const m = a?.anthropic?.request?.model || a?.anthropic?.response?.model;
    if (m) seen.add(m);
  }
  return Array.from(seen).sort();
}
