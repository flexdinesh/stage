import assert from "node:assert/strict";
import { test } from "node:test";
import { fakeApi } from "./fake-transport.mjs";
import { canLinkSession, sessionPath } from "../src/lib/sessions.ts";

const session = { t3ThreadId: "source/id ?#", parentThreadId: null, archived: false };

test("only unarchived top-level sessions can be linked", () => {
  assert.equal(canLinkSession(session), true);
  assert.equal(canLinkSession({ ...session, archived: true }), false);
  assert.equal(canLinkSession({ ...session, parentThreadId: "parent" }), false);
});

test("session routes encode the complete thread ID as one segment", () => {
  assert.equal(sessionPath(session.t3ThreadId), "/sessions/source%2Fid%20%3F%23");
});

test("HTTP contract: active browsing has no date cutoff, paginates, and repeats normalized states", async () => {
  let url;
  const { api } = fakeApi(async (path) => {
    url = new URL(path, "http://stage.test");
    return Response.json({ threads: [], total: 0 });
  });
  await api.threads({ active: true, sort: "activity", state: ["running", "needs_input"], subagents: "exclude", limit: 30, offset: 60 });
  assert.equal(url.pathname, "/threads");
  assert.equal(url.searchParams.get("active"), "1");
  assert.equal(url.searchParams.get("sort"), "activity");
  assert.deepEqual(url.searchParams.getAll("state"), ["running", "needs_input"]);
  assert.equal(url.searchParams.get("offset"), "60");
  assert.equal(url.searchParams.get("limit"), "30");
  assert.equal(url.searchParams.has("from"), false);
  assert.equal(url.searchParams.has("to"), false);
  await api.thread(session.t3ThreadId);
  assert.equal(url.pathname, "/threads/source%2Fid%20%3F%23");
});
