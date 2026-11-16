import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode, useEffect, useState } from "react";
import { RouterProvider } from "react-router";
import type { Platform } from "./platform/index.ts";
import { PlatformProvider } from "./platform/context.tsx";
import { router } from "./router.tsx";
import { HostGate } from "./components/Host.tsx";

/** The whole Stage UI. A host renders it with its platform adapters and the app stylesheet (`@stage/app/index.css`). */
export function StageApp({ platform }: { platform: Platform }) {
  const [queryClient] = useState(
    () => new QueryClient({ defaultOptions: { queries: { refetchInterval: 5000, refetchOnWindowFocus: true } } }),
  );
  // Deep links and menu items from a native host open their page.
  useEffect(() => platform.host?.onNavigate((path) => void router.navigate(path)), [platform]);
  return (
    <StrictMode>
      <PlatformProvider platform={platform}>
        <QueryClientProvider client={queryClient}>
          <HostGate>
            <RouterProvider router={router} />
          </HostGate>
        </QueryClientProvider>
      </PlatformProvider>
    </StrictMode>
  );
}
