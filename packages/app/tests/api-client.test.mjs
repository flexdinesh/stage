import assert from "node:assert/strict";
import { test } from "node:test";
import { ApiError } from "../src/api/client.ts";
import { fakeApi } from "./fake-transport.mjs";

test("JSON requests send Accept and Content-Type and return the parsed body", async () => {
  let seen;
  const { api } = fakeApi(async (path, init) => {
    seen = { path, init };
    return Response.json({ id: 4, title: "x" });
  });
  assert.deepEqual(await api.createProject({ title: "x", workspaceRoot: "/w" }), { id: 4, title: "x" });
  assert.equal(seen.path, "/projects");
  assert.equal(seen.init.method, "POST");
  assert.equal(seen.init.headers["Accept"], "application/json");
  assert.equal(seen.init.headers["Content-Type"], "application/json");
  assert.deepEqual(JSON.parse(seen.init.body), { title: "x", workspaceRoot: "/w" });
});

test("GET requests carry no body", async () => {
  let seen;
  const { api } = fakeApi(async (path, init) => {
    seen = init;
    return Response.json({ projects: [] });
  });
  await api.projects();
  assert.equal(seen.method, "GET");
  assert.equal(seen.body, undefined);
  assert.equal(seen.headers["Content-Type"], undefined);
});

test("uploads pass FormData through and leave the multipart boundary to the transport", async () => {
  let seen;
  const { api } = fakeApi(async (path, init) => {
    seen = { path, init };
    return Response.json({ id: 9, name: "a.png" });
  });
  const file = new File([new Uint8Array([1, 2, 3])], "a.png", { type: "image/png" });
  await api.uploadImage(file);
  assert.equal(seen.path, "/uploads");
  assert.ok(seen.init.body instanceof FormData);
  assert.equal(seen.init.body.get("file").name, "a.png");
  assert.equal(seen.init.headers["Content-Type"], undefined);
});

test("me reads the acting user from /me", async () => {
  let seen;
  const user = { id: 1, displayName: "Default user", createdAt: "2026-11-22T10:00:00Z" };
  const { api } = fakeApi(async (path, init) => {
    seen = { path, init };
    return Response.json(user);
  });
  assert.deepEqual(await api.me(), user);
  assert.equal(seen.path, "/me");
  assert.equal(seen.init.method, "GET");
});

test("204 endpoints resolve without reading a body", async () => {
  const { api } = fakeApi(async () => new Response(null, { status: 204 }));
  assert.equal(await api.deleteTask(3), undefined);
});

test("errors carry the server's status, message and code", async () => {
  const { api } = fakeApi(async () => Response.json({ error: "task is running", code: "conflict" }, { status: 409, statusText: "Conflict" }));
  await assert.rejects(api.deleteTask(3), (err) => {
    assert.ok(err instanceof ApiError);
    assert.equal(err.status, 409);
    assert.equal(err.message, "task is running");
    assert.equal(err.code, "conflict");
    return true;
  });
});

test("errors without a JSON body fall back to the status line", async () => {
  const { api } = fakeApi(async () => new Response("<html>bad gateway</html>", { status: 502, statusText: "Bad Gateway" }));
  await assert.rejects(api.task(1), (err) => err instanceof ApiError && err.status === 502 && err.message === "502 Bad Gateway" && err.code === null);
});

test("transport failures propagate unchanged", async () => {
  const boom = new TypeError("network down");
  const { api } = fakeApi(async () => {
    throw boom;
  });
  await assert.rejects(api.task(1), (err) => err === boom);
});

test("attachment URLs go through the transport", () => {
  const { api } = fakeApi();
  assert.equal(api.attachmentUrl(12), "asset:/attachments/12");
});

test("launch repair sends structured preview and revision-guarded attach requests", async () => {
 const seen = [];
 const { api } = fakeApi(async (path, init) => {
   seen.push({ path, body: JSON.parse(init.body) });
   return Response.json({});
 });
 const link = "t3code://threads/env/existing";
 await api.previewLaunchLink(4, link);
 await api.attachLaunchLink(4, link, { attempt: 2, revision: 9, environment: "env" });
 assert.deepEqual(seen, [
   { path: "/tasks/4/launch-link/preview", body: { link } },
   { path: "/tasks/4/launch-link", body: { link, attempt: 2, revision: 9, environment: "env" } },
 ]);
});

