import { defineConfig } from "vitest/config";

// Static files only (client contracts §10.1). The browser harness has its own runner (e2e/).
export default defineConfig({
  build: { outDir: "dist", emptyOutDir: true, modulePreload: { polyfill: false } },
  test: { include: ["src/**/*.test.ts"], environment: "node" },
});
