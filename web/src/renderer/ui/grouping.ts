// Grouping & filtering helpers shared by sidebar / statusbar / filter pill.
// Single source of truth so a future "session" grouping (Milestone E) only
// needs to swap groupKey() without rewriting the callers.
//
// These helpers operate on EntryMeta (the lightweight list projection).
// Deep per-entry analysis fields (bodies, tool calls, etc.) are only
// available on the selected full entry via selectedFull() in detail panes.

import type { EntryMeta, TrafficFilter } from "./state";

export function groupKey(entry: EntryMeta): string {
  return entry.endpoint || `${entry.method} ${entry.url || "/"}`;
}

export function applyFilter(
  entries: EntryMeta[],
  filter: TrafficFilter,
): EntryMeta[] {
  const text = filter.text.trim().toLowerCase();
  return entries.filter((e) => {
    if (text) {
      const hay = `${e.method} ${e.url} ${e.statusCode} ${e.model ?? ""}`.toLowerCase();
      if (!hay.includes(text)) return false;
    }
    if (filter.streaming && !e.streaming) return false;
    if (filter.errors) {
      const isErr = !!e.error || (e.statusCode > 0 && e.statusCode >= 400);
      if (!isErr) return false;
    }
    if (filter.model) {
      const m = e.model || "";
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

export function aggregateUsage(entries: EntryMeta[]): AggregateTotals {
  const out: AggregateTotals = {
    entries: entries.length,
    input: 0,
    output: 0,
    cacheRead: 0,
    cacheCreate: 0,
  };
  for (const e of entries) {
    out.input += e.inputTokens ?? 0;
    out.output += e.outputTokens ?? 0;
    out.cacheRead += e.cacheRead ?? 0;
    out.cacheCreate += e.cacheCreate ?? 0;
  }
  return out;
}

/** Collect distinct model labels across entries (sorted). */
export function distinctModels(entries: EntryMeta[]): string[] {
  const seen = new Set<string>();
  for (const e of entries) {
    const m = e.model;
    if (m) seen.add(m);
  }
  return Array.from(seen).sort();
}
