// Hooks over the host's connection and server list (platform.host). Without a host (the browser) they read as
// "no host": a null state, no servers, and T3 links shown.
import { useCallback, useSyncExternalStore } from "react";
import { usePlatform } from "../platform/context.tsx";
import type { HostServerEntry, HostServers, HostState } from "../platform/index.ts";
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

const NO_SERVERS: HostServerList = { servers: null, error: null };

/** One subscription per host server list, shared by every `useHostServers` caller. */
interface ServersStore {
  snapshot: HostServerList;
  listeners: Set<() => void>;
  stop: (() => void) | null;
}

const serverStores = new WeakMap<HostServers, ServersStore>();

function serversStore(servers: HostServers): ServersStore {
  let store = serverStores.get(servers);
  if (store === undefined) {
    store = { snapshot: NO_SERVERS, listeners: new Set(), stop: null };
    serverStores.set(servers, store);
  }
  return store;
}

function publish(store: ServersStore, snapshot: HostServerList) {
  store.snapshot = snapshot;
  store.listeners.forEach((listener) => listener());
}

/** The first listener starts the host subscription and one `list()`; the last one stops it. The last list stays as the snapshot. */
function subscribeServers(servers: HostServers, listener: () => void): () => void {
  const store = serversStore(servers);
  store.listeners.add(listener);
  if (store.stop === null) {
    let live = true;
    // Events first, then the snapshot, so no change is missed in between; a stale snapshot never wins.
    let seen = false;
    const off = servers.subscribe((next) => {
      seen = true;
      publish(store, { servers: next, error: null });
    });
    servers.list().then(
      (next) => {
        if (live && !seen) publish(store, { servers: next, error: null });
      },
      (error: unknown) => {
        if (live && !seen) publish(store, { servers: null, error });
      },
    );
    store.stop = () => {
      live = false;
      off();
    };
  }
  return () => {
    store.listeners.delete(listener);
    if (store.listeners.size > 0) return;
    store.stop?.();
    store.stop = null;
  };
}

/** The saved servers, kept current from the host's change events. Every caller shares one subscription. */
export function useHostServers(): HostServerList {
  const servers = usePlatform().host?.servers;
  const subscribe = useCallback(
    (listener: () => void) => (servers === undefined ? unsubscribed : subscribeServers(servers, listener)),
    [servers],
  );
  const snapshot = useCallback(() => (servers === undefined ? NO_SERVERS : serversStore(servers).snapshot), [servers]);
  return useSyncExternalStore(subscribe, snapshot);
}
