// Unified API service layer (§4.3.2). Single choke point for the renderer's
// backend calls so error handling lives in ONE place instead of every component
// open-coding getClient() + a silent try/catch. Failures surface through the
// global toast channel (§4.1.7) — the calls that used to swallow errors
// (fetchEntry, session rename, settings refresh, breakpoint mutations) now tell
// the user when something went wrong.
//
// Each function returns a discriminated result ({ ok, data } | { ok:false })
// so callers can branch on success without re-implementing error plumbing.
// Request/response types still come exclusively from the generated schema via
// getClient — this layer adds no hand-written wire types.

import { getClient } from "../../api/client";
import { t } from "../i18n";
import type {
  Breakpoint,
  ProxyInfo,
  Settings,
  TrafficEntry,
} from "./state";
import { toastError } from "./toast";

export type Result<T> = { ok: true; data: T } | { ok: false };

const FAIL: Result<never> = { ok: false };

// detail extracts a human-ish message from an openapi-fetch error body or a
// thrown exception.
function detail(error: unknown): string {
  if (error && typeof error === "object" && "detail" in error) {
    const d = (error as { detail?: unknown }).detail;
    if (typeof d === "string" && d) return d;
  }
  return String(error ?? "unknown");
}

// run wraps a client call: it catches thrown exceptions, inspects the
// openapi-fetch { data, error } envelope, toasts on failure, and returns a
// typed Result. `context` is an i18n key prefix used to build the toast.
async function run<T>(
  contextKey: string,
  fn: (client: Awaited<ReturnType<typeof getClient>>) => Promise<{
    data?: T;
    error?: unknown;
  }>,
): Promise<Result<T>> {
  try {
    const client = await getClient();
    const { data, error } = await fn(client);
    if (error || data === undefined) {
      toastError(t("toast.failed", { what: t(contextKey), error: detail(error) }));
      return FAIL;
    }
    return { ok: true, data };
  } catch (e) {
    toastError(t("toast.failed", { what: t(contextKey), error: detail(e) }));
    return FAIL;
  }
}

// runVoid is like run but for 204 / no-body endpoints where success is "no
// error" rather than "has data".
async function runVoid(
  contextKey: string,
  fn: (client: Awaited<ReturnType<typeof getClient>>) => Promise<{ error?: unknown }>,
): Promise<boolean> {
  try {
    const client = await getClient();
    const { error } = await fn(client);
    if (error) {
      toastError(t("toast.failed", { what: t(contextKey), error: detail(error) }));
      return false;
    }
    return true;
  } catch (e) {
    toastError(t("toast.failed", { what: t(contextKey), error: detail(e) }));
    return false;
  }
}

// ---- traffic ----

export function fetchEntry(id: string): Promise<Result<TrafficEntry>> {
  return run("toast.ctx.fetchEntry", (c) =>
    c.GET("/v1/traffic/{id}", { params: { path: { id } } }),
  );
}

export interface TailResult {
  appendBytes: string;
  responseSize: number;
  done: boolean;
}

// fetchTail is polled on a hot path and tolerates transient/404 failures
// (evicted entries) by design, so it stays SILENT — the selection controller
// already bounds retries. Returns null on any failure.
export async function fetchTail(id: string, since: number): Promise<TailResult | null> {
  try {
    const client = await getClient();
    const resp = await client.GET("/v1/traffic/{id}/tail", {
      params: { path: { id }, query: { since } },
    });
    return resp.data ?? null;
  } catch {
    return null;
  }
}

export function clearTraffic(): Promise<boolean> {
  return runVoid("toast.ctx.clearTraffic", (c) => c.DELETE("/v1/traffic", {}));
}

// ---- settings ----

export function getSettings(): Promise<Result<Settings>> {
  return run("toast.ctx.loadSettings", (c) => c.GET("/v1/settings", {}));
}

export function putSettings(body: Settings): Promise<Result<Settings>> {
  return run("toast.ctx.saveSettings", (c) => c.PUT("/v1/settings", { body }));
}

// putLayout persists ONLY the panel geometry via the dedicated /v1/settings/
// layout endpoint, which read-modify-writes just the layout fields server-side
// so a resize-drag save never clobbers an in-progress settings edit.
export function putLayout(body: {
  colLeft: number;
  colRight: number;
  bottomHeight: number;
}): Promise<Result<Settings>> {
  return run("toast.ctx.saveLayout", (c) =>
    c.PUT("/v1/settings/layout", { body }),
  );
}

// ---- proxy ----

export function getProxyInfo(): Promise<Result<ProxyInfo>> {
  return run("toast.ctx.proxyInfo", (c) => c.GET("/v1/proxy", {}));
}

export function setProxyEnabled(enabled: boolean): Promise<Result<ProxyInfo>> {
  return run("toast.ctx.proxyToggle", (c) =>
    c.PUT("/v1/proxy", { body: { enabled } }),
  );
}

// ---- sessions ----

export function renameSession(id: string, name: string): Promise<boolean> {
  return runVoid("toast.ctx.renameSession", (c) =>
    c.PATCH("/v1/sessions/{id}", { params: { path: { id } }, body: { name } }),
  );
}

// ---- breakpoints ----

export function addBreakpoint(body: {
  match: "endpoint" | "path";
  pattern: string;
  enabled: boolean;
}): Promise<Result<Breakpoint>> {
  return run("toast.ctx.addBreakpoint", (c) => c.POST("/v1/breakpoints", { body }));
}

export function updateBreakpoint(id: string, enabled: boolean): Promise<boolean> {
  return runVoid("toast.ctx.updateBreakpoint", (c) =>
    c.PUT("/v1/breakpoints/{id}", { params: { path: { id } }, body: { enabled } }),
  );
}

export function deleteBreakpoint(id: string): Promise<boolean> {
  return runVoid("toast.ctx.deleteBreakpoint", (c) =>
    c.DELETE("/v1/breakpoints/{id}", { params: { path: { id } } }),
  );
}

// togglePathBreakpoint creates a path breakpoint for `path`, or deletes the
// existing one if it already matches (used by the timeline rail gutter so the
// user can set/clear a breakpoint on an entry's endpoint with one click). The
// caller passes the current breakpoint list so we don't re-fetch.
export async function togglePathBreakpoint(
  path: string,
  existing: Breakpoint[],
): Promise<boolean> {
  const match = existing.find((b) => b.match === "path" && b.pattern === path);
  if (match) return deleteBreakpoint(match.id);
  const res = await addBreakpoint({ match: "path", pattern: path, enabled: true });
  return res.ok;
}

export function continuePaused(entryId: string): Promise<boolean> {
  return runVoid("toast.ctx.continuePaused", (c) =>
    c.POST("/v1/breakpoints/paused/{entryId}/continue", {
      params: { path: { entryId } },
    }),
  );
}

export function abortPaused(entryId: string): Promise<boolean> {
  return runVoid("toast.ctx.abortPaused", (c) =>
    c.POST("/v1/breakpoints/paused/{entryId}/abort", {
      params: { path: { entryId } },
    }),
  );
}

// ---- replay ----

export interface ReplayOverrides {
  model?: string;
  temperature?: number;
  topP?: number;
  maxTokens?: number;
}

export function replayEntry(
  id: string,
  overrides: ReplayOverrides,
): Promise<Result<{ entryId: string }>> {
  return run("toast.ctx.replay", (c) =>
    c.POST("/v1/traffic/{id}/replay", {
      params: { path: { id } },
      body: { overrides },
    }),
  );
}
