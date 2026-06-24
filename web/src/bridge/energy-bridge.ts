// Energy(CEF) bridge: implements `window.aiFox` on top of Energy's injected
// global `ipc` object, so the renderer's 7 bridge call sites stay unchanged.
//
// Wire model (single-direction, scaffold-verified): Go pushes data with
// ipc.Emit -> handled here by ipc.on; the renderer issues commands with
// ipc.emit (no return value relied upon — window state is reflected back
// through the "window:maximized-changed" event instead).
//
// This module must be imported first in renderer.ts so the ipc.on listeners
// are registered before Go's first ipc.Emit (which fires on CEF OnLoadEnd,
// after deferred module scripts have run).

import type { AiFoxBridge, Env, Handshake } from "../api/client";

// Energy injects `ipc` as a global. Fall back to window.ipc just in case.
declare const ipc:
  | {
      on(name: string, cb: (...args: unknown[]) => void): void;
      emit(name: string, ...args: unknown[]): void;
    }
  | undefined;

const bus =
  typeof ipc !== "undefined"
    ? ipc
    : (window as unknown as { ipc: NonNullable<typeof ipc> }).ipc;

// A one-shot value cache: callers awaiting before the value arrives are
// resolved when it does; callers after get it immediately.
function awaiter<T>() {
  let value: T | null = null;
  const waiters: ((v: T) => void)[] = [];
  return {
    set(v: T) {
      value = v;
      const pending = waiters.splice(0);
      for (const w of pending) w(v);
    },
    get(): Promise<T> {
      return value !== null
        ? Promise.resolve(value)
        : new Promise<T>((resolve) => waiters.push(resolve));
    },
  };
}

// Energy may deliver composite payloads as objects or as JSON strings;
// normalize defensively.
function asObject<T>(v: unknown): T {
  return (typeof v === "string" ? JSON.parse(v) : v) as T;
}

const hsA = awaiter<Handshake>();
bus.on("app:handshake", (h: unknown) => hsA.set(asObject<Handshake>(h)));

const envA = awaiter<Env>();
bus.on("app:env", (p: unknown) =>
  envA.set({ platform: String(p) as Env["platform"] }),
);

let lastMax = false;
const maxSubs = new Set<(m: boolean) => void>();
bus.on("window:maximized-changed", (m: unknown) => {
  lastMax = m === true || m === "true";
  for (const cb of maxSubs) cb(lastMax);
});

const bridge: AiFoxBridge = {
  handshake: () => hsA.get(),
  env: () => envA.get(),
  window: {
    minimize: () => {
      bus.emit("window:minimize");
      return Promise.resolve();
    },
    maximizeToggle: () => {
      bus.emit("window:maximize-toggle");
      return Promise.resolve(lastMax);
    },
    close: () => {
      bus.emit("window:close");
      return Promise.resolve();
    },
    isMaximized: () => Promise.resolve(lastMax),
    onMaximizedChanged: (cb: (max: boolean) => void) => {
      maxSubs.add(cb);
      return () => {
        maxSubs.delete(cb);
      };
    },
  },
  theme: {
    // CEF/Chromium follows the OS prefers-color-scheme; no reliable Go-side
    // OS theme API, so resolve in the renderer.
    native: () =>
      Promise.resolve({
        shouldUseDarkColors:
          window.matchMedia?.("(prefers-color-scheme: dark)").matches ?? true,
      }),
    onNativeChanged: (cb: (dark: boolean) => void) => {
      const mq = window.matchMedia?.("(prefers-color-scheme: dark)");
      const h = (e: MediaQueryListEvent) => cb(e.matches);
      mq?.addEventListener("change", h);
      return () => mq?.removeEventListener("change", h);
    },
  },
};

window.aiFox = bridge;
