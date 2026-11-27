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
  onSessionChanged: () => void;
}

/** EventSource.CLOSED: the browser gave up (the server refused the stream). */
const CLOSED = 2;

const apiUrl = (path: string) => `/api${path}`;

const SESSION_CHANGES = new Set(["/auth/login", "/auth/setup", "/auth/invite", "/auth/reset", "/auth/logout", "/account/password"]);

async function authenticationRequired(response: Response): Promise<boolean> {
  const error: unknown = await response.clone().json().catch(() => null);
  return typeof error === "object" && error !== null && "code" in error && error.code === "authentication_required";
}

/** Same-origin `/api` over the page's own connection: relative fetch, native EventSource and plain asset URLs. */
export function browserTransport({
  fetch: request = (input, init) => fetch(input, init),
  eventSource = (url) => new EventSource(url),
  onSessionChanged,
}: Partial<BrowserTransportDeps> = {}): Transport {
  let sessionGeneration = 0;
  return {
    async request(path, init) {
      const generation = sessionGeneration;
      const response = await request(apiUrl(path), init);
      if (onSessionChanged === undefined) return response;
      if (init.method === "POST" && response.ok && SESSION_CHANGES.has(path)) {
        sessionGeneration++;
        onSessionChanged();
      } else if (response.status === 401 && await authenticationRequired(response) && generation === sessionGeneration) {
        onSessionChanged();
      }
      return response;
    },
    assetUrl: apiUrl,
    subscribe(path: string, handlers: StreamHandlers): Subscription {
      const es = eventSource(apiUrl(path));
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
