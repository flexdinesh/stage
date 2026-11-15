import assert from "node:assert/strict";
import { test } from "node:test";

import { openEventStream, SSEParser, type StreamEvent } from "./sse.ts";

test("parses Stage's transcript framing across chunk boundaries", () => {
  const p = new SSEParser();
  const wire = 'retry: 3000\n\nevent: item\nid: 0\ndata: {"cursor":0}\n\n: ping\n\nevent: state\nid: 1\ndata: {"a":1}\n\nevent: reset\ndata: {}\n\n';
  const events: StreamEvent[] = [];
  for (let i = 0; i < wire.length; i += 3) events.push(...p.push(wire.slice(i, i + 3)));
  assert.equal(p.retry, 3000);
  assert.deepEqual(events, [
    { event: "item", id: "0", data: '{"cursor":0}' },
    { event: "state", id: "1", data: '{"a":1}' },
    { event: "reset", id: "1", data: "{}" },
  ]);
});

test("handles CRLF split between chunks, multi-line data and defaults", () => {
  const p = new SSEParser();
  const events = [...p.push("data: a\r"), ...p.push("\ndata:b\r\n\r\n"), ...p.push("id\n\n"), ...p.push("data\n\n")];
  assert.deepEqual(events, [
    { event: "message", id: "", data: "a\nb" },
    { event: "message", id: "", data: "" },
  ]);
});

test("ignores ids with NUL and non-numeric retry", () => {
  const p = new SSEParser();
  p.push("id: 5\nretry: x\n\nid: a\0b\ndata: z\n\n");
  assert.equal(p.lastEventId, "5");
  assert.equal(p.retry, undefined);
});

/** A fetch whose responses are SSE bodies the test controls. */
function fakeServer() {
  const requests: Headers[] = [];
  const controllers: ReadableStreamDefaultController<Uint8Array>[] = [];
  const enc = new TextEncoder();
  const fetch = (_url: string, init: RequestInit): Promise<Response> => {
    requests.push(new Headers(init.headers));
    const body = new ReadableStream<Uint8Array>({
      start(c) {
        controllers.push(c);
        init.signal?.addEventListener("abort", () => {
          try {
            c.error(new DOMException("aborted", "AbortError"));
          } catch {
            // already closed
          }
        });
      },
    });
    return Promise.resolve(new Response(body, { headers: { "content-type": "text/event-stream" } }));
  };
  const send = (i: number, text: string) => controllers[i]?.enqueue(enc.encode(text));
  const end = (i: number) => controllers[i]?.close();
  return { fetch, requests, send, end, controllers };
}

async function until(cond: () => boolean) {
  for (let i = 0; i < 200 && !cond(); i++) await new Promise((r) => setTimeout(r, 5));
  assert.ok(cond(), "timed out");
}

test("reconnects after the server drops, resuming with Last-Event-ID", async () => {
  const srv = fakeServer();
  const got: string[] = [];
  const s = openEventStream({ fetch: srv.fetch, url: "/s", onEvent: (e) => got.push(e.id) });
  await until(() => srv.controllers.length === 1);
  srv.send(0, "retry: 10\n\nevent: item\nid: 0\ndata: x\n\nevent: item\nid: 1\ndata: y\n\n");
  await until(() => got.length === 2);
  srv.end(0);
  await until(() => srv.controllers.length === 2);
  assert.equal(srv.requests[0]?.get("last-event-id"), null);
  assert.equal(srv.requests[1]?.get("last-event-id"), "1");
  srv.send(1, "event: item\nid: 2\ndata: z\n\n");
  await until(() => got.length === 3);
  assert.deepEqual(got, ["0", "1", "2"]);
  assert.equal(s.connections, 2);
  s.close();
});

test("close() aborts the request and stops reconnecting", async () => {
  const srv = fakeServer();
  const s = openEventStream({ fetch: srv.fetch, url: "/s", onEvent: () => {}, retryMs: 5 });
  await until(() => srv.controllers.length === 1);
  s.close();
  await new Promise((r) => setTimeout(r, 50));
  assert.equal(srv.controllers.length, 1);
});
