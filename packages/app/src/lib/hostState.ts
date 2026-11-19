// Hooks over the host's connection and server list (platform.host). Without a host (the browser) they read as
// "no host": a null state, no servers, and T3 links shown.
import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import { usePlatform } from "../platform/context.tsx";
import type { HostServerEntry, HostState } from "../platform/index.ts";
import { sessionLinks } from "./harnesses.ts";


const unsubscribed = () => {};

/** The host's connection state; null without a host-managed connection. */
export function useHostState(): HostState | null {
  const { host } = usePlatform();
  const subscribe = useCallback(
    (listener: () => void) => (host === undefined ? unsubscribed : host.connection.subscribe(listener)),
    [host],
  );
  const snapshot = useCallback(() => (host === undefined ? null : host.connection.current()), [host]);
  return useSyncExternalStore(subscribe, snapshot);
}

/** Increments each time the host's connection becomes ready again; 0 without a host. Live streams reopen on change. */
export function useHostEpoch(): number {
  return useHostState()?.epoch ?? 0;
}

/** Let the host choose a session destination for the active Stage server; none for a task on a replaced connection. */
export function useSessionDestination() {
  const { links: hostLinks } = usePlatform();
  useHostState();
  return useCallback((links: Parameters<typeof sessionLinks>[0]) => {
    const target = sessionLinks(links);
    return target === null ? null : hostLinks.executionDestination(target);
  }, [hostLinks]);
}

export interface HostServerList {
  /** null until the host answered, and always without server switching. */
  servers: HostServerEntry[] | null;
  error: unknown;
}

/** The saved servers, kept current from the host's change events. */
export function useHostServers(): HostServerList {
  const servers = usePlatform().host?.servers;
  const [list, setList] = useState<HostServerList>({ servers: null, error: null });
  useEffect(() => {
    if (servers === undefined) return;
    let live = true;
    // Events first, then the snapshot, so no change is missed in between; a stale snapshot never wins.
    let seen = false;
    const off = servers.subscribe((next) => {
      seen = true;
      setList({ servers: next, error: null });
    });
    servers.list().then(
      (next) => {
        if (live && !seen) setList({ servers: next, error: null });
      },
      (error: unknown) => {
        if (live && !seen) setList({ servers: null, error });
      },
    );
    return () => {
      live = false;
      off();
    };
  }, [servers]);
  return list;
}
