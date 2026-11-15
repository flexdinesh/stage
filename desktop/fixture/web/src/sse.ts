// An EventSource over any fetch, for bridge A: native EventSource cannot
// use @mygo-plugins/fetch. It follows the HTML spec's event-stream parsing
// and reconnection: retry field, Last-Event-ID on reconnect, 204 stops,
// close() cancels the request.

export interface StreamEvent {
  readonly event: string;
  readonly id: string;
  readonly data: string;
}

export type FetchLike = (input: string, init: RequestInit) => Promise<Response>;

export interface StreamOptions {
  readonly fetch: FetchLike;
  readonly url: string;
  readonly onEvent: (ev: StreamEvent) => void;
  readonly onOpen?: () => void;
  readonly onError?: (err: unknown) => void;
  /** Reconnection delay until the server sends `retry` (EventSource's default is UA-defined; Stage sends 3000). */
  readonly retryMs?: number;
  readonly lastEventId?: string;
}

export interface EventStream {
  close(): void;
  readonly lastEventId: string;
  readonly connections: number;
}

/** Incremental event-stream parser (HTML §9.2.6). */
export class SSEParser {
  lastEventId = "";
  retry: number | undefined;
  #buffer = "";
  #data = "";
  #event = "";
  #pendingCR = false;

  /** Feeds decoded text; returns the events it completed. */
  push(text: string): StreamEvent[] {
    let s = text;
    if (this.#pendingCR && s.startsWith("\n")) s = s.slice(1);
    this.#pendingCR = false;
    this.#buffer += s;
    const out: StreamEvent[] = [];
    for (;;) {
      const m = /\r\n|\r|\n/.exec(this.#buffer);
      if (!m) break;
      // A trailing \r may be the first half of \r\n split across chunks.
      if (m[0] === "\r" && m.index === this.#buffer.length - 1) {
        this.#pendingCR = true;
      }
      const line = this.#buffer.slice(0, m.index);
      this.#buffer = this.#buffer.slice(m.index + m[0].length);
      const ev = this.#line(line);
      if (ev) out.push(ev);
    }
    return out;
  }

  #line(line: string): StreamEvent | null {
    if (line === "") {
      if (this.#data === "") {
        this.#event = "";
        return null;
      }
      const ev = {
        event: this.#event || "message",
        id: this.lastEventId,
        data: this.#data.endsWith("\n") ? this.#data.slice(0, -1) : this.#data,
      };
      this.#data = "";
      this.#event = "";
      return ev;
    }
    if (line.startsWith(":")) return null;
    const colon = line.indexOf(":");
    const field = colon === -1 ? line : line.slice(0, colon);
    let value = colon === -1 ? "" : line.slice(colon + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    switch (field) {
      case "event":
        this.#event = value;
        break;
      case "data":
        this.#data += value + "\n";
        break;
      case "id":
        if (!value.includes("\0")) this.lastEventId = value;
        break;
      case "retry":
        if (/^\d+$/.test(value)) this.retry = Number(value);
        break;
    }
    return null;
  }
}

/** Opens a stream that reconnects until close(). */
export function openEventStream(opts: StreamOptions): EventStream {
  const ac = new AbortController();
  const state = { lastEventId: opts.lastEventId ?? "", connections: 0 };
  let retry = opts.retryMs ?? 3000;

  const run = async () => {
    while (!ac.signal.aborted) {
      const parser = new SSEParser();
      parser.lastEventId = state.lastEventId;
      try {
        const headers = new Headers({ accept: "text/event-stream", "cache-control": "no-cache" });
        if (state.lastEventId) headers.set("last-event-id", state.lastEventId);
        const res = await opts.fetch(opts.url, { headers, signal: ac.signal });
        if (res.status === 204) return; // the server ends the stream for good
        const type = res.headers.get("content-type") ?? "";
        if (!res.ok || !type.startsWith("text/event-stream") || !res.body) {
          // EventSource fails for good on these; so does this.
          opts.onError?.(new Error(`event stream: ${res.status} ${type}`));
          await res.body?.cancel();
          return;
        }
        state.connections++;
        opts.onOpen?.();
        const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
        try {
          for (;;) {
            const { done, value } = await reader.read();
            if (done) break;
            for (const ev of parser.push(value)) {
              state.lastEventId = parser.lastEventId;
              opts.onEvent(ev);
            }
            if (parser.retry !== undefined) retry = parser.retry;
          }
        } finally {
          reader.releaseLock();
        }
        state.lastEventId = parser.lastEventId;
      } catch (err) {
        if (ac.signal.aborted) return;
        opts.onError?.(err);
      }
      if (parser.retry !== undefined) retry = parser.retry;
      await sleep(retry, ac.signal);
    }
  };
  void run();

  return {
    close: () => ac.abort(),
    get lastEventId() {
      return state.lastEventId;
    },
    get connections() {
      return state.connections;
    },
  };
}

function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const t = setTimeout(done, ms);
    signal.addEventListener("abort", done, { once: true });
    function done() {
      clearTimeout(t);
      signal.removeEventListener("abort", done);
      resolve();
    }
  });
}
