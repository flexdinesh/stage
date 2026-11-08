import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "react-router";
import "./index.css";
import { router } from "./router.tsx";

const queryClient = new QueryClient({
  defaultOptions: { queries: { refetchInterval: 5000, refetchOnWindowFocus: true } },
});

// Service worker (Web Push only, no caching). Built only for production; /sw.js does not exist in dev.
if (import.meta.env.PROD && "serviceWorker" in navigator) {
  navigator.serviceWorker.register("/sw.js", { scope: "/" }).catch((err: unknown) => {
    console.error("service worker registration failed", err);
  });
}

const root = document.getElementById("root");
if (root === null) throw new Error("#root element missing");

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
