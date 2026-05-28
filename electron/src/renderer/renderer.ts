// Renderer entry. Bootstraps platform / theme, pulls initial settings + proxy
// info, subscribes to the SSE traffic stream, then mounts the app.

import { getClient } from "../api/client";
import { setLanguage } from "./i18n";
import { mountApp } from "./ui/app";
import { openSse } from "./ui/sse";
import {
  type Breakpoint,
  type PausedRequest,
  replaceBreakpoints,
  replaceEntries,
  replaceSessions,
  setState,
  upsertEntry,
  type SessionSummary,
  type TrafficEntry,
} from "./ui/state";
import { initTheme, setTheme, type ThemeChoice } from "./ui/theme";

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
  }
  if (proxyResp.data) setState({ proxy: proxyResp.data });

  mountApp(root);

  // The SSE stream sends a "snapshot" of the current buffer on connect, then
  // "entry" events for every new or updated capture. We don't need a separate
  // initial GET /v1/traffic because of that.
  void openSse("/v1/traffic/stream", (ev) => {
    if (ev.event === "snapshot") {
      const items = JSON.parse(ev.data) as TrafficEntry[];
      replaceEntries(items);
    } else if (ev.event === "entry") {
      const entry = JSON.parse(ev.data) as TrafficEntry;
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
  });
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