test("setAutoComplete PUTs the flag to the connection and returns it", async () => {
  let seen;
  const connection = { id: 2, configured: true, harness: "opencode", autoComplete: true };
  const { api } = fakeApi(async (path, init) => {
    seen = { path, init };
    return Response.json({ connection });
  });
  assert.deepEqual(await api.setAutoComplete(2, true), { connection });
  assert.equal(seen.path, "/connections/2/auto-complete");
  assert.equal(seen.init.method, "PUT");
  assert.deepEqual(JSON.parse(seen.init.body), { autoComplete: true });
});

test("machine routes: list, rename, remove, pairing create/status/revoke", async () => {
  const seen = [];
  const { api } = fakeApi(async (path, init) => {
    seen.push({ path, method: init.method, body: init.body === undefined ? undefined : JSON.parse(init.body) });
    if (init.method === "DELETE") return new Response(null, { status: 204 });
    return Response.json({});
  });
  await api.machines();
  await api.renameMachine(2, "desk");
  await api.removeMachine(2);
  await api.createPairing("");
  await api.createPairing("laptop");
  await api.pairingStatus(7);
  await api.revokePairing(7);
  assert.deepEqual(seen, [
    { path: "/machines", method: "GET", body: undefined },
    { path: "/machines/2", method: "PATCH", body: { label: "desk" } },
    { path: "/machines/2", method: "DELETE", body: undefined },
    { path: "/machines/pairings", method: "POST", body: {} },
    { path: "/machines/pairings", method: "POST", body: { label: "laptop" } },
    { path: "/machines/pairings/7", method: "GET", body: undefined },
    { path: "/machines/pairings/7", method: "DELETE", body: undefined },
  ]);
});

test("createPairing surfaces 409 public_url_required with its code", async () => {
  const { api } = fakeApi(async () => Response.json({ error: "Set STAGE_PUBLIC_URL", code: "public_url_required" }, { status: 409 }));
  await assert.rejects(api.createPairing(""), (err) => err instanceof ApiError && err.status === 409 && err.code === "public_url_required");
});

test("harness setup calls: local routes stay, forMachine drives a remote machine's", async () => {
  const seen = [];
  const { api } = fakeApi(async (path, init) => {
    seen.push(`${init.method} ${path}`);
    return Response.json({ details: { state: "unpaired", t3Url: "u", local: { available: false } }, fields: [], actions: [] });
  });
  await api.harnesses();
  await api.harnessStatus("t3");
  const desk = api.forMachine(2);
  await desk.harnesses();
  await desk.harnessStatus("x");
  await desk.harnessStatus("x", { refresh: true });
  await desk.configureHarness("x", { a: 1 });
  await desk.runHarnessAction("x", "connect local");
  await desk.disconnectHarness("x");
  await desk.runHarnessAction("opencode", "connect-local");
  assert.deepEqual(seen, [
    "GET /harnesses",
    "GET /harnesses/t3/status",
    "GET /machines/2/harnesses",
    "GET /machines/2/harnesses/x/status",
    "GET /machines/2/harnesses/x/status?refresh=1",
    "PUT /machines/2/harnesses/x/settings",
    "POST /machines/2/harnesses/x/actions/connect%20local",
    "DELETE /machines/2/harnesses/x/connection",
    "POST /machines/2/harnesses/opencode/actions/connect-local",
  ]);
});

test("a remote machine that cannot answer surfaces 503 with its code", async () => {
  const { api } = fakeApi(async () => Response.json({ error: "This machine is offline", code: "machine_offline" }, { status: 503 }));
  await assert.rejects(api.forMachine(3).harnessStatus("t3"), (e) => e instanceof ApiError && e.status === 503 && e.code === "machine_offline");
});
