import { defineConfig } from "vitest/config";

// Renderer unit tests cover pure-logic modules only (filtering, formatting,
// SSE wire parsing, diffing). DOM-heavy rendering stays under `task verify`'s
// typecheck umbrella — see docs/PLAN.md §5.
export default defineConfig({
  test: {
    include: ["electron/src/renderer/**/*.test.ts"],
    environment: "node",
  },
});
