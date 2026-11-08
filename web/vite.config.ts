import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, loadEnv } from "vite";

export default defineConfig(({ mode }) => ({
  plugins: [react(), tailwindcss()],
  build: {
    rolldownOptions: {
      // The service worker is a second entry emitted at a fixed root path (scope "/"). It imports nothing.
      input: { main: "index.html", sw: "src/sw.ts" },
      output: {
        entryFileNames: (chunk) => (chunk.name === "sw" ? "sw.js" : "assets/[name]-[hash].js"),
      },
    },
  },
  server: {
    proxy: {
      // `make dev` exports APP_PORT (default 7390; prod owns 7380).
      "/api": `http://127.0.0.1:${loadEnv(mode, ".", "").APP_PORT ?? "7390"}`,
    },
  },
}));
