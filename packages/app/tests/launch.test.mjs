import assert from "node:assert/strict";
import { test } from "node:test";
import { launchNotice } from "../src/lib/launch.ts";

const NOW = Date.parse("2026-11-24T04:10:00Z");
const launch = (over = {}) => ({ attempt: 1, revision: 4, phase: "unconfirmed", reason: "", retryAt: null, retryCount: 0, session: null, ...over });

test("launchNotice keeps the checking copy before any check failed", () => {
  const n = launchNotice({ launch: launch(), stateSince: "2026-11-24T04:07:00Z" }, "T3", NOW);
  assert.equal(n.title, "Launch unconfirmed");
  assert.equal(n.session, null);
  assert.equal(n.reason, null);
  assert.equal(n.meta, null);
  assert.match(n.body, /checking the existing attempt/);
});

test("launchNotice shows the cause, failed checks and next retry", () => {
  const n = launchNotice({ launch: launch({ reason: "connection reset", retryCount: 3, retryAt: "2026-11-24T04:10:40Z" }), stateSince: "2026-11-24T04:07:00Z" }, "T3", NOW);
  assert.equal(n.title, "Launch unconfirmed");
  assert.equal(n.reason, "connection reset");
  assert.equal(n.meta, "3 failed checks · next retry in 40s · started 3m ago");
});

test("launchNotice leads with the session a launch returned but Stage could not record", () => {
  const session = { connectionId: 4, externalSessionId: "thread-1", title: "delete viewed pages", state: "needs_input" };
  const n = launchNotice({ launch: launch({ reason: "constraint failed", retryCount: 1, retryAt: "2026-11-24T04:09:00Z", session }), stateSince: null }, "T3", NOW);
  assert.equal(n.title, "Launched in T3; Stage can't record it");
  assert.equal(n.session, "delete viewed pages · needs input");
  assert.equal(n.reason, "Recording failed: constraint failed");
  assert.equal(n.meta, "1 failed check · retrying now");
  assert.match(n.body, /until Stage records the session/);
});

test("launchNotice names an untitled session by its id", () => {
  const session = { connectionId: 4, externalSessionId: "thread-1", title: "", state: "running" };
  assert.equal(launchNotice({ launch: launch({ session }), stateSince: null }, "T3", NOW).session, "thread-1 · running");
});
