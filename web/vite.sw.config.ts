import { defineConfig } from "vite";

// The service worker: one self-contained classic script at dist/sw.js (the
// site root, so its scope is "/"), built after the app into the same dist.
export default defineConfig({
  publicDir: false,
  build: {
    outDir: "dist",
    emptyOutDir: false,
    lib: { entry: "src/offline/sw.ts", formats: ["iife"], name: "larkSw", fileName: () => "sw.js" },
  },
});
