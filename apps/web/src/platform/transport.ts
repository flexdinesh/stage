import type { StreamHandlers, Subscription, Transport } from "@stage/app/platform";

/** Minimal EventSource surface the transport uses (the browser's, or a test fake). */
export interface EventSourceLike {
  readonly readyState: number;
  addEventListener(type: string, listener: (e: Event) => void): void;
  close(): void;
}

export interface BrowserTransportDeps {
  fetch: (input: string, init: RequestInit) => Promise<Response>;
  eventSource: (url: string) => EventSourceLike;
}

/** EventSource.CLOSED: the browser gave up (the server refused the stream). */
const CLOSED = 2;

const apiUrl = (path: string) => `/api${path}`;

/** Same-origin `/api` over the page's own connection: relative fetch, native EventSource and plain asset URLs. */
export function browserTransport(
  deps: BrowserTransportDeps = {
    fetch: (input, init) => fetch(input, init),
    eventSource: (url) => new EventSource(url),
  },
): Transport {
  return {
    request: (path, init) => deps.fetch(apiUrl(path), init),
    assetUrl: apiUrl,
    subscribe(path: string, handlers: StreamHandlers): Subscription {
      const es = deps.eventSource(apiUrl(path));
      let closed = false;
      // EventSource retries by itself (sending Last-Event-ID) unless the server refused the stream.
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
