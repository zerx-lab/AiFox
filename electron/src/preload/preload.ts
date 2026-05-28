import { contextBridge, ipcRenderer } from "electron";

// Surface a minimal, typed API to the renderer. Keep this surface small;
// every method exposed here is a privilege escalation channel.
//
// Bridge name is `aiFox` (camelCase) because `window.ai-fox` parses as
// subtraction in JS.

type Handshake = {
  port: number;
  token: string;
  baseUrl: string;
  proxyPort: number;
  proxyBaseUrl: string;
  proxyEnabled: boolean;
};

type Env = {
  platform: NodeJS.Platform;
};

const api = {
  /** Fetch the backend handshake (baseUrl + auth token) from the main process. */
  handshake: () => ipcRenderer.invoke("ai-fox:handshake") as Promise<Handshake>,
  /** Platform / packaging info, used by the renderer to adapt the titlebar. */
  env: () => ipcRenderer.invoke("ai-fox:env") as Promise<Env>,
  window: {
    minimize: () => ipcRenderer.invoke("ai-fox:window:minimize") as Promise<void>,
    maximizeToggle: () =>
      ipcRenderer.invoke("ai-fox:window:maximize-toggle") as Promise<boolean>,
    close: () => ipcRenderer.invoke("ai-fox:window:close") as Promise<void>,
    isMaximized: () =>
      ipcRenderer.invoke("ai-fox:window:is-maximized") as Promise<boolean>,
    onMaximizedChanged: (cb: (max: boolean) => void) => {
      const listener = (_: unknown, max: boolean) => cb(max);
      ipcRenderer.on("ai-fox:window:maximized-changed", listener);
      return () =>
        ipcRenderer.removeListener("ai-fox:window:maximized-changed", listener);
    },
  },
  theme: {
    native: () =>
      ipcRenderer.invoke("ai-fox:theme:native") as Promise<{
        shouldUseDarkColors: boolean;
      }>,
    onNativeChanged: (cb: (dark: boolean) => void) => {
      const listener = (_: unknown, payload: { shouldUseDarkColors: boolean }) =>
        cb(payload.shouldUseDarkColors);
      ipcRenderer.on("ai-fox:theme:changed", listener);
      return () => ipcRenderer.removeListener("ai-fox:theme:changed", listener);
    },
  },
};

contextBridge.exposeInMainWorld("aiFox", api);

// Re-exported as a type for the renderer to import.
export type AiFoxBridge = typeof api;
