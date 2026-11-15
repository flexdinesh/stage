// The probe's check suite: the same checks over either bridge, against
// the Stage contract stub (desktop/fixture/stub) on the Unix socket.

import { pluginFetch, type Stream, type Transport } from "./transport.ts";

export interface Result {
  readonly name: string;
  readonly ok: boolean;
  readonly ms: number;
  readonly detail: string;
}

interface Check {
  readonly name: string;
  readonly only?: Transport["name"];
  run(t: Transport, ctx: Context): Promise<string>;
}

interface Context {
  attachmentId: number | null;
  readonly gallery: HTMLElement;
}

const STREAM_TIMEOUT = 8000; // Stage's retry is 3000 ms

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function field(v: unknown, key: string): unknown {
  return isRecord(v) ? v[key] : undefined;
}

function num(v: unknown, key: string): number {
  const x = field(v, key);
  if (typeof x !== "number") throw new Error(`expected number ${key} in ${JSON.stringify(v)}`);
  return x;
}

function str(v: unknown, key: string): string {
  const x = field(v, key);
  if (typeof x !== "string") throw new Error(`expected string ${key} in ${JSON.stringify(v)}`);
  return x;
}

async function json(res: Response, status: number): Promise<unknown> {
  const text = await res.text();
  if (res.status !== status) throw new Error(`status ${res.status}, want ${status}: ${text}`);
  const v: unknown = text ? JSON.parse(text) : null;
  return v;
}

