import assert from "node:assert/strict";
import { test } from "node:test";
import { api } from "../src/api/client.ts";
import { canEditContinuation, canLinkSession, continuationPreview, isPendingContinuation, sessionPath } from "../src/lib/sessions.ts";

const session = {
  t3ThreadId: "source/id ?#", title: "Prepare the migration", parentThreadId: null,
  archived: false, t3WebUrl: "https://t3.example/thread/source", t3AppUrl: null, t3DeepLink: null,
};
const unsent = { state: "waiting", sentAt: null, runId: null, messageId: null };

test("only unarchived top-level sessions can join an order", () => {
  assert.equal(canLinkSession(session), true);
  assert.equal(canLinkSession({ ...session, archived: true }), false);
  assert.equal(canLinkSession({ ...session, parentThreadId: "parent" }), false);
});

test("edits and actions stop when delivery is claimed, confirmed, or final", () => {
  for (const state of ["waiting", "paused", "blocked"]) assert.equal(canEditContinuation({ ...unsent, state }), true);
  for (const state of ["sending", "running", "awaiting_settle", "settled", "cancelled", "uncertain"]) {
    assert.equal(canEditContinuation({ ...unsent, state }), false);
  }
  for (const confirmed of [{ sentAt: "2026-10-11T00:00:00Z" }, { runId: "run" }, { messageId: "message" }]) {
    assert.equal(canEditContinuation({ ...unsent, state: "blocked", ...confirmed }), false);
  }
});

test("pending panel retains blocked, paused, uncertain, and delivered but unsettled entries", () => {
  for (const state of ["waiting", "paused", "blocked", "sending", "running", "awaiting_settle", "uncertain"]) {
    assert.equal(isPendingContinuation({ state }), true);
  }
  for (const state of ["settled", "cancelled"]) assert.equal(isPendingContinuation({ state }), false);
});

test("preview preserves saved prompt whitespace and includes source ID even without a T3 link", () => {
  const prompt = "  Keep this verbatim.\n\n ";
  assert.equal(continuationPreview(prompt, session), `${prompt}\n\n---\nPrerequisite session\nTitle: ${session.title}\nThread ID: ${session.t3ThreadId}\nT3: ${session.t3WebUrl}`);
  const noLink = continuationPreview(prompt, { ...session, t3WebUrl: null });
  assert.ok(noLink.startsWith(`${prompt}\n\n---\n`));
  assert.ok(noLink.endsWith(`Thread ID: ${session.t3ThreadId}`));
});

test("session routes encode the complete thread ID as one segment", () => {
  assert.equal(sessionPath(session.t3ThreadId), "/sessions/source%2Fid%20%3F%23");
});

test("HTTP contract: active browsing has no date cutoff, paginates, and repeats normalized states", async (context) => {
  let url;
  context.mock.method(globalThis, "fetch", async (path) => {
    url = new URL(path, "http://stage.test");
    return Response.json({ threads: [], total: 0 });
  });
  await api.threads({ active: true, sort: "activity", state: ["running", "needs_input"], subagents: "exclude", limit: 30, offset: 60 });
  assert.equal(url.pathname, "/api/threads");
  assert.equal(url.searchParams.get("active"), "1");
  assert.equal(url.searchParams.get("sort"), "activity");
  assert.deepEqual(url.searchParams.getAll("state"), ["running", "needs_input"]);
  assert.equal(url.searchParams.get("offset"), "60");
  assert.equal(url.searchParams.get("limit"), "30");
  assert.equal(url.searchParams.has("from"), false);
  assert.equal(url.searchParams.has("to"), false);
  await api.thread(session.t3ThreadId);
  assert.equal(url.pathname, "/api/threads/source%2Fid%20%3F%23");
});

test("saving and editing a continuation uses order endpoints and keeps the prompt verbatim", async (context) => {
  const calls = [];
  context.mock.method(globalThis, "fetch", async (path, options) => {
    calls.push({ path, method: options.method, body: options.body === undefined ? undefined : JSON.parse(options.body) });
    return Response.json({ id: 8 });
  });
  const prompt = "  Resume here.\n ";
  const body = { afterThreadId: "A", targetThreadId: "B", prompt };
  await api.createContinuation(body);
  await api.updateContinuation(8, prompt);
  for (const action of ["pause", "resume", "cancel"]) await api.continuationAction(8, action);
  assert.deepEqual(calls, [
    { path: "/api/continuations", method: "POST", body },
    { path: "/api/continuations/8", method: "PATCH", body: { prompt } },
    ...["pause", "resume", "cancel"].map((action) => ({ path: `/api/continuations/8/${action}`, method: "POST", body: undefined })),
  ]);
});
