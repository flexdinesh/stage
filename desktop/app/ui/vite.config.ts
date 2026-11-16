import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, loadEnv, type Plugin, type ProxyOptions } from "vite";

// The Go app embeds this build from desktop/app/dist (//go:embed all:dist), which keeps a tracked .gitkeep
// so the package compiles before a build; emptying the directory must not drop it.
function keepDist(): Plugin {
  return {
    name: "keep-dist",
    generateBundle() {
      this.emitFile({ type: "asset", fileName: ".gitkeep", source: "" });
    },
  };
}

// `mise run desktop:dev` exports STAGE_DESKTOP_DEV_SOCKET, the dev server's socket. In dev the page loads
// from this Vite server (MyGo devUrl), so /api goes through this proxy instead of the app's bridge.
function apiProxy(socketPath: string): ProxyOptions {
  return { target: { socketPath, host: "stage", port: 80 } };
}

export default defineConfig(({ mode }) => {
  const socket = loadEnv(mode, ".", "").STAGE_DESKTOP_DEV_SOCKET ?? "";
  return {
    plugins: [react(), tailwindcss(), keepDist()],
    build: { outDir: "../dist", emptyOutDir: true },
    server: {
      // `mise run dev` (the browser host) owns 5173.
      port: 5174,
      strictPort: true,
      host: "localhost",
      ...(socket === "" ? {} : { proxy: { "/api": apiProxy(socket) } }),
    },
  };
});
