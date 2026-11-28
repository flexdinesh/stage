import assert from "node:assert/strict";
import { test } from "node:test";
import { queueView, taskQueueKey } from "../src/lib/sessionQueue.ts";

test("queued previews preserve native order and show known metadata", () => {
  const later = "2026-11-29T02:00:00Z";
  const earlier = "2026-11-29T01:00:00Z";
  const messages = [
    { id: "second", text: " Review\n\n the patch ", delivery: "queue", createdAt: later, source: "stage" },
    { id: "first", text: "Run checks", delivery: "steer", createdAt: earlier, source: "harness" },
    { id: "unknown", text: "Follow up", delivery: "queue", createdAt: "0001-01-01T00:00:00Z", source: "" },
    { id: "blank", text: "  ", delivery: "queue", createdAt: "", source: "" },
  ];
  assert.deepEqual(queueView(messages), [
    { id: "second", preview: "Review the patch", canSteer: true, at: later, source: "Stage" },
    { id: "first", preview: "Run checks", canSteer: false, at: earlier, source: "Harness" },
    { id: "unknown", preview: "Follow up", canSteer: true, at: null, source: null },
    { id: "blank", preview: "Message preview unavailable", canSteer: true, at: null, source: null },
  ]);
  assert.deepEqual(queueView([]), []);
});

test("queue state distinguishes tasks, attempts and native sessions", () => {
  const session = { launchAttempt: 2, externalSessionId: "first" };
  const key = taskQueueKey(7, session);
  assert.deepEqual(taskQueueKey(7, { ...session, state: "needs_input" }), key);
  assert.notDeepEqual(taskQueueKey(8, session), key);
  assert.notDeepEqual(taskQueueKey(7, { ...session, launchAttempt: 3 }), key);
  assert.notDeepEqual(taskQueueKey(7, { ...session, externalSessionId: "second" }), key);
});
