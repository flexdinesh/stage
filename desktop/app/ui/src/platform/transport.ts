import type { HostState, StreamHandlers, Subscription, Transport } from "@stage/app/platform";

/** Minimal EventSource surface the transport uses (the webview's, or a test fake). */
export interface EventSourceLike {
  readonly readyState: number;
  addEventListener(type: string, listener: (e: Event) => void): void;
  close(): void;
}

export interface TransportDeps {
  fetch: (input: string, init: RequestInit) => Promise<Response>;
  eventSource: (url: string) => EventSourceLike;
}

/** EventSource.CLOSED: the webview gave up (the server refused the stream). */
const CLOSED = 2;

const apiUrl = (path: string) => `/api${path}`;

/**
 * Relative `/api` on the page's own scheme (mygo://localhost): the app's bridge forwards it to the Stage
 * server's Unix socket (docs/adr/0001-desktop-shell.md, bridge B), so fetch, EventSource and `<img>` work as in
 * a browser. A dropped server aborts streams as network errors, so EventSource keeps retrying.
 */
export function desktopTransport(
  deps: TransportDeps = {
    fetch: (input, init) => fetch(input, init),
    eventSource: (url) => new EventSource(url),
  },
  current?: () => HostState,
): Transport {
  return {
    request: (path, init) => deps.fetch(apiUrl(path), init),
    assetUrl(path) {
      const state = current?.();
      if (state === undefined) return apiUrl(path);
      const query = new URLSearchParams({ "stage-target": `${state.target.id}|${state.target.url}|${state.accountEpoch ?? 0}` });
      return `${apiUrl(path)}?${query.toString()}`;
    },
    subscribe(path: string, handlers: StreamHandlers): Subscription {
      const es = deps.eventSource(apiUrl(path));
      let closed = false;
      es.addEventListener("open", () => {
        if (!closed) handlers.open?.();
      });
      es.addEventListener("error", () => {
        if (!closed) handlers.error?.(es.readyState === CLOSED);
      });
      for (const [name, handle] of Object.entries(handlers.events)) {
        es.addEventListener(name, (e) => {
          if (closed || !(e instanceof MessageEvent) || typeof e.data !== "string") return;
          handle(e.data);
        });
      }
      return {
        close() {
          closed = true;
          es.close();
        },
      };
    },
  };
}
