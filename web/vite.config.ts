import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, loadEnv, type ProxyOptions } from "vite";

// `mise run dev` exports STAGE_LISTEN (default 127.0.0.1:7390; prod
// defaults to 7380): host:port or unix:/abs/path.sock.
function apiProxy(listen: string): ProxyOptions {
  const socketPath = listen.startsWith("unix:") ? listen.slice("unix:".length).replace(/^\/\/(?=\/)/, "") : "";
  if (socketPath) return { target: { socketPath, host: "stage", port: 80 } };
  const i = listen.lastIndexOf(":");
  const host = listen.slice(0, i);
  const wildcard = host === "" || host === "0.0.0.0" || host === "[::]";
  return { target: `http://${wildcard ? "127.0.0.1" : host}:${listen.slice(i + 1)}` };
}

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
      "/api": apiProxy(loadEnv(mode, ".", "").STAGE_LISTEN || "127.0.0.1:7390"),
    },
  },
}));
