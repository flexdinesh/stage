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