function post(t: Transport, path: string, body: unknown): Promise<Response> {
  return t.request(path, {
    method: "POST",
    headers: { "content-type": "application/json", accept: "application/json" },
    body: JSON.stringify(body),
  });
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

async function until(what: string, cond: () => boolean | Promise<boolean>, timeout = 3000): Promise<void> {
  const deadline = Date.now() + timeout;
  while (!(await cond())) {
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await sleep(25);
  }
}

async function counts(t: Transport): Promise<{ streams: number; slow: number }> {
  const v = await json(await t.request("/api/fixture/streams"), 200);
  return { streams: num(v, "streams"), slow: num(v, "slow") };
}

async function rejects(p: Promise<unknown>): Promise<string> {
  try {
    await p;
  } catch (err) {
    return err instanceof Error ? err.message : String(err);
  }
  throw new Error("request was not refused");
}

/** A PNG of w×h pixels drawn on a canvas. */
function makePng(w: number, h: number): Promise<Blob> {
  const c = document.createElement("canvas");
  c.width = w;
  c.height = h;
  const g = c.getContext("2d");
  if (!g) throw new Error("no 2d canvas");
  g.fillStyle = "#1f2328";
  g.fillRect(0, 0, w, h);
  return new Promise((resolve, reject) => c.toBlob((b) => (b ? resolve(b) : reject(new Error("toBlob failed"))), "image/png"));
}

function upload(t: Transport, blob: Blob, name: string): Promise<Response> {
  const form = new FormData();
  form.append("file", blob, name);
  return t.request("/api/uploads", { method: "POST", body: form });
}

class Collector {
  readonly ids: string[] = [];
  readonly items: { id: string; at: number; received: number }[] = [];
  opened = 0;
  reset = false;
  readonly stream: Stream;

  constructor(t: Transport, thread: string) {
    this.stream = t.stream(`/api/threads/${encodeURIComponent(thread)}/transcript/stream`, {
      onOpen: () => {
        this.opened++;
      },
      onItem: (id, data) => {
        this.ids.push(id);
        this.items.push({ id, at: num(JSON.parse(data), "at"), received: Date.now() });
      },
      onReset: () => {
        this.reset = true;
      },
      onError: () => {},
    });
  }
}

function collect(t: Transport, thread: string): Collector {
  return new Collector(t, thread);
}

function emit(t: Transport, thread: string, count: number): Promise<Response> {
  return post(t, `/api/fixture/threads/${encodeURIComponent(thread)}/emit`, { count });
}

function uniqueThread(prefix: string): string {
  return `${prefix}-${Math.random().toString(36).slice(2, 8)}`;
}

export const checks: readonly Check[] = [
  {
    name: "json-crud",
    async run(t) {
      const name = uniqueThread("snippet");
      const created = await json(await post(t, "/api/snippets", { name, body: "hello" }), 201);
      const id = num(created, "id");
      const list = await json(await t.request("/api/snippets"), 200);
      const all = field(list, "snippets");
      if (!Array.isArray(all) || !all.some((s) => field(s, "id") === id)) throw new Error("created snippet not listed");
      const patched = await json(
        await t.request(`/api/snippets/${id}`, { method: "PATCH", headers: { "content-type": "application/json" }, body: JSON.stringify({ body: "édité ✓" }) }),
        200,
      );
      if (str(patched, "body") !== "édité ✓") throw new Error("PATCH lost UTF-8");
      const del = await t.request(`/api/snippets/${id}`, { method: "DELETE" });
      if (del.status !== 204) throw new Error(`DELETE ${del.status}`);
      const gone = await json(await t.request(`/api/snippets/${id}`), 404);
      return `create/list/patch/delete ok; 404 body ${JSON.stringify(gone)}`;
    },
  },
  {
    name: "upload-image",
    async run(t, ctx) {
      const png = await makePng(4, 3);
      const a = await json(await upload(t, png, "probe.png"), 201);
      if (str(a, "mimeType") !== "image/png" || num(a, "sizeBytes") !== png.size) throw new Error(`bad attachment ${JSON.stringify(a)}`);
      ctx.attachmentId = num(a, "id");
      const notImage = await json(await upload(t, new Blob(["not an image"]), "x.png"), 400);
      if (!str(notImage, "error").includes("unsupported image type")) throw new Error("non-image accepted");
      const big = new Uint8Array(10 * 1024 * 1024 + 1);
      big.set([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
      const tooBig = await json(await upload(t, new Blob([big], { type: "image/png" }), "big.png"), 400);
      if (!str(tooBig, "error").includes("larger than 10 MB")) throw new Error("oversize accepted");
      return `201 ${png.size} B png; 400 non-image; 400 for 10 MB + 1`;
    },
  },
  {
    name: "image-render",
    async run(t, ctx) {
      if (ctx.attachmentId === null) throw new Error("needs upload-image");
      const src = await t.imageSrc(ctx.attachmentId);
      const img = new Image();
      img.alt = "uploaded probe image";
      const loaded = new Promise<void>((resolve, reject) => {
        img.addEventListener("load", () => resolve(), { once: true });
        img.addEventListener("error", () => reject(new Error(`<img> failed for ${src}`)), { once: true });
      });
      img.src = src;
      ctx.gallery.append(img);
      await loaded;
      if (img.naturalWidth !== 4 || img.naturalHeight !== 3) throw new Error(`decoded ${img.naturalWidth}x${img.naturalHeight}`);
      return `<img src=${src.slice(0, 40)}> decoded 4x3`;
    },
  },
  {
    name: "download",
    async run(t) {
      const res = await t.request("/api/fixture/download");
      const disposition = res.headers.get("content-disposition") ?? "";
      const text = await res.text();
      if (!disposition.includes('filename="stage-fixture.txt"')) throw new Error(`Content-Disposition ${disposition}`);
      if (text !== "stage fixture download\n") throw new Error(`body ${JSON.stringify(text)}`);
      return `bytes + Content-Disposition readable; saving to disk is a manual check`;
    },
  },
  {
    name: "routing",
    async run() {
      const before = location.pathname + location.search;
      history.pushState({}, "", "/tasks/42");
      const deep = location.pathname;
      history.replaceState({}, "", before);
      // A reload of a deep link asks the scheme for the path: it must get
      // index.html (SPA fallback), not a 404.
      // location.href, not .origin: a custom scheme may report an opaque origin.
      const res = await fetch(new URL("/tasks/42", location.href));
      const html = await res.text();
      if (deep !== "/tasks/42" || res.status !== 200 || !html.includes('id="probe"')) {
        throw new Error(`deep link ${deep}: ${res.status}`);
      }
      return `${new URL("/tasks/42", location.href).href} serves index.html`;
    },
  },
  {
    name: "sse-live",
    async run(t) {
      const id = uniqueThread("live");
      const c = collect(t, id);
      try {
        await until("stream open", () => c.opened > 0, STREAM_TIMEOUT);
        await emit(t, id, 5);
        await until("5 items", () => c.ids.length >= 5);
        if (c.ids.join() !== "0,1,2,3,4") throw new Error(`ids ${c.ids.join()}`);
        const worst = Math.max(...c.items.map((i) => i.received - i.at));
        if (worst > 1000) throw new Error(`event latency ${worst} ms`);
        return `5 events in order; worst latency ${worst} ms`;
      } finally {
        c.stream.close();
      }
    },
  },
  {
    name: "sse-reconnect",
    async run(t) {
      const id = uniqueThread("reconnect");
      const c = collect(t, id);
      try {
        await until("stream open", () => c.opened > 0, STREAM_TIMEOUT);
        await emit(t, id, 2);
        await until("2 items", () => c.ids.length >= 2);
        await post(t, `/api/fixture/threads/${id}/drop`, {});
        await until("reconnect", () => c.stream.connections() >= 2, STREAM_TIMEOUT);
        await emit(t, id, 2);
        await until("4 items", () => c.ids.length >= 4);
        await sleep(200);
        if (c.ids.join() !== "0,1,2,3") throw new Error(`ids ${c.ids.join()} (gap or duplicate)`);
        return `dropped, reconnected with Last-Event-ID, ids 0..3 once each`;
      } finally {
        c.stream.close();
      }
    },
  },
  {
    name: "sse-reset",
    async run(t) {
      const id = uniqueThread("reset");
      const c = collect(t, id);
      await until("stream open", () => c.opened > 0, STREAM_TIMEOUT);
      await post(t, `/api/fixture/threads/${id}/reset`, {});
      await until("reset event", () => c.reset);
      await sleep(500);
      if (c.stream.connections() !== 1) throw new Error("reconnected after reset");
      return `reset event closes the stream for good`;
    },
  },
  {
    name: "sse-cancel",
    async run(t) {
      await until("no stray streams", async () => (await counts(t)).streams === 0, STREAM_TIMEOUT);
      const c = collect(t, uniqueThread("cancel"));
      await until("stream open on the server", async () => (await counts(t)).streams === 1, STREAM_TIMEOUT);
      const closedAt = Date.now();
      c.stream.close();
      await until("stream closed on the server", async () => (await counts(t)).streams === 0);
      return `server saw the close in ${Date.now() - closedAt} ms`;
    },
  },
  {
    name: "fetch-abort",
    async run(t) {
      const ac = new AbortController();
      const res = await t.request("/api/fixture/slow", { signal: ac.signal });
      const reader = res.body?.getReader();
      if (!reader) throw new Error("no body");
      await reader.read();
      await until("slow request open on the server", async () => (await counts(t)).slow === 1);
      const abortedAt = Date.now();
      ac.abort();
      await reader.read().catch(() => undefined);
      await until("slow request ended on the server", async () => (await counts(t)).slow === 0);
      return `server saw the abort in ${Date.now() - abortedAt} ms`;
    },
  },
  {
    name: "policy-scheme",
    only: "scheme",
    async run(t) {
      const redirect = await json(await t.request("/api/fixture/redirect"), 502);
      if (str(redirect, "code") !== "redirect") throw new Error("redirect not refused");
      const method = await t.request("/api/health", { method: "PROPFIND" });
      if (method.status !== 405) throw new Error(`PROPFIND ${method.status}`);
      const mcp = await t.request("/mcp");
      if (!(mcp.headers.get("content-type") ?? "").startsWith("text/html")) throw new Error("non-/api path reached the server");
      return `redirect 502, PROPFIND 405, /mcp stays in the frontend`;
    },
  },
  {
    name: "policy-fetch",
    only: "fetch",
    async run(t) {
      const out = [
        await rejects(t.request("/api/fixture/redirect")),
        await rejects(t.request("/api/health", { method: "PROPFIND" })),
        await rejects(pluginFetch("http://example.com/api/health")),
        await rejects(pluginFetch("http://stage.invalid/mcp")),
        await rejects(pluginFetch("https://stage.invalid/api/health")),
      ];
      return `refused: ${out.length} (redirect, method, foreign host, non-/api, https)`;
    },
  },
  {
    name: "page-sandbox",
    async run() {
      const net = await rejects(fetch("https://example.com/"));
      const file = await rejects(fetch("file:///etc/hostname"));
      return `native fetch refused: network (${net.slice(0, 40)}), file: (${file.slice(0, 40)})`;
    },
  },
];

export async function runChecks(t: Transport, gallery: HTMLElement, onResult: (r: Result) => void): Promise<Result[]> {
  const ctx: Context = { attachmentId: null, gallery };
  const results: Result[] = [];
  for (const c of checks) {
    if (c.only && c.only !== t.name) continue;
    const start = performance.now();
    let r: Result;
    try {
      const detail = await c.run(t, ctx);
      r = { name: c.name, ok: true, ms: Math.round(performance.now() - start), detail };
    } catch (err) {
      r = { name: c.name, ok: false, ms: Math.round(performance.now() - start), detail: err instanceof Error ? err.message : String(err) };
    }
    results.push(r);
    onResult(r);
  }
  return results;
}
