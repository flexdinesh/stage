import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode, useEffect, useState } from "react";
import { RouterProvider } from "react-router";
import type { Platform } from "./platform/index.ts";
import { PlatformProvider, usePlatform } from "./platform/context.tsx";
import { router } from "./router.tsx";
import { HostGate } from "./components/Host.tsx";
import { AppErrorBoundary } from "./components/ErrorBoundary.tsx";
import { hostAccessScope, isServerSwitch } from "./lib/host.ts";
import { useHostState } from "./lib/hostState.ts";

/** The whole Stage UI. A host renders it with its platform adapters and the app stylesheet (`@stage/app/index.css`). */
export function StageApp({ platform }: { platform: Platform }) {
  // Deep links and menu items from a native host open their page.
  useEffect(() => platform.host?.onNavigate((path) => void router.navigate(path)), [platform]);
  return (
    <StrictMode>
      <AppErrorBoundary>
        <PlatformProvider platform={platform}>
          <AppTarget />
        </PlatformProvider>
      </AppErrorBoundary>
    </StrictMode>
  );
}

function AppTarget() {
  return <AppQueryScope key={hostAccessScope(useHostState())} />;
}

function AppQueryScope() {
  const platform = usePlatform();
  const [queryClient] = useState(
    // No default poll: live hooks in api/queries.ts declare their interval (lib/polling.ts).
    () => new QueryClient({ defaultOptions: { queries: { refetchOnWindowFocus: true } } }),
  );
  // The host sends the new target before any navigation that caused the switch, so that navigation still wins.
  useEffect(() => {
    const connection = platform.host?.connection;
    if (connection === undefined) return;
    let last = connection.current();
    return connection.subscribe((next) => {
      const switched = isServerSwitch(last, next);
      const accessChanged = last.server !== null && next.server !== null && last.server.sharedAccess !== next.server.sharedAccess;
      last = next;
      if (accessChanged) queryClient.clear();
      if (switched) void router.navigate("/");
    });
  }, [platform, queryClient]);
  useEffect(() => () => queryClient.clear(), [queryClient]);
  return (
    <QueryClientProvider client={queryClient}>
      <HostGate>
        <RouterProvider router={router} />
      </HostGate>
    </QueryClientProvider>
  );
}
