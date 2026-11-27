import { QueryClient } from "@tanstack/react-query";
import type { Platform, Subscription, Transport } from "../platform/index.ts";

/** One authenticated view owns all of its requests, streams, query data and private view preferences. */
export function createSharedSession(platform: Platform, onUnauthorized: () => void, recheck: () => void) {
  const abort = new AbortController();
  const streams = new Set<Subscription>();
  const preferences = new Map<string, string>();
  const client = new QueryClient({
    defaultOptions: {
      queries: {
        retry: (count, error) =>
          count < 3 && !(typeof error === "object" && error !== null && "status" in error &&
            (error.status === 401 || error.status === 403)),
      },
    },
  });
  const upstream = platform.transport;
  const transport: Transport = {
    async request(path, init) {
      abort.signal.throwIfAborted();
      const signal = init.signal == null ? abort.signal : AbortSignal.any([abort.signal, init.signal]);
      const response = await upstream.request(path, { ...init, signal, cache: "no-store" });
      abort.signal.throwIfAborted();
      if (response.status === 401 || response.status === 403 && path.startsWith("/admin/")) {
        const error: unknown = await response.clone().json().catch(() => null);
        abort.signal.throwIfAborted();
        if (typeof error === "object" && error !== null && "code" in error) {
          if (error.code === "authentication_required" || error.code === "principal_forbidden") onUnauthorized();
        }
      }
      return response;
    },
    assetUrl: (path) => upstream.assetUrl(path),
    subscribe(path, handlers) {
      if (abort.signal.aborted) return { close() {} };
      const stream = upstream.subscribe(path, {
        ...handlers,
        error(closed) {
          if (closed) recheck();
          handlers.error?.(closed);
        },
      });
      const tracked = {
        close() {
          if (streams.delete(tracked)) stream.close();
        },
      };
      streams.add(tracked);
      return tracked;
    },
  };
  const scoped: Platform = {
    ...platform,
    transport,
    storage: {
      get: (key) => key === "stage-theme" ? platform.storage.get(key) : preferences.get(key) ?? null,
      set(key, value) {
        if (key === "stage-theme") platform.storage.set(key, value);
        else if (abort.signal.aborted || value === null) preferences.delete(key);
        else preferences.set(key, value);
      },
    },
  };
  return {
    platform: scoped,
    client,
    close() {
      abort.abort();
      for (const stream of streams) stream.close();
      preferences.clear();
      client.clear();
      platform.attention.setNeedsInput(0);
      if (platform.sessionLifecycle !== undefined) platform.sessionLifecycle.clearPrivateState();
      else void platform.notifications?.closeShown();
    },
  };
}
