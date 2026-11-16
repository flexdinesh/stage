import type { HostServers, HostServices, HostState } from "@stage/app/platform";
import { INITIAL_STATE, type DesktopNative, type NativeServers } from "./native.ts";

/** Server switching over the shell's server calls and its servers:changed event. */
export function desktopServers(native: NativeServers): HostServers {
  return {
    list: () => native.servers(),
    add: (input) => native.addServer(input),
    update: (id, input) => native.updateServer(id, input),
    remove: (id) => native.removeServer(id),
    switch: (id) => native.switchServer(id),
    test: (url) => native.testServer(url),
    subscribe: (listener) => native.onServers(listener),
  };
}

/**
 * The connection state store over the shell's state events, plus login startup, navigation and (when the shell
 * has them) server switching.
 */
export function desktopHost(native: DesktopNative, servers?: NativeServers): HostServices & { current(): HostState } {
  let state = INITIAL_STATE;
  const listeners = new Set<(s: HostState) => void>();
  const set = (next: HostState) => {
    state = next;
    for (const l of listeners) l(next);
  };
  // Events first, then the snapshot, so no change is missed in between; a stale snapshot never wins.
  let seen = false;
  native.onState((s) => {
    seen = true;
    set(s);
  });
  void native.state().then((s) => {
    if (!seen) set(s);
  });

  return {
    current: () => state,
    connection: {
      current: () => state,
      subscribe(listener) {
        listeners.add(listener);
        return () => listeners.delete(listener);
      },
      retry: () => native.retry(),
      startServer: () => native.startServer(),
      restartServer: () => native.restartServer(),
      diagnostics: () => native.diagnostics(),
    },
    loginStartup: {
      get: () => native.loginStartup(),
      set: (enabled) => native.setLoginStartup(enabled),
    },
    ...(servers === undefined ? {} : { servers: desktopServers(servers) }),
    onNavigate(listener) {
      const off = native.onNavigate(listener);
      void native.takeNavigation().then((path) => {
        if (path !== "") listener(path);
      });
      return off;
    },
  };
}
