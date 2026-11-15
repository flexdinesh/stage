// The two candidate bridges, as the probe's checks see them. Bridge B
// (scheme) is what the real Stage UI does today: relative fetch, native
// EventSource, <img src>. Bridge A (fetch) is what it would have to become.

import { fetch as pluginFetch } from "@mygo-plugins/fetch";

import { openEventStream } from "./sse.ts";

export type BridgeName = "scheme" | "fetch";

export interface StreamHandlers {
  onOpen(): void;
  onItem(id: string, data: string): void;
  onReset(): void;
  onError(err: unknown): void;
}

export interface Stream {
  close(): void;
  /** Connections opened so far (reconnects count). */
  connections(): number;
}

export interface Transport {
  readonly name: BridgeName;
  /** API request to a /api/... path. */
  request(path: string, init?: RequestInit): Promise<Response>;
  /** An <img> src for an attachment. */
  imageSrc(id: number): Promise<string>;
  /** The transcript stream at a /api/... path. */
  stream(path: string, h: StreamHandlers): Stream;
}

/** The synthetic origin of bridge A (bridge.Origin in Go). */
export const SYNTHETIC_ORIGIN = "http://stage.invalid";

export const schemeTransport: Transport = {
  name: "scheme",
  request: (path, init) => fetch(path, init),
  imageSrc: (id) => Promise.resolve(`/api/attachments/${id}`),
  stream(path, h) {
    const es = new EventSource(path);
    let connections = 0;
    es.addEventListener("open", () => {
      connections++;
      h.onOpen();
    });
    es.addEventListener("error", (ev) => h.onError(ev));
    es.addEventListener("item", (ev) => h.onItem(ev.lastEventId, ev.data));
    es.addEventListener("reset", () => {
      es.close();
      h.onReset();
    });
    return { close: () => es.close(), connections: () => connections };
  },
};

export const fetchTransport: Transport = {
  name: "fetch",
  request: (path, init) => pluginFetch(SYNTHETIC_ORIGIN + path, init),
  async imageSrc(id) {
    const res = await pluginFetch(`${SYNTHETIC_ORIGIN}/api/attachments/${id}`);
    if (!res.ok) throw new Error(`attachment ${id}: ${res.status}`);
    return URL.createObjectURL(await res.blob());
  },
  stream(path, h) {
    let closed = false;
    const s = openEventStream({
      fetch: (url, init) => pluginFetch(url, init),
      url: SYNTHETIC_ORIGIN + path,
      onOpen: () => h.onOpen(),
      onError: (err) => h.onError(err),
      onEvent: (ev) => {
        if (closed) return;
        if (ev.event === "item") h.onItem(ev.id, ev.data);
        if (ev.event === "reset") {
          closed = true;
          s.close();
          h.onReset();
        }
      },
    });
    return {
      close: () => {
        closed = true;
        s.close();
      },
      connections: () => s.connections,
    };
  },
};

export function transportFor(name: BridgeName): Transport {
  return name === "fetch" ? fetchTransport : schemeTransport;
}

export { pluginFetch };
