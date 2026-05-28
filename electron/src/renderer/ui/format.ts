// Small formatting helpers shared between sidebar, details, and status bar.

export function fmtBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return "—";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(2)} MB`;
}

export function fmtDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "—";
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
}

export function fmtTime(iso: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return `${d.toLocaleTimeString(undefined, { hour12: false })}.${String(d.getMilliseconds()).padStart(3, "0")}`;
}

export function fmtClock(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

export function isPending(entry: { endedAt?: string; statusCode?: number }): boolean {
  const ended = entry.endedAt ? new Date(entry.endedAt).getTime() : 0;
  return !ended && !entry.statusCode;
}

export function statusKind(entry: {
  endedAt?: string;
  statusCode?: number;
  error?: string;
  streaming?: boolean;
}): "ok" | "err" | "pending" | "streaming" {
  if (entry.error) return "err";
  if (isPending(entry)) return "pending";
  if (entry.streaming) return "streaming";
  const s = entry.statusCode ?? 0;
  if (s >= 400 || s === 0) return "err";
  return "ok";
}
