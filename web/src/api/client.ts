// Hand-written wrapper around openapi-fetch that:
//   1. Pulls baseUrl + token from the preload bridge.
//   2. Injects the auth header on every request.
//   3. Exposes a single typed `client` for the rest of the renderer.
//
// Do NOT hand-write any request/response types here. Always import them from
// the generated `./schema`.

import createClient from "openapi-fetch";
import type { paths } from "./schema";

export interface Handshake {
  port: number;
  token: string;
  baseUrl: string;
  proxyPort: number;
  proxyBaseUrl: string;
  proxyEnabled: boolean;
}

export interface Env {
  platform: "darwin" | "win32" | "linux" | "aix" | "freebsd" | "openbsd" | "sunos" | "android" | "cygwin" | "haiku" | "netbsd";
}

export interface AiFoxBridge {
  handshake: () => Promise<Handshake>;
  env: () => Promise<Env>;
  window: {
    minimize: () => Promise<void>;
    maximizeToggle: () => Promise<boolean>;
    close: () => Promise<void>;
    isMaximized: () => Promise<boolean>;
    onMaximizedChanged: (cb: (max: boolean) => void) => () => void;
  };
  theme: {
    native: () => Promise<{ shouldUseDarkColors: boolean }>;
    onNativeChanged: (cb: (dark: boolean) => void) => () => void;
  };
}

declare global {
  interface Window {
    aiFox: AiFoxBridge;
  }
}

const AUTH_HEADER = "X-Ai-fox-Token";

let cachedClient: ReturnType<typeof createClient<paths>> | null = null;
let cachedHandshake: Handshake | null = null;

/**
 * Lazily create the API client on first use. Resolving the handshake is
 * async because the main process spawns the Go backend during app startup;
 * if the renderer loads before the backend is ready, the IPC call waits.
 */
export async function getClient() {
  if (cachedClient) return cachedClient;
  const hs = await getHandshake();
  cachedClient = createClient<paths>({
    baseUrl: hs.baseUrl,
    headers: { [AUTH_HEADER]: hs.token },
  });
  return cachedClient;
}

export async function getHandshake(): Promise<Handshake> {
  if (cachedHandshake) return cachedHandshake;
  cachedHandshake = await window.aiFox.handshake();
  return cachedHandshake;
}


// Re-export the schema types for callers that need them directly.
export type { components, operations, paths } from "./schema";
/** Auth header constant for callers that talk to the backend without openapi-fetch
 *  (notably the SSE EventSource, which has no auth-header API). */
export { AUTH_HEADER };
