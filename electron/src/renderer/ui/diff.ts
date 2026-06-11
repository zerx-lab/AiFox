// Replay diff view (§4.1.5). The core value of a replay is comparing the
// original request/response against the re-issued one. The Diff tab pairs the
// currently-selected (replayed) entry with the entry it was replayed from and
// renders a line-level diff of the response bodies (request bodies if the
// response is empty), +/- coloured, monospaced, with large bodies truncated.
//
// The original entry's full body isn't in the list projection (EntryMeta), so
// we fetch it once via the service layer and cache it module-side; the fetch
// completion nudges the detail region to re-render via a no-op state touch.

import { t } from "../i18n";
import { fetchEntry } from "./api-service";
import { h } from "./dom";
import {
  getState,
  replayOriginOf,
  setState,
  type TrafficEntry,
} from "./state";

// Cap on bytes diffed per side — diffing megabyte bodies is pointless and slow.
const MAX_DIFF_BYTES = 200_000;
// LCS is O(n·m); cap the line count so a pathological body can't hang the UI.
const MAX_DIFF_LINES = 4000;

// Cache of fetched original entries keyed by id, plus the set of ids currently
// in flight so we don't refetch on every re-render while one is pending.
const originalCache = new Map<string, TrafficEntry>();
const pending = new Set<string>();

export function renderDiff(entry: TrafficEntry): HTMLElement {
  const originId = replayOriginOf(entry.id);
  if (!originId) {
    return h("div.diff-empty", null, t("diff.noOrigin"));
  }

  const original = originalCache.get(originId);
  if (!original) {
    ensureFetched(originId);
    return h("div.diff-empty", null, t("diff.loading"));
  }

  // Prefer response bodies; fall back to request bodies when there's no
  // response yet on either side (e.g. an errored replay).
  const useResponse = !!(original.responseBody || entry.responseBody);
  const oldText = useResponse ? original.responseBody ?? "" : original.requestBody ?? "";
  const newText = useResponse ? entry.responseBody ?? "" : entry.requestBody ?? "";

  const wrap = h("div.diff-wrap");
  wrap.appendChild(
    h(
      "div.diff-head",
      null,
      h("span", null, t("diff.original", { id: originId })),
      h("span.vs", null, t("diff.vs")),
      h("span", null, t("diff.replay", { id: entry.id })),
    ),
  );

  const { oldClip, newClip, truncated } = clip(oldText, newText);
  const rows = diffLines(oldClip.split("\n"), newClip.split("\n"));
  const diff = h("div.diff");
  for (const r of rows) {
    const sign = r.kind === "add" ? "+" : r.kind === "del" ? "−" : " ";
    diff.appendChild(
      h(
        `div.diff-row.${r.kind}`,
        null,
        h("span.sgn", null, sign),
        h("span.txt", null, r.text || " "),
      ),
    );
  }
  wrap.appendChild(diff);
  if (truncated) wrap.appendChild(h("div.diff-trunc", null, t("diff.truncated")));
  return wrap;
}

function ensureFetched(id: string) {
  if (originalCache.has(id) || pending.has(id)) return;
  pending.add(id);
  void fetchEntry(id).then((res) => {
    pending.delete(id);
    if (res.ok) {
      originalCache.set(id, res.data);
      // Nudge the detail region to re-render now that the original is available.
      // An empty patch on detailTab keeps the current tab but bumps `detail`.
      setState({ detailTab: getState().detailTab });
    }
  });
}

interface ClipResult {
  oldClip: string;
  newClip: string;
  truncated: boolean;
}

function clip(oldText: string, newText: string): ClipResult {
  let truncated = false;
  let o = oldText;
  let n = newText;
  if (o.length > MAX_DIFF_BYTES) {
    o = o.slice(0, MAX_DIFF_BYTES);
    truncated = true;
  }
  if (n.length > MAX_DIFF_BYTES) {
    n = n.slice(0, MAX_DIFF_BYTES);
    truncated = true;
  }
  return { oldClip: o, newClip: n, truncated };
}

interface DiffRow {
  kind: "add" | "del" | "ctx";
  text: string;
}

// diffLines computes a line-level diff via a standard LCS table backtrace.
// Lines are capped (MAX_DIFF_LINES) so the O(n·m) table stays bounded; beyond
// the cap we degrade to a flat del-all/add-all so the view still renders.
export function diffLines(a: string[], b: string[]): DiffRow[] {
  if (a.length > MAX_DIFF_LINES || b.length > MAX_DIFF_LINES) {
    return [
      ...a.map((text): DiffRow => ({ kind: "del", text })),
      ...b.map((text): DiffRow => ({ kind: "add", text })),
    ];
  }
  const n = a.length;
  const m = b.length;
  // lcs[i][j] = LCS length of a[i:] and b[j:]. Rows are pre-filled so every
  // index access below is in-bounds; `!` documents that to the compiler.
  const lcs: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--) {
    const row = lcs[i]!;
    const rowNext = lcs[i + 1]!;
    for (let j = m - 1; j >= 0; j--) {
      row[j] = a[i] === b[j] ? rowNext[j + 1]! + 1 : Math.max(rowNext[j]!, row[j + 1]!);
    }
  }
  const out: DiffRow[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      out.push({ kind: "ctx", text: a[i]! });
      i++;
      j++;
    } else if (lcs[i + 1]![j]! >= lcs[i]![j + 1]!) {
      out.push({ kind: "del", text: a[i]! });
      i++;
    } else {
      out.push({ kind: "add", text: b[j]! });
      j++;
    }
  }
  while (i < n) out.push({ kind: "del", text: a[i++]! });
  while (j < m) out.push({ kind: "add", text: b[j++]! });
  return out;
}
