import { defineConfig } from "vite";
import { resolve } from "node:path";

// Renderer (browser) build.
//
// Two non-obvious knobs interact with Forge's plugin-vite defaults:
//
// 1. `root` is set to the renderer source folder so Vite finds index.html
//    directly. Forge defaults `root` to the project root; we override it.
// 2. Forge also sets `build.outDir` to the relative path `.vite/renderer/<name>`,
//    which Vite resolves against `root`. After (1) that would land in
//    `electron/src/renderer/.vite/...` and the asar packager — which collects
//    from the project root `.vite/` — would ship without the renderer bundle.
//    Symptom: "Not allowed to load local resource" pointing at
//    app.asar/.vite/renderer/main_window/index.html (file genuinely missing
//    in asar). Pin outDir to an ABSOLUTE path so the override sticks.
export default defineConfig({
  root: resolve(__dirname, "electron/src/renderer"),
  build: {
    outDir: resolve(__dirname, ".vite/renderer/main_window"),
    emptyOutDir: true,
  },
});
