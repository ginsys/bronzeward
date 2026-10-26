import { defineConfig } from "vitest/config";

// Static files only (client contracts §10.1). The browser harness has its own runner (e2e/).
export default defineConfig({
  // The footer's client version (client contracts §7 item 5): BW_VERSION at build time, else as the
  // server names a build without one.
  define: { __BW_VERSION__: JSON.stringify(process.env["BW_VERSION"] ?? "(devel)") },
  build: { outDir: "dist", emptyOutDir: true, modulePreload: { polyfill: false } },
  test: { include: ["src/**/*.test.ts"], environment: "node" },
});
