import { resolve } from "node:path";
import { defineConfig } from "vite";

// Standalone renderer build (no Electron/Forge). Outputs the static bundle
// into ../resources/app, which main.go embeds and assetserve serves at
// http://127.0.0.1:22022/app/index.html.
//
// base "./" makes index.html reference assets relatively so they resolve
// correctly under the /app/ sub-path.
export default defineConfig({
  root: resolve(__dirname, "src/renderer"),
  base: "./",
  build: {
    outDir: resolve(__dirname, "../resources/app"),
    emptyOutDir: true,
  },
});
