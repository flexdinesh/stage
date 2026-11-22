import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode, useEffect, useState } from "react";
import { RouterProvider } from "react-router";
import type { Platform } from "./platform/index.ts";
import { PlatformProvider } from "./platform/context.tsx";
import { router } from "./router.tsx";
import { HostGate } from "./components/Host.tsx";
import { AppErrorBoundary } from "./components/ErrorBoundary.tsx";
import { isServerSwitch } from "./lib/host.ts";

/** The whole Stage UI. A host renders it with its platform adapters and the app stylesheet (`@stage/app/index.css`). */
export function StageApp({ platform }: { platform: Platform }) {
  const [queryClient] = useState(
    () => new QueryClient({ defaultOptions: { queries: { refetchInterval: 5000, refetchOnWindowFocus: true } } }),
  );
  // Deep links and menu items from a native host open their page.
  useEffect(() => platform.host?.onNavigate((path) => void router.navigate(path)), [platform]);
  // A server switch: the cached data belongs to the old server, so drop it and open `/`. The host sends the new
  // target before any navigation that caused the switch (a notification click), so that navigation still wins.
  useEffect(() => {
    const connection = platform.host?.connection;
    if (connection === undefined) return;
    let last = connection.current();
    return connection.subscribe((next) => {
      const switched = isServerSwitch(last, next);
      last = next;
      if (!switched) return;
      queryClient.clear();
      void router.navigate("/");
    });
  }, [platform, queryClient]);
  return (
    <StrictMode>
      <AppErrorBoundary>
        <PlatformProvider platform={platform}>
          <QueryClientProvider client={queryClient}>
            <HostGate>
              <RouterProvider router={router} />
            </HostGate>
          </QueryClientProvider>
        </PlatformProvider>
      </AppErrorBoundary>
    </StrictMode>
  );
}
