// Renderer entry. Bootstraps platform / theme, pulls initial settings + proxy
// info, subscribes to the SSE traffic stream, then mounts the app.

import { getClient } from "../api/client";
import { setLanguage } from "./i18n";
import { mountApp } from "./ui/app";
import { applyPersistedLayout } from "./ui/layout-persist";
import { initSelection } from "./ui/selection";
import { openSse } from "./ui/sse";
import {
  type Breakpoint,
  type EntryMeta,
  type PausedRequest,
  replaceBreakpoints,
  replaceEntries,
  replaceSessions,
  type SessionSummary,
  setState,
  upsertEntry,
} from "./ui/state";
import { initTheme, setTheme, type ThemeChoice } from "./ui/theme";

// SSE reconnection constants (exponential backoff).
const SSE_BACKOFF_INITIAL_MS = 1_000;
const SSE_BACKOFF_MAX_MS = 30_000;

async function bootstrap() {
  const root = document.getElementById("root");
  if (!root) throw new Error("missing #root");

  // Environment first so the titlebar can render with the right variant on
  // the first paint (avoid a flash of "wrong-OS" buttons).
  try {
    const env = await window.aiFox.env();
    setState({ env });
  } catch {
    // dev/headless fallback — assume linux so the custom buttons show.
    setState({ env: { platform: "linux" } });
  }

  // Wire the OS-theme listener before we know the user's choice; setTheme
  // is called again once settings are loaded.
  await initTheme();

  // Maximized state mirror so the titlebar restore/maximize icon stays in sync.
  try {
    const maxed = await window.aiFox.window.isMaximized();
    setState({ windowMaximized: maxed });
  } catch {
    /* ignore */
  }
  window.aiFox.window.onMaximizedChanged((max) => {
    setState({ windowMaximized: max });
  });

  const client = await getClient();
  const [settingsResp, proxyResp] = await Promise.all([
    client.GET("/v1/settings", {}),
    client.GET("/v1/proxy", {}),
  ]);

  if (settingsResp.data) {
    setState({ settings: settingsResp.data });
    setLanguage((settingsResp.data.language ?? "") as "" | "en" | "zh-CN");
    setTheme((settingsResp.data.theme ?? "") as ThemeChoice);
    // Restore the user's persisted panel geometry before the first mount so the
    // window opens at the shape they left it (no resize flash).
    applyPersistedLayout(settingsResp.data);
  }
  if (proxyResp.data) setState({ proxy: proxyResp.data });

  mountApp(root);
  // Keep selectedEntry (full body) in sync with selectedId, fetching on demand.
  initSelection();

  // The SSE stream sends a "snapshot" of the current buffer on connect, then
  // "entry" events for every new or updated capture (lightweight EntryMeta —
  // bodies are fetched per entry on selection). No separate initial GET needed.
  //
  // subscribeTrafficStream wraps openSse with exponential-backoff reconnection.
  // On reconnect the server sends a fresh "snapshot" event that naturally
  // reconciles client state, so no additional recovery logic is needed here.
  let sseBackoffMs = SSE_BACKOFF_INITIAL_MS;
  let sseTimer: ReturnType<typeof setTimeout> | null = null;
  let sseStopped = false;

  function handleSseEvent(ev: { event: string; data: string }) {
    if (ev.event === "snapshot") {
      // Back online — clear reconnecting indicator.
      setState({ connection: "live" });
      sseBackoffMs = SSE_BACKOFF_INITIAL_MS;
      const items = JSON.parse(ev.data) as EntryMeta[];
      replaceEntries(items);
    } else if (ev.event === "entry") {
      const entry = JSON.parse(ev.data) as EntryMeta;
      upsertEntry(entry);
    } else if (ev.event === "sessions") {
      const items = JSON.parse(ev.data) as SessionSummary[];
      replaceSessions(items);
    } else if (ev.event === "breakpoints") {
      const payload = JSON.parse(ev.data) as {
        items: Breakpoint[];
        paused: PausedRequest[];
      };
      replaceBreakpoints(payload.items ?? [], payload.paused ?? []);
    }
  }

  function scheduleReconnect() {
    if (sseStopped) return;
    setState({ connection: "reconnecting" });
    sseTimer = setTimeout(() => {
      sseTimer = null;
      void connectSse();
    }, sseBackoffMs);
    // Double backoff, cap at max.
    sseBackoffMs = Math.min(sseBackoffMs * 2, SSE_BACKOFF_MAX_MS);
  }

  async function connectSse() {
    if (sseStopped) return;
    await openSse("/v1/traffic/stream", handleSseEvent, scheduleReconnect);
  }

  void connectSse();

  // Expose a cleanup hook for HMR / test teardown (dev only, best-effort).
  if (typeof window !== "undefined") {
    (window as unknown as Record<string, unknown>).__aifox_stopSse = () => {
      sseStopped = true;
      if (sseTimer !== null) clearTimeout(sseTimer);
    };
  }
}

bootstrap().catch((err) => {
  const root = document.getElementById("root");
  if (!root) return;
  root.innerHTML = "";
  const pre = document.createElement("pre");
  pre.style.color = "crimson";
  pre.style.padding = "1rem";
  pre.textContent = `startup failed: ${String(err)}`;
  root.appendChild(pre);
});
