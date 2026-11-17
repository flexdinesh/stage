import { createContext, useContext, useMemo, type ReactNode } from "react";
import { createApi, type Api } from "../api/client.ts";
import type { Platform } from "./index.ts";

interface Services {
  platform: Platform;
  api: Api;
}

const ServicesContext = createContext<Services | null>(null);

/** Supplies the host platform, and the API client over its transport, to the app. */
export function PlatformProvider({ platform, children }: { platform: Platform; children: ReactNode }) {
  const services = useMemo(() => ({ platform, api: createApi(platform.transport) }), [platform]);
  return <ServicesContext.Provider value={services}>{children}</ServicesContext.Provider>;
}

function useServices(): Services {
  const services = useContext(ServicesContext);
  if (services === null) throw new Error("PlatformProvider missing: render the app through StageApp");
  return services;
}

export const usePlatform = (): Platform => useServices().platform;

export const useApi = (): Api => useServices().api;
