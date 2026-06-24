import { defineConfig } from "vitest/config";

// Renderer unit tests cover pure-logic modules only (filtering, formatting,
// SSE wire parsing, diffing). DOM-heavy rendering stays under `task verify`'s
// typecheck umbrella.
export default defineConfig({
  test: {
    include: ["src/renderer/**/*.test.ts"],
    environment: "node",
  },
});
